# OrdinalFrontier Tooling Guide

[Codebase index](codebase-guide.md) · [System architecture](../architecture/system-architecture.md)

## Environment tooling

`scripts/lib/deployment-environment.sh` owns profile loading, conflicting-override
checks, cluster UID verification, and dev Docker identity verification.
`scripts/cd/setup-environment.sh` registers an existing cluster without changing
its original kubeconfig. `prepare-dev-secrets.sh` provisions private dev files;
`create-kind-cluster.sh` starts/bootstrap dev without recreation. Deployment,
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

[`deploy-local.sh`](../../scripts/cd/deploy-local.sh) is the development entry point. It reads the Git-ignored project-root `.env`; `scripts/lib/application-env.cjs` parses both environments' literal settings, validates credentials, and separates Kubernetes Secret fields. [`run-k8s-cli-agent.sh`](../../scripts/dev/run-k8s-cli-agent.sh) bridges authenticated host subscription CLIs to a Kubernetes MCP service. CI/CD helpers under `scripts` classify affected components, build images, apply migrations, deploy, smoke-test, and roll back.

[`scripts/ci/with-ghcr-auth.sh`](../../scripts/ci/with-ghcr-auth.sh) owns temporary
GHCR login and command cleanup for both Jenkins pipelines and production rollback.
[`scripts/test/test-registry-auth.sh`](../../scripts/test/test-registry-auth.sh)
checks credential isolation, argument/input forwarding, failures, and cancellation
without registry access. Component-classifier tests include application test files,
CI lint/coverage-summary scripts, mixed changes, and production-to-test renames.

[`scripts/lib/image-metadata.sh`](../../scripts/lib/image-metadata.sh) owns published
repository defaults, policy reference validation, and source-URL normalization.
[`scripts/test/test-image-metadata.sh`](../../scripts/test/test-image-metadata.sh)
checks publishing and admission rendering with stub external commands.
[`scripts/test/test-registry-verification.sh`](../../scripts/test/test-registry-verification.sh)
exercises publication and deployment signature checks, including Cosign 3 bundle
summaries without annotations, retained legacy revisions, and rejected evidence. [`scripts/test/test-base-image-pins.py`](../../scripts/test/test-base-image-pins.py)
requires every Dockerfile base image to be pinned by digest under a
`# Base image: name:tag` comment; the
[supply-chain runbook](../operations/supply-chain-runbook.md#base-image-pins) covers
refreshing them. The Vite
configuration and `frontend/vite.config.test.ts` cover development proxy overrides.

Local launchers live under `scripts/dev` and `scripts/cd`; invoke those paths directly. `infra/deploy/` owns Kubernetes configuration; `infra/aws/` provisions cloud resources.
`infra/ansible/` owns the Ubuntu host foundation through `scripts/infra/server.sh`;
see the [server bootstrap guide](../operations/server-bootstrap.md). Operational instructions live under `docs/operations`.

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
before pushing. Larger balance, opening, and generation tests use named scenario
helpers to keep setup, execution, and assertions readable.
Ruleset and plan validation, recruitment, healing routes, and agent claims have
separate helpers for their individual validation or execution phases; their
owning packages and runtime contracts remain the same.

Battle inspector regions use labelled sections, and asynchronous loading messages
use native `output` elements. Component tests verify the resulting accessibility
roles rather than requiring redundant explicit role attributes.

Package-specific PostgreSQL integration tests remain beside their adapters and require `TEST_DATABASE_URL`; the isolated PostgreSQL harness supplies it. `scripts/test` contains reusable test runners and deployment script tests. Invoke the canonical runners in `scripts/test/` directly; `scripts/ci/` owns build, analysis and assurance commands. `scripts/test/test-kind-deployment.sh` checks selected-overlay staging, preservation of registered auth paths across child preflight checks, auth-only Secret inputs, collector configuration with Langfuse enabled and disabled, real Kustomize renders, and rollout failure handling. It loads the deployment environment helpers with explicit Langfuse overrides so overlay tests do not query a live cluster. `scripts/test/test-security-launchers.sh` checks scanner artifact paths, failure propagation, and the shared Trivy pin using scanner stubs. `scripts/lib/access_service.py` validates private CI credentials, destination binding and expiry; `scripts/ops/verify-access-service.py` verifies private denial and public service authentication without redirects. `scripts/test/test-access-service.py` tests those boundaries, and `test-runtime-assurance.sh` exercises service credential mounting and k6 requests. Performance scenarios and SQL fixtures live in `backend/test/performance`; their launchers live in `scripts/perf`. AI cases remain in `backend/evals`. Application browser tests already live in `frontend/e2e`, with deterministic API fixtures and desktop/mobile Chromium projects.

Merge-blocking gates require at least 80% Go statement coverage in aggregate across the module — one repository-wide total from `go tool cover`, not a per-package floor, so thin wiring in `cmd/*` may sit below 80% while the module passes — and at least 80% frontend statements, branches, functions, and lines. Measure through the PostgreSQL harness; a coverage run without `TEST_DATABASE_URL` skips tests that require the database and omits the code exercised only by those tests:

```bash
./scripts/test/with-test-postgres.sh ./scripts/test/test-go-coverage.sh
(cd frontend && npm test -- --coverage)
```

`test-go-coverage.sh` measures the backend gate. Jenkins runs
`./scripts/test/test-backend.sh` under the same harness, which runs that coverage
script and then the Go race checks (`test-go-race.sh`).

For a focused change, start with the affected package tests. Before completing implementation work, run the applicable repository coverage gate.

Frontend Vitest runs use at most two workers, configured in
`frontend/vite.config.ts`, to limit CPU contention with Jenkins's parallel Go,
fuzz, and security checks. Each test has a 15-second timeout to accommodate
multi-step DOM interactions under shared-agent load. The 80% coverage gates
remain in effect; the timeout is a hang guard, not a performance benchmark.
Testing Library async queries and `waitFor` use a five-second deadline configured
in `frontend/src/test/setup.ts`, within Vitest's separate per-test budget.

Battle-history pagination, browser-back navigation, and refresh-to-newest behavior
have separate component tests so each interaction sequence has its own timeout
budget on the shared CI agent.

### AI comparison and regression coverage

[`cmd/aieval/comparison.go`](../../backend/cmd/aieval/comparison.go) owns compare
flags and configuration; [`ai/eval/comparison.go`](../../backend/internal/ai/eval/comparison.go)
runs isolated MCP sessions through production validation. `ai/eval/knowledge.go`
exports retrieval metadata. `backend/evals/comparison-cases/` contains synthetic
contexts, including the large-roster fixture. See [comparison operations](ai-comparisons.md).

`DecisionRepository.ListFactionMemory` bounds history in SQL; the battle worker
freezes memory and selected knowledge before hashing. Tests cover faction/attempt
isolation, bounds, optional-read failure, invalid submissions, and false CLI success.
`ai/mcpserver/tool_logging_test.go` verifies safe schema-rejection diagnostics;
worker tests verify fallback error attribution.

`game/movement_test.go` covers blocked approaches and enemy filtering.
Recruitment tests in `game`, `game/controller`, and `ai` cover explicit quantities,
reserves, spawn blocking, empty orders, and validation. `AIDecisionPanel.test.tsx`
checks their presentation. `frontend/src/app/App.test.tsx` uses controlled timers
to verify event-page draining without load-sensitive wall-clock deadlines.

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
- `scripts/security/capture-zap-traffic.py` verifies and sanitizes authenticated CI
  reads for an offline ZAP passive scan. `test-production-hardening.py` covers
  destination binding, redirect rejection, credential exclusion, TLS staging,
  recovery mismatches, policy-only reconciliation and scanner failure propagation.

## Cross-review tooling

`scripts/review/cross-review.py` resolves a committed diff and temporary source
snapshot, invokes the opposite CLI reviewer and saves private reports outside the
repository. `--pr` publishes a completed review after checking the live PR revisions;
`--publish-only` retries publication without another model call. Marked PR
review comments determine the prior reviewed head and enforce a two-round
automatic limit. Follow-ups diff that head against the new commit and receive
only the latest report/author summary in `discussion.json`. Full discussion
fingerprints still prevent knowingly stale publication. An explicitly approved
`--allow-extra-round` permits one more focused call. `scripts/test/test-cross-review.py` checks snapshot boundaries,
read-only CLI options, version/argument checks, rejected responses, publication
identity/body matching, delta-only follow-ups, compact context, round limits,
process timeouts, and per-review token usage accounting (`--usage`). Codex and Claude Code
2.1.277 or newer load the shared `AGENTS.md` rules at startup; the repository has
no `CLAUDE.md`, so older Claude Code sessions start without them. See the
[cross-review workflow](cross-review.md).

## Container build contexts

Image builders use `backend/` and `frontend/` as separate Docker contexts.
Each component owns its `.dockerignore`; root exclusions do not apply to those
contexts. Both exclude `.env`, `.env.*`, and nested equivalents while retaining
`.env.example`. Local credentials and Vite overrides must not enter builder layers.
`./scripts/test/test-docker-context.sh` verifies these boundaries with synthetic
files and scratch images; it requires Docker but no registry pulls or credentials. Jenkins runs it during
PR deployment checks and the main deployment-scope stage, separately from the
offline deployment-script suite.

The manual [GHCR retention report](../../scripts/ops/ghcr-retention-report.sh)
remains under `scripts/ops`. It inventories package versions and protected image
digests without deleting registry data. See the
[retention runbook](../operations/supply-chain-runbook.md#artifact-identity-and-retention).
