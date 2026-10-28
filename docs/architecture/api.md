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
unauthenticated caller receives `401` with a session body rather than an error.
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
or the Admin role. Players can read shared factions, battles, events, replays and
history, but receive gameplay-only responses: events and replay events without
payloads, battles without
inspection fields, and a reduced model-profile list. Decision history, saved
decision context and [leagues](#leagues) require Developer or Admin; only Admins
create or cancel a league.

Selecting a model controller, for a battle or for background generation, requires
an enabled, verified profile and a role allowed by the
[model access policy](#model-access-administration). Players need explicit
approval for each API and CLI profile.

### Admission limits

| Limit | Default |
| --- | --- |
| Battles per person per UTC day | 20 |
| Unfinished battles per person | 2 |
| Unfinished battles across the service | 4 |
| Background generations per person per UTC day | 10 |

Operators can change these defaults in `runtime_policy` and the background
generation configuration. Exceeding a limit returns `429`. Admin battles bypass all
three battle limits, including the service-wide one, but still count as unfinished
when other people's battles are admitted. Paused battles are unfinished. A
retry that reuses a request's identifier with the same inputs and identity
returns the work already admitted rather than consuming another allowance. League
battles are Admin battles and also skip the per-battle model decision budget.

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
different caller or content returns `409`. Names, doctrines and backgrounds are
trimmed before storage. Doctrine is limited to 4,000 and background to 1,000
Unicode characters, not UTF-8 bytes; a missing background means empty.

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
move a battle in or out of a filtered list between requests.

Summaries omit maps, states, events and detailed results; fetch those from the
battle routes. Only a completed battle with an outcome and no winner is a draw.

### Status and controls

`GET /v1/battles/{id}` returns the authored map and the current state, attempt and
result. Each faction's result totals include per-unit-type and per-building-type
figures and its upgrade purchase ticks; results completed before these were added
omit those lists. While a battle is queued or running, `production` gives the next point at
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
remain visible. The full battle snapshot used by viewers retains both sides' settings. The deprecated
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
segment (such as a battle that finished before replays were recorded), returns
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
and cancel leagues; Developers and Admins read them.

`POST /v1/leagues` takes a client-supplied `id`, a `name`, one to five
`scenarioIds`, the battle timing (`tickCount`, optional `tickDuration` and
`decisionIntervalTicks`, as for battles) and 8 or 16 `entrants`, four per group
`A`–`D`, each a distinct faction with a controller. Every entrant is checked on every
map, from both spawn sides, with the battle-creation rules, so an unavailable model,
missing faction or invalid timing fails the create with the same `404`, `422` or
`400` a battle would. Repeating an identical request returns `201` with the stored
league; reusing the `id` for a different request returns `409 already_exists`.

Each group plays every pairing twice, once from each spawn side, on one map;
pairings rotate through the maps. Group tables rank by points (win 3, draw 1), then
points among the entrants level on points, castle-health difference, damage
difference and entry order. The top two of each group play a crossover knockout:
semifinals A1–B2 and B1–A2 with two groups; with four, quarterfinals A1–B2, B1–A2,
C1–D2 and D1–C2, arranged so group-mates meet again only in the final. Each tie has
two legs; its winner has more wins, then the higher castle-health and damage
differences, then the better group finish, more group points and entry order. A
league plays 30 fixtures with 8 entrants and 62 with 16.

The API plays fixtures in order, one battle at a time, and only when no other battle
is queued or running, so a league battle never waits out the one-hour battle timeout
in the queue. League battles belong to the creating Admin, skip the battle
allowances and the decision budget, and appear on `GET /v1/battles/{id}` with a
`league` field naming the league and fixture. A failed or stopped battle is retried
once with a new battle; a second failure makes the fixture `void`, which scores for
nobody. Model access is checked again for every battle, so revoking access
mid-league voids that entrant's remaining fixtures.

`GET /v1/leagues/{id}` returns the settings, entrants, ranked `groups`, `knockout`
ties and every fixture with its `battleId` and, once completed, its `result`.
`GET /v1/leagues` lists summaries, newest first. `POST /v1/leagues/{id}/cancel`
cancels unfinished fixtures and stops the running battle; repeating it returns `200`,
and cancelling a completed league returns `409`.

## Faction background generation

Background generation drafts a faction background with an operator-configured
model. A succeeded job is an editable draft, not a saved faction: the client must
use the draft and save the faction through the normal profile endpoint. Each actor
sees only their own jobs; with authentication disabled, every client shares the
`local-development` actor and its allowance.

Admission allows 10 requests per actor per UTC day, at least 30 seconds apart, and
one queued or running job. A repeated `idempotencyKey` with the same normalized
input returns the original job without another charge; the same key with
different input returns `409`. Validation failures (`400`) and unavailable or
disabled profiles (`503`) reserve no quota. A limit returns `429` with
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

A `202` confirms persisted intent, not Cloudflare admission. A Player is active
only while `desiredAccess` is true and `version` equals `syncedVersion`; removing
access denies further API requests immediately. Repeating an unchanged grant or
removal is idempotent. Emails are trimmed and lowercased, and must use a dotted DNS
domain, a local part of at most 64 bytes and a total of at most 254 bytes. A
configured Admin or Developer email returns `409`. `syncError` is a sanitized
group-level failure, and automatic retries continue with bounded backoff. No route
accepts a role or returns Cloudflare credentials. See
[Player invitations](../operations/player-invitations.md) for the lifecycle.

## Model access administration

Both routes require Admin outside role preview and return `no-store`. A provider or
profile without a stored entry defaults to enabled, with the profile's configured
roles. A `PUT` must list every configured provider and profile, with no extras, and
roles drawn once each from `admin`, `developer` and `player`; an empty
`allowedRoles` denies everyone. A stale `version` returns `409`. The policy
restricts only new battle selections and generation admissions, and cannot enable a
profile whose credentials or runtime are unavailable. See
[model access and role preview](system-architecture.md#model-access-and-role-preview).

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

`GET /v1/model-profiles` includes `modelOptions` for authorized profiles, including
Player-visible profiles. Each option contains `id`, `reasoningEfforts`, and an
optional `defaultReasoningEffort`. An absent option list means only the configured
model is selectable. `POST /v1/battles` accepts optional `modelIdentifier` and
`reasoningEffort` in each model controller. Unsupported combinations return 422;
scripted controllers reject these model fields. Profile role checks still apply.
The selection is frozen for execution and retry matching. Provider aliases remain
aliases, rather than promises of a specific underlying version.

Decision history adds `reasoningEffort`, `claimedAt`, `contextServedAt`,
`runnerCompletedAt`, and optional `cliProcessStartMs`. Absent timestamps mean
unknown, including historical rows. Existing inspection permissions still apply;
this does not expose decision inspection to Player roles. `createdAt` to
`resolvedAt` measures total wait until acceptance/fallback/failure, not subprocess
shutdown. Reclaimed jobs report the latest claim while preserving total wait.
Internal agent claims require `selectionVersion: 1`; incompatible workers receive
409 before any job is leased.

Decision-history rows optionally include `contextDelivery: "tool" | "prefetch"`.
This identifies the CLI worker's configured delivery path; it is absent from older
records. In prefetch mode the context timestamp is recorded before CLI launch.
History permissions are unchanged, and raw provider metadata is not exposed.
