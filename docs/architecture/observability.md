# Observability architecture

Autonomous Battlefield exposes Prometheus metrics from the API and engine and
ships local metrics, tracing, and logging stacks for the `kind` deployment. Prometheus
and Grafana provide metrics; Fluent Bit, Elasticsearch, and Kibana provide
centralized logs. The dashboard and objectives in this guide are internal
operating targets. They are not a customer-facing service-level agreement.

HTTP completion logs include `actor_id` and `actor_role` established by trusted
authentication middleware, alongside method, resource path, status and request ID.
The request ID is echoed in `X-Request-ID`. A caller-supplied value is kept only when it
matches `[A-Za-z0-9._:-]{1,128}`, so it is safe to echo and log; otherwise the server
generates one.
An authentication observer carries identity to the outer request logger; rejected
credentials leave identity empty. Tokens, cookies and email headers are not logged.
Error text passes through `observability/redact` before it reaches a log or a span.
A pgx connection error (database user, database name, host) becomes "failed to connect
to the database", keeping any SQLSTATE; a connection string pgx cannot parse becomes
"invalid database connection string", since pgx quotes it with only best-effort
password masking; and network errors lose their address and
hostname, in every branch of a wrapped or joined error; other error text is kept for
debugging. A typed-nil error logs as `<nil>`. The logging handler redacts every
error-valued attribute under any key, so call sites keep logging `"error", err`. Spans
record errors through `tracing.RecordError` (also used by `tracing.EndSpan`), and
`backend/test/conventions` fails the build if code calls `span.RecordError` directly.
These operational logs provide request attribution, not a tamper-proof audit store.

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

The local stack deliberately does not include Alertmanager. Prometheus evaluates
the checked-in alert rules and shows their state, but notifications require an
Alertmanager or another production alert-delivery integration.

Fluent Bit runs once per node and tails API, engine, agent, AI gateway, frontend, and migration
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
and raw provider responses are excluded from span attributes. Tempo storage is
also disposable; it is not an audit archive.

## Application metrics

The API and engine expose `/metrics` on their existing operational ports. The
endpoint remains available during startup and graceful shutdown so a failed
dependency does not hide process diagnostics.

HTTP request series appear only after a replica handles application traffic;
lifecycle probes and metrics scrapes bypass request instrumentation.

| Metric | Type | Meaning |
| --- | --- | --- |
| `battlefield_http_server_requests_total` | Counter | API requests by method, normalized route pattern, and status code. |
| `battlefield_http_server_request_duration_seconds` | Histogram | API request latency by method and normalized route pattern. |
| `battlefield_engine_battle_claims_total` | Counter | Initial and reclaimed battle claims. |
| `battlefield_engine_battles_processed_total` | Counter | Processing attempts by `completed`, `failed`, `error`, `lease_lost`, or `requeued` outcome. |
| `battlefield_engine_battle_processing_duration_seconds` | Histogram | Wall-clock processing duration by outcome. |
| `battlefield_engine_ticks_total` | Counter | Successfully advanced simulation ticks. |
| `battlefield_engine_worker_ready` | Gauge | `1` while the worker can claim work, otherwise `0`. |
| `battlefield_result_cache_operations_total` | Counter | API completed-battle Redis cache operations by `operation` (`get`, `set`) and `outcome` (`hit`, `miss`, `stored`, `error`). The API falls back to PostgreSQL on any cache miss or failure, so a rising `error` count is the only sign of an unhealthy cache. Recorded only when a Redis URL is configured. |

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

Workload telemetry explains pod health and resource use; it does not directly
measure whether a user request succeeded.

## Engine capacity and recovery limits

Each engine worker processes one battle synchronously before claiming another;
the application manifest defaults to one engine replica. Additional admitted
battles queue. The default admission limit is four unfinished battles globally,
and queued time counts toward the one-hour battle expiry. Queue delay therefore
depends on battle duration, decision waits, pauses and available engine replicas;
admission does not guarantee that a battle finishes before expiry.

Crash recovery has two gates: the previous lease must expire (30 seconds by
default), and an available worker must make its next claim. Claims select eligible
battles by creation time and ID. An engine already processing another battle
does not interrupt it to reclaim an orphan, so recovery can take longer than the
lease duration.

Every claim starts a new attempt at tick 0 from the frozen initial state. It does
not resume the last persisted tick. Earlier attempt events remain inspectable,
and matching resolved decisions can be reused as described in
[battle retries](system-architecture.md#battle-retries).

Every claim consumes an attempt, including a claim after a successful graceful
shutdown requeue. Requeueing preserves the attempt count. With
`engine.maxAttempts: 3`, an interrupted third attempt is marked `failed` with
`attempts_exhausted` on a subsequent claim pass instead of being claimed again.
Repeated rolling restarts during a battle can therefore exhaust its retry budget.

Database errors returned by the claim operation terminate the worker loop and
cause the engine process to exit with an error; Kubernetes restarts the container.
A database interruption that reaches this path can consequently trigger lease
recovery and a fresh attempt. Restart timing and recovery are separate: a ready
replacement still has to satisfy the claim gates above.

The API and engines share PostgreSQL. Inspect database CPU and throttling, query
latency and connection-pool pressure alongside API pod usage when assessing
capacity. Adding API replicas does not remove shared database work; the limiting
resource and sustainable request rate depend on the workload and resource limits.
Use [reliability testing](../operations/reliability-testing.md) to measure them.

These contracts come from the
[worker loop](../../backend/internal/battle/worker/worker.go),
[claim and requeue operations](../../backend/internal/battle/repo/queue.go),
[expiry sweep](../../backend/internal/battle/repo/expiry.go) and
[engine entry point](../../backend/cmd/engine/main.go). Historical queue timings
and load measurements are kept separately from these runtime contracts.

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
health-request logs. Environment changes take effect when the process restarts.

Logs emitted with an active OpenTelemetry span include root-level `trace_id`
and `span_id`. API access logs share the inbound request span, including
rejected requests; engine and agent logs use their existing consumer spans.
Startup logs without a span omit these fields. Sampling can mean
a logged trace ID has no stored trace. HTTP logs include `duration_ms` while
retaining the older `duration` field for compatibility.

Automation should filter on `event`; the provisioned AI search also matches
legacy records that used event identifiers in `msg`.

`decision_window_started` records the configured timeout,
`decision_context_served` confirms observation retrieval,
`decision_plan_submitted` records validation and acceptance, and
`decision_resolved` is the authoritative final outcome selected by the engine.
Operational logs intentionally exclude capability tokens, credentials, full
observations, prompts, and CLI output.

## Health endpoints

Process entry points wire the shared `httpapi.LifecycleHandler`, which owns all
health probes. Application routing does not register a second set of probes.
Engine readiness requires both a running worker and a successful database ping;
worker shutdown or database failure makes readiness fail while liveness remains
available. See the [codebase guide](../developer/backend-guide.md#process-health-ownership)
for source ownership and lifecycle tests.


For stack deployment, access, storage recovery and trusted kubelet certificates,
see the [observability runbook](../operations/observability.md).
