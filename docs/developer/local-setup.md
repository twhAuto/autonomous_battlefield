# Local development setup

Use the registered Kind environment to build and run working-tree changes.
The [configuration model](../architecture/configuration.md) describes file locations,
credential precedence and cluster identity checks. For production, see
[environment operations](../operations/environments.md).

## WSL setup and normal use

Requirements: Docker, Kind, kubectl, Bash, Node.js and Python 3.
Register an existing dev cluster once:
```bash
./scripts/cd/setup-environment.sh dev kind-battlefield
./scripts/cd/prepare-dev-secrets.sh
./deploy-local.sh
```

Open `http://battlefield.localhost:8888`. Edit the root `.env`, then rerun
`./deploy-local.sh`. Existing `.env` values are validated and preserved.
For a missing `.env`, preparation recovers existing Kubernetes credentials or
generates initial dev secrets. It never invents a database password for existing
PVCs with missing credentials.

For a stopped or absent initial cluster, use `./scripts/cd/create-kind-cluster.sh`.
It reuses registered clusters and rejects `--recreate`. Registration never
overwrites an existing profile or kubeconfig.


## Standalone process ports

When running processes outside Kind, the defaults in
[`backend/config.yaml`](../../backend/config.yaml),
[`frontend/vite.config.ts`](../../frontend/vite.config.ts) and
[`backend/compose.yaml`](../../backend/compose.yaml) are:

| Port | Process |
| --- | --- |
| `5173` | Vite frontend |
| `8080` | Public API |
| `8081` | Engine health and metrics |
| `8083` | Agent health |
| `8084` | MCP and internal agent control plane |
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
