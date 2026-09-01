# Autonomous Battlefield

Autonomous Battlefield is a Go backend for an auto-battler and expansion game. The long-term product will let human players and AI agents control factions that gather resources, expand territory, and fight on a continuously running battlefield.

The project is in an early stage. The current backend provides PostgreSQL-backed faction and battle APIs, a separate queued battle engine, deterministic castle-siege combat on an authored hex map, point-buy faction blueprints, autonomous economies, fog-of-war views, production-window decisions, provider-neutral model controllers over MCP, an optional single-admin login, validated configuration, health endpoints, and graceful shutdown. Player commands, multi-user authorization, and seeded map generation described in the [revision guide](REVISION.md) remain planned work.

## Current capabilities

- Create reusable faction blueprints under a versioned point-buy ruleset.
- Retrieve a faction's name by ID.
- Store factions in PostgreSQL through `pgx`.
- Configure HTTP and database connection settings from YAML and `DATABASE_URL`.
- Apply pending schema changes through a migration-only server mode.
- Shut down the HTTP server gracefully on `SIGINT` or `SIGTERM`.
- Rank valid faction actions with an in-memory deterministic utility controller.
- Schedule configurable strategy-decision windows and use a safe fallback plan between them.
- Run OpenAI or Gemini API profiles through a shared MCP tool loop, with optional local Codex and Gemini CLI subscription profiles.
- Queue finite battles durably in PostgreSQL and claim them safely across engine workers.
- Run deterministic hex movement, economy, recruitment, castle combat, and persist replay state, statistics, and paginated events.
- Protect `/v1` application routes with an explicitly enabled, environment-backed administrator login and signed HttpOnly session cookie.
- Exercise HTTP delivery, faction and battle use cases, PostgreSQL adapters, deterministic combat, workers, scenarios, configuration, and JSON helpers with tests.

## Requirements

- Go 1.26.5 or a compatible newer version.
- Docker with Docker Compose for the local PostgreSQL instance.

For the planned path from local Kubernetes to Amazon EKS, including the
Kustomize-versus-Helm decision and the first local cluster exercise, see the
[deployment learning guide](DEPLOYMENT.md).

## Setup paths

Choose the guide that matches the task:

| Goal | Entry point |
| --- | --- |
| Run the application while coding | `./run-dev.sh` and [Run locally](#run-locally) |
| Quickly redeploy the backend and frontend to an existing local cluster | `./deploy-local.sh` |
| Build and deploy to local Kubernetes | [DEPLOYMENT.md](DEPLOYMENT.md#deploy-the-application-locally) |
| Inspect metrics, logs, Grafana, Kibana, and reliability objectives | [OBSERVABILITY.md](OBSERVABILITY.md) |
| Understand runtime components and AI decision flow | [System architecture](docs/design/system-architecture.md) |
| Navigate packages, files, and common code changes | [Codebase guide](docs/design/codebase-guide.md) |
| Resume kind after restarting the computer | [DEPLOYMENT.md](DEPLOYMENT.md#resume-the-cluster-after-restarting-the-computer) |
| Install and configure Jenkins, GitHub, and SonarQube | [JENKINS.md](JENKINS.md) |
| Start the existing CI/CD services after reboot | [JENKINS.md](JENKINS.md#daily-startup-after-a-reboot) |
| Understand the planned architecture and milestones | [REVISION.md](REVISION.md) |

## Run locally

Start PostgreSQL, apply migrations, and run the API, battle engine, and frontend
with one command from the project root:

```bash
./run-dev.sh
```

Open `http://localhost:5173`. Press Ctrl+C in the launcher terminal to stop the
application processes. PostgreSQL remains running so its local data is
preserved; stop it with `docker compose -f backend/compose.yaml stop postgres`.

Authentication is disabled unless `BATTLEFIELD_AUTH_ENABLED=true`. To test the
starter login locally without writing its secrets to Git, export them in the
terminal that starts the application:

```bash
export BATTLEFIELD_AUTH_ENABLED=true
export BATTLEFIELD_ADMIN_USERNAME=admin
read -rsp 'Battlefield admin password: ' BATTLEFIELD_ADMIN_PASSWORD && echo
export BATTLEFIELD_ADMIN_PASSWORD
export BATTLEFIELD_SESSION_SECRET="$(openssl rand -hex 32)"
export BATTLEFIELD_AUTH_COOKIE_SECURE=false
./run-dev.sh
```

Use `BATTLEFIELD_AUTH_COOKIE_SECURE=true` for an HTTPS tunnel or deployment.
The signing secret must be shared by every API replica and remain stable while
sessions should remain valid. This single-admin control is suitable for a
temporary private demo; it is not player identity or faction authorization.

The script uses `DATABASE_URL` and `ENGINE_WORKER_ID` when they are already set.
Set `START_POSTGRES=0` to use an existing PostgreSQL instance without starting
the Compose service:

```bash
START_POSTGRES=0 DATABASE_URL='postgres://user:password@localhost:5432/battlefield?sslmode=disable' ./run-dev.sh
```

Set `START_AGENT=1` to also start the provider-neutral agent on port `8083`
and the internal MCP decision service at `http://localhost:8084/mcp`:

```bash
START_AGENT=1 OPENAI_API_KEY='...' OPENAI_MODEL='gpt-5-mini' ./run-dev.sh
```

API profiles are available only when both configured credential and model
variables are present. Local `codex-cli` and `gemini-cli` profiles instead
require the corresponding binary and an existing interactive login. The CLI
runners use an empty temporary workspace and an isolated MCP-only
configuration; subscription authentication directories are never mounted in
Kubernetes. `supportedVersionPrefix` pins the tested CLI release family; an
unmatched installation is reported as unavailable instead of being invoked.
Each CLI profile also pins its model and passes it explicitly with `--model`.
The supplied profiles use `gpt-5.6-luna` for Codex and the Gemini CLI `flash`
alias. Their safe model labels appear in the profile catalog and battle history.

To use a subscription CLI in a battle running on the local Kubernetes cluster,
deploy the application and start the host bridge in a second terminal:

```bash
./deploy-local.sh
./run-k8s-cli-agent.sh codex
```

Use `gemini`, `both`, or `auto` instead of `codex` when appropriate. Keep the
bridge terminal open, browse to `http://battlefield.localhost:8888`, and select
the enabled subscription profile as a faction controller. The host agent
long-polls the internal MCP service through a loopback-only port-forward and
handles decisions at ticks `10, 20, 30, ...`. It never copies the CLI login or
its authentication files into Kubernetes. Press Ctrl+C after the battle; the
helper then disconnects the agent and removes the CLI profiles from the battle
form.

To start each component manually instead, run:

```bash
cd backend
docker compose up -d postgres

export DATABASE_URL='postgres://battlefield:battlefield@localhost:5432/battlefield?sslmode=disable'

go run ./cmd/server -migrate

go run ./cmd/server
```

The server listens on `http://localhost:8080` by default. Verify its liveness in
another terminal:

```bash
curl http://localhost:8080/livez
```

The response is `ok` with HTTP status `200`.

Check whether the API is ready to receive database-backed traffic with:

```bash
curl http://localhost:8080/readyz
```

The readiness endpoint returns `200 OK` when PostgreSQL responds within one
second and `503 Service Unavailable` otherwise.

Start the separate battle worker in another terminal with the same database URL:

```bash
cd backend
export DATABASE_URL='postgres://battlefield:battlefield@localhost:5432/battlefield?sslmode=disable'
export ENGINE_WORKER_ID='engine-local-1'
go run ./cmd/engine
```

The engine does not expose gameplay routes. Its internal operational endpoint listens on `http://localhost:8081`; `/startupz` reports initialization, `/livez` checks the process, and `/readyz` checks worker readiness plus database connectivity. The API exposes the same three lifecycle endpoints on port 8080. Both processes retry transient PostgreSQL connection failures during startup and become unready before graceful shutdown.

### Inspect the local database with DBHub

DBHub is available as an opt-in development tool. Start it and its PostgreSQL
dependency from the project root with:

```bash
docker compose -f backend/compose.yaml --profile tools up -d dbhub
```

Open `http://localhost:8082`. DBHub connects to the Compose PostgreSQL service
with the repository's local-only `battlefield` credentials. It is bound to the
host loopback interface because its HTTP endpoint does not provide application
authentication. Stop it with:

```bash
docker compose -f backend/compose.yaml stop dbhub
```

Application processes write structured JSON operational logs to stdout for container log collection. API logs include request IDs, status codes, response sizes, and latency. Engine logs cover claims, reclaimed attempts, completion results, graceful requeues, lease loss, and failures. Individual combat ticks remain in the durable `battle_events` stream instead of being duplicated in normal process logs. Send `X-Request-ID` with an API request to preserve an upstream correlation ID; otherwise the server generates one and returns it in the response header. The API and engine expose Prometheus metrics at `/metrics`; see the [observability and reliability guide](OBSERVABILITY.md) for the metric catalog and proposed SLIs/SLOs.

The credentials in `compose.yaml` and `.env.example` are for local development only. Use a secret manager to provide `DATABASE_URL` in deployed environments. The application only runs schema migrations when explicitly started with `-migrate`.

## Run in local Kubernetes

The repository includes a staged Kustomize deployment for `kind`:

1. `deploy/overlays/local/infrastructure` creates the namespace, configuration,
   local Secret, and development PostgreSQL StatefulSet.
2. `deploy/overlays/local/migration` applies schema migrations through a
   one-shot Job.
3. `deploy/overlays/local/application` creates the API, engine, agent, internal
   MCP service, frontend,
   Services, HPA, and disruption budgets. The portable base Ingress is removed
   locally in favor of Gateway API routes.
4. `deploy/overlays/local/observability` creates Prometheus, namespace-scoped
   kube-state-metrics, provisioned application and Kubernetes Grafana
   dashboards, SLO rules, Elasticsearch, and Kibana;
   `deploy/overlays/local/logging-collector` isolates the Fluent Bit node log
   collector in its own namespace.
5. `deploy/overlays/local/gateway` creates Envoy Gateway routes for the
   frontend, Grafana, and Kibana without persistent port-forward commands.

Follow the complete build, image-loading, deployment, verification, and cleanup
commands in the [deployment learning guide](DEPLOYMENT.md#deploy-the-application-locally).
The standard container workflow uses `./scripts/cd/create-kind-cluster.sh`,
`./scripts/ci/build-images.sh`, and `./scripts/cd/deploy-kind.sh` so every deployed
application image is identified by the full source commit SHA. After deployment,
open `http://battlefield.localhost:8888` for the application and
`http://grafana.localhost:8888` for Grafana, or
`http://kibana.localhost:8888` for logs. In Kibana Discover, use the
provisioned **Battlefield AI Decisions** saved search to correlate model claims,
MCP calls, accepted plans, timeouts, and engine fallbacks. The `VERSION` file
supplies release metadata, while CI builds add a human-readable
`major.minor.patch-build.N` alias to the same image. Merged-main deployments compare against the previous
successful build and only build, scan, migrate, or apply components affected by
that change; an unavailable comparison or unclassified path safely falls back
to a full deployment.

The root `Jenkinsfile` provides pull-request CI and merged-`main` deployment:
lint, coverage, tests, and vulnerability scans gate the PR, while the merge
pipeline also waits for SonarQube before it rebuilds the exact commit, migrates,
deploys to local kind, smoke-tests, and supports rollback.
See the [Jenkins setup guide](JENKINS.md) before configuring the Multibranch job
or its local Kubernetes credential.

For fast iteration after the local cluster, infrastructure, and gateway have
already been deployed, rebuild and redeploy only the backend and frontend with:

```bash
./deploy-local.sh
```

This convenience command accepts uncommitted local changes, runs the backend
migration gate, and updates the API, engine, agent, MCP, and frontend workloads. When the
working tree is dirty, it also restarts those workloads so images rebuilt under
the current commit tag are picked up immediately. It does not deploy or update
PostgreSQL, observability, logging, or gateway resources.

### Use a different configuration file

The server loads `config.yaml` from its working directory by default. Pass another path with `-config`:

```bash
go run ./cmd/server -config /path/to/config.yaml
```

Unknown YAML fields, invalid durations, invalid connection-pool values, and a missing database URL cause startup to fail with an error.

### Apply database migrations

Apply all pending migrations and exit without starting the HTTP server:

```bash
go run ./cmd/server -migrate
```

Migration files are embedded in the executable, so a deployed binary does not need a separate `migrations` directory or Goose CLI. The command is safe to repeat; if the database is current, it reports that zero migrations were applied. Use `-config` alongside `-migrate` when needed.

## Local quality checks

Run the same Go lint and coverage work used by Jenkins:

```bash
./scripts/ci/lint-go.sh
./scripts/ci/test-go-coverage.sh
(cd frontend && npm ci && npm test -- --coverage && npm run build)
```

Go coverage is written to `backend/coverage.out`; frontend LCOV coverage is
written to `frontend/coverage/lcov.info`. Both are ignored by Git and imported
by SonarQube. The test commands require at least 80% Go statement coverage and
80% frontend statement, branch, function, and line coverage. See
[JENKINS.md](JENKINS.md#configure-local-sonarqube) for the local server and
Jenkins quality-gate setup. SonarQube displays and evaluates these externally
generated reports alongside static reliability, security, maintainability,
complexity, and duplication findings; it does not run the tests that produce
coverage.

## API

When authentication is enabled, all `/v1` routes except login and session
discovery require the signed session cookie. Lifecycle and metrics endpoints
remain public for infrastructure probes.

### Administrator session

```http
POST /v1/auth/login
Content-Type: application/json

{"username":"admin","password":"..."}
```

Successful login returns `204 No Content` and an HttpOnly, SameSite=Strict
session cookie. `GET /v1/auth/session` reports the current actor and
`POST /v1/auth/logout` expires the cookie. Invalid or expired sessions receive
`401 Unauthorized`.

### Startup check

```http
GET /startupz
```

Returns `503 Service Unavailable` while dependencies and application state are
initializing, then `200 OK`. Kubernetes uses this for its startup probe.

### Liveness check

```http
GET /livez
```

Returns `200 OK` with a plain-text `ok` response. This confirms that the HTTP
process is running and is suitable for a liveness probe. `/healthz` remains as
a compatibility alias.

### Readiness check

```http
GET /readyz
```

Returns `200 OK` with a plain-text `ok` response when PostgreSQL responds within
one second. It returns `503 Service Unavailable` when the database is not
reachable and is suitable for a readiness probe.

### Create a faction

```http
POST /v1/factions
Content-Type: application/json

{
  "id": "4db8d152-1e04-4d10-8824-859c6ed72878",
  "name": "Northern Alliance",
  "rulesetId": "castle-siege-v1",
  "blueprint": {
    "units": [
      {"type":"vanguard","count":1},
      {"type":"striker","count":1},
      {"type":"ranger","count":1}
    ],
    "upgradeIds": ["arsenal"],
    "recruitmentPriority": ["vanguard","striker","ranger"]
  }
}
```

Example:

```bash
curl -i \
  -H 'Content-Type: application/json' \
  -d '{"id":"4db8d152-1e04-4d10-8824-859c6ed72878","name":"Northern Alliance","rulesetId":"castle-siege-v1","blueprint":{"units":[{"type":"vanguard","count":1},{"type":"striker","count":1},{"type":"ranger","count":1}],"upgradeIds":["arsenal"],"recruitmentPriority":["vanguard","striker","ranger"]}}' \
  http://localhost:8080/v1/factions
```

Responses:

| Status | Meaning |
| --- | --- |
| `201 Created` | The faction was created. The response has no body. |
| `400 Bad Request` | The JSON is invalid, the ID is not UUID-shaped, or the trimmed name is empty. |
| `422 Unprocessable Entity` | The blueprint is illegal, unknown, or over budget. |
| `409 Conflict` | A faction with the same ID already exists. |
| `500 Internal Server Error` | The faction could not be persisted. |

Request bodies are limited to 64 KiB and must contain exactly one JSON value. Names are trimmed before storage.

`GET /v1/faction-rulesets` returns the available budgets, unit statistics, upgrades, and costs used by the faction builder.

### Delete a faction

```http
DELETE /v1/factions/{id}
```

A successful deletion returns `204 No Content`; malformed IDs return `400` and
missing factions return `404`. Deletion removes the faction from the active
registry. Existing battle history remains readable because each battle stores
an immutable scenario and participant snapshot.

### Get a faction name

```http
GET /v1/factions/{id}/name
```

Example:

```bash
curl http://localhost:8080/v1/factions/4db8d152-1e04-4d10-8824-859c6ed72878/name
```

A successful request returns:

```json
{"name":"Northern Alliance"}
```

The endpoint returns `404 Not Found` when the faction does not exist and `500 Internal Server Error` for other storage failures.

### Queue a battle

Both factions must already exist and use the scenario's ruleset. The scenario ID refers to a strict YAML template in `backend/scenarios`; the API resolves both persisted blueprints, the map, ruleset, and controller plans into a deterministic snapshot for replay.

The frontend discovers available templates through `GET /v1/scenarios`, which returns scenario IDs, rules versions, scenario-specific maximum ticks, tick pacing, and decision timing. `castle-siege-v1` allows at most 500 ticks.

```http
POST /v1/battles
Content-Type: application/json

{
  "id": "7f22e693-bacd-46bb-b942-64fa613cd388",
  "scenarioId": "castle-siege-v1",
  "participants": [
    {
      "factionId": "4db8d152-1e04-4d10-8824-859c6ed72878",
      "controller": {"type": "model", "profileId": "gemini-api-balanced"}
    },
    {
      "factionId": "41ac3a2f-3371-4de1-b0f6-70600fcd9052",
      "controller": {"type": "scripted"}
    }
  ],
  "tickCount": 100
}
```

A valid request returns `202 Accepted`, a queued status, and `Location: /v1/battles/{id}`. Invalid input returns `400`, a missing scenario or faction returns `404`, incompatible faction rulesets return `422`, and a duplicate battle ID returns `409`.

`GET /v1/model-profiles` returns the operator allowlist with safe availability,
provider, runtime, scope, model-label, and tool-capability fields. It never
returns credential values or credential environment-variable names.

At ticks 10, 20, 30, and so on, the engine freezes the prior state, creates one
fog-of-war-bounded decision per faction, waits up to two minutes for model plans,
then applies income, reserve-aware priority production, tactics, and combat.
Late plans cannot overwrite the fallback selected by the engine. Completed
decision metadata is available from
`GET /v1/battles/{id}/decisions?attempt={attempt}`.

For offline comparisons, query `battle_controller_evaluation`. It groups
scripted and model profiles by controller/provider/runtime and reports win,
invalid-plan, fallback, latency, normalized usage, and plan-diversity metrics.

### Get battle status and results

```http
GET /v1/battles/{id}
```

The response reports `queued`, `running`, `completed`, `failed`, or `stopped`, along with the authored map and current units, buildings, treasuries, tick, and attempt. Completed battles include castle health plus unit, economy, loot, damage, and posture statistics.

Stop a queued or running battle with `POST /v1/battles/{id}/stop`. The operation
releases the engine lease and marks unresolved model decisions as failed so a
host subscription agent cannot continue the stopped run.

`GET /v1/battles/{battleId}/factions/{factionId}/view` returns the same terrain with faction fog-of-war applied. Owned entities and both castles are always present; other hostile entities appear only inside current friendly vision.

### Page through battle events

```http
GET /v1/battles/{id}/events?attempt=1&after=0&limit=100
```

`attempt` defaults to the battle's current attempt, `after` is the last event sequence already observed, and `limit` defaults to 100 with a maximum of 500. Events are ordered by sequence and the response includes `nextCursor`.

## Configuration

Non-secret settings live in `backend/config.yaml`:

| Setting | Default | Purpose |
| --- | --- | --- |
| `http.address` | `:8080` | HTTP listen address. |
| `http.readHeaderTimeout` | `5s` | Maximum time to read request headers. |
| `http.readTimeout` | `10s` | Maximum time to read a request. |
| `http.writeTimeout` | `10s` | Maximum time to write a response. |
| `http.idleTimeout` | `60s` | Keep-alive idle timeout. |
| `http.shutdownTimeout` | `10s` | Graceful-shutdown deadline. |
| `database.urlEnvironmentVariable` | `DATABASE_URL` | Environment variable containing the PostgreSQL URL. |
| `database.connectTimeout` | `5s` | Initial database connection deadline. |
| `database.maxConnections` | `10` | Maximum pool size. |
| `database.minConnections` | `1` | Minimum pool size. |
| `database.maxConnectionLifetime` | `30m` | Maximum lifetime of a pooled connection. |
| `api.scenarioCatalogPath` | `scenarios` | Directory containing strict YAML scenario templates. |
| `api.rulesetCatalogPath` | `rulesets` | Directory containing strict faction-design rulesets. |
| `api.maxTicks` | `10000` | Maximum requested ticks for one battle. |
| `auth.enabledEnvironmentVariable` | `BATTLEFIELD_AUTH_ENABLED` | Boolean environment switch for the single-admin login. |
| `auth.usernameEnvironmentVariable` | `BATTLEFIELD_ADMIN_USERNAME` | Environment variable containing the administrator name. |
| `auth.passwordEnvironmentVariable` | `BATTLEFIELD_ADMIN_PASSWORD` | Environment variable containing a password of at least 12 characters. |
| `auth.sessionSecretEnvironmentVariable` | `BATTLEFIELD_SESSION_SECRET` | Environment variable containing at least 32 characters of session-signing material. |
| `auth.cookieSecureEnvironmentVariable` | `BATTLEFIELD_AUTH_COOKIE_SECURE` | Boolean environment switch for HTTPS-only cookies. |
| `auth.sessionTTL` | `12h` | Signed administrator-session lifetime. |
| `engine.healthAddress` | `:8081` | Internal liveness/readiness listen address. |
| `engine.pollInterval` | `1s` | Delay between empty queue polls. |
| `engine.leaseDuration` | `30s` | Exclusive battle ownership lease. |
| `engine.heartbeatInterval` | `10s` | Lease-renewal interval during paced waits. |
| `engine.shutdownTimeout` | `10s` | Deadline for health shutdown and battle requeue. |
| `engine.maxAttempts` | `3` | Processing attempts before a battle fails. |
| `ai.environment` | `local` | Profile compatibility scope (`local` or `cluster`). |
| `ai.agentHealthAddress` | `:8083` | Provider-runner health address. |
| `ai.mcpAddress` | `:8084` | Internal Streamable HTTP MCP listen address. |
| `ai.mcpUrl` | `http://127.0.0.1:8084/mcp` | MCP endpoint used by the agent. |
| `ai.decisionTimeout` | `2m` | Maximum unresolved model-decision wait. |

## Development

Run all tests from `backend`:

```bash
go test ./...
```

Apply migrations explicitly:

```bash
go run ./cmd/server -migrate
```

Rollback remains an intentional operator action available through the standalone [Goose CLI](https://github.com/pressly/goose).

Stop the local database without deleting its volume:

```bash
docker compose stop postgres
```

## Project structure

```text
backend/
├── cmd/agent/             Provider-neutral API and local CLI decision runner
├── cmd/engine/            Queued battle worker and operational health server
├── cmd/mcp/               Authenticated internal MCP decision service
├── cmd/server/            Application entry point and shutdown lifecycle
├── internal/app/          Dependency composition
├── internal/ai/           Profiles, decision contract, MCP, providers, and runners
├── internal/authn/        Administrator credentials, signed sessions, identity context, and API middleware
├── internal/battle/       Battle feature boundary
│   ├── engine/
│   │   └── worker/        Queue claims, tick pacing, leases, and shutdown
│   ├── repo/              Shared PostgreSQL battle persistence
│   ├── server/
│   │   ├── api/           Battle and scenario HTTP handlers
│   │   ├── catalog/       Strict YAML loading and snapshot resolution
│   │   └── service/       Battle validation and API-facing use cases
│   └── simulation/controller/ Tactical policies and strategy windows
├── internal/game/         Pure hex-map, economy, combat, state, and visibility domain
├── internal/config/       YAML and environment configuration
├── internal/faction/      Faction model and feature boundary
│   ├── api/               Faction HTTP handlers
│   ├── repo/              PostgreSQL faction persistence
│   └── service/           Faction validation and use cases
├── internal/httpapi/      Shared HTTP router, middleware, and JSON helpers
├── internal/logging/      Structured logger construction
├── internal/postgres/     Shared PostgreSQL pool and migration infrastructure
├── migrations/            Embedded Goose SQL migrations
├── rulesets/              Versioned point-buy unit and upgrade catalogs
├── scenarios/             Versioned authored maps and strategy templates
├── compose.yaml           Local PostgreSQL service
└── config.yaml            Default non-secret configuration
```

The process paths are currently:

```text
HTTP handler -> faction use case -> PostgreSQL adapter
HTTP handler -> battle use case -> scenario snapshot -> PostgreSQL queue
battle worker -> decision records -> scripted resolver or agent -> MCP decision service
battle worker -> validated plans -> deterministic battle -> PostgreSQL events/result
```

## Project direction

The intended architecture uses an authoritative, deterministic engine: clients propose typed commands and the engine validates and applies them. This keeps game rules out of HTTP handlers and prevents clients or AI agents from writing arbitrary world state.

See [REVISION.md](REVISION.md) for the proposed architecture, design exercises, AI-agent boundary, infrastructure evolution, and milestone roadmap. The separate [Interview Revision Hub](../../revision/README.md) contains the broader Go, backend/cloud, system-design, AI, and behavioral study material.
