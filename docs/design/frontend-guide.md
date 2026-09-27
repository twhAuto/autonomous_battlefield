# OrdinalFrontier Frontend Guide

[Codebase index](codebase-guide.md) · [System architecture](system-architecture.md)

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

Routes: `/` (Home), `/battles/new`, `/battles`, `/battles/:id`, `/factions`,
`/factions/new`, `/factions/:id/edit`, `/admin`, and `/admin/members`. Admin routes
mount only for Admins outside role preview; `/admin/members` also requires the
session's `members` capability. Unknown paths render a not-found page.

Feature-specific API clients live in `src/features/<feature>/api`; common HTTP transport lives in `src/shared/api`. Page components should consume typed client functions rather than duplicating `fetch` calls.

`features/factions/presentation/availability.ts` derives background-writer status
from existing availability, quota, cooldown, and active-job fields. Retrying status
never configures a provider. Identifiers are generated internally and retained in
API requests and routes, but ordinary pages show no identifier or copy controls.
The former shared `Identifier` component has been removed.

Home and primary navigation share three destinations in order: Stage battle,
Factions, Battle history. Administrator navigation also includes Admin. The compact home has no promotional hero. Factions
combines directory access with its Create faction action; the existing creation
and editing routes remain available and keep the Factions navigation item active.

`frontend/e2e/fixtures.ts` intercepts the complete API boundary for application
Playwright tests. `e2e/usability.spec.ts` runs against a local Vite server in
desktop and mobile Chromium, including destructive cases only against fixtures.
`e2e/staging.spec.ts` submits the battle setup form end to end: the fixture serves
`POST /v1/battles`, records the request body, and then serves the new battle as
queued under its own generated identifier, so the journey continues into the live
battle view. It also covers a rejected creation and a retry after recovery.
Map keyboard tests focus the native “Pan with arrow keys” button; pointer panning
continues through the map viewport. Admin settings split per-model access controls
into `ModelAccessCard` and use native outputs for loading and save status.
See [frontend commands](../../frontend/README.md#commands) for browser setup,
coverage, and reports. Jenkins archives failure screenshots, traces, and HTML
reports alongside coverage without changing the 80% coverage thresholds; the
HTML report is archived by a step that fails rather than archiving nothing.

The battle feature's `hooks/` directory owns status, event, decision-history, and faction-perspective requests. Each hook preserves its own polling cadence, retries, cancellation, and terminal-state handling. `components/AIDecisionPanel.tsx`, `components/EventConsole.tsx`, and `components/BattlePresentation.tsx` own the panels and visual presentation; shared UI state types live in `types.ts`. There is no WebSocket or server-sent-event client.

`useBattleStatus` cancels polling when a control returns a terminal status.
`useBattleStatus.test.ts` verifies that a late in-flight response cannot overwrite
an operator stop or schedule further polls.

`BattlePage` owns map perspective and controls. `components/BattleInspector.tsx`
owns the System/player dropdown, the shared inner tab, and the scoped context cache;
`InspectorTabs.tsx` implements inner-tab keyboard navigation. `AIDecisionPanel` keeps
individual decision expansion stable, while `DecisionContextPanel.tsx` lazily
loads and presents the historical observation. `EventConsole` preserves loaded
event filtering and payload expansion. `presentation/events.ts` owns readable
summaries and Key events/Combat/Economy/All events categorization. Mobile initially
shows ten matching events, with Show more revealing additional loaded rows.
Player/decision sections remain mounted
when hidden; the inspector is reset for a different battle or attempt.

The battle toolbar places compact tick progress on the left, faction summaries in
the flexible middle, and the outcome above Play/Pause/Stop on the right. Faction
cards share available width with a 160px minimum before wrapping into equal-height
rows. Progress, faction summaries, and controls stretch to matching outer heights;
the toolbar and map layout stack at 1000px. Controls and inspector share a 340px
desktop column and a 14px gutter, aligning their left and right borders.
The inline map uses the board's aspect ratio and can expand into a native modal
dialog. Camera fitting happens on opening and viewport resizing, not live ticks.
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
show unavailable status, and missing metrics display a dash. The display accepts
any participant count; battle creation and engine faction limits are unchanged.

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

Backend `internal/battle/api/decision_context.go` routes the scoped context read;
`internal/battle/service/decision_context.go` validates ownership and projects the
saved observation through the existing repository `GetDecision` method. The
battle response includes the scenario snapshot's background and doctrine.

Component and route tests cover three-player navigation, keyboard focus,
retained sections, null opening collections, recruitment updates, unit grouping,
legacy profiles, lazy context loading/cache/retry, obsolete response cancellation,
and stable decision expansion. Backend tests cover scope and public projection.

`shared/api/request.ts` accepts API paths and applies the configured base URL. `createApiClient` preserves abort errors and wraps network failures in the feature's error type. Authentication disables global unauthorized notifications so login/session failures are handled locally. Feature clients, including faction ruleset discovery, use this common error handling and retain HTTP status messages; battle stop/pause/resume share one response mapper.

The frontend build enables TypeScript `noUnusedLocals` and `noUnusedParameters`
to reject unused local declarations and parameters alongside strict type checks.

### Responsive navigation and forms

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
