# Local development setup

Use the registered Kind environment to build and run working-tree changes.
The [configuration model](../architecture/configuration.md) describes file locations,
credential precedence and cluster identity checks. For production, see
[environment operations](../operations/environments.md).

## WSL setup and normal use

Requirements: Docker, Kind, kubectl, Bash, curl, Python 3, Node.js 24 and npm.
Backend development and Go checks require a toolchain compatible with
[`backend/go.mod`](../../backend/go.mod). Container builds use the version pinned
in the [backend Dockerfile](../../backend/Dockerfile). Run commands from the repository root unless a command changes directory
explicitly.

For a fresh checkout, create the dev cluster and its registration:

```bash
./scripts/cd/create-kind-cluster.sh
./scripts/cd/prepare-dev-secrets.sh
./scripts/cd/deploy-local.sh
```

The cluster helper also resumes stopped nodes and prepares the gateway. It refuses
to replace a missing registered cluster or recreate its data. If you already have
a running, prepared Kind cluster but no registration, register it once with
`./scripts/cd/setup-environment.sh dev kind-battlefield` before preparing secrets.
Registration never overwrites an existing profile or kubeconfig.

Open `http://battlefield.localhost:8888`. Edit the root `.env`, then rerun
`./scripts/cd/deploy-local.sh`. For a missing `.env`, preparation recovers existing
Kubernetes credentials or generates initial dev secrets. It never invents a
database password for existing PVCs with missing credentials.

### Application settings

Edit the Git-ignored root `.env` using [`.env.example`](../../.env.example), then
rerun `./scripts/cd/deploy-local.sh`. The [configuration model](../architecture/configuration.md)
explains the literal file format, source selection, Secret rendering and restart rules,
including the distinction between deployment and standalone processes.

[`backend/config.yaml`](../../backend/config.yaml) defines local process settings;
unknown fields and invalid values fail startup. Scenarios, rulesets and content
are embedded from `backend/gamedata`, so editing them requires rebuilding the
server. From `backend/`, `go run ./cmd/server -config /path/to/config.yaml`
selects another configuration. `go run ./cmd/server -migrate` applies embedded
migrations and exits; normal server startup does not apply them.

### Optional model controllers

Scripted controllers do not need provider credentials. API-backed profiles need
a configured model and key. Subscription CLI profiles need a supported binary,
an existing login and a running local bridge in a second terminal:

```bash
./scripts/dev/run-k8s-cli-agent.sh codex
```

The helper also accepts `antigravity`, `claude`, `both` (Codex and Antigravity),
`all` (all three CLIs) and `auto` (every available authenticated CLI).
See [Antigravity setup](../operations/antigravity-cli.md),
[Claude setup](../operations/claude-cli.md) and
[profile availability](../operations/deployment.md#1-prepare-private-dev-secret-files).
Configured model/version prefixes live in `backend/config.yaml`; the
[execution contract](../architecture/ai-decisions.md#model-execution)
explains their isolation boundaries.

## Standalone development services

[`infra/local/compose.yaml`](../../infra/local/compose.yaml) runs PostgreSQL and
optional Redis or DBHub for focused development outside Kind. It does not launch
the application or supply database services to Kubernetes pods.

For a new standalone stack, create `infra/local/.env` from
[the template](../../infra/local/.env.example) and set a local database password.
Keep the file private and untracked. Then run:

```bash
docker compose --env-file infra/local/.env -f infra/local/compose.yaml up -d postgres
# Optional cache or database browser:
docker compose --env-file infra/local/.env -f infra/local/compose.yaml --profile cache up -d redis
docker compose --env-file infra/local/.env -f infra/local/compose.yaml --profile tools up -d dbhub
# Stop the stack while retaining database data:
docker compose --env-file infra/local/.env -f infra/local/compose.yaml down
```

The Compose project name is fixed as `backend`, so the network and the
`backend_battlefield-postgres` volume keep stable names. `infra/local/.env` is
Git-ignored.

Application processes started separately need their own `DATABASE_URL` pointing
to this PostgreSQL instance. The Compose environment file is not the Kind root
`.env` and is not loaded by Go commands. Redis is a disposable cache on loopback
port 6379; DBHub uses loopback port 8082. PostgreSQL retains its existing published
port 5432. See [Redis operations](../operations/redis.md) for cache configuration.

## Standalone process ports

When running processes outside Kind, the defaults in
[`backend/config.yaml`](../../backend/config.yaml),
[`frontend/vite.config.ts`](../../frontend/vite.config.ts) and
[`infra/local/compose.yaml`](../../infra/local/compose.yaml) are:

| Port | Process |
| --- | --- |
| `5173` | Vite frontend |
| `8080` | Public API |
| `8081` | Engine health and metrics |
| `8083` | Agent health |
| `8084` | AI gateway: MCP tools and internal agent dispatch API |
| `5432` | PostgreSQL |

These are process defaults; the normal Kind workflow uses the gateway URL above.

## Optional observability

The [observability runbook](../operations/observability.md#run-the-local-stack) covers
the complete local stack, dashboards, log searches and troubleshooting.

## Verify changes

Follow [local CI checks](ci.md#run-local-build-and-deployment-checks-without-jenkins)
for lint, coverage, builds and smoke checks. Environment changes also need the
deployment regression suite:

```bash
./scripts/test/test-deployment.sh
```

It covers environment resolution, component selection, deployment, production
preparation, tunnels, command wrappers and runtime assurance using isolated fixtures.
Dev smoke checks cover readiness, authentication and scenario access.
