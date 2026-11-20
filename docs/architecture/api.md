# HTTP API reference

[`openapi.yaml`](openapi.yaml) is the wire contract: every `/v1` route, its
parameters, request and response fields, status codes, and the battle state and
event payloads. CI checks its route set against the routes the API serves. This
guide covers what a schema cannot express: who may call what, retry and admission
rules, and how battle data behaves over time. When the two disagree about a field,
the spec is meant to be right; fix whichever is wrong.

Paths are relative to the API origin: `http://battlefield.localhost:8888` through
the Kind gateway, or `http://localhost:8080` for a standalone API process (see
[local setup](../developer/local-setup.md)). Lifecycle and metrics endpoints are
public for infrastructure probes; everything under `/v1` follows the rules below.

## Limited-access authentication and permissions

| Mode | Identity for protected requests |
| --- | --- |
| `cloudflare` | Cloudflare Access JWT, verified by the API. The application login cookie is not accepted. Production uses this mode. |
| `local-password` | Signed application cookie from the [administrator login](#administrator-session). |
| `disabled-development` | Shared `local-development` identity for every client; never a production mode. |

`GET /v1/auth/session` is always public so a client can learn the mode; an
unauthenticated caller receives `401` with a session body rather than an error. For
an Admin, Tester or Player it also records the caller's current role and returns
their [display name](#display-names) as `actor.displayName` once one is set; it
returns `503` if that record cannot be read or written.
Unsafe browser requests must send the configured `Origin`, and nonempty bodies
must be `application/json`. Missing or invalid credentials return `401`; a missing
role or permission never grants access. See the
[production access runbook](../operations/access.md) for the production setup.

### Administrator session

In `local-password` mode, `POST /v1/auth/login` with the configured username and
password returns `204` and an HttpOnly, SameSite=Strict session cookie. Wrong
usernames and wrong passwords get identical responses. `POST /v1/auth/logout`
clears the cookie; expired or invalid sessions receive `401`. In `cloudflare` mode
login returns `404`, and logout returns a `logoutUrl` for the browser to navigate
to.

### Roles and ownership

Faction and battle responses carry a server-assigned `ownerId`. Updating or
deleting a faction, and stopping, pausing or resuming a battle, require ownership
or the Admin role. Players can read shared factions, battles, events, replays,
history, decision history, [leagues](#leagues) and [analytics](#analytics), but
receive gameplay-only responses: events and replay events without payloads,
battles without inspection fields but with each side's controller and league,
decisions with only their status and applied plan, and a reduced model-profile
list. Saved decision context requires Tester or Admin; only Admins create or cancel
a league or disqualify its entrants.

Guests only view. Every token from the guest Access application is a Guest,
whatever its email, and guests are off unless that application is configured.
Guests read the scenario catalog, factions, faction rulesets, battle history,
battles, faction views, events, replays, decision history, leagues and analytics,
with the Player projection. Every other route returns `403`, including model profiles, the battle
allowance, background generation and every mutation, and Guests own nothing they
can manage.

Selecting a model controller, for a battle or for background generation, requires
an enabled, verified profile and a role the
[model access policy](#model-access-administration) allows for both the profile and
the model. Players need explicit approval for each profile.

### Display names

`PUT /v1/profile` sets the caller's own display name. Players, Testers and Admins
may call it; Guests receive `403`. The name is trimmed, must be 3 to 24 letters,
digits, spaces, hyphens or underscores (`400` otherwise), and must not match
another person's name ignoring case (`409 conflict`); saving one's own name again in
another case succeeds. Names can be changed at any time. The
[frontend guide](../developer/frontend-guide.md#routes-and-session) describes when the
web app asks for one.

Factions from `GET /v1/factions`, battle history rows, and the battle returned by
`GET /v1/battles/{id}` and by stop, pause and resume carry `ownerName` for every
role, Guests included: `Admin` for anything an Admin owns, league battles and
`legacy-admin` rows included; otherwise the owner's display name, or
`Unnamed commander` before they choose one. The label follows the owner's role as
last recorded by their session, so an Admin's items say `Admin` whatever name they
hold. No response names another person by email. When names cannot be read
promptly, responses omit `ownerName` instead of waiting or failing.

### Admission limits

| Limit | Player | Tester |
| --- | --- | --- |
| Battles started per person per UTC day | 5 | 10 |
| Unfinished battles per person | 1 | 2 |
| Unfinished battles across the service | 4, shared | 4, shared |
| Background generations per person per UTC day | 10 | 10 |

Operators can change these defaults in `runtime_policy` and the background
generation configuration. Exceeding a limit returns `429`. A new battle is checked
against the allowances of the role starting it; the counts include all of that
person's battles, whatever role started them. Admin battles bypass all three battle
limits, including the service-wide one, but still count as unfinished when other
people's battles are admitted. Paused battles are unfinished. A
retry that reuses a request's identifier with the same inputs and identity
returns the work already admitted rather than consuming another allowance. League
battles are Admin battles and also skip the per-battle model decision budget.
Faction ownership has its own [Player limit](#factions).

`GET /v1/battles/allowance` reports the caller's battle counts and limits from the
same counts admission uses, without reserving anything, so a start can still
return `429` if another battle is admitted first.

Private worker APIs are outside this contract: they use a separate bearer secret
and server-minted request capabilities, described in the
[agent control plane](system-architecture.md#agent-control-plane).

## Errors

Every `/v1` error uses one envelope, whatever the status:

```json
{"error":{"code":"not_found","message":"battle not found","requestId":"4f0c…"}}
```

- `code` is stable. Branch on it, not on `message`, which may change.
- `requestId` matches the `X-Request-ID` response header and the server's
  `request_id` log field. It is omitted when no request identifier is in scope.

| Status | Code |
| --- | --- |
| 400 | `invalid_request` |
| 401 | `authentication_required` |
| 403 | `permission_denied` |
| 404 | `not_found` |
| 405 | `method_not_allowed`, with an `Allow` header |
| 409 | `conflict`, or `already_exists` for a reused battle or faction `id` |
| 415 | `unsupported_media_type` |
| 422 | `validation_failed` |
| 429 | `rate_limited` |
| 500 | `internal_error` |
| 501 | `not_implemented` |
| 503 | `service_unavailable` |

Unregistered `/v1` routes, and registered paths called with an unregistered
method, return `403 permission_denied`. The permission registry denies anything it
does not list, so a response never reveals whether a route exists. The three
`/v1/auth/*` routes sit outside that registry and return `405` for a wrong method.

Internal failures report a generic message and never the underlying cause. A
background generation `429` keeps its availability fields (`retryAt`,
`remaining`, and so on) beside the envelope. The unauthenticated session response
above is the one `/v1` non-success that is not an envelope.

## Lifecycle probes

| Route | Behavior |
| --- | --- |
| `GET /startupz` | `503` while dependencies and application state initialize, then `200`. Used by the startup probe. |
| `GET /livez` | `200` with `ok` while the HTTP process runs. `/healthz` is a compatibility alias. |
| `GET /readyz` | `200` with `ok` when PostgreSQL responds within one second, otherwise `503`. |

Failures use the error envelope, but probes rely on the status alone. These routes
and `/metrics` are outside the OpenAPI contract.

## Factions

The client chooses the faction `id`, so creation is safe to retry: the same `id`,
caller and trimmed content returns `201` again without another write, while a
different caller or content returns `409 already_exists`.

A Player can own one faction at a time. While they own any, creating one with a new
`id` returns `409 conflict` with the message `players can own only one faction at a
time`; deleting their faction lets them create another. The limit only blocks
creation: a Player who owns several factions, such as a Tester whose role became
Player, keeps them all but cannot create another until they own none. Testers and
Admins have no faction limit. Concurrent creates by one Player cannot both succeed;
the [database guide](database.md#admission-triggers) describes the lock.

Names, doctrines and backgrounds are trimmed before storage. Doctrine is limited to
4,000 and background to 1,000 Unicode characters, not UTF-8 bytes; a missing
background means empty.

The cosmetic `emblem` field accepts `star`, `crown`, `shield`, `swords`, `wolf`,
`raven`, `serpent`, `tree`, `leaf`, `flame`, `sun`, or `moon`. Omitted or empty
values default to `star` on creation and preserve the saved choice on update.
Unknown identifiers return `400`. Creation retries compare the normalized emblem
as well as the profile text. Registry responses include the saved identifier;
colors are derived from the faction name in the frontend.

Edits apply only to future battles. Deleting a faction removes it from the active
registry, but battle history stays readable because each battle stores an
immutable snapshot of its participants.

## Battles

### Queue a battle

Both factions must exist, and the scenario ID names a template in
`backend/gamedata/scenarios`; clients discover them through `GET /v1/scenarios`,
which also returns each scenario's authored `map` for layout previews.
Queueing snapshots each faction's name, background and doctrine, the map, the
ruleset, and the controller plans, so later faction edits do not change the
battle. Battle detail participants show these historical values, not the current
profile.

Malformed input returns `400`, and a missing scenario or faction `404`. A
well-formed request that cannot run returns `422 validation_failed`: an
incompatible faction, an invalid blueprint, or an unavailable model profile.

The client-supplied `id` makes retries safe. Repeating a request with the same
`id` returns `202` and the existing battle when the caller, resolved scenario and
`tickCount` all match; its `status` is then the battle's current status. Reusing
the `id` with different values returns `409`. If the existing battle cannot be
read to check a retry, the request fails with `500` rather than `409`, so a client
does not abandon an `id` that may already hold its battle.

Optional `tickDuration` (a Go duration from `100ms` to `10s`) and
`decisionIntervalTicks` (an integer from 1 to 100) override the map's timing for
this battle. Omitted or null values retain catalog defaults. Invalid overrides
return `400`; an explicit zero or empty duration is invalid. Both values are
frozen in the scenario snapshot and digest, so changed timing on a create retry
returns `409`; equivalent duration spellings such as `500ms` and `0.5s` match.
The interval may exceed the requested battle length, leaving only the opening
decision. The tick duration is a pacing target; AI decision waits can extend it.
Shorter intervals consume the existing per-battle `runtime_policy.decision_executions`
budget sooner (default 100). Claim admission serializes that budget, and new model
windows immediately fall back once it is spent; timing overrides do not bypass it.
Only [league](#leagues) battles are exempt.

Every `decisionIntervalTicks` the engine pauses for each faction's plan, so a
model-controlled battle can wait at a window; a late plan never replaces the
fallback the engine chose. See the
[strategy decision window](system-architecture.md#52-strategy-decision-window).

### Browse battle history

`GET /v1/battles` is a service-wide list for all roles. `paused` filters active
battles whose pause flag is set; the `queued` and `running` filters exclude them.
Rows are ordered newest first, and a cursor page is stable: newer battles do not
shift later pages, so restart without a cursor to see them. Status changes can
move a battle in or out of a status-filtered list between requests, but a battle's
`rulesVersion` is frozen with its scenario, so a version filter keeps the same
battles. `GET /v1/battles/rules-versions` lists the versions to filter by, as the
[OpenAPI contract](openapi.yaml) defines.

Summaries omit maps, states, events and detailed results; fetch those from the
battle routes. Only a completed battle with an outcome and no winner is a draw.

### Status and controls

`GET /v1/battles/{id}` returns the authored map and the current state, attempt and
result. Each faction's result totals include per-unit-type and per-building-type
figures and its upgrade purchase ticks; a result that did not record them omits
those lists. While a battle is queued or running, `production` gives the next point at
which factions spend gold; all purchases for a window happen at once and nothing
is queued between windows.

Pause, resume and stop are safe to retry: repeating one on a battle already in
that state returns `200` with the current battle. Controlling a completed or
failed battle, or pausing or resuming a stopped one, returns `409`. Pausing keeps
the current attempt. Stopping releases the engine lease and fails unresolved model
decisions, so a host subscription agent cannot keep working on the run.

### Battle state and visibility

`GET /v1/battles/{battleId}/factions/{factionId}/view` exposes the whole map and
all entities, actions, movement routes and deaths. It retains only the requesting
faction's economy/upgrade/production totals in `state.factions`. Opponent building
`production` settings are omitted, keeping future unit selections, on/off choices
and gold/reserve waits private. Paid training progress and all physical entities
remain visible. The full battle snapshot used by viewers retains both sides' settings.
Under gold-mine rulesets every view also carries `state.goldMines`, each mine's
`reserve` and `remaining` gold, for both factions. The deprecated
`visiblePositions` field contains every map coordinate for older clients.

Buildings expose `buildRange`, optional `trains` (supported unit IDs), `trainingQueue`,
and `trainingBlocked`. Core v7 and later also expose `production: {configured?, unitType?, enabled, waitingForGold?, waitingForUnitLimit?, waitingForUpgrade?}` on every training building. Selection persists; disabled sources finish paid training. Insufficient gold above the AI reserve, a faction at its ruleset unit cap, or a selected core v8 type whose unlock upgrade is not yet owned pauses starts until affordable, a unit slot frees or the unlock is bought. The queue contains at most one paid unit in continuous mode; frozen older battles retain up to twelve. Viewers can inspect production but cannot change it; no manual production endpoint exists. The current catalog has five constructible production kinds.
Each faction starts with only a castle, which trains the Barracks roster and exposes
`attack: 15` and `range: 3`. Resolved rules save `buildings.castleTrains`,
`castleAttack` and `castleRange`; omitted legacy fields retain older behavior. Current AI `productionOrders` explicitly identify each source and select its unit/on-off setting. Frozen legacy recruitment supports automatic source selection; incompatible requests are rejected by validation.
Destroyed buildings remain in snapshots for history/results but are hidden on the map.
Each queue item includes `unitType`, `trainingTicks`, `remainingTicks`,
`queuedAtTick` and optional `opening`. A blocked ready head has zero remaining
ticks and remains in place until a free adjacent traversable cell is available.
`unit_queued` charges for a purchase; `unit_recruited` records actual deployment.

Per-tick fields (`movementPath`, attack targets and outcomes, heal targets and
`unitDeaths`) describe only the current tick and clear at the next one. A unit
that disappears has not necessarily died; only `unitDeaths` and the
`unit_destroyed` event confirm a death. For a `position` attack,
`attackTargetPosition` is the impact hex and a miss carries no target ID; tracking
renderers can follow the visible `attackTargetId` instead. Older snapshots remain readable without a database migration. Replaying a scenario
uses current engine behavior; old content fields retain their saved values. The [game rules](../game/game-rules.md) and
[Frontier Siege](../game/frontier-siege.md) explain the mechanics behind these
fields.

### Battle events

`GET /v1/battles/{id}/events` pages through one attempt's events in sequence
order. Pass the last sequence you have seen as `after`, or set `latest=true` to
fetch the most recent `limit` events (still ascending); `latest` cannot be
combined with a nonzero `after`. The `BattleEvent` schema lists every event type
and its payload. Players receive the same events with an empty `data` object.

### Battle replay

`GET /v1/battles/{id}/replay?tick=N` returns one segment of a completed, stopped or
failed battle's final attempt: the stored checkpoint at or before `N` and every tick
up to the next decision window or the final tick. Each frame carries that tick's
full battle `state` (as in `GET /v1/battles/{id}`) and its events. A client plays a
battle by starting at `tick=0` and following `nextTick` until it is `null`; seeking
requests the segment that contains the target tick. Segments are computed on demand
and are not cached.

A queued or running battle, or one whose final attempt saved no checkpoint for the
segment returns
`409 conflict`. A tick beyond the final tick returns `400`. Players receive replay
events with an empty `data` object, as on the events route. The engine re-simulates
ticks between checkpoints with the current engine, so a replay is exact for battles
run on the deployed engine version; see the
[gameplay cutover](../operations/deployment.md#gameplay-rules-cutover) for engine
upgrades.

### Decision history and saved context

`GET /v1/battles/{id}/decisions?attempt=N` lists completed decisions with the
`resolvedPlan` the engine applied (the `StrategyPlan` schema). Plans use IDs from
the decision context, such as recruitment sources and construction sites, never
guessed ones; the [decision contracts](ai-decisions.md#submitted-plans)
describe how plans are validated.

`GET /v1/battles/{id}/decisions/{requestId}/context?attempt=N` returns the saved
observation for one decision. `context` is the faction-visible battlefield saved
at the time, not current spectator state, and is null for legacy records saved
without one. `initialContext` is the current agent projection rebuilt from that
observation, not a recording of what the model received, and
`availableUnitDetails` is the unit reference the model could look up, not a record
of lookups it made. A request belonging to another battle or attempt returns
`404`. Clients should fetch it on demand and cache by battle, attempt and request.

## Leagues

A league plays AI entrants against each other in background battles. Admins create
and cancel leagues and disqualify entrants; every signed-in role reads them.

`POST /v1/leagues` takes a client-supplied `id`, a `name`, one to five
`scenarioIds`, the battle timing (`tickCount`, optional `tickDuration` and
`decisionIntervalTicks`, as for battles) and 8 or 16 `entrants`, four per group
`A`–`D`, each a distinct faction with a controller. Every entrant is checked on every
map, from both spawn sides, with the battle-creation rules, so an unavailable model,
missing faction or invalid timing fails the create with the same `404`, `422` or
`400` a battle would. Repeating an identical request returns `201` with the stored
league; reusing the `id` for a different request returns `409 already_exists`.

Each group of four plays a dual tournament. In the opening matches the group's first
entrant by entry order meets its fourth, and the second meets the third. The opening
winners then meet in the winners' match and the losers in the losers' match, and the
decider pits the winners'-match loser against the losers'-match winner. The
winners'-match winner places first, the decider's winner second and its loser third,
and the losers'-match loser fourth. A group opens its next round as soon as its
current round settles, whatever the other groups are doing. Once every group is
placed, the top two of each play a crossover knockout: semifinals A1–B2 and B1–A2
with two groups; with four, quarterfinals A1–B2, B1–A2, C1–D2 and D1–C2, arranged so
group-mates meet again only in the final. A league plays 13 fixtures with 8 entrants
and 27 with 16, rotating through the maps group by group and then through the
knockout.

Every match is one battle. A coin flip decides which entrant takes the first spawn
side; it derives from the league's secret battle salt and the fixture, so it holds
across retries and clients cannot predict it. Every match has a winner: the battle's
winner, or after a tick-limit draw or a void fixture the higher castle-health
difference, then damage difference, then the higher seed. In a group the higher seed
is the earlier entrant; in the knockout it is the better group finish, then the
earlier entrant.

The API starts fixtures in order into free engine slots, so up to `battleSlots`
battles run at once and a league battle never waits out the one-hour battle timeout
in the queue. Battles of other owners occupy slots too, and CLI model sides share
`cliSlots`, as the [league scheduler](system-architecture.md#57-play-a-league)
describes; `GET /v1/leagues` reports both counts. League battles count toward the
global limit of unfinished battles, so while they reach it, new Player battles are
rejected. League battles belong to the creating Admin, skip the battle
allowances and the decision budget, and appear on `GET /v1/battles/{id}` with a
`league` field naming the league and fixture. A failed or stopped battle is retried
once with a new battle; a second failure makes the fixture `void`, and the tiebreak
above decides its match. Model access is checked again for every battle, so revoking access
mid-league voids that entrant's remaining fixtures.

`GET /v1/leagues/{id}` returns the settings, entrants, each group's win and loss
records with placements once decided, `knockout` ties, and every fixture with its
match, battle, progress, result and winner as the [`LeagueFixture`](openapi.yaml)
schema describes. Each entrant reports whether it is `eliminated` (two group losses
or a knockout loss) and its `disqualification`, if any.
`GET /v1/leagues` lists summaries, newest first. Each fixture's battle takes the
rules current when it starts, so a league that spans a gameplay release reports more
than one entry in `rulesVersions`. `POST /v1/leagues/{id}/cancel`
cancels unfinished fixtures and stops the running battle; repeating it returns `200`
and stops any league battle still running. Cancelling a completed league returns
`409`.

`POST /v1/leagues/{id}/entrants/{entrant}/disqualify` removes an entrant, given by
its entry index, from a running league, for example a model too slow to keep the
league moving. The body is a JSON object with an optional `reason` of up to 200
characters, stored with the time and the Admin's identity. The entrant loses the
match it is playing, whose battle is stopped, and every match it reaches later is
settled at once without a battle. Matches already decided stand, including one whose
battle completed before the request. A forfeited fixture has status `forfeited` and
names the disqualified side in `forfeit`. The match goes to the other side and counts
in the group record and the bracket like any other; when both sides were disqualified
before the match opened, the higher seed goes through and forfeits its next match.
The request returns `409` when the league is not running, when the entrant is already
eliminated, or when no other entrant that can still win would remain undisqualified;
cancel the league instead. This guarantees a disqualified entrant never becomes
champion. Repeating it returns `200` with the league unchanged.

## Analytics

`GET /v1/analytics` summarizes completed battles for every signed-in role: per
controller (scripted, or profile, model and reasoning effort) the win, draw and loss
record, castle wins, a 95% Wilson interval on the win rate, mean ticks to win,
fallback and invalid-plan rates, mean decision latency and tokens per battle; the
same outcomes per faction; a head-to-head record for every pair of controllers; per
unit type the totals, kills per loss, damage per gold and its share of winning and
losing armies, and each controller's army mix; per upgrade the purchase rate, mean
purchase tick and win rate with and without it; and per map the draw rate, mean
length and each spawn side's wins.

`leagueId`, `since` and `excludeFallback=true` narrow the battles. `rulesVersion`
defaults to the current rules, so balance changes do not mix; it can name another
version, or `all` for every version. A report reads at most the 5000 most recent
battles and says when it was truncated. Unit and upgrade figures use only results
with per-type statistics, so battles completed before those were recorded count for
outcomes alone. A model's
decisions come from its battles' final attempts; a fallback plan a retry reused from
an earlier attempt still counts as a fallback. A renamed faction is reported under
its newest name.

## Faction background generation

Background generation drafts a faction background with an operator-configured
model. A succeeded job is an editable draft, not a saved faction: the client must
use the draft and save the faction through the normal profile endpoint. Each actor
sees only their own jobs; with authentication disabled, every client shares the
`local-development` actor and its allowance.

Admission allows 10 requests per actor per UTC day, at least 30 seconds apart, and
one queued or running job. A repeated `idempotencyKey` with the same normalized
input returns the original job without another charge; the same key with
different input returns `409`. Validation failures (`400`) reserve no quota, and
neither do unavailable or disabled profiles (`503`), which include a profile or
configured model the caller's role [may not use](#model-access-administration). A limit returns `429` with
`Retry-After` and the retry and reset times. Options and job reads are `no-store`.

Queued jobs expire after five minutes, and running jobs time out after two; both
keep their charge on the original admission day, as do failures. Expired jobs are
never rerun automatically. Three invalid submissions from the model close the job
with `invalid_output`. How workers claim and run these jobs is described in
[AI decisions](ai-decisions.md#background-generation).

## Player membership administration

The session advertises `capabilities.members` only to Admins when managed Players
are configured; otherwise the member routes return `503`. All routes require Admin
outside role preview and return `no-store`.

A `202` confirms persisted intent, not Cloudflare admission. A member is active
only while `desiredAccess` is true and `version` equals `syncedVersion`, and acts
with its stored `role`, `player` or `tester`; removing access denies further API
requests immediately. A grant may name the role: a new member without one is a
Player, and an existing member keeps its role unless the grant names another.
Changing only the role of an active member leaves `version` unchanged, so it
applies to the member's next request. Admin cannot be assigned here, and any other
role returns `400`. Repeating an unchanged grant or removal is idempotent. The list includes
removed members until they [expire](database.md#expiry); a grant after that creates
a new member. Emails
are trimmed and lowercased, and must use a dotted DNS domain, a local part of at
most 64 bytes and a total of at most 254 bytes. A configured Admin or Tester email
returns `409`. `syncError` is a sanitized group-level failure, and automatic
retries continue with bounded backoff. No route returns Cloudflare credentials.
See [Player invitations](../operations/player-invitations.md) for the lifecycle.

## Model access administration

Both routes require Admin outside role preview and return `no-store`. A provider or
profile without a stored entry defaults to enabled, with the profile's configured
roles. A `PUT` must list every configured provider and profile, with no extras, and
roles drawn once each from `admin`, `tester` and `player`; an empty
`allowedRoles` denies everyone. A stale `version` returns `409`. The policy
restricts only new battle selections and generation admissions, and cannot enable a
profile whose credentials or runtime are unavailable. See
[model access and role preview](system-architecture.md#model-access-and-role-preview).

A profile entry's optional `models` map narrows its
[model options](#model-and-effort-selection) by role. Each key must be one of the
profile's option IDs and each `allowedRoles` a subset of the profile's own, or the
`PUT` returns `400`; an empty list denies everyone. A model without an entry,
including every model of a profile entry without `models`, allows every role its
profile allows. `GET` omits entries for models outside the profile's options.

A caller's role must be allowed both the profile and the chosen model.
`GET /v1/model-profiles` lists only the models the caller's role may use and omits a
profile whose models it may use none of. When the role may not use the profile's
configured model, `modelLabel` names the first option it may use, and a model
controller without `modelIdentifier` selects that option. Battle and league creation
return `403 permission_denied` for a model the role may not use; league entrants are
checked with the Admin role that owns league battles. Background generation runs its
profile's configured model, so it is unavailable to a role that may not use that
model.

### Upgrade-level orders

The shared battlefield ruleset uses `arsenal`, `arsenal_2`, `arsenal_3` and equivalent IDs for
Plating and Industry. Catalog entries expose optional `track`, `level` and
`requiresUpgradeId`; bonus fields describe each purchase's increment.
`upgradeOrders` remains an ordered string array. Missing or reversed prerequisites
are validation errors; an owned prerequisite or one ordered earlier is accepted.
Repeated owned levels are free, while duplicate IDs in the same submission remain
invalid. An unaffordable level stops further upgrades in that window. Ownership
retains every purchased ID in `ownedUpgradeIds`, and level purchase events add
track, level and incremental effect fields. Saved battles keep their original semantics. New map IDs are `castle-siege`,
`frontier-siege`, `highland-crown`, `long-march` and `river-fords`; retired version-specific IDs are not accepted for new battles.
Scenario summary roster fields describe the same shared catalog on every map.
Specialist upgrades additionally expose `targetUnit`, `targetClass`,
`exclusiveGroup` and scoped bonus fields. Conflicting branch orders are validation
errors. The faction-rulesets response contains one live ruleset. Unit snapshots
include optional specialist capabilities and remaining `repairCharges`; repairs
emit `unit_repaired` events and use support-action target markers.

## Model and effort selection

`GET /v1/model-profiles` includes `modelOptions` for each listed profile, limited to
the models the caller's role may use. Each option contains `id`, `reasoningEfforts`,
an optional `defaultReasoningEffort`, and optional `aliases`. An absent option list means only the
configured model is selectable. `POST /v1/battles` accepts optional `modelIdentifier`
and `reasoningEffort` in each model controller. Unsupported combinations return 422;
scripted controllers reject these model fields. The
[model access](#model-access-administration) role checks for the profile and the
model still apply.
The selection is frozen for execution and retry matching. A `modelIdentifier` that
names one of an option's `aliases` selects that option, and the frozen selection
records the option's `id`; model access rules saved under an alias apply to the
option too.

Decision history includes `reasoningEffort`, `claimedAt`, `contextServedAt`,
`runnerCompletedAt`, optional `cliProcessStartMs`, and optional `resolvedModel`, the
model the provider reported running (see
[model selection](ai-decisions.md#model-selection-and-latency)); it is absent when
the provider names none. Absent timestamps mean unknown. `createdAt` to
`resolvedAt` measures total wait until acceptance/fallback/failure, not subprocess
shutdown. Reclaimed jobs report the latest claim while preserving total wait.
Internal agent claims require `selectionVersion: 1`; incompatible workers receive
409 before any job is leased.

Decision-history rows optionally include `contextDelivery: "tool" | "prefetch"`.
This identifies the CLI worker's configured delivery path; absence means unknown.
In prefetch mode the context timestamp is recorded before CLI launch. Raw provider
metadata is not exposed.
