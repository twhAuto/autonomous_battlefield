# OrdinalFrontier Tooling Guide

[Codebase index](codebase-guide.md) · [System architecture](../architecture/system-architecture.md)

This guide maps scripts to their purpose and tests. Runbooks under
`docs/operations/` own the procedures that use them.

## Environment tooling

`scripts/lib/deployment-environment.sh` owns profile loading, conflicting-override
checks, cluster UID verification, and dev Docker identity verification.
`scripts/cd/setup-environment.sh` registers an existing cluster without changing
its original kubeconfig. `prepare-dev-secrets.sh` provisions private dev files;
`create-kind-cluster.sh` starts or bootstraps dev without recreation. Deployment,
rollback, smoke, dev CLI agents, retention reports, and runtime assurance share
the resolver. `scripts/test/test-deployment-environment.sh` exercises rejection
and bootstrap paths. See the [configuration model](../architecture/configuration.md)
for file ownership, [local setup](local-setup.md) for dev commands, and
[environment operations](../operations/environments.md) for production setup.

## Deployment code

[`infra/deploy/base`](../../infra/deploy/base) contains portable Kustomize resources.
[`infra/deploy/overlays/local`](../../infra/deploy/overlays/local) adds PostgreSQL, Gateway API,
observability and logging resources with Kind defaults. The
[`K3s overlays`](../../infra/deploy/overlays/k3s) reuse those resources and patch the
retained storage class and volume sizes. Delivery scripts handle gateway access
and telemetry differences; see [K3s delivery](../operations/k3s-delivery.md#deliver-and-verify).

The key application manifests are:

- `api-deployment.yaml` and `api-service.yaml`;
- `engine-deployment.yaml`;
- `agent-deployment.yaml`;
- `ai-gateway-deployment.yaml`, `ai-gateway-service.yaml`, and `ai-gateway-network-policy.yaml`.
  The transitional `battlefield-mcp` alias Service lives in
  `infra/deploy/transition/`; the deploy script applies it only after the gateway
  rollout is ready;
- frontend deployment and service;
- `ingress.yaml` for external routing;
- `api-hpa.yaml` for API horizontal autoscaling;
- `api-pdb.yaml` and `frontend-pdb.yaml` for disruption budgets;
- migration overlay job.

[`deploy-local.sh`](../../scripts/cd/deploy-local.sh) is the development entry point.
It reads the Git-ignored project-root `.env`; `scripts/lib/application-env.cjs`
parses both environments' literal settings, validates credentials, and separates
Kubernetes Secret fields. [`run-k8s-cli-agent.sh`](../../scripts/dev/run-k8s-cli-agent.sh)
bridges authenticated host subscription CLIs to a Kubernetes MCP service.
`scripts/cd/deploy.sh`, `smoke-test.sh` and `rollback.sh` are the driver-neutral
entry points Jenkins uses: on main, `capture-rollback-images.sh` records the running
images before rollout and `rollback-on-failed-verification.sh` decides whether a
failed smoke test may call `rollback.sh`. `scripts/cd/lib/kubernetes-deployment.sh`
stages the selected overlays, `scripts/lib/k3s-delivery.py` performs read-only K3s
platform and retained-volume checks, and `scripts/lib/production_recovery.py` binds
verified images to the applied migration ledger. `scripts/lib/deployment-components.sh`
and `scripts/cd/detect-deployment-components.sh` classify changed paths; their tests
cover application test files, CI lint/coverage-summary scripts, mixed changes and
production-to-test renames.

## Images and registry

- [`scripts/ci/with-ghcr-auth.sh`](../../scripts/ci/with-ghcr-auth.sh) owns temporary
  GHCR login and cleanup for both Jenkins pipelines and production rollback;
  [`test-registry-auth.sh`](../../scripts/test/test-registry-auth.sh) checks credential
  isolation, forwarding, failures and cancellation without registry access.
- [`scripts/lib/image-metadata.sh`](../../scripts/lib/image-metadata.sh) owns published
  repository defaults, policy reference validation and source-URL normalization;
  `test-image-metadata.sh` checks publishing and admission rendering with stubs.
  `test-registry-verification.sh` exercises publication and deployment signature
  checks, including Cosign 3 bundle summaries, retained legacy revisions and
  rejected evidence.
- `scripts/ci/generate-ai-manifest.sh` combines runtime metadata with a digest of the
  Git-tracked files under `backend/evals`; `test-ai-manifest.py` covers
  reproducibility and corpus identity.
- `scripts/ci/verify-frontend-image.py` starts a built frontend image with production
  filesystem and user restrictions and checks its probes and assets;
  `test-frontend-image.sh` builds the real Dockerfile from private-permission inputs.
- `test-base-image-pins.py` requires every Dockerfile base image to be pinned by
  digest under a `# Base image: name:tag` comment; `scripts/ops/check-base-image-pins.py`
  reports stale pins on demand. See the
  [supply-chain runbook](../operations/supply-chain-runbook.md#base-image-pins).
- `scripts/ops/ghcr-retention-report.sh` inventories package versions and protected
  digests without deleting registry data; `test-retention-report.py` covers failed
  collection and interrupted swaps. See the
  [retention runbook](../operations/supply-chain-runbook.md#artifact-identity-and-retention).

## Platform automation

Each workflow has offline tests that `scripts/test/test-deployment.sh` runs, most
through `test-server-bootstrap.sh`. Where privileged behavior matters, an opt-in
rehearsal runs only against a disposable, identity-bound VM or cloud fixture. The [codebase guide](codebase-guide.md#infrastructure-and-delivery-entry-points)
lists entry points and runbooks.

| Workflow | Key helpers | Offline tests | Disposable rehearsal |
| --- | --- | --- | --- |
| Host foundation | `scripts/infra/server.py`, `control-tools.py`, `inspect-host.py`; `infra/ansible/site.yml` | `test-server-bootstrap.sh`, `test-data-storage.py` | `scripts/infra/rehearse-server.py` with `scripts/test/rehearse-server-guest.py`; `rehearse-data-storage.py` |
| Jenkins and SonarQube | `scripts/infra/ci-bootstrap.py`, `ci-runtime.py`; `infra/ansible/ci.yml`; `infra/jenkins/` lock, JCasC and Job DSL | `test-ci-bootstrap.py` | `scripts/test/run-ci-bootstrap-rehearsal.py` |
| Platform jobs | `scripts/infra/platform.py`, `operation-lock.py`, `setup-platform.py`, `seed-platform.py`; `infra/ansible/callback_plugins/battlefield_report.py` | `test-platform.py` | — |
| K3s VM | `scripts/infra/vm.py`, `vm-policy.py`, `vm-tools.py`, `vm-state.py`; `infra/libvirt` | `test-vm.py`, `check-vm-terraform.py` | `scripts/infra/rehearse-vm.py` with `scripts/test/rehearse-vm-guest.py` |
| K3s platform | `scripts/infra/k3s.py`, `k3s-config.py`; `infra/ansible/k3s.yml` | `test-k3s.py` | `scripts/infra/rehearse-k3s.py` with `scripts/test/k3s-registry-fixture.py` |
| K3s delivery | `scripts/cd/deploy.sh`; `scripts/lib/k3s-delivery.py`, `production_recovery.py` | `test-k3s-delivery.py`, `test-k3s-delivery-rehearsal.py` | `scripts/infra/rehearse-k3s-delivery.py` |
| CLI worker VM | `scripts/infra/cli-vm.py`; `infra/ansible/cli-vm.yml`; `infra/deploy/agent-vm/*.xml` | `test-cli-vm.py` | `scripts/infra/rehearse-cli-vm.py` with `scripts/test/rehearse-cli-vm-guest.py` |
| CLI worker services | `scripts/infra/cli-bootstrap.py`; `infra/ansible/cli.yml`; `scripts/ops/render-agent-vm-host.py` | `test-cli-bootstrap.py`, `test-agent-vm-host.py` | `scripts/test/run-cli-bootstrap-rehearsal.py` with `rehearse-cli-bootstrap.py` |
| CLI worker repair | `scripts/infra/cli-worker.py`; `scripts/ci/run-cli-worker.sh` | `test-cli-worker.py` | — |
| Cloudflare | `scripts/infra/cloudflare.py`; `infra/ansible/cloudflare.yml` | `test-cloudflare.py` | `scripts/infra/rehearse-cloudflare.py` (idle cloud fixture) |
| Local HTTPS | `scripts/infra/https.py`, `https-runtime.py`; `infra/ansible/https.yml` | `test-local-https.py` | `scripts/infra/rehearse-https.py` with `scripts/test/rehearse-https-guest.py` |

`scripts/test/test-server-rehearsal.py` checks that every rehearsal entry point
starts with ordinary Python path settings. Offline-test names above live in
`scripts/test/`.

## Testing layout and expectations

Tests are colocated with their Go or React implementation. The codebase includes:

- pure domain tests for map rules, combat, economy, and determinism;
- service tests for validation and use cases;
- PostgreSQL adapter tests with mocked query behavior and integration coverage;
- worker tests for leases, cancellation, decisions, fallback, pause/resume, and stop;
- provider, MCP, runner, profile, and capability tests;
- HTTP contract/status tests;
- React component and typed API-client tests.

Go tests assert with plain `if` statements and `t.Fatal`. SonarQube's cognitive
complexity rule (go:S3776, limit 15) applies to test code too, so a long test is
split into named `check…` helpers with `t.Helper()` rather than hiding branches in
an assertion wrapper. Run
`go run github.com/uudashr/gocognit/cmd/gocognit@latest -over 15 .` in `backend/`
before pushing. Component tests verify accessibility roles from labelled sections
and native `output` elements rather than redundant explicit role attributes.

Other test locations:

- Package PostgreSQL integration tests sit beside their adapters and need
  `TEST_DATABASE_URL`, which the isolated harness supplies. `test-database-harness.py`
  checks the harness's admission and query-plan target validation with command doubles.
- `scripts/test/` holds reusable runners and deployment-script tests; invoke them
  directly. `scripts/ci/` owns build, analysis and assurance commands.
- `test-kind-deployment.sh` checks overlay staging, auth-only Secret inputs,
  collector configuration with Langfuse on and off, real Kustomize renders and
  rollout failures, using explicit Langfuse overrides so it never queries a cluster.
  `test-ensure-helm.py` and `test-langfuse-detection.sh` cover the Langfuse installer.
- `test-security-launchers.sh` checks scanner artifact paths, failure propagation
  and the shared Trivy pin with scanner stubs.
- `scripts/lib/access_service.py` validates private CI credentials;
  `scripts/ops/verify-access-service.py` checks private denial and public service
  authentication. `test-access-service.py` and `test-runtime-assurance.sh` cover them.
- Performance scenarios and SQL fixtures live in `backend/test/performance`, with
  launchers in `scripts/perf`. AI cases live in `backend/evals`; see
  [AI comparisons](ai-comparisons.md). Browser tests live in `frontend/e2e`, with
  deterministic API fixtures and desktop/mobile Chromium projects.

Coverage thresholds and commands are in
[coverage gate details](ci.md#coverage-gate-details).

Frontend Vitest runs use at most two workers, configured in
`frontend/vite.config.ts`, to limit CPU contention with Jenkins's parallel Go,
fuzz, and security checks. Each test has a 15-second timeout as a hang guard, not
a performance benchmark. Testing Library async queries and `waitFor` use a
five-second deadline configured in `frontend/src/test/setup.ts`. Long interaction
sequences, such as battle-history pagination, get separate tests so each has its
own timeout budget on the shared agent.

## Production assurance helpers

`scripts/lib/prometheus-smoke.sh` supplies the bounded Prometheus query wait and
timeout target diagnostics used by `scripts/cd/smoke-test.sh`. Run
`bash scripts/test/test-prometheus-smoke.sh` to verify delayed recovery, persistent
failure, unavailable/malformed diagnostics and timeout validation without a cluster.

`scripts/ops/render-limited-launch.py` includes the same-namespace Prometheus
scrape peer in the API ingress policy. `scripts/test/test-limited-launch.py`
asserts exact API/frontend peer selectors and ports to prevent broadening access.

Production hardening entry points:

- `scripts/test/test-deployment.sh` is the shared PR/main infrastructure regression gate.
- `scripts/ci/run-sonarqube-pr-gate.sh` resets a PR preview project and seeds it
  from its target merge base, dated at that commit, before the gated head scan;
  `run-sonarqube-gate.sh` owns one scanner invocation. `test-sonarqube-gate.sh`
  verifies preview-project selection, the reset, baseline dating and ordering, and
  scanner arguments without contacting SonarQube.
- `scripts/cd/reconcile-network-policies.sh` applies only the separately rendered
  `network-policies.json`; `deploy-tunnel.sh` applies connector inputs with checksum
  annotations instead of an unconditional restart.
- `scripts/ops/configure-production-prometheus.py` stages verified kubelet TLS;
  `export-kind-kubelet-ca.sh` obtains public trust material via the registered Docker
  daemon. `scripts/ci/render-security-manifests.sh` includes synthetic production
  TLS and tunnel/policy resources in schema and security scanning.
- `scripts/lib/prometheus-targets.cjs` checks all live API replicas and fresh scrapes;
  `scripts/test/prometheus-targets.test.cjs` covers missing, stale and wrong-IP targets.
- `scripts/cd/production-recovery.sh` and `scripts/lib/production_recovery.py` maintain
  the private cluster/image recovery approval after successful runtime assurance.
- `scripts/ops/prepare-limited-production.sh` checks production cluster identity,
  then validates and renders private launch files (`test-production-preparation.py`).
  `scripts/ops/ai-policy.sh` is the private operator stop/status/usage interface;
  `scripts/ops/verify-agent-isolation.py` tests the CLI worker guest boundary.
- `scripts/security/capture-zap-traffic.py` verifies and sanitizes authenticated CI
  reads for an offline ZAP passive scan. `test-production-hardening.py` covers
  destination binding, redirect rejection, credential exclusion, TLS staging,
  recovery mismatches, policy-only reconciliation and scanner failure propagation.

## Cross-review tooling

`scripts/review/cross-review.py` runs the opposite agent's review of a committed
diff and publishes it to the PR; `scripts/test/test-cross-review.py` covers it. The
[cross-review guide](cross-review.md) owns its options, round limits and evidence.
Codex and Claude Code 2.1.277 or newer load the shared `AGENTS.md` rules at startup;
the repository has no `CLAUDE.md`, so older Claude Code sessions start without them.

## Container build contexts

Image builders use `backend/` and `frontend/` as separate Docker contexts.
Each component owns its `.dockerignore`; root exclusions do not apply to those
contexts. Both exclude `.env`, `.env.*`, and nested equivalents while retaining
`.env.example`. Local credentials and Vite overrides must not enter builder layers.
`./scripts/test/test-docker-context.sh` verifies these boundaries with synthetic
files and scratch images; it requires Docker but no registry pulls or credentials.
Jenkins runs it during PR deployment checks and the main deployment-scope stage,
separately from the offline deployment-script suite.
