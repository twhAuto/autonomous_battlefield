# Limited-access launch

The chosen application hostname is **play.ordinalfrontier.com**, under Access team
**ordinalfrontier-lab**. Start with a single administrator's Google identity. Application
privileged roles are private operator configuration; optional managed Player
invitations live in PostgreSQL. No email list is committed to Git. This
runbook describes the implemented deployment path; it does not certify that a
live tunnel, firewall, or VM has been provisioned.

Use [Owner-only Access](#owner-only-access) for initial protection,
[sessions and roles](#sessions-roles-and-removal) for access changes,
[CI service authentication](#ci-service-authentication) for delivery credentials,
and [validation and recovery](#validation-and-recovery) for acceptance checks.
CLI enablement has a separate [isolation gate](#isolated-cli-vm-enable-only-in-the-last-phase).

## Local development

Local development uses `./scripts/cd/deploy-local.sh` and the project-root `.env`.
The deployer reads literal values and renders separate database, auth, and AI
Kubernetes Secrets. It never sources `.env` as executable shell. The registered
kubeconfig identifies the dev cluster; production uses separate private settings.

The deployment sets `BATTLEFIELD_ENV=dev`; in `.env`, explicitly choose `BATTLEFIELD_AUTH_MODE=local-password`
with `BATTLEFIELD_ADMIN_USERNAME`, a strong `BATTLEFIELD_ADMIN_PASSWORD` (12+
characters), and random `BATTLEFIELD_SESSION_SECRET` (32+ bytes). If retaining the
legacy `BATTLEFIELD_AUTH_ENABLED` variable, it must agree with the selected mode.
To deliberately skip login on localhost, use `disabled-development` and remove
or set the legacy flag to `false`. Neither mode is accepted in production.

The setup command generates two independent random secrets for
`BATTLEFIELD_MCP_CAPABILITY_SECRET` and `BATTLEFIELD_AGENT_SECRET`. The MCP process
needs both, each at least 32 bytes, and refuses to start otherwise; workers need only the latter. The local Kubernetes CLI launcher reads
the worker credential from `battlefield-ai` and keeps its bridge on loopback.
Local CLI login stays on the workstation. Do not use that launcher to serve public
CLI jobs from your personal home directory.

## Owner-only Access

1. Create a named Cloudflare Tunnel for the application. Route DNS for
   `play.ordinalfrontier.com` to that tunnel. Create a self-hosted Access application
   covering the **entire hostname**, with a six-hour application session.
   The privileged MFA challenge
   duration remains one hour; it is separate from the application session.
   Keep policy session durations inherited from the application.
2. Configure Google as the identity provider. Add an Allow policy listing the
   administrator's exact email. Do not add a domain-wide allow, Bypass, Everyone, or public
   service-token policy. Require MFA for Admin/Developer identities using an IdP
   and Access policy that actually enforces it. Test the MFA challenge; a normal
   Google login alone is not evidence of MFA. For the initial owner-only phase,
   applying MFA to everyone is appropriate.
3. Record the Access application AUD tag, the team name, tunnel UUID, and a verified
   digest-pinned `cloudflare/cloudflared` image. Store tunnel credentials outside Git.
   The local configuration uses the tunnel's credentials JSON, not an account-wide
   API token. Obtain the tunnel-specific credentials through Cloudflare's supported
   tunnel creation flow.
4. Put Access issuer, audience, public origin and role assignments in production
   `.env`. Put `BF_TUNNEL_ID`, `BF_TUNNEL_IMAGE` (digest-pinned), and
   `BF_TUNNEL_CREDENTIALS_FILE=tunnel-credentials.json` in its `deployment.env`.
   Install the application's tunnel credentials beside those files (mode 600).
5. Configure independent worker and MCP capability credentials in `.env`. Keep provider and
   CLI availability false until intentionally enabled and verified.
6. Validate with `./scripts/ops/prepare-limited-production.sh`, then use the normal
   verified application pipeline. Production database migration backups remain
   under the configured backup storage; changing configuration needs no database reset.
7. When ready to activate public access, run `./scripts/cd/deploy-tunnel.sh`.
   It generates manifests in private temporary storage, applies the tunnel Secret
   and resources. Configuration/credential checksums roll the connector only when
   its inputs change. No persistent generated files are needed.

   Both tunnel ingress rules require Access token validation. The API independently
   validates JWT signature, RS256 algorithm, issuer, application audience, expiry,
   issue time, and subject/email claims. Missing keys or unknown roles deny access;
   there is no password, cookie, or unsigned-header fallback.
8. Remove public gateway/ingress routes and router port forwards to the origin.
   Only the tunnel's outbound connection should expose the application. Block
   inbound direct access on **both IPv4 and IPv6**, including forwarded container
   ports. Keep SSH restricted to the operator's private management network.
   The AI gateway, databases, Kubernetes, Jenkins, SonarQube and monitoring stay private.

The rendered NetworkPolicies allow the tunnel to reach frontend/API and frontend
to reach API. The API policy also permits same-namespace Prometheus pods matching
`app.kubernetes.io/name=prometheus`, `app.kubernetes.io/component=observability`,
and `app.kubernetes.io/part-of=autonomous-battlefield` to reach TCP 8080 for scraping.
NetworkPolicy operates at the port level, so application authentication still
protects `/v1` routes on that listener. No Prometheus allowance is added to the
frontend policy. After deploying this policy change, verify API scrape targets
are healthy in Prometheus; increasing the smoke timeout cannot fix blocked ingress.
Production deployments reconcile these NetworkPolicies before application rollout.
For a policy-only update, run `./scripts/cd/reconcile-network-policies.sh`; it reads
no tunnel credentials and does not activate or restart the connector. Policy-only
source changes select `network-policy`, avoiding application image rebuilds.
`./scripts/cd/deploy-tunnel.sh` remains the explicit tunnel activation/update command.
**Kind's default networking must not be assumed to enforce these
policies.** Use a CNI with verified NetworkPolicy enforcement before enabling this
public deployment. From an unrelated test pod, prove frontend/API ingress is
blocked. From a separate internet connection, try the server's public IPv4 and
IPv6 directly on every published port. Record failure to connect; a DNS-only
change or an Access login screen is not proof that the origin is private.

## Repeatable production preparation

From the production server checkout, run:

```bash
./scripts/ops/prepare-limited-production.sh
```

The command checks the registered cluster UID, shared application schema,
Cloudflare roles, tunnel credential UUID and permissions, and pinned image.
It renders into a temporary directory and removes that directory on exit.
It does not edit configuration, deploy workloads, create DNS, or activate a tunnel.

This is preparation, not launch certification. NetworkPolicy enforcement, direct
origin isolation over IPv4/IPv6, and MFA still require live checks. Automated
production checks use a dedicated CI service identity; see
[CI service authentication](#ci-service-authentication). The preparation script
never obtains or mints login tokens.


## Tunnel startup, shutdown and credential rotation

Start or stop both connectors with `kubectl -n battlefield scale deployment/battlefield-tunnel --replicas=2`
or `--replicas=0`. Verify the public hostname becomes unavailable after stopping
all connectors; Access/DNS must never fall back to a direct origin. Inspect connector
readiness and logs privately when startup fails, without printing credentials.

For a tunnel credential incident, stop its connectors, rotate or replace the
named tunnel credentials using Cloudflare's supported management flow, update only
the tunnel Secret from the new private credentials file, and restart its deployment.
Revoke the old credential and verify the old connector can no longer establish a
connection. Rotating worker credentials requires updating both the AI gateway and all workers;
rotating capability-signing material requires only the AI gateway and invalidates
active jobs.

To retire the deployment, stop the connectors and worker first, remove the
application DNS route and Access application/Allow policies, and delete the dedicated
tunnel resources and Secret. Keep database backups and unrelated private services.
Do not delete a shared domain, identity provider, or shared infrastructure.

## Model access and role preview

Apply the embedded `00012_model_access.sql` migration through the normal migration
Job before rolling out the new API and frontend. Standalone deployments can run
`go run ./cmd/server -migrate` from `backend` with their existing configuration.

Open **Admin → Models & access** to switch a whole provider or one configured
model profile on/off and set its allowed roles. Click **Save changes** to persist
the policy. Provider switches preserve individual profile choices. A profile still
needs deployment-enabled configuration, credentials, and a working runtime; adding
new model variants remains a deployment configuration task. To grant or revoke
Player access to a CLI subscription, check or uncheck **Player** on that profile
and save. CLI access is off for Players by default. An empty role selection grants
access to nobody.

Settings are shared across API replicas and survive restarts. HTTP 409 indicates
another administrator saved first: discard the local edits, refresh, and reapply
the intended changes. Settings read/save failures remain visible; the application
does not claim an unsuccessful save worked. Back up `model_access_policy` with the
application database. Overrides take precedence over configured role defaults;
new profiles inherit their configured defaults, while removed profiles disappear
from the settings page on refresh.

Changes affect new battles and background-generation requests. Existing battles
and accepted jobs keep their configuration. To halt AI execution, use the runtime
stop procedure below rather than the model access switches.

**View as Player** or **View as Developer** opens a read-only app preview with the
selected role's actual API visibility and inspection permissions. Save or discard
edits before entering it. The banner stays visible while navigating; **Exit preview**
restores the administrator view. Preview uses your own identity and ownership and
does not impersonate another user. It grants no privileges to non-admin callers.
Player invitations are available separately at **Admin → Members** when managed
Players are enabled. See [Player invitations](player-invitations.md) for setup,
removal and recovery. Admin/Developer assignments remain operator-managed.

## Sessions, roles and removal

The persistent identity is verified `issuer|subject`. Each request maps the
verified normalized email to exactly one role. With managed Players enabled,
Admin/Developer roles remain configured and Player eligibility comes from the
database on every request; new grants require confirmed Cloudflare synchronization.
Without managed Players, all roles use the configured assignments. All roles share gameplay
and faction reads. Editing/deletion and battle stop/pause/resume require ownership
or Admin. Existing factions and battles become `legacy-admin`, manageable only by
Admin. Owners never come from client JSON. Developers can inspect diagnostics and
use enabled CLI profiles; this role grants no host or deployment access.

Players receive explicit gameplay responses and cannot fetch decision history or
saved context. Their model catalog includes only available profiles explicitly
allowed for Players. To enable an API or CLI profile for Players, configure its
credentials/model and runtime availability,
then select Player in the profile’s allowed roles under Admin → Models & access
and save. Without an admin policy override, configured `allowedRoles` supplies the
default (for example `[player, developer, admin]`).
Unlisted roles cannot select it. Background generation checks the same eligibility;
its CLI profile is unavailable to Players unless explicitly allowed. Background-generation
allowances also apply to Admin; battle allowances do not (see [limits](#limits-and-the-stop-switch)).

Cloudflare mode issues no application auth cookie and ignores old application
cookies. Logout returns the documented `/cdn-cgi/access/logout` destination and
the browser navigates there. Expired API sessions prompt reauthentication. Mutation
requests must carry the configured HTTPS Origin; missing/cross-origin values are
rejected. Bodies must use `application/json`. Private worker routes use their own
bearer authentication and do not use browser cookies.

For a managed Player, use Admin → Members as described in the
[invitation runbook](player-invitations.md); it blocks new API requests immediately.
For a static-role identity: remove their Access Allow rule, revoke existing Access sessions,
remove their application role assignment, and roll out the updated auth configuration
to **every API replica**. Offline JWT signature checks do not detect Access revocation.
Check denied access after rollout and allow for Cloudflare's documented revocation
propagation. Do not restore anonymous/password mode during an authentication incident.

## Limits and the stop switch

PostgreSQL serializes admission across replicas: 20 battles per identity per UTC
day, 2 unfinished per identity, 4 unfinished globally. Admin battles skip these
battle limits but count toward the global unfinished total, so a busy Admin can
still delay Player battles. Queued and running rows,
including paused rows, are unfinished. A same-ID retry with identical scenario
snapshot/ticks and owner returns the existing battle. Different inputs conflict.
Background generations retain 10/day, cooldown, queue/execution timeout and their
existing idempotency key. Expired queued jobs and failed executions both consume that day’s allowance.

A shared database limit admits up to four CLI executions across decisions and
background jobs (`runtime_policy.cli_concurrency`, range 1–4). Workers separately
limit their active jobs with `ai.agentConcurrency` (1 when omitted; the K3s API worker and CLI VM template set 4).
Submission alone does not release a slot: the worker must report
completion, or the bounded execution lease must expire. Claims never wait in a
worker's local execution queue. Workers use the database lease deadline rather than
claim response arrival time; synchronize host/VM clocks and test delayed claims. Battles allow at most 100 model decision execution
attempts across retries, then use the existing fallback. API token and step limits,
CLI output capture (4 MiB), and execution timeouts remain enforced; subscription
CLI internal token counts are reported when available, not guaranteed monetary caps.

Use the existing private kubectl context and set `BATTLEFIELD_POSTGRES_POD` to
`battlefield-postgres-0`, then run:

```bash
scripts/ops/ai-policy.sh status
scripts/ops/ai-policy.sh stop
scripts/ops/ai-policy.sh usage
# Only after diagnosing the cause:
scripts/ops/ai-policy.sh start
```

`stop` prevents new generation admission and new model claims. New model windows
immediately fall back. Workers poll the private policy endpoint every second and
cancel execution on stop or failed policy checks (HTTP timeout two seconds). Linux
CLI cancellation kills the process group. Stopping the VM worker systemd unit also
kills its entire control group. Previously accepted plans can still be simulated.
Usage is queryable by identity/profile via `friend_ai_usage`; these figures bound
work and record reported usage, not an exact financial bill.

## Isolated CLI VM: enable only in the last phase

Use Ubuntu's KVM/libvirt on the server. Docker containers share the host kernel;
the public CLI worker belongs in a dedicated VM. Start with 2 vCPU, 4 GiB memory,
24 GiB disk. Verify virtualization support, free memory/disk, and host headroom
while the game, database, observability, and one CLI execute together. This is
a one-slot starting size, not validated four-CLI capacity. Before selecting four
slots, test four processes together and increase guest/service limits if measured
headroom is insufficient. Leave CLI disabled if the host cannot sustain this.

Use [CLI VM provisioning and adoption](cli-vm-platform.md) to create or adopt
the isolated VM, then [CLI worker bootstrap](cli-bootstrap.md) to install or adopt
its host and guest services. These reviewed workflows own image verification,
capacity checks, the isolated network/NIC filter and interruption recovery.
Neither workflow enables public CLI availability or provisions provider logins.
Keep retained disks, provider homes and the existing administration identity.

The VM has **no default route, NAT, DNS server, host mounts or Docker socket**.
`bf-agent0` is `192.168.234.1/24`; the guest is `192.168.234.2`. The external NIC
filter permits only guest connections to the host proxy at 3128 and AI gateway bridge at
18084, plus operator SSH from the host. IPv6 is dropped. The proxy resolves approved
provider/update names; direct guest DNS and direct internet traffic are unnecessary.

The service bootstrap installs dedicated Squid, Caddy, firewall and loopback
bridge configuration. Review the exact provider allowlist and verify actual login
and inference; never broadly permit HTTPS or remove the private-IP deny. The
proxy resolves approved names, and HTTPS package updates use it.

Caddy binds `192.168.234.1:18084` and forwards to the loopback-only kubectl bridge,
setting upstream Host to `127.0.0.1:8084`. The renderer defaults to port 28084,
separate from Jenkins smoke ports 18081–18085. Preserve the verified bridge/proxy
pair and the narrow firewall exception for smoke forwards on `127.0.0.1:18084`.
The kubeconfig and deployment credentials never enter the VM. Verify unauthenticated
bridge requests return 401, then verify a real accepted plan: authentication runs
before the SDK Host check, so 401 alone is insufficient. Obtain the verified worker
and version-matched CLI/helper using the
[pinned release inputs](agent-vm.md#pinned-release-inputs), then let the service
bootstrap install them. Check actual CLI versions before enabling profiles.

The service bootstrap installs `/etc/battlefield-agent/worker.env` as
**root:root 0400**, containing only
`BATTLEFIELD_AGENT_SECRET`, `BATTLEFIELD_ENV=production`, and the isolation flag
once verified. Never put the capability-signing secret, database URLs, API server
passwords, personal files, SSH keys, or deployment credentials in the worker's home.
Configure CLI login interactively as `battlefield-worker` inside the VM using its
dedicated home `/var/lib/battlefield-worker`. Linux workers disable dumpability to
block same-user subprocess reads of parent environment/memory; CLI children receive
an explicit allowlist of transport/login settings and their scoped capability.

For initial boundary checks, keep **all VM profiles disabled** and the
**API's CLI availability false**, then start the worker and execute:

```bash
# Inside the VM, as battlefield-worker; use the real worker PID and real private IPs:
python3 verify-agent-isolation.py --worker-pid "$WORKER_PID" \
  --blocked-host "$HOME_ROUTER_IP" --blocked-host "$ADMIN_SERVICE_IP"
```

Also test actual provider requests through the proxy, inspect the attached libvirt
filter, try private IPv6 destinations when present, and load-test a two-model-faction
battle with the single slot. One faction can fall back if both cannot finish before
the opening/window deadline; the battle must continue and the slot must never overlap.
Existing bounded deadlines deliberately take precedence over waiting indefinitely.
Test malicious faction text requesting extra tools, another request ID, files, and
network destinations. The server must reject unrelated MCP contexts and the VM
boundary must hold regardless of how the model responds.

After the initial boundary checks, a freshly bootstrapped managed worker remains
disabled until a reviewed update workflow is available. Do not hand-edit its
environment or profiles; see the [managed profile enablement boundary](cli-bootstrap.md#managed-profile-enablement-boundary).
For an existing enabled installation adopted with matching files, confirm its
previously recorded isolation evidence and complete the
[accepted-plan and launch checks](agent-vm.md#verify-before-advertising-availability).
Only after recording the required checks, set `BATTLEFIELD_CLI_ISOLATION_VERIFIED=true`
in the API's private AI configuration and enable the desired CLI availability flag.
Production startup rejects advertised CLI profiles without this flag. The flag is
an operator attestation, not an automatic proof of isolation. Invite Developers
only after these checks; invite Players earlier while CLI remains off.

## Validation and recovery

Run repository tests and the deployment artifact checks:

```bash
GOCACHE=/tmp/battlefield-go-cache scripts/test/with-test-postgres.sh scripts/test/test-go-coverage.sh
(cd frontend && npm test -- --coverage)
python3 scripts/test/test-limited-launch.py
scripts/test/test-kind-deployment.sh
scripts/test/test-security-launchers.sh
```

Production smoke keeps readiness and missing/forged-identity checks on private
origin port forwards. Authenticated session, frontend liveness and scenario reads
run through the public hostname with CI service credentials on each request.
Checks also require denial of admin access, battle reads and logout mutations.
Redirects are not followed and response bodies/credentials are not logged.
Separately test the public hostname with allowed and denied Google identities,
MFA, six-hour user-session expiry, logout, account removal, and direct-origin
IPv4/IPv6 probes. Use real
browser sessions for human-role checks; do not copy browser JWTs into scripts or
Jenkins. Keep the service-token credential and tunnel credentials, which serve
different purposes; the retired browser-JWT credential is covered in
[CI bootstrap](ci-bootstrap.md).

If authentication fails, keep the application unavailable while repairing the
Access configuration. Rollbacks must retain production Cloudflare mode and current
role assignments. Production rollback additionally requires
`BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST` to match the exact verified backend
digest. After successful deployment verification and runtime assurance, Jenkins
records cluster UID, both image digests and the verified revision in mode-600
`recovery.json` beside production `deployment.env`. Before the next rollout, it
loads approval only when the record matches that cluster and both current images.
Missing or mismatched records disable automatic rollback; the first rollout with
this feature requires operator recovery on failure. Corrupt records fail preflight.
Never seed a recovery record from unverified images or edit it in the repository.
Do not roll back to a pre-Access binary or restore a database over
live new writes. Stop workloads, restore into a separate database, validate, and
switch deliberately if database recovery is required. Keep unverified CLI profiles
disabled throughout recovery.

References: [Access JWT validation](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/),
[Access sessions](https://developers.cloudflare.com/cloudflare-one/access-controls/access-settings/session-management/),
[libvirt network filters](https://libvirt.org/formatnwfilter.html),
[Linux process dumpability](https://man7.org/linux/man-pages/man2/PR_SET_DUMPABLE.2const.html).

Production k6 smoke load uses the same `battlefield-production-access-service-token`
file credential as origin verification, bound separately for its stage. The launcher
requires Cloudflare mode, validates the file and mounts it read-only into k6; see
[CI service authentication](#ci-service-authentication). Password login remains
development-only.

The image-verification admission webhook allows 30 seconds per request for
registry signature and attestation retrieval (`webhookConfiguration.timeoutSeconds`).
Verification remains enforced with `failurePolicy: Fail`; this timeout does not
waive missing or invalid evidence.

## CI service authentication

Jenkins uses the Secret file credential `battlefield-production-access-service-token`
for credential preflight, deployment smoke, and k6 smoke load.
Service authentication does not grant a Player, Developer or Admin role. The API
validates the signed Access application JWT (RS256, fixed issuer and audience,
expiry, issuance time and signing keys), requires its `common_name` to exactly
match `BATTLEFIELD_ACCESS_CI_CLIENT_ID`, and requires an empty user subject/email.
The resulting `ci` actor can only GET `/v1/auth/session` and `/v1/scenarios`.
No CI identity is enrolled when the setting is empty. Human role assignments and
browser authentication remain separate.

### One-time activation

1. In Cloudflare Zero Trust, open **Access controls → Service credentials → Service
   Tokens** and create `jenkins-production-smoke`. Choose a managed duration, for
   example one year. Save the Client ID, Client Secret and actual expiry privately.
2. In the existing Access application for `play.ordinalfrontier.com`, add a policy
   with action **Service Auth**, including only this specific service token. Keep
   the existing invited-user policies and MFA requirements. Do not use Bypass or
   allow all service tokens. Use the existing application AUD and issuer.
3. On the production host, add this line to
   `~/.config/autonomous-battlefield/production/.env`, replacing the value with the
   actual Client ID: `BATTLEFIELD_ACCESS_CI_CLIENT_ID=<client-id>`. The Client
   Secret stays exclusively in Jenkins, not in the application `.env` or Kubernetes.
4. Create a private file (chmod 600) with the following JSON structure. Replace all
   placeholders and use the actual Cloudflare expiry, formatted in UTC. The
   `origin` binds this credential to its intended destination; scripts refuse a
   different target and refuse redirects when sending credentials.

   ```json
   {
     "client_id": "<client-id>",
     "client_secret": "<client-secret>",
     "origin": "https://play.ordinalfrontier.com",
     "expires_at": "<YYYY-MM-DDTHH:MM:SSZ>"
   }
   ```

5. In Jenkins **Manage Jenkins → Credentials**, create a **Secret file** credential
   with ID `battlefield-production-access-service-token` in the store accessible
   to this job. Upload that JSON file. Delete the local upload copy when finished.
6. Provision these settings before merging the CI service-authentication change.
   After the updated scripts are checked out on the production host, run the
   production environment validator and `./scripts/ops/prepare-limited-production.sh`.
   For the first delivery, run main with **APPLY_CONFIGURATION=true** so the new
   CI client ID reaches `battlefield-auth` and the application pods are restarted.
   Jenkins first validates the credential file against the registered `.env`,
   then deploys and verifies the live service identity. A format/identity/expiry
   failure stops before live rollout; a Cloudflare policy or deployed-config
   mismatch is detected by authenticated smoke verification.

k6 mounts the credential file read-only, sends both Access service headers on each
HTTPS request, and disables redirects. Client secrets are absent from command
arguments, environment values passed to Docker, and test reports. Keep shell
tracing and k6 HTTP debug logging disabled while using production credentials.
Jenkins controls file lifetime; credential files are never archived as artifacts.

### Expiry monitoring and rotation

Service credentials also expire. Each credential preflight, smoke and k6 run checks
`expires_at`, warns within 30 days, and fails if less than one hour remains. This
metadata is an operator-maintained copy of Cloudflare's expiry, not proof that the
remote credential has not been revoked. Public smoke checks verify actual access.

Enable Cloudflare's **Expiring Access Service Token** notification in the account
Notifications page so expiry is reported even when Jenkins is idle. Schedule a
quarterly credential review/secret rotation and renew the token before expiration.
These account notifications and calendar scheduling require operator setup; the
repository does not create them or grant Jenkins Cloudflare management privileges.

For rotation, use Cloudflare's service-token secret rotation with a grace period,
update the Jenkins JSON secret (and `expires_at` after any renewal), run a main
verification, then retire the previous secret after success. Rotating the secret
for the same Client ID requires no application config change. If replacing the
whole token, update the specific Service Auth policy and the `.env` client ID,
then run main with **APPLY_CONFIGURATION=true**. Keep human Admin role assignments
unchanged. Record the next review date and owner in your operational calendar.

References: [service tokens, rotation and expiry notifications](https://developers.cloudflare.com/cloudflare-one/access-controls/service-credentials/service-tokens/),
[service-token application JWT claims](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/application-token/).

### Diagnose an origin authentication 401

After deploying the authentication diagnostics, repeat the production session
smoke request and inspect API request logs in the registered production context:

```bash
kubectl --context "$KUBERNETES_CONTEXT" -n battlefield logs \
  -l app.kubernetes.io/component=api -c api --since=10m --prefix=true \
  --tail=200 | rg 'auth_failure'
```

Match `request_id` to the response's `X-Request-ID`. An empty `auth_failure`
means no Cloudflare verifier rejection was observed on that request; it is not
proof of authentication. The HTTP response deliberately remains a generic 401.
No JWT, cookie, client secret, claim value or raw upstream error is logged.

- `assertion_missing`, `assertion_duplicate`, `assertion_oversized`,
  `token_format_invalid`, `token_header_invalid`: inspect assertion forwarding.
- `jwks_tls_verification_failed`, `jwks_dns_failed`, `jwks_timeout`,
  `jwks_request_canceled`, `jwks_transport_failed`: inspect the API runtime's
  trust, DNS and outbound connection. The transport code also covers redirects.
- `jwks_http_<status>`, `jwks_json_invalid`, `jwks_keys_invalid`,
  `jwks_request_invalid`: inspect the fixed issuer's certs endpoint/configuration.
- `jwks_kid_unknown`, `jwks_refresh_throttled`: check signing-key rotation;
  refresh attempts remain limited to once per 30 seconds. A failed refresh keeps
  its specific failure code during that interval.
- `signature_invalid`, `claims_json_invalid`: inspect token integrity/format.
  JWT `aud` may be a single string or an array of strings; both are supported
  with exact audience matching. Older deployments accepted only arrays and
  rejected valid service tokens with a string audience as `claims_json_invalid`.
  Deploy the audience-parser fix for that case; no credential rotation or
  Cloudflare policy change is required. See [RFC 7519 section 4.1.3](https://www.rfc-editor.org/rfc/rfc7519.html#section-4.1.3).
- `issuer_mismatch`, `audience_mismatch`, `token_expired`,
  `issued_at_missing_or_invalid`, `issued_at_in_future`, `token_not_yet_valid`,
  `token_type_invalid`: compare configuration and clocks without sharing JWTs.
- `service_identity_not_configured`, `service_client_id_mismatch`,
  `service_token_type_invalid`, `service_user_identity_present`,
  `user_identity_or_role_invalid`: inspect service enrollment or human roles.

API containers have a read-only root filesystem; do not copy diagnostic binaries
into `/tmp` or weaken that setting. Use these server logs for verifier failures.

### Cloudflare Browser Integrity Check exception

Cloudflare's Browser Integrity Check blocks the production API clients with error
1010 before Access or the application can authenticate them. Under the zone's Configuration Rules,
set **Browser Integrity Check: Off** only for this expression:

```text
(http.host eq "play.ordinalfrontier.com" and
 (starts_with(http.request.uri.path, "/v1/") or http.request.uri.path eq "/livez"))
```

Leave Access and the specific Service Auth policy enabled. This exception does not
authorize requests; valid Access credentials and application permissions are still
required. Do not disable Browser Integrity Check across the zone or add an Access
Bypass policy. Record any dashboard changes, then rerun authenticated smoke checks.
See [Cloudflare's Browser Integrity Check documentation](https://developers.cloudflare.com/waf/tools/browser-integrity-check/).
