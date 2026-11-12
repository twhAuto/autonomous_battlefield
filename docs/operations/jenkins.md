# Jenkins operations

This guide administers the registered CI server: its controller, jobs, agent,
credentials, SonarQube and GitHub integration, and the `main` delivery pipeline.
Contributors who want to know what a PR build runs, or how to reproduce it
locally, should read [continuous integration](../developer/ci.md).

Related guides own the surrounding procedures:

- [Server bootstrap](server-bootstrap.md): the Ubuntu host foundation and
  credential import. It can run from WSL before Jenkins exists.
- [CI bootstrap](ci-bootstrap.md): installing, adopting or restoring the
  controller, agent and SonarQube, including pinned versions.
- [Platform jobs](platform-jobs.md): the manual `battlefield-platform` host, VM,
  K3s and CLI jobs.

## Controller

The production Jenkins controller runs on the registered server (see
[registered facts](server-rebuild-contract.md#registered-facts)), port `8080`. Its state persists under `/var/lib/jenkins`, and the native service starts
at boot. Routine diagnostics:

```bash
sudo systemctl status jenkins
sudo journalctl -u jenkins --since today
```

Install, adopt or restore the controller only through the external
[CI bootstrap workflow](ci-bootstrap.md) from a registered operator session. Never
run controller maintenance from a Jenkins job or improvise installation over
retained controller state. A restore needs the complete matching Jenkins home,
including its keys and credential stores, plus matching Sonar data. Use the
[reviewed maintenance and recovery procedure](ci-bootstrap.md#maintenance-backup-and-failure-recovery)
for changes that can stop the controller.

The controller's plugins are pinned in
[`infra/jenkins/controller-lock.json`](../../infra/jenkins/controller-lock.json),
and platform jobs add the pins in
[`infra/jenkins/platform-plugins.json`](../../infra/jenkins/platform-plugins.json).
Keep Jenkins core and plugins patched through a reviewed pin and compatibility
change; ordinary adoption preserves existing identities.

## Jobs

CI bootstrap seeds the repository and assurance jobs with Job DSL. New jobs start
**disabled** until an operator enables them after reviewing their credentials and
target. Bootstrap never re-seeds an existing job, so when a job definition changes,
such as a moved Jenkinsfile or different PR discovery, update that job's
configuration by hand to match it.

| Job | Definition | Trigger | Purpose |
| --- | --- | --- | --- |
| `autonomous-battlefield` (Multibranch) | [`repository.groovy`](../../infra/jenkins/repository.groovy), `Jenkinsfile` | Periodic repository scan every 2 minutes | PR verification for PRs into `dev` or `main`, and delivery of `main` |
| `battlefield-assurance` | [`assurance.groovy`](../../infra/jenkins/assurance.groovy), `infra/jenkins/Jenkinsfile.assurance` on trusted `main` | Manual only | Modes `offline`, `load`, `failure-drills`, `provider-eval` and `aws-promotion` |
| `battlefield-platform/*` | [Platform jobs](platform-jobs.md) | Manual only | Reviewed host, VM, K3s and CLI worker plan/apply/verify |

### Repository job

The Multibranch job uses the `github-app-battlefield` credential and builds only
`main` and `PR-N` heads. It discovers PRs only from branches in this repository,
never from forks. It merges each origin PR with its current target, and it
excludes branches that are also filed as PRs. Pushes to `dev` and work branches
therefore build nothing. A merged `dev` head is covered by its PR's last build when
`git rev-parse MERGE^{tree}` equals `git merge-tree --write-tree TARGET HEAD`, where
`MERGE` is the merge commit on `dev` and `TARGET` and `HEAD` are the commits named in
that build's
`Merging remotes/origin/dev commit TARGET into PR head commit HEAD` console line.

GitHub cannot reach Jenkins on the private network, so there is no webhook; the
periodic scan discovers new PRs and commits. Run **Scan Repository Now** on the job
to pick up a change sooner. Add a webhook only after Jenkins has an authenticated,
HTTPS-protected address that GitHub can reach.

A scan builds a PR when it first appears and whenever its head commit changes; a
newer target-branch commit alone starts no build. A PR that conflicts with its
target gets no `PR-N` job until the conflict is resolved and a scan runs, so check
the PR's mergeability before looking for a missing build. To rebuild without a new
commit, use **Build with Parameters** on the `PR-N` job (a plain build request is
rejected because the job takes parameters); the rebuild reuses the previous build's
revisions. Before treating a green build as evidence, compare the revision it built
with the current PR head and target tip.

Each `PR-N` child job numbers its builds independently, and the `Jenkinsfile`
keeps the last 20 builds of each active job. Closed PRs become orphaned child jobs,
which are kept for 30 days, up to 20 items. After that, a scan deletes the job;
GitHub keeps the check result, but its Jenkins link stops working.

### Assurance job

Run `battlefield-assurance` on demand; it has no schedule because production is
not kept running. The `offline` mode runs the security and policy checks, a
two-minute fuzz run and the deterministic AI evaluations. The other modes require
explicit bounds.

#### AWS promotion limits

The AWS promotion helper copies verified image subjects and their evidence to ECR.
The `aws-promotion` mode does not produce or explicitly select a successful
build's `artifacts/build/images.env`, and its cleanup removes `CI_WORK_ROOT` while
that default artifact path stays in the retained checkout. Do not treat a file left
by an earlier run as the selected release. Before routine use, the job needs
explicit successful-build evidence selection and clean artifact handling. The
helper accepts `BUILD_IMAGES_FILE` for an explicitly selected input, but the job
does not yet pass one.

The [AWS reference](../../infra/aws/README.md) provisions infrastructure only; an
application overlay, workload secret integration and account-specific deployment
verification are still required. AWS promotion also needs an assumed agent role
or another short-lived AWS credential source; never store long-lived access keys
in the repository.

## Agent

[CI bootstrap](ci-bootstrap.md#managed-scope-and-preserved-state) owns the
persistent inbound WebSocket agent `battlefield-local`, its enrollment secret,
tools and operator user service. The node has four executors and the
`battlefield-production` label, which PR verification, `main` delivery and
assurance all use; the controller has zero executors. Its remote root is the
operator's `~/jenkins-agent`.

Inspect the agent as the registered operator:

```bash
systemctl --user status jenkins-agent
journalctl --user -u jenkins-agent --since today
```

Adoption preserves the node's enrollment secret and service configuration. If an
identity is exposed or lost, use reviewed credential recovery and repeat bootstrap
verification. Do not recreate the node or overwrite its service after a reboot;
matching controller and agent state must be restored together.

### Requirements

- Git, and a Go toolchain compatible with [`backend/go.mod`](../../backend/go.mod).
- GCC and the C development toolchain required by `go test -race`.
- Node.js 24 and npm, with Playwright's Linux browser libraries installed once by
  `npx playwright install-deps chromium`. CI does not attempt privileged package
  installation.
- Python 3.12–3.14 for the deployment regression tests. The first run of each
  pinned runtime and Python minor version needs PyPI; it installs and caches
  checksum-verified dependencies, and later runs do not contact PyPI.
  `./scripts/infra/server.sh tools` can prewarm the cache. Keep it persistent: an
  unavailable uncached runtime fails the gate rather than skipping the checks.
- Docker on the same host as the agent, with permission to use the daemon.
- `kubectl`, `kind`, `curl`, and the AWS CLI for optional promotion.
- The `battlefield-production` kubectl context for the registered cluster. The
  `battlefield-production-kubeconfig` credential must match that profile's live
  cluster UID.

Each build needs enough free space and inodes in its workspace for parallel Go
compilation; see [disk quota](#disk-quota-and-go-caches) when that runs out.

### Concurrency

One PR is verified at a time. The `PR verification` stage holds the
`battlefield-pr-verification` lock, because its test, browser and deployment-script
time limits assume the host is running one build and overlapping builds starve
each other past them. Other PR builds queue for the lock in arrival order, each
occupying an executor while it waits. A build's 45-minute limit starts when it
gets the lock, so time in the queue does not count against it.

`main` does not take that lock, so a delivery can overlap one PR build; builds
isolate PostgreSQL test ports, Playwright ports and image tags, so the two do not
collide on those. `main` uses `disableConcurrentBuilds()`, has 45 minutes for
`Main delivery`, and holds the `battlefield-production-platform` lock for its
rollout, the same lock the platform jobs take for their whole operation. Size
executors to the host's capacity; sharing the agent does not remove locking
requirements for other deployment or drill jobs.

### Shared CI and production agent

Every Jenkinsfile runs on this one agent. That is a deliberate tradeoff for a small
project with trusted build authors. PR scripts and dependency installers run with
access to the shared host and its Docker daemon, which is effectively privileged
host access, so mistakes or malicious code can affect production. Stage-scoped
credentials and main-only delivery still apply, but a PR-editable Jenkinsfile and
inherited Multibranch credentials are not a security boundary.

Only accounts that can push to the repository can start a PR build, because the
[repository job](#repository-job) never discovers fork PRs. Keep builds off the
controller's built-in node. Revisit isolation before accepting contributors without
push access: a separate CI VM and Docker daemon,
delivery credentials scoped to trusted jobs, and controller-enforced node access.
A second label or executor on this host would not provide that isolation.

## Credentials

CI bootstrap creates these global credentials. All except
`sonarqube-battlefield-token` come from `ci-credentials.json` on a fresh install;
bootstrap generates the Sonar token itself, or imports it from the separate
encrypted `ci_sonar_token` input. [Clean installation inputs](ci-bootstrap.md#clean-installation-inputs)
is the authoritative input schema.

| ID | Kind | Used for |
| --- | --- | --- |
| `github-app-battlefield` | GitHub App | Repository scans and checkouts, and the `Coverage` check |
| `battlefield-ghcr` | username/password | GitHub Packages login with only `read:packages` and `write:packages` |
| `battlefield-cosign-key` | secret file | Encrypted Cosign private key |
| `battlefield-cosign-password` | secret text | Password for the encrypted signing key |
| `battlefield-cosign-public-key` | secret file | Reviewed public verification key |
| `battlefield-production-kubeconfig` | secret file | Dedicated production kubeconfig; bound only during rollout or an explicit assurance load |
| `battlefield-production-access-service-token` | secret file | Restricted CI service identity; bound only for production smoke, load and authenticated response capture |
| `battlefield-production-application-env` | secret file | Production application `.env`; bound only during rollout and CLI worker checks and repairs |
| `sonarqube-battlefield-token` | secret text | SonarQube analysis |

The assurance job's `provider-eval` mode also uses `battlefield-openai-api-key` or
`battlefield-gemini-api-key` (secret text), which you add by hand. Never expose
signing or provider credentials to pull-request stages. The `battlefield-platform`
folder holds its own credentials; see [platform jobs](platform-jobs.md#managed-credentials).

### Production application configuration

The `battlefield-production-application-env` credential holds the production
application `.env`: database, authentication, provider and integration settings.
Rollout, the CLI worker check and the [CLI worker job](cli-worker.md) bind it as
`BATTLEFIELD_APPLICATION_FILE`; pull-request stages never do. The file is parsed as
literal data, and Jenkins binds no separate database credential.

Keep no `.env`, and no backup copy of one, in the agent user's home: every build,
pull requests included, runs as that user and can read its files. The agent's
`~/.config/autonomous-battlefield/production/deployment.env` identifies the cluster
and stays there. Keep the master copy of `.env` outside the agent host. To change a
setting, edit that copy, upload it over the credential's file in **Manage Jenkins →
Credentials**, then run the `main` pipeline with `APPLY_CONFIGURATION`.

The credential removes the standing copy from the agent user's home; it does not
isolate the file. While a delivery or CLI worker job has it bound, a build running
on another of the agent's executors can read the temporary copy, and a build can
bind the credential by editing its own Jenkinsfile; see
[shared CI and production agent](#shared-ci-and-production-agent).

### CI service identity

Provision the `battlefield-production-access-service-token` file and the
production `BATTLEFIELD_ACCESS_CI_CLIENT_ID` before delivery. The
[CI service identity](access.md#ci-service-identity) procedure
covers the JSON schema, the Cloudflare Service Auth policy, expiry monitoring and
rotation. Jenkins validates the credential's local metadata before rollout, then
tests read-only service access through the public hostname; it needs no copied
browser session cookie.

## SonarQube server

[CI bootstrap](ci-bootstrap.md) owns the server's pinned SonarQube and PostgreSQL
Compose deployment (`/etc/battlefield-ci/sonar-compose.yaml`), its passwords and
token, and Jenkins's Sonar server and scanner configuration. It preserves database
credentials and named volumes, so inspect or change the deployment through that
workflow rather than replacing it. Contributors can run a separate
[local SonarQube](../developer/ci.md#local-sonarqube).

SonarQube imports `backend/coverage.out` and `frontend/coverage/lcov.info` through
`sonar-project.properties`; it does not generate them. The scanner polls for the
quality-gate result for up to five minutes and fails the build when the gate fails,
so SonarQube never needs to call back to Jenkins and no webhook is configured.
`scripts/ci/run-sonarqube-gate.sh` removes Jenkins multibranch metadata before
invoking the scanner and can override the project identity.

### PR preview projects

Community Build cannot analyze the `dev` branch, so each PR is an isolated project
named `autonomous-battlefield-pr-<number>`. Before every gated scan,
`scripts/ci/run-sonarqube-pr-gate.sh`:

1. deletes the preview project;
2. uploads the PR target's merge-base tree as version `base-<commit>`, dated at that
   commit's committer time (`sonar.projectDate`), without waiting for its gate;
3. uploads the head as version `pr-<number>`, waits for its gate and fails Jenkins
   when it is red.

The inherited **Previous version** New Code period therefore measures the PR
against its own target. The baseline date matters because SonarQube backdates
issues in new files to each line's commit date; a baseline dated at scan time would
let issues in new files and new-code coverage escape the PR gate. SonarQube rejects
an analysis dated before the project's latest one, which is why each scan starts
from a fresh project. Deleting it needs **Administer**, so the Jenkins token's user
is a SonarQube administrator.

Preview projects inherit the instance's default gate. The server was verified with
**Sonar way** as that default, the same gate bootstrap assigns to the production
project; keep it, or update preview provisioning before changing the default. The `autonomous-battlefield` project is reserved
for merged `main`, which is scanned again before deployment. Periodically delete
preview projects for closed PRs from the Projects page; they hold only analysis
history.

## GitHub integration

### GitHub App and the Coverage check

GitHub lets only GitHub Apps, not personal access tokens, create Check Runs. The
PR pipeline uses Jenkins `publishChecks` to add a `Coverage` check showing the Go
statement percentage and the four frontend percentages. It fails when Go
statement coverage or any frontend metric is below 80%; **Details** opens the
Jenkins console.

To create the app for a new installation:

1. In GitHub, open **Settings > Developer settings > GitHub Apps > New GitHub
   App** and give it a unique name, such as `Jenkins OrdinalFrontier`.
2. Grant these repository permissions: Checks and Commit statuses **Read and
   write**; Contents, Metadata and Pull requests **Read-only**.
3. Disable the app webhook, because GitHub cannot reach Jenkins.
4. Install the app only on `twhAuto/autonomous_battlefield`, then generate and
   download a private key.
5. Convert the key to the PKCS#8 form Jenkins expects:

   ```bash
   openssl pkcs8 -topk8 -inform PEM -outform PEM \
     -in downloaded-github-app-key.pem \
     -out jenkins-github-app-key.pem -nocrypt
   ```

6. Supply the app ID and converted key as `github-app-battlefield` in
   `ci-credentials.json`. On an existing controller, update that credential in
   **Manage Jenkins > Credentials**, then run **Scan Repository Now**.

### `main` branch protection

Only `main` is protected. `dev` intentionally has no ruleset, so maintainers can
push to it directly; Jenkins and SonarQube still run on PRs into `dev`, but their
results there are advisory. In **Settings > Rules > Rulesets**, the `main` ruleset
should:

1. require a pull request before merging and block direct pushes;
2. require the Jenkins PR result named `Jenkins`, which includes the SonarQube
   gate;
3. require the separate `Coverage` check;
4. require the branch to be up to date before merging, when that fits the merge
   strategy.

Do not require the `main` delivery result as a pre-merge check: that build exists
only after GitHub creates the merge commit. Ruleset enforcement depends on the
repository's visibility and GitHub plan; if either changes, confirm GitHub still
reports the rulesets as active rather than treating the checks as advisory.

## Production delivery

For `main`, the pipeline runs:

1. Clean checkout, Go lint, backend and frontend coverage, source security, and
   deterministic AI evaluation.
2. SonarQube analysis and quality-gate enforcement.
3. Deployment-script checks and component classification against the previous
   successful commit. Documentation-only, application-test-only, and CI
   lint/coverage-summary script changes select no deployment; an unknown path or
   unavailable baseline selects all components. The quality gates still run.
4. Publication of changed images to GHCR, scanning of the published images, and
   frontend startup and static-asset verification of the exact pulled digest,
   then attestation, signing and verification.
5. Production rollout with the dedicated kubeconfig, recorded previous backend and
   frontend digests, migration gating, and Kyverno admission verification.
6. Application and observability smoke verification, then parallel k6 smoke load
   and a ZAP baseline check.
7. An atomic update of the private, cluster-bound recovery image record, after all
   rollout and runtime checks pass.
8. A read-only CLI worker check (`cli-worker.py verify`). A down or out-of-sync
   worker marks the build unstable, never failed, and never triggers rollback;
   repair it with [`battlefield-platform/cli`](cli-worker.md). A worker older than
   the release, or Codex other than the pinned release, is only a notice.

[Deployment component mapping](deployment.md#change-to-deployment-mapping) lists
the paths that select each rollout stage, and the
[supply-chain assets](../../infra/deploy/supply-chain/README.md) describe the trust
root. The pipeline calls the shared `scripts/cd/deploy.sh`, `smoke-test.sh` and
`rollback.sh` entry points. The registered profile's `BF_DRIVER` selects the
cluster driver; the production profile selects K3s, and a profile without it
defaults to Kind.

Application rollouts have a five-minute budget; the observability group as a whole
has 15 minutes, so slow image pulls don't fail the first Deployment. On failure,
Jenkins prints descriptions and logs for every unavailable observability Deployment
plus recent namespace events.

Before the migration Job, the deploy streams a `pg_dump` from the Postgres pod
through `age` into `~/.local/state/battlefield/backups` on the agent, encrypted to
the `BF_BACKUP_RECIPIENT` public key in the production `deployment.env`, while
`pg_restore --list` checks the same stream inside the pod. Only ciphertext reaches
the disk. The dump and its check get five minutes each (`BACKUP_TIMEOUT`, default
`300s`). The limit applies inside the Postgres pod, and `kubectl exec` gets 15
seconds more. `pg_dump` gives up on table locks after one minute. If a step fails or
runs out of time, encryption fails, the profile has no valid recipient, or the agent
lacks `age`, the deploy deletes the partial dump and stops before the migration Job,
so production keeps its running release. A failed dump or check also prints the
`battlefield-postgres-0` pod status, its recent events, and the database sessions
with their states, wait events, transaction ages and blocking sessions. Use that
output to find a restarting pod or a session holding locks before rerunning the
build. After a verified backup, the deploy keeps the newest three encrypted dumps
(`pre-migration-*.dump.age`) and deletes older ones and any plaintext
`pre-migration-*.dump`, because each holds a full copy of the database, member
emails included.

The CI role installs `age`. Generate the key pair on the operator workstation and
keep the private key there, with the other
[operator recovery inputs](server-rebuild-contract.md#state-and-credential-locations);
it never goes on the host. `age-keygen` prints the public key:

```bash
age-keygen -o ~/.config/autonomous-battlefield/server/backup-identity.txt
```

Add the public key to the production `deployment.env` as
`BF_BACKUP_RECIPIENT=age1…`, or pass it as `BACKUP_RECIPIENT` to
`scripts/cd/setup-environment.sh` when registering a cluster. To inspect a dump,
decrypt it on the workstation and stream the plaintext, substituting the
[registered operator and host](server-rebuild-contract.md#registered-facts):

```bash
ssh -o IdentitiesOnly=yes -i ~/.ssh/battlefield-automation <operator>@<host> \
  'cat ~/.local/state/battlefield/backups/<dump>.dump.age' |
  age --decrypt --identity ~/.config/autonomous-battlefield/server/backup-identity.txt |
  pg_restore --list
```

To restore, stream the same decrypted output into `pg_restore --no-owner
--exit-on-error --single-transaction` for the target database, whose roles must
already exist, as in the [cluster move](k3s-delivery.md#move-production-to-a-new-cluster).

### Rollback

Before deploying, `scripts/cd/capture-rollback-images.sh` records the deployed
backend and frontend digests. If an application apply or rollout fails,
`scripts/cd/deploy.sh` can reapply a temporary overlay with the previous images.
If an application-related deployment later fails smoke testing,
`scripts/cd/rollback-on-failed-verification.sh` calls `scripts/cd/rollback.sh` to
restore those two exact digests, but only when the recovery record approves that
image pair. An absent or mismatched record requires operator recovery.

Rollback does not rerun migrations or change observability, and it is skipped for
infrastructure-, migration-, observability- or gateway-only changes. Database
migrations are never reversed automatically, so each migration must stay
compatible with the previous application revision.

### Production k6 and ZAP

Production k6 reads the authentication mode and the enrolled CI client ID from the
deployed `battlefield-auth` Secret. It validates the service-token JSON and mounts
it read-only, so secret values never appear as Docker arguments. It unsets
admin-password inputs and refuses redirects; username/password and cookie login
are dev-only.

Production ZAP scans passively. Jenkins captures authenticated GET responses for
`/v1/auth/session`, `/v1/scenarios` and `/livez`, verifies the CI identity and the
catalog response, and strips credential and cookie headers before exporting a HAR.
The scanner receives no credentials, runs with `--network none` and imports the
recorded responses without resending them. It fails on high-risk findings and
writes `artifacts/security/zap/coverage.json` to record this limited coverage. It
does not certify authenticated admin or player routes or the whole frontend;
broader crawling and role-specific security tests belong in staging. See
[ZAP HAR import](https://www.zaproxy.org/docs/desktop/addons/import-export/automation/).

Kubelet scrapes in production need trusted kubelet certificates; see
[production kubelet TLS](observability.md#production-kubelet-tls).

## Troubleshooting

| Symptom | Meaning and check |
| --- | --- |
| A new PR or commit has no build | The periodic scan hasn't run yet. Run **Scan Repository Now** on `autonomous-battlefield`. |
| A feature-branch push has no stages | Intentional. Open a PR into `dev` to trigger CI. |
| A new PR shows only build `#1` | Build numbering is local to each `PR-N` job; this is not evidence that history was deleted. |
| `No tool named SonarScanner found` | Install **SonarQube Scanner for Jenkins**, restart Jenkins if requested, then add a tool named exactly `SonarScanner` under **Manage Jenkins > Tools**. |
| SonarQube analysis times out waiting for the gate | Check `curl --fail http://localhost:9000/api/system/status`, then **Administration > Projects > Background Tasks** in SonarQube. |
| `battlefield-local` is offline | Run `systemctl --user status jenkins-agent`, confirm the controller is active, and restart the user service after fixing the reported error. |
| Frontend exits with `default.conf.template: Permission denied` | Private agent checkouts use mode `0600`, so the Dockerfile must set readable config modes and a traversable template directory; keep `UMask=0077` on the agent. Run `./scripts/test/test-frontend-image.sh` before publishing the fix. |
| Frontend proxy returns `502` | Confirm `API_UPSTREAM` renders as `battlefield-api.battlefield.svc.cluster.local:8080`, then check the frontend logs and the `battlefield-api` Service endpoints. |
| `kind load docker-image` says the image is not present and is loading | Informational: kind is copying a host image into the cluster node. |

### Disk quota and Go caches

Pipelines keep `GOCACHE`, `GOTMPDIR` and temporary files in the build's temporary
workspace and delete it after every build. If a build still reports
`disk quota exceeded`, inspect capacity, inodes, the user quota and leftover
caches from older or interrupted builds:

```bash
df -h /tmp "$HOME/jenkins-agent"
df -i /tmp "$HOME/jenkins-agent"
quota -s 2>/dev/null || true
du -sh /tmp/autonomous-battlefield-go-cache-* /tmp/go-build* 2>/dev/null || true
find /tmp -maxdepth 1 -user "$(id -un)" -type d \
  \( -name 'autonomous-battlefield-go-cache-*' -o -name 'go-build*' \) -print
```

After all builds have stopped, remove only the directories that the last command
printed. They are compiler caches and interrupted Go work trees, not build
evidence:

```bash
find /tmp -maxdepth 1 -user "$(id -un)" -type d \
  \( -name 'autonomous-battlefield-go-cache-*' -o -name 'go-build*' \) \
  -exec rm -rf -- {} +
```

If standard-library packages appear missing, check the Go installation as the
agent account. `go list sync/atomic` must print `sync/atomic`; a stale cache from
a replaced Go installation can otherwise hide packages.

```bash
go version
go list sync/atomic
```

### Trivy database download

The source-security launcher prepares the vulnerability database in the persistent
Docker volume `autonomous-battlefield-trivy-cache` (override with
`TRIVY_CACHE_VOLUME`). It tries `mirror.gcr.io/aquasec/trivy-db:2`, then
`ghcr.io/aquasecurity/trivy-db:2`, with a three-minute timeout each. Only after a
successful preparation does the scan use `--skip-db-update`; cached data alone
does not bypass a failed preparation, and two failures fail the security gate.
Kubernetes policy checks still run and publish their reports.

A `read: connection reset by peer` during preparation is a download failure, not a
vulnerability finding. If both registries fail, check the agent's and containers'
connectivity to them and rerun.

### k6 HTTPS trust

The k6 launcher mounts the agent's `/etc/ssl/certs/ca-certificates.crt` read-only
and points `SSL_CERT_FILE` at it, so k6 trusts the agent's installed private CAs
while keeping TLS verification. `K6_CA_BUNDLE` selects another readable, nonempty
PEM bundle; a missing or empty bundle fails before k6 starts. Browser trust
settings do not affect k6. Public Cloudflare endpoints use public roots, so add a
private CA only when a target needs one.

### Daily startup after a reboot

Jenkins and SonarQube run on the server, and production Kubernetes runs in its
Terraform-managed K3s VM. Old Kind data is retained for migration recovery, with
its application writers and tunnel connectors stopped. After a reboot, verify those
services and run `DEPLOY_ENV=production ./scripts/cd/smoke-test.sh` as the agent
account. Do not run the dev bootstrap on this machine. If the production cluster
is missing, recover its data and registered identity rather than recreating it.

[Environment operations](environments.md) cover production registration and
configuration, and [K3s delivery](k3s-delivery.md) covers the kubeconfig credential
and database export and restore. WSL needs only Docker and its dev cluster for
local work; local Jenkins and SonarQube need not be running.
