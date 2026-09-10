# Dev and production configuration

Both environments use the same application schema in [.env.example](../../.env.example).
Application files contain literal `KEY=value` lines: do not quote values, interpolate
variables, or source them as shell scripts. The shared parser rejects unknown and
duplicate settings. Each environment has its own credentials.

## File layout

Dev:
```text
<project>/.env
~/.config/autonomous-battlefield/dev/deployment.env
~/.config/autonomous-battlefield/dev/kubeconfig
```

Production:
```text
~/.config/autonomous-battlefield/production/
├── .env
├── deployment.env
├── kubeconfig
└── tunnel-credentials.json
```

The application `.env` holds database URLs, authentication, provider settings,
worker/MCP credentials, and optional Redis/logging/telemetry settings.
`deployment.env` holds only deployment identity, Docker endpoint, cluster UID,
gateway URL, kubeconfig path, and production tunnel settings. See the
[deployment template](../../deploy/environments/deployment.env.example).
Paths within the deployment profile are relative to that file.
Directories use mode 700; credential files use mode 600.

`BATTLEFIELD_ENV_FILE` can select a different deployment profile.
Production always reads `.env` beside that profile; dev always reads the
project-root `.env`. Application credential exports do not override the file.
The resolver verifies the selected environment and pinned cluster UID. It rejects
conflicting cluster, URL, secret-file, and image-source overrides. Jenkins may
bind its deployment kubeconfig; the live UID must still match.

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

## Production setup

Production is not publicly launched by configuration setup.
On the production server:
```bash
./scripts/cd/setup-environment.sh production kind-battlefield
```

Create the private production `.env` from the shared template and fill in its own
database and provider credentials. Use `BATTLEFIELD_AUTH_MODE=cloudflare`,
Access issuer/audience/public origin and role assignments. Production rejects
development login modes. Use distinct worker and MCP secrets of at least 32 bytes.
Keep provider availability false until intentionally configured; CLI availability
requires verified isolation.

Add `BF_TUNNEL_ID`, `BF_TUNNEL_CREDENTIALS_FILE=tunnel-credentials.json`,
and a digest-pinned `BF_TUNNEL_IMAGE` to production `deployment.env`.
Install this application's tunnel credential JSON at that relative path.
Validate application configuration and tunnel rendering without public activation:
```bash
./scripts/ops/prepare-limited-production.sh
```

Jenkins `main` delivery uses verified registry image digests, the production
profile on its agent, and the private production `.env`. Jenkins retains registry,
signing, smoke-test Access-token and stage-scoped kubeconfig credentials; it no
longer binds a separate database file. For a change only to production `.env`,
run the main pipeline with `APPLY_CONFIGURATION=true`.

## Runtime configuration

Deployment renders distinct database, auth, AI, API-only cache and runtime Kubernetes Secrets
using explicit field classification. Cluster and tunnel settings never enter
application Secrets. Temporary files are removed on exit. Application and
infrastructure configuration are applied together; application pods restart
when configuration is reapplied, even if images did not change.
Editing a password file does not rotate the PostgreSQL role.

Tunnel activation is a separate, explicit operation:
```bash
./scripts/cd/deploy-tunnel.sh
```
It validates production settings, generates resources temporarily, updates the
tunnel Secret, and restarts the tunnel. Normal application deployments do not
activate public access. See [limited launch](limited-launch.md) for network and
Access requirements.

## Converting an existing checkout

The one-time conversion tool reads the old profile as data without executing it:
```bash
python3 scripts/ops/consolidate-config.py dev
python3 scripts/ops/consolidate-config.py dev --apply
```
Use `production` on the server. A pre-existing dev root `.env` is authoritative.
Production conflicting inputs fail for resolution. After validation, conversion
removes the old profile and split inputs; it creates no backups or runtime
fallbacks and does not modify databases or workloads.

## Other managed credentials and validation

Kubernetes-generated observability credentials remain in Kubernetes, CI
credentials remain in Jenkins, and database backups remain under the configured
backup storage. Shared SSH and Cloudflare account credentials are not application
configuration and are not relocated.

Run environment, deployment-component, Kind deployment, production-preparation,
tunnel, command-wrapper and runtime-assurance regression checks. Dev smoke checks
cover readiness, authentication and scenario access; production smoke checks use
an invited user's Access token. Database migration backups and verified image
rollback remain operational features, independent of this configuration cleanup.
Failure drills still require a separately registered disposable dev cluster,
matching UID and explicit acknowledgement; production is forbidden.
