# Environment configuration model

Use this reference to choose where a setting belongs and understand how it reaches
the application. For setup commands, use [local development](../developer/local-setup.md)
or [production setup](../operations/deployment.md#production-setup).

## Configuration sources

Deployment scripts and backend binaries read different inputs. The scripts turn
private application files into Kubernetes Secrets; binaries read their selected
YAML file and process environment at startup.

| Source | Consumer | Selection and responsibility |
| --- | --- | --- |
| Application `.env` | Deployment scripts | Dev uses the project-root file; production uses the file that `BATTLEFIELD_APPLICATION_FILE` names, or else `.env` beside its deployment profile. Supplies credentials and environment-specific settings using [`.env.example`](../../.env.example). Exported application credentials do not override this file during deployment. |
| `deployment.env` | Deployment scripts | `DEPLOY_ENV` selects `dev` or `production`; `BATTLEFIELD_ENV_FILE` can select another profile for that environment. Pins deployment identity and infrastructure endpoints using the [deployment template](../../infra/deploy/environments/deployment.env.example). |
| Backend YAML, such as [`backend/config.yaml`](../../backend/config.yaml) | API, engine, agent and AI gateway binaries | `-config` selects one file; the default is `config.yaml` relative to the process working directory. Defines listeners, timeouts, limits and model profiles. It is not merged with a deployment profile. |
| Kubernetes `battlefield-config` ConfigMap | Backend pods | Supplies the YAML mounted at `/etc/battlefield/config.yaml`, selected by the pod's `-config` argument. Its source is the [deployment ConfigMap](../../infra/deploy/base/infrastructure/configmap.yaml), not the local `backend/config.yaml`. |
| Process environment | Backend binaries | Supplies settings such as `DATABASE_URL`, authentication and provider credentials. Pods receive these from manifests and Secrets; standalone processes inherit them from their launcher. Binaries do not load `.env` or `deployment.env` themselves. |

YAML and environment variables have distinct fields rather than a general
“environment overrides YAML” rule. The [backend loaders](../../backend/internal/config)
validate each binary's settings and reject unknown YAML keys. For example,
database pool limits come from YAML, while the connection URL comes from
`DATABASE_URL`. Editing the project-root `.env` alone has no effect on a directly
launched `go run ./cmd/server` process. The registered CLI VM has a separate
[managed worker configuration workflow](../operations/cli-worker.md).

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
├── kubelet-ca.crt
├── recovery.json
└── tunnel-credentials.json
```

A machine that sets `BATTLEFIELD_APPLICATION_FILE`, such as the Jenkins agent, keeps
no `.env` in this directory.
`kubelet-ca.crt` is the trusted kubelet serving CA for production monitoring, and
`recovery.json` is the verified-release record that Jenkins writes after each
successful delivery; never edit it by hand.

Application files contain literal `KEY=value` lines: do not quote values,
interpolate variables, or source them as shell scripts. The
[shared parser](../../scripts/lib/application-env.cjs) rejects unknown and duplicate
settings. Each environment has its own credentials; private directories use mode
700 and credential files use mode 600.

The deployment profile holds the driver, context, cluster identity, gateway URL,
kubeconfig path and applicable Docker, tunnel and kubelet TLS settings. File paths
inside it are relative to the profile. Production uses `BF_DRIVER=k3s`; a profile
without `BF_DRIVER` selects Kind, which only the retained recovery profile uses.
Dev uses local Kind. See
[K3s delivery](../operations/k3s-delivery.md) for K3s registration requirements.

The [environment resolver](../../scripts/lib/deployment-environment.sh) verifies
the selected environment and the registered `kube-system` namespace UID before
deployment. It rejects conflicting cluster, URL, secret-file and image-source
overrides. Jenkins may bind its deployment kubeconfig; the live UID must still match.
Jenkins also binds the production application file through
`BATTLEFIELD_APPLICATION_FILE`, an absolute path that the resolver accepts only for
production.

## Secret rendering and restart behavior

Deployment classifies application fields into database, auth, AI, cache, runtime
and managed-Player Secrets. Pod manifests select which values each process
receives; cache and managed-Player settings are API-only. Deployment identity and
tunnel settings never enter application Secrets. Rendering uses temporary files
that are removed on exit.

After changing `.env`, rerun `./scripts/cd/deploy-local.sh` in dev, or run the
`main` pipeline with `APPLY_CONFIGURATION=true` in production. Both reapply
infrastructure and application configuration and restart application pods even
when images are unchanged. Backend YAML and environment settings are
loaded at startup; standalone processes must also be restarted after changes.
Editing a password file does not rotate the PostgreSQL role.

## Credential ownership

Kubernetes-generated observability credentials remain in Kubernetes; CI credentials
remain in Jenkins. Shared SSH and Cloudflare account credentials belong to their
platform workflows. The
[server rebuild contract](../operations/server-rebuild-contract.md#state-and-credential-locations)
lists every state and credential location, including database backups.

## Optional managed Players

Managed-Player configuration belongs in the private application `.env` and is
rendered into `battlefield-members`, consumed only by API pods. The
[Player invitation activation guide](../operations/player-invitations.md#enable-managed-players)
lists the required settings and validation rules. Once enabled, invitation changes
are stored in PostgreSQL and need no deployment.
