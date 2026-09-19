# Player invitations

Admin → Members manages ordinary Players without a deployment per invitation.
An administrator grants an exact email, shares the game URL, and the player asks
Cloudflare for a one-time email code. The application does not send invitation
emails. Admin/Developer identities and their MFA policies remain operator-managed.

## One-time activation

1. Deploy the reviewed migration and API/frontend changes through the normal
   pipeline, leaving `BATTLEFIELD_MANAGED_PLAYERS=false` initially. Keep the
   production database backup, including the three new membership tables.
2. In Cloudflare Zero Trust → Integrations → Identity providers, add **One-time
   PIN**. Enable it alongside the existing identity provider on the Access
   application covering the entire `play.ordinalfrontier.com` hostname. Disable
   automatic redirection to Google if it prevents choosing the email-code method.
3. Create a dedicated Access group named **Battlefield managed Players**. Start
   with **Include → Everyone** and **Exclude → Everyone** (deny all). The app
   owns this group's complete contents, including its name, include/exclude rules,
   and empty require list; do not share it with other apps or edit it manually.
4. Add an **Allow** policy to the game: **Include → Access groups → Battlefield
   managed Players**, **Require → Login Methods → One-time PIN**. Login Methods
   must be a Require rule, not an additional Include rule. Keep Admin/Developer
   MFA and CI service policies separate. Remove any older Player-specific Allow
   rules during activation so this group is the sole Player admission path.
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

   Keep `BATTLEFIELD_ACCESS_ROLES` with the existing Admin/Developer assignments.
   Its Player emails are imported **once** at first managed startup. Imported
   Players wait for successful synchronization before becoming active. Later
   changes to the configured Player list do not change database membership.
7. Run `./scripts/ops/prepare-limited-production.sh` on the production host, then
   the normal reviewed main delivery with **APPLY_CONFIGURATION=true**. This
   stages the settings in `battlefield-members` and restarts application pods.
   Only the API consumes that Secret; migrations, engine, MCP and agent do not.
   Do not manage invitations until every API replica finishes this rollout:
   older replicas still use static role assignments.
8. Verify the live checks below before considering activation complete. The
   existing tunnel, JWT issuer/audience, exact-origin checks and MFA stay in place.

Cloudflare references: [OTP setup](https://developers.cloudflare.com/cloudflare-one/integrations/identity-providers/one-time-pin/),
[policy rules](https://developers.cloudflare.com/cloudflare-one/access-controls/policies/common-policies/),
[group API and required permission](https://developers.cloudflare.com/api/resources/zero_trust/subresources/access/subresources/groups/methods/update/).

## Everyday use

The Members link appears only when the authenticated Admin session reports managed
Players enabled. Open **Admin → Members**, enter an email and select **Grant access**. Once the row
says **Ready to play · Cloudflare synchronized**, copy and share the game link.
The invitee requests a code when visiting; no password or application signup is
required. Only Players can be managed here. Repeating an unchanged grant or
removal is harmless and does not generate duplicate membership/audit records.

**Remove access** denies subsequent game API requests as soon as the database
change commits, including requests with a previously valid JWT. Cloudflare group
removal happens asynchronously. Its existing browser session may still load the
frontend until expiry, but cannot use the game API. Requests already authorized
may finish; accepted battles and generation jobs continue. Ownership and game
history are retained. Reinviting the same email waits for fresh synchronization;
ownership still follows the verified Cloudflare `issuer|subject`, not the email.

Admin/Developer emails cannot be invited, removed, or assigned a role here. On
every managed API startup while the feature is enabled, any prior active Player
invitation for a configured
Admin/Developer is permanently revoked and audited before readiness. Removing
that privileged role later does not restore its old Player access; a new explicit
invitation is required. Role changes performed entirely while managed Players are
disabled cannot be inferred later; reconcile invitations before restoring Player
admission. Before
promoting a former Player through operator configuration, remove their invitation
and wait for Cloudflare synchronization, then configure the privileged MFA policy
and role. Do not give privileged identities the email-code-only Player policy.

## Synchronization and recovery

Each API checks for work every two seconds. PostgreSQL permits only one replica
to write the group at a time. Complete desired membership is sent with a ten-second
HTTP deadline and a fifteen-second reconciliation deadline. Concurrent membership
edits invalidate the old transaction so a stale response cannot activate a newer
grant. Successful synchronization is rechecked every minute to repair drift.

Failures persist a generic status and exponential retry delay from five seconds
to five minutes. The page polls every five seconds and offers **Retry
synchronization**. A saved invitation is not a successful Cloudflare update.
Removed Players remain denied during outages; new Players remain pending.

For errors, check the configured account/group IDs, token expiry/permissions, and
API network access to `https://api.cloudflare.com`. API logs emit
`player_members_sync_failed` with a sanitized HTTP status or transport/database/timeout
category, without credentials or Cloudflare response bodies. Expected concurrent-edit
serialization retries use debug-level `player_members_sync_retry`.
Admin/Developer authentication remains available independently of the membership
lookup. Player lookup failures deny access; they never fall back to static roles.
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

- An invited email receives an OTP and reaches the game with Player permissions.
- An uninvited email cannot enter; unsigned/invalid API credentials remain denied.
- Removal blocks the next API request from an already authenticated player, and
  synchronization removes that email from the dedicated group.
- Reinvitation waits for synchronization and survives an API restart.
- Admin/Developer MFA still challenges as configured; the Player policy cannot
  admit either privileged identity. CI service authentication remains separate.
- Secrets are absent from the frontend, migration/worker environments, and logs.

Local database and HTTP-double tests verify failure/race behavior; these live
checks require the configured Cloudflare account and are separate rollout evidence.
