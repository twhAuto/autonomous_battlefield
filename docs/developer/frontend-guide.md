# OrdinalFrontier Frontend Guide

[Codebase index](codebase-guide.md) · [System architecture](../architecture/system-architecture.md)

Read by task: [routes](#routes-and-session) · [faction editing](#faction-editor) ·
[battle requests](#battle-data-lifecycle) · [inspector](#inspector-and-historical-context) ·
[map](#map-layout-and-motion) · [styles](#shared-contracts-and-styles) · [verification](#verification)

## Frontend organization

The frontend is a React/Vite single-page application.

| Area | Responsibility |
| --- | --- |
| [`app/App.tsx`](../../frontend/src/app/App.tsx) | Shared layout and route table |
| [`features/auth/api/auth.ts`](../../frontend/src/features/auth/api/auth.ts) | Session discovery, login, and logout client |
| [`shared/api/request.ts`](../../frontend/src/shared/api/request.ts) | API base URL, cancellation-preserving network error mapping, and opt-in unauthorized-session notifications |
| [`features/factions/api/factions.ts`](../../frontend/src/features/factions/api/factions.ts) | Typed faction and ruleset requests |
| [`features/battles/api/battles.ts`](../../frontend/src/features/battles/api/battles.ts) | Typed scenario, profile, battle, history, event, view, control, and decision requests |
| [`features/factions/pages/CreateFactionPage.tsx`](../../frontend/src/features/factions/pages/CreateFactionPage.tsx) | Faction profile editor and background generator |
| [`features/factions/pages/FactionsPage.tsx`](../../frontend/src/features/factions/pages/FactionsPage.tsx) | Faction registry and authorized edit links; deletion lives in the editor's Danger zone |
| [`features/battles/pages/CreateBattlePage.tsx`](../../frontend/src/features/battles/pages/CreateBattlePage.tsx) | Scenario (default `frontier-siege`), participants, controllers, and tick-limit selection |
| [`features/battles/components/ControllerSelect.tsx`](../../frontend/src/features/battles/components/ControllerSelect.tsx) | Scripted or model controller with dependent model and effort selectors (default Low effort), shared by battle and league setup |
| [`features/battles/components/MapPreview.tsx`](../../frontend/src/features/battles/components/MapPreview.tsx) | Static terrain and castle-spawn preview of the selected scenario map |
| [`features/battles/pages/BattleHistoryPage.tsx`](../../frontend/src/features/battles/pages/BattleHistoryPage.tsx) | Status-filtered history table/cards, cursor navigation, refresh, and detail links |
| [`features/battles/pages/BattlePage.tsx`](../../frontend/src/features/battles/pages/BattlePage.tsx) | Route-level composition, selected panel, and control actions |
| [`features/auth/pages/LoginPage.tsx`](../../frontend/src/features/auth/pages/LoginPage.tsx) | Local-password login form used only in the development auth mode |
| [`features/admin/pages/AdminSettingsPage.tsx`](../../frontend/src/features/admin/pages/AdminSettingsPage.tsx) | Persistent provider/profile switches, allowed roles, save/conflict states, and entry to read-only role preview |
| [`features/battles/components/HexBattleMap.tsx`](../../frontend/src/features/battles/components/HexBattleMap.tsx) | Hex-map visualization, movement routes, tracking arrows, attack outcomes, and accessible unit animation |
| [`features/battles/components/PlayerStatusPanel.tsx`](../../frontend/src/features/battles/components/PlayerStatusPanel.tsx) | Null-safe spectator summaries, economy/building overview, and grouped living units |
| [`features/battles/components/TypeStatistics.tsx`](../../frontend/src/features/battles/components/TypeStatistics.tsx) | After-action unit and building tables by type and the upgrade timeline; renders nothing for a result without per-type figures |
| [`features/leagues/api/leagues.ts`](../../frontend/src/features/leagues/api/leagues.ts) | Typed league list, read, create and cancel requests |
| [`features/leagues/pages/CreateLeaguePage.tsx`](../../frontend/src/features/leagues/pages/CreateLeaguePage.tsx) | League setup: name, 8 or 16 entrants with faction, group and controller, maps, timing, and estimates of battles, duration and model decisions; `?from=<league id>` pre-fills the form from that league, and copied factions, maps, profiles, models or efforts that no longer exist block starting until replaced |
| [`features/leagues/pages/LeaguePage.tsx`](../../frontend/src/features/leagues/pages/LeaguePage.tsx) | Group tables, knockout bracket and fixtures with battle links (`components/LeagueStandings.tsx`), each entrant named with its controller, refreshed while the league runs; for Admins, cancellation and **START AGAIN** into a pre-filled setup |
| [`features/analytics/pages/AnalyticsPage.tsx`](../../frontend/src/features/analytics/pages/AnalyticsPage.tsx) | Statistics report with URL-backed league, rules-version and fallback filters: model, head-to-head, unit, army-mix, upgrade, faction and map tables |
| [`features/analytics/components/WinRateChart.tsx`](../../frontend/src/features/analytics/components/WinRateChart.tsx) | Model leaderboard: win rate per controller with its 95% interval |
| [`features/admin/pages/MembersPage.tsx`](../../frontend/src/features/admin/pages/MembersPage.tsx) | Managed Player invitations, removal, sync retry, and game link (Admins only, when enabled) |
| [`shared/components/Emblem.tsx`](../../frontend/src/shared/components/Emblem.tsx) | Saved faction symbol with name-derived colors |

## Routes and session

Home and primary navigation share three destinations in order: Stage battle,
Factions, Battle history. Developers and Admins also see Leagues and Statistics,
and Admins see Admin. The compact home has no promotional hero. Factions
combines directory access with its Create faction action; the creation and
editing routes keep the Factions navigation item active.

Routes: `/` (Home), `/battles/new`, `/battles`, `/battles/:id`, `/factions`,
`/factions/new`, `/factions/:id/edit`, `/leagues`, `/leagues/new`, `/leagues/:id`,
`/analytics`, `/admin`, and `/admin/members`. League and statistics routes mount for
Developers and Admins, including in Developer preview. The league setup form at
`/leagues/new`, the start and cancel actions, and the admin routes are only for
Admins outside role preview; anyone else opening `/leagues/new` gets the not-found
page. `/admin/members` also requires the session's `members` capability. Unknown
paths render a not-found page.

The shell shows the signed-out screen after a successful logout or an HTTP 401
confirming that the session is no longer authenticated. If the request otherwise
fails, it retains the current screen and shows an accessible retry message; it cannot confirm that the server session ended. Component tests
cover local-password and Access sessions, and browser tests cover retry on desktop
and mobile. The footer displays the product name without a hardcoded build number.

Ordinary pages hide identifier and copy controls; internal IDs remain in API requests
and routes. Inspection payloads and entity tooltips retain IDs for debugging.

Feature-specific API clients live in `src/features/<feature>/api`; common HTTP transport lives in `src/shared/api`. Page components should consume typed client functions rather than duplicating `fetch` calls.

## Faction editor

The editor's emblem picker saves the `emblem` identifier with the profile, and the
registry and battle setup render the same identifier.
`shared/domain/emblems.ts` owns the frontend identifiers and labels;
`shared/components/Emblem.tsx` draws each symbol with name-derived colors and falls
back to Star for missing or unknown identifiers. Adding a symbol also changes the
backend lists named in the [feature map](backend-guide.md#feature-map).

`features/factions/presentation/availability.ts` distinguishes failed status reads,
unavailable service, exhausted allowance, cooldown, and active work. Quota/reset details appear only for an available service. Retry reads do
not activate providers or configure credentials, and manual editing stays enabled.
Controller menus show available profiles; unavailable integrations have a separate
status disclosure. Inspection permissions govern model/execution details.
Faction deletion is offered only in the authorized editor's Danger zone. It keeps
the named registry-removal confirmation, disables repeat submissions, leaves failed
deletions on the editor, and navigates to the directory after success.

A generated background stays in an editable preview until **Use background**;
**Discard** and errors leave the current profile fields intact, and saving the
faction remains a separate explicit action. Manual background editing works while
generation is disabled.

`CreateFactionPage` keys its editor by route identity. Switching between creation
and editing, or between factions, starts fresh form, loading, and error state and
cancels the previous editor's load.

## Battle data lifecycle

The battle feature's `hooks/` directory owns status, event, and decision-history requests. Each hook preserves its own polling cadence, retries, cancellation, and terminal-state handling. `components/AIDecisionPanel.tsx`, `components/EventConsole.tsx`, and `components/BattlePresentation.tsx` own the panels and visual presentation; shared UI state types live in `types.ts`. There is no WebSocket or server-sent-event client.

`BattlePage` keys its detail component by battle ID, resetting controls and cached
views when navigating between battles. The main battle snapshot supplies the full
map.

`useBattleEventStream` owns one cancellable request loop per selected attempt.
Initial loading, polling, and manual refresh cannot overlap. Status changes fetch
from the retained cursor, including the final events after completion, stop, or
failure. Full incremental pages for the current attempt drain immediately; live
polling otherwise waits one second. Historical attempts load on selection and
manual refresh. Errors retain loaded events and expose retry. The buffer remains
deduplicated by sequence and bounded to the latest 500 events. Retrying a failed
initial load still requests the latest page; after a successful load, retries
resume from the retained cursor. After a failed request on the live current attempt,
the hook retries automatically after 2, 4, 8 and 16 seconds, then every 30 seconds
until a request succeeds or the battle settles. Terminal and historical attempts wait
for manual retry. A new current attempt is followed during render, so no request is
sent for the attempt it replaces.

`useBattleStatus` cancels polling when a control returns a terminal status.
`useBattleStatus.test.ts` verifies that a late in-flight response cannot overwrite
an operator stop or schedule further polls.

`useBattleReplay` plays a settled battle from `GET /v1/battles/{id}/replay`
segments. It debounces seeks, so dragging the timeline loads only the segment where
it stops, prefetches the next segment while one plays, and shows BUFFERING while it
waits. Only the segments nearest the current tick stay in memory. A failed segment
stops playback and keeps a retry; leaving the page aborts outstanding requests.

`BattleHistoryPage` fetches on entry, filter or page navigation, retry, and manual
refresh; it does not poll. URL parameters keep the status filter, current cursor,
and prior cursors for Previous and browser navigation, and refresh returns to the
newest page with the same filter. Obsolete requests are aborted and their results
ignored.

## Battle page layout

`BattlePage` lays the live battle out as a command center. At desktop widths
(over 1000px) the page spans the full window and fits one viewport height: a
single command band (`BattleHeader` with the scenario, tick progress, outcome and
controls, a connection badge while the battle is active, and for a league battle a
link to its league, group or stage and leg), one
faction strip per participant, then the map filling the remaining height beside the
inspector. The title states the battle's status, so the band repeats neither status
nor attempt. The map legend and selected-entity details float over the map, so
opening them never resizes the map viewport (which would refit and lose pan and
zoom). The inspector can be widened or hidden to a rail; focus moves to the
replacement control. Narrower screens stack the same regions in document order and
keep the map's aspect ratio.

A battle with a result opens on its after-action statistics (`BattleStatistics`:
each faction's totals, units and buildings by type, and upgrade timeline), laid over
the map so viewers see the outcome without scrolling. Each faction's card heading and
the header's outcome name the faction with the same model label as its faction strip;
the winner's card is marked **Winner**. The covered map is inert, so
keyboard and assistive-technology users reach the results first. The results' header is a
map toolbar led by **Show map**; the map's toolbar is led by **Show results** in the same spot,
size and style, so switching views never moves the control. Focus moves to the replacement
control.
Narrower screens show the results in place of the map. A battle without a map lists
its results below the page.

A completed, stopped or failed battle offers **WATCH REPLAY** under its tick
progress and, for a league battle, **BACK TO LEAGUE** to the league page. `components/BattleReplay.tsx` then replaces the faction strips and map in
the same grid areas, with playback controls (restart, step, play/pause, a timeline
slider and speed) above the map. Faction strips show the frame's gold, units,
buildings, castle health and production, and the plan applied by that tick. The
inspector keeps the final state and the full event history. Sequential ticks animate
like live updates; a backward seek jumps. **EXIT REPLAY** returns to the final state,
with any results over the map again.

## Inspector and historical context

`BattlePage` owns battle controls. `components/BattleInspector.tsx`
owns the independent System/player dropdown, shared inner tab, and scoped context
cache. `InspectorTabs.tsx` implements keyboard navigation. System shows a player
comparison, attempt-selectable events, and the next production window (or pending
opening purchase) with the per-faction recruit limit. The engine computes
`production`; `presentation/production.ts` only formats it.

Player headers show current gold, living units, castle health, and snapshot tick.
Overview groups buildings and economy totals; Units groups living entities with
HP/coordinates; Faction shows the saved profile. Navigation stays above the
scrolling content region. Hidden sections remain mounted to preserve filters and
expanded decisions. Changing battle or attempt resets the inspector.

`AIDecisionPanel` preserves decision expansion as history arrives.
`DecisionContextPanel.tsx` loads observations only for active, expanded decisions,
caches by battle/attempt/request, and aborts and ignores obsolete responses.
Decision history also rejects responses for an obsolete battle or attempt.

`GET /v1/battles/{id}/decisions/{requestId}/context?attempt=N` reads the persisted
observation. Backend `internal/battle/api/decision_context.go` routes the request;
`internal/battle/service/decision_context.go` checks battle, attempt, request, and
observation scope through repository `GetDecision`. Its public projection includes
historical identity, economy, faction-visible view, legal choices, prior plan,
memory, and knowledge; provider metadata and capabilities are excluded.
`initialContext` is a reconstructed projection, and `availableUnitDetails` is the
frozen reference snapshot; the inspector labels them **Agent context
(reconstructed)** and **On-demand unit reference**. Legacy observations can return
`context: null`. Participant background and doctrine come from the scenario snapshot.

The inspector distinguishes observation ticks from its current-resource header.
Plans describe intent; events provide execution evidence. A running decision does
not prove MCP connectivity. Private reasoning and tool-call traces are not exposed.

`EventConsole` preserves filtering and payload expansion. `presentation/events.ts`
owns summaries and Key events/Combat/Economy/All events categories. Key events is
the default; All events includes unknown types. Presets and exact-type filters
operate on loaded history without changing cursor requests, attempt selection, or
server records. Details retains original fields and JSON. Mobile shows ten matching
rows initially; Show more reveals loaded rows. A polite status announces counts.

## Map layout and motion

[`HexBattleMap.tsx`](../../frontend/src/features/battles/components/HexBattleMap.tsx)
draws the SVG map: entity icons, HP bars, movement routes, tracking and
fixed-position attack markers, tower fire, healing lines and death markers. Each
faction's [territory](../game/game-rules.md#construction-and-training) is tinted in
its faction color. When the battle response sets `contestedTerritory`, overlapping
territory is hatched instead and the legend explains it is closed to construction;
otherwise overlapping tints blend. A tile's tooltip names the faction whose territory
it is, or the factions contesting it. Pages can add toolbar controls through the `actions`
prop, which the expanded dialog leaves out, and mark the inline map `covered` to make
it inert beneath page content.
`features/battles/battles.css` and `map.css` own the battle layout; browser tests
assert the rendered map aspect ratio. Layout values, breakpoints and animation
timings live in the CSS and components with their tests, not in this guide.

- Animation follows server data only. A unit moves along its recorded
  `movementPath` when the path starts at its last displayed position; otherwise it
  jumps. The UI never infers multi-hex routes.
- Effects such as attack nudges, impact markers and death markers come from the
  current tick's state; polling and map controls do not replay them. Reduced-motion
  preferences disable motion and transitions.
- Faction views show allied deaths and visible enemy deaths only; disappearance
  alone never creates a death marker.
- The expanded map is a native modal dialog that keeps map position and selection.
  Opening, closing or resizing it fits the board again; live tick updates keep the
  current camera. Keyboard panning uses the native "Pan with arrow keys" button.

`FactionSummaries.tsx` shows each participant's decision activity, gold, units,
buildings and castle health from spectator state, from the full battlefield, edged in
the same faction color as its map units (`presentation/factions.ts`). Units count
living units plus paid units in training, the total the engine caps, shown against
the battle's `maximumUnits` when the ruleset has a cap; buildings exclude the castle.
Each strip also
carries that faction's production chips, one upgrade chip with an icon per owned
upgrade (the highest level per general track, with its numeral) and, for inspecting
viewers, a plan disclosure. Every strip control is one 44 px touch target tall; an
upgrade chip too wide for its column wraps its icons and grows instead of overflowing.
Production and upgrade chips wrap in their own column beside the plan. Chips and the plan share one exclusive `<details name>` group per
strip, so opening one closes the others. Hovering an upgrade icon shows its name and
effect; the chip's disclosure lists every owned upgrade with its effect for touch,
keyboard and screen-reader users. Failed-battle reasons in the header are
never clamped. Paused battles override decision activity, settled battles show none
(the title states the outcome), and Go `null` collections are treated as empty.
A model-controlled participant's heading also shows its model identifier and any
reasoning effort. Player responses carry only the controller type, so players see
the faction name alone.

## Entity presentation

[`EntityIcon.tsx`](../../frontend/src/features/battles/components/EntityIcon.tsx)
supplies the SVG artwork for the fourteen unit and nine current building types (plus legacy Watchtower), reused by the
map, roster, building overview and legend. `presentation/entities.ts` owns type
labels and readable references such as `Iron Legion · Ranger #12`, derived from the
sequence embedded in generated IDs so deaths, polling and older attempts never
renumber entities. Full IDs stay in tooltips and raw payloads; freeform AI prose
and raw JSON are shown unchanged.

[`UpgradeIcon.tsx`](../../frontend/src/features/battles/components/UpgradeIcon.tsx)
draws upgrade artwork in the same style: one glyph per general track and unit
unlock, and the improved unit's icon beside an effect glyph for each
specialization. `presentation/upgrades.ts` owns upgrade labels, effect text and art
keys; the effect text states the shipped catalog's bonuses, so update it with any
ruleset that changes them. The
faction strip chip, the inspector's owned-upgrade list and AI decision cards'
upgrade orders use it; decision cards also show unit and building icons beside
recruitment and construction orders.

## Shared HTTP transport

`shared/api/request.ts` accepts API paths and applies the configured base URL
(`VITE_API_URL` in builds). The Vite development proxy defaults to
`http://localhost:8080`; set the server-only `DEV_API_PROXY_TARGET` in the frontend
env files or shell to point it elsewhere. `createApiClient` preserves abort errors and wraps network failures in the feature's error type. Authentication disables global unauthorized notifications so login/session failures are handled locally. Feature clients, including faction ruleset discovery, use this common error handling and retain HTTP status messages; battle stop/pause/resume share one response mapper.

## Responsive navigation and forms

`AppLayout` owns the mobile menu, account actions, Escape/focus handling and skip
link. `useMobile` shares the 780px breakpoint with CSS. `ResponsivePreview` keeps
form previews after the controls and collapses them on mobile. Responsive and interaction rules live with the shell, feature, or shared component
that owns them; global accessibility defaults live in `shared/styles/accessibility.css`.
Navigation and preview tests cover disclosure behavior and responsive changes.

## Shared contracts and styles

`shared/domain/model-profile.ts` owns `ModelProfile`, used by battle setup and
administration. `shared/domain/units.ts` owns `UnitCapabilities`, used by battles
and faction rulesets. Feature API clients import these contracts directly;
battle-only payloads remain in the battle client and UI state in `battles/types.ts`.

`src/styles.css` is the single stylesheet entry point, imported by `App.tsx`.
It loads shared foundations, then shell and feature styles, and finally shared
accessibility defaults to preserve their cross-feature cascade:

- `shared/styles/base.css`: tokens, resets, common page/status elements.
- `shared/styles/forms.css`: shared forms and preview panels.
- `shared/styles/accessibility.css`: cross-feature typography, focus, target sizes, and mobile form/page defaults, loaded last.
- `shared/components/Emblem.css` and `ResponsivePreview.css`: reusable visuals.
- `app/shell.css` and `pages/HomePage.css`: navigation, layout, and the home page.
- `features/{auth,admin,factions}/*.css`: feature-owned styles.
- `features/battles/battles.css` and `map.css`: battle panels/history and map visuals.

Keep responsive and interaction rules in the owning stylesheet. Changes to
shared rules require desktop/mobile browser checks as well as component tests.

## Verification

`frontend/e2e/fixtures.ts` intercepts the complete API boundary for Playwright
tests, which run against a local Vite server in desktop and mobile Chromium:

- `e2e/usability.spec.ts` covers navigation, layout reflow, map controls, replay
  playback and destructive actions against fixtures only.
- `e2e/staging.spec.ts` submits the battle setup form end to end, continues into
  the live battle view, and covers a rejected creation and a retry.
- `e2e/admin.spec.ts` covers model-access saves and Player role preview.

See [frontend commands](../../frontend/README.md#commands) for browser setup,
coverage and reports, and [coverage gate details](ci.md#coverage-gate-details) for
thresholds. Jenkins archives failure screenshots, traces and the HTML report; a
missing report fails the stage.

The frontend build enables TypeScript `noUnusedLocals` and `noUnusedParameters`
alongside strict type checks.

Forests, gold mines and rocks have distinct SVG silhouettes via `TerrainGlyph.tsx`,
terrain colors and accessible descriptions. When `state.goldMines` is present, each
mine carries a badge with its gold left (compact, such as 1.2K) and a bar of its share of
the reserve, drawn above entities so a building on the mine never hides it; the tile
description gives the exact figures, depleted mines are dimmed,
and the player overview totals the gold left in the mines a faction holds. Selecting a living building highlights its
construction radius. Production-building details show their `trains` roster, selected unit, on/off setting,
remaining/total ticks, progress and blocked exits. `BuildingProduction.tsx`
also shows every living production source as a chip in its faction strip to all
viewers, including gold waits, unit-limit waits, locked-unit waits (`lock`) and pending activation. Production controls belong to the AI; the viewer
is read-only. Historical batch queues retain their FIFO presentation. Current castles
show the Barracks roster, their queue, attack/range stats and tower-fire visuals.
In historical snapshots without `trains`, populated queues still display, but empty
legacy castle/Barracks queues have no training-capability label because absence of
the field alone does not identify their historical production rules. Dead buildings are omitted from the map, selection details and
range overlays immediately, while history can still use their saved identities.
The radius overlay shows reach; terrain, occupancy and spacing still determine
actual legal placements in the engine.
