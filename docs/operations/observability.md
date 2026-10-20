# Observability operations

Use this guide to deploy and inspect metrics, traces and logs, and configure
production monitoring trust. The [observability architecture](../architecture/observability.md)
owns signal paths, metric and log contracts, trace relationships and proposed SLOs.
For repeatable load tests and failure drills, use [reliability testing](reliability-testing.md).

## Run the local stack

After [dev environment registration and secret preparation](../developer/local-setup.md#wsl-setup-and-normal-use),
build and deploy the complete stack with:

```bash
DEPLOY_COMPONENTS=all SMOKE_TEST_OBSERVABILITY=1 ./scripts/cd/deploy-local.sh
```

Visit `http://grafana.localhost:8888` and open **Autonomous Battlefield /
Autonomous Battlefield Overview** for application SLIs, **Autonomous
Battlefield / Kubernetes Workloads** for pod state and resource usage, or
**Autonomous Battlefield / Battle Traces** to follow one battle's traces. The
anonymous local user is a Viewer, which Grafana does not allow into Explore; the
Battle Traces dashboard lists a battle's engine and decision traces and draws the
selected trace, so no Editor access is needed. Visit
`http://kibana.localhost:8888`, open **Discover**, and select **Autonomous
Battlefield Logs** for logs. The deployment job creates that data view before
the observability stage is considered complete.

For model-controller troubleshooting, open the provisioned **Battlefield AI
Decisions** saved search in Discover. It filters to decision lifecycle events
and defaults to `@timestamp` and readable `msg` columns. Expand a row to see
the correlation fields needed to follow one decision across the
engine, AI gateway, and in-cluster agent: `battle_id`, `request_id`,
`faction_id`, `target_tick`, `profile_id`, `decision_status`,
`error_category`, `fallback_source`, and `duration_ms`. The collector includes
the API, engine, agent, AI gateway, frontend, and migration workloads. A
subscription CLI worker runs outside Kubernetes (the development bridge terminal,
or the CLI VM's `battlefield-worker` journal in production), so its process-local
messages stay there; its claims, MCP tool calls, submissions, failures and final
engine resolution are recorded by Kubernetes services and appear in Kibana.

For a compact timeline, select `@timestamp` and `msg`; expand rows for structured
correlation fields. Use DEBUG only while diagnosing tick or health-request details,
then restore INFO and restart the process. Paste a log's `trace_id` into the
**Trace ID** box of the Battle Traces dashboard; sampling can leave a logged trace
without stored span data. See [structured logs](../architecture/observability.md#structured-logs)
for event semantics and excluded sensitive fields.

Useful KQL filters include:

```text
event: decision_*
decision_status: fallback
error_category: timeout
battle_id: "<battle UUID>"
request_id: "<decision UUID>"
```

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

## Storage and access by environment

Development Kind and production K3s run the same stack; the
[architecture environments table](../architecture/observability.md#environments)
summarizes the differences and the accepted access risk.

- **Metrics, dashboards and traces:** the Prometheus TSDB, Grafana database and
  Tempo storage use `emptyDir` in both environments, so their data is lost when a
  pod is replaced. Prometheus keeps up to 30 days only while its pod remains intact.
- **Logs and Kibana objects:** Elasticsearch indices and Kibana saved objects use
  the `battlefield-elasticsearch-data` PVC: 10 Gi on Kind's `standard` class, and
  20 Gi on `battlefield-retained` in production, which survives pod and guest-OS
  replacement. Neither is a backup against cluster or disk loss. The setup Job
  recreates checked-in saved objects but does not restore logs.
- **Access:** on Kind, use `grafana.localhost` and `kibana.localhost` through the
  loopback gateway. In production, use `grafana.home.arpa` and `kibana.home.arpa`
  from the host network; the [local HTTPS workflow](local-https-platform.md) owns
  those routes. Neither is published through the Cloudflare tunnel.

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
and route alerts to an owned notification channel. The unauthenticated Elastic
and anonymous Grafana configuration is accepted only for the private,
single-operator K3s cluster; do not reuse it on a shared cluster or one reachable
from untrusted networks.

## Find a battle trace

Open **Autonomous Battlefield / Battle Traces** and enter a **Battle ID** to list
its engine and decision traces, or a **Trace ID** to draw one trace. The anonymous
Viewer cannot open Grafana Explore and the login form is disabled, so the TraceQL
queries below apply only where Grafana has been configured with Editor access; the
dashboard panels cover the same searches. Links point from the later trace to the earlier one:
the engine trace links to the creating request, and each decision links to its
decision window. To follow a battle end to end in TraceQL:

- `{ span."battlefield.battle_id" = "<battle ID>" }` finds the engine trace; its
  root's link opens the creating request.
- `{ span."langfuse.trace.metadata.battle_id" = "<battle ID>" }` lists the
  decision traces; each root's link opens its decision window in the engine trace.

Tempo's search results can omit leading zeros from trace IDs; pad them to 32 hex
characters before comparing them with link targets.

## Verify application metrics

HTTP request series appear only after a replica handles application traffic;
lifecycle probes and metrics scrapes bypass request instrumentation. The smoke
test probes `/v1/auth/session` through the same port-forward used for the metrics
scrape, accepting anonymous session responses of 200 or 401, then waits for the
request counter. Gateway traffic may reach another replica.

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

Production Prometheus does not use the development-only
`insecure_skip_verify: true` cAdvisor setting. Its staged overlay requires
`BF_KUBELET_CA_FILE` in the private production `deployment.env`; relative paths
resolve beside that file. The bundle is mounted at `/etc/prometheus/kubelet-ca.crt`
through the generated Prometheus ConfigMap. Normal CA and node-IP SAN verification
remain enabled; missing or invalid trust stops staging before workload changes.
Prometheus keeps only `nodes/metrics` permission, never broad `nodes/proxy` access.

For K3s, the platform job exports the cluster's serving CA and
[K3s registration](k3s-delivery.md#prepare-a-separate-registration) copies it into the
profile as `kubelet-ca.crt`. Only the retained Kind recovery profile needs the
Kind path: collect the current public serving certificates through the trusted
Docker daemon, after environment and cluster UID validation:

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

API scrape verification checks every nonterminating API pod against its
actual IP and current readiness, the deployment generation and desired replica
count, and a successful scrape within 60 seconds. Missing discovery, a failed
replica, stale results, or incomplete HPA rollout cannot pass on another pod's
healthy metric. The bounded 120-second wait still accommodates discovery after
rollout and returns immediately when every expected target is healthy.
