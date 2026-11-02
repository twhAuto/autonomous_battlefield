# Production access

Production serves **play.ordinalfrontier.com** behind Cloudflare Access (team
**ordinalfrontier-lab**) through a named Cloudflare Tunnel. Admin roles, and any
Tester roles assigned in configuration, are private operator configuration;
optional managed Player and Tester invitations live in PostgreSQL. No email list is
committed to Git.

This runbook owns the access requirements and checks, the production tunnel,
roles and removal, AI limits and the stop switch, the CI service identity, and
access recovery. Related guides own the rest:

- [Cloudflare platform](cloudflare-platform.md): capturing, adopting or creating
  the tunnel, DNS record, Access application and policies.
- [Player invitations](player-invitations.md): managed Player and Tester membership.
- [CLI worker verification](cli-worker-verification.md#isolation-gate): the isolation gate that
  must pass before any CLI profile is advertised.

These procedures describe the implemented path; they do not prove that a live
tunnel, firewall or VM is configured correctly.

## Exposure requirements

The [Cloudflare workflow](cloudflare-platform.md) owns the tunnel, the proxied
CNAME and the Access application and policies. Account, domain and identity-provider
setup stay manual. Whatever creates or changes them, the configuration must keep
these properties:

- The Access application covers the **entire hostname**, with a six-hour
  application session. Policies inherit that duration. The privileged MFA challenge
  lasts one hour, separately.
- Google is the identity provider. Allow policies list exact emails or the managed
  member group. Never add a domain-wide allow, Bypass, Everyone, or a policy that
  allows all service tokens.
- Admin identities require MFA from an IdP and Access policy that actually enforce
  it. Test the challenge: a normal Google login alone is not evidence of MFA.
  Testers need no MFA: managed Testers sign in with the managed group's one-time
  code, and a Tester assigned in configuration uses its own exact-email policy.
- Only the tunnel's outbound connection exposes the application. Remove public
  gateway or ingress routes and router port forwards to the origin, and block
  inbound direct access on **both IPv4 and IPv6**, including forwarded container
  ports. SSH stays restricted to the operator's private management network. The AI
  gateway, databases, Kubernetes, Jenkins, SonarQube and monitoring stay private.

Every tunnel ingress rule requires Access token validation for its hostname's
application: the game's two rules, and the two for the optional
[guest hostname](#guest-access). The API independently
validates the JWT signature, RS256 algorithm, issuer, application audience,
expiry, issue time and subject or email claims. Missing keys or unknown roles deny
access; there is no password, cookie or unsigned-header fallback.

### Network policies

The rendered NetworkPolicies let the tunnel reach the frontend and API, and the
frontend reach the API. The API policy also admits same-namespace Prometheus pods
labelled `app.kubernetes.io/name=prometheus`,
`app.kubernetes.io/component=observability` and
`app.kubernetes.io/part-of=autonomous-battlefield` on TCP 8080 for scraping.
NetworkPolicy works at the port level, so application authentication still
protects `/v1` on that listener; the frontend policy has no Prometheus allowance.
If API scrape targets go unhealthy after a policy change, fix the policy:
increasing the smoke timeout cannot fix blocked ingress.

Production deployments reconcile these policies before the application rollout.
For a policy-only change, run `./scripts/cd/reconcile-network-policies.sh`; it reads
no tunnel credentials and does not restart the connector. Policy-only source
changes select the `network-policy` component, so no images are rebuilt.

**Do not assume Kind's default networking enforces these policies.** Use a CNI with
verified NetworkPolicy enforcement.

### Verify exposure

1. From the production server checkout, run
   `./scripts/ops/prepare-limited-production.sh`. It checks the registered cluster
   UID, the shared application schema, Cloudflare roles, the tunnel credential's
   UUID and permissions, and the pinned image. It renders into a temporary
   directory, changes nothing, and never obtains login tokens.
2. From an unrelated test pod, prove that frontend and API ingress is blocked.
3. From a separate internet connection, try the server's public IPv4 and IPv6
   addresses directly on every published port, and record that each connection
   fails. A DNS-only change or an Access login screen is not proof that the origin
   is private.
4. Test the public hostname with allowed and denied Google identities, MFA,
   six-hour session expiry, logout and account removal. Use real browser sessions
   for human roles; never copy browser JWTs into scripts or Jenkins.

Automated production smoke checks use the [CI service identity](#ci-service-identity).

## Production profile and tunnel activation

The production profile lives in `~/.config/autonomous-battlefield/production/` on
the registered host; see [environments](environments.md) for its registration.

1. Put the Access issuer, audience, public origin and role assignments in `.env`.
   The Cloudflare workflow's `handoff.json` supplies these values for a newly
   created application.
2. Put `BF_TUNNEL_ID`, `BF_TUNNEL_IMAGE` (a verified, digest-pinned
   `cloudflare/cloudflared` image) and
   `BF_TUNNEL_CREDENTIALS_FILE=tunnel-credentials.json` in `deployment.env`, and
   install the tunnel's credentials JSON beside them with mode `0600`. These are
   tunnel-specific credentials, not an account-wide API token.
3. Configure independent worker and MCP capability secrets in `.env`. Keep provider
   and CLI availability false until each is intentionally enabled and verified.
4. Run `./scripts/ops/prepare-limited-production.sh`, then deliver through the
   normal `main` pipeline. Configuration changes need no database reset.
5. To activate or update the tunnel, run `./scripts/cd/deploy-tunnel.sh`. It
   renders manifests in private temporary storage and applies the tunnel Secret and
   resources. Configuration and credential checksums roll the connector only when
   its inputs change.

## Tunnel operations

Start or stop both connectors:

```bash
kubectl -n battlefield scale deployment/battlefield-tunnel --replicas=2
kubectl -n battlefield scale deployment/battlefield-tunnel --replicas=0
```

After stopping all connectors, check that the public hostname is unavailable;
Access and DNS must never fall back to a direct origin. When startup fails, inspect
connector readiness and logs privately without printing credentials.

For a tunnel credential incident, stop the connectors, rotate or replace the tunnel
credentials through Cloudflare's supported flow, update only the tunnel Secret
from the new private credentials file, and restart the deployment. Then revoke the
old credential and check that the old connector can no longer connect.

Rotating the worker control secret requires updating the AI gateway and every
worker; rotating the capability-signing secret requires only the AI gateway and
invalidates active jobs.

To retire the deployment, stop the connectors and the worker first, then remove the
DNS route, the Access application and its policies, and the tunnel resources and
Secret. Keep database backups and unrelated private services, and never delete a
shared domain, identity provider or other shared infrastructure.

## Roles, sessions and removal

The persistent identity is the verified `issuer|subject`. Each request maps the
verified, normalized email to exactly one role. With managed Players enabled,
Admin and configured Tester roles come from configuration, and every other
identity's eligibility and role, Player or Tester, come from the database on every
request; new grants require confirmed Cloudflare synchronization, and role changes
apply at once. Without managed Players, every role comes from configuration.

- All roles share gameplay and faction reads.
- Editing or deleting factions and stopping, pausing or resuming battles require
  ownership or Admin. Rows created before ownership existed belong to
  `legacy-admin`, which only Admin can manage. Owners never come from client JSON.
- Testers can inspect diagnostics and use enabled CLI profiles; the role grants
  no host or deployment access.
- Players receive gameplay-only responses; the
  [API roles](../architecture/api.md#roles-and-ownership) list what they can read.
- [Guests](#guest-access), when a guest Access application is configured, receive
  the same responses and can change nothing.

In Cloudflare mode the application issues no auth cookie and ignores old ones.
Logout sends the browser to `/cdn-cgi/access/logout`, and expired API sessions
prompt reauthentication. Mutations must carry the configured HTTPS `Origin` and an
`application/json` body. Private worker routes use their own bearer authentication.

### Guest access

Guests sign in at a separate hostname, such as `guest.ordinalfrontier.com`, through
their own Access application that admits anyone with a one-time PIN. The game's
application keeps its exact-email and managed-group policies, and the API maps
every token from the guest application to the view-only Guest role, whatever its
email. The guest application and its DNS record are manual Cloudflare changes,
outside the [Cloudflare workflow](cloudflare-platform.md), which owns only the
game's application and hostname.

Each guest takes a Zero Trust seat on their first Access sign-in, from the same
pool as Players and configured identities (the Free plan has 50). A seat is released
only when the user is removed, so in Zero Trust **Settings → Admin controls →
Remove inactive users from seats**, choose an inactivity period (one month to one
year); a daily check then frees the seats of guests who stopped visiting. See
[seat management](https://developers.cloudflare.com/cloudflare-one/team-and-resources/users/seat-management/).

To enable guests:

1. In Zero Trust, enable the **One-time PIN** identity provider if it is not
   already on, then add a self-hosted Access application for the entire guest
   hostname with only that provider. Give it one **Allow** policy: **Include →
   Everyone**, **Require → Login Methods → One-time PIN**, and **Multi-factor
   authentication → Disable MFA**. Without that override, the account's global MFA
   setting asks every guest to enrol an authenticator after the code. Add no other
   policy.
2. Add a proxied DNS `CNAME` for the guest hostname pointing to
   `<BF_TUNNEL_ID>.cfargotunnel.com`, the same tunnel as the game.
3. Put the guest application's AUD tag and origin in the production `.env`:

   ```dotenv
   BATTLEFIELD_ACCESS_GUEST_AUDIENCE=<64-character AUD tag>
   BATTLEFIELD_GUEST_ORIGIN=https://guest.ordinalfrontier.com
   ```

   Both are set together and must differ from the game's audience and origin.
4. Run `./scripts/ops/prepare-limited-production.sh`, the normal `main` delivery
   with **APPLY_CONFIGURATION=true**, then `./scripts/cd/deploy-tunnel.sh`, which
   renders the guest hostname's routes with the guest audience.
5. Verify from a private window: any email can sign in at the guest hostname with a
   code and sees the **Guest access · View only** banner; a request that changes
   anything, such as `POST /v1/battles`, returns `403`; signing in at the game's
   hostname is unchanged.

To turn guests off, remove both settings and repeat step 4, then delete the guest
Access application and its DNS record.

### Model access and role preview

**Admin → Models & access** switches a whole provider or a single model profile on
or off and sets which roles may use it; **Save changes** persists the policy.
Provider switches preserve individual profile choices. A profile still needs
deployment configuration, credentials and a working runtime; adding a model variant
is a deployment configuration task.

- Players see only available profiles explicitly allowed for Players. CLI profiles
  are off for Players by default; check **Player** on the profile and save to
  grant it. An empty role selection grants access to nobody.
- Without an admin override, the profile's configured `allowedRoles` is the default
  (for example `[player, tester, admin]`). Overrides take precedence; new
  profiles inherit their defaults, and removed profiles disappear on refresh.
- Background generation checks the same eligibility.
- Changes apply to new battles and background-generation requests; existing battles
  and accepted jobs keep their configuration.
- Settings are shared across API replicas and survive restarts; back up
  `model_access_policy` with the database. HTTP 409 means another administrator
  saved first: discard local edits, refresh and reapply. Failed reads and saves stay
  visible.

To halt AI execution, use the [stop switch](#limits-and-the-stop-switch), not these
toggles.

**View as Player**, **View as Tester** or **View as Guest** opens a read-only
preview with that role's actual API visibility and inspection permissions. Save or
discard edits first. The banner stays visible while you navigate, and **Exit
preview** restores the administrator view. Preview uses your own identity and ownership, impersonates
nobody and grants nothing to non-admin callers.

### Remove an identity

- **Managed Player or Tester:** use **Admin → Members** as described in
  [Player invitations](player-invitations.md); new API requests are blocked
  immediately.
- **Configured role:** remove the Access Allow rule (a
  [manual policy change](#policy-changes-and-the-cloudflare-workflow)), revoke the
  identity's Access sessions, remove its role assignment, and roll out the updated
  auth configuration to **every API replica**. Offline JWT checks cannot detect
  Access revocation, so check that access is denied after the rollout and allow
  for Cloudflare's revocation propagation.

Never fall back to anonymous or password mode during an authentication incident.

## Limits and the stop switch

PostgreSQL serializes admission across replicas:

- Players and Testers have separate daily and unfinished battle allowances and
  share a global cap of unfinished battles; the
  [admission limits](../architecture/api.md#admission-limits) list the defaults,
  which operators change in `runtime_policy`. Admin battles bypass all three limits,
  so an Admin can exceed the global total, but they still count as unfinished when
  other identities' battles are admitted, so a busy Admin can delay Player and
  Tester battles. Queued, running and paused battles are all unfinished.
- A retry with the same battle ID, scenario snapshot, ticks and owner returns the
  existing battle; different inputs conflict.
- Background generation allows 10 jobs per identity per UTC day, including for
  Admin, with a cooldown, queue and execution timeouts, and idempotency keys.
  Expired queued jobs and failed executions both count.
- At most four CLI executions run at once across decisions and background jobs
  by default (`runtime_policy.cli_concurrency`, 1–8). A slot is released when the worker
  reports completion or its bounded lease expires, not on submission. Each worker
  also caps its own jobs with `ai.agentConcurrency` (1 when omitted). The K3s API
  worker sets 4; the managed CLI VM currently omits it and runs one job at a time
  (see [CLI worker operations](cli-worker.md)).
- Each battle allows at most 100 model decision executions across retries, then
  uses the normal fallback. API token and step limits, the 4 MiB CLI output cap and
  execution timeouts still apply. Subscription CLI token counts are reported when
  available; they are not monetary caps.

Workers use the database lease deadline rather than the time a claim arrives, so
keep host and VM clocks synchronized.

Set `BATTLEFIELD_POSTGRES_POD=battlefield-postgres-0` and `DEPLOY_ENV` to `dev` or
`production`, and run the commands below. With `DEPLOY_ENV` set, the script uses
that environment's registered kubeconfig and context and refuses a cluster whose
identity does not match ([environments](environments.md)). Like rollback, it skips
the platform-readiness checks, so `stop` still works while an unrelated controller
is unhealthy. Without `DEPLOY_ENV`, it uses the shell's current kubectl context.

```bash
scripts/ops/ai-policy.sh status
scripts/ops/ai-policy.sh stop
scripts/ops/ai-policy.sh usage
# Only after diagnosing the cause:
scripts/ops/ai-policy.sh start
# Match the CLI workers' combined slots (1–8):
scripts/ops/ai-policy.sh cli-slots 4
```

`stop` blocks new background-generation admissions and new model claims, and new
model decision windows fall back immediately. Workers poll the private policy
endpoint every second (two-second HTTP timeout) and cancel running work when policy
stops or a check fails; on Linux that kills the CLI process group, and stopping the
VM worker's systemd unit kills its control group. Previously accepted plans can
still be simulated. `usage` reads `friend_ai_usage` by identity and profile; it
records reported usage, not an exact bill. `cli-slots` sets
`runtime_policy.cli_concurrency`, which the league scheduler plans with: set it to
the CLI workers' combined `ai.agentConcurrency`, since a higher value starts league
battles whose decisions then wait for a slot toward the decision timeout. Lowering it
blocks new claims until capacity frees; it does not cancel active jobs.

## CI service identity

Jenkins authenticates to production with a Cloudflare Access service token stored
as the Secret file credential `battlefield-production-access-service-token`. It is
used for credential preflight, deployment smoke and k6 smoke load. The token grants
no Player, Tester or Admin role. The API validates the signed Access application
JWT (RS256, fixed issuer and audience, expiry, issue time and signing keys),
requires its `common_name` to equal `BATTLEFIELD_ACCESS_CI_CLIENT_ID`, and requires
an empty user subject and email. The resulting `ci` actor can only `GET`
`/v1/auth/session` and `/v1/scenarios`. With the setting empty, no CI identity is
enrolled.

### Set up the service token

1. In Cloudflare Zero Trust, open **Access controls → Service credentials →
   Service Tokens** and create `jenkins-production-smoke` with a managed duration,
   such as one year. Save the Client ID, Client Secret and actual expiry privately.
2. In the Access application for `play.ordinalfrontier.com`, add a **Service Auth**
   policy that includes only this token. Keep the existing user policies and MFA;
   never use Bypass or allow all service tokens. This is a manual policy change;
   see [policy changes and the Cloudflare workflow](#policy-changes-and-the-cloudflare-workflow).
3. Add `BATTLEFIELD_ACCESS_CI_CLIENT_ID=<client-id>` to the production `.env`. The
   Client Secret stays only in Jenkins, never in `.env` or Kubernetes.
4. Create a private file (`chmod 600`) with the actual values and the Cloudflare
   expiry in UTC. `origin` binds the credential to its destination: scripts refuse a
   different target and refuse redirects when sending credentials.

   ```json
   {
     "client_id": "<client-id>",
     "client_secret": "<client-secret>",
     "origin": "https://play.ordinalfrontier.com",
     "expires_at": "<YYYY-MM-DDTHH:MM:SSZ>"
   }
   ```

5. In **Manage Jenkins → Credentials**, create or update the Secret file credential
   `battlefield-production-access-service-token` with that file, then delete the
   local copy.
6. Run `./scripts/ops/prepare-limited-production.sh`, then run `main` with
   **APPLY_CONFIGURATION=true** so the client ID reaches `battlefield-auth` and the
   API restarts. Jenkins validates the file against `.env` before rollout, so a
   format, identity or expiry problem stops it early; a Cloudflare policy or
   deployed-configuration mismatch fails the authenticated smoke check.

Smoke checks keep readiness and missing- or forged-identity checks on private
origin port forwards. Session, frontend liveness and scenario reads go through the
public hostname with service credentials on each request, and the checks also
require that admin access, battle reads and logout are denied. k6 mounts the
credential file read-only, sends both Access service headers on every HTTPS
request, and disables redirects. Secrets never appear in command arguments, Docker
environment values, test reports or archived artifacts, and responses and
credentials are not logged. Keep shell tracing and k6 HTTP debug logging off with
production credentials.

### Expiry and rotation

Every preflight, smoke and k6 run checks `expires_at`: it warns within 30 days and
fails when less than an hour remains. That field is your copy of Cloudflare's
expiry, not proof the token hasn't been revoked; the public smoke checks verify
actual access.

Enable Cloudflare's **Expiring Access Service Token** notification so expiry is
reported even when Jenkins is idle, and schedule a quarterly review to rotate the
secret before it expires. The repository does not create either, and Jenkins has
no Cloudflare management privileges.

- **Rotate the secret** (same Client ID): use Cloudflare's rotation with a grace
  period, update the Jenkins JSON (and `expires_at` if the token was renewed), run
  a `main` verification, then retire the old secret. No application change is
  needed.
- **Replace the token:** update the Service Auth policy (a manual policy change; see
  below), change the client ID in `.env`, update the Jenkins file, and run `main`
  with **APPLY_CONFIGURATION=true**.

### Policy changes and the Cloudflare workflow

The [Cloudflare workflow](cloudflare-platform.md) is not a policy editor or
credential rotator. Once it has adopted the application, it records the captured
inputs and the referenced service-token identities, and its plan, apply and verify
refuse to run when either differs. Adding or replacing a Service Auth policy or
token by hand therefore puts the workflow in that state: a new capture produces
different inputs and fails with *Owned configuration or physical host changed;
explicit recovery required*. The repository has no supported procedure yet for
updating the recorded ownership after an intentional policy change.

Before a manual policy change, record it in the ticket, and expect the workflow
to need an operator-directed ownership recovery afterwards. Never delete the
controller state or retry a name match to force it through. Rotating the secret of
an existing token changes neither the policy nor its identity.

References: [service tokens, rotation and expiry notifications](https://developers.cloudflare.com/cloudflare-one/access-controls/service-credentials/service-tokens/),
[service-token application JWT claims](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/application-token/).

### Diagnose an origin 401

Repeat the production session smoke request and search the API logs in the
registered production context:

```bash
kubectl --context "$KUBERNETES_CONTEXT" -n battlefield logs \
  -l app.kubernetes.io/component=api -c api --since=10m --prefix=true \
  --tail=200 | rg 'auth_failure'
```

Match `request_id` to the response's `X-Request-ID`. No `auth_failure` line means
no Cloudflare verifier rejection was seen for that request; it is not proof of
authentication. The response stays a generic 401, and no JWT, cookie, secret, claim
value or raw upstream error is logged.

| `auth_failure` | Check |
| --- | --- |
| `assertion_missing`, `assertion_duplicate`, `assertion_oversized`, `token_format_invalid`, `token_header_invalid` | Assertion forwarding |
| `jwks_tls_verification_failed`, `jwks_dns_failed`, `jwks_timeout`, `jwks_request_canceled`, `jwks_transport_failed` | The API's trust store, DNS and outbound connection (redirects count as transport failures) |
| `jwks_http_<status>`, `jwks_json_invalid`, `jwks_keys_invalid`, `jwks_request_invalid` | The fixed issuer's certs endpoint and configuration |
| `jwks_kid_unknown`, `jwks_refresh_throttled` | Signing-key rotation. Refreshes happen at most once every 30 seconds, and a failed refresh keeps its failure code for that interval |
| `signature_invalid`, `claims_json_invalid` | Token integrity and format. `aud` may be a string or an array of strings; both are matched exactly |
| `issuer_mismatch`, `audience_mismatch`, `token_expired`, `issued_at_missing_or_invalid`, `issued_at_in_future`, `token_not_yet_valid`, `token_type_invalid` | Configuration and clocks, without sharing JWTs |
| `service_identity_not_configured`, `service_client_id_mismatch`, `service_token_type_invalid`, `service_user_identity_present`, `user_identity_or_role_invalid` | Service enrollment or human role assignments |

API containers have a read-only root filesystem; do not copy diagnostic binaries
into `/tmp` or weaken that setting.

### Browser Integrity Check exception

Cloudflare's Browser Integrity Check blocks the production API clients with error
1010 before Access or the application can authenticate them. In the zone's
Configuration Rules, set **Browser Integrity Check: Off** for this expression only:

```text
(http.host eq "play.ordinalfrontier.com" and
 (starts_with(http.request.uri.path, "/v1/") or http.request.uri.path eq "/livez"))
```

This exception authorizes nothing: Access, the Service Auth policy and application
permissions still apply. Never disable the check zone-wide or add a Bypass policy.
Record dashboard changes, then rerun the authenticated smoke checks. See
[Browser Integrity Check](https://developers.cloudflare.com/waf/tools/browser-integrity-check/).

## Recovery

If authentication fails, keep the application unavailable while you repair the
Access configuration. Rollbacks must keep production in Cloudflare mode with the
current role assignments, and production rollback also requires
`BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST` to match the exact verified backend
digest. Never roll back to a binary that predates Access. Automatic image rollback
depends on the private recovery record that Jenkins writes after a verified
delivery; see [Jenkins production delivery](jenkins.md#production-delivery).

Never restore a database over live writes. Stop the workloads, restore into a
separate database, validate it, and switch deliberately. Keep unverified CLI
profiles disabled throughout recovery.

References: [Access JWT validation](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/),
[Access sessions](https://developers.cloudflare.com/cloudflare-one/access-controls/access-settings/session-management/).
