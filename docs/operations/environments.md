# WSL dev and server production

The WSL machine runs dev. The server at `192.168.1.104` runs production, Jenkins,
and SonarQube. Both machines retain their physical `battlefield` Kind cluster and
`battlefield` namespace. No cluster, PVC, database, or gateway is renamed.

| Setting | WSL dev | Server production |
| --- | --- | --- |
| Context in dedicated kubeconfig | `battlefield-dev` | `battlefield-production` |
| `DEPLOY_ENV` | `dev` (local default) | `production` (explicit) |
| Image source | Working-tree images loaded into Kind | Verified GHCR digests |
| Trigger | `./deploy-local.sh` | Successful Jenkins `main` delivery |
| Default components | Database/config, migrations, application, gateway | Existing component-aware delivery, including observability |
| Secrets | Independent private dev files | Existing server files/Jenkins credentials/cluster Secrets |

## Configuration and identity

Run commands from the repository root. Registration creates a private, flattened
kubeconfig with one context alias and an `environment.env` file under
`~/.config/autonomous-battlefield/dev/` or `production/`. It leaves the original
kubeconfig and current context untouched. These are trusted local shell files,
not files to commit, source from untrusted repositories, or archive in Jenkins.
The credential-free environment profile template is
[`deploy/environments/environment.env.example`](../../deploy/environments/environment.env.example).
Secret references live beside it: [database.env.example](../../deploy/environments/database.env.example),
[auth.env.example](../../deploy/environments/auth.env.example), and
[ai.env.example](../../deploy/environments/ai.env.example). Actual secrets stay in
the private configuration directory; do not copy them into the manifest tree.

`BF_UID` pins the `kube-system` namespace UID. All deployment entry points compare
it with the actual cluster before operating. Local deployment also checks that
the registered Docker daemon's Kind cluster has the same UID. Conflicting
context, cluster, namespace, image-source, verification, URL, or Docker overrides
fail rather than silently redirecting deployment. Dev requires a Unix Docker
socket and refuses production secret-file overrides. These checks prevent
accidents; anyone with Kubernetes administrator credentials still has direct
cluster access outside these scripts.

`BATTLEFIELD_ENV_FILE` selects a non-default profile. `BATTLEFIELD_CONFIG_DIR`
selects the directory when registering; afterward set `BATTLEFIELD_ENV_FILE` to
that directory's `environment.env`. `KUBECONFIG` may be Jenkins's private copy of
the registered kubeconfig; its context and UID must still match. Production
credential bindings can override database, AI, and auth file locations. Missing
production AI/auth files preserve the existing cluster Secrets; missing Secrets
fail deployment rather than importing dev settings.

## WSL setup and normal use

Requirements: Docker with WSL access, Kind, kubectl, Bash, Node.js, and Git.
Python 3 is also required to run the deployment-environment regression tests.

For the existing, running WSL cluster, register it once:

```bash
./scripts/cd/setup-environment.sh dev kind-battlefield
./scripts/cd/prepare-dev-secrets.sh
./deploy-local.sh
```

Registration refuses to overwrite an existing profile or dedicated kubeconfig.
Dev secret preparation is repeatable: it preserves existing files and exports
existing PostgreSQL credentials into a private file. For an empty cluster it
creates new database credentials, but refuses to invent credentials if PVCs
already exist. It generates independent dev login/session, worker-control and MCP capability
secrets; API and CLI provider profiles initially remain disabled. Read the dev
login locally from `~/.config/autonomous-battlefield/dev/auth.env`. Enable paid
providers only by editing the private dev `ai.env` intentionally.

For a stopped cluster or first-time setup, start Docker, then run:

```bash
./scripts/cd/create-kind-cluster.sh
./scripts/cd/prepare-dev-secrets.sh
./deploy-local.sh
```

Bootstrap starts stopped existing Kind nodes, reuses healthy nodes, and creates
only when neither a registration nor cluster exists. A registered but missing
cluster requires recovery; it is never silently recreated. `--recreate` is no
longer supported. Normal deployment does not start, delete, or recreate clusters.

`./deploy-local.sh` builds dirty working-tree changes, loads both images, runs the
migration gate, applies database/configuration and gateway resources, restarts
dirty-build application workloads, and performs application smoke checks at
`BF_URL` (default `http://battlefield.localhost:8888`). Observability checks are
skipped by default; existing observability resources and volumes are left alone.
Opt into the complete stack with:

```bash
DEPLOY_COMPONENTS=all SMOKE_TEST_OBSERVABILITY=1 ./deploy-local.sh
```

For direct inspection, always select the dedicated config and context:

```bash
KUBECONFIG="$HOME/.config/autonomous-battlefield/dev/kubeconfig" \
  kubectl --context battlefield-dev -n battlefield get pods
```

## Server registration and migration

Perform these steps as the server's Jenkins agent account; do not run dev
bootstrap or copy WSL kubeconfigs/secrets to the server.

1. Inventory the existing `kind-battlefield` cluster: `kube-system` UID, PVCs,
   deployment image references, and secret names. Record the current backend and
   frontend digests in the existing rollback records. Keep its database and
   application access arrangement unchanged.
2. Export the production database before the first rollout. From the server:

   ```bash
   umask 077
   mkdir -p "$HOME/battlefield-backups"
   kubectl --context kind-battlefield -n battlefield exec battlefield-postgres-0 -- \
     sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
     > "$HOME/battlefield-backups/pre-environment-split.dump"
   test -s "$HOME/battlefield-backups/pre-environment-split.dump"
   ```

   Copy that backup to existing off-server storage and validate it with
   `pg_restore --list` before proceeding. Automated scheduling/retention is a
   follow-up. Image rollback does not undo database migrations.
3. Register the server cluster once:

   ```bash
   ./scripts/cd/setup-environment.sh production kind-battlefield
   ```

   The default smoke target remains the existing server-local gateway. If the
   existing access uses a different URL, supply `TARGET_URL` during registration.
   Existing Secure-cookie authentication requires an HTTPS target. Preserve
   existing gateway/tunnel configuration; this change adds no LAN/public access.
4. Set the registered profile's database/AI/auth paths to existing server files
   where applicable. Keep `battlefield-local-secret-env`: Jenkins still binds it
   as `BATTLEFIELD_SECRET_FILE`. Existing auth and AI Kubernetes Secrets are
   retained if their configured files are absent. Never use the dev example
   password or session key on production.
5. Add label `battlefield-production` to the existing server Jenkins agent.
   Both pipeline files select this label. Ensure Node.js and Python 3 are
   installed alongside existing build tools.
6. Create Jenkins **Secret file** credential `battlefield-production-kubeconfig`
   from the generated production kubeconfig. Keep existing GHCR, signing, and
   database credential IDs. The dedicated kubeconfig is bound only inside the
   main pipeline's **Production rollout** stages, not PR or build stages.
   The profile must exist in the agent account's default config directory.
7. Trigger delivery through the usual PR process. Observe **Publish immutable images**,
   **Deploy production**, verification, and runtime assurance. Confirm recorded
   deployment digests match GHCR and the production PVC is unchanged.
8. Run another local dev deployment and compare production's recorded image
   references to prove that local iteration does not change them. Remote checks
   remain pending until actual server evidence is collected.

Server runtime checks can be run with `DEPLOY_ENV=production`; they use the
registered target URL. Standalone production rollback requires both digest
references, GHCR credentials, and the reviewed Cosign public key; it verifies
signatures and attestations before updating images. No tag-only or implicit
`rollout undo` production rollback is permitted.

The assurance pipeline's nightly mode remains offline. Its load mode explicitly
uses production. Failure drills require `DISPOSABLE_ENV_FILE` pointing to a
separately registered **dev** cluster profile with `BF_DISPOSABLE=1`, the existing
explicit destructive-drill acknowledgement, and a matching cluster UID. Never
mark the normal dev data or production cluster disposable.

## Validation and recovery

The migration job requires the `battlefield-auth` Secret because it validates
server configuration before connecting to PostgreSQL. Dev staging sets its
`BATTLEFIELD_ENV=dev`; production keeps `production`. CLI profiles are disabled
in the migration job. If deployment waits at the migration gate, inspect its pods
and logs using the dedicated kubeconfig/context before changing database credentials.

Run `scripts/test/test-deployment-environment.sh` alongside deployment-component,
Kind deployment, command-wrapper, runtime-assurance, and security-launcher tests.
These cover guard failures, bootstrap behavior, render/staging behavior, and
launcher failure propagation. Application-only smoke testing checks readiness,
login when enabled, and the scenario route without requiring observability.

If identity verification fails, inspect the dedicated kubeconfig and server
inventory. Do not replace `BF_UID` merely to bypass the error. Recover the
original cluster or deliberately register a restored cluster after verifying
its data. Keep the old profile until recovery is validated. A lost Kind node
can mean a lost PostgreSQL volume; neither cluster recreation nor image rollback
restores that data.

## Limited-access production authentication

Production API pods require Cloudflare Access. Use the private rendered `auth.env`
as `BF_AUTH_FILE`; local password and disabled-development modes are rejected.
`battlefield-ai` now needs an independent `BATTLEFIELD_AGENT_SECRET` in addition
to the MCP capability-signing secret. Only the MCP service receives both. Developer
CLI profiles additionally require the private API setting
`BATTLEFIELD_CLI_ISOLATION_VERIFIED=true` after verified VM provisioning.
See [limited-access launch](limited-launch.md) for the `play.ordinalfrontier.com` rollout,
Google allowlist/MFA, migration backups, smoke-token setup and recovery gates.
