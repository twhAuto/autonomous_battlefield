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
[shared-agent tradeoff](../operations/jenkins.md#production-hardening-rollout).
Pushes to `dev` and work branches run no pipeline: a merged `dev` head is first
built by the next `dev`→`main` promotion PR.

Only `main` is protected. Its ruleset requires a pull request and the `Jenkins` and
`Coverage` checks, and blocks direct pushes; Jenkins sees a `main` update rather
than a distinct "PR merged" event, so that rule is what makes a `main` update
equivalent to an approved merge. `dev` has no ruleset: work branches still open
pull requests into `dev`, but Jenkins results there are advisory and maintainers
may push directly. See [branch protection](../operations/jenkins.md#configure-github-integration-branch-protection).

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

The shared `scripts/test/test-deployment.sh` gate includes offline Helm bootstrap
and Langfuse detection regressions. The chart-rendering validator
`scripts/test/test-langfuse-security.py` remains an explicit chart-upgrade check;
see [Langfuse operations](../operations/langfuse.md).

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
`test-go-coverage.sh` and then the Go race checks. Start a focused change with the
affected package's tests, and run the applicable gate before declaring it complete.

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

## Observability rule checks

Run `./scripts/test/test-prometheus-rules.sh` to check the Prometheus recording
and alert rules with `promtool`. It requires Docker and uses the deployed
Prometheus image. The fixtures cover both healthy traffic with no 5xx series
and failing traffic. The deployment regression suite includes these checks.
