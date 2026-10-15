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
| [`features/battles/pages/CreateBattlePage.tsx`](../../frontend/src/features/battles/pages/CreateBattlePage.tsx) | Scenario, participants, controllers, and tick-limit selection |
| [`features/battles/pages/BattleHistoryPage.tsx`](../../frontend/src/features/battles/pages/BattleHistoryPage.tsx) | Status-filtered history table/cards, cursor navigation, refresh, and detail links |
| [`features/battles/pages/BattlePage.tsx`](../../frontend/src/features/battles/pages/BattlePage.tsx) | Route-level composition, selected perspective/panel, and control actions |
| [`features/auth/pages/LoginPage.tsx`](../../frontend/src/features/auth/pages/LoginPage.tsx) | Single-administrator credential form and error state |
| [`features/admin/pages/AdminSettingsPage.tsx`](../../frontend/src/features/admin/pages/AdminSettingsPage.tsx) | Persistent provider/profile switches, allowed roles, save/conflict states, and entry to read-only role preview |
| [`features/battles/components/HexBattleMap.tsx`](../../frontend/src/features/battles/components/HexBattleMap.tsx) | Hex-map visualization, movement routes, tracking arrows, attack outcomes, and accessible unit animation |
| [`features/battles/components/PlayerStatusPanel.tsx`](../../frontend/src/features/battles/components/PlayerStatusPanel.tsx) | Null-safe spectator summaries, economy/building overview, and grouped living units |
| [`features/admin/pages/MembersPage.tsx`](../../frontend/src/features/admin/pages/MembersPage.tsx) | Managed Player invitations, removal, sync retry, and game link (Admins only, when enabled) |
| [`shared/components/Emblem.tsx`](../../frontend/src/shared/components/Emblem.tsx) | Deterministic faction emblem |

## Routes and session

Home and primary navigation share three destinations in order: Stage battle,
Factions, Battle history. Administrator navigation also includes Admin. The compact home has no promotional hero. Factions
combines directory access with its Create faction action; the existing creation
and editing routes remain available and keep the Factions navigation item active.

Routes: `/` (Home), `/battles/new`, `/battles`, `/battles/:id`, `/factions`,
`/factions/new`, `/factions/:id/edit`, `/admin`, and `/admin/members`. Admin routes
mount only for Admins outside role preview; `/admin/members` also requires the
session's `members` capability. Unknown paths render a not-found page.

The shell shows the signed-out screen after a successful logout or an HTTP 401
confirming that the session is no longer authenticated. If the request otherwise
fails, it retains the current screen and shows an accessible retry message; it cannot confirm that the server session ended. Component tests
cover local-password and Access sessions, and browser tests cover retry on desktop
and mobile. The footer displays the product name without a hardcoded build number.

Ordinary pages hide identifier and copy controls; internal IDs remain in API requests
and routes. Inspection payloads and entity tooltips retain IDs for debugging.

Feature-specific API clients live in `src/features/<feature>/api`; common HTTP transport lives in `src/shared/api`. Page components should consume typed client functions rather than duplicating `fetch` calls.

## Faction editor

`features/factions/presentation/availability.ts` distinguishes failed status reads,
unavailable service, exhausted allowance, cooldown, and active work using existing
fields. Quota/reset details appear only for an available service. Retry reads do
not activate providers or configure credentials, and manual editing stays enabled.
Controller menus show available profiles; unavailable integrations have a separate
status disclosure. Existing inspection permissions govern model/execution details.
Faction deletion is offered only in the authorized editor's Danger zone. It keeps
the named registry-removal confirmation, disables repeat submissions, leaves failed
deletions on the editor, and navigates to the directory after success.

`CreateFactionPage` keys its editor by route identity. Switching between creation
and editing, or between factions, starts fresh form, loading, and error state and
cancels the previous editor's load.

## Battle data lifecycle

The battle feature's `hooks/` directory owns status, event, decision-history, and faction-perspective requests. Each hook preserves its own polling cadence, retries, cancellation, and terminal-state handling. `components/AIDecisionPanel.tsx`, `components/EventConsole.tsx`, and `components/BattlePresentation.tsx` own the panels and visual presentation; shared UI state types live in `types.ts`. There is no WebSocket or server-sent-event client.

`BattlePage` keys its detail component by battle ID, resetting controls and cached
views when navigating between battles. Faction-view results belong to one battle,
attempt, and perspective; cancelled requests cannot replace the current selection.
While a faction view is unavailable, the map shows loading or retry feedback,
keeping the selector and expanded dialog available. Only spectator mode renders
the full map. While a tick refresh is loading, the same faction's last successful
view remains visible. A failed refresh replaces it with the retry placeholder.

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
It also returns `lastUpdated`, the time of the most recent successful status response
or control result; `BattleHeader` shows it beside the connection badge, which already
reports reconnecting and error states.

## Inspector and historical context

`BattlePage` owns map perspective and controls. `components/BattleInspector.tsx`
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
frozen reference snapshot, labelled as optional. Legacy observations can return
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

The battle toolbar places compact tick progress on the left, faction summaries in
the flexible middle, and the outcome above Play/Pause/Stop on the right. Faction
cards share available width with a 160px minimum before wrapping into equal-height
rows. Progress, faction summaries, and controls stretch to matching outer heights;
the toolbar and map layout stack at 1000px. Controls and inspector share a 340px
desktop column and a 14px gutter, aligning their left and right borders.
`features/battles/battles.css` overrides the legacy fixed map height at the battle-layout
specificity; browser tests assert the rendered aspect ratio to prevent regression.
The inspector's desktop content scrolls internally; mobile uses page scrolling.
Above 1000px the inspector stretches to the map's grid-row height. Size
containment prevents event content from enlarging that row; the map determines
the shared height. Stacked and events-only layouts keep independent heights.
Terminal battles hide Play/Pause/Stop and label tick usage as a maximum rather
than completion. `components/FactionSummaries.tsx` shows each
participant's decision activity, gold, and living unit count using existing
spectator state and current-attempt decision polling, independent of map
perspective. Paused and terminal states override decision activity, polling errors
show unavailable status, and missing metrics display a dash. Null collection
slices from Go JSON are handled safely: missing faction state is unavailable,
while an empty army displays zero living units. The display accepts
any participant count; battle creation and engine faction limits are unchanged.

The inline map fits its board aspect ratio. Expanding moves the same map state
into a native modal dialog with focus containment, Escape dismissal, scroll
locking, and focus restoration. Perspective and entity selection survive this
transition. Opening or resizing fits the board; live tick updates preserve the
camera. Pointer and keyboard panning remain local UI state. A native “Pan with
arrow keys” toolbar button provides keyboard panning without making the map container
an application landmark. On mobile the inline
overview permits page scrolling, with dragging enabled in the expanded map.

The SVG map uses compact directional arrows beneath entity icons, matching the
acting faction's unit color. Movement remains dashed and subdued; attacks use solid
lines with a subtle traveling highlight, and tower fire uses a thicker stroke. Endpoints clear entity outlines; paths too short to fit an
arrowhead are omitted. Indicators follow the current battle snapshot, and reduced
motion preferences retain static lines without the traveling highlight. Health
bars update their green fill and numeric HP immediately; lost HP appears red for
300 ms before shrinking over 500 ms. Healing immediately covers the red layer.
Reduced motion disables the damage trail transition. Defeated units leave the live roster immediately. Per-tick `unitDeaths` records
provide a fading cross/ring in the fallen unit's faction color at its confirmed
death position for 900 ms, or a
static marker for reduced motion. Faction views expose allied deaths and visible
enemy deaths only; disappearance from vision never creates a death marker.

Unit snapshots include optional `movementPath` coordinates (start plus each hex
actually traversed during the tick). The engine records these for normal, siege,
retreat, and support movement and clears them with the other per-tick actions.
Snapshot and faction-view copies own their route arrays; enemy routes crossing
hidden hexes are omitted from faction views. No database migration is required.
The map draws these routes and animates existing units along them at 220 ms per
step, capped at 700 ms. Animation requires the recorded start to match the last
displayed position; initial loads, discontinuous snapshots, and reduced-motion
preferences show the current position immediately. New updates cancel any prior
animation. Older snapshots can animate adjacent endpoint-only moves, but the UI
does not infer missing multi-hex routes.

Tracking attacks retain immediate damage. Their arrows follow the target's rendered
walking position; missing/destroyed targets use a short impact marker at the stored
hit position. Attacking units nudge 0.75 pixels forward and back over 200 ms on a
separate transform layer, while towers remain stationary. Fixed-location shots
show a fading impact ring for hits or a muted cross for misses, without a damage
tracer on misses. Polling and map controls do not replay these effects, and reduced
motion disables them.

## Entity presentation

`components/EntityIcon.tsx` supplies local SVG artwork for all seven core unit
and five building types, reused by the map, player roster, building overview,
and a collapsible legend of types present in the displayed map. Map markers keep
faction-colored backgrounds and HP bars, with a separate selection/focus ring.
`presentation/entities.ts` owns type labels, entity indexing, and readable
references such as `Iron Legion · Ranger #12` (or `Ranger #12` inside that player's
panel). It uses the sequence embedded in generated IDs, so deaths, polling,
reordering, and historical event pages do not renumber entities. Opaque IDs use
available entity metadata plus an eight-character ID; unknown references use
`Entity · <short ID>`. Event metadata is scoped to the current attempt; older
attempts resolve generated IDs directly. Full IDs remain in tooltips and raw
inspection payloads. Structured recruitment sources and unit/building types use
the same helpers, while freeform AI prose and raw JSON remain unchanged.
Presentation tests cover naming stability, unknown types/IDs, historical attempts,
raw payload preservation, icon distinctions, and keyboard selection.

## Shared HTTP transport

`shared/api/request.ts` accepts API paths and applies the configured base URL. `createApiClient` preserves abort errors and wraps network failures in the feature's error type. Authentication disables global unauthorized notifications so login/session failures are handled locally. Feature clients, including faction ruleset discovery, use this common error handling and retain HTTP status messages; battle stop/pause/resume share one response mapper.

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

`frontend/e2e/fixtures.ts` intercepts the complete API boundary for application
Playwright tests. `e2e/usability.spec.ts` runs against a local Vite server in
desktop and mobile Chromium, including destructive cases only against fixtures.
`e2e/staging.spec.ts` submits the battle setup form end to end: the fixture serves
`POST /v1/battles`, records the request body, and then serves the new battle as
queued under its own generated identifier, so the journey continues into the live
battle view. It also covers a rejected creation and a retry after recovery.
`e2e/admin.spec.ts` verifies model-access saves and Player role preview on desktop
and mobile using API fixtures.
Map keyboard tests focus the native “Pan with arrow keys” button; pointer panning
continues through the map viewport. Admin settings split per-model access controls
into `ModelAccessCard` and use native outputs for loading and save status.
See [frontend commands](../../frontend/README.md#commands) for browser setup,
coverage, and reports. Jenkins archives failure screenshots, traces, and HTML
reports alongside coverage without changing the 80% coverage thresholds; the
HTML report is archived by a step that fails rather than archiving nothing.

Component and route tests cover three-player navigation, keyboard focus,
retained sections, null opening collections, recruitment updates, unit grouping,
legacy profiles, lazy context loading/cache/retry, obsolete response cancellation,
and stable decision expansion. Backend tests cover scope and public projection.

The frontend build enables TypeScript `noUnusedLocals` and `noUnusedParameters`
to reject unused local declarations and parameters alongside strict type checks.
