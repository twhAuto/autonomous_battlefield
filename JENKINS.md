# Jenkins and GitHub deployment pipeline

The repository owns its pipeline in `Jenkinsfile`. GitHub stores the source and
notifies Jenkins about branches and pull requests; Jenkins checks out the exact
commit and executes the pipeline on an agent.

```text
Pull request targeting main
          |
          `--> lint, test with coverage, render, build immutable images, and scan

Merge into main
          |
          `--> regenerate coverage, pass the SonarQube quality gate,
               rebuild and scan the exact merge SHA, migrate, deploy,
               smoke test, and rollback on failure
```

Direct pushes to non-`main` branches do not run CI or CD stages. This keeps
`dev` available as a local work branch: use `run-dev.sh` while developing, then
open a pull request when the change is ready for Jenkins CI. Only `main`
receives the local `kind` deployment credential and can deploy.

The `main` branch must require pull requests and disallow direct pushes. Jenkins
sees a `main` update rather than a distinct "PR merged" pipeline condition, so
the branch rule is what makes a `main` update equivalent to an approved merge.

## Jenkins agent requirements

Use a dedicated Linux or WSL Jenkins agent with:

- Git.
- A complete Go 1.26.5 or newer installation, including the `covdata` tool.
- Node.js 24 and npm.
- Docker, with permission to use the Docker daemon.
- `kubectl`, `kind`, and `curl`.
- Access to the `battlefield` kind cluster through context `kind-battlefield`.

The agent needs Docker access to build images and run the pinned Trivy scanner.
Docker daemon access is effectively privileged host access. Do not run untrusted
fork pull requests on this agent, and do not use the Jenkins controller itself
as the long-term build agent.

## Required Jenkins plugins

- Pipeline.
- Git.
- GitHub Branch Source.
- Credentials Binding.
- SonarQube Scanner for Jenkins.

Keep Jenkins core and plugins patched. The pipeline invokes command-line tools
that are installed on the agent rather than downloading toolchains dynamically.

Verify the Go installation under the same account that runs the agent:

```bash
go version
test -x "$(go env GOTOOLDIR)/covdata"
```

If the second command fails, reinstall the complete Go distribution. Normal
`go test` can still pass with this file missing, but coverage generation cannot.

## Configure local SonarQube

The repository includes a persistent local SonarQube Community Build and
PostgreSQL Compose stack. It is separate from the application database:

```bash
docker compose -f quality/sonarqube/compose.yaml up -d
curl --fail http://localhost:9000/api/system/status
```

The first startup can take several minutes. Open `http://localhost:9000`, sign
in with the initial `admin` / `admin` credentials, change the password, and
create an analysis token under the account security settings.

Configure Jenkins as follows:

1. Add the token as a **Secret text** credential.
2. In **Manage Jenkins > System > SonarQube servers**, add a server named
   `battlefield-sonarqube`, URL `http://localhost:9000`, using that credential.
3. In **Manage Jenkins > Tools > SonarQube Scanner installations**, add an
   installation named `SonarScanner` and enable automatic installation.
4. In SonarQube, add a webhook pointing to
   `http://host.docker.internal:8080/sonarqube-webhook/`. Change port `8080` if
   Jenkins uses a different one. The trailing slash is required.

The webhook lets `waitForQualityGate` resume the Jenkins pipeline without
polling. The Compose service includes the Docker host mapping needed to reach a
Jenkins service running on the WSL host.

SonarQube imports `backend/coverage.out` and `frontend/coverage/lcov.info` using
`sonar-project.properties`. It does not generate those reports itself. The
Community Build is analyzed only for merged `main`; PR CI still runs Go lint and
prints both backend and frontend coverage before merge. This avoids unsupported
Community Build pull-request analysis while keeping the deployment behind the
quality gate.

The SonarQube data is stored in named Docker volumes and survives restarts. Stop
it without deleting its data with:

```bash
docker compose -f quality/sonarqube/compose.yaml stop
```

Do not use `docker compose down -v` unless you intentionally want to erase the
SonarQube database and configuration.

## Add the local database credential

Create a local secret file using the same three-line format as
`deploy/overlays/local/infrastructure/secret.env.example`:

```text
POSTGRES_USER=...
POSTGRES_PASSWORD=...
DATABASE_URL=...
```

In Jenkins, add it as a **Secret file** credential with this exact ID:

```text
battlefield-local-secret-env
```

The `main` deployment stage binds the credential to a temporary file. The
deployment script copies it into a private temporary Kustomize overlay and
removes that directory on exit. Jenkins must mask and manage the credential; do
not commit the real file or print it in the pipeline.

## Create the GitHub Multibranch Pipeline

1. In Jenkins, select **New Item** and **Multibranch Pipeline**.
2. Add a **GitHub** branch source.
3. Set the repository URL to
   `https://github.com/twh09/autonomous_battlefield.git`.
4. Configure GitHub scan and checkout credentials if required.
5. Under **Behaviors**, configure **Discover branches** to exclude branches
   that are also filed as pull requests, and configure **Discover pull requests
   from origin** to merge the pull request with the current target revision.
6. Under **Scan Multibranch Pipeline Triggers**, enable **Periodically if not
   otherwise run** while Jenkins is reachable only through `localhost`.
7. Keep the script path as `Jenkinsfile`.
8. Save, then select **Scan Repository Now**.

Jenkins creates a job for each discovered branch containing a `Jenkinsfile`.
For a private repository, prefer a narrowly scoped GitHub App or token managed
by Jenkins rather than embedding credentials in the repository.

A GitHub webhook cannot reach Jenkins through `localhost`. While Jenkins runs
only inside local WSL, trigger **Scan Repository Now** or configure periodic
repository scans. Add a webhook only after Jenkins has an authenticated,
HTTPS-protected address reachable from GitHub.

## Pipeline behavior

For a pull request targeting `main`, the pipeline performs:

1. Clean checkout of the exact GitHub commit.
2. Parallel Go formatting/vet, Go tests with a coverage profile, frontend tests
   with LCOV coverage, frontend build, and Kustomize rendering.
3. Image build through `scripts/build-images.sh`. Jenkins `BUILD_NUMBER`
   produces the human-readable build alias; the full SHA remains immutable.
4. Image scanning through `scripts/scan-images.sh`. Trivy v0.74.0 is pinned by
   image digest and rejects fixable HIGH or CRITICAL findings.

After that pull request is merged, the `main` pipeline performs:

1. Clean checkout of the exact merged commit.
2. Regenerate coverage and wait for the SonarQube quality gate.
3. Rebuild and scan immutable images tagged with the merge SHA. A PR validation
   SHA and the final merge SHA are not assumed to be interchangeable.
4. A one-shot migration Job followed by the application rollouts.
5. Frontend liveness, API readiness, and frontend-to-API proxy smoke tests.

`scripts/deploy-kind.sh` can reapply a temporary Kustomize overlay containing
the previous images if an application apply or rollout fails. Before deploying,
Jenkins records the currently deployed full SHA. If post-deployment smoke
testing fails, `scripts/rollback-kind.sh` redeploys that exact immutable
revision. Database migrations are deliberately not reversed automatically, so
migrations must remain backward-compatible with the previous application
revision.

The local pipeline loads images directly into kind. The later AWS version will
push both tags to ECR and deploy the SHA tag to EKS while preserving the same
test, scan, migration, verification, and rollback gates.

## Run the pipeline commands without Jenkins

After committing the intended source so the worktree is clean:

```bash
./scripts/lint-go.sh
./scripts/test-go-coverage.sh
(cd frontend && npm ci && npm test -- --coverage && npm run build)
BUILD_NUMBER=1 ./scripts/build-images.sh
./scripts/scan-images.sh "$(git rev-parse HEAD)"
ROLLBACK_ON_FAILURE=1 ./scripts/deploy-kind.sh "$(git rev-parse HEAD)"
./scripts/smoke-test-kind.sh
```

These commands exercise the same local path, but they do not validate Jenkins
Declarative Pipeline syntax. Use the configured Jenkins Multibranch job for the
final `Jenkinsfile` validation.
