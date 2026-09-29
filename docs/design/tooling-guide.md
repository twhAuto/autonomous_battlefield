# OrdinalFrontier Tooling Guide

[Codebase index](codebase-guide.md) · [System architecture](system-architecture.md)

## Environment tooling

`scripts/lib/deployment-environment.sh` owns profile loading, conflicting-override
checks, cluster UID verification, and dev Docker identity verification.
`scripts/cd/setup-environment.sh` registers an existing cluster without changing
its original kubeconfig. `prepare-dev-secrets.sh` provisions private dev files;
`create-kind-cluster.sh` starts/bootstrap dev without recreation. Deployment,
rollback, smoke, dev CLI agents, retention reports, and runtime assurance share
the resolver. `scripts/test/test-deployment-environment.sh` exercises rejection
and bootstrap paths. See the [environment guide](../operations/environments.md)
for machine configuration, Jenkins credentials, and commands.

## Deployment code

[`deploy/base`](../../deploy/base) contains portable Kustomize resources. [`deploy/overlays/local`](../../deploy/overlays/local) adds kind-specific PostgreSQL, Gateway API, observability, and logging resources.

The key application manifests are:

- `api-deployment.yaml` and `api-service.yaml`;
- `engine-deployment.yaml`;
- `agent-deployment.yaml`;
- `mcp-deployment.yaml`, `mcp-service.yaml`, and `mcp-network-policy.yaml`;
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
CI compatibility wrappers, mixed changes, and production-to-test renames.

[`scripts/lib/image-metadata.sh`](../../scripts/lib/image-metadata.sh) owns published
repository defaults, policy reference validation, and source-URL normalization.
[`scripts/test/test-image-metadata.sh`](../../scripts/test/test-image-metadata.sh)
checks publishing and admission rendering with stub external commands.
[`scripts/test/test-registry-verification.sh`](../../scripts/test/test-registry-verification.sh)
exercises publication and deployment signature checks, including Cosign 3 bundle
summaries without annotations, retained legacy revisions, and rejected evidence. The Vite
configuration and `frontend/vite.config.test.ts` cover development proxy overrides.

Root launch scripts remain compatibility entry points that `exec` their implementations under `scripts/dev` and `scripts/cd`. `deploy/` owns Kubernetes configuration; `infra/aws/` provisions cloud resources. Operational instructions live under `docs/operations`.

## Testing layout and expectations

Tests are colocated with their Go or React implementation. The codebase includes:

- pure domain tests for map rules, combat, economy, and determinism;
- service tests for validation and use cases;
- PostgreSQL adapter tests with mocked query behavior and integration coverage;
- worker tests for leases, cancellation, decisions, fallback, pause/resume, and stop;
- provider, MCP, runner, profile, and capability tests;
- HTTP contract/status tests;
- React component and typed API-client tests.

`backend/internal/testutil.Require` shares fatal assertion checks while evaluating
diagnostics only on failure. Larger balance, opening, and generation tests use
named scenario helpers to keep setup, execution, and assertions readable.
Ruleset and plan validation, recruitment, healing routes, and agent claims have
separate helpers for their individual validation or execution phases; their
owning packages and runtime contracts remain the same.

Battle inspector regions use labelled sections, and asynchronous loading messages
use native `output` elements. Component tests verify the resulting accessibility
roles rather than requiring redundant explicit role attributes.

Package-specific PostgreSQL integration tests remain beside their adapters and require `TEST_DATABASE_URL`; the isolated PostgreSQL harness supplies it. `scripts/test` contains reusable test runners and deployment script tests. Old `scripts/ci` test commands forward to those runners. `scripts/test/test-command-wrappers.sh` verifies argument, environment, working-directory, exit-status, and signal forwarding without launching the application. `scripts/test/test-kind-deployment.sh` checks selected-overlay staging, preservation of registered auth paths across child preflight checks, auth-only Secret inputs, collector configuration with Langfuse enabled and disabled, real Kustomize renders, and rollout failure handling. It loads the deployment environment helpers with explicit Langfuse overrides so overlay tests do not query a live cluster. `scripts/test/test-security-launchers.sh` checks scanner artifact paths, failure propagation, and the shared Trivy pin using scanner stubs. `scripts/lib/access_service.py` validates private CI credentials, destination binding and expiry; `scripts/ops/verify-access-service.py` verifies private denial and public service authentication without redirects. `scripts/test/test-access-service.py` tests those boundaries, and `test-runtime-assurance.sh` exercises service credential mounting and k6 requests. Performance scenarios live in `tests/performance`; AI cases remain in `backend/evals`. Application browser tests already live in `frontend/e2e`, with deterministic API fixtures and desktop/mobile Chromium projects.

Merge-blocking gates require at least 80% Go statement coverage in aggregate across the module — one repository-wide total from `go tool cover`, not a per-package floor, so thin wiring in `cmd/*` and `internal/postgres` may sit below 80% while the module passes — and at least 80% frontend statements, branches, functions, and lines. Measure through the PostgreSQL harness; a coverage run without `TEST_DATABASE_URL` skips tests that require the database and omits the code exercised only by those tests:

```bash
./scripts/test/with-test-postgres.sh ./scripts/test/test-backend.sh
(cd frontend && npm test -- --coverage)
```

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
contexts, including the large-roster fixture. See [comparison operations](../operations/ai-comparisons.md).

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
timeout target diagnostics used by `scripts/cd/smoke-test-kind.sh`. Run
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
identity/body matching, delta-only follow-ups, compact context, round limits
and process timeouts. `CLAUDE.md` points
Claude to the shared `AGENTS.md` rules. See the
[cross-review workflow](../operations/cross-review.md).

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
