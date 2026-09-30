# Reviewed Jenkins host operations

`battlefield-platform/host` is a separate, manually started Jenkins job for the
existing Ansible host foundation and optional data partition. A merge into `main`
runs application delivery; it does not run this job. The job reads
[`Jenkinsfile.platform`](../../Jenkinsfile.platform) from trusted `main` and runs
locally as the registered `twh09` operator on `battlefield-production`. The SSH
private key stays on the workstation.

This implements the host slice of
[AB-131 — Add reviewed Jenkins platform jobs and infrastructure verification](https://app.notion.com/p/3eab7a19f8f781f7b89ad3606e2dc11f).
[AB-107](https://app.notion.com/p/3e3b7a19f8f7816e8b29d8e532189e2d) holds the execution
map. Terraform VM provisioning (AB-126), reproducible Jenkins/SonarQube services
(AB-127), guest K3s (AB-128), delivery integration (AB-129), and full platform
verification remain separate work. Scheduled backups (AB-130) are deferred by the
owner. This job does not migrate Docker/PostgreSQL or deploy K3s.

## One-time installation

Use the workstation checkout containing this change. Existing Jenkins must already
have the `battlefield-production` agent running as the registered operator and
SCM credential `github-app-battlefield`. Controller rebuilds and maintenance remain
external administration work; a job cannot reliably rebuild its own controller.

1. In Jenkins, open your user settings, **Security → API Token → Add new Token**.
   Use an administrator account for job/credential/plugin installation. This
   repository's approval submitter is `whtan`; change that reviewed setting before
   adopting a different owner. Keep the token outside Git and do not paste it into
   chat, ticket text, or a command argument.
2. Run the setup command below in a terminal. It asks privately for the Jenkins
   token, a new Vault passphrase (twice), and the server sudo password. If inputs
   were sealed previously, supply their existing Vault passphrase. An optional
   `--token-file /absolute/private/path` avoids retyping a saved token.
3. Wait for successful setup. If Jenkins reports a plugin needs maintenance,
   arrange a controller restart outside the job when existing builds are idle,
   then rerun setup. After the reviewed change reaches `main`, open the host job
   and run **plan** first.

For the registered server and its existing 500 GB ext4 partition:

```bash
python3 scripts/infra/setup-platform.py \
  --host 192.168.1.126 --user twh09 \
  --identity-file "$HOME/.ssh/battlefield-automation" \
  --data-uuid 75db9b1d-5c46-4e5b-bd94-5391b2a35411 \
  --data-mount /srv/battlefield-data \
  --jenkins-user whtan --install-plugins
```

The command registers and inspects the host, selects the already formatted data
partition, seals discovered input files, verifies the passphrase and sudo access,
opens a strict SSH tunnel to Jenkins, and creates the folder/job and four folder
credentials. It does not format/mount a disk, run an apply, start a build, rotate
existing credentials, or restart Jenkins. Inventory and the encrypted input bundle
stay in `~/.config/autonomous-battlefield/server/`; retain the Vault passphrase in
a password manager. Temporary plaintext password files are removed on exit.
Registration discovers known inputs; inspect `inputs.json` for any additional
files required by future service roles before sealing, as described in
[server bootstrap](server-bootstrap.md#register-and-inspect).

The installer only adopts folders, jobs and credentials carrying its ownership
marker. Reruns preserve folder properties and existing credential values. A
changed local inventory/bundle is **not** uploaded over an existing credential:
rotate the corresponding file credential deliberately in the folder's Credentials
page, then create and review a new plan. Keep inventory and bundle versions paired.
Do not delete ownership markers to force adoption. The lower-level
`seed-platform.py --help` supports already prepared private files and trusted HTTPS
or a manually established loopback SSH tunnel. Public CA paths require validation
on the host before using that lower-level procedure.

Required plugin versions tested on Jenkins 2.568.3 are recorded in
[`platform-plugins.json`](../../infra/jenkins/platform-plugins.json).
`--install-plugins` installs missing plugins using those pins and Jenkins dependency
resolution; it preserves installed versions. Setup also calls the Jenkins
Declarative validator. Install **Lockable Resources before publishing the updated
application Jenkinsfile**, since its rollout stage declares a lock. Missing/inactive
plugins or Declarative validation errors stop setup. Existing versions and the
running core are not compared with the recorded test versions.
The pin list is not a complete controller rebuild or dependency lockfile.

## Credentials and trust

| Folder file credential | Content |
| --- | --- |
| `host-inventory` | Registered machine/operator and selected disk identity |
| `host-inputs` | Encrypted Ansible Vault input bundle |
| `host-vault-password` | Vault passphrase |
| `host-sudo-password` | Existing registered operator's sudo password |

Jenkins binds these files outside the checkout and supplies paths, never password
values, to Ansible. The setup API uses an in-memory authentication header and
refuses remote plaintext HTTP and redirected credential forwarding. Jenkins API
tokens work without disabling [CSRF protection](https://www.jenkins.io/doc/book/security/csrf-protection/).

Folder scope prevents ordinary credential lookup from sibling jobs; it does not
isolate malicious code running as the same Unix user. The project retains its
accepted shared-host CI model. Limit job configuration/replay, trusted `main`
changes and approvals to the owner. Jenkins administrators can override the
[input submitter restriction](https://www.jenkins.io/doc/pipeline/steps/pipeline-input-step/).
Never expose these credentials to PR job parameters or SCM references.

## Plan, apply and verify

Choose **Build with Parameters**:

- **plan** (default): checks the registered local host, runs real Ansible check
  mode, and archives `platform-plan.json` with revision, target identity and task
  outcomes. No infrastructure apply occurs.
- **apply**: makes a fresh plan in the same build, archives its report, and waits
  up to ten minutes for `whtan` to select **Apply reviewed plan**. Review changed
  tasks and the target/revision before approving. Main must still point to the
  same commit. The runner checks input fingerprints and receipt age, reruns the
  plan under the host lock, and rejects a different change digest. It consumes
  the receipt before mutation, applies, then verifies. A failed or interrupted
  apply needs a fresh build and approval.
- **verify**: checks the installed host foundation and data contract without
  applying changes, then archives the verification report.

The callback emits task names/statuses and an opaque change digest. The digest
binds task outcomes and available module diffs, including loop-item diffs; it is
not a complete snapshot of host state. Secret (`no_log`) tasks contribute only
outcomes, and modules without diffs cannot bind unseen changes such as exact
package-resolution results. Final failure follows Ansible's recap, so ignored or
rescued failures remain visible without incorrectly failing a successful run.
If Ansible fails, the console identifies failed/unreachable task names from this
same sanitized callback, without module errors, raw output or secret values.
If execution stops before the callback writes a report, the console retains a
generic failure and directs the operator to private interactive diagnostics.
Reports publish an application-specific HMAC fingerprint in place of the raw
machine ID, following [systemd's guidance](https://www.freedesktop.org/software/systemd/man/latest/machine-id.html).
They do not publish raw diffs, decrypted inputs, module output, or credentials. This is a
bounded task-level preview; use the interactive bootstrap on a trusted terminal
when detailed diagnostics are needed. A successful verify covers current host
roles, not complete service/application reconstruction. The receipt expires after
30 minutes and lives outside the checkout; it is removed with private build state.

The Jenkins [Lockable Resources](https://www.jenkins.io/doc/pipeline/steps/lockable-resources/)
resource `battlefield-production-platform` covers the host job's plan/review/apply
and the application's whole production rollout, including smoke tests and
recovery. Separate jobs can queue while approval is pending.

A second, OS-level lock at the registered operator's
`~/.local/state/battlefield/bootstrap.lock` coordinates workstation Ansible applies,
the host runner, and individual production deploy/tunnel/rollback commands.
The Ansible child inherits the held lock descriptor or the pipe that keeps the
workstation's lock proxy alive. A forced controller/process-group stop therefore
retains the lock while that child runs; normal cancellation stops its process
group before releasing the lock. The Jenkins agent must run as the registered operator, so its home resolves the
same lock as workstation applies. This lock is scoped to that host/operator. An operator
CLI apply can start between separate application pipeline commands; use the
Jenkins job for coordinated operations, and do not run external bootstrap applies
during application delivery. Commands from another user or direct `kubectl`
operations are outside this lock contract.

## Acceptance and recovery

Local tests cover lock contention, startup cancellation and controller crashes,
revision and plan-drift rejection (including loops),
secret-free real Ansible reports, and safe setup. Disposable Jenkins validation
covers pipeline compilation and repeated job/credential creation without starting
builds. Before marking the host slice complete, record live evidence in AB-131:
first plan, approved apply, verify, repeat apply, main/platform contention, rejected
approval, cancellation, and absent/wrong data mount. AB-134 needs its own live
storage-adoption evidence. Keep the tickets in progress until those checks pass.

On failure, keep the failed build visible and inspect the sanitized task summary.
A `Gathering Facts (unreachable)` failure can indicate a sudo password-handshake
problem. Host inspection selects traditional sudo (`/usr/bin/sudo.ws` on hosts
whose default is sudo-rs); do not disable password authentication or replace the
system-wide sudo alternative to work around the pinned Ansible prompt matcher. Run
`server.sh inspect`, then the appropriate interactive plan/verify command for
private diagnostics. Do not replay a consumed receipt or force a different disk
identity. If an installation was interrupted, rerun the installer: owned objects
and existing credentials are retained. If Jenkins is unavailable, use the
workstation [bootstrap entry point](server-bootstrap.md) and external controller
maintenance procedure; the host job does not repair its own controller.
