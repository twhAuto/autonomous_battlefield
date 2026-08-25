# Autonomous Battlefield

Autonomous Battlefield is a Go backend for an auto-battler and expansion game. The long-term product will let human players and AI agents control factions that gather resources, expand territory, and fight on a continuously running battlefield.

The project is in an early stage. The current backend provides PostgreSQL-backed faction and battle APIs, a separate queued battle engine, deterministic unit combat, configurable scripted strategy decisions, validated configuration, health endpoints, and graceful shutdown. Maps, economies, player commands, authentication, and LLM agents described in the [revision guide](REVISION.md) remain planned work.

## Current capabilities

- Create a faction with a caller-supplied UUID and non-empty name.
- Retrieve a faction's name by ID.
- Store factions in PostgreSQL through `pgx`.
- Configure HTTP and database connection settings from YAML and `DATABASE_URL`.
- Apply pending schema changes through a migration-only server mode.
- Shut down the HTTP server gracefully on `SIGINT` or `SIGTERM`.
- Rank valid faction actions with an in-memory deterministic utility controller.
- Schedule configurable strategy-decision windows and use a safe fallback plan between them.
- Queue finite battles durably in PostgreSQL and claim them safely across engine workers.
- Run deterministic initiative combat and persist status, statistics, and paginated events.
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
| Build and deploy to local Kubernetes | [DEPLOYMENT.md](DEPLOYMENT.md#deploy-the-application-locally) |
| Inspect metrics, Grafana, and reliability objectives | [OBSERVABILITY.md](OBSERVABILITY.md) |
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

The script uses `DATABASE_URL` and `ENGINE_WORKER_ID` when they are already set.
Set `START_POSTGRES=0` to use an existing PostgreSQL instance without starting
the Compose service:

```bash
START_POSTGRES=0 DATABASE_URL='postgres://user:password@localhost:5432/battlefield?sslmode=disable' ./run-dev.sh
```

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

Both processes write structured JSON operational logs to stdout for container log collection. API logs include request IDs, status codes, response sizes, and latency. Engine logs cover claims, reclaimed attempts, completion results, graceful requeues, lease loss, and failures. Individual combat ticks remain in the durable `battle_events` stream instead of being duplicated in normal process logs. Send `X-Request-ID` with an API request to preserve an upstream correlation ID; otherwise the server generates one and returns it in the response header. Both processes also expose Prometheus metrics at `/metrics`; see the [observability and reliability guide](OBSERVABILITY.md) for the metric catalog and proposed SLIs/SLOs.

The credentials in `compose.yaml` and `.env.example` are for local development only. Use a secret manager to provide `DATABASE_URL` in deployed environments. The application only runs schema migrations when explicitly started with `-migrate`.

## Run in local Kubernetes

The repository includes a staged Kustomize deployment for `kind`:

1. `deploy/overlays/local/infrastructure` creates the namespace, configuration,
   local Secret, and development PostgreSQL StatefulSet.
2. `deploy/overlays/local/migration` applies schema migrations through a
   one-shot Job.
3. `deploy/overlays/local/application` creates the API, engine, frontend,
   Services, HPA, and disruption budgets. The portable base Ingress is removed
   locally in favor of Gateway API routes.
4. `deploy/overlays/local/observability` creates namespace-scoped Prometheus,
   a provisioned Grafana dashboard, and SLO recording and alert rules.
5. `deploy/overlays/local/gateway` creates Envoy Gateway routes for the
   frontend and Grafana on localhost without persistent port-forward commands.

Follow the complete build, image-loading, deployment, verification, and cleanup
commands in the [deployment learning guide](DEPLOYMENT.md#deploy-the-application-locally).
The standard container workflow uses `./scripts/create-kind-cluster.sh`,
`./scripts/build-images.sh`, and `./scripts/deploy-kind.sh` so every deployed
application image is identified by the full source commit SHA. After deployment,
open `http://battlefield.localhost:8888` for the application and
`http://grafana.localhost:8888` for Grafana. The `VERSION` file supplies release
metadata, while CI builds add a human-readable `major.minor.patch-build.N` alias
to the same image.

The root `Jenkinsfile` provides pull-request CI and merged-`main` deployment:
lint, coverage, tests, and vulnerability scans gate the PR, while the merge
pipeline also waits for SonarQube before it rebuilds the exact commit, migrates,
deploys to local kind, smoke-tests, and supports rollback.
See the [Jenkins setup guide](JENKINS.md) before configuring the Multibranch job
or its local Kubernetes credential.

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
./scripts/lint-go.sh
./scripts/test-go-coverage.sh
(cd frontend && npm ci && npm test -- --coverage && npm run build)
```

Go coverage is written to `backend/coverage.out`; frontend LCOV coverage is
written to `frontend/coverage/lcov.info`. Both are ignored by Git and imported
by SonarQube. See [JENKINS.md](JENKINS.md#configure-local-sonarqube) for the
local server and Jenkins quality-gate setup. SonarQube displays and evaluates
these externally generated reports alongside static reliability, security,
maintainability, complexity, and duplication findings; it does not run the
tests that produce coverage.

## API

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
  "name": "Northern Alliance"
}
```

Example:

```bash
curl -i \
  -H 'Content-Type: application/json' \
  -d '{"id":"4db8d152-1e04-4d10-8824-859c6ed72878","name":"Northern Alliance"}' \
  http://localhost:8080/v1/factions
```

Responses:

| Status | Meaning |
| --- | --- |
| `201 Created` | The faction was created. The response has no body. |
| `400 Bad Request` | The JSON is invalid, the ID is not UUID-shaped, or the trimmed name is empty. |
| `409 Conflict` | A faction with the same ID already exists. |
| `500 Internal Server Error` | The faction could not be persisted. |

Request bodies are limited to 64 KiB and must contain exactly one JSON value. Names are trimmed before storage.

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

Both factions must already exist. The scenario ID refers to a strict YAML template in `backend/scenarios`; the API resolves the selected faction identities into its two loadout slots and freezes that snapshot for replay.

The frontend discovers available templates through `GET /v1/scenarios`, which returns scenario IDs, rules versions, tick pacing, and decision timing.

```http
POST /v1/battles
Content-Type: application/json

{
  "id": "7f22e693-bacd-46bb-b942-64fa613cd388",
  "scenarioId": "skirmish-v1",
  "factionIds": [
    "4db8d152-1e04-4d10-8824-859c6ed72878",
    "41ac3a2f-3371-4de1-b0f6-70600fcd9052"
  ],
  "tickCount": 100
}
```

A valid request returns `202 Accepted`, a queued status, and `Location: /v1/battles/{id}`. Invalid input returns `400`, a missing scenario or faction returns `404`, and a duplicate battle ID returns `409`.

### Get battle status and results

```http
GET /v1/battles/{id}
```

The response reports `queued`, `running`, `completed`, or `failed`, along with the current tick and attempt. Completed battles include the winner or draw reason and per-faction unit, damage, attack, and defense statistics.

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
| `api.maxTicks` | `10000` | Maximum requested ticks for one battle. |
| `engine.healthAddress` | `:8081` | Internal liveness/readiness listen address. |
| `engine.pollInterval` | `1s` | Delay between empty queue polls. |
| `engine.leaseDuration` | `30s` | Exclusive battle ownership lease. |
| `engine.heartbeatInterval` | `10s` | Lease-renewal interval during paced waits. |
| `engine.shutdownTimeout` | `10s` | Deadline for health shutdown and battle requeue. |
| `engine.maxAttempts` | `3` | Processing attempts before a battle fails. |

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
├── cmd/engine/            Queued battle worker and operational health server
├── cmd/server/            Application entry point and shutdown lifecycle
├── internal/app/          Dependency composition
├── internal/battle/       Battle feature boundary
│   ├── engine/
│   │   └── worker/        Queue claims, tick pacing, leases, and shutdown
│   ├── repo/              Shared PostgreSQL battle persistence
│   ├── server/
│   │   ├── api/           Battle and scenario HTTP handlers
│   │   ├── catalog/       Strict YAML loading and snapshot resolution
│   │   └── service/       Battle validation and API-facing use cases
│   └── simulation/        Shared deterministic combat state and rules
│       └── controller/    Tactical policies and strategy windows
├── internal/config/       YAML and environment configuration
├── internal/faction/      Faction model and feature boundary
│   ├── api/               Faction HTTP handlers
│   ├── repo/              PostgreSQL faction persistence
│   └── service/           Faction validation and use cases
├── internal/httpapi/      Shared HTTP router, middleware, and JSON helpers
├── internal/logging/      Structured logger construction
├── internal/postgres/     Shared PostgreSQL pool and migration infrastructure
├── migrations/            Embedded Goose SQL migrations
├── scenarios/             Versioned battle loadout and strategy templates
├── compose.yaml           Local PostgreSQL service
└── config.yaml            Default non-secret configuration
```

The process paths are currently:

```text
HTTP handler -> faction use case -> PostgreSQL adapter
HTTP handler -> battle use case -> scenario snapshot -> PostgreSQL queue
battle worker -> PostgreSQL claim -> deterministic battle -> PostgreSQL events/result
```

## Project direction

The intended architecture uses an authoritative, deterministic engine: clients propose typed commands and the engine validates and applies them. This keeps game rules out of HTTP handlers and prevents clients or AI agents from writing arbitrary world state.

See [REVISION.md](REVISION.md) for the proposed architecture, design exercises, AI-agent boundary, infrastructure evolution, and milestone roadmap. The separate [Interview Revision Hub](../../revision/README.md) contains the broader Go, backend/cloud, system-design, AI, and behavioral study material.
