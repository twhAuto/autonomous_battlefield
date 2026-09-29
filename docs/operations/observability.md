# Observability and reliability objectives

Autonomous Battlefield exposes Prometheus metrics from the API and engine and
ships local metrics, tracing, and logging stacks for the `kind` deployment. Prometheus
and Grafana provide metrics; Fluent Bit, Elasticsearch, and Kibana provide
centralized logs. The dashboard and objectives in this guide are internal
operating targets. They are not a customer-facing service-level agreement.

HTTP completion logs include `actor_id` and `actor_role` established by trusted
authentication middleware, alongside method, resource path, status and request ID.
An authentication observer carries identity to the outer request logger; rejected
credentials leave identity empty. Tokens, cookies and email headers are not logged.
These operational logs provide request attribution, not a tamper-proof audit store.

## Signal path

```text
API pods :8080/metrics --------+
Engine pods :8081/metrics -----+
kube-state-metrics :8080 ------+--> Prometheus --> Grafana dashboards
kubelet /metrics/cadvisor -----+          |
                                          +--> recording and alert rules

application container stdout --> Fluent Bit --> Elasticsearch --> Kibana
API / engine / MCP / agent spans --> OTel Collector --> Tempo --> Grafana
```

Prometheus discovers annotated pods in the `battlefield` namespace and uses
Kubernetes node discovery for one cAdvisor target per kubelet. Its namespaced
Role can list and watch application pods. A separate ClusterRole permits only
node discovery and `GET` access to the kubelet metrics subresource. Prometheus
uses its mounted service-account token to scrape each local kubelet directly;
it does not receive the broader `nodes/proxy` permission. Grafana receives no
Kubernetes credentials. It uses internal Prometheus and Tempo Services as
its metrics and trace data sources.

The local stack deliberately does not include Alertmanager. Prometheus evaluates
the checked-in alert rules and shows their state, but notifications require an
Alertmanager or another production alert-delivery integration.

Fluent Bit runs once per node and tails API, engine, agent, MCP, frontend, and migration
container logs from the `battlefield` namespace. It enriches each record with
pod, container, and label metadata and sends daily
`battlefield-app-YYYY.MM.DD` indices to Elasticsearch. JSON application logs
are merged into searchable fields; plain-text frontend logs retain their `log`
field. Keeping infrastructure logs out of the application index also prevents
field-mapping collisions with their different schemas. The collector runs in
the dedicated `battlefield-logging` namespace so its required read-only node
log mounts do not weaken the application namespace's Pod Security policy. Its
Role can read pods in the `battlefield` namespace only, and its own logs are
outside the configured input path.

## Distributed traces

The API, engine, MCP, and agent use OpenTelemetry. W3C trace context is stored on
battle and decision rows so workers can continue it after queue claims. The
collector accepts OTLP and exports traces to Tempo. In Grafana Explore, select
**Tempo** and search for the relevant service or trace ID. Host agents need an
OTLP endpoint reachable from the host to export their own spans.

See [`internal/telemetry/tracing.go`](../../backend/internal/telemetry/tracing.go)
for environment configuration and instrumentation, and the
[collector configuration](../../deploy/overlays/local/observability/otel-collector/config.yaml)
for the in-cluster path. Prompts, credentials, capabilities, full observations,
and raw provider responses are excluded from span attributes. Tempo storage is
also disposable; it is not an audit archive.

## Application metrics

The API and engine expose `/metrics` on their existing operational ports. The
endpoint remains available during startup and graceful shutdown so a failed
dependency does not hide process diagnostics.

HTTP request series appear only after a replica handles application traffic;
lifecycle probes and metrics scrapes bypass request instrumentation. The smoke
test probes `/v1/auth/session` through the same port-forward used for the metrics
scrape, accepting anonymous session responses of 200 or 401, then waits for the
request counter. Gateway traffic may reach another replica.

| Metric | Type | Meaning |
| --- | --- | --- |
| `battlefield_http_server_requests_total` | Counter | API requests by method, normalized route pattern, and status code. |
| `battlefield_http_server_request_duration_seconds` | Histogram | API request latency by method and normalized route pattern. |
| `battlefield_engine_battle_claims_total` | Counter | Initial and reclaimed battle claims. |
| `battlefield_engine_battles_processed_total` | Counter | Processing attempts by `completed`, `failed`, `error`, `lease_lost`, or `requeued` outcome. |
| `battlefield_engine_battle_processing_duration_seconds` | Histogram | Wall-clock processing duration by outcome. |
| `battlefield_engine_ticks_total` | Counter | Successfully advanced simulation ticks. |
| `battlefield_engine_worker_ready` | Gauge | `1` while the worker can claim work, otherwise `0`. |

The Prometheus Go client also publishes standard `go_*`, `process_*`, and
`promhttp_*` metrics. Route labels use Go HTTP patterns such as
`GET /v1/battles/{id}` rather than raw paths. Battle IDs, faction IDs, request
IDs, pod names, and worker IDs are intentionally absent from application metric
labels to avoid unbounded time-series cardinality. Pod identity is added only
as the `scrape_pod` target label by Prometheus. That name deliberately avoids
colliding with kube-state-metrics' `pod` label, which identifies the workload
being described rather than the exporter pod.

## Kubernetes workload metrics

The Kubernetes dashboard combines two complementary metric sources:

| Source | Answers | Example metrics |
| --- | --- | --- |
| `kube-state-metrics` | What Kubernetes says should exist and whether workload objects are healthy | `kube_pod_status_phase`, `kube_pod_container_status_ready`, `kube_pod_container_status_restarts_total`, `kube_deployment_status_replicas_available` |
| kubelet cAdvisor endpoint | What CPU, memory, and network resources containers are actually consuming | `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`, `container_network_receive_bytes_total`, `container_network_transmit_bytes_total` |

The local kube-state-metrics deployment watches only the `battlefield`
namespace and only the workload resource types listed in its manifest. It does
not read Secrets or cluster-wide application objects. Prometheus scrapes
cAdvisor directly from each local kubelet over HTTPS with service-account
authentication, so no privileged cAdvisor DaemonSet or host mounts are
required. The Kind-only scrape skips serving-certificate verification because
the kubelet certificate is node-local rather than signed by the API CA mounted
in the Prometheus pod; this exception must not be carried into an external
cluster without a trusted kubelet CA.

Open **Autonomous Battlefield / Kubernetes Workloads** in Grafana to see pod
phases, readiness, restarts, deployment availability, and per-pod CPU, memory,
and network usage. This is operational telemetry rather than a service SLI:
pod health can explain an SLO failure, but it does not directly measure whether
a user request succeeded.

## Proposed SLIs and SLOs

These targets are a starting policy for an early-stage service. Review them
after enough representative traffic has accumulated.

| Service | SLI | Proposed SLO | Eligible events and exclusions |
| --- | --- | --- | --- |
| API availability | Non-5xx API responses divided by all API responses | 99.9% over a rolling 30 days | Includes matched and unmatched gameplay requests. Excludes lifecycle probes and `/metrics`, which bypass API instrumentation. A 4xx is a successful service response. |
| API latency | API responses completed in at most 500 ms divided by all API responses | 95% over a rolling 30 days | Measures server handling time. It does not include browser, ingress, DNS, or client-network latency. |
| Battle processing success | `completed` outcomes divided by `completed + failed + error + lease_lost` outcomes | 99% over a rolling 30 days | Graceful-shutdown `requeued` outcomes are excluded because ownership was safely handed back. Claim-loop process failures are covered by target/readiness alerts. |

The availability objective has a request-based 0.1% error budget. It should not
be translated directly into “minutes of downtime,” because requests are not
uniformly distributed over time.

An SLA should be added only when there is an external commitment. It needs an
explicit service boundary, measurement source, calendar window, maintenance
and dependency exclusions, reporting rules, and remedies. The internal SLOs
above should be tested and adjusted before any such contract is made.

## Alerts

Prometheus loads `alerts.yml` from the local observability overlay. It includes:

- fast availability-budget burn over 5-minute and 1-hour windows at 14.4 times
  the sustainable burn rate;
- persistent availability-budget burn over 30-minute and 6-hour windows at six
  times the sustainable burn rate;
- an application metrics target down for five minutes; and
- any failed, errored, or lease-lost battle processing outcome within 15
  minutes.

The multi-window availability alerts reduce noise from a brief bad sample while
still detecting both acute and sustained failures. Their thresholds are based
on the proposed 99.9% objective (`1 - 0.999 = 0.001`).

The availability recording rule treats a missing 5xx series as zero
(`or vector(0)`). Without it, a healthy API that has never returned a 5xx has no
error series, the ratio is an empty vector, and the SLI shows no data.
`scripts/test/test-prometheus-rules.sh` runs `promtool` unit tests for both the
healthy and the failing case.

## Load and failure evidence

Load tests and failure drills run on demand against the dev Kind cluster, not on
a schedule; production is not kept running. Record each run's date, revision,
and results here so the SLO targets above stay tied to evidence.

### How to run

```bash
# Representative load: 20 viewers for 10 minutes against two scripted battles.
# Set the load explicitly; run-k6.sh otherwise applies its 3-viewer, 30 s smoke defaults.
ADMIN_USERNAME=... ADMIN_PASSWORD=... DEPLOY_ENV=dev K6_VUS=20 K6_DURATION=10m \
  ./scripts/perf/run-k6.sh tests/performance/k6-representative.js

# Saturation: K6_STAGES replaces the fixed viewer count and duration.
ADMIN_USERNAME=... ADMIN_PASSWORD=... DEPLOY_ENV=dev \
  K6_STAGES=30s:100,60s:100,30s:300,60s:300,30s:600,60s:600,20s:0 \
  ./scripts/perf/run-k6.sh tests/performance/k6-representative.js

# Failure drills: requires BF_DISPOSABLE=1 in the dev profile; restore it afterwards.
DEPLOY_ENV=dev RUN_DESTRUCTIVE_KIND_DRILLS=I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS \
  ./scripts/ops/run-kind-failure-drills.sh
```

For an AI outage, set `runtime_policy.ai_enabled` to `false` in the dev
database, run a battle with one model controller, and restore it to `true`.
New model decisions are stored as `failed` with `ai_disabled_or_budget` and are
never claimed, so no provider is called even for an API profile.

`tests/performance/k6-representative.js` queues the admitted number of
scripted battles (two per person) and replays the battle page: each viewer
polls status, the next event page, and decision history once a second, and
browses history once the battle settles. It records two battle metrics from
server timestamps: `battle_queue_wait_seconds` (queued to started) and
`battle_pace_ratio` (run time divided by ticks; 1.0 is one tick per second).
`battles_completed` must be 100%, so one failed or unfinished battle fails the
run even when thousands of polling checks pass.
Run the drills while the representative load runs, so the drill shows its
effect on viewers and on a live battle.

The drill script deletes each workload's pods and fails unless replacement pods
with new UIDs become Ready, so a selector that matches nothing cannot report a
passed drill. Its `artifacts/failure-drills/summary.json` records the recovery
seconds for each drill.

### 2026-09-29 results (dev Kind, `8cc7c07`)

Single-node Kind on one workstation; k6 ran on the same host, so saturation
figures are a lower bound.

| Run | Load | Result |
| --- | --- | --- |
| Representative | 20 viewers, 10 min, about 57 req/s | 34,138 requests, 0 failed. k6 p95 7.9 ms; server p95 9.3 ms, p99 10 ms. API pods about 0.09 cores each, PostgreSQL 0.12. |
| Saturation, 100 viewers | 204 req/s | Server p95 5 ms, 100% under 500 ms. |
| Saturation, 300 viewers | 408 req/s | Server p95 787 ms, 88.5% under 500 ms. PostgreSQL CPU-throttled at its 500m limit. |
| Saturation, 600 viewers | 516 req/s | Server p95 975 ms, 0.3% under 500 ms. 0 failed requests out of 108,441. |
| Engine pod restart | Live battle | Replacement Ready in 3 s. The battle was requeued and resumed as a new attempt. |
| Engine process crash (SIGKILL) | Live battle, second battle queued | Process restarted in 1 s, but the orphaned battle was reclaimed after 91.5 s: the worker first claimed the queued battle and ran it to completion. |
| PostgreSQL restart | Live battle and 6 viewers | Replacement Ready in 1 s. 9 of 6,627 viewer requests failed (0.13%). The engine process exited on the lost connection and restarted; the battle, already on its third attempt after the two engine drills, failed with `attempts_exhausted`. |
| MCP outage | 30 s at zero replicas | Recovered in 1 s. Scripted battles do not use MCP. |
| AI outage (`runtime_policy.ai_enabled=false`) | Model-vs-scripted battle, 60 ticks | Completed in 60 s with no agent executions. All 7 model windows fell back: scripted baseline for the opening and first window, then the previous plan. |

### Capacity findings

- **One battle at a time per engine.** The worker runs a claimed battle to
  completion before claiming another, and the engine runs one replica. Admitted
  battles queue: in the runs above the second battle waited 45 to 125 s. With
  admission at four unfinished battles globally and 800-tick battles, the
  fourth battle can wait about 40 minutes, inside the one-hour expiry. Adding
  engine concurrency needs its own load and failover test first.
- **Crash recovery waits for the current battle.** An orphaned battle is
  reclaimable only after its 30 s lease expires, and only when the worker next
  claims. A queued battle claimed first delays it by a full battle.
- **Reclaimed battles replay from tick 0.** Each reclaim restarts the
  simulation, so a disrupted battle took 1.38 times its tick count to finish.
- **Graceful handoffs consume attempts.** Every claim increments the attempt,
  including a requeue after a clean shutdown, and `maxAttempts` is 3. Three
  engine restarts during one battle (for example, rolling deploys) fail it with
  `attempts_exhausted`, as the drills above did.
- **A PostgreSQL restart restarts the engine.** The claim loop treats a lost
  connection as fatal, so the engine exits and Kubernetes restarts it; the
  running battle is then reclaimed as a new attempt.
- **PostgreSQL CPU is the API bottleneck.** API pods stayed under 0.3 cores
  while PostgreSQL reached its 500m limit at about 400 req/s. Raise its limit
  before adding API replicas.
- **The API HPA cannot scale in Kind.** The cluster has no metrics-server, so
  the HPA reports `cpu: <unknown>` and stays at two replicas.

### Calibrated targets

The measured API latency (p99 10 ms at 57 req/s, 100% under 500 ms up to about
200 req/s) leaves the proposed 95%-under-500-ms objective with wide headroom.
The objectives stay as proposed until real traffic accumulates, but the
following capacity assumptions now come from measurement: about 100 concurrent
viewers (200 req/s) within the latency objective on one Kind node, one running
battle per engine replica, and battle pace within 1.1 of its tick count when
undisturbed (measured 1.001 to 1.018, including under 600 viewers).

## Run the local stack

After [dev environment registration and secret preparation](environments.md#wsl-setup-and-normal-use),
build and deploy the complete stack with:

```bash
DEPLOY_COMPONENTS=all SMOKE_TEST_OBSERVABILITY=1 ./deploy-local.sh
```

Visit `http://grafana.localhost:8888` and open **Autonomous Battlefield /
Autonomous Battlefield Overview** for application SLIs or **Autonomous
Battlefield / Kubernetes Workloads** for pod state and resource usage. Visit
`http://kibana.localhost:8888`, open **Discover**, and select **Autonomous
Battlefield Logs** for logs. The deployment job creates that data view before
the observability stage is considered complete.

For model-controller troubleshooting, open the provisioned **Battlefield AI
Decisions** saved search in Discover. It filters to decision lifecycle events
and defaults to `@timestamp` and readable `msg` columns. Expand a row to see
the correlation fields needed to follow one decision across the
engine, MCP service, and in-cluster agent: `battle_id`, `request_id`,
`faction_id`, `target_tick`, `profile_id`, `decision_status`,
`error_category`, `fallback_source`, and `duration_ms`. The collector includes
the API, engine, agent, MCP, frontend, and migration workloads. A subscription
CLI agent runs on the host, so its process-local messages remain in the bridge
terminal; its MCP claims, tool calls, submissions, failures, and final engine
resolution are recorded by Kubernetes services and therefore appear in
Kibana.

Application logs separate the stable machine-readable `event` identifier from
`msg`, a readable summary that includes service and relevant operational
context. Keep Discover on `@timestamp` and `msg` for a compact timeline; fields
such as `battle_id`, `request_id`, `error`, and timing values remain structured
for filtering and expanded inspection. For example:

```text
[engine] Battle simulation progressing; battle: castle-42; worker: engine-1; attempt: 1; requested ticks: 600; tick: 120; elapsed (ms): 94000
[engine] Waiting for faction decisions; battle: castle-42; attempt: 1; target tick: 130; pending factions: 2; elapsed (ms): 0; remaining (ms): 119999
```

The engine emits the first progress update after a successful running tick,
then at most once every 15 seconds as ticks finish. Updates contain the current
and requested ticks plus elapsed wall time. A pending decision window emits an
immediate waiting update and repeats at most every 15 seconds with the pending
faction count and remaining timeout. Resolved windows produce no waiting
message. Pauses and resumes retain their own lifecycle events; elapsed battle
time includes pauses and decision waits. Completion includes ticks run, elapsed
time, winner (when present), and reason. Full combat events remain in
`battle_events`.

`LOG_LEVEL` controls backend process logging (`DEBUG`, `INFO`, `WARN`, `ERROR`;
case-insensitive). It defaults to INFO, including for invalid values. DEBUG adds
`battle_tick_completed` after each successful running-tick save and enables
health-request logs. Set it only while diagnosing detailed execution, then
restore INFO. Environment changes take effect when the process restarts.

Logs emitted with an active OpenTelemetry span include root-level `trace_id`
and `span_id`. API access logs share the inbound request span, including
rejected requests; engine and agent logs use their existing consumer spans.
Startup logs without a span omit these fields. Copy `trace_id` into Grafana
Explore's Tempo trace-ID search to inspect a retained trace. Sampling can mean
a logged trace ID has no stored trace. HTTP logs include `duration_ms` while
retaining the older `duration` field for compatibility.

Older indexed records retain event identifiers in `msg`; the provisioned AI
search matches both formats. New automation should filter on `event`. These
changes affect newly emitted logs after rebuilding and deploying the backend;
rerun the observability deployment to update the provisioned saved search.
Your other saved Discover views are unaffected.

Useful KQL filters include:

```text
event: decision_* OR msg: decision_*
decision_status: fallback
error_category: timeout
battle_id: "<battle UUID>"
request_id: "<decision UUID>"
```

`decision_window_started` records the configured timeout,
`decision_context_served` confirms observation retrieval,
`decision_plan_submitted` records validation and acceptance, and
`decision_resolved` is the authoritative final outcome selected by the engine.
Operational logs intentionally exclude capability tokens, credentials, full
observations, prompts, and CLI output.

Local Grafana allows anonymous Viewer access and disables basic login. The
local Elasticsearch and Kibana deployment also disables authentication. Envoy
Gateway exposes the web interfaces only through a Kind node port bound to
`127.0.0.1`, so they are reachable from the local machine but not the network.

For the direct `kubectl` commands below, first select the registered dev
kubeconfig in this shell:

```bash
export KUBECONFIG="$HOME/.config/autonomous-battlefield/dev/kubeconfig"
kubectl config current-context  # must be battlefield-dev
```

As a troubleshooting fallback, bypass the gateway with a temporary forward:

```bash
kubectl port-forward --namespace battlefield \
  service/battlefield-grafana 3000:3000
```

Inspect Prometheus targets, rules, and queries:

```bash
kubectl port-forward --namespace battlefield \
  service/battlefield-prometheus 9090:9090
```

Open `http://localhost:9090/targets` or `http://localhost:9090/rules`. Useful
starter queries are:

```promql
battlefield:api_availability:ratio_rate5m

histogram_quantile(
  0.95,
  sum by (le, route) (
    rate(battlefield_http_server_request_duration_seconds_bucket{component="api"}[5m])
  )
)

sum by (outcome) (
  increase(battlefield_engine_battles_processed_total{component="engine"}[1h])
)

sum by (phase) (
  kube_pod_status_phase{namespace="battlefield"} == 1
)

sum by (pod) (
  rate(container_cpu_usage_seconds_total{
    namespace="battlefield", container!="", container!="POD"
  }[5m])
)

sum by (pod) (
  container_memory_working_set_bytes{
    namespace="battlefield", container!="", container!="POD"
  }
)
```

Useful Kibana Query Language filters in Discover include:

```text
service: api
service: engine and level: ERROR
kubernetes.container_name: frontend
request_id: "<request-id>"
```

To inspect Elasticsearch directly:

```bash
kubectl port-forward --namespace battlefield \
  service/battlefield-elasticsearch 9200:9200
curl --fail 'http://localhost:9200/_cat/indices/battlefield-app-*?v'
```

For a direct endpoint check:

```bash
kubectl port-forward --namespace battlefield \
  service/battlefield-api 8080:8080
curl --fail http://localhost:8080/metrics
```

## Local versus production

The local Prometheus TSDB and Grafana database use `emptyDir`; their data is
lost when their pods are replaced. Prometheus retains up to 30 days only while
its pod remains intact. Elasticsearch log indices and Kibana saved objects use
the 10Gi `battlefield-elasticsearch-data` PVC (`standard` storage class), retaining
data across pod replacement. This is not a backup against cluster or disk loss.
The first transition from `emptyDir` starts empty; preserve required data before
rolling out that storage change. The setup Job recreates checked-in saved objects,
but does not restore logs.

The deployment helper waits for Elasticsearch, restarts and waits for Kibana,
then applies `kibana/setup-job.yaml` separately from the observability overlay.
Setup requests and the Job lifetime are bounded. Each retry gets a separate pod,
and terminal failure immediately triggers diagnostics. Finished Jobs and their
pods expire after 300 seconds, so capture failure output promptly.

Elasticsearch keeps its container root filesystem read-only. A non-root init
container copies the image's complete default configuration and the checked-in
`elasticsearch.yml` into a dedicated writable `emptyDir`, because Elasticsearch
must create and upgrade `elasticsearch.keystore` in that directory at startup.
The writable mount contains configuration state only and remains disposable.

For AWS, keep the application metric contract but replace the local stack with
a durable managed or self-hosted metrics and logging backend. Add ingress or
load-balancer metrics so the availability SLI covers failures that never reach
the API, retain Kubernetes workload and container metrics with an EKS-compatible
collector, use durable retention and lifecycle policies, encrypt log transport
and storage, configure authenticated least-privilege Grafana and Kibana access,
and route alerts to an owned notification channel. The unauthenticated local
Elastic configuration must not be reused in a shared or production cluster.

## Health endpoint implementation

Process entry points wire the shared `httpapi.LifecycleHandler`, which owns all
health probes. Application routing does not register a second set of probes.
Engine readiness requires both a running worker and a successful database ping;
worker shutdown or database failure makes readiness fail while liveness remains
available. See the [codebase guide](../design/backend-guide.md#process-health-ownership)
for source ownership and lifecycle tests.

## AI decision traces in Langfuse

Open `http://langfuse.localhost:8888` through the shared gateway after installation.
The installed project is detected automatically by observability deployments and
host CLI launches; neither a UI port-forward nor a per-shell tracing flag is needed.

The optional self-hosted [Langfuse deployment](langfuse.md) adds decision, model,
and tool observations to the existing collector. AI spans are sampled at 100%;
infrastructure retains its configured sampling ratio and Tempo pipeline. Codex
JSONL and Antigravity envelopes provide explicitly scoped CLI usage totals.
The decision inspector shows known cache/reasoning counts and a trace link for
new recorded decisions. Raw prompts, tool payloads and reasoning are excluded.
See the linked guide for installation, host CLI forwarding and measurement limits.

## Production kubelet TLS

Production Prometheus no longer reuses the development-only
`insecure_skip_verify: true` cAdvisor setting. Its staged overlay requires
`BF_KUBELET_CA_FILE` in the private production `deployment.env`; relative paths
resolve beside that file. The bundle is mounted at `/etc/prometheus/kubelet-ca.crt`
through the generated Prometheus ConfigMap. Normal CA and node-IP SAN verification
remain enabled; missing or invalid trust stops staging before workload changes.
Prometheus keeps only `nodes/metrics` permission, never broad `nodes/proxy` access.

For this registered Kind server, collect the current public serving certificates
through the trusted Docker daemon, after environment and cluster UID validation:

```bash
./scripts/ops/export-kind-kubelet-ca.sh
```

The helper reads only `/var/lib/kubelet/pki/kubelet.crt` from the registered Kind
nodes, rejects private keys and certificates expiring within seven days, and writes
`kubelet-ca.crt` beside production `deployment.env`. Add:

```dotenv
BF_KUBELET_CA_FILE=kubelet-ca.crt
```

Review the exported certificates and ensure they cover the node IPs used by
Prometheus discovery. For a cluster with CA-issued kubelet certificates, provision
that approved CA bundle instead. Do not copy a certificate from an unauthenticated
network handshake or use the public system root bundle as a production default.
After adding nodes or rotating serving certificates, refresh the approved bundle
and redeploy observability. A trust failure is a deployment failure, not a reason
to disable certificate verification.

API scrape verification now checks every nonterminating API pod against its
actual IP and current readiness, the deployment generation and desired replica
count, and a successful scrape within 60 seconds. Missing discovery, a failed
replica, stale results, or incomplete HPA rollout cannot pass on another pod's
healthy metric. The bounded 120-second wait still accommodates discovery after
rollout and returns immediately when every expected target is healthy.
