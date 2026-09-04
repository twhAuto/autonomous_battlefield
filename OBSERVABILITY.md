# Observability and reliability objectives

Autonomous Battlefield exposes Prometheus metrics from both Go processes and
ships local metrics and logging stacks for the `kind` deployment. Prometheus
and Grafana provide metrics; Fluent Bit, Elasticsearch, and Kibana provide
centralized logs. The dashboard and objectives in this guide are internal
operating targets. They are not a customer-facing service-level agreement.

## Signal path

```text
API pods :8080/metrics --------+
Engine pods :8081/metrics -----+
kube-state-metrics :8080 ------+--> Prometheus --> Grafana dashboards
kubelet /metrics/cadvisor -----+          |
                                          +--> recording and alert rules

application container stdout --> Fluent Bit --> Elasticsearch --> Kibana
```

Prometheus discovers annotated pods in the `battlefield` namespace and uses
Kubernetes node discovery for one cAdvisor target per kubelet. Its namespaced
Role can list and watch application pods. A separate ClusterRole permits only
node discovery and `GET` access to the kubelet metrics proxy. Grafana receives
no Kubernetes credentials and uses the internal Prometheus Service as its sole
data source.

The local stack deliberately does not include Alertmanager. Prometheus evaluates
the checked-in alert rules and shows their state, but notifications require an
Alertmanager or another production alert-delivery integration.

Fluent Bit runs once per node and tails API, engine, frontend, and migration
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

## Application metrics

The API and engine expose `/metrics` on their existing operational ports. The
endpoint remains available during startup and graceful shutdown so a failed
dependency does not hide process diagnostics.

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
cAdvisor through the authenticated Kubernetes API proxy, so no privileged
cAdvisor DaemonSet or host mounts are required.

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

## Run the local stack

The normal deployment command renders and applies observability after the
application rollouts:

```bash
./scripts/cd/create-kind-cluster.sh
./scripts/cd/deploy-kind.sh
./scripts/cd/smoke-test-kind.sh
```

Visit `http://grafana.localhost:8888` and open **Autonomous Battlefield /
Autonomous Battlefield Overview** for application SLIs or **Autonomous
Battlefield / Kubernetes Workloads** for pod state and resource usage. Visit
`http://kibana.localhost:8888`, open **Discover**, and select **Autonomous
Battlefield Logs** for logs. The deployment job creates that data view before
the observability stage is considered complete.

For model-controller troubleshooting, open the provisioned **Battlefield AI
Decisions** saved search in Discover. It filters to decision lifecycle events
and shows the correlation fields needed to follow one decision across the
engine, MCP service, and in-cluster agent: `battle_id`, `request_id`,
`faction_id`, `target_tick`, `profile_id`, `decision_status`,
`error_category`, `fallback_source`, and `duration_ms`. The collector includes
the API, engine, agent, MCP, frontend, and migration workloads. A subscription
CLI agent runs on the host, so its process-local messages remain in the bridge
terminal; its MCP claims, tool calls, submissions, failures, and final engine
resolution are recorded by Kubernetes services and therefore appear in
Kibana.

Useful KQL filters include:

```text
msg: decision_*
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

The local Prometheus TSDB, Grafana database, and Elasticsearch data use
`emptyDir`; their data is lost when the corresponding pod is replaced. Kibana
saved objects live in the same disposable Elasticsearch store. Prometheus
retains up to 30 days only while its pod remains intact. The Kibana setup Job
recreates the checked-in data view, but it does not restore logs. This is
appropriate for a disposable kind cluster, not for an SLA evidence or audit
source.

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
