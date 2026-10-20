# OrdinalFrontier System Architecture

OrdinalFrontier is a deterministic strategy battle simulator. The implementation
uses `autonomous-battlefield` and `battlefield` as technical identifiers.


Jump to [components](#3-runtime-components), [boundaries](#4-architectural-boundaries),
[flows](#5-core-runtime-flows), [AI decisions](#6-ai-decisions),
[persistence](#7-persistence-and-durability), [security](#8-security-and-trust-boundaries),
[deployment](#10-deployment-topology), or
[limitations](#13-current-limitations-and-follow-up-questions).

## 1. Purpose

OrdinalFrontier is a deterministic castle-siege auto-battler. Users create reusable
faction doctrines, stage a finite battle between two factions, and watch a separate
engine execute it. Each faction can use either deterministic scripted plans or a
model-backed strategic controller.

The central architectural rule is:

> Models propose bounded strategy plans. The deterministic engine validates and
> applies those plans and remains the only component that controls battlefield-state
> mutation.

This document describes the system's runtime boundaries and interactions. The
[AI decisions](ai-decisions.md) page owns the engine–model contract;
[database](database.md) owns schema and coordination rules;
[configuration model](configuration.md) owns environment files and Secret
boundaries; and [observability architecture](observability.md) owns telemetry
contracts and reliability objectives. The
[codebase guide](../developer/codebase-guide.md) explains the implementation package
by package, [contributor CI](../developer/ci.md) covers verification, and the
[server rebuild contract](../operations/server-rebuild-contract.md) lists the
infrastructure state and recovery boundaries. Operational instructions remain in
[deployment](../operations/deployment.md) and
[observability operations](../operations/observability.md).

## 2. System context

```mermaid
flowchart TB
    User[Browser user]
    UI[React frontend]
    API["Go API: cmd/server"]
    Engine["Battle engine: cmd/engine"]
    DB[(PostgreSQL)]
    Cache[(Optional Redis)]
    Gateway["AI gateway: cmd/aigateway"]
    Agent["Decision agent: cmd/agent"]
    Provider["OpenAI or Gemini API, or Codex, Antigravity or Claude CLI"]

    User --> UI
    UI -->|HTTP /v1| API
    API -->|completed battle snapshots with TTL| Cache
    API -->|factions, battle runs, cache misses| DB
    Engine -->|claim battles, persist ticks, create and resolve decisions| DB
    DB -.->|LISTEN/NOTIFY wake-up| Gateway
    Gateway -->|restricted role: decisions, generation jobs, admission| DB
    Gateway ~~~ Agent
    Agent -->|claim work, MCP tools, result metadata| Gateway
    Agent <-->|model turns or isolated CLI process| Provider
```

PostgreSQL is the durable coordination point. The API and engine do not call each
other directly, and the engine and agent do not call each other directly. Each
workload can fail or restart while preserving battle and decision state.

## 3. Runtime components

| Component | Responsibility | Direct dependencies |
| --- | --- | --- |
| React frontend | Session-aware navigation (Cloudflare Access in production, development login locally), faction creation, battle staging and history, map display, controls, event stream, and AI decision history | Public API |
| Public API | Access identity verification, role/ownership authorization, managed Player membership, HTTP validation, scenario resolution, faction and battle persistence, and selectable model-profile catalog | PostgreSQL, embedded scenario/ruleset/content YAML, optional Redis, optional Cloudflare Access group API |
| Battle engine | Battle queue claims, deterministic ticks, strategy windows, state/event persistence | PostgreSQL |
| AI gateway | MCP tool server with request-scoped battle tools (`get_decision_context`, `get_unit_details`, `submit_strategy_plan`) and background-generation tools, plus the agent dispatch API (claims, capability minting, result reports) | PostgreSQL |
| Decision agent | Model-decision claims and API or CLI model execution | AI gateway, model provider |
| Redis (optional) | Disposable completed battle read cache; no engine or readiness dependency | Memory, API-only connection |
| PostgreSQL | Factions, battle queue, event history, snapshots, leases, and model decisions | Persistent storage |
| Scenario catalog | Authored map, timing, fallback plans, unit/building rules, and combat rules, resolved from versioned [content libraries](../developer/content-library.md) | Embedded in the server binary (`backend/gamedata`), validated at API startup and frozen into battle rows |

The backend commands are `cmd/server`, `cmd/engine`, `cmd/aigateway`, `cmd/agent`,
and `cmd/aieval`. The first four are runtime processes; `aieval` runs deterministic
offline cases or explicitly enabled live comparisons and produces reports.

### Why four backend processes?

- The API remains responsive while battles and model calls take time.
- The engine can restart or scale without exposing gameplay HTTP routes.
- Provider credentials and model behavior remain outside the deterministic simulation process.
- The AI gateway can receive narrower database permissions than the API or engine.
- Local subscription CLIs can run on a developer host without copying login
  credentials into Kubernetes.

## 4. Architectural boundaries

### Source ownership

See the [backend guide](../developer/backend-guide.md#backend-organization) for package
boundaries, the [frontend guide](../developer/frontend-guide.md) for browser ownership,
and the [codebase guide](../developer/codebase-guide.md#executable-entry-points) for launchers.

### Browser delivery

The frontend is a static build served by Nginx. Nginx requires revalidation of the
SPA entry document after rollouts and returns 404 for missing hashed assets instead
of routing those requests to HTML. Every Nginx response carries
`frame-ancestors 'none'` CSP, `X-Frame-Options: DENY`, `nosniff`, a strict-origin
referrer policy and HSTS, so the admin and gameplay UI cannot be framed for
clickjacking. The CSP deliberately leaves script and connect sources open because
`VITE_API_URL` may name a separate API origin.

### Battle inspection

The browser presents a battle from one selected perspective, but the perspective
selector controls presentation only: server-side role and ownership checks remain
the access boundary, and faction views are projected on the server. Participant
background and doctrine come from the persisted scenario, not the editable faction
directory. The presentation accepts multiple participants; battle creation,
scenario spawns, opponent selection, and castle-destruction victory rules still
require two factions.

See the [frontend guide](../developer/frontend-guide.md#battle-data-lifecycle) for
request lifecycle, inspector state, map layout, and animation;
the [OpenAPI contract](openapi.yaml) for state, event and context fields, and the
[HTTP API reference](api.md#battle-state-and-fog-of-war) for fog-of-war projection;
[saved decision context](ai-decisions.md#saved-decision-context) for what the
inspector can show of a model's input; and [game rules](../game/game-rules.md) for
movement and attack resolution.

### Deterministic simulation boundary

The simulation receives a resolved scenario and optional plans, then produces
deterministic state transitions and events. It does not access PostgreSQL, HTTP,
environment variables, or model providers.

The engine owns orchestration around this pure domain: leases, retries, pauses,
decision deadlines, persistence, and shutdown handling. Movement and attack
targeting use authoritative engine state; faction sight filters AI observations
rather than controlling these engine paths.

### Public application boundary

Production requires Cloudflare Access on the whole application hostname through a
named Tunnel. The API independently verifies the Access JWT and maps issuer/subject
identity and verified email to a role on every request. Privileged roles remain
configured; optionally managed Player membership is read from PostgreSQL. No legacy
password/cookie fallback is accepted. Explicit local-password and
disabled-development modes remain development-only. Exact-origin mutation checks and
a deny-by-default route permission registry wrap every application API. The browser
never connects to the engine, AI gateway or PostgreSQL. See
[production access](../operations/access.md).

### Model boundary

The model never receives a repository, global game state, or mutation function. It can only:

1. read one authorized faction-visible decision context;
2. look up frozen reference details for units in that context;
3. submit one validated strategy plan for that decision.

### Durable coordination boundary

Battle work and model work are both durable PostgreSQL state machines. Notifications
and long-polling improve latency but are not correctness dependencies.

## 5. Core runtime flows

### 5.1 Create and run a battle

```mermaid
sequenceDiagram
    actor User
    participant UI as React frontend
    participant API as Public API
    participant DB as PostgreSQL
    participant Engine as Battle engine
    participant Match as Game library (internal/game)

    User->>UI: Select factions and controllers
    UI->>API: POST /v1/battles
    API->>API: Validate IDs, profiles, and tick limit
    API->>API: Resolve ruleset + scenario + faction snapshots
    API->>Match: game.NewMatch (in-process)
    Match-->>API: Validated initial state
    API->>DB: Insert queued battle_run
    API-->>UI: 202 Accepted
    Engine->>DB: Claim with FOR UPDATE SKIP LOCKED
    DB-->>Engine: Scenario snapshot + lease attempt
    Engine->>Match: game.NewMatch from snapshot
    loop Each tick
        Engine->>Match: Advance or AdvanceWithPlans
        Match-->>Engine: State transition + events
        Engine->>DB: Save state/events and heartbeat lease
    end
    Engine->>DB: Atomically store result and final events
    UI->>API: Poll battle, events, and decisions
    API->>DB: Read durable state
    API-->>UI: Current presentation data
```

The game library is the deterministic `internal/game` package compiled into the
API and engine binaries, not a separate service. The API builds the match once to
validate the request and store its initial state; only the engine advances it.

Battle execution is asynchronous from the creation request. Each battle stores a
complete resolved scenario and initial state, so later edits to factions or catalog
YAML cannot change an existing run.

### 5.2 Strategy decision window

A decision window occurs immediately before ticks configured by
`decisionIntervalTicks`. The supplied scenario currently uses ticks 10, 20, 30, and
so on.

```mermaid
sequenceDiagram
    participant Engine as Battle engine
    participant Match as Game library (internal/game)
    participant DB as PostgreSQL
    participant Gateway as AI gateway
    participant Agent as Agent worker + runner
    participant Model as Model provider / CLI

    Note over Gateway,Agent: Before the window: each agent slot holds an open claim
    Agent->>Gateway: POST /internal/agent/claim (long poll, waits up to 5 s)
    Gateway->>DB: Claim query finds nothing, so the request is held

    Note over Engine,DB: 1. Engine opens the window
    Engine->>Match: DecisionSnapshot(faction)
    Match-->>Engine: Visible view + economy + legal choices
    Engine->>DB: INSERT pending row + pg_notify, in one statement
    DB-->>Gateway: Notification after commit (no data, only "look now")

    Note over DB,Agent: 2. Gateway claims the row for the waiting agent
    Gateway->>DB: FOR UPDATE SKIP LOCKED: pending to running + lease
    Gateway-->>Agent: Answer the held claim: job + request capability token

    Note over Gateway,Model: 3. Model works only through the gateway's MCP tools
    Agent->>Model: Start bounded tool loop
    Model->>Agent: get_decision_context
    Agent->>Gateway: MCP call with capability token
    Gateway->>DB: Read frozen observation
    Gateway-->>Agent: Faction-visible context
    Agent-->>Model: Tool result
    opt Unit details needed
        Model->>Agent: get_unit_details
        Agent->>Gateway: MCP call with capability token
        Gateway-->>Agent: Frozen base stats and guidance
        Agent-->>Model: Tool result
    end
    Model->>Agent: submit_strategy_plan
    Agent->>Gateway: MCP call with capability token
    Gateway->>Gateway: Validate request, version, tick, plan, units and sites
    Gateway->>DB: UPDATE to accepted if the version still matches
    Gateway-->>Agent: Structured result
    Agent->>Gateway: Report usage or failure

    Note over Engine,DB: 4. Engine collects plans (nobody notifies it)
    loop Every 100 ms until no row is pending or running, or the deadline passes
        Engine->>DB: Count unresolved rows in the window + heartbeat lease
    end
    Engine->>DB: Read the window, write a fallback for unresolved rows
    Engine->>Match: AdvanceWithPlans(resolved plans)
```

How the plan travels:

1. **Agents wait first.** Each agent concurrency slot keeps one claim request open
   at the gateway. The gateway holds it for up to the agent's wait (5 seconds by
   default, 20 at most) and then answers `204`; the agent reissues it at once.
2. **The engine writes the row.** The same statement that inserts a pending model
   decision calls `pg_notify('battlefield_decision_ready', ...)`, so PostgreSQL
   delivers the notification only after the row is committed.
3. **The gateway claims it.** Its `LISTEN` connection wakes every held claim
   request. Each runs the locked claim query; `SKIP LOCKED` gives each row to one
   request, and the rest keep waiting. The winner's response is the job itself, with
   a capability token valid only for that decision until its lease ends.
4. **The model submits through the gateway.** The runner (an API tool loop or a CLI
   configured with the gateway's MCP URL) calls the tools with the token. The
   gateway validates the plan and sets the row to `accepted`.
5. **The engine reads the row.** The engine is never called. It counts the
   window's unresolved rows every 100 ms (`battle_decisions_window_idx`, one query
   for all factions) until none remain or `ai.decisionTimeout` (2 minutes by
   default) passes, then reads the window once and falls back for anything
   unresolved.

The engine, gateway and agent never call each other about a decision; they share
only its row. The notification only saves latency: if it is lost, the next claim
request finds the row within one long poll. A plan arriving at the deadline races
the fallback, and the `version` check lets exactly one write win (see
[timeout and fallback](#53-timeout-and-fallback)).

The engine creates a decision row for every faction:

- A `scripted` controller submits its deterministic baseline immediately through the
  same validation service.
- A `model` controller remains pending until an agent claims and resolves it.

Using the same durable record shape supports consistent history and
scripted-versus-model evaluation.

The first worker transition is an opening decision at tick zero, using the same
durable records, deadlines, and attempt/requeue semantics. The worker persists the
opening state and events through `SaveTick` before tick one, and the persisted
`openingComplete` flag prevents repeated spending.

### 5.3 Timeout and fallback

The battle must make progress even if a model does not answer.

At the decision deadline:

- the tick-zero opening uses a deterministic sorted-roster purchase fallback;
- the first in-battle production window uses the scenario's scripted baseline;
- later windows reuse the faction's previously active plan;
- the fallback is stored with status `fallback`, keeping the error category of a
  runner that already failed, or `timeout` when the request was still unresolved;
- a racing model submission and fallback are resolved by version and status
  predicates, so only one wins;
- the stored plan is applied exactly like an accepted model plan.

### 5.4 Pause, resume, and stop

- Pausing a queued battle prevents it from being claimed.
- Pausing a running battle retains and heartbeats its lease.
- Paused time extends the current decision and tick deadlines.
- Resuming continues the same in-memory attempt.
- Stopping releases the battle lease and fails outstanding decisions with `battle_stopped`.
- A stopped or reclaimed battle prevents the previous engine owner from making
  further guarded writes.

### 5.5 Battle expiry

Unfinished battles expire one hour after creation, including queued and paused
time. The engine sweeps at startup and every minute independently of active
battle processing. It atomically marks expired battles failed with
`battle_timeout`, clears their leases, and cancels pending/running decisions
using the existing `battle_stopped` cancellation category. History and tick
state remain available; active admission slots are released but daily usage is
not refunded. While the engine is offline, expiry waits for restart.

## 6. AI decisions

Each faction's controller is either scripted or a model profile. For a model, the
decision agent claims the durable decision, runs an API tool loop or an isolated
subscription CLI against the AI gateway's MCP tools, and the gateway validates and
records one submitted plan. The engine applies whatever the decision resolved to,
accepted or fallback, exactly like a scripted plan. Background drafting for
faction profiles reuses the same workers, runners and capability model with a
separate tool set.

[AI decisions](ai-decisions.md) owns this contract: runners and CLI isolation,
prompt provenance and faction memory, the decision context and unit knowledge, plan
validation, fallback error categories, and background generation.

## 7. Persistence and durability

PostgreSQL schema, access and coordination rules are in the [database guide](database.md).
This section covers the completed-result cache, battle retries and history reads.

### Completed battle result caching

The API can check an optional Redis cache before reading a battle from PostgreSQL.
Only a `completed` battle with a result is cached, as a snapshot of its final state
and scenario; faction views are projected from it after retrieval, authentication
runs first, and events and decision history are always PostgreSQL reads. A miss,
invalid entry or Redis error falls back to PostgreSQL, and Redis is excluded from
readiness, so an outage never fails a battle read. The engine and AI gateway never
use Redis.

Completed battles cannot be changed by any application command, so the cache needs
no invalidation. Any future result edit, battle deletion or reuse, or restart of a
completed battle must add invalidation or versioning first, and an incompatible
snapshot change must bump the key namespace. After restoring PostgreSQL, clear the
cache. Use a separate Redis database per environment; see
[Redis operations](../operations/redis.md) for keys, TTL and setup.

### Battle retries

Every mutating battle write includes battle ID, attempt, and lease owner. A
mismatched write fails with lease loss.

A reclaimed battle starts a new deterministic attempt at tick 0 from
`initial_state`; it does not resume the last persisted tick. Events from earlier
attempts remain available by attempt number. When an observation hash matches a
previously resolved decision for the same faction and target tick, the decision
repository can reuse that plan.

Each claim increments `attempt`, including a claim after a graceful-shutdown
requeue (the requeue itself preserves the count). Once a queued or lease-expired
battle has used `engine.maxAttempts` (default 3), the next claim pass marks it
`failed` with `attempts_exhausted` instead of starting another attempt, so repeated
rolling restarts during one battle can exhaust its retry budget. A stored scenario
that cannot construct a match fails immediately with `invalid_scenario` and is not
retried. Together with `battle_timeout` (see [battle expiry](#55-battle-expiry)),
these are the engine's terminal battle error codes.

### Engine capacity and recovery

Each engine worker processes one battle synchronously before claiming another;
the application manifest defaults to one engine replica, so **only one battle runs
at a time** and other admitted battles queue. While it runs, the engine also holds
it through every decision wait (up to the decision timeout) and through any pause:
a paused running battle keeps its lease and blocks the engine until resumed or
stopped. Paused battles that are still queued are skipped at claim.

The default admission limit is four unfinished battles globally, and queued time
counts toward the one-hour battle expiry. Queue delay therefore depends on battle
duration, decision waits, pauses and available engine replicas; admission does not
guarantee that a battle finishes before expiry.

Claims use `FOR UPDATE SKIP LOCKED` and leases, so more engine replicas run more
battles in parallel without code changes. Each running battle adds its own state
writes, heartbeats and decision polls to PostgreSQL, and model battles share the
agent's concurrency slots, so those become the next limits.

Crash recovery has two gates: the previous lease must expire (30 seconds by
default), and an available worker must make its next claim. Claims select eligible
battles by creation time and ID. An engine already processing another battle
does not interrupt it to reclaim an orphan, so recovery can take longer than the
lease duration.

Database errors returned by the claim operation terminate the worker loop and
cause the engine process to exit with an error; Kubernetes restarts the container.
A database interruption that reaches this path can therefore trigger lease
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
[engine entry point](../../backend/cmd/engine/main.go).

### Battle history reads

`GET /v1/battles` projects participant identities and controllers from the
immutable scenario snapshot in a single query, with lifecycle timestamps,
progress, status and a compact outcome. It does not load simulation states or use
the completed-result cache. Keyset pagination on `battle_runs_history_idx`
(`created_at DESC, id DESC`) fetches one extra row to detect the next page; status
filters read current values, so membership may change between requests.

## 8. Security and trust boundaries

### Access identity, roles and owners

Production API manifests require `BATTLEFIELD_ENV=production`; startup accepts only
`BATTLEFIELD_AUTH_MODE=cloudflare` with valid issuer (`BATTLEFIELD_ACCESS_ISSUER`),
audience (`BATTLEFIELD_ACCESS_AUDIENCE`), HTTPS origin (`BATTLEFIELD_PUBLIC_ORIGIN`)
and nonconflicting email/role assignments (`BATTLEFIELD_ACCESS_ROLES`). JWT
verification uses RS256, a fixed issuer's JWKS with one-hour key cache and a
30-second refresh throttle, and required identity/
time/audience checks. The audience claim accepts a single string or an array of
strings (RFC 7519 section 4.1.3), matched exactly and case-sensitively to the
configured audience. Invalid types and arrays with non-string entries are rejected.
Missing or unverifiable credentials deny access, with a generic response and a
bounded `auth_failure` reason in the server log
([diagnosis](../operations/access.md#diagnose-an-origin-401)). Access owns
sessions and logout; the application issues no production auth cookie, and logout
sends the browser to `/cdn-cgi/access/logout`.

Players, Developers and Admins share gameplay/faction reads. Mutation ownership is
stored in PostgreSQL and taken from the authenticated request, never client JSON.
Legacy rows belong to `legacy-admin`. Only Admin can manage another owner's rows.
Decision history/context require Developer/Admin. Player gameplay projections run
after shared cache retrieval and omit model diagnostics, raw prompts and internal
errors. The frontend hides inspection panels and stops their requests for Players.
Cloudflare Access admission and MFA are independent deployment requirements. For
static roles, user removal requires Access rule removal, session revocation,
application role removal,
and deployment to every replica; offline JWT verification alone cannot see revocation.
Managed Player removal instead denies each new API request from PostgreSQL immediately
and asynchronously removes the email from a dedicated Access group. It does not
revoke sessions in unrelated Cloudflare applications.

The local password login retains signed HttpOnly/SameSite cookies for development.
Missing auth mode and enable flags fail startup; explicit legacy enable flags remain
supported only in development. Local cookie logout does not revoke copied tokens.

### Managed Player invitations

Optional Admin → Members routes persist Player grants, removals and audit events in
PostgreSQL (`player_members`, `player_membership_sync` and
`player_membership_audit`). The singleton imports configured Players once, retaining
removed-member tombstones across restarts. Every initialization also durably
revokes and audits prior Player invitations for configured privileged identities,
so subsequent privileged-role removal cannot resurrect Player access.
Admin/Developer roles and CI identities continue through their existing
authentication paths; membership cannot elevate roles.
After signature/issuer/audience/time validation, Player authentication reads current
desired access and confirmed membership version with a two-second deadline and no
positive cache. Lookup failure denies access; `issuer|subject` ownership is unchanged.

The API process starts a cancellable reconciler when `BATTLEFIELD_MANAGED_PLAYERS`
is enabled. A transaction advisory lock serializes remote writes across replicas;
repeatable-read transactions reject concurrent edits before confirming a grant.
Mutations lock only the singleton desired-state row, not the remote worker lock,
so removals commit while a Cloudflare call is in flight. A successful PUT confirms
current versions; failures persist bounded retry backoff. Restart recovery and
one-minute full-list reconciliation repair ambiguous writes and dedicated-group drift.

The API alone receives `battlefield-members`; its token can manage Access groups
within the configured account, while code only calls the configured group endpoint.
The browser sees emails, access/sync status and a game URL, never the token. Empty
membership sends an explicitly deny-all group. No policy edits or account-wide
session revocation are automated. New grants stay inactive until confirmed; removals
block subsequent API requests immediately, without cancelling already accepted work.
See [Player invitations](../operations/player-invitations.md) for activation, policy
separation, credential rotation, rollback limitations and live acceptance checks.

### Model access and role preview

The administrator-only `/admin` control center uses `GET /v1/admin/model-access` and
`PUT /v1/admin/model-access` to manage provider and individual profile access,
including allowed roles, stored in a singleton PostgreSQL policy row. Version checks
reject conflicting saves with HTTP 409. Each API
selection reads the durable policy with a bounded timeout, so replicas and restarts
share settings; read failures make models unavailable. The policy can restrict
access but cannot activate missing credentials or deployment-disabled runtimes.
Role overrides supersede configured `allowedRoles`; an explicit empty role list
denies everyone. Players can select API or CLI profiles only when explicitly included
in the profile's allowed roles; default roles remain Admin and Developer. Disables
apply to new battle selection and background-generation admission; already accepted jobs
and frozen controller snapshots continue unchanged. This is not an emergency stop.

Role preview keeps the administrator's authenticated session and identity while
applying Player or Developer read permissions. The browser sends
`X-Battlefield-Preview-Role`; authentication validates the real administrator before
preview middleware substitutes the effective role ahead of route authorization.
Both transport and server reject mutations during preview, and the browser clears
privileged cached views when the role changes. Logout or session expiry also ends
preview. This previews the administrator's own ownership, not another
person's account. Role preview does not invite users, fabricate audit events, or
provide budget controls.

### Model-facing capability

The AI gateway mints each model session an HMAC-signed token containing one request
ID and expiry. Workers cannot mint capabilities and never receive the signing
secret. The gateway's MCP endpoints verify the signature, expiry, and equality
between the authorized and supplied request IDs.

Tool bodies are bounded, plan fields are validated, and model-provided state is never authoritative.

### Agent control plane

`/internal/agent/claim`, `/fail`, `/usage`, `/generation-finish` and `/enabled`
require a distinct worker bearer secret of at least 32 bytes, compared in constant
time. The AI gateway refuses to start, and denies worker authentication, when it is missing,
shorter or equal to the signing secret. Every worker placement (the K3s API
worker and the CLI worker VM) currently shares this one secret. Claims return
server-minted, request-scoped capabilities; workers never receive the signing key.

The AI gateway is private to the worker network. Production subscription CLI workers run in
an isolated VM and reach it through an authenticated private bridge. Their
provider egress is allowlisted; provider login state stays inside the worker
boundary. Development bridges bind to loopback. Workers poll the durable operator
AI switch and cancel work on stop or loss of policy connectivity. The
[worker operations guide](../operations/cli-worker-verification.md) owns addresses,
proxy settings, firewall rules and service installation.

### Production automation identity

Cloudflare signs service-authentication JWTs for the enrolled
`BATTLEFIELD_ACCESS_CI_CLIENT_ID`. After normal issuer, audience, expiry and
signature validation, an exact `common_name` match with `type: app` and empty
user subject/email produces a separate `ci` actor limited to GET session and
scenario-catalog routes. It cannot control
battles or use admin/inspection routes. Human roles and direct-origin denial
remain independent. The application receives the client ID, while the client
secret stays with the verification tooling. Credential setup and rotation live
in the [service authentication runbook](../operations/access.md#ci-service-identity).

### Runtime security requirements

- Every binary validates `BATTLEFIELD_ENV` (unset, `dev` or `production`) at
  startup, so a misspelt value cannot disable production-only checks.
- The AI gateway requires a capability secret and a worker secret of at least 32
  bytes each, different from each other; the agent requires the same worker secret.
  Secrets copied unchanged from `.env.example` (`replace-with-…`) and the retired
  public development capability secret are rejected, as they are for the admin
  password and session secret. Deployment validation of `.env` rejects those worker
  and capability values too, so a deployment fails before it changes the cluster.
- In production, the API server and the agent refuse to start with an offered CLI
  profile unless `BATTLEFIELD_CLI_ISOLATION_VERIFIED=true`. They decide whether a
  profile is offered with the same availability check the profile catalog uses.
- Production sessions use Access identity; direct-origin access is denied and
  network policy restricts private services.
- Provider credentials belong to the decision agent, separate from the API and engine.
- Model rationale remains untrusted display text; bounded length does not make it authoritative.
- CLI isolation depends on the configured, validated provider version family.

The [production access runbook](../operations/access.md) owns credential
provisioning, network verification and enablement procedures.

## 9. Configuration and profile availability

The shared YAML configuration defines HTTP, database, API, authentication, engine
and AI settings, and each process loads only the sections it needs; the API fails
startup when enabled secrets are missing or too short. The
[configuration model](configuration.md) owns where each setting lives and how
deployment renders `.env` into Kubernetes Secrets.

A model profile specifies:

- stable ID and display name;
- provider and API/CLI runtime;
- cluster or local scope;
- API credential/model environment variables or a pinned CLI model identifier;
- optional external-agent availability environment variable;
- token and step limits;
- enabled state and supported CLI version prefix.

Key runtime settings and their defaults:

| Setting | Default | Effect |
| --- | --- | --- |
| `api.maxTicks` | 10000 (maximum) | Upper bound for a battle's requested tick limit |
| `engine.pollInterval` | 1s | Delay between empty battle-queue claims |
| `engine.leaseDuration` | 30s | Battle lease length; an expired lease lets another engine reclaim the battle |
| `engine.heartbeatInterval` | 10s | Lease renewal cadence; must be shorter than `leaseDuration` |
| `engine.maxAttempts` | 3 | Claims per battle before `attempts_exhausted` |
| `engine.shutdownTimeout` | 10s | Bound for final writes and requeue during shutdown |
| `ai.decisionTimeout` | 2m | Engine decision deadline and agent execution timeout |
| `ai.agentConcurrency` | 1 | Execution slots per agent process (1–4) |
| `resultCache.ttl` / `resultCache.timeout` | 1h / 100ms | Completed-battle cache entry lifetime and per-operation deadline |

`backend/config.yaml` is the reference for all keys; unknown keys fail startup.

The API catalog advertises profiles users may select. In cluster mode, an operator
flag can advertise a host-connected CLI even though the API pod cannot execute it.
An agent advertises only profiles it can execute, and its claims include those IDs
so heterogeneous agents do not lease incompatible work. The worker ID and profile
list are self-reported: this is a scheduling filter, not an authorization
boundary, because the AI gateway does not bind the worker secret to a placement or
profile set.

## 10. Deployment topology

Kubernetes hosts the runtime processes; battle rules and database coordination do
not depend on Kubernetes APIs. Local development uses Kind. Production runs K3s
in a dedicated VM, with a separate VM for subscription CLI workers. Development
and production have distinct clusters, databases, volumes and credentials.

| Workload | Communication and state |
| --- | --- |
| Frontend | Serves the browser application, which calls the API through the shared gateway; no durable game state. |
| API | Reads and writes PostgreSQL, optionally caches completed results in Redis, and exposes authenticated HTTP routes. |
| Engine | Claims work and persists ticks through PostgreSQL; exposes health and metrics rather than gameplay routes. |
| AI gateway | Exposes private MCP decision tools and agent dispatch routes, with narrower database privileges than the API or engine. |
| API-provider agent | Calls the AI gateway and external model APIs; provider keys stay in this workload. |
| Migration Job | Applies database migrations separately from long-running application workloads. |

Public production traffic passes through Cloudflare Access and the tunnel to the
private gateway. Network policies keep AI gateway and database access private while
allowing the required workload and monitoring connections. CLI workers use a
private AI gateway bridge and restricted provider proxy; subscription credentials stay
outside the application cluster.

PostgreSQL is the durable game-state store. Persistent volumes retain database
and supported observability data across pod replacement; Redis remains a
disposable cache. Retained storage does not provide whole-host disaster recovery.
The application and worker VMs share a physical host, so their process/network
isolation does not eliminate the common host failure domain.

Application configuration is separated into workload-specific Secrets and a
non-secret ConfigMap. Deployment identity stays outside application Secrets;
see the [configuration model](configuration.md). Production image admission
requires verified image provenance. Registry publication, AI build manifests,
controller provisioning, migrations and rollback procedures belong to
[deployment operations](../operations/deployment.md),
[K3s delivery](../operations/k3s-delivery.md), and the
[supply-chain runbook](../operations/supply-chain-runbook.md).
The image-verification admission webhook fails closed on missing or invalid
evidence; see [admission verification](../operations/supply-chain-runbook.md#admission-verification)
for the configured retrieval timeout.

## 11. Observability

Development Kind and production K3s run the same observability stack, and the
[observability architecture](observability.md) owns its detail:

- **Logs:** structured JSON with stable `event` identifiers, request IDs and
  actor attribution, shipped by Fluent Bit to Elasticsearch and Kibana. Full
  combat detail stays in `battle_events`, not process logs.
- **Metrics:** the API and engine expose Prometheus metrics; Prometheus also
  scrapes kube-state-metrics and kubelet cAdvisor, and Grafana shows dashboards
  and checked-in alert rules.
- **Traces:** OpenTelemetry spans from all four processes go through the collector
  to Tempo, linked across battle creation, engine attempts and decisions. AI
  observations can also go to an optional self-hosted Langfuse.
- **Health:** every process exposes health endpoints; the agent also reports drain
  state.

Every `/v1` error's `requestId` equals the `X-Request-ID` header and the
`request_id` log field, so a client-reported failure maps to one log line (see
[API errors](api.md#errors)).

## 12. Decisions and trade-offs

### Determinism over direct model control

Models submit strategy and production priorities rather than state mutations. This
preserves replayability, legal-action enforcement, testing, and controller
comparison.

### Durable database coordination over in-memory queues

Battle and decision work survives restarts. Leases and `SKIP LOCKED` support
competing workers without another queue system, at the cost of additional PostgreSQL
state-machine logic.

### Frozen snapshots over live catalog references

Rules, factions, and initial state are captured per battle. Replays remain stable,
at the cost of larger JSONB rows and explicit version provenance.

### MCP as a capability boundary

The same small, auditable MCP tool surface supports API and subscription CLI models.
The AI gateway also hosts the agent dispatch API, so one process holds the
restricted database role and the capability signing secret. The trade-off is another
service and authentication boundary.

### Polling with advisory wake-ups

The UI polls the API, the engine polls battle rows, and agents long-poll the AI
gateway. PostgreSQL notifications reduce model-claim latency without becoming a
correctness dependency. Higher scale may justify push delivery or a dedicated
broker.

## 13. Current limitations and follow-up questions

- Production identity is delegated entirely to Cloudflare Access, so Access rule
  removal and session revocation are deployment responsibilities that offline JWT
  verification cannot observe. The development password login remains
  single-administrator and is not player identity.
- Public battle updates use polling rather than push delivery.
- Load and failover behavior needs testing before increasing engine, AI gateway, or agent replicas.
- Antigravity decisions prefetch the context into the prompt and refuse contexts
  over 64 KiB, because the CLI offloads large tool results to files it cannot read.
  Large late-game contexts therefore fall back for that profile.
- All worker placements share one control-plane secret, and claims self-report their
  profile IDs. A compromised worker could claim another placement's profiles. The
  secret cannot be rotated without changing every holder at once; the CLI VM's copy
  is replaced through the managed
  [`battlefield-platform/cli`](../operations/cli-worker.md) job.
- Rigorous controller experiments may need experiment identity, model parameters,
  and cost normalization beyond the current evaluation view.
