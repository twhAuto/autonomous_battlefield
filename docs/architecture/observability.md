# Observability architecture

OrdinalFrontier exposes Prometheus metrics from the API and engine and ships one
metrics, tracing, and logging stack. Prometheus and Grafana provide metrics;
Fluent Bit, Elasticsearch, and Kibana provide centralized logs; the OpenTelemetry
Collector and Tempo provide traces. Development Kind and production K3s deploy the
same manifests (`infra/deploy/overlays/local/observability`, which the K3s overlay
reuses); [environments](#environments) lists the differences. The dashboard and
objectives in this guide are internal operating targets. They are not a
customer-facing service-level agreement.

## Signal path

```text
API pods :8080/metrics --------+
Engine pods :8081/metrics -----+
kube-state-metrics :8080 ------+--> Prometheus --> Grafana dashboards
kubelet /metrics/cadvisor -----+          |
                                          +--> recording and alert rules

application container stdout --> Fluent Bit --> Elasticsearch --> Kibana
API / engine / AI gateway / agent spans --> OTel Collector --> Tempo --> Grafana
```

Prometheus discovers annotated pods in the `battlefield` namespace and uses
Kubernetes node discovery for one cAdvisor target per kubelet. Its namespaced
Role can list and watch application pods. A separate ClusterRole permits only
node discovery and `GET` access to the kubelet metrics subresource. Prometheus
uses its mounted service-account token to scrape each local kubelet directly;
it does not receive the broader `nodes/proxy` permission. Grafana receives no
Kubernetes credentials. It uses internal Prometheus and Tempo Services as
its metrics and trace data sources.

Grafana provisions three dashboards: **Autonomous Battlefield Overview**,
**Kubernetes Workloads** and **Battle Traces**.

The stack deliberately does not include Alertmanager in either environment.
Prometheus evaluates the checked-in alert rules and shows their state, but
notifications require an Alertmanager or another alert-delivery integration.

## Environments

| Aspect | Development (Kind) | Production (K3s) |
| --- | --- | --- |
| Prometheus, Grafana, Tempo data | `emptyDir`; lost when a pod is replaced (Prometheus keeps up to 30 days while its pod lives) | Same |
| Elasticsearch indices and Kibana saved objects | 10 Gi `standard` PVC | 20 Gi PVC on the retained `battlefield-retained` storage class |
| Kubelet scrape TLS | Skips verification (node-local certificate) | Verified with the exported kubelet CA |
| Access | `grafana.localhost` / `kibana.localhost` through the loopback Kind gateway | `grafana.home.arpa` / `kibana.home.arpa` through the host's private HTTPS aliases; never through the public tunnel |

Grafana serves an anonymous read-only Viewer, and Elasticsearch and Kibana run
without authentication. In production this is an accepted risk for a private,
single-operator cluster: anyone who can reach the private aliases on the host
network can read dashboards and logs, and can change Kibana saved objects. Logs
exclude credentials, tokens and full observations (see
[structured logs](#structured-logs)). Revisit this before granting anyone else
network access to the host, or before using a shared cluster. Retained storage is
not a backup; see [observability operations](../operations/observability.md#storage-and-access-by-environment).

Fluent Bit runs once per node and tails API, engine, agent, AI gateway, frontend
and migration container logs from the `battlefield` namespace. It adds pod,
container and label metadata and writes daily `battlefield-app-YYYY.MM.DD`
indices. JSON application logs become searchable fields; plain-text frontend logs
keep their `log` field. Infrastructure logs stay out of this index. The collector
runs in its own `battlefield-logging` namespace, so its read-only node log mounts
do not weaken the application namespace's Pod Security policy; its Role reads pods
in `battlefield` only.

## Distributed traces

The API, engine, AI gateway, and agent use OpenTelemetry. W3C trace context is stored on
battle and decision rows so workers can connect their spans after queue claims. The
collector accepts OTLP and exports traces to Tempo. Host agents need an
OTLP endpoint reachable from the host to export their own spans.

A battle is traced as three linked parts, all always sampled; other spans use
`OTEL_TRACES_SAMPLER_ARG` (10% by default):

1. **Creation:** the API server span for `POST /v1/battles`, including its
   database insert.
2. **Processing:** `battlefield.engine.process`, a new root per battle attempt
   with a span link to the creating request. It contains one
   `battlefield.engine.decision_window` span per production window and the
   engine's database operations. A battle outlives its request, so the attempt is
   its own trace rather than a child.
3. **Decisions:** `battlefield.agent.decision`, a new root per model decision
   (with a stable ID for Langfuse) linked to its decision window. It contains the
   provider calls (`battlefield.model.api_call`) and the MCP tool spans.

Links point from the engine attempt to the creating request, and from each
decision to its decision window. See [trace lookup](../operations/observability.md#find-a-battle-trace)
for operational queries.

Spans carry `deployment.environment` from `BATTLEFIELD_ENVIRONMENT`: the base manifests
set `production` on the API, engine, AI gateway and agent, and a dev deployment rewrites it
to `dev` along with `BATTLEFIELD_ENV`. A process started without it, such as a local
`go run`, reports `local`.

See [`internal/observability/tracing/tracing.go`](../../backend/internal/observability/tracing/tracing.go)
for environment configuration and instrumentation, and the
[collector configuration](../../infra/deploy/overlays/local/observability/otel-collector/config.yaml)
for the in-cluster path. Prompts, credentials, capabilities, full observations,
and raw provider responses are excluded from span attributes. Tempo keeps traces
for 24 hours on disposable storage; it is not an audit archive.

## AI observations in Langfuse

Langfuse is optional. When it is installed, the collector forwards only AI
observations (decision roots, model calls and MCP tool spans) to a self-hosted
Langfuse release; Tempo keeps everything else. Only the collector holds the
ingestion credentials, and the API reads an optional public URL and project for
inspector links. Export is asynchronous and never takes part in decision
acceptance or timeouts. Decision trace IDs are derived from the request ID, so the
AI gateway's tool spans join the right trace even when a CLI does not propagate
trace headers. [Langfuse operations](../operations/langfuse.md) owns installation,
sampling, privacy, storage and token accounting.

## Application metrics

The API and engine expose `/metrics` on their existing operational ports. The
endpoint remains available during startup and graceful shutdown so a failed
dependency does not hide process diagnostics.

A labelled counter or histogram series that first appears already at 1 shows no
increase to `rate()` or `increase()`. The engine therefore creates every claim and
outcome series at zero when its worker starts, and the API creates every result-cache
series at zero when a Redis URL is configured, so the first event after a restart
counts in dashboards, SLIs and alerts. HTTP request series cannot be listed in advance:
each method, route and status series appears with its first request, so that first
request after a replica starts is invisible to `rate()` and `increase()`. Lifecycle
probes and metrics scrapes bypass request instrumentation.

| Metric | Type | Meaning |
| --- | --- | --- |
| `battlefield_http_server_requests_total` | Counter | API requests by method, normalized route pattern, and status code. |
| `battlefield_http_server_request_duration_seconds` | Histogram | API request latency by method, normalized route pattern, and `success` or `error` (5xx) outcome. |
| `battlefield_engine_battle_claims_total` | Counter | Initial and reclaimed battle claims. |
| `battlefield_engine_battles_processed_total` | Counter | Processing attempts by `completed`, `failed`, `error`, `lease_lost`, or `requeued` outcome. |
| `battlefield_engine_battle_processing_duration_seconds` | Histogram | Wall-clock processing duration by outcome. |
| `battlefield_engine_ticks_total` | Counter | Successfully advanced simulation ticks. |
| `battlefield_engine_worker_ready` | Gauge | `1` while the worker can claim work, otherwise `0`. |
| `battlefield_engine_battle_slots` | Gauge | Battles the worker can run at once (`engine.concurrency`). |
| `battlefield_engine_active_battles` | Gauge | Battles the worker is running now; equal to the slots when the engine is saturated. |
| `battlefield_result_cache_operations_total` | Counter | API completed-battle Redis cache operations by `operation` (`get`, `set`) and `outcome` (`hit`, `miss`, `stored`, `error`). The API falls back to PostgreSQL on any cache miss or failure, so a rising `error` count is the only sign of an unhealthy cache. Recorded only when a Redis URL is configured. |

The Prometheus Go client also publishes standard `go_*`, `process_*`, and
`promhttp_*` metrics. Route labels use the registered Go HTTP pattern that the
request matches, such as `GET /v1/battles/{id}`, rather than its raw path. A request
that authentication or authorization rejects keeps its route's label, so a 401 or 403
counts against the route it was sent to; only a request no route matches is labelled
`unmatched`. The API server span records the same pattern as `http.route`. Method
labels keep the nine standard HTTP methods and record any other method a client
sends as `other`. Battle IDs, faction IDs, request IDs, pod names, and worker IDs
are intentionally absent from application metric labels to avoid unbounded
time-series cardinality. Prometheus adds pod identity
only as the `scrape_pod` target label, which stays distinct from kube-state-metrics'
`pod` label for the workload being described.

## Kubernetes workload metrics

The Kubernetes dashboard combines two complementary metric sources:

| Source | Answers | Example metrics |
| --- | --- | --- |
| `kube-state-metrics` | What Kubernetes says should exist and whether workload objects are healthy | `kube_pod_status_phase`, `kube_pod_container_status_ready`, `kube_pod_container_status_restarts_total`, `kube_deployment_status_replicas_available` |
| kubelet cAdvisor endpoint | What CPU, memory, and network resources containers are actually consuming | `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`, `container_network_receive_bytes_total`, `container_network_transmit_bytes_total` |

kube-state-metrics watches only the `battlefield` namespace and the workload types
in its manifest; it does not read Secrets. cAdvisor is scraped directly from each
kubelet as described in the [signal path](#signal-path), so no privileged
DaemonSet or host mounts are needed. Kubelet certificate handling differs by
environment; see [production kubelet TLS](../operations/observability.md#production-kubelet-tls).

Workload telemetry explains pod health and resource use; it does not directly
measure whether a user request succeeded. For engine queueing, claim timing and
crash recovery, which shape the battle-processing metrics, see
[engine capacity and recovery](system-architecture.md#engine-capacity-and-recovery).

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

## Alerts

Prometheus loads `alerts.yml` from the local observability overlay:

| Alert | Fires when | Severity |
| --- | --- | --- |
| `BattlefieldAPIAvailabilityBudgetBurnFast` | The 5xx ratio over both 5 minutes and 1 hour exceeds 14.4 times the budget, for 2 minutes | critical |
| `BattlefieldAPIAvailabilityBudgetBurnSlow` | The 5xx ratio over both 30 minutes and 6 hours exceeds 6 times the budget, for 15 minutes | warning |
| `BattlefieldMetricsTargetDown` | An application metrics target cannot be scraped for 5 minutes | warning |
| `BattlefieldEngineProcessingFailure` | Any `failed`, `error` or `lease_lost` processing outcome in the last 15 minutes | warning |

The multi-window availability alerts reduce noise from a brief bad sample while
still detecting both acute and sustained failures. Their thresholds are based
on the proposed 99.9% objective (`1 - 0.999 = 0.001`).

The availability recording rule treats a missing 5xx series as zero
(`or vector(0)`). Without it, a healthy API that has never returned a 5xx has no
error series, the ratio is an empty vector, and the SLI shows no data.
Rule regression checks are described in [CI verification](../developer/ci.md#observability-rule-checks).

## Structured logs

Application logs separate the stable machine-readable `event` identifier from
`msg`, a readable summary that includes service and relevant operational
context. Fields such as `battle_id`, `request_id`, `error`, and timing values remain structured
for filtering and expanded inspection. For example:

```text
[engine] Battle simulation progressing; battle: castle-42; worker: engine-1; attempt: 1; requested ticks: 600; tick: 120; elapsed (ms): 94000
[engine] Waiting for faction decisions; battle: castle-42; attempt: 1; target tick: 130; pending factions: 2; elapsed (ms): 0; remaining (ms): 119999
```

The engine logs progress after the first running tick and then at most every 15
seconds. A waiting decision window logs at once and then at most every 15 seconds
with the pending faction count and remaining timeout. Elapsed time includes pauses
and decision waits. Completion logs ticks run, elapsed time, winner and reason;
full combat events stay in `battle_events`.

`LOG_LEVEL` controls backend process logging (`DEBUG`, `INFO`, `WARN`, `ERROR`;
case-insensitive). It defaults to INFO, including for invalid values. DEBUG adds
`battle_tick_completed` after each successful running-tick save and enables
health-request logs. Environment changes take effect when the process restarts.

Logs emitted with an active OpenTelemetry span include root-level `trace_id`
and `span_id`. API access logs share the inbound request span, including
rejected requests; engine and agent logs use their existing consumer spans.
Startup logs without a span omit these fields. Sampling can mean
a logged trace ID has no stored trace. HTTP logs include `duration_ms` while
retaining the older `duration` field for compatibility.

Automation should filter on `event`.

`decision_window_started` records the configured timeout,
`decision_context_served` confirms observation retrieval,
`decision_plan_submitted` records validation and acceptance, and
`decision_resolved` is the authoritative final outcome selected by the engine.
Operational logs intentionally exclude capability tokens, credentials, full
observations, prompts, and CLI output.

### Request attribution and redaction

HTTP completion logs include `actor_id` and `actor_role` established by trusted
authentication middleware, alongside method, resource path, status and request ID.
An authentication observer carries identity to the outer request logger; rejected
credentials leave identity empty. Tokens, cookies and email headers are not logged.
These logs provide request attribution, not a tamper-proof audit store.

The request ID is echoed in `X-Request-ID`. A caller-supplied value is kept only
when it matches `[A-Za-z0-9._:-]{1,128}`, so it is safe to echo and log; otherwise
the server generates one.

Error text passes through `observability/redact` before it reaches a log or a span:

- A pgx connection error (database user, database name, host) becomes "failed to
  connect to the database", keeping any SQLSTATE.
- A connection string pgx cannot parse becomes "invalid database connection
  string", because pgx quotes it with only best-effort password masking.
- Network errors lose their address and hostname.

Redaction applies to every branch of a wrapped or joined error; other error text is
kept for debugging, and a typed-nil error logs as `<nil>`. The logging handler
redacts every error-valued attribute under any key, so call sites keep logging
`"error", err`. Spans record errors through `tracing.RecordError` (also used by
`tracing.EndSpan`), and `backend/test/conventions` fails the build if code calls
`span.RecordError` directly.

## Health endpoints

The API and engine share one handler for startup, liveness, readiness and metrics
(see [lifecycle probes](api.md#lifecycle-probes)). Engine readiness requires both a
running worker and a successful database ping; worker shutdown or database failure
makes readiness fail while liveness remains available.

The agent and AI gateway serve their own `/healthz`. The agent also serves `/status`
with its drain state (`draining`, `active`, `concurrency`), the profile IDs it claims
work for (`profiles`) and the reason each other configured profile cannot run here
(`unavailable`, also logged at startup as `agent_profile_unavailable`), and the AI gateway's
`/readyz` pings PostgreSQL for its readiness probe. See the
[backend guide](../developer/backend-guide.md#process-health-ownership) for source
ownership and lifecycle tests.

For stack deployment, access, storage recovery and trusted kubelet certificates,
see the [observability runbook](../operations/observability.md).
