# Jenkins and SonarQube bootstrap

`server.sh ci-*` recreates the CI services on Ubuntu 24.04/26.04 x86_64 or adopts
an existing/restored controller. Run it from WSL over the registered SSH connection,
or in an operator terminal on the server with `--local`. It requires sudo. It must
run outside Jenkins: controller maintenance drains builds and can stop Jenkins.
Normal `main` delivery does not invoke this playbook.

The agent launcher requires `/usr/bin/sg` to enter the Docker group without
restarting the operator's entire user manager. The role installs its Ubuntu package
(`util-linux-extra` on 26.04, `login` on 24.04) and checks it as the registered
operator before draining Jenkins. A missing or unusable launcher stops the apply
before controller maintenance. See the [Ubuntu 26.04 package reference](https://manpages.ubuntu.com/manpages/resolute/man1/sg.1.html).

Promotion and a reviewed production adoption are
separate from disposable-host testing; merging this code does not change the
current controller, Sonar containers, agent or their data.

## Existing server

Complete [host registration](server-bootstrap.md) first. Keep the existing Jenkins
administrator API token in `~/.config/jenkins/token`, owned by your user with mode
`0600`. An API token is needed only by the external operator; do not paste it into
chat or commit it. The default administrator login is the registered Jenkins
administrator ([registered facts](server-rebuild-contract.md#registered-facts)).

Before registration, confirm the server is the supported native Jenkins plus
Sonar Compose layout:

1. Compare its installed Jenkins core, installed required plugins, and
   Sonar/PostgreSQL image digests with `infra/jenkins/controller-lock.json`.
   Missing required plugins can be installed by this role; existing required
   plugins must already match their pins. Additional unrelated plugins remain.
2. Ensure the ten documented global credentials, the owned `battlefield-platform`
   folder and its four credentials already exist. Use the existing
   [platform seeder](platform-jobs.md) for that folder and
   [Jenkins credential setup](jenkins.md) for missing external identities.
3. For a restored controller, restore the complete matching home, including keys,
   users and credentials, and its matching Sonar Compose containers/volumes first.
   Use `--ci-install` only for a clean host with no existing controller/agent.

If versions differ, stop before apply. This command does not upgrade or downgrade
an existing release. Prepare a separate reviewed update to the lock and service
compatibility checks, validate it on a disposable host, then review the new plan;
or restore the matching release from a known backup. Do not change lock entries
just to bypass a refusal. The initial pins were checked against this server's
read-only inventory, but that inventory does not replace the current preflight.

```bash
private_dir="$HOME/.config/autonomous-battlefield/server"
./scripts/infra/server.sh ci-register
./scripts/infra/server.sh seal \
  --files "$private_dir/ci-files.json" \
  --vault "$private_dir/ci-inputs.vault"
./scripts/infra/server.sh ci-plan --vault "$private_dir/ci-inputs.vault"
```

Registration creates `ci.json` and `ci-files.json` outside Git. It binds the
machine/operator identity, current agent-unit fingerprint and Jenkins root URL.
Use `--ci-admin LOGIN` or `--ci-url https://jenkins.example` at registration if the
defaults differ. Existing registration files are never silently replaced.
On the physical server, append `--local` to every command and store the API token
there privately before sealing. Ansible prompts for Vault and sudo passwords;
automated callers may pass private non-executable password files with
`--vault-password-file` and `--become-password-file`.

The plan performs read-only identity, plugin and credential checks. Its
`needs_maintenance` and `changes` fields describe the intended operation; it does
not download packages or run the configuration tasks in check mode. For adoption,
use an administrator token: the helper privately reads the existing Sonar string
credential through Jenkins's authenticated script API, and verifies it with Sonar.
It also captures the matching Sonar/PostgreSQL password from the existing Docker
containers. Neither value is printed or rotated. No Sonar UI export is required.

Review the plan, host identity, URL, pinned release and maintenance scope. Then,
from the same external terminal:

```bash
./scripts/infra/server.sh ci-apply --vault "$private_dir/ci-inputs.vault"
./scripts/infra/server.sh ci-verify --vault "$private_dir/ci-inputs.vault"
./scripts/infra/server.sh ci-plan --vault "$private_dir/ci-inputs.vault"
```

The final plan should report `needs_maintenance: false`. An unchanged second
apply verifies services without restarting them. Stopped services require a changed
apply to restore readiness. A changed source, encrypted
input bundle or managed configuration file requires maintenance again. Extracted
third-party tool trees/caches are excluded from the configuration fingerprint;
their pinned archive versions remain part of the release contract. `--report-file` is
available for task-only private evidence; ordinary plan output includes the
human-readable maintenance scope.

## Managed scope and preserved state

| Component | Automation |
| --- | --- |
| Controller | Native Jenkins Debian service, `/var/lib/jenkins`, Java 21; checksum-pinned core and complete plugin dependency closure in `infra/jenkins/controller-lock.json` |
| Configuration as Code | Built-in executors set to zero and the registered root URL; no replacement lists for users, security realms, nodes, jobs or credentials |
| Agent | Existing `battlefield-local` identity, four executors, `battlefield-production`/`local-kind` labels, operator user service and persistent workspace; enrollment is automatic |
| Toolchain | Pinned Go, Node, kubectl and SonarScanner under `/opt/battlefield-ci/tools`; Docker/Buildx/Compose, Java and Chromium OS libraries; Playwright's browser version remains owned by the frontend lockfile |
| Jenkins integration | Scoped Sonar server and scanner configuration; preserve other installations; reuse `seed-platform.py` and its owned `host`, `vm` and `k3s` definitions |
| Sonar | Digest-pinned Compose images, existing `sonarqube` project and named volumes, private database credentials, loopback port 9000, API initialization and project-scoped Sonar way gate; preserve the instance-wide default |
| Certificates | Inventory's public CA certificates enter Ubuntu/Java trust; no global TLS-verification bypass |

Adoption preserves the security realm, administrator, credential values, node
secret, unrelated jobs, repository folder properties and build history. Missing
required credentials, a foreign platform folder, incompatible plugins, changed
agent registration, a different Sonar image/layout or database password mismatch
stop reconciliation. Restored Jenkins homes must include their matching
`secrets/`, `master.key`, users and credential stores. Do not restore XML alone.

Fresh repository/assurance jobs are created with Job DSL and **disabled** until
an operator deliberately enables them after reviewing the credentials and target.
The repository definition discovers `main` and PRs; assurance uses trusted main
and runs only on demand. Existing jobs keep their current trigger and
disabled state. Seeding itself never starts a build. Existing platform jobs stay
manual plan/apply/verify jobs; controller maintenance has no Jenkins job.

Jenkins state, Docker/Sonar volumes and the agent workspace retain their existing
locations. This command does not move them to the 500 GB data filesystem, alter
K3s/Kind, install CLI workers, change Cloudflare or introduce scheduled backups.
Application database storage continues to use the registered K3s data disk.

## Clean installation inputs

On a registered clean Ubuntu host, use `ci-register --ci-install`. Registration
generates three random password files beside `ci.json` and a `ci-files.json`
manifest. Supply the fourth file, `ci-credentials.json`, with the existing external
identities needed for repository builds, image publication and deployment. Seal
these files with the same `seal` command above before `ci-plan`/`ci-apply`.

`ci-credentials.json` is a JSON object keyed by these IDs:

| IDs | Value shape |
| --- | --- |
| `github-app-battlefield` | `{"type":"githubApp","appId":"NUMERIC_ID","privateKey":"PEM_CONTENT"}` |
| `battlefield-ghcr` | `{"type":"usernamePassword","username":"LOGIN","password":"TOKEN"}` |
| `battlefield-cosign-password` | `{"type":"string","value":"PASSPHRASE"}` |
| `battlefield-cosign-key`, `battlefield-cosign-public-key`, `battlefield-production-kubeconfig`, `battlefield-production-access-service-token` | `{"type":"file","value":"FILE_CONTENT"}` |
| `host-inventory`, `host-inputs`, `host-vault-password`, `host-sudo-password` | The same file shape, installed only in the owned `battlefield-platform` folder; `host-inputs` must already be Ansible Vault ciphertext |

Keep this JSON and each input file outside Git, mode `0600`. Values are file
contents, not paths; JSON-escape newlines when preparing PEM/configuration files.
The platform inputs have the same meanings as the [platform seeder](platform-jobs.md).
Do not use fixture credentials outside the disposable rehearsal.

Earlier releases also required `battlefield-local-secret-env` and
`battlefield-production-access-token`. No pipeline uses them; remove both keys from
`ci-credentials.json` (a fresh install rejects unknown IDs) and delete any leftover
copies from **Manage Jenkins > Credentials**. Adoption ignores them.

Fresh Jenkins starts with authentication required, signup disabled and the chosen
administrator. Sonar's initial `admin` password is replaced through its API and a
user token is generated privately for Jenkins and preview-project administration.
After controller initialization succeeds, the temporary plaintext bootstrap JSON
is replaced with metadata only (administrator name, home, mode and scanner path).
Credentials then remain in Jenkins's credential store and the encrypted operator
inputs. If initialization fails before that checkpoint, its private staging file
remains for diagnosis/recovery and must be handled as secret material. The local
Sonar database password/token and agent/bootstrap API token still require private
service files; maintenance archives can contain these, plus Jenkins decryption
keys, and therefore remain root-only.

The resulting identities persist across repeats/reboots. `ci-verify` checks local
service, plugin and agent readiness; the first real repository PR also needs green
Jenkins/GitHub checks and a Sonar gate before production adoption is complete.

## Maintenance backup and failure recovery

A changed apply holds the existing host operation lock, enters Jenkins quiet mode
and waits up to five minutes for running **and paused** builds to finish. It never
aborts them. If a build is waiting for the same host lock, this bounded drain can
time out; let that build finish and retry. A pre-existing quiet mode belongs to
its original operator and is not claimed by this command.

After draining, the command keeps quiet mode across the controller restart and
stops Jenkins for a consistent private archive. It preserves jobs/history,
credentials and encryption keys, while excluding disposable workspaces/caches.
It checks space for the estimated controller/database size, 20% headroom and
2 GiB of remaining free space before writing the backup. It takes a PostgreSQL
custom-format dump of Sonar's database. Backups go to
`/srv/battlefield-data/maintenance` when that filesystem is mounted, otherwise
`/var/backups/battlefield-ci`. Both are root-only. The exact completed directory is
recorded in `/var/lib/battlefield-ci/last-backup`. This is a local pre-maintenance
copy, not a scheduled backup policy or whole-server-loss recovery copy.

If drain fails, the helper releases only its own quiet mode. A backup failure
before configuration changes automatically restarts an originally running
controller and releases its own receipt/quiet mode; an originally stopped
controller stays stopped. The safe failure message identifies the rejected
backup condition, so correct it and rerun the plan. If automatic restart fails,
inspect/start the unchanged service before clearing that pre-change receipt.
Incomplete backup directories are not recorded as completed backups.

If the agent fails with `203/EXEC` and its journal reports a missing
`/usr/bin/sg`, repair that dependency first; on Ubuntu 26.04 use
`sudo apt-get install --no-upgrade util-linux-extra` (use `login` instead on
Ubuntu 24.04), then restart the operator's
`jenkins-agent` user service. Confirm that the existing node reconnects. For an
already interrupted apply, keep its completed backup and maintenance receipt:
installing the dependency alone does not complete acceptance or resume builds.
Review and finish the original service acceptance before clearing maintenance;
a blind repeat is deliberately rejected while its receipt remains.

If configuration
fails after services begin changing, it keeps the maintenance receipt/quiet state
and reports that recovery is required. Inspect `systemctl status jenkins`, its
journal and `docker compose` status locally; do not paste full private logs or
configuration into chat. Fix the reported cause before trying again.

For a failed adoption, keep the original completed backup. Stop Jenkins, restore
its archived home **with matching keys and ownership**, restore the prior service
configuration, and start that matching release. Restore the Sonar dump only if its
database was changed and rollback requires it; ordinary configuration failures do
not justify overwriting a healthy database. Verify local authentication, existing
jobs and agent connectivity before clearing `battlefield-maintenance` in Jenkins
home and `/var/lib/battlefield-ci/maintenance.json`, then explicitly cancel Jenkins
quiet mode. Do not clear the receipt merely to bypass a failed preflight.
A failed fresh install can be inspected and repaired in the same external session;
its generated input files and private state allow identities to be retained.

## Validation

```bash
python3 scripts/test/test-ci-bootstrap.py
./scripts/test/test-deployment.sh
# On a libvirt-capable operator host; uses synthetic credentials only:
python3 scripts/test/run-ci-bootstrap-rehearsal.py \
  --evidence-dir "$HOME/ci-bootstrap-rehearsal-$(date +%Y%m%d-%H%M%S)"
```

The secret scan retains its default detectors. `.gitleaks.toml` enumerates only
known public dependency version strings in the controller lock and the isolated
Sonar project identifier, each scoped to its source file. Review those exact
entries when updating plugin dependencies; do not exclude the whole lock file.

The opt-in rehearsal uses an isolated 8 GB / 40 GB disposable Ubuntu VM and the
existing NAT network. It validates fresh installation, no-change repeat, service
readiness, temporary credential cleanup, safe restart after a backup guard fails,
a credential-bound Sonar analysis/quality gate, restored-controller
adoption with unmanaged history, and reboot identity/agent reconnection, then removes its own UUID-bound
VM and pool. It never uses production input credentials.

References: [Jenkins Configuration as Code](https://plugins.jenkins.io/configuration-as-code/),
[Jenkins backup guidance](https://www.jenkins.io/doc/book/system-administration/backing-up/),
[Sonar Web API](https://docs.sonarsource.com/sonarqube-community-build/extension-guide/web-api).
