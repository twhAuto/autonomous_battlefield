# Player invitations

Admin → Members manages Players and Testers without a deployment per invitation.
An administrator grants an exact email with a role, shares the game URL, and the
member asks Cloudflare for a one-time email code. The application does not send
invitation emails. Admins and their MFA policy remain operator-managed, as do
Testers assigned in `BATTLEFIELD_ACCESS_ROLES`.

## Enable managed Players

1. Start with `BATTLEFIELD_MANAGED_PLAYERS=false` on a deployed release. Keep the
   production database backup, which includes the membership tables.
2. In Cloudflare Zero Trust → Integrations → Identity providers, add **One-time
   PIN**. Enable it alongside the existing identity provider on the Access
   application covering the entire `play.ordinalfrontier.com` hostname. Disable
   automatic redirection to Google if it prevents choosing the email-code method.
3. Create a dedicated Access group named **Battlefield managed Players**. Start
   with **Include → Everyone** and **Exclude → Everyone** (deny all). The app
   owns this group's complete contents, including its name, include/exclude rules,
   and empty require list; do not share it with other apps or edit it manually.
4. Add an **Allow** policy to the game: **Include → Access groups → Battlefield
   managed Players**, **Require → Login Methods → One-time PIN**, and
   **Multi-factor authentication → Disable MFA** so the account's global MFA
   setting does not apply. Login Methods must be a Require rule, not an additional
   Include rule. Keep the Admin MFA
   policy, any policy for configured Testers and the CI service policy separate.
   Remove any older Player-specific Allow rules so this group is the sole
   admission path for managed members. These are manual Access
   policy changes; see
   [policy changes and the Cloudflare workflow](access.md#policy-changes-and-the-cloudflare-workflow).
5. Create a private API token with **Access: Organizations, Identity Providers,
   and Groups Write**, scoped to the relevant account. Cloudflare does not scope
   this permission to a single group. The application restricts its requests to
   the configured group; protect and rotate the token accordingly.
6. Add these literal values to the private production `.env`:

   ```dotenv
   BATTLEFIELD_MANAGED_PLAYERS=true
   BATTLEFIELD_MEMBERS_ACCOUNT_ID=<32-character-account-id>
   BATTLEFIELD_MEMBERS_GROUP_ID=<dedicated-group-uuid>
   BATTLEFIELD_MEMBERS_API_TOKEN=<private-api-token>
   ```

   Enabling managed Players requires `BATTLEFIELD_AUTH_MODE=cloudflare`, a
   32-character lowercase hexadecimal account ID, a lowercase group UUID, and a
   nonblank, single-line API token. Invalid settings fail API startup.

   Keep `BATTLEFIELD_ACCESS_ROLES` with the existing Admin/Tester assignments.
   Its Player emails are imported **once** at first managed startup. Imported
   Players wait for successful synchronization before becoming active. Later
   changes to the configured Player list do not change database membership.
7. Run `./scripts/ops/prepare-limited-production.sh` on the production host, then
   the normal reviewed main delivery with **APPLY_CONFIGURATION=true**. This
   stages the settings in `battlefield-members` and restarts application pods.
   Only the API consumes that Secret; migrations, engine, AI gateway and agent do not.
   Do not manage invitations until every API replica finishes this rollout:
   older replicas still use static role assignments.
8. Verify the live checks below before considering activation complete. The
   existing tunnel, JWT issuer/audience, exact-origin checks and MFA stay in place.

Cloudflare references: [OTP setup](https://developers.cloudflare.com/cloudflare-one/integrations/identity-providers/one-time-pin/),
[policy rules](https://developers.cloudflare.com/cloudflare-one/access-controls/policies/common-policies/),
[group API and required permission](https://developers.cloudflare.com/api/resources/zero_trust/subresources/access/subresources/groups/methods/update/).

## Everyday use

The Members link appears only when the authenticated Admin session reports managed
Players enabled. Open **Admin → Members**, enter an email, choose **Player** or
**Tester** and select **Grant access**. Once the row says **Ready to play ·
Cloudflare synchronized**, copy and share the game link. The invitee requests a
code when visiting; no password or application signup is required. Repeating an
unchanged grant or removal is harmless and does not generate duplicate
membership/audit records.

Managed Testers sign in with the same one-time code, without MFA, and receive the
Tester [permissions](../architecture/api.md#roles-and-ownership) and
[battle allowance](../architecture/api.md#admission-limits). Changing an active
member's role in its row applies to the member's next API request without a
Cloudflare update, because every managed member belongs to the same group. A
removed member keeps its role, and **Grant access** on its row restores it, until
the member [expires](../architecture/database.md#expiry) and leaves the list.
Granting that email afterwards is a new invitation, as a Player unless you choose
Tester.

**Remove access** denies subsequent game API requests as soon as the database
change commits, including requests with a previously valid JWT. Cloudflare group
removal happens asynchronously. Its existing browser session may still load the
frontend until expiry, but cannot use the game API. Requests already authorized
may finish; accepted battles and generation jobs continue. Ownership and game
history are retained. Reinviting the same email waits for fresh synchronization;
ownership still follows the verified Cloudflare `issuer|subject`, not the email.

Emails assigned Admin or Tester in `BATTLEFIELD_ACCESS_ROLES` cannot be invited,
removed or given a role here. On every managed API startup while the feature is
enabled, any prior active invitation for such an email is permanently revoked and
audited before readiness. Removing that configured role later does not restore the
old invitation; a new explicit invitation is required. Role changes performed
entirely while managed Players are disabled cannot be inferred later; reconcile
invitations before restoring member admission. To make a member a Tester, change
its role here. To make one an Admin, remove the invitation and wait for Cloudflare
synchronization, then configure the Admin MFA policy and role. Do not give an Admin
the email-code-only member policy.

## Synchronization and recovery

Each API checks for work every two seconds. PostgreSQL permits only one replica
to write the group at a time. Complete desired membership is sent with a ten-second
HTTP deadline and a fifteen-second reconciliation deadline. Concurrent membership
edits invalidate the old transaction so a stale response cannot activate a newer
grant. Successful synchronization is rechecked every minute to repair drift.

Failures persist a generic status and exponential retry delay from five seconds
to five minutes. The page polls every five seconds and offers **Retry
synchronization**. A saved invitation is not a successful Cloudflare update.
Removed members remain denied during outages; new members remain pending. Role
changes of active members do not wait for synchronization.

For errors, check the configured account/group IDs, token expiry/permissions, and
API network access to `https://api.cloudflare.com`. API logs emit
`player_members_sync_failed` with a sanitized HTTP status or transport/database/timeout
category, without credentials or Cloudflare response bodies. Expected concurrent-edit
serialization retries use debug-level `player_members_sync_retry`. A purge of expired
members logs `player_members_purged` with a count, or `player_members_purge_failed`
with a fixed reason, never an email; a failed purge retries an hour later.
Configured Admin and Tester authentication remains available independently of
the membership lookup. Member lookup failures deny access; they never fall back to
static roles.
Managed configuration, including imported Player email syntax/length, is validated
before opening the API database pool; invalid configuration fails startup with a
setting-level error and does not log an address. Domains must use dotted DNS names,
not address literals. Restarting recovers pending work and never repeats the initial import or deletes
removed-member records. A request timeout may have updated Cloudflare remotely;
the next attempt repairs the complete list before activating pending members.

Rotate the token in private `.env`, run configuration validation and the normal
configuration delivery, then verify synchronization. No token belongs in Git,
the browser, command arguments, or normal logs.

Do not turn managed Players off or roll back to an older static-role API as a
revocation recovery shortcut: doing so restores the old configured Player list.
Keep managed mode on and repair the token/database/group instead. If a rollback
is unavoidable, first reconcile the static Player list and Access policy against
current removals and keep Player access closed during the transition. Retain the
membership and audit tables; a down migration destroys the removal history.

No account-wide user revocation is performed: Cloudflare's
[user revocation API](https://developers.cloudflare.com/api/resources/zero_trust/subresources/organizations/methods/revoke_users/)
signs that user out of every Access application in the account. The game uses
its own immediate API denial and dedicated group removal instead.

## Live acceptance

- An invited email receives an OTP and reaches the game with its role's
  permissions, Player or Tester; a role change applies to its next request.
- An uninvited email cannot enter; unsigned/invalid API credentials remain denied.
- Removal blocks the next API request from an already authenticated player, and
  synchronization removes that email from the dedicated group.
- Reinvitation waits for synchronization and survives an API restart.
- Admin MFA still challenges as configured; the member policy cannot admit a
  configured Admin or Tester. CI service authentication remains separate.
- Secrets are absent from the frontend, migration/worker environments, and logs.

Local database and HTTP-double tests verify failure/race behavior; these live
checks require the configured Cloudflare account and are separate rollout evidence.
