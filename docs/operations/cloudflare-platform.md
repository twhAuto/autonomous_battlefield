# Cloudflare capture and reviewed lifecycle

AB-141 adds a separate Ansible workflow for the existing locally managed tunnel,
proxied application CNAME and self-hosted Access application/policies. It preserves
the current account, zone, audience, issuer, connector secret and service identities
when adopting. Application delivery continues to consume its existing production
profile; merging this code never provisions Cloudflare resources.

The API operations use Cloudflare's documented [local tunnel creation](https://developers.cloudflare.com/api/resources/zero_trust/subresources/tunnels/subresources/cloudflared/methods/create/),
[application policies](https://developers.cloudflare.com/api/resources/zero_trust/subresources/access/subresources/applications/subresources/policies/methods/list/)
and [reusable policies](https://developers.cloudflare.com/api/resources/zero_trust/subresources/access/subresources/policies/) contracts.
Creation copies the captured policy definitions into new reusable policies, then
creates the Access application with those policy links. It verifies Access before
creating the tunnel and publishing DNS. The workflow never starts a connector.

## Prerequisites and ownership

Use the Linux/WSL operator controller that holds the registered
`~/.config/autonomous-battlefield/server/inventory.json` and strict SSH access to
the Ubuntu host. Python, OpenSSH and the pinned Ansible tools installed by
`./scripts/infra/server.sh tools` are required. `--local` is available when the
controller is that registered Ubuntu host. Cloudflare operations need no sudo.

Keep the scoped API token at
`~/.config/autonomous-battlefield/bootstrap/cloudflare-api-token`, mode `0600`.
Use Account Cloudflare Tunnel Edit, Access Apps and Policies Edit,
Access Organizations/Identity Providers/Groups Read (Edit also works), Access
Service Tokens Read (Edit also works), and Zone DNS Edit/Zone Read restricted to
the intended account and zone. An API token cannot recover an existing connector
secret or an issued Access service-token secret. Capture requires the retained
production profile and connector JSON on the registered host.

Account creation, domain/registrar ownership, external identity-provider setup and
interactive sign-in remain manual prerequisites. Existing referenced identity
providers, groups and service tokens must still exist for fresh creation. Their
IDs and service client identities are preserved; the workflow neither creates nor
rotates them. Player-group membership remains owned by the application, is checked
for unsafe broad selectors and is included in stale-plan observations, but is
never overwritten from a capture. Group membership may be empty, denying entry.

The supported contract is deliberately narrow: one exact hostname, local tunnel,
proxied CNAME, explicit email/group human rules and explicit service-token rules.
Bypass policies, wildcard/additional application domains, nested/broad groups,
unknown security fields and conflicting resources stop the operation. Existing
App Launcher and unrelated resources are outside ownership. Configuration drift
is refused; this is not a general Access policy editor or credential rotator.

## Capture and adopt the current production configuration

Capture is read-only at Cloudflare and on the host. It compares the deployed
hostname, issuer, audience and tunnel with the live API, then encrypts the versioned
configuration, policy definitions and connector credentials. No token or policy
membership is printed. Use a new Vault path for each capture; old inputs remain.

```bash
private_dir="$HOME/.config/autonomous-battlefield/server"
python3 scripts/infra/cloudflare.py capture \
  --zone ordinalfrontier.com \
  --vault "$private_dir/cloudflare-inputs.vault"
```

For saved password automation, add `--vault-password-file` pointing to a private
non-executable file outside Git. Otherwise Ansible prompts through the terminal.
Never paste tokens, passwords or decrypted Vault contents into a command or chat.

After promotion, use a clean checkout of the exact fetched `origin/main`. Plan,
apply and verify reject feature/PR revisions and dirty checkouts. They validate the
registered host identity, serialize controller state, and hold the existing host
operation lock used by platform/deployment operations.

```bash
git fetch origin
revision="$(git rev-parse origin/main)"
# Run from a clean checkout whose HEAD equals this revision.
python3 scripts/infra/cloudflare.py plan --revision "$revision" \
  --vault "$private_dir/cloudflare-inputs.vault" \
  --report "$private_dir/cloudflare-plan.json"
```

Review the report's hostname, revision, mode, identity/observation fingerprints and
`changes`. First adoption should show only `record_ownership`: it makes no
Cloudflare API writes and leaves the existing production profile untouched. A
plan expires after 30 minutes. Changed inputs, host identity, observed policies,
referenced group membership or cloud identities invalidate the approval.

```bash
python3 scripts/infra/cloudflare.py apply --revision "$revision" \
  --vault "$private_dir/cloudflare-inputs.vault" \
  --review "$private_dir/cloudflare-plan.json" \
  --report "$private_dir/cloudflare-applied.json"
python3 scripts/infra/cloudflare.py verify --revision "$revision" \
  --vault "$private_dir/cloudflare-inputs.vault" \
  --report "$private_dir/cloudflare-verified.json"
python3 scripts/infra/cloudflare.py plan --revision "$revision" \
  --vault "$private_dir/cloudflare-inputs.vault" \
  --report "$private_dir/cloudflare-repeat-plan.json"
```

Verification and the repeat plan must report no changes. Each report path must be
new. Apply consumes its review before mutation; a failed operation needs a fresh
plan. Reports contain no memberships, connector secret or service client identity.
This adoption plus the scoped lifecycle evidence is required before AB-141 is Done.

## Fresh creation and delivery handoff

Reuse the captured policy intent and existing external identity references. Choose
an unused exact hostname in the same zone and a unique name. To rebuild deleted
resources at the original hostname, that hostname and the corresponding names must
actually be absent; this command never replaces retained resources.

```bash
python3 scripts/infra/cloudflare.py prepare-create \
  --source-vault "$private_dir/cloudflare-inputs.vault" \
  --hostname fresh.ordinalfrontier.com --name ordinalfrontier-fresh \
  --vault "$private_dir/cloudflare-fresh.vault"
```

Run and review plan/apply/verify with the new Vault using the commands above and
fresh report paths. Creation allocates new resource IDs, an Access audience and a
new connector secret; it does not reuse the old tunnel identity. Policies are
created first, the app is linked and verified, and DNS is published last. A matching
name without a recorded ID is not sufficient ownership, including after timeouts.

Canonical operator state is under
`~/.local/state/battlefield/cloudflare/<identity-fingerprint>/` on the controller:

- `owner.json`: private retained resource IDs, input binding and connector secret.
- `review.json`: one expiring approval bound to source, inputs, host and observation.
- `tunnel-credentials.json`: connector credentials in the existing delivery format.
- `handoff.json`: application issuer/audience/public-origin and deployment tunnel
  settings, with the credential file path. These are inputs to the existing
  production-profile setup, not an automatic traffic cutover.

All files are private to the operator and must stay outside Git, artifacts and
shared backups. Keep this controller state with its encrypted capture for recovery;
changing controllers does not move it automatically. For fresh delivery, consume
the handoff values through the existing [production profile](environments.md) and
[limited launch](limited-launch.md) procedures, retaining the other application
secrets, roles and pinned cloudflared image. The existing renderer enforces Access
at the connector, validates the API audience and uses a final 404 catch-all. The
new workflow does not modify Kubernetes, `.env`, Jenkins credentials or live routing.

## Failure and recovery boundaries

Successful API mutations are journaled by ID. A definitive HTTP 4xx rejection
retains confirmed prior steps; rerun and review a new plan to resume. No completed
step or secret is recreated. Connection loss, malformed successful responses and
other ambiguous POST results retain `pending` and block further mutation. Inspect
the private journal and Cloudflare audit/resource state before an operator-directed
recovery; do not delete state or retry a name match to force progress. Never delete
production resources merely to satisfy a fresh-create plan.

There is no automatic destructive rollback, policy overwrite, DNS retargeting,
account migration or credential rotation. Keep the saved Vault, API token and
controller state private. Scheduled/off-host backup policy remains deferred.

## Verification

`python3 scripts/test/test-cloudflare.py` exercises synthetic API lifecycle,
pagination, failed/ambiguous requests, unsafe selectors, ownership drift, review
expiry/replay, private-file rules and secret-free reporting. It runs in the full
`./scripts/test/test-deployment.sh` infrastructure suite with Ansible syntax checks.

The optional live fixture uses a random `battlefield-ab141-test-*` hostname and
resource names in the captured zone. It creates a protected application and idle
tunnel, verifies repeat apply and explicit adoption, then deletes only its recorded
IDs after rechecking configuration. It never runs a connector, invokes a model,
changes current memberships or cuts over production traffic. It compares the
original resources before/after and retains private cleanup evidence. Ambiguous
results or unexpected drift stop cleanup for investigation.

```bash
python3 scripts/infra/rehearse-cloudflare.py \
  --vault "$private_dir/cloudflare-inputs.vault" \
  --evidence-dir "$HOME/.local/state/autonomous-battlefield/evidence/cloudflare-new"
```
