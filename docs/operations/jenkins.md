# Jenkins and GitHub deployment pipeline

Production now runs on the server at `192.168.1.104` (Jenkins port `8080`).
For initial setup or migration of an existing server, complete the
[server registration](environments.md#server-registration-and-migration).
The following installation commands are server administration instructions.

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

The diagram summarizes the branch split; [pipeline behavior](#pipeline-behavior)
lists all current gates, publication, and verification stages.

The Declarative Pipeline groups these paths under `PR verification` and
`Main delivery`. Jenkins still displays the inactive parent stage as skipped so
the branch decision remains auditable, but it does not execute that parent's
nested stages. Within an active `Main delivery` run, image and deployment stages
can still appear as skipped when the changed-component classifier determines
that they are unnecessary; those skips are intentional and remain visible.

PRs targeting `dev` or `main`, main delivery and assurance use the existing
`battlefield-production` agent. Direct pushes to non-`main` branches skip delivery.
The owner accepts a shared host and Docker daemon for this small, trusted project;
no separate CI VM or new agent label is required. Keep the assurance job pinned to
trusted main. Delivery stays main-only and production credentials are bound only
in the stages that need them, but these controls do not isolate PR code from the
shared host. See the [shared-agent tradeoff](#production-hardening-rollout).

The `dev` and `main` branches must require pull requests, the Jenkins and
Coverage checks, and disallow direct pushes. Jenkins sees a `main` update rather
than a distinct "PR merged" pipeline condition, so the branch rule is what makes
a `main` update equivalent to an approved merge.

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
- Node.js 24, npm, and Python 3 for deployment regression tests.
- GCC and the C development toolchain required by `go test -race`.
- Docker, with permission to use the Docker daemon.
- `kubectl`, `kind`, `curl`, and AWS CLI for the optional promotion job.
- Access to the `battlefield` kind cluster through the dedicated `battlefield-production` context and registered production profile.

The agent needs Docker access to build images and run the pinned Trivy scanner.
It also needs enough free space and inode quota in its Jenkins workspace for
parallel Go compilation. The pipelines place `GOCACHE`, `GOTMPDIR`, and general
temporary files in Jenkins's workspace-associated temporary directory, share
that Go cache across parallel stages, and remove the project subdirectory
before archiving build evidence.
The publishing script creates or reuses a dedicated `docker-container` Buildx
builder named `battlefield-jenkins`; this driver preserves BuildKit SBOM and
provenance attestations when images are pushed even if the daemon uses the
classic local image store. Provenance is explicitly emitted as SLSA v0.2 so
the extracted predicate is compatible with Cosign's `slsaprovenance` schema.
Docker daemon access is effectively privileged host access. Do not run untrusted
fork pull requests on this agent, and do not use the Jenkins controller itself
as the long-term build agent.

Security scanners, Kubeconform, Conftest, Syft, Cosign, ORAS, k6, and ZAP run
from versions pinned in `scripts/ci/tool-versions.env`. This is also the default
Trivy pin for local image scans; `TRIVY_IMAGE` can override that local command.
The source-security stage owns the shared manifest rendering, schema checks,
and policy checks on both PR and main builds. The deployment-script checks
exercise staging, component selection, scanner launchers, and compatibility
wrappers separately. `SECURITY_ARTIFACT_DIR` can relocate security reports,
including the rendered manifest consumed by Trivy and Conftest.
Main builds require
private GHCR packages named `autonomous-battlefield-backend` and
`autonomous-battlefield-frontend` with inherited repository access.
Cosign publishes each signature and signed attestation in its v3 OCI 1.1
referrer format and as a legacy digest-tag compatibility mirror. The OCI
referrers remain the portable promotion evidence; the mirror exists because
the pinned Kyverno 1.15 admission verifier discovers `.sig` and `.att` tags,
not Cosign v3's referrer-only evidence. Both forms are signed with the same
Jenkins-managed key and cover the same immutable subject digest.
[`scripts/ci/with-ghcr-auth.sh`](../../scripts/ci/with-ghcr-auth.sh) supplies a
mode-700 temporary `DOCKER_CONFIG` for publishing, signing, deployment, rollback,
and AWS promotion. It logs in through standard input, forwards command arguments
and exit status, and removes credentials on success, failure, or cancellation.
[`test-registry-auth.sh`](../../scripts/test/test-registry-auth.sh) checks this
lifecycle with a stub Docker command; it never contacts a registry. Docker may still warn that the temporary
configuration is not encrypted, but GHCR credentials are not stored in the
agent user's persistent `~/.docker/config.json`. `BUILDX_CONFIG` remains on the
agent so the named Buildx builder can be reused without persisting registry
authentication.

Image repository defaults are shared by publication and policy rendering in
`scripts/lib/image-metadata.sh`. `REGISTRY_NAMESPACE` changes the namespace;
`PUBLISHED_BACKEND_IMAGE` and `PUBLISHED_FRONTEND_IMAGE` override individual full
repository names. Names must omit tags, digests, and wildcards. Registry credentials
must still grant access to the chosen repositories; the supplied login wrapper
continues to authenticate GHCR.

Both local and published image source labels come from Git `origin`, normalizing
HTTPS and common Git SSH URLs. Set `SOURCE_URL=https://github.com/your-org/your-repo`
to override this label. Credential-bearing or unsupported URLs are rejected without
printing their contents. Publishing requires a source URL; local builds without
an origin omit the label.

`test-image-metadata.sh` exercises the real build/publish and policy-rendering
scripts with stub Docker/Kubernetes commands, including repository overrides,
preserved images, source metadata, and invalid-input rejection. Both PR and main
run this check.

The source-security stage first requests a full npm audit from a digest-pinned
Node container. If the npm advisory endpoint remains unavailable after its
bounded retry, the stage requires a passing Trivy lockfile scan covering all
frontend dependencies, including development and unfixed vulnerabilities. A
definitive npm vulnerability report is never replaced by the fallback. The
archived `npm-audit-mode.json` records which path supplied the build evidence.
npm debug logs from the disposable audit containers are archived under
`artifacts/security/npm-logs/`; paths printed by npm refer to the container and
are not paths on the Jenkins agent.

The Trivy source scan skips generated `artifacts`, `frontend/coverage`,
`frontend/playwright-report`, and `frontend/test-results`
directories. Vitest creates and removes temporary coverage files during the
parallel frontend stage; scanning those files can otherwise fail with a
`stat frontend/coverage/.tmp/coverage-*.json: no such file or directory` error.
Frontend source and dependency lockfiles remain included in the scan.

Frontend Vitest runs are capped at two workers with a 15-second per-test timeout
in `frontend/vite.config.ts`. This gives DOM interaction tests headroom while
other CI branches share the agent; all assertions and 80% coverage gates apply.
Testing Library's independent async-query deadline is five seconds, configured
in `frontend/src/test/setup.ts`, so chained fetch/render effects can settle under
the same shared-agent load.

Both frontend validation stages install the lockfile-matched Chromium browser
with `npx playwright install chromium` and run `npm run test:e2e` after coverage
and build. Provision Linux browser libraries on the agent once using
`npx playwright install-deps chromium`; CI does not attempt privileged package
installation. The suite starts a local Vite server on port 4173, intercepts all
API routes with deterministic fixtures, and covers desktop and mobile Chromium.
It uses one worker in CI. No shared database or live AI is required. The always
post step archives `frontend/playwright-report/**/*` and
`frontend/test-results/**/*`, including failure screenshots and retained traces.

Development k6 and ZAP containers use host networking on the Linux agent and map
`battlefield.localhost` to `127.0.0.1`, matching kind's loopback-only gateway
publication. The Docker daemon must run on the same host as the agent. Both
containers run with the invoking user's UID/GID so their bind-mounted reports
are writable and remain owned by Jenkins. ZAP uses container-local `/tmp` for
its Python/Java home and working directory. Target overrides (`TARGET_URL` and
`ZAP_TARGET_URL`) are still supported. A refused connection to `172.17.0.1:8888`
indicates an outdated launcher using the Docker bridge gateway.
Run `./scripts/test/test-runtime-assurance.sh` to check launcher networking,
report ownership settings, target overrides, and failure propagation without
starting scanners or contacting a deployment. Production k6 contacts the registered
HTTPS origin; production ZAP runs offline with no network using authenticated
response captures, as described in the hardening rollout below.

## Delivery-assurance credentials

Create these narrowly scoped Jenkins credentials before enabling main delivery:

| ID | Kind | Purpose |
|---|---|---|
| `battlefield-production-kubeconfig` | secret file | Dedicated production kubeconfig; bound only during rollout (or explicit assurance load) |
| `battlefield-production-access-service-token` | secret file | Restricted CI service identity; bound only for production smoke, load and authenticated response capture |
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
go list sync/atomic
```

The second command must print `sync/atomic`. A stale build cache from a replaced
Go installation can otherwise make standard-library packages appear missing.
Jenkins uses a disposable workspace-scoped cache; the local coverage script
continues to use the caller's `GOCACHE` or a version-specific cache under
`TMPDIR`.

If a build reports `disk quota exceeded`, inspect capacity, inode usage, the
user quota, and old project caches on the agent before retrying:

```bash
df -h /tmp "$HOME/jenkins-agent"
df -i /tmp "$HOME/jenkins-agent"
quota -s 2>/dev/null || true
du -sh /tmp/autonomous-battlefield-go-cache-* /tmp/go-build* 2>/dev/null || true
find /tmp -maxdepth 1 -user "$(id -un)" -type d \
  \( -name 'autonomous-battlefield-go-cache-*' -o -name 'go-build*' \) -print
```

After all Jenkins builds have stopped, remove only the directories printed by
that final command. They are compiler caches and interrupted Go work trees, not
build evidence:

```bash
find /tmp -maxdepth 1 -user "$(id -un)" -type d \
  \( -name 'autonomous-battlefield-go-cache-*' -o -name 'go-build*' \) \
  -exec rm -rf -- {} +
```

New pipeline runs no longer create those project caches in `/tmp`.

## Create the persistent `battlefield-local` agent

Use a separate agent so builds do not execute on the Jenkins controller:

1. Open **Manage Jenkins > Nodes > New Node**.
2. Name it `battlefield-local`, choose **Permanent Agent**, and use one executor.
3. Add the label `battlefield-production`, shared by PR verification, main
   delivery and assurance for this owner-operated deployment.
   Set the remote root directory to the agent account’s `~/jenkins-agent`.
4. Choose **Launch agent by connecting it to the controller** and enable the
   WebSocket launch option.
5. Save the node, create its work directory, and download the current agent JAR:

   ```bash
   install -d -m 700 $HOME/jenkins-agent
   curl --fail --location \
     --output $HOME/jenkins-agent/agent.jar \
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
controller on the server. `scripts/ci/run-sonarqube-gate.sh` removes Jenkins
multibranch metadata before invoking the scanner and can override the project
identity used by `sonar-project.properties`.

SonarQube imports `backend/coverage.out` and `frontend/coverage/lcov.info` using
`sonar-project.properties`. It does not generate those reports itself. Because
Community Build cannot analyze the repository's `dev` branch, each GitHub PR is
an isolated SonarQube project named `autonomous-battlefield-pr-<number>`. Its
checked-out code is that preview project's main branch, and all retries use the
stable `pr-<number>` project version. This preserves the inherited **Previous
version** New Code baseline within one PR and prevents another PR from changing
it. The default **Sonar way** gate applies to preview projects. The production
`autonomous-battlefield` project remains dedicated to merged `main`, which is
scanned again before deployment. Jenkins requires at least 80% Go statement
coverage and 80% for each frontend metric: statements, branches, functions, and
lines. Delete preview projects for closed PRs periodically from the SonarQube
Projects page; they contain analysis history, not runtime data.

The SonarQube data is stored in named Docker volumes and survives restarts. Stop
it without deleting its data with:

```bash
docker compose -f quality/sonarqube/compose.yaml stop
```

Do not use `docker compose down -v` unless you intentionally want to erase the
SonarQube database and configuration.

## Publish pull-request coverage to GitHub

The PR pipeline uses Jenkins `publishChecks` to add a separate `Coverage` check
to the pull request. Its summary contains the Go statement percentage and the
frontend statement, branch, function, and line percentages. The check fails if
Go statement coverage or any frontend metric is below 80%. **Details** opens the
Jenkins console for the complete test output. The Jenkins build also runs the
PR's SonarQube preview-project analysis and fails when its quality gate is red.

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

## Production application configuration

The production agent reads `~/.config/autonomous-battlefield/production/.env`
for database, authentication, provider and integration settings. Its adjacent
`deployment.env` identifies the cluster. Keep both private; the application file
is parsed as literal data. Jenkins no longer binds a separate database credential.
It retains registry, signing, production Access-token and kubeconfig credentials.
Use the main pipeline's `APPLY_CONFIGURATION` parameter after editing only `.env`.

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
on a private server, trigger **Scan Repository Now** or configure periodic
repository scans. Add a webhook only after Jenkins has an authenticated,
HTTPS-protected address reachable from GitHub.

The scan interval can be changed later without recreating the Multibranch job:
open `autonomous-battlefield`, select **Configure**, change **Scan Multibranch
Pipeline Triggers**, and save. A shorter interval discovers pull requests more
quickly but makes more GitHub API calls.

## Configure GitHub integration-branch protection

Ruleset enforcement depends on the repository visibility and GitHub plan. The
current repository uses active rulesets; if its visibility or plan changes,
confirm that GitHub still reports the rulesets as active rather than treating
the Jenkins checks as advisory.

In the repository, open **Settings > Rules > Rulesets** and create or update
rulesets targeting `dev` and `main`:

1. Require a pull request before merging and block direct pushes.
2. Require the Jenkins PR result named `Jenkins`. It includes the SonarQube
   quality gate, so a red gate blocks the merge.
3. Require the separate `Coverage` check.
4. Require the branch to be current before merging when that fits the selected
   merge strategy.

Do not require the merged-`main` CD result as a pre-merge check: that job exists
only after GitHub creates the final `main` commit. Jenkins CI begins when a pull
request targets `dev` or `main`.

AI-created feature and fix branches start from the latest fetched `origin/dev`
and open pull requests into `dev`, unless the user explicitly requests otherwise.
See [agent branch guidance](../../AGENTS.md#branches-and-pull-requests). Jenkins
verifies PRs targeting both branches on the shared agent. Run applicable local
checks before opening them. A `dev`-to-`main` promotion is a separate release PR.

After merging a release pull request into `main`, synchronize the long-lived local branches before
starting the next change:

```bash
git switch main
git pull --ff-only origin main
git switch dev
git merge main
git push origin dev
```

This keeps the next `dev`-to-`main` pull request focused on new work. Short-lived
AI work branches use `dev` as their base and PR target; the release PR into
`main` receives the configured Jenkins PR checks.

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

Jenkins, SonarQube, and the production Kind cluster run on the server. Verify
those existing services and run `DEPLOY_ENV=production ./scripts/cd/smoke-test-kind.sh`
as the configured agent account. Do not run the dev bootstrap on this machine.
If the production cluster is missing, recover its data and registered identity;
do not recreate it as a daily startup step.

Use the [server transition and recovery guide](environments.md) for registration,
the `battlefield-production` agent label, dedicated kubeconfig credential, and
pre-rollout database export. WSL requires only Docker and its dev cluster for
local iteration; local Jenkins and SonarQube need not be started.

## Troubleshooting setup and pipeline waits

| Symptom | Meaning and check |
| --- | --- |
| `No tool named SonarScanner found` | Install **SonarQube Scanner for Jenkins**, restart Jenkins if requested, then add a tool named exactly `SonarScanner` under **Manage Jenkins > Tools**. |
| SonarQube analysis times out waiting for the quality gate | Confirm SonarQube is healthy with `curl --fail http://localhost:9000/api/system/status`, then inspect **Administration > Projects > Background Tasks** in SonarQube. The scanner polls the server for up to five minutes and does not require a webhook. |
| `battlefield-local` is offline | Run `systemctl --user status jenkins-agent`, confirm the controller is active, and restart the user service after correcting the reported error. |
| A new PR shows only build `#1` | Build numbering is local to that `PR-N` child job; this is not evidence that active history was deleted. Check the Orphaned Item Strategy for closed PR retention. |
| A feature-branch push has no stages | This is intentional. Open a pull request into `dev` to trigger CI. Promotion to `main` is a separate release. |
| Frontend proxy returns `502` | Confirm `API_UPSTREAM` renders as `battlefield-api.battlefield.svc.cluster.local:8080`, then inspect frontend logs and the `battlefield-api` Service endpoints. |
| `kind load docker-image` says the image is not present and is loading | This is normal informational output: kind is copying a host Docker image into the cluster node. |

## Pipeline behavior

For a pull request targeting `dev` or `main`, the pipeline performs:

1. Clean checkout of the exact GitHub commit.
2. Parallel Go lint, isolated PostgreSQL-backed tests (coverage and race checks),
   frontend coverage/build, source security and policy validation, fuzz smoke,
   deterministic AI evaluation, and deployment-script checks.
3. Publication of the GitHub `Coverage` check.
4. SonarQube preview-project analysis and quality-gate enforcement for the exact
   PR checkout.
5. Local image builds and vulnerability scans, without publication or production credentials.

Backend integration packages run concurrently against the disposable PostgreSQL
database. Migration fixtures use the same advisory lock as startup, including
tests that upgrade or downgrade selected versions. Separate schemas alone do not
isolate shared role/grant changes. A failed backend test stops the coverage run;
the published `Unavailable` backend coverage result is not evidence of a low
coverage percentage. Inspect the failing test before changing any coverage gate.

For `main`, the pipeline performs:

1. Clean checkout, Go lint, backend/frontend coverage, source security, and deterministic AI evaluation.
2. SonarQube analysis and quality-gate enforcement.
3. Deployment-script checks and component classification against the previous
   successful commit. Documentation-only, application-test-only, and known CI-test-wrapper
   changes select no deployment; an unknown path or unavailable baseline selects
   all components. CI quality gates still run for these changes.
4. Publication of changed images to GHCR, scanning of the published images,
   then attestation, signing, and verification.
5. Production rollout with the dedicated kubeconfig, recorded previous backend
   and frontend digest references, migration gating, and Kyverno admission verification.
6. Application and observability smoke verification, followed by parallel k6
   smoke load and ZAP baseline checks.
7. An atomic update of the private, cluster-bound recovery image record after
   all rollout and runtime checks pass.

See [deployment component mapping](deployment.md#change-to-deployment-mapping)
for the paths that select each rollout stage and
[supply-chain assets](../../deploy/supply-chain/README.md) for the trust root.

The deployment keeps the normal application rollout budget at five minutes but
allows 15 minutes for the observability group as a whole. A new single-node
Kind cluster may queue the large Elastic, Grafana, Prometheus, OpenTelemetry,
and Tempo pulls; the group deadline prevents the first named Deployment from
failing while its image is still waiting behind those pulls. On failure,
Jenkins prints descriptions and logs for every unavailable observability
Deployment plus recent namespace events.

`scripts/cd/deploy-kind.sh` can reapply a temporary Kustomize overlay containing
the previous images if an application apply or rollout fails. Before deploying,
Jenkins records the independently deployed backend and frontend digest references. If an
application-related deployment later fails smoke testing,
`scripts/cd/rollback-kind.sh` restores those two exact immutable revisions only
when the private cluster-bound recovery record approves the exact image pair.
The record is written after all runtime assurance stages succeed; an absent or
mismatched record requires operator recovery. The script operates without
re-running migrations or changing observability. Image rollback is skipped for
infrastructure-, migration-, observability-, or gateway-only changes. Database
migrations are deliberately not reversed automatically, so migrations must
remain backward-compatible with the previous application revision.

Local dev loads images into Kind. Production pulls verified GHCR digests. The
separate AWS promotion helper copies those verified subjects and their evidence
to ECR; its assurance-job artifact-input gap remains documented in the
[deployment review](deployment-review.md).

## Run local build and deployment checks without Jenkins

After [dev environment setup](environments.md#wsl-setup-and-normal-use) and
committing the intended source so the worktree is clean:

```bash
./scripts/ci/lint-go.sh
./scripts/test/with-test-postgres.sh ./scripts/test/test-go-coverage.sh
(cd frontend && npm ci && npm test -- --coverage && npm run build)
BUILD_NUMBER=1 ./scripts/ci/build-images.sh
./scripts/ci/scan-images.sh "$(git rev-parse HEAD)"
ROLLBACK_ON_FAILURE=1 ./scripts/cd/deploy-kind.sh "$(git rev-parse HEAD)"
./scripts/cd/smoke-test-kind.sh
```

Set `DEPLOY_COMPONENTS=frontend`, `backend`, `observability`, or another
supported comma-separated selection on the build, scan, and deploy commands to
exercise an incremental path. Run
`./scripts/test/test-deployment-components.sh` to verify the path classifier.

These commands exercise local build/deploy checks after dev environment setup;
they do not publish, sign, or verify production registry artifacts or validate Jenkins
Declarative Pipeline syntax. Use the configured Jenkins Multibranch job for the
final `Jenkinsfile` validation.

The source-security stage logs each failed check and its report path. Inspect the
matching file under the build's archived `artifacts/security/` directory to
separate dependency findings from rendered-manifest or workload-policy failures.

### Trivy database download recovery

The source-security launcher prepares the vulnerability database before scanning,
using the persistent Docker volume `autonomous-battlefield-trivy-cache` (override
with `TRIVY_CACHE_VOLUME`). It tries `mirror.gcr.io/aquasec/trivy-db:2`, then
`ghcr.io/aquasecurity/trivy-db:2` if preparation fails, with a three-minute timeout
per attempt and progress bars disabled. Only after successful preparation does the
filesystem scan use `--skip-db-update`; cached data alone does not bypass failed
preparation. Both preparation failures fail the security gate. Scan findings are
not retried or ignored. Kubernetes policy checks still run and publish reports.

A `read: connection reset by peer` during preparation indicates a download failure,
not a vulnerability finding. If both registries fail, inspect agent/container
connectivity to those registries and rerun after recovery. The launcher tests stub
networked scanners to verify registry fallback and failure propagation; they do
not establish that the remote registries are reachable.

### k6 HTTPS trust

The k6 launcher mounts the Linux agent's `/etc/ssl/certs/ca-certificates.crt`
read-only into the container and sets `SSL_CERT_FILE` to that mount. This makes
the agent's installed private CAs available to k6 while retaining TLS verification.
`K6_CA_BUNDLE` can select another readable, nonempty PEM CA bundle; missing or
empty bundles fail before starting k6. The Docker daemon must share the agent's
filesystem, as required for the existing source and report mounts. Browser trust
settings do not configure k6. Public Cloudflare endpoints use public trust roots;
install an additional private CA only when an intended target actually requires it.

Production k6 reads the authentication mode and enrolled CI client ID from the
deployed `battlefield-auth` Secret. It validates the private Jenkins service-token
JSON and mounts it read-only; secret values are not Docker arguments. Production
unsets admin-password inputs and refuses redirects. Username/password and cookie
login remain dev-only behavior.

## Production smoke credentials

Provision the `battlefield-production-access-service-token` Secret file and the
production `BATTLEFIELD_ACCESS_CI_CLIENT_ID` before delivering the CI service-auth
change. See [activation and rotation](limited-launch.md#ci-service-authentication)
for the JSON schema, Cloudflare Service Auth policy, expiry alerts and first rollout
with `APPLY_CONFIGURATION=true`. Jenkins validates local credential metadata before
rollout, then tests actual read-only service access through the public hostname.
It no longer needs a copied browser session cookie.

## Production hardening rollout

Both Jenkinsfiles use the existing `battlefield-production` agent. The owner has
chosen to share CI and production infrastructure for this small project with
trusted build authors. Keep the existing agent online with its current label and
CI tools; a separate VM and `battlefield-ci` label are not rollout prerequisites.

This is an accepted tradeoff: PR scripts and dependency installers execute with
access to the shared host and Docker daemon, so mistakes or malicious code can
affect production. Stage-scoped credentials and main-only delivery still apply,
but a PR-editable Jenkinsfile and inherited Multibranch credentials are not a
security boundary. Limit automatic PR builds to trusted repository authors and
keep builds on the agent rather than the controller's built-in node.

Revisit isolation before accepting untrusted contributors or builds. A future
setup can use a separate CI VM/daemon, trusted delivery credential scope, and
controller-enforced node access. A second label or executor on this host would
not provide that isolation.

If an older build is already waiting for the retired `battlefield-ci` label,
abort that build and run the updated PR revision. Running builds retain the
Jenkinsfile loaded at their start, and the job disables concurrent builds.

All deployment regression suites run through `./scripts/test/test-deployment.sh`
on both PR and main builds. This includes tunnel preparation, policy boundaries,
recovery approval, authenticated capture and per-replica scrape checks. Source
security additionally renders synthetic tunnel policies and production TLS config.
The regression checks use fixtures and require no production secrets.

Production passive ZAP scanning captures authenticated GET responses for
`/v1/auth/session`, `/v1/scenarios`, and `/livez`, verifies the CI identity and
catalog response, and removes credential/cookie headers before HAR export. The
scanner receives no credentials and runs with `--network none`. It imports recorded
responses without resending requests, fails on high-risk findings, and writes
`artifacts/security/zap/coverage.json` explicitly identifying this limited coverage.
It does not certify authenticated admin/player routes or the whole frontend.
Broader crawling and role-specific security tests belong in staging. See
[ZAP HAR import](https://www.zaproxy.org/docs/desktop/addons/import-export/automation/).

Configure kubelet TLS trust using the [production monitoring setup](observability.md#production-kubelet-tls).
Keep the first hardening rollout separate from unrelated application releases:
there is no automatic recovery approval until a successful new pipeline records it.
Mutable deployment selection and recovery values are initialized in Checkout,
not in immutable Declarative environment bindings, so subsequent shell steps use
the selected components and approved digests.
