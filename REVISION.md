# Autonomous Battlefield Revision Guide

This document is the learning plan and design notebook for Autonomous Battlefield. It connects the project to backend, cloud, Go, system-design, AI-agent, AWS, and Kubernetes interview topics.

For the software's current capabilities, setup instructions, API, and configuration, see the [project README](README.md).

The intended product is an auto-battler and expansion game in which several **factions** build, gather resources, expand territory, and fight on a continuously running battlefield. Humans or AI agents will control factions through an API. They will submit commands to an authoritative game engine rather than modify world state directly, keeping the simulation deterministic, testable, secure, and suitable for replay.

Most sections below describe target architecture or future exercises, not functionality that is already implemented. The implementation should begin as a well-structured Go service and add infrastructure only when a game requirement gives it a clear purpose.

## Using this project for revision

The project turns abstract interview topics into experiments. Answer a questionnaire question from memory first, then compare it with the detailed `Improved answer` and implement or test the matching behavior here:

- [Backend and Cloud questionnaire](../../revision/Backend-Cloud/QUESTIONNAIRE.md)
- [Go questionnaire](../../revision/Golang/QUESTIONNAIRE.md)
- [System Design questionnaire](../../revision/System%20Design/QUESTIONNAIRE.md)
- [AI questionnaire](../../revision/AI/QUESTIONNAIRE.md)

The Backend/Cloud and AI questionnaires contain reviewed answers. The blank Go and System Design answer fields remain recall exercises and should be completed before adding reference answers. Behavioral answers require genuine personal examples and should not be inferred from this project.

## Product concept

A battlefield contains territory, buildings, units, resources, and factions. The simulation advances in discrete ticks. At each tick, the engine processes accepted commands and deterministic rules such as movement, production, construction, combat, and resource generation.

Example commands:

- Create a battlefield or join a faction.
- Build a structure at a location.
- Recruit, move, or attack with units.
- Research an upgrade.
- Change production priorities.
- Ask for the faction's currently visible world state.

The first version can be turn-like internally even if it runs automatically. A fixed tick makes behavior easier to reason about than many independently mutating goroutines.

## Core design principles

1. **The engine is authoritative.** Clients and AI agents propose commands; deterministic code checks permissions, cost, timing, and game rules.
2. **Commands express intent.** `MoveUnits` or `BuildStructure` is preferable to allowing clients to write arbitrary entity fields.
3. **Every accepted command has an identity.** A command ID or idempotency key makes retries safe.
4. **State and emitted events stay consistent.** Persist the state transition and its outbox events in one database transaction.
5. **Ordering is explicit.** A battlefield has a tick/version, and commands define which version they expect.
6. **The first architecture is a modular monolith.** Package boundaries allow later extraction without paying distributed-system costs immediately.
7. **AI is outside the rules engine.** A model may plan and select tools, but it cannot bypass deterministic validation or authorization.
8. **One reliability model does not fit every operation.** Command state needs immediate correctness; spectator views, analytics, and notifications can tolerate bounded staleness.

## Initial architecture

```text
Human UI / AI agent
        |
        | REST commands and queries
        v
Go API: authentication, authorization, validation, rate limits
        |
        v
Command service -> authoritative simulation engine
        |                    |
        |                    +-> domain events
        v
PostgreSQL transaction: battlefield state + command result + outbox
        |
        v
Outbox publisher -> queue -> projections, notifications, analytics, replay archive
```

Keep these as packages in one process initially:

```text
cmd/server/          process startup and graceful shutdown
internal/api/        HTTP handlers and transport models
internal/auth/       player/agent identity and faction authorization
internal/game/       entities, commands, rules, and simulation ticks
internal/store/      PostgreSQL repositories and transactions
internal/outbox/     durable event publication
internal/worker/     bounded asynchronous consumers
internal/telemetry/  logs, metrics, and traces
```

The pure game package should not depend on HTTP, PostgreSQL, AWS, Kubernetes, or an AI provider. Given the same state, command sequence, rules version, and random seed, it should produce the same result.

## Suggested API

Resource reads use REST-style endpoints:

```http
POST /v1/battlefields
GET  /v1/battlefields/{battlefield_id}
GET  /v1/battlefields/{battlefield_id}/events?after={cursor}&limit=100
GET  /v1/battlefields/{battlefield_id}/factions/{faction_id}/view
POST /v1/battlefields/{battlefield_id}/commands
GET  /v1/commands/{command_id}
```

A command request could be:

```http
POST /v1/battlefields/bf-1/commands
Idempotency-Key: 4db8d152-1e04-4d10-8824-859c6ed72878
Content-Type: application/json

{
  "type": "move_units",
  "factionId": "faction-2",
  "expectedBattlefieldVersion": 81,
  "unitIds": ["unit-7", "unit-9"],
  "destination": {"x": 12, "y": 4}
}
```

Possible responses:

- `202 Accepted` when the command is durably queued for a later tick.
- `201 Created` when creating a new resource.
- `200 OK` when a command is processed synchronously.
- `409 Conflict` for an outdated battlefield version or conflicting command.
- `422 Unprocessable Content` when the command is structurally valid but violates a game rule.
- `429 Too Many Requests` when a player or agent exceeds its command budget.

An idempotency key identifies one logical command across network retries. A trace ID correlates one technical request path through logs and services. Do not use a trace ID for deduplication, authentication, or authorization.

## Data model sketch

Start with PostgreSQL tables similar to:

```text
battlefields(id, status, current_tick, version, rules_version, seed, created_at)
factions(id, battlefield_id, owner_id, resources, version)
units(id, battlefield_id, faction_id, type, x, y, health, version)
buildings(id, battlefield_id, faction_id, type, x, y, health, version)
commands(id, idempotency_key, battlefield_id, faction_id, type, payload,
         expected_version, status, result, created_at, processed_at)
outbox_events(id, aggregate_id, aggregate_version, type, payload,
              created_at, published_at)
processed_events(consumer, event_id, processed_at)
```

If the service becomes a hosted multi-tenant product, introduce an explicit `tenant_id` on owned records rather than treating faction IDs as a security boundary. A tenant might be a game studio, tournament organizer, classroom, or private server; players and factions then exist within that tenant.

Important constraints and indexes might include:

```sql
CREATE UNIQUE INDEX commands_idempotency_idx
ON commands (faction_id, idempotency_key);

CREATE INDEX units_battlefield_faction_idx
ON units (battlefield_id, faction_id, id);

CREATE INDEX events_battlefield_created_id_idx
ON outbox_events (aggregate_id, created_at, id);
```

Indexes are selected from actual query patterns. A B-tree can locate a starting key in approximately `O(log n)` and then scan matching entries. Indexes improve reads, ordering, joins, uniqueness, and keyset pagination, but consume storage and add work to every write.

## Security and tenant isolation

Authentication proves the caller's identity. Authorization determines whether that identity may observe or control a particular faction or administer a battlefield. Derive tenant and actor identity from validated credentials; never trust an HTTP body or model-generated tool argument to establish authority.

For a hosted version:

- Include tenant scope in database queries, constraints, cache keys, events, object paths, metrics, and audit records.
- Add cross-tenant negative tests, not only successful authorization tests.
- Apply per-tenant command, AI-token, connection, and storage quotas.
- Use fair scheduling or isolated worker pools so one large tenant cannot consume every tick worker.
- Give services and AWS roles only the database tables, queues, buckets, and actions they require.
- Keep secrets in a managed secret store, rotate them, and exclude them from source, prompts, traces, and logs.
- Record tamper-resistant audit events containing actor, tenant, action, resource, time, outcome, and safe change metadata.

An audit log is different from a debug log: it is a security/compliance record with controlled access, retention, export, and integrity requirements.

## Consistency and concurrency

PostgreSQL is the source of truth. A command should update game state, record its outcome, and insert outbox events in one short transaction.

At the default `READ COMMITTED` level in many databases, a query sees committed data but two queries in one transaction may observe different committed versions. Use version checks or row locks for invariants that require them:

```sql
UPDATE battlefields
SET current_tick = current_tick + 1,
    version = version + 1
WHERE id = :battlefield_id
  AND version = :expected_version;
```

If zero rows are updated, another worker won the race. Reload and retry or return a conflict. Alternatively, a tick processor can lock the battlefield row with `SELECT ... FOR UPDATE` while applying a short state transition.

Deadlocks occur when transactions acquire locks in conflicting orders. Always lock battlefield, faction, and entity records in a documented stable order; keep transactions short; never call an AI model or remote service while holding a database transaction; and retry database-selected deadlock victims with a bounded policy.

## Events and eventual consistency

The committed battlefield state is strongly consistent within PostgreSQL. Derived views can be eventually consistent:

- Leaderboards
- Search or discovery
- Analytics
- Notifications
- Spectator projections
- Replay files

The outbox publisher provides **at-least-once delivery**, so duplicate events are normal. Each event receives a stable event ID, and consumers deduplicate it or apply naturally idempotent updates. This gives effectively-once business effects without claiming magical end-to-end exactly-once delivery.

Processing choices:

- **At-most-once:** no duplicates, but a crash can lose work; acceptable for disposable telemetry.
- **At-least-once:** retries prevent ordinary loss, but consumers must handle duplicates; preferred for game events.
- **Exactly-once effect:** requires a precisely defined transactional boundary, deduplication, unique constraints, or idempotent external APIs.

Measure eventual consistency with a target such as “99.9% of projections catch up within five seconds,” then monitor queue depth, oldest event age, failures, and reconciliation mismatches.

## Pagination and battlefield history

Use keyset pagination for a long, changing event history:

```sql
SELECT *
FROM battlefield_events
WHERE battlefield_id = :battlefield_id
  AND (created_at, id) > (:last_created_at, :last_id)
ORDER BY created_at ASC, id ASC
LIMIT :limit;
```

The pair comparison means “later timestamp, or the same timestamp with a larger ID.” The unique `id` is the tie-breaker. It is stable under new inserts and avoids scanning a deep offset.

Offset pagination remains useful for small administration screens that require numbered pages. On frequently changing data, inserts before an offset can create duplicate or skipped results. Prefer cursors for feeds, events, units, and replays.

## AI-agent boundary

An AI faction controller observes only the information that faction is permitted to see. The model returns a typed plan or proposes tool calls such as:

```text
get_faction_view(battlefield_id)
list_available_actions(battlefield_id, faction_id)
submit_command(command, expected_version, idempotency_key)
explain_last_command(command_id)
```

The application—not the model—must:

- Derive identity and faction authorization from trusted credentials.
- Validate tool arguments and game rules.
- Limit commands, model steps, time, and cost per tick.
- Treat model output and retrieved content as untrusted.
- Prevent repeated/no-progress tool loops.
- Require deterministic confirmation for high-impact administrative actions.
- Record model, prompt, tool, observation, and outcome versions for evaluation.

Start with a deterministic scripted bot. Add an LLM bot only after the command contract and game simulator are testable. Compare bots using win rate, invalid-command rate, decision latency, token cost, strategy diversity, and rule-version regressions. Do not optimize solely for winning; an entertaining and explainable agent may be a better product.

As a separate experiment, train a narrow traditional model to choose among a fixed set of actions from numeric game-state features. Compare its predictable schema, training-data needs, latency, and narrow objective with the LLM's flexible language/tool reasoning, higher cost, nondeterminism, and hallucination risk. This makes the traditional-ML-versus-LLM distinction concrete without putting ML inside the authoritative rules engine.

### AI rules coach and RAG lab

The faction controller exercises agents and tools, but it does not naturally teach the complete RAG pipeline. Add a separate read-only coach that answers questions about game rules, unit statistics, patch notes, and replay explanations with citations.

Its ingestion pipeline should parse documents at semantic boundaries, preserve title/rules-version/tenant/permission metadata, create embeddings, and build searchable indexes. Its query pipeline should compare:

- Sparse retrieval for exact unit names, rule IDs, and error codes.
- Dense retrieval for paraphrased strategy and rule questions.
- Hybrid retrieval followed by a reranker for mixed queries.

Create a versioned evaluation set and measure retrieval separately from answer quality. Include outdated rules, ambiguous questions, unanswerable questions, malicious replay text, incorrect permissions, and citations pointing to the wrong rules version.

Conversation history is not model memory; it consumes the context window because the application resends it. Keep recent turns within a token budget, summarize cautiously, and store only consented durable preferences or strategy facts with provenance, permissions, expiry, and deletion. Never use a generated summary as authoritative battlefield state.

Compare prompt engineering, RAG, and fine-tuning for the right reasons: prompts guide behavior, RAG supplies changing/private/citable facts, and fine-tuning primarily teaches repeated behavior or format. Track temperature, prompt/model version, context and output tokens, time to first token, total latency, cost, invalid output, grounding, and fallback use.

For model failure, validate structured output before execution, allow only bounded repair attempts, use timeouts and circuit breakers, and fall back to a scripted bot when safe. A second model provider is optional and must satisfy the same privacy, retention, schema, quality, and cost requirements.

## Go concepts this project should exercise

- `context.Context` propagation and cancellation from HTTP through storage and workers.
- Small consumer-owned interfaces around the store, clock, random source, and agent.
- Table-driven tests for game rules and seeded deterministic simulation tests.
- Bounded worker pools for independent battlefields or projections.
- Channels for work ownership, mutexes for small in-memory invariants, and no unowned goroutines.
- HTTP server/client timeouts, graceful shutdown, and connection-pool sizing.
- Wrapped errors with stable transport error codes.
- Race-detector, CPU, heap, goroutine, block, and mutex profiling under simulation load.
- Backpressure when agents submit work faster than ticks can process it.

A useful concurrency model is one logical command stream per battlefield, with multiple battlefields processed in parallel. Avoid one goroutine per unit until measurements show a real need; it complicates determinism and coordination.

Add deliberate small tests or benchmarks for language concepts that the architecture alone might not expose:

- Copy and append unit-ID slices to demonstrate length, capacity, reallocation, and accidental shared backing arrays.
- Compare maps with serialized ownership, `sync.Mutex`, and the specialized cases appropriate for `sync.Map`; run the race detector on an intentionally unsafe version.
- Define narrow store/clock/random-source interfaces and test the typed-nil interface trap.
- Compare pointer and value receivers on game entities and document the chosen mutation semantics.
- Use `defer` for rollback and cleanup, while avoiding deferred resource closure inside an unbounded processing loop.
- Capture goroutine, block, and mutex profiles from a deliberately leaked or blocked worker and explain why race freedom does not prove liveness.
- Configure one reusable HTTP client for the AI provider with transport reuse, connect/response timeouts, and request context.
- Load-test `database/sql` with deliberately undersized and oversized pools; observe wait counts, database saturation, leaked rows, and long transactions.

## AWS and Kubernetes evolution

### Local development

```text
Go service + PostgreSQL + optional local queue, all through Docker Compose
```

Implement the domain and failure semantics here first. OpenTelemetry can provide vendor-neutral traces, metrics, and logs.

### First AWS deployment

- Container image in Amazon ECR.
- Service on ECS/Fargate for the simplest managed container deployment, or EKS if Kubernetes practice is itself a primary goal.
- PostgreSQL on Amazon RDS with backups and point-in-time recovery.
- Application Load Balancer for ingress and health checks.
- SQS for outbox-driven asynchronous work and dead-letter handling.
- S3 for replay archives, exported snapshots, and AI evaluation datasets.
- CloudWatch and/or an OpenTelemetry collector for operational signals.
- Secrets Manager and IAM roles instead of credentials in configuration files.
- AWS WAF and API Gateway only when their edge, throttling, or API-management capabilities are actually required.

### Kubernetes learning deployment

Move the same container to a local cluster first, then EKS:

- `Deployment` for stateless API/worker processes.
- `Service` and ingress/load balancer for network exposure.
- `ConfigMap` for non-secret configuration and a managed secret integration for secrets.
- Readiness, liveness, and startup probes with distinct purposes.
- CPU/memory requests and limits based on measurements.
- Horizontal Pod Autoscaler using a useful signal such as active battlefields or queue backlog, not CPU by reflex.
- Pod disruption budget, topology spread, rolling updates, and graceful termination.
- Migration `Job` or controlled deployment step; never let every replica race to run schema changes.

Create a controlled memory-pressure exercise: distinguish a container exceeding its cgroup limit from node pressure and eviction, then compare Go heap profiles with RSS. Investigate unbounded queues/caches, retained buffers, goroutine stacks, memory outside the Go heap, allocation rate, garbage-collection behavior, and poorly chosen requests or limits before changing capacity.

Keep PostgreSQL and durable queues managed outside the cluster initially. Running them yourself in Kubernetes teaches storage operations, but it also expands the project from application learning into database-platform ownership.

## Zero-downtime changes

Use expand–migrate–contract when changing schemas or event/API contracts:

1. **Expand:** add a nullable column, new event field, or new endpoint while old and new application versions remain compatible.
2. **Migrate:** dual-write if necessary, backfill in bounded batches, switch readers, and verify data and consumer adoption.
3. **Contract:** remove old writes and fields only after no running process or external consumer depends on them.

For example, renaming `factions.resources` to `resource_balance` should be several deployments, not one destructive rename. Avoid long transactions and check whether a database DDL operation takes a table lock or rewrites data before running it in production.

## Observability and reliability targets

Every command should be traceable through API receipt, validation, tick processing, database commit, event publication, and projection. Use separate identifiers:

```text
trace_id       one technical request/trace
command_id     one logical game command
event_id       one durable event
battlefield_id one aggregate and operational partition
```

Initial signals:

- Command acceptance, rejection, conflict, and processing latency.
- Tick duration, scheduling delay, and active battlefield count.
- Queue depth, oldest message age, retries, and dead-letter count.
- Database transaction latency, lock waits, deadlocks, connections, and slow queries.
- AI decision latency, tokens, cost, invalid actions, and tool-loop limit hits.
- HTTP rate, errors, duration, saturation, and availability SLO.

Retries require a timeout, transient-error classification, exponential backoff with jitter, an attempt cap, and an idempotent operation. Load shedding is preferable to allowing unbounded command queues to exhaust memory.

### Reliability vocabulary to demonstrate

- **SLI:** the measured user-visible indicator, such as the proportion of commands processed within two ticks.
- **SLO:** an internal target, such as 99.9% of accepted commands processed within two ticks over 30 days.
- **SLA:** an external commitment that may include remedies; do not promise one merely because an SLO exists.
- **Error budget:** the unreliability permitted by the SLO, used to decide when reliability work should take priority over risky releases.
- **Backpressure:** bound command queues and slow or reject producers when tick workers cannot keep up.
- **Load shedding:** preserve active battles by rejecting lower-priority replays, analytics, or new battle creation during overload.
- **Circuit breaker:** temporarily stop AI-provider or webhook calls after sustained failure, then probe for recovery.
- **Bulkhead:** isolate worker pools, connection budgets, and concurrency by workload or tenant so one failure does not exhaust the whole service.

Build a timeout budget from the incoming command deadline inward. A dependency timeout must leave the caller enough time to handle the failure and return a controlled result. Inject latency, saturation, invalid responses, dropped acknowledgements, and dependency outages to test these behaviors.

## Optional system-design extension labs

These are not prerequisites for the game engine. They connect the generic system-design questionnaire to useful game features without forcing each one into the core service.

| Extension | Concepts exercised |
| --- | --- |
| Public command rate limiter | Token bucket or sliding window, distributed counters, bursts, per-tenant quotas, fail-open/closed decisions |
| Battle notification service | Preferences, templates, scheduling, fan-out, provider retries, deduplication, and cancellation |
| Tick/tournament scheduler | Leases, recurring jobs, ownership, duplicate execution, worker failure, and cancellation |
| Live spectator gateway | WebSocket/SSE connection ownership, ordering, presence, reconnect cursors, and regional fan-out |
| Replay storage and sharing | Multipart object upload, metadata, checksums/deduplication, permissions, versions, retention, and scanning |
| Metrics and alerting pipeline | High-cardinality labels, aggregation, retention tiers, alert evaluation, and noisy alerts |
| Player/unit search | Prefix indexes, ranking, typo tolerance, freshness, caching, and abusive-query controls |
| Signed webhooks | Subscription lifecycle, signatures, secret rotation, retry schedules, duplicate delivery, dead letters, and customer debugging |
| Audit-log service | Append-only integrity, sensitive-data minimization, tenant queries, regional retention, and export |
| Feature flags and rules rollout | Local low-latency evaluation, targeting, percentage rollout, consistency, kill switches, and audit history |

The URL-shortener prompt is intentionally left as a separate design exercise: inventing shortened replay links would add little beyond key generation, redirect caching, expiration, hot-key handling, abuse prevention, and analytics.

## Engineering-practice artifacts

Keep lightweight evidence of how the project evolves:

- Architecture decision records for choices such as ticks, storage model, queue, and ECS versus EKS.
- A threat model for player, tenant, AI-tool, and infrastructure boundaries.
- Load-test reports with hypotheses, measurements, bottlenecks, and the next change.
- Runbooks for a stuck tick worker, database saturation, queue backlog, AI-provider outage, and failed deployment.
- Blameless incident exercises and postmortems that separate root cause, contributing factors, mitigation, and prevention.
- A cost model covering steady traffic, battle spikes, AI tokens, database/storage growth, and observability retention.

These artifacts strengthen design and operational reasoning. They may support discussion of learning and personal projects, but should not be presented as professional production experience.

## Incremental roadmap

### Milestone 1 — Deterministic engine

- Model a small map, two factions, resources, units, buildings, and combat.
- Advance a seeded simulation through ticks entirely in memory.
- Add unit and property-style invariant tests: resources never become negative, ownership is valid, and replay produces the same state.

**Revision:** Go types/interfaces, testing, algorithms, determinism, and domain modeling.

### Milestone 2 — REST service

- Add battlefield resources, command submission, validation, and stable errors.
- Implement authentication placeholders, faction authorization, idempotency keys, rate limits, and trace IDs.
- Generate an OpenAPI contract and write handler/integration tests.

**Revision:** REST constraints, POST/PUT/PATCH, idempotency, HTTP semantics, security, and observability.

### Milestone 3 — PostgreSQL

- Persist battlefields, commands, results, and state.
- Add optimistic versions, transactions, constraints, and query-driven indexes.
- Compare offset and keyset pagination with `EXPLAIN (ANALYZE, BUFFERS)` on generated data.
- Deliberately reproduce lock waits and a deadlock in an integration test, then implement safe retry behavior.

**Revision:** B-trees, query planning, MVCC, isolation, locks, deadlocks, migrations, and connection pools.

### Milestone 4 — Events and workers

- Add a transactional outbox and an at-least-once publisher.
- Build idempotent projections and a dead-letter/replay path.
- Inject crashes between processing and acknowledgement to prove duplicate safety.

**Revision:** eventual consistency, delivery semantics, deduplication, backpressure, retries, and reconciliation.

### Milestone 5 — AI factions

- Establish scripted-bot baselines.
- Give an LLM a read-only observation tool and a typed command tool.
- Add budgets, authorization, deterministic validation, audit traces, and an evaluation suite.
- Test malicious observations, invalid arguments, repeated calls, timeouts, and provider failure.
- Build the read-only rules coach and compare sparse, dense, and hybrid retrieval with grounded citations.

**Revision:** agent loops, tool use/MCP, RAG, embeddings, reranking, memory, structured output, prompt injection, evaluation, latency, and cost.

### Milestone 6 — Production-shaped runtime

- Add health probes, graceful shutdown, resource bounds, load tests, dashboards, alerts, and SLOs.
- Run several service replicas and prove only one worker owns a battlefield tick at a time.
- Perform an expand–migrate–contract change while old and new application versions coexist.
- Run failure drills for saturation, a slow dependency, a deadlock victim, duplicate delivery, and exhausted error budget.

**Revision:** distributed coordination, deployments, bulkheads, circuit breakers, reliability targets, zero downtime, and incident diagnosis.

### Milestone 7 — AWS and Kubernetes

- Deploy the straightforward managed AWS version first.
- Package Kubernetes manifests or Helm/Kustomize configuration and deploy to a local cluster, then EKS if cost is justified.
- Exercise autoscaling, pod termination, pod disruption, secret rotation, restore from backup, and a failed deployment rollback.

**Revision:** IAM, VPC/networking, managed services, containers, Kubernetes scheduling/probes/scaling, disaster recovery, and cost control.

## Topics from this revision thread

Use the project to make these concepts concrete:

| Concept | Project exercise |
| --- | --- |
| REST and RESTful constraints | Resource reads plus command submission with stateless requests |
| `POST`, `PUT`, and `PATCH` | Create battlefields, replace configuration, partially edit safe settings |
| Idempotency | Retry `submit_command` without applying a move twice |
| Idempotency key vs trace ID | Preserve command identity across retries while tracing each request attempt |
| Offset vs keyset pagination | Compare admin page numbers with the live event feed |
| Database index and B-tree | Index event cursors and faction/unit lookup; inspect query plans |
| Read committed and MVCC | Observe committed snapshots and prevent lost state transitions |
| Locks and deadlocks | Serialize ticks and reproduce conflicting entity lock order |
| Expand–migrate–contract | Rename or split a live schema field across deployments |
| Eventual consistency | Build leaderboard and spectator projections asynchronously |
| At-most/at-least/exactly-once | Drop disposable metrics, retry events, deduplicate business effects |

## Decisions to make later

Do not block milestone 1 on these:

- Real-time transport: polling, server-sent events, or WebSocket.
- Tick ownership: database advisory locks, leases, or queue partitioning.
- State storage: normalized current state, snapshots plus event log, or full event sourcing.
- Map partitioning and whether one battlefield can outgrow one processor.
- AI provider and whether MCP adds value over in-process typed tools initially.
- ECS versus EKS for the first public deployment.
- Multi-region recovery and whether any command path truly needs active-active writes.

Choose these from measured scale, learning priorities, operating cost, and desired failure behavior—not from a wish to include more technologies.

## First concrete deliverable

Build one executable that can:

1. Create a seeded battlefield with two scripted factions.
2. Accept typed commands in memory.
3. Run 1,000 deterministic ticks.
4. Print the winner and a compact event history.
5. Replay the same seed and commands and prove the final state is identical.

That small vertical slice establishes the most important invariant—the game engine—before adding HTTP, databases, queues, AI, AWS, or Kubernetes.
