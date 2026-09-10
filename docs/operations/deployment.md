# Deployment learning guide

Deployment targets are now explicit: WSL dev and server production. Start with
the [environment setup and transition guide](environments.md). The `local` overlay
name below denotes shared Kind manifests used by both environments.

This guide records the deployment direction for Autonomous Battlefield and the
recommended path from local Kubernetes to AWS. The goal is to learn the native
Kubernetes concepts first while keeping the application portable enough to run
on Amazon EKS without rewriting it.

The application has production-shaped container images and a staged local
Kubernetes deployment. The AWS infrastructure reference is in `infra/aws`; the application still needs
an AWS overlay and account-specific provisioning and verification.

The [deployment structure review](deployment-review.md) records the cleanup,
remaining operational gaps, and optional simplifications.

## Limited-access production

For `play.ordinalfrontier.com`, follow [limited-access launch](limited-launch.md). Production
requires Cloudflare mode and an Access application audience/issuer/origin and private
role assignments. The API manifest fails startup on missing authentication config;
development staging explicitly substitutes the dev environment. The tunnel exposes
only frontend/API. CLI availability remains false until VM verification.

Production migrations create and inspect a PostgreSQL backup before applying SQL.
Production smoke requires `BATTLEFIELD_ACCESS_TOKEN_FILE` from an invited identity;
it uses private origin port forwards and never logs in with a legacy password.
Production rollback requires an exact previously verified Access-compatible backend
digest in `BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST`, in addition to existing
image signature/provenance checks. Failure keeps the application unavailable.

## Deployment direction

Use the same application containers and core Kubernetes resources locally and
on AWS:

See the [system context](../design/system-architecture.md#2-system-context)
for the API, engine, MCP, agent, PostgreSQL, and optional Redis topology.
PostgreSQL remains the durable queue in both deployment environments.

For a first AWS deployment, use:

- Amazon ECR for container images.
- Amazon EKS for Kubernetes.
- Amazon RDS for PostgreSQL.
- An Application Load Balancer for HTTPS ingress.
- AWS Secrets Manager for database credentials.
- CloudWatch or OpenTelemetry for operational signals.
- Route 53 and ACM when a public domain and TLS certificate are needed.

Keep PostgreSQL outside EKS in AWS. Running a production database inside the
cluster introduces storage, backup, failover, and upgrade responsibilities that
are separate from the application-deployment exercise.

## Portable base and environment overlays

Start with native YAML resources and organize them with Kustomize:

```text
deploy/
├── base/
│   ├── infrastructure/     Namespace and non-secret configuration
│   ├── migration/          One-shot schema migration Job
│   └── application/        Workloads, Services, Ingress, HPA, and PDBs
└── overlays/
    └── local/
        ├── infrastructure/ PVC-backed PostgreSQL and staged Secrets
        ├── migration/      Local migration composition
        ├── application/    Local application composition
        ├── observability/  Composed metrics and logging backends
        │   ├── prometheus/ Metrics collection and alert rules
        │   ├── kube-state-metrics/  Namespaced Kubernetes object state
        │   ├── grafana/    Metrics UI and dashboard provisioning
        │   ├── otel-collector/  OTLP trace receiver
        │   ├── tempo/      Trace storage
        │   ├── elasticsearch/  Persistent log storage
        │   ├── kibana/     Logging UI and data-view provisioning
        │   └── collector-rbac/  Collector access to application pod metadata
        ├── logging-collector/  Node log collector and isolated namespace
        └── gateway/        Local Gateway API routes through Envoy Gateway
```

The base should contain standard Kubernetes behavior. Provider-specific
annotations, resource identifiers, and secret integrations belong in the AWS
overlay.

| Portable Kubernetes resource | Local implementation | AWS implementation |
| --- | --- | --- |
| Container image | Image loaded into `kind` | Image pushed to ECR |
| API and engine | `Deployment` | The same `Deployment` on EKS |
| Internal networking | `Service` DNS | The same `Service` DNS on EKS |
| Database | PVC-backed development PostgreSQL | RDS PostgreSQL |
| Database credential | Uncommitted local Secret | Secrets Manager integration |
| Public access | Loopback-only Envoy Gateway | ALB-backed Ingress |
| Non-secret settings | `ConfigMap` | The same `ConfigMap`, with an overlay if needed |

Kustomize is already included in `kubectl`. Render each stage before applying
it when reviewing a change. The infrastructure overlay requires staged private
secret files and cannot render directly from a clean checkout. Use
`./scripts/test/test-kind-deployment.sh` for disposable render fixtures; deployment
stages registered secret inputs into temporary overlays. The remaining overlays
can be rendered without credentials:

```bash
kubectl kustomize deploy/overlays/local/migration
kubectl kustomize deploy/overlays/local/application
kubectl kustomize deploy/overlays/local/observability
kubectl kustomize deploy/overlays/local/logging-collector
kubectl kustomize deploy/overlays/local/gateway
```

## Why Helm comes later

Do not begin by translating every resource into a Helm template. First write,
deploy, inspect, and troubleshoot the Kubernetes resources directly. This keeps
the revision exercise focused on what Kubernetes itself is doing.

Add a Helm chart later when one or more of these becomes true:

- Other users need to install the application as a packaged release.
- Many deployments need configurable domains, images, replicas, resources, and
  feature settings.
- The project needs chart versions and Helm install, upgrade, and rollback
  workflows.
- Kustomize overlays become repetitive or difficult to maintain.

Converting working resources into a chart is then a useful packaging exercise,
not a prerequisite for the first deployment.

## First local Kubernetes session

The recommended local cluster is
[`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/). It runs Kubernetes
nodes as Docker containers and works well for application development and
deployment exercises.

### 1. Verify the existing prerequisites

From WSL, confirm Docker and `kubectl` are usable:

```bash
docker info
kubectl version --client
```

If `docker info` cannot reach the daemon, start Docker Desktop and enable its
WSL integration for the current distribution before continuing.

### 2. Install `kind`

With Go already installed, install the current pinned `kind` release:

```bash
go install sigs.k8s.io/kind@v0.32.0
export PATH="$(go env GOPATH)/bin:$PATH"
kind version
```

Add the Go binary directory to the shell profile if it is not already on
`PATH`. The upstream quick-start page should be used when intentionally updating
the pinned version.

### 3. Register or resume WSL dev

Follow the [environment setup guide](environments.md) to register the existing
cluster, prepare independent dev secrets, and deploy. For a stopped or absent
dev cluster, `./scripts/cd/create-kind-cluster.sh` starts existing nodes or creates
a first cluster. It never recreates a registered cluster and rejects `--recreate`.

The dedicated kubeconfig uses context `battlefield-dev`. Production uses
`battlefield-production` on the server; neither deployment depends on your
current kubectl context. Both keep the physical Kind name `battlefield`.

## Deploy the application locally

### 1. Prepare private dev secret files

After registering dev, run `./scripts/cd/prepare-dev-secrets.sh`. It preserves
existing PostgreSQL credentials and creates independent authentication and
capability secrets in the project-root `.env`. An existing `.env` is never overwritten. `./deploy-local.sh`
renders separate Kubernetes Secrets from `.env` and restarts application pods.
Production uses the same application format in its private `.env`, beside
`deployment.env`. See [environment setup](environments.md) for both layouts.

Set `BATTLEFIELD_AUTH_COOKIE_SECURE=false` for the checked-in loopback HTTP
gateway and `true` before publishing the application through an HTTPS tunnel.
Set `BATTLEFIELD_AUTH_MODE=local-password` for dev login. To disable dev login,
explicitly select `disabled-development` and set `BATTLEFIELD_AUTH_ENABLED=false`.
Production requires Cloudflare Access and rejects both development modes.

The setup command generates or recovers MCP and worker secrets in `.env`.
New provider profiles start unavailable, so scripted battles need no provider
credentials. The deployment reuses `DATABASE_URL` for MCP when `.env` does not
contain `MCP_DATABASE_URL`.

To enable an API provider, edit the root `.env` using [`.env.example`](../../.env.example) as a reference, set
a strong capability secret, add the provider key and model, and change that
provider's `*_PROFILE_AVAILABLE` value to `true`. Production should always
supply explicit secrets and point `MCP_DATABASE_URL` at a login granted only
the `battlefield_mcp` role created by the decision migration.
The production migration principal therefore needs `CREATEROLE` for the first
application of migration `00004`; the MCP workload login itself must not have
that privilege.

The `CODEX_CLI_PROFILE_AVAILABLE` and `GEMINI_CLI_PROFILE_AVAILABLE` values
should normally remain `false` in the secret file. For a local kind battle,
`run-k8s-cli-agent.sh` manages these flags while an authenticated host worker is
connected:

```bash
./run-k8s-cli-agent.sh codex
```

The helper supports `auto`, `codex`, `antigravity`, and `both` (`gemini` is a compatibility alias).
The retained `GEMINI_CLI_PROFILE_AVAILABLE` flag now controls Antigravity.
Follow [Antigravity setup](antigravity-cli.md) to install and authenticate it. It verifies the
tested CLI version and existing host login, forwards only the internal MCP
service to loopback, runs the decision agent on the host, and temporarily makes
the connected profiles selectable through the API. No CLI authentication
directory or provider credential is mounted into the cluster. Keep the helper
running for the duration of the battle and press Ctrl+C afterward. The generated
host-agent profile passes `--model gpt-5.6-luna` to Codex or `--model gemini-3.7-flash-medium` to
Antigravity, matching the allowlisted profile metadata deployed to the API.

Do not reuse these credentials outside the disposable local cluster.

### 2. Build commit-addressed images

The normal Kubernetes workflow tags each changed application image with the
full Git commit SHA. Clean builds identify their source revision; local dirty
builds require an explicit opt-in and must not be published as release evidence.

`VERSION` contains the release line in semantic `major.minor.patch` form. The
initial version is `0.1.0`. When a CI build number is present, the build also
adds a human-readable alias to the same image:

```text
autonomous-battlefield-backend:0.1.0-build.27
autonomous-battlefield-frontend:0.1.0-build.27
```

Jenkins supplies `BUILD_NUMBER` automatically. GitHub Actions supplies
`GITHUB_RUN_NUMBER` and `GITHUB_RUN_ATTEMPT`; the script recognizes both CI
systems. `CI_BUILD_NUMBER` and `CI_BUILD_ATTEMPT` are provider-neutral explicit
overrides.

Commit the intended source first and confirm the worktree is clean:

```bash
git status --short
./scripts/ci/build-images.sh
```

To simulate CI build 27 locally after committing the changes:

```bash
CI_BUILD_NUMBER=27 ./scripts/ci/build-images.sh
```

The build refuses a dirty worktree by default because a Git SHA cannot identify
uncommitted source. For an intentional local experiment only, set
`ALLOW_DIRTY=1`; do not publish an image produced that way.

The images include OCI revision, version, and source labels. Their names look
like:

```text
autonomous-battlefield-backend:<full-git-sha>
autonomous-battlefield-frontend:<full-git-sha>
```

Local dev deploys the full-SHA tag loaded into Kind. Jenkins production deploys
verified `repository@sha256:...` references from GHCR. Build-number aliases are
for human navigation and are not production deployment identities.

### Release tags on GitHub

Do not create a Git tag for every Jenkins or GitHub Actions build. Create an
annotated Git tag only for an intentional release, using the value in `VERSION`:

```bash
git tag -a "v$(<VERSION)" -m "Autonomous Battlefield v$(<VERSION)"
git push origin "v$(<VERSION)"
```

After it is pushed, the tag appears on GitHub and can be used to create a GitHub
Release. Increment `VERSION` deliberately: patch for compatible fixes, minor for
compatible features, and major for breaking releases.

### 3. Deploy that exact revision

The deployment script defaults to a full deployment of the checked-out commit:
it loads both tagged images into `kind`, applies infrastructure, runs the
migration gate, waits for the application rollouts, deploys Prometheus,
Grafana, Fluent Bit, Elasticsearch, and Kibana, and programs the local Gateway
API routes:

```bash
./scripts/cd/deploy-kind.sh
```

Application rollouts default to five minutes. The observability stack has a
separate 15-minute budget because a new single-node Kind cluster may serialize
the Prometheus, Grafana, Elastic, OpenTelemetry, and Tempo image pulls. Override
these with positive seconds values such as `ROLLOUT_TIMEOUT=600s` and
`OBSERVABILITY_ROLLOUT_TIMEOUT=1200s` on a slower connection.
Each observability Deployment must finish its new rollout; waits run concurrently
within that budget. Cancellation stops outstanding rollout waits. Timeout
output includes the failed workload descriptions, container logs,
and recent namespace events.

Pass a previous full SHA to deploy a locally built revision explicitly:

```bash
./scripts/cd/deploy-kind.sh "$(git rev-parse HEAD)"
```

For an incremental manual deployment, set `DEPLOY_COMPONENTS`. The image build
and scan scripts consume the same value and ignore non-image components:

```bash
# Frontend source changed: build, scan, and roll only the frontend.
DEPLOY_COMPONENTS=frontend ./scripts/ci/build-images.sh
DEPLOY_COMPONENTS=frontend ./scripts/ci/scan-images.sh "$(git rev-parse HEAD)"
DEPLOY_COMPONENTS=frontend ./scripts/cd/deploy-kind.sh "$(git rev-parse HEAD)"

# Only dashboards, alert rules, or observability manifests changed.
DEPLOY_COMPONENTS=observability ./scripts/cd/deploy-kind.sh "$(git rev-parse HEAD)"
```

Supported components are `infrastructure`, `migration`, `backend`, `frontend`,
`application`, `observability`, and `gateway`. Selecting `backend` also runs the
migration gate and applies the application overlay; selecting `frontend` also
applies the application overlay. `all` preserves the original full-deployment
behavior and `none` exits without changing the cluster. Empty entries, trailing
commas, whitespace, and unknown names are rejected. Deployment scratch space
contains only selected overlays and their dependencies; local secret files are
staged explicitly only when infrastructure is selected.

Jenkins calculates this value with
`scripts/cd/detect-deployment-components.sh`, comparing the current revision with
the previous successful build. An unavailable baseline intentionally selects
`all`; documentation-only changes select `none`. Any path not covered by an
explicit classifier rule also selects `all` and writes a warning to the build
log. This fail-safe means a newly added directory cannot silently skip a needed
deployment; add a targeted or known-non-deploying rule once its ownership is
understood.

#### Change-to-deployment mapping

Changes under `scripts/test/` and `tests/`, Go `*_test.go` files, React/TypeScript `*.test.ts` and `*.test.tsx` files under `frontend/src`, and frontend test setup do not select application deployment components. These changes still run the normal CI quality gates. Renames inspect both old and new paths, so moving production code into a test path still selects the production component. Unknown paths retain the conservative full-deployment fallback.

The path classifier evaluates the following rules from top to bottom for each
path, so an earlier documentation rule takes precedence over a broader source
folder rule. Multiple changed paths contribute the union of their components;
`all` takes precedence over every targeted selection.

| Changed path | Selected components | Image build and scan |
| --- | --- | --- |
| Any `*.md`, `LICENSE`, `LICENSE.*`, or root `.gitignore` | `none` | None |
| `quality/**` or `sonar-project.properties` | `none` | None |
| `scripts/test/**` and `tests/**` | `none` | None |
| Go `backend/**/*_test.go`, frontend `src/**/*.test.ts`, `src/**/*.test.tsx`, or `src/test/**` | `none` | None |
| CI lint/coverage summary and existing test compatibility wrappers (`test-*`, `fuzz-smoke`, `with-test-postgres`) | `none` | None |
| `backend/**` | `migration,backend,application` | Backend image |
| `frontend/**` | `frontend,application` | Frontend image |
| `deploy/base/infrastructure/**` or `deploy/overlays/local/infrastructure/**` | `infrastructure` | None |
| `deploy/base/migration/**` or `deploy/overlays/local/migration/**` | `migration` | None; uses the deployed backend image |
| `deploy/base/application/**` or `deploy/overlays/local/application/**` | `application` | None; preserves deployed image tags |
| `deploy/overlays/local/observability/**` or `deploy/overlays/local/logging-collector/**` | `observability` | None |
| `deploy/overlays/local/gateway/**` | `gateway` | None |
| `VERSION` | `migration,backend,frontend,application` | Both application images |
| CI image build or scan scripts | `all` | Both application images |
| Anything under `scripts/cd/**` or `scripts/lib/**` | `all` | Both application images |
| `deploy/kind.yaml` | `all` | Both application images |
| Any other or newly introduced path | `all`, with a warning | Both application images |
| Missing or invalid previous-successful revision | `all` | Both application images |

The selected components have these deployment effects:

| Component | Kubernetes action |
| --- | --- |
| `infrastructure` | Apply namespace configuration, Secret, and PostgreSQL; wait for PostgreSQL rollout. |
| `migration` | Recreate the migration Job with the selected or currently deployed backend image and wait for completion. |
| `backend` | Load the backend image into kind; automatically includes `migration` and `application`. |
| `frontend` | Load the frontend image into kind; automatically includes `application`. |
| `application` | Apply API, engine, agent, internal MCP, frontend, Services, network policy, autoscaling, and disruption-budget resources; wait for application rollouts. |
| `observability` | Apply Prometheus, kube-state-metrics, Grafana, Fluent Bit, Elasticsearch, Kibana, dashboards, data views, recording rules, and alerts; wait for readiness. |
| `gateway` | Apply Envoy Gateway routes and wait for the local Gateway to become programmed. |
| `all` | Execute every action above. |
| `none` | Build, scan, and deployment commands exit without changing Kubernetes. |

Only the existing named CI test wrappers are exempt; new CI scripts still use
the unknown-path fallback until explicitly classified.

Renames are evaluated as one deletion plus one addition, so both the old and new
locations influence the result. Renaming a production Go file to `*_test.go`
therefore still deploys the backend removal. When adding or repurposing a folder, update the
classifier, its tests, and this table together. Until then, the unknown-path
fallback safely selects `all`.

The script composes temporary Kustomize overlays with the selected backend and
frontend tags, leaving the tracked portable manifests unchanged. An unchanged
component retains its currently deployed tag. Because the local Deployments use
`imagePullPolicy: IfNotPresent`, Kubernetes uses commit-addressed images loaded
into the `kind` node rather than an external registry.

Automation is grouped by pipeline responsibility: `scripts/ci` contains lint,
coverage, image build and scan, and classifier tests; `scripts/cd` contains
cluster setup, deployment-scope detection, deploy, rollback, and smoke-test
commands. `scripts/cd/deploy-kind.sh` is the public deployment orchestrator. Its
internal functions live in `scripts/cd/lib/kind-deployment.sh`, while
`scripts/lib/deployment-components.sh` provides the component-selection rules
shared by CI and CD. Files under either `lib` directory are sourced libraries
and are not intended to be run directly.

The following sections show individual stages for diagnosis. Direct `kubectl`
commands do not use the scripts' environment resolver. In a dedicated dev shell,
select the registered kubeconfig before running the inspection or cleanup commands:

```bash
export KUBECONFIG="$HOME/.config/autonomous-battlefield/dev/kubeconfig"
kubectl config current-context  # must be battlefield-dev
```

For production inspection, use the production profile as described in
[environments](environments.md); local cleanup commands below are for dev.

### 4. Inspect PostgreSQL and configuration

```bash
kubectl rollout status statefulset/battlefield-postgres \
  --namespace battlefield \
  --timeout=120s
```

The local PostgreSQL StatefulSet uses a 1 GiB persistent volume from `kind`'s
default local-path provisioner. It is suitable for learning only.

### 5. Inspect the migration gate

The deployment script deletes the previous completed Job, recreates it with the
selected backend image, and waits for success. Inspect the result with:

```bash
kubectl wait --for=condition=complete \
  job/battlefield-migrate \
  --namespace battlefield \
  --timeout=120s

kubectl logs job/battlefield-migrate --namespace battlefield
```

Do not apply the application stage if the Job fails.

### 6. Inspect the application

```bash
kubectl rollout status deployment/battlefield-api \
  --namespace battlefield \
  --timeout=120s
kubectl rollout status deployment/battlefield-engine \
  --namespace battlefield \
  --timeout=120s
kubectl rollout status deployment/battlefield-agent \
  --namespace battlefield \
  --timeout=120s
kubectl rollout status deployment/battlefield-mcp \
  --namespace battlefield \
  --timeout=120s
kubectl rollout status deployment/battlefield-frontend \
  --namespace battlefield \
  --timeout=120s

kubectl get pods,services,jobs,persistentvolumeclaims \
  --namespace battlefield
```

The base application retains a portable Ingress for later cloud overlays. The
local overlay removes that unhandled resource and uses Gateway API routes
managed by Envoy Gateway instead. The API HPA remains a scaling exercise; its
CPU target is `<unknown>` until metrics-server is installed.

### Inspect local observability

See [observability operations](observability.md) for deployment, rollout checks,
Grafana/Kibana access, direct service inspection, and storage/security limits.
The dev smoke check skips this stack by default; set `SMOKE_TEST_OBSERVABILITY=1`
to verify it after installing the full overlay.

### 7. Open the application

Open `http://battlefield.localhost:8888`. The Gateway API routes `/v1` directly
to the API and all other paths to the frontend. The `.localhost` domain resolves
to loopback without an `/etc/hosts` entry. The frontend Nginx process retains
its internal `/v1` proxy so direct Service access and cloud deployment overlays
continue to work.

Run the same non-interactive verification used by Jenkins at any time:

```bash
./scripts/cd/smoke-test-kind.sh
```

It verifies frontend access and API readiness, authentication, and scenario
responses. With `SMOKE_TEST_OBSERVABILITY=1` (the production default), it also
checks Grafana, Kibana, metrics, and log ingestion. Temporary diagnostic
port-forwards are removed automatically. When
the optional `battlefield-auth` Secret enables authentication, the smoke test
reads its administrator credentials without printing them, logs in through the
gateway, and keeps the resulting session cookie only in its private temporary
directory. The check fails with an explicit configuration error if that Secret
enables Secure cookies while verification is using the local HTTP gateway.

### Diagnose a local deployment

Start with workload state, recent events, and the application logs:

```bash
kubectl get deployments,pods,services,endpointslices \
  --namespace battlefield \
  --output wide
kubectl get events --namespace battlefield \
  --sort-by='.metadata.creationTimestamp'
kubectl logs deployment/battlefield-api \
  --namespace battlefield --tail=100
kubectl logs deployment/battlefield-engine \
  --namespace battlefield --tail=100
kubectl logs deployment/battlefield-agent \
  --namespace battlefield --tail=100
kubectl logs deployment/battlefield-mcp \
  --namespace battlefield --tail=100
kubectl logs deployment/battlefield-frontend \
  --namespace battlefield --tail=100
```

Use `kubectl describe pod <pod-name> --namespace battlefield` for probe,
scheduling, image, or volume failures. A completed migration pod is expected;
inspect `job/battlefield-migrate` only when its Job did not reach `Complete`.

### 8. Remove the local deployment

Deleting both local namespaces also deletes the PostgreSQL claim, observability
data, and logging collector. Remove Prometheus's cluster-scoped kubelet access
at the same time:

```bash
kubectl delete clusterrole/battlefield-prometheus-kubelet \
  clusterrolebinding/battlefield-prometheus-kubelet \
  --ignore-not-found
kubectl delete namespace battlefield battlefield-logging
```

Deleting the entire `kind` cluster has the same data-loss consequence.

## Jenkins deployment pipeline

[Jenkins operations](jenkins.md#pipeline-behavior) owns the PR/main stage sequence,
credentials, publication, verification, and digest rollback workflow. Main deploys
to the registered production Kind cluster; local dev loads its own images.
AWS promotion copies verified images to ECR without rebuilding.

## Repository-specific requirements

- Run migrations once through a Kubernetes `Job` using the existing
  `server -migrate` mode. Never let every API replica race to migrate.
- Pass an explicit configuration path in containers, for example
  `-config /etc/battlefield/config.yaml`.
- Scenario paths are resolved relative to the configuration file. If the
  ConfigMap is mounted under `/etc/battlefield`, set
  `api.scenarioCatalogPath` to an absolute image path such as
  `/app/scenarios`.
- Use `/startupz`, `/livez`, and `/readyz` for startup, liveness, and readiness
  respectively. The API serves them on port 8080 and the engine on port 8081.
- Startup remains unavailable until PostgreSQL and application initialization
  complete. Liveness is process-only. Readiness checks PostgreSQL and becomes
  unavailable before graceful shutdown; engine readiness also checks its worker.
- The API and engine expose lifecycle endpoints before connecting and retry
  transient PostgreSQL startup failures. Invalid configuration still fails
  immediately.
- Frontend Nginx resolves the API Service at request time, so temporary absence
  of the API DNS record no longer prevents the frontend container from starting.
- Set `terminationGracePeriodSeconds` longer than the configured ten-second
  graceful-shutdown deadline.
- The engine can have multiple replicas because work is claimed through durable
  database leases. Each pod still requires a unique worker identity; its pod
  name or UID can be injected with the Downward API.
- Account for database-pool multiplication. Each replica currently permits up
  to ten connections, so the total possible connections grow with every API and
  engine pod.
- Keep `DATABASE_URL` out of Git, image layers, ConfigMaps, logs, and command
  examples containing real credentials.
- Keep the administrator password and session-signing secret out of Git,
  images, ConfigMaps, and command examples. Public HTTPS deployments must set
  `BATTLEFIELD_AUTH_COOKIE_SECURE=true`; all API replicas must share the same
  signing secret for stateless session validation.

## Scaling and reliability exercises

Once one replica works, exercise these behaviors deliberately:

- Scale the API and engine separately.
- Terminate an engine pod during a battle and verify requeue or lease recovery.
- Verify readiness removes a pod from service without immediately restarting it.
- Verify liveness restarts a genuinely stuck process.
- Perform a rolling update and rollback.
- Add CPU and memory requests based on observations.
- Scale the API from a request or CPU signal and the engine from battle backlog
  or oldest queued work rather than CPU by default.
- Add a Pod Disruption Budget only to workloads with enough replicas to remain
  available during disruption.
- Recreate the entire cluster and application from committed configuration.

## Moving the deployment to AWS

The implementation root is [`infra/aws`](../../infra/aws). It creates budget alerts before the network and runtime resources, then provisions VPC networking, immutable ECR repositories, EKS, encrypted RDS PostgreSQL with managed master credentials and backups, Secrets Manager placeholders, control-plane logging, and an OIDC provider for IRSA.

GHCR remains the build source of truth. `scripts/cd/promote-to-ecr.sh` copies the already verified subject and OCI referrers with ORAS, proves that the ECR subject digest is unchanged, then verifies the Cosign signature plus CycloneDX, SLSA-provenance, and AI-manifest attestations. It never rebuilds an image.

Registry-backed Kind deployment uses full digest inputs:

```bash
DEPLOY_ENV=production IMAGE_SOURCE=registry \
BACKEND_IMAGE_REF='ghcr.io/twh09/autonomous-battlefield-backend@sha256:…' \
FRONTEND_IMAGE_REF='ghcr.io/twh09/autonomous-battlefield-frontend@sha256:…' \
./scripts/cd/deploy-kind.sh
```

The admission policy derives its repository patterns from the same
`REGISTRY_NAMESPACE`, `PUBLISHED_BACKEND_IMAGE`, and `PUBLISHED_FRONTEND_IMAGE`
settings used by publication. During rollout, `deploy-kind.sh` supplies the
resolved backend/frontend digest references through `POLICY_BACKEND_IMAGE_REF`
and `POLICY_FRONTEND_IMAGE_REF`; this keeps preserved images covered during a
partial rollout. Standalone policy installation uses the configured repository
defaults when those references are absent. Inputs are validated before policy
installation. Each repository receives separate tag and digest patterns, so a
similarly prefixed repository name does not accidentally match. See
[Kyverno image-reference matching](https://kyverno.io/docs/policy-types/cluster-policy/verify-images/overview/).

The production environment resolver requires `ENFORCE_SUPPLY_CHAIN_POLICY=1`, which installs the Jenkins-only Kyverno verification policy. The tagged Kyverno manifest is installed with server-side apply so its large CRDs do not exceed Kubernetes' client-side last-applied annotation limit. Deployment waits for the admission controller's `Available` condition and prints pod, Deployment, and controller-log diagnostics on timeout. For private GHCR verification, the stage's temporary Docker authentication is copied into a `battlefield-ghcr` pull Secret in the `kyverno` namespace and referenced only by the verification policy. Cosign publishes modern OCI 1.1 referrers for promotion plus legacy digest-tag signature and attestation mirrors for Kyverno 1.15 admission discovery; both sets cover the same digest and use the same key. Unsigned local Kind development remains available through `IMAGE_SOURCE=local` and is never treated as promotion evidence.

The AWS step should reuse the tested images and base resources:

An AWS account is required before this stage, including billing verification.
Secure the root user with MFA, avoid using it for daily administration, and use
temporary role-based credentials for normal human and workload access. Create a
cost budget and alerts before provisioning. Free Tier eligibility does not make
an EKS environment free: the cluster control plane and its compute, database,
load balancer, networking, and storage resources are independently billable.

The checked-in Terraform is a reference implementation and has not been applied
to an AWS account by this repository. Account-specific plans, IAM review,
secret population, ingress installation, and recovery evidence are still
required before treating the environment as production-ready.

1. Create the AWS account, cost budget, and billing alerts.
2. Review and apply `infra/aws` to provision ECR, networking, EKS, RDS, and
   Secrets Manager.
3. Promote immutable GHCR digests and all OCI referrers to ECR without rebuilding.
4. Configure EKS workload identity and least-privilege access to the required
   secret.
5. Point `DATABASE_URL` at the private RDS endpoint with TLS enabled.
6. Add the AWS Load Balancer configuration in the AWS overlay.
7. Run the migration Job and deploy the workloads.
8. Exercise secret rotation, backup restore, pod disruption, failed rollout,
   and teardown.

Budget alerts are notifications, not guaranteed spending limits.

## Reference documentation

- [`kind` quick start](https://kind.sigs.k8s.io/docs/user/quick-start/)
- [Kustomize with `kubectl`](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/)
- [Kubernetes probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/)
- [Amazon EKS pricing](https://aws.amazon.com/eks/pricing/)
- [AWS Free Tier plans](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-plans.html)
- [AWS Budgets](https://docs.aws.amazon.com/cost-management/latest/userguide/create-cost-budget.html)
- [AWS root-user security practices](https://docs.aws.amazon.com/IAM/latest/UserGuide/root-user-best-practices.html)
- [EKS Auto Mode](https://docs.aws.amazon.com/eks/latest/userguide/automode.html)
- [Using Secrets Manager secrets with EKS pods](https://docs.aws.amazon.com/eks/latest/userguide/manage-secrets.html)

## Doctrine profile rollout

Deploy API, engine, MCP, agent, and frontend from the same revision. Startup migration
00008 preserves existing faction IDs/names and legacy blueprints, backfills balanced
doctrine, and permits tick-zero decisions. New battles use snapshot version 2;
existing queued battles retain their snapshotted legacy rules. No data reset is needed.
Obsolete clients posting blueprint-based faction bodies must be updated.

The migration down path deliberately refuses a lossy rollback when doctrine-only
profiles or opening decisions exist. Restore a pre-rollout database backup if a full
application/database rollback is required. Validate the rollout by creating/editing
a doctrine, staging a battle, and observing opening decisions and purchase events at
tick zero before tick one. Purchases should reduce the same gold balance throughout.

### Faction background generation rollout

Deploy API, MCP, engine/agent workers and frontend from the same revision. Apply
migration `00009_faction_background.sql` before rollout. It preserves existing
factions (empty background), legacy blueprints and historical battle snapshots.
Migration runners use a PostgreSQL advisory session lock to serialize startup.
Restart host CLI workers too: old workers advertise battle support only and cannot
execute background jobs. The internal MCP service must route `/generation-mcp`;
it should not be exposed through public ingress.

Configure the writer in `backend/config.yaml` or the deployment ConfigMap:

```yaml
ai:
  backgroundGeneration:
    enabled: true
    profileId: codex-subscription-local
    dailyAllowance: 10
    cooldown: 30s
    queueTimeout: 5m
    executionTimeout: 2m
```

The profile uses its existing configured model, credentials, version validation,
and availability settings. Enable the host CLI profile and run the normal host
worker for the default subscription writer. Operators can select a supported
OpenAI/Gemini API or Codex/Antigravity CLI profile independently of battle models.
No player model selector or automatic fallback is used. An invalid/unavailable
profile rejects admission before quota is reserved; a profile whose worker goes
offline after admission leaves a queued job until expiry. Disabled
generation still permits manually writing and saving backgrounds.

Allowance is persisted per authenticated actor, with a UTC-day boundary; when
authentication is disabled every client shares `local-development`. All admitted jobs count, including expiry before execution. Repeated successful submissions do not change the
result, and execution timeouts are not automatically retried. Queued reservations
expire after five minutes by default and still count on the original day; the admission
cooldown remains in effect after expiry. Options/job reads and worker claims
perform expiry, so an idle expired row may remain until the next such operation.

Check options availability, queue a draft, observe queued/running/succeeded, use
or discard the preview, then save a faction. Verify the historical battle retains
its original identity after edits. Inspect job `error_category`, timestamps,
`usage`, and `metadata` for failures; never log capability tokens or raw profile
prompts. API models have a hard three-turn limit. CLI jobs are deadline-bound and
instructed to use at most three turns; native CLI internal provider-turn counts
are not available to enforce separately. Three invalid submissions fail the job.

### Elasticsearch and Kibana rollout recovery

Elasticsearch uses the 10Gi `battlefield-elasticsearch-data` PVC with the Kind
`standard` storage class. Pod replacement retains log indices and Kibana saved
objects; deleting the cluster or its backing storage does not. The first rollout
from the previous `emptyDir` configuration starts with empty storage. Export or
snapshot any data that must be preserved before that transition.

The deployment helper waits for Elasticsearch and the other observability
workloads, restarts Kibana against the ready Elasticsearch instance, waits for
Kibana's rollout, then applies its setup job separately. Applying the observability
Kustomization alone no longer creates the setup job. Each observability rollout
briefly restarts Kibana. The setup job has a 300-second deadline, bounded HTTP
requests, and separate pods for retries so failed-attempt logs remain available.
The helper detects both successful and failed Job conditions and prints job, pod,
and container diagnostics on failure. Finished jobs are retained for 300 seconds.

After a manual recovery, recreate only the setup job with
`kubectl -n battlefield delete job battlefield-kibana-setup --ignore-not-found`
and `kubectl -n battlefield apply -f deploy/overlays/local/observability/kibana/setup-job.yaml`,
then confirm completion before running production smoke verification.

Unfinished battles expire one hour after creation, including queued and paused
time. The engine sweeps at startup and every minute independently of active
battle processing. It atomically marks expired battles failed with
`battle_timeout`, clears their leases, and cancels pending/running decisions
using the existing `battle_stopped` cancellation category. History and tick
state remain available; active admission slots are released but daily usage is
not refunded. Existing overdue battles expire on rollout. No migration or new
configuration is required; while the engine is offline, expiry waits for restart.
