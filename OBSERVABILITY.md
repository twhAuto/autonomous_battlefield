# Observability and reliability objectives

Autonomous Battlefield exposes Prometheus metrics from both Go processes and
ships a local Prometheus and Grafana stack for the `kind` deployment. The
dashboard and objectives in this guide are internal operating targets. They are
not a customer-facing service-level agreement.

## Signal path

```text
API pods :8080/metrics ----+
                            +--> Prometheus --> Grafana dashboard
Engine pods :8081/metrics -+          |
                                      +--> recording and alert rules
```

Prometheus discovers only annotated pods in the `battlefield` namespace. Its
Role can list and watch pods in that namespace only. Grafana receives no
Kubernetes credentials and uses the internal Prometheus Service as its sole
data source.

The local stack deliberately does not include Alertmanager. Prometheus evaluates
the checked-in alert rules and shows their state, but notifications require an
Alertmanager or another production alert-delivery integration.

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
as a scrape target label by Prometheus.

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
Autonomous Battlefield Overview**. Local Grafana allows anonymous Viewer access
and disables basic login. Envoy Gateway exposes it only through a Kind node port
bound to `127.0.0.1`, so it is reachable from the local machine but not the
network.

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
```

For a direct endpoint check:

```bash
kubectl port-forward --namespace battlefield \
  service/battlefield-api 8080:8080
curl --fail http://localhost:8080/metrics
```

## Local versus production

The local Prometheus TSDB and Grafana database use `emptyDir`; their data is
lost when the corresponding pod is replaced. Prometheus retains up to 30 days
only while that pod remains intact. This is appropriate for a disposable kind
cluster, not for an SLA evidence source.

For AWS, keep the application metric contract but replace the local stack with
a durable managed or self-hosted metrics backend. Add ingress or load-balancer
metrics so the availability SLI covers failures that never reach the API, use
durable retention longer than the objective window, configure authenticated
Grafana access, and route alerts to an owned notification channel.
