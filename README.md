# Autonomous Battlefield

Deployment environments are separate: **WSL dev** builds and loads local images;
**server production** receives verified GHCR images from Jenkins `main` delivery.
Start with the [environment setup guide](docs/operations/environments.md);
version-controlled configuration templates live in [`deploy/environments/`](deploy/environments/).

Autonomous Battlefield is a Go backend for an auto-battler and expansion game. The long-term product will let human players and AI agents control factions that gather resources, expand territory, and fight on a continuously running battlefield.

The project is in an early stage. The current backend provides PostgreSQL-backed faction and battle APIs, a separate queued battle engine, deterministic castle-siege combat on an authored hex map, editable faction doctrines and gold-funded opening armies, autonomous economies, fog-of-war views, production-window decisions, provider-neutral model controllers over MCP, an optional single-admin login, validated configuration, health endpoints, and graceful shutdown. Player commands, multi-user authorization, and seeded map generation remain planned work. The optional `frontier-siege-v2` scenario adds Catapults, Battering Rams, Pikemen, Herbalists, defense/watch towers, and forward Barracks on a 25×17 map. See [Frontier Siege](docs/design/frontier-siege.md) for the roster, construction limits, and play instructions.

The **Battle history** menu opens `/battles`: browse every run, filter by status
(including paused battles), page through older engagements, and manually refresh
the newest entries. Open a battle to inspect its existing map, results, events,
and AI decisions. Saved participant names survive faction edits and deletion.

The battle inspector combines a **System** event stream and player comparison
with an **Inspect** dropdown for selecting System or a player. Each player has **Overview**, **Decisions**, **Units**,
and **Faction** sections, with current gold, living units, and castle health
always visible. Decisions can load the saved observation used by that AI,
including historical resources, visible forces, available choices, and memory.
Faction background and doctrine come from the battle snapshot. Progress and
controls remain in a compact toolbar above the map. The inspector accommodates
additional players; creating and simulating battles still requires two factions.
Unit and building markers use distinct tactical icons, with a collapsible icon
legend. The map, roster, events, and recruitment sources display readable names
such as **Iron Legion · Ranger #12**; full IDs remain available in tooltips and
raw inspection views.

## Documentation

| Task | Guide |
| --- | --- |
| Start the application | [Run locally](#run-locally) |
| Understand components and runtime behavior | [System architecture](docs/design/system-architecture.md) |
| Find packages, tests, and change paths | [Codebase guide](docs/design/codebase-guide.md) |
| Use the HTTP API | [API reference](docs/reference/api.md) |
| Register dev or production and resume a stopped cluster | [Environments](docs/operations/environments.md) |
| Build, deploy, inspect, or remove Kubernetes workloads | [Deployment](docs/operations/deployment.md) |
| Configure CI/CD and SonarQube | [Jenkins](docs/operations/jenkins.md) |
| Inspect metrics, traces, logs, and reliability objectives | [Observability](docs/operations/observability.md) |
| Configure subscription CLI authentication | [Antigravity](docs/operations/antigravity-cli.md) |
| Compare controllers and author unit knowledge | [AI comparisons](docs/operations/ai-comparisons.md) |
| Enable the optional completed-battle cache | [Redis](docs/operations/redis.md) |
| Review AI development and artifact assurance | [AI policy](docs/security/ai-development-policy.md), [supply-chain runbook](docs/operations/supply-chain-runbook.md) |
| Provision the AWS reference | [AWS](infra/aws/README.md) |

The architecture guide owns runtime explanations; the codebase guide owns source
navigation. Operational guides own their setup and recovery commands. The root
`DEPLOYMENT.md`, `JENKINS.md`, and `OBSERVABILITY.md` remain compatibility links.

## Requirements

- Go 1.26.5 or a compatible newer version.
- Node.js 24 and npm for the frontend.
- Docker with Docker Compose for the local PostgreSQL instance.
- Bash, curl, and OpenSSL for the development launcher and setup below.

## Run locally

Start PostgreSQL, apply migrations, and run the API, battle engine, and frontend
with one command from the project root. Create a persistent, Git-ignored local
database password once before the first start:

```bash
umask 077
printf 'BATTLEFIELD_POSTGRES_PASSWORD=%s\n' "$(openssl rand -hex 24)" > backend/.env
./run-dev.sh
```

PostgreSQL keeps the password that initialized an existing Compose volume.
When upgrading an older checkout, put that volume's current password in
`backend/.env` first; changing the file alone does not rotate the database role.

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

API profiles require a configured model and credentials. Subscription CLI profiles
require a supported binary and an existing host login; the configured models and
version prefixes live in [`backend/config.yaml`](backend/config.yaml).
See [model execution](docs/design/system-architecture.md#6-model-execution) for
the execution contract and isolation, and [Antigravity setup](docs/operations/antigravity-cli.md)
for its authentication checks.

For battles in local Kubernetes, keep `./run-k8s-cli-agent.sh codex` running in a
second terminal. The helper also accepts `antigravity`, `both`, or `auto`;
[deployment setup](docs/operations/deployment.md#1-prepare-private-dev-secret-files)
explains profile availability and the host bridge.

To start each component manually instead, run:

```bash
cd backend
docker compose up -d postgres

set -a
source .env
set +a
export DATABASE_URL="postgres://battlefield:${BATTLEFIELD_POSTGRES_PASSWORD}@localhost:5432/battlefield?sslmode=disable"

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
set -a
source .env
set +a
export DATABASE_URL="postgres://battlefield:${BATTLEFIELD_POSTGRES_PASSWORD}@localhost:5432/battlefield?sslmode=disable"
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
with the Git-ignored password from `backend/.env`. It is bound to the
host loopback interface because its HTTP endpoint does not provide application
authentication. Stop it with:

```bash
docker compose -f backend/compose.yaml stop dbhub
```

Backend logs provide readable `msg` summaries, stable `event` fields, battle
progress and decision-wait updates, and trace correlation. `LOG_LEVEL` defaults
to INFO; DEBUG enables per-tick diagnostics. For compact Kibana views, request
correlation, health behavior, metrics, and traces, see
[observability](docs/operations/observability.md).

## Run in local Kubernetes

Register the dev environment and prepare its private secrets using the
[environment guide](docs/operations/environments.md), then run:

```bash
./deploy-local.sh
```

Open `http://battlefield.localhost:8888`. This command builds working-tree changes,
applies database/configuration, migrations, application and gateway resources,
and runs application smoke checks. Observability is optional; the environment
guide includes the full-stack option and restart/recovery procedure.

For a development backend on another port, set `DEV_API_PROXY_TARGET` in
`frontend/.env.local`; see [frontend setup](frontend/README.md#run-locally).

Jenkins `main` delivery publishes, scans, signs, and verifies GHCR images before
production deployment by digest. See [Jenkins](docs/operations/jenkins.md) for
setup and [deployment](docs/operations/deployment.md) for staged operations.

## Local quality checks

Run the same Go lint and coverage work used by Jenkins:

```bash
./scripts/ci/lint-go.sh
./scripts/test/test-go-coverage.sh
(cd frontend && npm ci && npm test -- --coverage && npm run build)
```

Go coverage is written to `backend/coverage.out`; frontend LCOV coverage is
written to `frontend/coverage/lcov.info`. Both are ignored by Git and imported
by SonarQube. The test commands require at least 80% Go statement coverage and
80% frontend statement, branch, function, and line coverage. See
[docs/operations/jenkins.md](docs/operations/jenkins.md#configure-local-sonarqube) for the local server and
Jenkins quality-gate setup. SonarQube displays and evaluates these externally
generated reports alongside static reliability, security, maintainability,
complexity, and duplication findings; it does not run the tests that produce
coverage.

## API

See the [HTTP API reference](docs/reference/api.md) for authentication, faction
profiles, battle creation and controls, results, views, and event pagination.

## Configuration

[`backend/config.yaml`](backend/config.yaml) is the checked-in local configuration;
[`backend/.env.example`](backend/.env.example) lists environment inputs.
Cluster settings live in the
[deployment ConfigMap](deploy/base/infrastructure/configmap.yaml), with private
inputs described in the [environment guide](docs/operations/environments.md).
Unknown YAML fields and invalid configuration fail startup. Catalog paths are
resolved relative to the configuration file.

From `backend`, use `go run ./cmd/server -config /path/to/config.yaml` to select
another configuration, or `go run ./cmd/server -migrate` to apply pending embedded
migrations and exit. Normal startup does not run migrations.

## AI evaluation and authored content

See [AI comparisons](docs/operations/ai-comparisons.md) for the free scripted
comparison command, live-provider opt-ins, memory and knowledge ablations,
explicit recruitment orders, and report limits. Unit rosters and optional
knowledge cards are authored in ruleset YAML.

Reusable content libraries and per-scenario restrictions are documented in the
[content authoring guide](docs/operations/content-library.md).

Factions support optional backgrounds and editable AI-generated drafts. Backgrounds
and doctrines are snapshotted for each battle, and agents act as their faction’s
commanding voice. The writer uses an operator-configured API or local CLI profile
(default `codex-subscription-local`), with 10 requests per account per UTC day and
a 30-second cooldown. Manual editing remains available when generation is off.
See [generation configuration and rollout](docs/operations/deployment.md#faction-background-generation-rollout)
and the [generation API](docs/reference/api.md#faction-background-generation).
