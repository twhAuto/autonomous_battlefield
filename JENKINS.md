# Jenkins and GitHub deployment pipeline

The repository owns its pipeline in `Jenkinsfile`. GitHub stores the source and
notifies Jenkins about branches and pull requests; Jenkins checks out the exact
commit and executes the pipeline on an agent.

```text
GitHub branch or pull request
          |
          v
Jenkins Multibranch Pipeline
          |
          +--> test Go, frontend, and Kubernetes rendering
          +--> build SHA and 0.1.0-build.N image tags
          +--> scan images with pinned Trivy
          |
          `--> main only: migrate, deploy, smoke test, rollback on failure
```

Pull requests and non-`main` branches stop after the scan. Only `main` receives
the local `kind` deployment credentials and can deploy.

## Jenkins agent requirements

Use a dedicated Linux or WSL Jenkins agent with:

- Git.
- Go 1.26.5 or newer.
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

Keep Jenkins core and plugins patched. The pipeline invokes command-line tools
that are installed on the agent rather than downloading toolchains dynamically.

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
5. Keep the script path as `Jenkinsfile`.
6. Save, then select **Scan Repository Now**.

Jenkins creates a job for each discovered branch containing a `Jenkinsfile`.
For a private repository, prefer a narrowly scoped GitHub App or token managed
by Jenkins rather than embedding credentials in the repository.

A GitHub webhook cannot reach Jenkins through `localhost`. While Jenkins runs
only inside local WSL, trigger **Scan Repository Now** or configure periodic
repository scans. Add a webhook only after Jenkins has an authenticated,
HTTPS-protected address reachable from GitHub.

## Pipeline behavior

The committed pipeline performs these gates in order:

1. Clean checkout of the exact GitHub commit.
2. Parallel Go tests, frontend install/tests/build, and Kustomize rendering.
3. Image build through `scripts/build-images.sh`. Jenkins `BUILD_NUMBER`
   produces the human-readable build alias; the full SHA remains immutable.
4. Image scanning through `scripts/scan-images.sh`. Trivy v0.74.0 is pinned by
   image digest and rejects fixable HIGH or CRITICAL findings.
5. On `main`, a one-shot migration Job followed by the application rollouts.
6. Frontend liveness, API readiness, and frontend-to-API proxy smoke tests.

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
BUILD_NUMBER=1 ./scripts/build-images.sh
./scripts/scan-images.sh "$(git rev-parse HEAD)"
ROLLBACK_ON_FAILURE=1 ./scripts/deploy-kind.sh "$(git rev-parse HEAD)"
./scripts/smoke-test-kind.sh
```

These commands exercise the same local path, but they do not validate Jenkins
Declarative Pipeline syntax. Use the configured Jenkins Multibranch job for the
final `Jenkinsfile` validation.
