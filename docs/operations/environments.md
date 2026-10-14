# Production environment operations

Use this guide to register production, apply configuration and validate access.
For local Kind setup, see [local development](../developer/local-setup.md).
The [configuration model](../architecture/configuration.md) owns file layouts,
credential precedence, environment identity and Secret boundaries.

## Production setup

Production is not publicly launched by configuration setup.
For K3s, follow [K3s registration](k3s-delivery.md). For a Kind production
environment, run the following on its server:
```bash
./scripts/cd/setup-environment.sh production kind-battlefield
```

Create the private production `.env` from the shared template and fill in its own
database and provider credentials. Use `BATTLEFIELD_AUTH_MODE=cloudflare`,
Access issuer/audience/public origin and role assignments. Production rejects
development login modes. Use distinct worker and MCP capability secrets of at least 32 bytes.
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
Deployment extracts auth settings into a private temporary file while keeping
`BATTLEFIELD_AUTH_SECRET_FILE` pointed at the registered production `.env`, so
child policy-installation commands can repeat environment identity checks.

## Activate public access

Tunnel activation is a separate, explicit operation:
```bash
./scripts/cd/deploy-tunnel.sh
```
It validates production settings, generates resources temporarily, updates the
tunnel Secret, and rolls the connector only when its config, credentials, or image
changes. NetworkPolicies reconcile independently during production CD. Normal application deployments do not
activate public access. See [limited launch](limited-launch.md) for network and
Access requirements.

## Other managed credentials and validation

Production smoke checks use an enrolled Access service identity with read-only
permissions. Keep only its
`BATTLEFIELD_ACCESS_CI_CLIENT_ID` in the application `.env`; the Client Secret stays
in the Jenkins service credential. See the [CI service runbook](limited-launch.md#ci-service-authentication). Database migration backups and verified image
rollback remain operational features.
Failure drills still require a separately registered disposable dev cluster,
matching UID and explicit acknowledgement; production is forbidden.

Production monitoring requires `BF_KUBELET_CA_FILE` in `deployment.env`; see
[trusted kubelet certificates](observability.md#production-kubelet-tls). Successful
Jenkins deployments maintain a private `recovery.json` beside that profile. It is
bound to the registered cluster UID, exact verified image pair and applied Goose migration versions, and is not an
application Secret or a credential to copy into Git.


For managed-player configuration and Secret ownership, see
[the configuration model](../architecture/configuration.md#optional-managed-players)
and [Player invitations](player-invitations.md).
