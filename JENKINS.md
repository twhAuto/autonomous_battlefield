# Jenkins and GitHub deployment pipeline

The repository owns its pipeline in `Jenkinsfile`. GitHub stores the source;
the local Jenkins Multibranch job discovers branches and pull requests through
periodic scans, checks out the exact commit, and executes the pipeline on an
agent. A public Jenkins installation can use a GitHub webhook instead.

```text
Pull request targeting main
          |
          `--> lint, test with coverage, publish a GitHub Coverage check,
               render, build immutable images, and scan

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

## Install the Jenkins controller on Ubuntu or WSL

This project currently uses the Jenkins LTS Debian package as a system service
on port 8080. Install Java before Jenkins, then use the official LTS package
repository:

```bash
sudo apt update
sudo apt install -y fontconfig openjdk-21-jre

sudo install -m 0755 -d /etc/apt/keyrings
sudo wget -O /etc/apt/keyrings/jenkins-keyring.asc \
  https://pkg.jenkins.io/debian-stable/jenkins.io-2026.key
echo "deb [signed-by=/etc/apt/keyrings/jenkins-keyring.asc]" \
  https://pkg.jenkins.io/debian-stable binary/ | \
  sudo tee /etc/apt/sources.list.d/jenkins.list >/dev/null

sudo apt update
sudo apt install -y jenkins
sudo systemctl enable --now jenkins
sudo systemctl status jenkins
```

If the signing-key filename has changed, use the current commands from the
[official Jenkins Linux installation guide](https://www.jenkins.io/doc/book/installing/linux/)
rather than guessing a replacement. Open `http://localhost:8080`, retrieve the
one-time unlock password locally, install the suggested plugins, and create the
administrator account:

```bash
sudo cat /var/lib/jenkins/secrets/initialAdminPassword
```

The Debian package persists controller state under `/var/lib/jenkins` and
enables `jenkins.service` at boot. Restarting the computer does not require
recreating credentials, tools, nodes, or Multibranch jobs. Useful controller
commands are:

```bash
sudo systemctl status jenkins
sudo systemctl restart jenkins
sudo journalctl -u jenkins --since today
```

Restart Jenkins after installing a plugin when the plugin manager requests it.
Installing a plugin alone does not create its global tool configuration; for
example, `SonarScanner` must still be added under **Manage Jenkins > Tools**.

## Jenkins agent requirements

Use a dedicated Linux or WSL Jenkins agent with:

- Git.
- Go 1.26.5 or newer.
- Node.js 24 and npm.
- GCC and the C development toolchain required by `go test -race`.
- Docker, with permission to use the Docker daemon.
- `kubectl`, `kind`, `curl`, and AWS CLI for the optional promotion job.
- Access to the `battlefield` kind cluster through context `kind-battlefield`.

The agent needs Docker access to build images and run the pinned Trivy scanner.
The publishing script creates or reuses a dedicated `docker-container` Buildx
builder named `battlefield-jenkins`; this driver preserves BuildKit SBOM and
provenance attestations when images are pushed even if the daemon uses the
classic local image store.
Docker daemon access is effectively privileged host access. Do not run untrusted
fork pull requests on this agent, and do not use the Jenkins controller itself
as the long-term build agent.

Security scanners, Kubeconform, Conftest, Syft, Cosign, ORAS, k6, and ZAP run
from versions pinned in `scripts/ci/tool-versions.env`. Main builds require
private GHCR packages named `autonomous-battlefield-backend` and
`autonomous-battlefield-frontend` with inherited repository access.

## Delivery-assurance credentials

Create these narrowly scoped Jenkins credentials before enabling main delivery:

| ID | Kind | Purpose |
|---|---|---|
| `battlefield-ghcr` | username/password | GitHub Packages identity and token with only `read:packages` and `write:packages` |
| `battlefield-cosign-key` | secret file | encrypted Cosign private key |
| `battlefield-cosign-password` | secret text | password for the encrypted signing key |
| `battlefield-cosign-public-key` | secret file | reviewed public verification key |
| `battlefield-openai-api-key` | secret text | manual OpenAI evaluation only |
| `battlefield-gemini-api-key` | secret text | manual Gemini evaluation only |

Never expose signing or provider credentials to pull-request stages. Configure a
second Pipeline job using `Jenkinsfile.assurance`; schedule only its `nightly`
mode. All other modes require an operator trigger and explicit bounds. AWS
promotion additionally requires an assumed agent role or other short-lived AWS
credential source; do not store long-lived access keys in the repository.

## Required Jenkins plugins

- Pipeline.
- Git.
- GitHub Branch Source.
- GitHub Checks (which installs Checks API as a dependency).
- Credentials Binding.
- SonarQube Scanner for Jenkins.

Keep Jenkins core and plugins patched. The pipeline invokes command-line tools
that are installed on the agent rather than downloading toolchains dynamically.

Verify the Go installation under the same account that runs the agent:

```bash
go version
GOCACHE="/tmp/autonomous-battlefield-go-check-$(go env GOVERSION)" go list sync/atomic
```

The second command must print `sync/atomic`. A stale build cache from a replaced
Go installation can otherwise make standard-library packages appear missing.
Jenkins uses a build-specific cache, and the local coverage script defaults to
a version-specific cache under `/tmp`.

## Create the persistent `battlefield-local` agent

Use a separate agent so builds do not execute on the Jenkins controller:

1. Open **Manage Jenkins > Nodes > New Node**.
2. Name it `battlefield-local`, choose **Permanent Agent**, and use one executor.
3. Set the remote root directory to `/home/efilorez/jenkins-agent`.
4. Choose **Launch agent by connecting it to the controller** and enable the
   WebSocket launch option.
5. Save the node, create its work directory, and download the current agent JAR:

   ```bash
   install -d -m 700 /home/efilorez/jenkins-agent
   curl --fail --location \
     --output /home/efilorez/jenkins-agent/agent.jar \
     http://localhost:8080/jnlpJars/agent.jar
   ```

6. Copy the node's connection secret into a private local environment file. Do
   not commit, paste into documentation, or share this value:

   ```bash
   install -d -m 700 "$HOME/.config/jenkins-agent"
   umask 077
   ${EDITOR:-nano} "$HOME/.config/jenkins-agent/agent.env"
   ```

   The file contains one line:

   ```text
   JENKINS_AGENT_SECRET=replace-with-the-node-secret
   ```

7. Create `~/.config/systemd/user/jenkins-agent.service`:

   ```ini
   [Unit]
   Description=Jenkins battlefield-local agent
   Wants=network-online.target
   After=network-online.target

   [Service]
   Type=simple
   EnvironmentFile=%h/.config/jenkins-agent/agent.env
   ExecStart=/usr/bin/java -jar %h/jenkins-agent/agent.jar -url http://localhost:8080/ -secret ${JENKINS_AGENT_SECRET} -name battlefield-local -webSocket -workDir %h/jenkins-agent
   Restart=on-failure
   RestartSec=5

   [Install]
   WantedBy=default.target
   ```

8. Load and start the user service:

   ```bash
   systemctl --user daemon-reload
   systemctl --user enable --now jenkins-agent
   systemctl --user status jenkins-agent
   ```

Once these commands succeed, both `jenkins.service` and
`jenkins-agent.service` are enabled. Recreate the node only if its Jenkins
configuration or local service files are lost. If the connection secret is ever
exposed, create a replacement inbound node credential, update `agent.env` and
the service's node name if required, verify the replacement agent, and remove
the old node.

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

Both containers use Docker's `unless-stopped` restart policy. After the Docker
engine starts following a reboot, PostgreSQL and SonarQube start automatically.
When using Docker Desktop, enable its login startup setting so that the Docker
engine itself starts automatically. If the stack was stopped intentionally
with `docker compose stop`, start it once with the `up -d` command above; it will
then resume automatic startup after later Docker engine restarts.

Configure Jenkins as follows:

1. Add the token as a **Secret text** credential.
2. In **Manage Jenkins > System > SonarQube servers**, add a server named
   `battlefield-sonarqube`, URL `http://localhost:9000`, using that credential.
3. In **Manage Jenkins > Tools > SonarQube Scanner installations**, add an
   installation named `SonarScanner` and enable automatic installation.

The scanner polls SonarQube for the quality-gate result for up to five minutes
and fails the build when the gate fails. No SonarQube webhook is required. This
avoids requiring the SonarQube Docker container to connect back to the Jenkins
controller running in WSL.

SonarQube imports `backend/coverage.out` and `frontend/coverage/lcov.info` using
`sonar-project.properties`. It does not generate those reports itself. The
Community Build is analyzed only for merged `main`; PR CI still runs Go lint and
publishes both backend and frontend coverage in a GitHub `Coverage` check before
merge. Jenkins requires at least 80% Go statement coverage and 80% for each
frontend metric: statements, branches, functions, and lines. This avoids
unsupported Community Build pull-request analysis while keeping the deployment
behind the quality gate.

The SonarQube data is stored in named Docker volumes and survives restarts. Stop
it without deleting its data with:

```bash
docker compose -f quality/sonarqube/compose.yaml stop
```

Do not use `docker compose down -v` unless you intentionally want to erase the
SonarQube database and configuration.

SonarQube displays coverage but does not run the tests that generate it. Go
writes `backend/coverage.out`, Vitest writes `frontend/coverage/lcov.info`, and
SonarScanner imports both files together with static reliability, security,
maintainability, complexity, and duplication findings. The quality gate turns
the configured rules into a pass/fail deployment decision.

## Publish pull-request coverage to GitHub

The PR pipeline uses Jenkins `publishChecks` to add a separate `Coverage` check
to the pull request. Its summary contains the Go statement percentage and the
frontend statement, branch, function, and line percentages. The check fails if
Go statement coverage or any frontend metric is below 80%. **Details** opens the
Jenkins console for the complete test output. SonarQube remains the richer
report for merged `main` because Community Build does not analyze pull requests.

GitHub permits only GitHub Apps, not personal access tokens, to create Check
Runs. Configure this once:

1. In **Manage Jenkins > Plugins**, install **GitHub Checks** and restart Jenkins
   if requested. The existing Checks API plugin alone is not a GitHub publisher.
2. In GitHub, open **Settings > Developer settings > GitHub Apps > New GitHub
   App**. Use a unique name such as `Jenkins Autonomous Battlefield`.
3. Grant these repository permissions:
   - Checks: **Read and write**.
   - Commit statuses: **Read and write**.
   - Contents: **Read-only**.
   - Metadata: **Read-only**.
   - Pull requests: **Read-only**.
4. Jenkins is currently local, so disable the app webhook. Periodic multibranch
   scanning remains the trigger. Add an HTTPS Jenkins webhook URL later if the
   controller becomes reachable from GitHub.
5. Install the app only on `twh09/autonomous_battlefield`, then generate and
   download a private key from the app settings.
6. Convert the key to the PKCS#8 form expected by Jenkins:

   ```bash
   openssl pkcs8 -topk8 -inform PEM -outform PEM \
     -in downloaded-github-app-key.pem \
     -out jenkins-github-app-key.pem -nocrypt
   ```

7. In **Manage Jenkins > Credentials**, add a global credential whose kind is
   **GitHub App**. Enter the app ID and paste the converted private key. Set its
   repository owner to `twh09` if that advanced field is shown.
8. Open the `autonomous-battlefield` Multibranch Pipeline configuration. In its
   GitHub branch source, replace the current scan/checkout credential with this
   GitHub App credential, validate it, save, and run **Scan Repository Now**.

Open or rebuild a pull request after configuration. The PR **Checks** area
should then contain both the normal Jenkins build result and `Coverage`. Do not
make `Coverage` a required `main` branch check until one build has published it
successfully; afterward it can be selected in the GitHub branch ruleset.

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

The scan interval can be changed later without recreating the Multibranch job:
open `autonomous-battlefield`, select **Configure**, change **Scan Multibranch
Pipeline Triggers**, and save. A shorter interval discovers pull requests more
quickly but makes more GitHub API calls.

## Configure GitHub `main` protection

GitHub Free does not enforce rulesets or protected branches on a private
organization repository. The Jenkins checks still report failures, but blocking
the merge requires GitHub Team, a public repository, or another plan that
supports protection for private repositories.

In the repository, open **Settings > Rules > Rulesets** and create or update a
ruleset targeting `main`:

1. Require a pull request before merging and block direct pushes.
2. Require the Jenkins PR result
   `continuous-integration/jenkins/pr-merge`.
3. After one successful publication, also require the separate `Coverage`
   check if coverage reporting should block merges.
4. Require the branch to be current before merging when that fits the selected
   merge strategy.

Do not require the merged-`main` CD result as a pre-merge check: that job exists
only after GitHub creates the final `main` commit. Direct pushes to `dev` remain
available for local iteration, but Jenkins CI begins when a pull request targets
`main`.

After merging a pull request, synchronize the long-lived local branches before
starting the next change:

```bash
git switch main
git pull --ff-only origin main
git switch dev
git merge main
git push origin dev
```

This keeps the next `dev`-to-`main` pull request focused on new work. Use a
short-lived feature branch instead when several changes need independent
review; the pipeline behavior is the same for any pull request targeting
`main`.

## Retain pull-request build history

Each Multibranch child job has independent numbering. `PR-3` therefore begins
at build `#1` even if `PR-2` had several builds. The `Jenkinsfile` keeps the last
20 builds for each active branch or pull-request job:

```groovy
buildDiscarder(logRotator(numToKeepStr: '20'))
```

Closed pull requests become orphaned child jobs. To keep their history, open
the Multibranch project configuration and set **Orphaned Item Strategy** to a
bounded retention such as 30 days and 20 items. Without orphan retention,
Jenkins may remove the entire `PR-N` job at a later repository scan. GitHub
keeps the original check result, but its Jenkins details link will no longer
work after the underlying job is deleted.

## Daily startup after a reboot

The Jenkins configuration is persistent, and SonarQube uses Docker restart
policies. The normal daily sequence is therefore to verify the existing
services, not repeat the one-time setup:

```bash
# Configure Docker Desktop to start at login and wait until this succeeds.
docker info >/dev/null

systemctl is-active --quiet jenkins
systemctl --user is-active --quiet jenkins-agent

until curl --silent --fail http://localhost:9000/api/system/status | \
  grep --quiet '"status":"UP"'; do sleep 2; done

kind get clusters
kubectl cluster-info --context kind-battlefield
kubectl get pods --namespace battlefield
./scripts/cd/smoke-test-kind.sh
```

If a service was stopped intentionally, start it again with
`sudo systemctl start jenkins`, `systemctl --user start jenkins-agent`, or
`docker compose -f quality/sonarqube/compose.yaml up -d` as appropriate.

If `kind get clusters` does not include `battlefield`, recreate it and redeploy
the committed revision:

```bash
./scripts/cd/create-kind-cluster.sh
./scripts/ci/build-images.sh
./scripts/cd/deploy-kind.sh
./scripts/cd/smoke-test-kind.sh
```

If the cluster predates local Gateway API access, recreate it with
`./scripts/cd/create-kind-cluster.sh --recreate`. This explicitly deletes the
cluster and its local PostgreSQL data before publishing the required node port.

The build and deployment commands require a clean Git worktree. The local
secret file or Jenkins `battlefield-local-secret-env` credential must still
exist. Starting SonarQube with `docker compose up -d` reuses its named volumes;
do not add `-v` when stopping it unless the data is intentionally disposable.

After the checks pass, open `http://localhost:8080` for Jenkins,
`http://localhost:9000` for SonarQube,
`http://battlefield.localhost:8888` for the deployed application, and
`http://grafana.localhost:8888` for Grafana, or
`http://kibana.localhost:8888` for Kibana. The three `.localhost` routes use the
Kind node port and do not require a persistent `kubectl port-forward` process.

## Troubleshooting setup and pipeline waits

| Symptom | Meaning and check |
| --- | --- |
| `No tool named SonarScanner found` | Install **SonarQube Scanner for Jenkins**, restart Jenkins if requested, then add a tool named exactly `SonarScanner` under **Manage Jenkins > Tools**. |
| SonarQube analysis times out waiting for the quality gate | Confirm SonarQube is healthy with `curl --fail http://localhost:9000/api/system/status`, then inspect **Administration > Projects > Background Tasks** in SonarQube. The scanner polls the server for up to five minutes and does not require a webhook. |
| `battlefield-local` is offline | Run `systemctl --user status jenkins-agent`, confirm the controller is active, and restart the user service after correcting the reported error. |
| A new PR shows only build `#1` | Build numbering is local to that `PR-N` child job; this is not evidence that active history was deleted. Check the Orphaned Item Strategy for closed PR retention. |
| A direct `dev` push has no stages | This is intentional. Run locally and open a pull request into `main` to trigger CI. |
| Frontend proxy returns `502` | Confirm `API_UPSTREAM` renders as `battlefield-api.battlefield.svc.cluster.local:8080`, then inspect frontend logs and the `battlefield-api` Service endpoints. |
| `kind load docker-image` says the image is not present and is loading | This is normal informational output: kind is copying a host Docker image into the cluster node. |

## Pipeline behavior

For a pull request targeting `main`, the pipeline performs:

1. Clean checkout of the exact GitHub commit.
2. Parallel Go formatting/vet, Go tests with a coverage profile, frontend tests
   with LCOV and JSON-summary coverage, frontend build, and Kustomize rendering.
3. A dedicated GitHub `Coverage` check containing both coverage summaries.
4. Image build through `scripts/ci/build-images.sh`. Jenkins `BUILD_NUMBER`
   produces the human-readable build alias; the full SHA remains immutable.
5. Image scanning through `scripts/ci/scan-images.sh`. Trivy v0.74.0 is pinned by
   image digest and rejects fixable HIGH or CRITICAL findings.

After that pull request is merged, the `main` pipeline performs:

1. Clean checkout of the exact merged commit.
2. Regenerate coverage and wait for the SonarQube quality gate.
3. Compare the merge SHA with `GIT_PREVIOUS_SUCCESSFUL_COMMIT` and classify
   changed deployment components. If the baseline is unavailable, deploy all
   components conservatively. Unclassified paths also deploy all components and
   emit a warning until an explicit classifier rule is added.
4. Build and scan only changed backend and frontend images. A PR validation SHA
   and the final merge SHA are not assumed to be interchangeable.
5. Apply only the selected infrastructure, migration, application,
   observability, and Gateway API stages. Documentation-only changes deploy
   nothing.
6. After a deployment, verify frontend, Grafana, and Kibana gateway access, API
   readiness and metrics, Prometheus application, kube-state-metrics, and
   cAdvisor target discovery, both Grafana dashboards, the Kibana data view,
   and Elasticsearch log ingestion.

`scripts/cd/deploy-kind.sh` can reapply a temporary Kustomize overlay containing
the previous images if an application apply or rollout fails. Before deploying,
Jenkins records the independently deployed backend and frontend SHAs. If an
application-related deployment later fails smoke testing,
`scripts/cd/rollback-kind.sh` restores those two exact immutable revisions without
re-running migrations or changing observability. Image rollback is skipped for
infrastructure-, migration-, observability-, or gateway-only changes. Database
migrations are deliberately not reversed automatically, so migrations must
remain backward-compatible with the previous application revision.

The local pipeline loads changed images directly into kind. The later AWS
version will push changed tags to ECR and deploy their SHA tags to EKS while
preserving the same test, scan, migration, verification, and rollback gates.

## Run the pipeline commands without Jenkins

After committing the intended source so the worktree is clean:

```bash
./scripts/ci/lint-go.sh
./scripts/ci/test-go-coverage.sh
(cd frontend && npm ci && npm test -- --coverage && npm run build)
BUILD_NUMBER=1 ./scripts/ci/build-images.sh
./scripts/ci/scan-images.sh "$(git rev-parse HEAD)"
ROLLBACK_ON_FAILURE=1 ./scripts/cd/deploy-kind.sh "$(git rev-parse HEAD)"
./scripts/cd/smoke-test-kind.sh
```

Set `DEPLOY_COMPONENTS=frontend`, `backend`, `observability`, or another
supported comma-separated selection on the build, scan, and deploy commands to
exercise an incremental path. Run
`./scripts/ci/test-deployment-components.sh` to verify the path classifier.

These commands exercise the same local path, but they do not validate Jenkins
Declarative Pipeline syntax. Use the configured Jenkins Multibranch job for the
final `Jenkinsfile` validation.
