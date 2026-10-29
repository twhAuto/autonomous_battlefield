# Continuous integration for contributors

Use this guide to understand pull-request checks, diagnose failures and reproduce
checks locally. Controller, agent, credential and production-delivery procedures
are in [Jenkins operations](../operations/jenkins.md).

The repository owns its pipeline in `Jenkinsfile`. GitHub stores the source;
the local Jenkins Multibranch job discovers branches and pull requests through
periodic scans, checks out the exact commit, and executes the pipeline on an
agent. A public Jenkins installation can use a GitHub webhook instead.

```text
Pull request targeting dev or main
          |
          `--> lint, test with coverage, publish a GitHub Coverage check,
               pass the SonarQube quality gate, render, build immutable
               images, and scan

Merge into main
          |
          `--> regenerate coverage, pass the SonarQube quality gate,
               rebuild and scan the exact merge SHA, migrate, deploy,
               smoke test, and rollback on failure
```

The diagram summarizes the branch split; [pull-request verification](#pull-request-verification) and [production delivery](../operations/jenkins.md#production-delivery)
list the current gates, publication, and verification stages.

The Declarative Pipeline groups these paths under `PR verification` and
`Main delivery`. Jenkins still displays the inactive parent stage as skipped so
the branch decision remains auditable, but it does not execute that parent's
nested stages. Within an active `Main delivery` run, image and deployment stages
can still appear as skipped when the changed-component classifier determines
that they are unnecessary; those skips are intentional and remain visible.

PRs targeting `dev` or `main`, main delivery and assurance all run on the shared
`battlefield-production` agent; see the
[shared-agent tradeoff](../operations/jenkins.md#shared-ci-and-production-agent).
Pushes to `dev` and work branches run no pipeline: a merged `dev` head is first
built by the next `dev`→`main` promotion PR.

Only `main` is protected. Its ruleset requires a pull request and the `Jenkins` and
`Coverage` checks, and blocks direct pushes; Jenkins sees a `main` update rather
than a distinct "PR merged" event, so that rule is what makes a `main` update
equivalent to an approved merge. `dev` has no ruleset: work branches still open
pull requests into `dev`, but Jenkins results there are advisory and maintainers
may push directly. See [branch protection](../operations/jenkins.md#main-branch-protection).

Work branches start from the latest fetched `origin/dev` and open PRs into `dev`
([agent branch guidance](../../AGENTS.md#branches-and-pull-requests)). A
`dev`-to-`main` promotion is a separate release PR. After it merges, bring `dev` up
to date so the next promotion contains only new work:

```bash
git switch main
git pull --ff-only origin main
git switch dev
git merge main
git push origin dev
```

## Pull-request verification

For a pull request targeting `dev` or `main`, the pipeline performs:

1. Clean checkout of the exact GitHub commit.
2. Parallel Go lint, isolated PostgreSQL-backed tests (coverage and race checks),
   frontend coverage/build, source security and policy validation, fuzz smoke,
   deterministic AI evaluation, and deployment-script checks.
3. Publication of the GitHub `Coverage` check.
4. SonarQube preview-project analysis and quality-gate enforcement for the exact
   PR checkout.
5. Local image builds, frontend container startup/static-asset verification and
   vulnerability scans, without publication or production credentials.

Backend integration packages run concurrently against the disposable PostgreSQL
database. Migration fixtures use the same advisory lock as startup, including
tests that upgrade or downgrade selected versions. Separate schemas alone do not
isolate shared role/grant changes. A failed backend test stops the coverage run;
the published `Unavailable` backend coverage result is not evidence of a low
coverage percentage. Inspect the failing test before changing any coverage gate.

All deployment regression suites run through `./scripts/test/test-deployment.sh`
on both PR and `main` builds, using fixtures and no production secrets. It covers
tunnel preparation, policy boundaries, recovery approval, authenticated capture,
per-replica scrape checks, offline Helm bootstrap, Langfuse detection and internal
documentation links and anchors. The
chart-rendering validator `scripts/test/test-langfuse-security.py` remains an
explicit chart-upgrade check; see [Langfuse operations](../operations/langfuse.md).

## What the pipeline runs

This section explains the tools and checks behind the stages above, for
diagnosing a failure. Agent setup and credentials are in
[Jenkins operations](../operations/jenkins.md).

### Pinned tools and registry access

Security scanners, Kubeconform, Conftest, Syft, Cosign, ORAS, k6 and ZAP run from
the versions pinned in `scripts/ci/tool-versions.env`, which is also the default
Trivy pin for local image scans (`TRIVY_IMAGE` overrides it locally). The
source-security stage owns the shared manifest rendering, schema checks and policy
checks on both PR and `main` builds; the deployment-script checks exercise
staging, component selection and scanner launchers separately.
`SECURITY_ARTIFACT_DIR` relocates the security reports, including the rendered
manifest that Trivy and Conftest consume.

Pipelines place `GOCACHE`, `GOTMPDIR` and temporary files in Jenkins's
workspace-associated temporary directory, share that Go cache across parallel
stages, and remove it before archiving build evidence.

`main` builds publish to the private GHCR packages
`autonomous-battlefield-backend` and `autonomous-battlefield-frontend`, which
inherit repository access. The publishing script creates or reuses a
`docker-container` Buildx builder named `battlefield-jenkins`; that driver keeps
BuildKit SBOM and provenance attestations even when the daemon uses the classic
image store. Provenance is emitted as SLSA v0.2 so the extracted predicate matches
Cosign's `slsaprovenance` schema.

Cosign publishes each signature and signed attestation as a v3 OCI 1.1 referrer
and also as a legacy digest-tag mirror. The referrers are the portable promotion
evidence; the mirror exists because the pinned Kyverno 1.15 admission verifier
discovers `.sig` and `.att` tags, not referrer-only evidence. Both forms are signed
with the same Jenkins-managed key and cover the same digest.

[`scripts/ci/with-ghcr-auth.sh`](../../scripts/ci/with-ghcr-auth.sh) gives
publishing, image scanning and startup checks, signing, deployment, rollback and
AWS promotion a mode-700 temporary `DOCKER_CONFIG`. It logs in through standard
input, forwards arguments and exit status, and removes the credentials on success,
failure or cancellation, so GHCR credentials never reach the agent's
`~/.docker/config.json`. Docker may still warn that the temporary configuration is
unencrypted. `BUILDX_CONFIG` stays on the agent so the named builder can be reused.
[`test-registry-auth.sh`](../../scripts/test/test-registry-auth.sh) checks this
lifecycle with a stub Docker command and never contacts a registry.

### Image naming and source metadata

Publication and policy rendering share the image repository defaults in
`scripts/lib/image-metadata.sh`. `REGISTRY_NAMESPACE` changes the namespace, and
`PUBLISHED_BACKEND_IMAGE` and `PUBLISHED_FRONTEND_IMAGE` override individual
repository names, which must omit tags, digests and wildcards. Registry
credentials must still grant access to the chosen repositories.

Image source labels come from Git `origin`, normalizing HTTPS and common SSH URLs;
`SOURCE_URL=https://github.com/your-org/your-repo` overrides the label.
Credential-bearing or unsupported URLs are rejected without printing them.
Publishing requires a source URL; local builds without an origin omit the label.

`test-image-metadata.sh` runs the real build, publish and policy-rendering scripts
with stub Docker and Kubernetes commands, covering repository overrides, preserved
images, source metadata and invalid input. `test-docker-context.sh` builds
synthetic scratch contexts to check that each component excludes local environment
files while keeping source and examples. PR and `main` builds run both.

### Source security

The source-security stage first runs a full npm audit from a digest-pinned Node
container. If the npm advisory endpoint is still unavailable after a bounded retry,
the stage requires a passing Trivy lockfile scan of all frontend dependencies,
including development and unfixed vulnerabilities. A definitive npm vulnerability
report is never replaced by that fallback. The archived `npm-audit-mode.json`
records which path supplied the evidence, and npm debug logs are archived under
`artifacts/security/npm-logs/` (paths printed by npm are inside the container).

The Trivy source scan skips the generated `artifacts`, `frontend/coverage`,
`frontend/playwright-report` and `frontend/test-results` directories, because
Vitest creates and removes temporary coverage files during the parallel frontend
stage. Scanning them can otherwise fail with
`stat frontend/coverage/.tmp/coverage-*.json: no such file or directory`. Frontend
source and lockfiles stay in the scan.

### Browser tests and reports

Vitest runs with at most two workers and a 15-second per-test timeout
(`frontend/vite.config.ts`), and Testing Library's async queries wait up to five
seconds (`frontend/src/test/setup.ts`). That gives DOM tests headroom while other
builds share the agent; every assertion and the 80% coverage gates still apply.

Both frontend stages install the lockfile-matched Chromium with
`npx playwright install chromium` and run `npm run test:e2e` after coverage and
build. The suite starts a Vite server on port 4173, intercepts every API route with
deterministic fixtures, and covers desktop and mobile Chromium with one worker in
CI; it needs no shared database or live AI. Each stage then asserts
`test -s playwright-report/index.html`, so a run without a report fails in the
stage.

An always-run post step archives `frontend/test-results/**/*`, including failure
screenshots and traces, and allows an empty archive because those paths depend on
which stages ran. `frontend/playwright-report/**/*` is archived separately, only
when `index.html` exists, and does not allow an empty archive. Builds that never
reach the frontend stages log that no report was produced.

### k6 and ZAP networking

Development k6 and ZAP containers use host networking on the Linux agent and map
`battlefield.localhost` to `127.0.0.1`, matching kind's loopback-only gateway, so
the Docker daemon must run on the agent's host. Both run with the invoking user's
UID and GID so their bind-mounted reports stay writable and owned by Jenkins; ZAP
uses container-local `/tmp` for its home and working directory. `TARGET_URL` and
`ZAP_TARGET_URL` override the targets. A refused connection to `172.17.0.1:8888`
means an outdated launcher is using the Docker bridge gateway.

`./scripts/test/test-runtime-assurance.sh` checks launcher networking, report
ownership, target overrides and failure propagation without starting scanners or
contacting a deployment. Production k6 and ZAP are described under
[production delivery](../operations/jenkins.md#production-k6-and-zap).

## Frontend image checks

Both PR and main deployment-script checks run
`./scripts/test/test-frontend-image.sh`: it copies tracked frontend inputs into a
private temporary context with files `0600` and directories `0700`, builds the
actual Dockerfile, verifies startup, and proves an unreadable template is rejected.
It needs Docker with BuildKit and access to the configured base-image/package
registries; it uses the default daemon-backed builder and removes its child image
before the parent and temporary files. It deliberately runs for every PR/main
quality check, including changes that select no deployment, to exercise startup
independently of the real checkout modes. BuildKit can reuse this regression's
cache across runs.

`scripts/ci/verify-frontend-image.py IMAGE` checks an already-local image as UID/GID
101 with a read-only root filesystem, dropped capabilities, no new privileges,
and the production writable paths in temporary filesystems. It publishes no ports,
uses no application credentials and disables container networking. Its resolver
input is pinned to loopback so host DNS changes cannot invalidate this isolated
check. It checks all three Kubernetes probes, the built index and its JavaScript/CSS entry assets over
loopback HTTP. PR/local image builds invoke it after building the frontend; main
pulls and checks the scanned frontend digest before signing or rollout. Failures
print Docker diagnostics and bounded container logs and remove only that check's
container. The published-image gate removes digest references it pulled solely for
verification on success or failure, while retaining references already present on
the agent. Cleanup failure emits a warning. These image checks do not verify the API proxy or replace Kubernetes deployment smoke tests.

## Run local build and deployment checks without Jenkins

After [dev environment setup](local-setup.md#wsl-setup-and-normal-use) and
committing the intended source so the worktree is clean:

```bash
./scripts/ci/lint-go.sh
./scripts/test/with-test-postgres.sh ./scripts/test/test-go-coverage.sh
(cd frontend && npm ci && npm test -- --coverage && npm run build)
BUILD_NUMBER=1 ./scripts/ci/build-images.sh
./scripts/ci/scan-images.sh "$(git rev-parse HEAD)"
ROLLBACK_ON_FAILURE=1 ./scripts/cd/deploy.sh "$(git rev-parse HEAD)"
./scripts/cd/smoke-test.sh
```

Set `DEPLOY_COMPONENTS=frontend`, `backend`, `observability`, or another
supported comma-separated selection on the build, scan, and deploy commands to
exercise an incremental path. Run
`./scripts/test/test-deployment-components.sh` to verify the path classifier.

These commands exercise local build/deploy checks after dev environment setup;
they do not publish, sign, or verify production registry artifacts or validate Jenkins
Declarative Pipeline syntax. Use the configured Jenkins Multibranch job for the
final `Jenkinsfile` validation.

### Coverage gate details

This section owns the coverage rule; other guides link here.

```bash
./scripts/test/with-test-postgres.sh ./scripts/test/test-go-coverage.sh
(cd frontend && npm test -- --coverage)
```

Jenkins runs `./scripts/test/test-backend.sh` under the same harness, which runs
`test-go-coverage.sh` and then the Go race checks. The race detector reports only
memory shared between goroutines, so in the race run, engine and catalog tests that
play a whole battle in one goroutine skip themselves (`skipSequentialBattleUnderRace`
in `internal/game` and `internal/battle/catalog`), and the parallel map sweep plays
only each map's first 100 transitions. The coverage run plays all of them in full.
Mark any new long single-goroutine battle test the same way; under the race detector
it can take minutes on the shared agent. Start a focused change with the affected
package's tests, and run the applicable gate before declaring it complete.

Backend Go statement coverage must be at least 80% in aggregate across the
module, as reported by the `total:` line from `go tool cover`. The gate is a
single repository-wide number, not a per-package floor, so thin wiring in
`cmd/*` may sit below 80% while the module passes. A low
per-package figure is not a gate failure, and the aggregate is not licence to
leave new behavior untested. Frontend statements, branches, functions and lines
must each be at least 80%.

Backend integration tests skip themselves unless `TEST_DATABASE_URL` is set, so
code exercised only by those tests stays uncovered. Always measure through
`with-test-postgres.sh`, which starts a throwaway PostgreSQL container and exports
`TEST_DATABASE_URL` and its exact `TEST_POSTGRES_CONTAINER_ID` for the wrapped
command. `test-go-coverage.sh` rejects a missing database URL before running Go,
so a coverage invocation cannot silently omit database-backed tests. The harness
requires Docker; if it cannot run, report that CI-equivalent coverage was not
verified. The supported coverage command cannot produce a figure without the
harness-provided database; a plain Go test run is not equivalent evidence.

### Source-security failures

The source-security stage logs each failed check and its report path. Inspect the
matching file under the build's archived `artifacts/security/` directory to
separate dependency findings from rendered-manifest or workload-policy failures.

## Local SonarQube

The repository includes a SonarQube Community Build and PostgreSQL Compose stack
for local analysis. It is separate from the application database and from the
registered server's [managed deployment](../operations/jenkins.md#sonarqube-server),
and it does not configure Jenkins.

```bash
docker compose -f infra/sonarqube/compose.yaml up -d
curl --fail http://localhost:9000/api/system/status
```

On a fresh instance, sign in as `admin` / `admin`, change the password, and create
a private analysis token; keep it out of Git. The containers use `unless-stopped`,
so they restart with Docker. Data lives in named volumes; stop the stack without
losing it, and resume it with `up -d`:

```bash
docker compose -f infra/sonarqube/compose.yaml stop
```

Do not run `docker compose down -v` unless you intend to erase the SonarQube
database and configuration.

## Observability rule checks

Run `./scripts/test/test-prometheus-rules.sh` to check the Prometheus recording
and alert rules with `promtool`. It requires Docker and uses the deployed
Prometheus image. The fixtures cover both healthy traffic with no 5xx series
and failing traffic. The deployment regression suite includes these checks.
