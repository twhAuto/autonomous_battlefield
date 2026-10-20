# Database

PostgreSQL is the system of record and the only coordination channel between the
API, the battle engine and the AI gateway. Processes never call each other to hand
over work: they claim rows, hold leases and record results in the database. This
page owns the schema, database access, coordination patterns and migration
conventions; the SQL in [`backend/migrations`](../../backend/migrations) is the
source of truth. [System architecture](system-architecture.md) describes the
runtime flows that use them; operations runbooks own backup, restore and migration
gates.

## Deployment and access

One PostgreSQL 17 instance runs as the `battlefield-postgres` StatefulSet. K3s
places its volume on the retained `battlefield-retained` storage class.

| Process | Login | Reads and writes |
| --- | --- | --- |
| API (`cmd/server`) | `DATABASE_URL` (application login) | Factions, battle creation, generation jobs, model access, Player membership; reads battles, events and decisions for inspection |
| Engine (`cmd/engine`) | `DATABASE_URL` (application login) | Battle claims, ticks and state, events, decision windows and fallbacks, expiry sweeps |
| Migration job (`cmd/server -migrate`) | `DATABASE_URL` (application login) | Schema changes; needs `CREATEROLE` the first time `00004` runs |
| AI gateway (`cmd/aigateway`) | `AI_GATEWAY_DATABASE_URL`, a login that inherits only `battlefield_ai_gateway` | Decision and generation claims, context reads, plan submission, usage and failures |
| Agent (`cmd/agent`) | None | Reaches data only through the AI gateway |

### The `battlefield_ai_gateway` role

Migration `00004` created this `NOLOGIN` group role as `battlefield_mcp`; `00017`
renamed it to `battlefield_ai_gateway`. A rename keeps the role's identity, so
logins already granted the old name keep their membership. Operators create a
separate login and grant it this role, which keeps credential lifecycle apart from
schema lifecycle. Grants (restated in full by `00017`):

| Object | Privilege | Migration |
| --- | --- | --- |
| `battle_decisions` | `SELECT`, `INSERT`, `UPDATE` | `00004` |
| `battle_controller_evaluation` | `SELECT` | `00004` |
| `faction_background_generations` | `SELECT`, `UPDATE` | `00009` |
| `runtime_policy` | `SELECT`, `UPDATE(id)` (row lock only) | `00011` |
| `battle_runs` | `SELECT(id, ai_executions)`, `UPDATE(ai_executions)` | `00011` |
| `agent_execution_leases` | `SELECT`, `INSERT`, `UPDATE`, `DELETE` | `00011` |

The role has no access to battle events, battle state, factions, membership or
policy tables beyond these columns. When `AI_GATEWAY_DATABASE_URL` is absent, the
deployment scripts use the pre-rename `MCP_DATABASE_URL` if present, and otherwise
reuse `DATABASE_URL`, which is acceptable only for local development. Production
must supply the restricted login
([deployment configuration](../operations/deployment.md)).

## Schema

| Table or view | Purpose | Key rules |
| --- | --- | --- |
| `factions` | Reusable faction identity: name, doctrine, background, owner | `ruleset_id` and `blueprint` are legacy-only. Battles snapshot faction data, so deleting a faction never changes a battle |
| `battle_runs` | One battle: scenario snapshot, initial and current state, status, lease, attempt, pause flag, result, owner, model-execution count, trace context | Status `queued`, `running`, `completed`, `failed` or `stopped`. A lease owner and expiry are both set or both null, and only while `running` |
| `battle_events` | Ordered events per battle attempt | Primary key `(battle_id, attempt, sequence)` makes inserts idempotent and serves event paging |
| `battle_decisions` | One strategy decision window: frozen observation and hash, controller and profile, lease, resolved plan, fallback source, error category, usage, provider metadata, trace context | Unique per `(battle_id, attempt, faction_id, target_tick)`. Status `pending`, `running`, `accepted`, `fallback` or `failed`. `version` guards resolution; `idempotency_key` deduplicates submissions. `faction_id` is snapshot data, not a foreign key |
| `faction_generation_accounts` | Per-actor row used as a generation lock | Locked `FOR UPDATE` while creating a job |
| `faction_background_generations` | Durable background-draft jobs | Unique `(actor_id, idempotency_key)`; a partial unique index allows one queued or running job per actor. Status `queued`, `running`, `succeeded`, `failed` or `expired` |
| `runtime_policy` | Singleton: AI stop switch, battle allowances, per-battle model budget, CLI concurrency | Row lock serializes every admission decision |
| `agent_execution_leases` | Active CLI execution slots, keyed by decision or generation ID | Rows expire with their lease and are pruned on admission |
| `model_access_policy` | Singleton administrator model-access policy | `version` rejects conflicting saves |
| `player_members` / `player_membership_sync` / `player_membership_audit` | Managed Player grants, Cloudflare Access group synchronization state, and audit history | Unique email; versioned desired and synced state |
| `battle_controller_evaluation` (view) | Per-controller decision count, fallback and invalid-plan rates, latency, tokens, plan diversity and win rate | Readable by the AI gateway role |
| `friend_ai_usage` (view) | Model usage per identity across decisions and generation jobs | Private reporting view |
| `goose_db_version` | Goose's applied-migration ledger | Production recovery records compare it before approving an image rollback |

```mermaid
erDiagram
    BATTLE_RUNS ||--o{ BATTLE_EVENTS : "records (cascade)"
    BATTLE_RUNS ||--o{ BATTLE_DECISIONS : "coordinates (cascade)"
    FACTION_GENERATION_ACCOUNTS ||--o{ FACTION_BACKGROUND_GENERATIONS : "owns"
    PLAYER_MEMBERS ||--o{ PLAYER_MEMBERSHIP_AUDIT : "audited by"

    FACTIONS {
        uuid id PK
        text name
        text doctrine
        text background
        text owner_id
    }
    BATTLE_RUNS {
        uuid id PK
        text scenario_id
        jsonb scenario_snapshot
        jsonb current_state
        text status
        int attempt
        text lease_owner
        bool paused
        text owner_id
        int ai_executions
    }
    BATTLE_EVENTS {
        uuid battle_id PK,FK
        int attempt PK
        bigint sequence PK
        bigint tick
        jsonb payload
    }
    BATTLE_DECISIONS {
        uuid id PK
        uuid battle_id FK
        int attempt
        uuid faction_id
        bigint target_tick
        text status
        bigint version
        jsonb observation
        jsonb resolved_plan
        text lease_owner
    }
    FACTION_GENERATION_ACCOUNTS {
        text actor_id PK
    }
    FACTION_BACKGROUND_GENERATIONS {
        uuid id PK
        text actor_id FK
        text status
        text runtime
        text worker_id
    }
    PLAYER_MEMBERS {
        uuid id PK
        text email
        bool desired_access
    }
    PLAYER_MEMBERSHIP_AUDIT {
        bigint id PK
        uuid member_id FK
        text action
    }
    RUNTIME_POLICY {
        bool id PK
        bool ai_enabled
        int decision_executions
        int cli_concurrency
    }
    AGENT_EXECUTION_LEASES {
        uuid id PK
        timestamptz expires_at
    }
```

`runtime_policy`, `model_access_policy` and `player_membership_sync` are
single-row tables (`id boolean PRIMARY KEY CHECK (id)`). `agent_execution_leases.id`
holds a decision or generation ID without a foreign key, because one table serves
both job types.

## Coordination patterns

### Claims and leases

Workers claim queued rows with `FOR UPDATE SKIP LOCKED`, so concurrent workers never
block on or double-claim the same row.

- **Battles:** the engine claims unpaused `queued` battles, or `running` battles
  whose lease expired, while `attempt` is below `engine.maxAttempts`, through the
  partial index `battle_runs_unpaused_claim_idx`. Each claim increments `attempt` and
  restarts from `initial_state`. Tick writes extend the lease only where status,
  attempt and lease owner still match, so a previous owner cannot write after a
  reclaim.
- **Decisions:** the AI gateway claims `model` decisions that are `pending`, or `running` with
  an expired lease, for the worker's profile IDs, and sets `status='running'` with a
  lease. The capability token issued with the claim expires at that lease deadline.
- **Generation jobs:** the AI gateway claims queued jobs by setting `status='running'`, the
  worker ID and an expiry.

### Admission triggers

PL/pgSQL triggers enforce limits inside the same transaction as the write, so every
API replica, engine and AI gateway instance sees one serialized policy:

| Trigger (function) | Fires | Effect |
| --- | --- | --- |
| `friend_battle_admission` (`admit_friend_battle`) | Before inserting a battle | Locks `runtime_policy` and rejects with SQLSTATE `P0429` when the owner reached the daily or unfinished allowance, or the global unfinished cap. Skips `legacy-admin` and `admission_exempt` (Admin) battles; retried IDs reach the unique constraint without spending an allowance |
| `gate_decision` / `gate_generation` (`gate_new_ai_job`) | Before inserting a decision or generation job | A new model decision becomes `failed` with `ai_disabled_or_budget` when AI is stopped or the battle's budget is spent, so the engine falls back at once. A new generation job raises `P0429` while AI is stopped |
| `agent_decision_admission` / `agent_generation_admission` (`admit_agent_execution`) | Before a claim sets `lease_owner` or `worker_id` | Locks `runtime_policy`. Cancels the claim (returns `NULL`) when AI is stopped or CLI slots are full, prunes expired leases, counts the battle's model execution (marking the decision `failed` with `decision_budget` past the limit), and inserts a CLI lease |
| `release_decision_execution` / `release_generation_execution` (`release_agent_execution`) | After a worker reports usage | Deletes the job's CLI lease |

Defaults (`runtime_policy`): 20 battles per person per UTC day, 2 unfinished per
person, 4 unfinished globally, 100 model executions per battle, 4 CLI slots. API
jobs do not take CLI slots. Paused battles remain `queued` or `running`, so they
still count as unfinished. Admin battles skip the allowances but count toward other
identities' global cap.

Rows created before ownership existed belong to `legacy-admin`, which bypasses the
battle allowances for migrations and trusted fixtures; application identities,
including the local Admin, cannot select that owner ID.

Background generation is admitted in application code under the actor's
`faction_generation_accounts` row lock: `ai.backgroundGeneration.dailyAllowance`
(default 10) jobs per UTC day, counting queued jobs that later expire, with one
active job per actor and same-key retries reusing the existing job.

### Wake-up notifications

Inserting a model decision runs `pg_notify('battlefield_decision_ready', profile_id)`.
The AI gateway listens on that channel to wake long-polling claims. The notification carries no
authoritative data and is safe to miss: every woken worker still claims through the
locked query, and workers claim again after each long poll ends. The engine is not notified when a plan arrives;
it reads the decision row when the window closes and falls back if no plan was
accepted.

### Idempotency and versions

- Battle events are keyed by `(battle_id, attempt, sequence)`.
- An accepted plan is written only to a `pending` or `running` decision whose
  `version` and `target_tick` match the submission, and the write increments
  `version`. Resubmitting with the same idempotency key after acceptance returns the
  same accepted result; any other later submission gets a version conflict.
- Generation jobs are unique per `(actor_id, idempotency_key)`.
- Battle creation retries with the same ID reach the primary-key constraint without
  counting against admission.

### Expiry

- **Battles:** the engine sweeps at startup and every minute. Battles older than one
  hour, including queued and paused time, become `failed` with `battle_timeout`,
  and their open decisions become `failed` with `battle_stopped`.
- **Generation jobs:** expired lazily whenever a job is created, read or claimed. A
  queued job past its expiry becomes `expired` (`queue_timeout`); a running one
  becomes `failed` (`timeout`).
- **CLI leases:** pruned on every admission and deleted when usage is reported.

## Migrations

Migrations are SQL files in [`backend/migrations`](../../backend/migrations),
embedded into the backend binary (`migrations.Files`) and applied with Goose by
`cmd/server -migrate`. Deployment runs this as the `battlefield-migrate` Job before
rolling out application workloads. `internal/postgres.NewMigrationProvider` holds a
database-wide advisory session lock, so concurrent runs and parallel tests cannot
interleave DDL.

| Migration | Change |
| --- | --- |
| `00001_create_factions.sql` | Initial faction table |
| `00002_create_battle_queue.sql` | Battle runs and per-attempt event stream |
| `00003_castle_siege_state.sql` | Ruleset/blueprint and battle state snapshots |
| `00004_battle_decisions.sql` | Decision windows, claim indexes, evaluation view, `battlefield_mcp` role |
| `00005_stop_battles.sql` | `stopped` battle status |
| `00006_pause_battles.sql` | Pause flag and unpaused claim index |
| `00007_trace_context.sql` | Persisted trace context on battles and decisions |
| `00008_faction_doctrine.sql` | Faction doctrine; tick-zero opening decisions |
| `00009_faction_background.sql` | Faction background and generation jobs |
| `00010_battle_history.sql` | Descending creation-time/ID index for battle history |
| `00011_friends_access.sql` | Ownership, admission triggers, model-execution counts, CLI leases, runtime policy, usage view |
| `00012_model_access.sql` | Versioned administrator model-access policy |
| `00013_player_members.sql` | Player membership, synchronization and audit |
| `00014_cli_concurrency.sql` | Shared CLI capacity of four with serialized admission |
| `00015_drop_duplicate_event_index.sql` | Drops an index that duplicated the `battle_events` primary key |
| `00016_admin_battle_admission.sql` | Admin battles bypass admission allowances but still occupy capacity |
| `00017_rename_ai_gateway_role.sql` | Renames `battlefield_mcp` to `battlefield_ai_gateway` and restates its grants |

Conventions:

- Add a new numbered migration for every schema change; never change the SQL of one
  that may have been applied to a shared database. Comment-only edits are safe: Goose
  does not checksum files and comments never execute.
- Start each file with a comment describing the change, and include one
  `-- +goose Up` and one `-- +goose Down` section; wrap every dollar-quoted DO block
  and function body in `StatementBegin`/`StatementEnd`. `migrations/embed_test.go`
  enforces these rules and contiguous version numbers.
- Guard data-dependent downgrades instead of silently discarding rows.
  `migrations/round_trip_integration_test.go` applies migrations one at a time,
  recording the schema after each (columns with type, nullability and default,
  indexes, views, constraints, functions and triggers), then runs each Down and
  requires exactly the previous version's schema. 00017's Down renames the
  cluster-wide role, so the earlier migrations are taken down only from 00016.
- Grant `battlefield_ai_gateway` only the columns the AI gateway needs, in the same
  migration that adds them.
- Update repository scanning, serialization and tests in the same change, and update
  this page.

The [backend guide](../developer/backend-guide.md#database-migrations-and-adapters)
covers adapter structure and migration tests. Production migrations save and
validate a database backup first. [Jenkins production delivery](../operations/jenkins.md#production-delivery)
owns the migration gate, the [local walkthrough](../developer/local-kubernetes.md#migration-gate)
shows how to inspect it, and [K3s delivery](../operations/k3s-delivery.md) covers
backup, restore and recovery records.

## Related guides

- [System architecture](system-architecture.md): runtime flows built on these tables.
- [Backend guide](../developer/backend-guide.md): repository packages and query-plan results.
- [Deployment](../operations/deployment.md) and [local Kubernetes deployment](../developer/local-kubernetes.md): delivery, migration gate and inspection.
- [K3s delivery](../operations/k3s-delivery.md): backup, restore and data recovery.
