# Application deployment

Application delivery uses `deploy.sh`, `rollback.sh` and `smoke-test.sh` in
`scripts/cd/`. The registered environment's driver selects the manifests and
cluster checks: production runs on K3s, and development on Kind. This page explains
how delivery works; the linked guides own each procedure.

| Task | Start here |
| --- | --- |
| Register a target and configure its private inputs | [Environment operations](environments.md) |
| Deploy or troubleshoot development Kind | [Local Kubernetes deployment](../developer/local-kubernetes.md) |
| Prepare public production access | [Production access](access.md) |
| Deliver to K3s or move production to a new cluster | [K3s delivery](k3s-delivery.md) |
| Understand the pipeline, rollback and recovery records | [Jenkins production delivery](jenkins.md#production-delivery) |
| Coordinate host, VM and platform operations with delivery | [Platform jobs](platform-jobs.md#plan-apply-and-verify) |
| Assess the AWS reference | [Moving to AWS](#moving-the-deployment-to-aws) |

PostgreSQL remains the durable queue in every environment; see the
[system context](../architecture/system-architecture.md#2-system-context).
Production migrations create and inspect a database backup before applying SQL,
production smoke checks authenticate with the
[CI service identity](access.md#ci-service-identity), and rollback records include
the applied migration versions, so a changed ledger requires reviewed database
recovery before an automatic image rollback. Before enabling production delivery
on a new or rebuilt environment, complete the
[shared agent setup](jenkins.md#shared-ci-and-production-agent) and
[trusted kubelet monitoring](observability.md#production-kubelet-tls). Run
`./scripts/test/test-deployment.sh` before opening a deployment PR.

## Gameplay rules cutover

AB-170 changes engine semantics without retaining the previous engine. A retry,
restart or resumed lease for an old battle reconstructs it under the new rules:
recruitment is queued, the map is open, and construction requires building range.
An old order targeting a distant authored pad can now be rejected. Frozen scenario
JSON preserves data, not the old executable's behavior.

Before deploying this overhaul, the release operator must arrange a maintenance
window that prevents new battle creation, then finish every queued/running battle
on the old engine. Resume paused battles so they can finish; stopping unfinished
battles instead requires the owner's agreement. Verify that no queued or running
rows remain (paused battles can have either status) before replacing the engine.
Worker claim-draining alone is insufficient: it can leave queued battles behind.
If the creation freeze or empty-queue condition cannot be established, postpone
the release. Ordinary rolling deployment is not sufficient for this cutover.

AB-173 applies the same precondition. Its engine lets jammed units pass through
allies and enforces the frozen ruleset's unit cap; ruleset v8 raises the building
limit to 12 and caps units at 40. A resumed older battle keeps its frozen limits
(none for the unit cap) but moves under the new routing rules.

Completed battle snapshots and event history remain readable without replay.
An explicit future replay of old inputs uses the new engine and can produce a
different trace. This is a release precondition, not evidence that a maintenance
window or production deployment has been performed.

## Overlays

Application manifests use Kustomize. This tree shows the shared base and Kind
overlays; [K3s delivery](k3s-delivery.md#deliver-and-verify) describes the additional
K3s compositions and retained-storage overrides:

```text
infra/deploy/
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

The base contains standard Kubernetes resources. Provider-specific annotations,
resource identifiers, and secret integrations belong in environment overlays. The
AWS column below describes the intended mapping, not a verified deployment.

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
kubectl kustomize infra/deploy/overlays/local/migration
kubectl kustomize infra/deploy/overlays/local/application
kubectl kustomize infra/deploy/overlays/local/observability
kubectl kustomize infra/deploy/overlays/local/logging-collector
kubectl kustomize infra/deploy/overlays/local/gateway
```

Application delivery uses Kustomize, not an application Helm chart; keep changes in
the owning base or overlay and validate their rendered manifests. The optional
[Langfuse service](langfuse.md) has its own Helm installation.

## Image identity and releases

The normal Kubernetes workflow tags each changed application image with the
full Git commit SHA. Clean builds identify their source revision; local dirty
builds require an explicit opt-in and must not be published as release evidence.

`VERSION` contains the release line in semantic `major.minor.patch` form
(currently `0.1.0`). When a CI build number is present, the build also
adds a human-readable alias to the same image:

```text
autonomous-battlefield-backend:0.1.0-build.27
autonomous-battlefield-frontend:0.1.0-build.27
```

Jenkins supplies `BUILD_NUMBER` automatically. GitHub Actions supplies
`GITHUB_RUN_NUMBER` and `GITHUB_RUN_ATTEMPT`; the script recognizes both CI
systems. `CI_BUILD_NUMBER` and `CI_BUILD_ATTEMPT` are provider-neutral explicit
overrides.

The images include OCI revision, version, and source labels. Their names look
like:

```text
autonomous-battlefield-backend:<full-git-sha>
autonomous-battlefield-frontend:<full-git-sha>
```

Local dev deploys the full-SHA tag loaded into Kind. Jenkins production deploys
verified `repository@sha256:...` references from GHCR. Build-number aliases are
for human navigation and are not production deployment identities.

Do not create a Git tag for every Jenkins or GitHub Actions build. Create an
annotated Git tag only for an intentional release, using the value in `VERSION`:

```bash
git tag -a "v$(<VERSION)" -m "OrdinalFrontier v$(<VERSION)"
git push origin "v$(<VERSION)"
```

After it is pushed, the tag appears on GitHub and can be used to create a GitHub
Release. Increment `VERSION` deliberately: patch for compatible fixes, minor for
compatible features, and major for breaking releases.

## Deployment components

`DEPLOY_COMPONENTS` limits a deployment, and the image build and scan scripts
consume the same value, ignoring non-image components. The
[local walkthrough](../developer/local-kubernetes.md#deploy-a-revision) shows
examples.

Supported components are `infrastructure`, `migration`, `backend`, `frontend`,
`application`, `observability`, `gateway`, and `network-policy`. Selecting `backend` also runs the
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

### Change-to-deployment mapping

Changes under `scripts/test/` and `backend/test/`, Go `*_test.go` files,
React/TypeScript `*.test.ts` and `*.test.tsx` files under `frontend/src`, and
frontend test setup do not select application deployment components. These changes
still run the normal CI quality gates. Renames inspect both old and new paths, so
moving production code into a test path still selects the production component.
Unknown paths retain the conservative full-deployment fallback.

The path classifier evaluates the following rules from top to bottom for each
path, so an earlier documentation rule takes precedence over a broader source
folder rule. Multiple changed paths contribute the union of their components;
`all` takes precedence over every targeted selection.

| Changed path | Selected components | Image build and scan |
| --- | --- | --- |
| Any `*.md`, `LICENSE`, `LICENSE.*`, or root `.gitignore` | `none` | None |
| `infra/jenkins/Jenkinsfile.assurance` | `all` | Backend and frontend images |
| `infra/ansible/**`, other `infra/jenkins/**`, `infra/libvirt/**`, `infra/k3s/**`, `scripts/infra/**`, `scripts/ci/run-platform.sh`, `scripts/ci/run-vm.sh`, or `scripts/ci/run-k3s.sh` (explicit platform provisioning) | `none` | None |
| `infra/local/**`, `infra/sonarqube/**` or `sonar-project.properties` | `none` | None |
| `scripts/test/**` and `backend/test/**` | `none` | None |
| Go `backend/**/*_test.go`, frontend `src/**/*.test.ts`, `src/**/*.test.tsx`, or `src/test/**` | `none` | None |
| `scripts/ci/lint-go.sh` or `scripts/ci/coverage-check-summary.sh` | `none` | None |
| `backend/**` | `migration,backend,application` | Backend image |
| `frontend/**` | `frontend,application` | Frontend image |
| `infra/deploy/base/infrastructure/**` or `infra/deploy/overlays/{local,k3s}/infrastructure/**` | `infrastructure` | None |
| `infra/deploy/base/migration/**` or `infra/deploy/overlays/{local,k3s}/migration/**` | `migration` | None; uses the deployed backend image |
| `infra/deploy/base/application/**` or `infra/deploy/overlays/{local,k3s}/application/**` | `application` | None; preserves deployed image tags |
| `infra/deploy/langfuse/**`, `infra/deploy/overlays/{local,k3s}/observability/**` or `infra/deploy/overlays/{local,k3s}/logging-collector/**` | `observability` | None |
| `infra/deploy/overlays/{local,k3s}/gateway/**` | `gateway` | None |
| `VERSION` | `migration,backend,frontend,application` | Both application images |
| CI image build or scan scripts | `all` | Both application images |
| `scripts/ops/render-limited-launch.py` or `scripts/cd/reconcile-network-policies.sh` | `network-policy` | None |
| Other paths under `scripts/cd/**` or `scripts/lib/**` | `all` | Both application images |
| `infra/deploy/policy/**` or `infra/deploy/kind.yaml` | `all` | Both application images |
| Any other or newly introduced path | `all`, with a warning | Both application images |
| Missing or invalid previous-successful revision | `all` | Both application images |

The selected components have these deployment effects:

| Component | Kubernetes action |
| --- | --- |
| `infrastructure` | Apply namespace configuration, Secret, and PostgreSQL; wait for PostgreSQL rollout. |
| `migration` | Recreate the migration Job with the selected or currently deployed backend image and wait for completion. |
| `backend` | Load the backend image into kind; automatically includes `migration` and `application`. |
| `frontend` | Load the frontend image into kind; automatically includes `application`. |
| `application` | Apply API, engine, agent, internal AI gateway, frontend, Services, network policy, autoscaling, and disruption-budget resources; wait for application rollouts. |
| `observability` | Apply Prometheus, kube-state-metrics, Grafana, Fluent Bit, Elasticsearch, Kibana, dashboards, data views, recording rules, and alerts; wait for readiness. |
| `gateway` | Apply Envoy Gateway routes and wait for the local Gateway to become programmed. |
| `network-policy` | Reconcile production API/frontend ingress policies without image builds or a tunnel restart. |
| `all` | Execute every action above. |
| `none` | Build, scan, and deployment commands exit without changing Kubernetes. |

Every nonempty production deployment reconciles ingress policies before application rollout;
explicit `network-policy` selection performs that reconciliation without application
rollouts. The tunnel itself is activated separately.

In `scripts/ci/`, only the platform launchers listed above, `lint-go.sh` and
`coverage-check-summary.sh` are exempt; new CI scripts still use the unknown-path
fallback until explicitly classified.

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
commands. `scripts/cd/deploy.sh` is the public deployment orchestrator. Its
internal functions live in `scripts/cd/lib/kubernetes-deployment.sh`, while
`scripts/lib/deployment-components.sh` provides the component-selection rules
shared by CI and CD. Files under either `lib` directory are sourced libraries
and are not intended to be run directly.

## Verified registry delivery

The registered environment selects Kind or K3s. These image and provenance checks
apply before delivery; they do not provision a cluster.

Registry-backed delivery uses full digest inputs:

```bash
DEPLOY_ENV=production IMAGE_SOURCE=registry \
BACKEND_IMAGE_REF='ghcr.io/twh09/autonomous-battlefield-backend@sha256:…' \
FRONTEND_IMAGE_REF='ghcr.io/twh09/autonomous-battlefield-frontend@sha256:…' \
./scripts/cd/deploy.sh
```

The admission policy derives its repository patterns from the same
`REGISTRY_NAMESPACE`, `PUBLISHED_BACKEND_IMAGE`, and `PUBLISHED_FRONTEND_IMAGE`
settings used by publication. During rollout, `deploy.sh` supplies the
resolved backend/frontend digest references through `POLICY_BACKEND_IMAGE_REF`
and `POLICY_FRONTEND_IMAGE_REF`; this keeps preserved images covered during a
partial rollout. Standalone policy installation uses the configured repository
defaults when those references are absent. Inputs are validated before policy
installation. Each repository receives separate tag and digest patterns, so a
similarly prefixed repository name does not accidentally match. See
[Kyverno image-reference
matching](https://kyverno.io/docs/policy-types/cluster-policy/verify-images/overview/).

The production resolver requires `ENFORCE_SUPPLY_CHAIN_POLICY=1` and installs the
Jenkins-managed image verification policy. On Kind, the helper also installs the
pinned Kyverno controller using server-side apply, avoiding the client-side
annotation size limit on its CRDs. On K3s, the platform owns the controller;
application delivery does not install or replace it. Both paths wait for admission
controller availability and report pod, Deployment, and log diagnostics on failure.

For private GHCR verification, the temporary Docker authentication becomes a
`battlefield-ghcr` pull Secret in the `kyverno` namespace, referenced by the policy.
Cosign publishes OCI 1.1 referrers for promotion and legacy digest-tag signature
and attestation mirrors for Kyverno 1.15 discovery. Both cover the same digest and
use the same key.

Publication and GHCR delivery use `cosign verify --new-bundle-format=false` to
check the full-commit `revision` in the legacy signature. Cosign 3.0.2 modern
bundle signatures omit this annotation; their summarized output has
`optional: null` and cannot satisfy the reused-image revision check. Attestations
still use modern verification. If a configuration-only rollout reports a missing
revision annotation, use the corrected verifier; do not bypass verification or
invent a revision. Images with the published legacy mirror need no rebuild.
Unsigned Kind development through `IMAGE_SOURCE=local` is not promotion evidence.

## Runtime requirements

- Run migrations once through a Kubernetes `Job` using the existing
  `server -migrate` mode. Never let every API replica race to migrate.
- Pass an explicit configuration path in containers, for example
  `-config /etc/battlefield/config.yaml`.
- Scenarios, rulesets and content libraries are embedded in the server binary
  (`backend/gamedata`). The base ConfigMap still sets the retired
  `api.scenarioCatalogPath: /app/scenarios` and `api.rulesetCatalogPath:
  /app/rulesets`: images built before embedding resolve a missing path beside
  `/etc/battlefield/config.yaml`, so an image-only rollback to one would otherwise
  fail to load its catalog. Current binaries accept only those two values; remove
  the keys once no rollback target predates `backend/gamedata`.
- Use `/startupz`, `/livez`, and `/readyz` for startup, liveness, and readiness
  respectively. The API serves them on port 8080 and the engine on port 8081.
- Startup remains unavailable until PostgreSQL and application initialization
  complete. Liveness is process-only. Readiness checks PostgreSQL and becomes
  unavailable before graceful shutdown; engine readiness also checks its worker.
- The API and engine expose lifecycle endpoints before connecting and retry
  transient PostgreSQL startup failures. Invalid configuration still fails
  immediately.
- Frontend Nginx resolves the API Service at request time, so temporary absence
  of the API DNS record does not prevent the frontend container from starting.
- Set `terminationGracePeriodSeconds` longer than each process's shutdown deadline.
  API and engine timeouts come from their configuration; the agent currently
  waits up to 20 seconds for its health server and in-flight jobs. Check the
  workload manifest whenever changing these timeouts.
- The engine can have multiple replicas because work is claimed through durable
  database leases. Each pod still requires a unique worker identity; its pod
  name or UID can be injected with the Downward API.
- Account for database-pool multiplication. Each replica currently permits up
  to ten connections, so the total possible connections grow with every API and
  engine pod.
- Keep `DATABASE_URL` out of Git, image layers, ConfigMaps, logs, and command
  examples containing real credentials.
- Production requires Cloudflare Access identity verification; follow the
  [production access runbook](access.md). Password/cookie authentication is only for
  explicit development mode. In that mode, keep the administrator password and
  signing secret out of Git, images, ConfigMaps, and examples; replicas share the
  signing secret, and HTTPS development uses `BATTLEFIELD_AUTH_COOKIE_SECURE=true`.

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

The implementation root is [`infra/aws`](../../infra/aws). It creates budget alerts
before the network and runtime resources, then provisions VPC networking, immutable
ECR repositories, EKS, encrypted RDS PostgreSQL with managed master credentials and
backups, Secrets Manager placeholders, control-plane logging, and an OIDC provider
for IRSA.

GHCR remains the build source of truth. `scripts/cd/promote-to-ecr.sh` copies the
already verified subject and OCI referrers with ORAS, proves that the ECR subject
digest is unchanged, then verifies the Cosign signature plus CycloneDX,
SLSA-provenance, and AI-manifest attestations. It never rebuilds an image.

Reuse tested images and base resources. See
[AWS promotion limits](jenkins.md#aws-promotion-limits) before copying images to ECR.
Keep production PostgreSQL outside EKS in managed RDS; account-specific ingress,
identity and recovery configuration remain necessary.

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

- [Kustomize with `kubectl`](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/)
- [Kubernetes probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/)
- [Amazon EKS pricing](https://aws.amazon.com/eks/pricing/)
- [AWS Free Tier plans](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-plans.html)
- [AWS Budgets](https://docs.aws.amazon.com/cost-management/latest/userguide/create-cost-budget.html)
- [AWS root-user security practices](https://docs.aws.amazon.com/IAM/latest/UserGuide/root-user-best-practices.html)
- [EKS Auto Mode](https://docs.aws.amazon.com/eks/latest/userguide/automode.html)
- [Using Secrets Manager secrets with EKS pods](https://docs.aws.amazon.com/eks/latest/userguide/manage-secrets.html)
