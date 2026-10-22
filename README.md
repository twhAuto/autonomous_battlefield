# OrdinalFrontier

OrdinalFrontier is a deterministic strategy battle simulator with a Go backend
and React frontend. Create factions, choose scripted or AI controllers, and watch
them compete on authored hex maps. AI controllers propose strategy plans; the game
engine validates and executes them.

- Create reusable factions with editable names, backgrounds and strategic doctrines.
- Run two-faction castle-siege battles with autonomous combat, economies and fog of war.
- Choose tick duration and decision interval when staging a battle, or keep map defaults.
- Inspect live battles, saved results, events and AI decision history.
- Manage invited players and model access through Player, Developer and Admin roles.

The hosted application is at [play.ordinalfrontier.com](https://play.ordinalfrontier.com)
and requires an invitation. Battles are finite and use authored maps. See
[game rules](docs/game/game-rules.md) and the
[Frontier Siege roster](docs/game/frontier-siege.md) for supported gameplay.
Every map shares the full roster, three-level Arsenal/Plating/Industry upgrades
and specialist branches. Choose Castle Siege or Frontier Siege for the layout.

Repository paths and deployment resources use the technical name
`autonomous-battlefield`; local development uses `battlefield.localhost`.

## Run locally

The normal development environment uses Kind on Linux or WSL with Docker access.
Install Docker, Kind, kubectl, Bash, curl, Python 3, Node.js 24 and npm.
Backend development requires a Go toolchain compatible with
[`backend/go.mod`](backend/go.mod). Container builds use the version pinned in the
[backend Dockerfile](backend/Dockerfile).

From the repository root:

```bash
# Create and register a fresh cluster, or resume an existing one:
./scripts/cd/create-kind-cluster.sh
# Once: preserve existing credentials or initialize fresh dev secrets:
./scripts/cd/prepare-dev-secrets.sh
# Build and deploy working-tree changes:
./scripts/cd/deploy-local.sh
```

Open `http://battlefield.localhost:8888`. Edit the Git-ignored root `.env` for
application settings, then rerun the deployment command. The
[local setup guide](docs/developer/local-setup.md) covers credentials, existing
cluster registration, optional model controllers and standalone development services.
For subscription controllers, keep `./scripts/dev/run-k8s-cli-agent.sh auto` running
in another terminal. Local CLIs allow the [selected stable version](docs/developer/local-kubernetes.md#subscription-clis)
or newer.

## Repository layout

| Location | Purpose |
| --- | --- |
| [`backend/`](backend/) | Go API, deterministic engine, model execution, migrations and authored game content |
| [`frontend/`](frontend/) | React application, component tests and browser tests |
| [`infra/`](infra/README.md) | Deployment manifests and policies, local services, platform provisioning and auxiliary pipelines |
| [`scripts/`](scripts/) | Development, CI, deployment, verification and operations commands |
| [`backend/test/performance/`](backend/test/performance/) | Load scenarios and query-plan SQL fixtures; launchers are in `scripts/perf/` |
| [`docs/`](docs/README.md) | Game rules, developer guides, architecture and operations runbooks |

Go and React unit tests live beside the code they exercise. The root `Jenkinsfile`
is the application pipeline; auxiliary pipelines live in `infra/jenkins/`.
Use `scripts/test/` for shared test runners and `scripts/cd/{deploy,rollback,smoke-test}.sh`
for deployment commands. Their registered environment selects Kind or K3s.

## Documentation

Start with the [documentation index](docs/README.md) for the full guide catalogue.

| I want to… | Start here |
| --- | --- |
| Run or change the application | [Local setup](docs/developer/local-setup.md), [local Kubernetes deployment](docs/developer/local-kubernetes.md), [codebase guide](docs/developer/codebase-guide.md) |
| Understand gameplay | [Game rules](docs/game/game-rules.md), [Frontier Siege](docs/game/frontier-siege.md) |
| Understand the system | [System architecture](docs/architecture/system-architecture.md), [AI decisions](docs/architecture/ai-decisions.md), [database](docs/architecture/database.md) |
| Use the API | [HTTP reference](docs/architecture/api.md), [OpenAPI](docs/architecture/openapi.yaml) |
| Author game content or compare controllers | [Content authoring](docs/developer/content-library.md), [AI comparisons](docs/developer/ai-comparisons.md) |
| Deploy, operate or recover an environment | [Environment operations](docs/operations/environments.md), [deployment](docs/operations/deployment.md) |

## Contributing and checks

Work branches and pull requests target `dev`; promotion to `main` is a separate
release action. See [contributor CI](docs/developer/ci.md) for the checks and
[AI development policy](docs/developer/ai-development-policy.md) for assisted changes.

```bash
./scripts/ci/lint-go.sh
./scripts/test/with-test-postgres.sh ./scripts/test/test-go-coverage.sh
(cd frontend && npm ci && npm test -- --coverage && npm run build)
(cd frontend && npx playwright install chromium && npm run test:e2e)
```

The PostgreSQL harness uses a throwaway Docker database. Go aggregate statement
coverage and each frontend coverage metric must reach 80%. Infrastructure changes
also require `./scripts/test/test-deployment.sh`. See
[contributor CI](docs/developer/ci.md) for browser prerequisites and additional gates.

## License

Copyright © 2026 Tan Wei Hao. All rights reserved. The source is shared for
portfolio and learning purposes; no license is granted to copy, modify or
distribute it.

Battle setup supports per-faction model and reasoning-effort choices from the operator
allowlist, including Claude Haiku, GPT-6 Luna and Antigravity Gemini 3.8 Flash
low/medium/high variants. The AI decision inspector shows elapsed waits and completed timing details;
see [model selection and latency](docs/architecture/ai-decisions.md#model-selection-and-latency)
and the [worker rollout order](docs/operations/cli-worker.md#model-selection-rollout).

For the optional CLI latency experiment, use `HOST_CLI_PREFETCH_CONTEXT=true` with
the host agent launcher; see [context-prefetch comparison](docs/operations/cli-worker.md#context-prefetch-comparison).
