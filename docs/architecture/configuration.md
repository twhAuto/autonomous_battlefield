# Environment configuration model

Both environments use the same application schema in [.env.example](../../.env.example).
Application files contain literal `KEY=value` lines: do not quote values, interpolate
variables, or source them as shell scripts. The shared parser rejects unknown and
duplicate settings. Each environment has its own credentials.

Production supports an explicit `BF_DRIVER=kind|k3s`; older profiles default to
Kind. Dev remains the local Kind workflow. K3s registration and its controller,
storage, TLS and cutover requirements are in [K3s delivery](../operations/k3s-delivery.md).
The profile pins the driver as well as the context and namespace UID.

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
worker and MCP capability credentials, and optional Redis/logging/telemetry settings.
`deployment.env` holds only deployment identity, Docker endpoint, cluster UID,
gateway URL, kubeconfig path, and production tunnel settings. See the
[deployment template](../../infra/deploy/environments/deployment.env.example).
Paths within the deployment profile are relative to that file.
Directories use mode 700; credential files use mode 600.

`BATTLEFIELD_ENV_FILE` can select a different deployment profile.
Production always reads `.env` beside that profile; dev always reads the
project-root `.env`. Application credential exports do not override the file.
The resolver verifies the selected environment and pinned cluster UID. It rejects
conflicting cluster, URL, secret-file, and image-source overrides. Jenkins may
bind its deployment kubeconfig; the live UID must still match.

## Secret rendering and restart behavior

Deployment renders distinct database, auth, AI, API-only cache and runtime Kubernetes Secrets
using explicit field classification. Cluster and tunnel settings never enter
application Secrets. Temporary files are removed on exit. Application and
infrastructure configuration are applied together; application pods restart
when configuration is reapplied, even if images did not change.
Editing a password file does not rotate the PostgreSQL role.

## Credential ownership

Kubernetes-generated observability credentials remain in Kubernetes, CI
credentials remain in Jenkins, and database backups remain under the configured
backup storage. Shared SSH and Cloudflare account credentials are not application
configuration and are not relocated.

## Optional managed Players

`BATTLEFIELD_MANAGED_PLAYERS` and `BATTLEFIELD_MEMBERS_ACCOUNT_ID`,
`BATTLEFIELD_MEMBERS_GROUP_ID`, `BATTLEFIELD_MEMBERS_API_TOKEN` live in the private
application `.env`. Environment extraction stages them in the separate
`battlefield-members` Secret consumed only by API pods. Configuration delivery
restarts the API; invitation changes after activation are stored in PostgreSQL
and need no deployment. See [Player invitations](../operations/player-invitations.md).

For setup and validation, use [local development](../developer/local-setup.md)
or [production environment operations](../operations/environments.md).
