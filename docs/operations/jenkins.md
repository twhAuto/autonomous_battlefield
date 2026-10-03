# Jenkins operations

For contributor checks and local reproduction, see [continuous integration](../developer/ci.md).
This guide covers controller and agent administration, credentials, delivery and recovery.

The production Jenkins controller runs on the server at `192.168.1.126` (Jenkins port `8080`).
For initial setup or migration of an existing server, complete the
[host registration](server-bootstrap.md).
For repeatable installation or adoption, use the [Ansible CI bootstrap](ci-bootstrap.md).
This guide owns administration and diagnostics; the bootstrap guide owns
installation, adoption, pinned versions and service restoration.

The deployment regression gate includes real Ansible input tests. Agents need
Python 3.12–3.14 and access to PyPI on the first run of each pinned runtime/Python
minor version. The entry point installs and caches checksum-verified dependencies;
`./scripts/infra/server.sh tools` can prewarm that cache during agent provisioning.
Keep the agent cache persistent. An unavailable uncached runtime fails the gate;
the pipeline does not silently skip these checks when downloads fail. Subsequent
runs with the completed cache do not contact PyPI for this runtime.

For the repeatable Ubuntu host foundation and credential import, start with the
[server bootstrap](server-bootstrap.md). It can run from WSL before Jenkins exists.
The foundation playbook preserves CI services. The separate
[CI bootstrap](ci-bootstrap.md) recreates or adopts the controller, agent and SonarQube
from an external operator session; merging application code does not run it.

## Reviewed host operations

The separate [platform host job](platform-jobs.md) uses `infra/jenkins/Jenkinsfile.platform`
from trusted `main` for manual plan/apply/verify. Its installer registers inputs,
binds folder credentials and creates the job; it never starts a build. Normal
`main` delivery remains application-only. Install the required Lockable Resources
plugin before adopting the updated application Jenkinsfile: production rollout
and host operations share `battlefield-production-platform` for their full Jenkins
operation. See the guide for one-time setup, approval, credential rotation and the
limits of locking when using external operator commands. Use the separate [external CI maintenance path](ci-bootstrap.md) for controller/agent
and SonarQube reconstruction.

## Install or restore the CI services

Use the external [CI bootstrap workflow](ci-bootstrap.md) from a registered
operator session. For an existing server, follow its adoption prerequisites; for
a clean replacement host, use its clean-install inputs. The workflow installs
pinned Java/Jenkins, plugins, tools, the agent and Sonar services. Never run
controller maintenance from a Jenkins job or improvise installation over retained
controller state. Restore the complete matching Jenkins home, including its keys
and credential stores, plus matching Sonar data before adopting a restored host.

Controller state persists under `/var/lib/jenkins`; the native service starts at
boot. Routine diagnostics are:

```bash
sudo systemctl status jenkins
sudo journalctl -u jenkins --since today
```

Use the [reviewed maintenance and recovery procedure](ci-bootstrap.md#maintenance-backup-and-failure-recovery)
for changes that can stop the controller. Package/plugin upgrades need a reviewed
pin and compatibility change; ordinary adoption preserves existing identities.

## Jenkins agent requirements

Use a dedicated Linux or WSL Jenkins agent with:

- Git.
- Go 1.26.5 or newer.
- Node.js 24, npm, and Python 3 for deployment regression tests.
- GCC and the C development toolchain required by `go test -race`.
- Docker, with permission to use the Docker daemon.
- `kubectl`, `kind`, `curl`, and AWS CLI for the optional promotion job.
- Access to the registered production cluster (K3s after the initial cutover) through the dedicated `battlefield-production` context. The `battlefield-production-kubeconfig` file credential must match that profile's live cluster UID.

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
mode-700 temporary `DOCKER_CONFIG` for publishing, image scanning/startup verification, signing, deployment, rollback,
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

The PR deployment-script stage and main deployment-scope stage also run
`./scripts/test/test-docker-context.sh`. It builds synthetic scratch contexts to
verify that each component excludes local environment files while retaining
source and examples, using the existing Docker-enabled Jenkins agent.

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
It uses one worker in CI. No shared database or live AI is required. Each frontend
stage then asserts `test -s playwright-report/index.html`, so a run that produces
no browser-test report fails in the stage rather than archiving nothing.

The always post step archives `frontend/test-results/**/*`, including failure
screenshots and retained traces, with `allowEmptyArchive` enabled because those
paths depend on which stages ran. `frontend/playwright-report/**/*` is archived by
a separate step that runs only when `frontend/playwright-report/index.html` exists
and does not allow an empty archive, so a missing browser-test report cannot pass
unnoticed. Builds that never reach the frontend stages log that no report was
produced instead of failing.

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
second Pipeline job using `infra/jenkins/Jenkinsfile.assurance`; schedule only its `nightly`
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

## Manage the persistent `battlefield-local` agent

[CI bootstrap](ci-bootstrap.md#managed-scope-and-preserved-state) owns the inbound
WebSocket agent, enrollment secret, tools and operator user service. The registered
node uses four executors and the `battlefield-production` label shared by PR
verification, main delivery and assurance; the controller has zero executors.
The persistent remote root is the operator's `~/jenkins-agent`.

PR builds isolate PostgreSQL test ports, Playwright ports and image tags by build.
`main` uses `disableConcurrentBuilds()` and the production platform lock to
serialize delivery. Size executors to available host capacity; sharing the accepted
agent does not remove locking requirements for other deployment or drill jobs.

Inspect the existing service as the registered operator:

```bash
systemctl --user status jenkins-agent
journalctl --user -u jenkins-agent --since today
```

Adoption preserves the node's enrollment secret and service configuration. If an
identity is exposed or lost, use reviewed credential recovery and repeat bootstrap
verification. Do not recreate the node or overwrite its service merely after a
reboot; matching controller and agent state must be restored together.

## Configure local SonarQube

The repository includes a persistent local SonarQube Community Build and
PostgreSQL Compose stack. It is separate from the application database:

```bash
docker compose -f infra/sonarqube/compose.yaml up -d
curl --fail http://localhost:9000/api/system/status
```

For the registered server, [CI bootstrap](ci-bootstrap.md) owns the pinned
Sonar/PostgreSQL Compose deployment, initial passwords/token, Jenkins Sonar server
and scanner configuration. It preserves database credentials and named volumes;
use its reviewed maintenance/recovery path for service changes.

For a separate local analysis service only, the repository Compose command above
starts the stack. On a fresh local instance, sign in with its initial `admin` /
`admin` credentials, change the password and create a private analysis token.
Keep it out of Git. This local stack does not configure Jenkins or replace the
registered server's managed Compose deployment.

Containers use `unless-stopped`; Docker must be running for automatic recovery.
After an intentional local `docker compose stop`, the `up -d` command resumes it.
For the registered server, inspect `/etc/battlefield-ci/sonar-compose.yaml` through
the managed workflow instead of replacing it with the local manifest.

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
checked-out code is that preview project's main branch. Before every gated head
scan, `scripts/ci/run-sonarqube-pr-gate.sh` deletes the preview project, uploads
the PR target's merge-base tree as version `base-<commit>` dated at that commit's
committer time (`sonar.projectDate`), and then uploads the head as version
`pr-<number>`. SonarQube processes the baseline first, so the inherited
**Previous version** New Code period measures the PR against its own target and
cannot be moved by another PR. The baseline upload does not wait on its gate;
the head upload waits and fails Jenkins when red.

The baseline date matters because SonarQube backdates issues in newly added files
to the line's commit date. A baseline dated at scan time would put every PR commit
before the New Code period, so issues in new files and new-code coverage would
escape the PR gate and surface only in the main gate. SonarQube rejects an
analysis dated before the project's latest one, which is why each gated scan
starts from a fresh preview project. Deletion needs **Administer** on the preview
project; the Jenkins token's user is a SonarQube administrator. The preview
project therefore holds only the latest head's analysis.

The server was verified with the default **Sonar way** gate assigned to the
production project, so newly created preview projects inherit the same gate.
Keep the production project on the default gate, or update preview provisioning
before changing that assignment. The `autonomous-battlefield` project remains
dedicated to merged `main`, which is scanned again before deployment. Jenkins
requires at least 80% Go statement coverage and 80% for each frontend metric:
statements, branches, functions, and lines. Delete preview projects for closed
PRs periodically from the SonarQube Projects page; they contain analysis history,
not runtime data.

SonarQube data is stored in named Docker volumes and survives restarts. Stop
the separate local analysis stack without deleting its data with:

```bash
docker compose -f infra/sonarqube/compose.yaml stop
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
the ruleset targeting `main`. `dev` is intentionally left without a ruleset so
the owner can work on it directly when needed; Jenkins and SonarQube still run
on pull requests into `dev`, but their results there are advisory. The `main`
ruleset should:

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

Jenkins and SonarQube run on the server; production Kubernetes runs in its
Terraform-managed K3s VM. Old Kind data is retained for migration recovery, with
application writers and tunnel connectors stopped. Verify
those existing services and run `DEPLOY_ENV=production ./scripts/cd/smoke-test.sh`
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
| Frontend exits with `default.conf.template: Permission denied` | Check the image build: private agent checkouts use mode `0600`. The Dockerfile must set readable config modes and a traversable template directory; keep `UMask=0077` on the agent. Run `./scripts/test/test-frontend-image.sh` before publishing the fix. |
| Frontend proxy returns `502` | Confirm `API_UPSTREAM` renders as `battlefield-api.battlefield.svc.cluster.local:8080`, then inspect frontend logs and the `battlefield-api` Service endpoints. |
| `kind load docker-image` says the image is not present and is loading | This is normal informational output: kind is copying a host Docker image into the cluster node. |

## Production delivery

For `main`, the pipeline performs:

1. Clean checkout, Go lint, backend/frontend coverage, source security, and deterministic AI evaluation.
2. SonarQube analysis and quality-gate enforcement.
3. Deployment-script checks and component classification against the previous
   successful commit. Documentation-only, application-test-only, and known CI-test-wrapper
   changes select no deployment; an unknown path or unavailable baseline selects
   all components. CI quality gates still run for these changes.
4. Publication of changed images to GHCR, scanning of the published images,
   and frontend startup/static-asset verification of the exact pulled digest,
   then attestation, signing, and verification.
5. Production rollout with the dedicated kubeconfig, recorded previous backend
   and frontend digest references, migration gating, and Kyverno admission verification.
6. Application and observability smoke verification, followed by parallel k6
   smoke load and ZAP baseline checks.
7. An atomic update of the private, cluster-bound recovery image record after
   all rollout and runtime checks pass.

See [deployment component mapping](deployment.md#change-to-deployment-mapping)
for the paths that select each rollout stage and
[supply-chain assets](../../infra/deploy/supply-chain/README.md) for the trust root.

The deployment keeps the normal application rollout budget at five minutes but
allows 15 minutes for the observability group as a whole. A new single-node
Kind cluster may queue the large Elastic, Grafana, Prometheus, OpenTelemetry,
and Tempo pulls; the group deadline prevents the first named Deployment from
failing while its image is still waiting behind those pulls. On failure,
Jenkins prints descriptions and logs for every unavailable observability
Deployment plus recent namespace events.

`scripts/cd/deploy.sh` can reapply a temporary Kustomize overlay containing
the previous images if an application apply or rollout fails. Before deploying,
Jenkins records the independently deployed backend and frontend digest references. If an
application-related deployment later fails smoke testing,
`scripts/cd/rollback.sh` restores those two exact immutable revisions only
when the private cluster-bound recovery record approves the exact image pair.
The record is written after all runtime assurance stages succeed; an absent or
mismatched record requires operator recovery. The script operates without
re-running migrations or changing observability. Image rollback is skipped for
infrastructure-, migration-, observability-, or gateway-only changes. Database
migrations are deliberately not reversed automatically, so migrations must
remain backward-compatible with the previous application revision.

Local dev loads images into Kind. Production pulls verified GHCR digests.

### AWS promotion limits

The separate AWS promotion helper copies verified image subjects and their
evidence to ECR. The `aws-promotion` mode in `infra/jenkins/Jenkinsfile.assurance` does not
produce or explicitly select a successful build's `artifacts/build/images.env`.
Its cleanup removes `CI_WORK_ROOT`, while that default artifact path is in the
retained checkout. Do not treat a file left by an earlier run as the selected
release. Before routine use, the job needs explicit successful-build evidence
selection and clean artifact handling. The helper accepts `BUILD_IMAGES_FILE`
for an explicitly selected input; this does not add selection to the Jenkins job.

The [AWS reference](../../infra/aws/README.md) provisions infrastructure; an
application overlay, workload secret integration and account-specific deployment
verification are still required.

## Trivy database download recovery

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

## k6 HTTPS trust

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


## K3s delivery adoption

Main invokes the shared `deploy.sh`, `smoke-test.sh` and `rollback.sh` entry points.
Older profiles without `BF_DRIVER` default to Kind; code merges alone never
change their cluster or kubeconfig credential. The current production profile
explicitly selects K3s. Follow [K3s delivery](k3s-delivery.md)
for separate registration, database restore, profile/credential adoption and
coordinated tunnel cutover. K3s platform jobs continue to
own provisioning and controller versions. Existing recovery records without
migration metadata cannot authorize automatic rollback until a successful main
run records the current applied Goose versions.
