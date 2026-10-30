# AI decisions

This page is the contract between the deterministic engine and the model
controllers: how a decision is executed, what a model sees, what it may submit,
and how failures are attributed. The [system architecture](system-architecture.md)
owns the surrounding components, the
[decision window flow](system-architecture.md#52-strategy-decision-window) and
[timeout fallback](system-architecture.md#53-timeout-and-fallback), and the
[capability](system-architecture.md#model-facing-capability) and
[agent control-plane](system-architecture.md#agent-control-plane) trust
boundaries. The [game rules](../game/game-rules.md) define what a plan does once
applied.

Battle setup can override the map's decision interval for a single battle
(1–100 ticks after the opening) and tick duration (100ms–10s). These values are
validated and frozen in the scenario snapshot. Shorter intervals request plans
more often; tick pacing does not shorten the model decision deadline.

For Admin and Developer viewers, each faction status box shows the latest
accepted or fallback plan at or before the current tick and its submitted
rationale. A pending or future decision does not replace the applied plan.
Stopped battles retain the last plan; missing records have explicit empty states.
Fallback plans are labeled. Players cannot inspect decisions.

## Decision lifecycle

```mermaid
flowchart TD
    start((" ")) -->|created| pending([pending])
    pending -->|scripted| accepted([accepted])
    pending -->|claimed| running([running])
    running -->|submitted| accepted
    pending -->|stopped| failed([failed])
    running -->|error| failed
    start -->|blocked| failed
    pending --- deadline{{deadline}}
    running --- deadline
    deadline --> fallback([fallback])
    failed -->|battle continues| fallback
    failed -->|battle ended| done((" "))
    accepted --> done
    fallback --> done
```

| Transition | When |
| --- | --- |
| created → `pending` | The engine opens a decision window for a faction. |
| blocked → `failed` | The decision is created while AI is stopped or the battle's model budget is spent (`ai_disabled_or_budget`). |
| `pending` → `accepted` (scripted) | A scripted controller's plan is submitted directly by the engine. |
| `pending` → `running` (claimed) | A worker claims a model decision and receives its request capability. An expired lease is reclaimed and stays `running`. |
| `running` → `accepted` (submitted) | The model submits a valid plan with that capability. A model cannot submit from `pending`, because the capability is issued only by the claim. |
| `pending` → `failed` (stopped) | The budget runs out at claim time (`decision_budget`), or the battle is stopped, expires or is requeued by a stopping engine (`battle_stopped`). |
| `running` → `failed` (error) | The runner fails, or the battle is stopped, expires or is requeued. |
| deadline → `fallback` | The window closes without an accepted plan; the engine records its fallback plan. |
| `failed` → `fallback` | While the battle continues, the engine resolves a failed decision with its fallback plan. A failed decision whose battle has ended stays `failed`. |

An accepted submission is idempotent when it repeats the stored idempotency key.
Optimistic version checks prevent late or duplicate workers from overwriting
another resolution.

## Model execution

`internal/ai/agent` (`agent.Executor`) owns decision claims, concurrency, timeouts
and outcome reporting. A worker runs `ai.agentConcurrency` jobs at once (1–8, 1
when omitted). CLI jobs also need one of the `runtime_policy.cli_concurrency`
leases (1–8, default 4), which PostgreSQL shares across every worker, battle
decision and generation job; API jobs take no CLI lease. A lease is held until the
worker reports completion or its execution deadline passes, even after the plan is
accepted. A job that agent shutdown cancels still reports its failure, or an accepted
plan's usage, within five seconds, so its decision closes and the engine falls back
without waiting out its timeout. A claim's lease counts from the decision row's creation, not from the
claim: it ends the worker's `ai.decisionTimeout` plus five seconds after creation, so
a decision that waited for a slot runs only for the window it has left. The engine
starts its own `ai.decisionTimeout` wait once it has created the window's decisions,
so with the same setting on both sides a run ends at most five seconds after the
engine falls back. A decision with less than ten seconds of lease left (half the
lease, for leases under twenty seconds) is not claimed and falls back at the
deadline. Pausing a battle extends the engine's wait but not a claimed lease. Claims carry the database lease
deadline, which also bounds the request capability, so network delay never extends
a worker's time; keep the database, AI gateway and VM clocks synchronized.
[CLI worker operations](../operations/cli-worker.md) give the production worker's
current setting.

The executor runs either an `APIRunner` or a `CLIRunner` from `internal/ai/runner`;
both implement the same provider-neutral runner contract.

### API runtime

`APIRunner` (`internal/ai/runner/api.go`) runs one tool loop for every API
provider:

1. connect to MCP with the request-scoped capability;
2. offer only the battle tools `get_decision_context`, `get_unit_details` and
   `submit_strategy_plan`;
3. ask the provider for a tool-calling turn, rejecting unknown tools and turns
   without a tool call;
4. append the turn and each tool result to the conversation;
5. stop after an accepted submission or the profile's step limit.

Each turn is one step. The shipped API profiles allow five: the context fetch, one
unit-details lookup and the three submissions a decision accepts before it closes.

Provider adapters send the decision policy as the provider's system instruction,
separate from the untrusted battlefield data, and normalize tool calls, refusals and
usage into common types.

### CLI runtime

The CLI runner starts Codex or Antigravity in a fresh temporary directory with:

- an empty workspace and a request-scoped MCP bearer token;
- only the session's battlefield tools enabled (three for battle decisions, two for
  background generation), and file, command and URL actions denied;
- shell, browser, app, image, plugin, skill and multi-agent features disabled where
  supported, and no approval prompts;
- the allowlisted profile model passed explicitly, and a CLI version matching
  the worker profile;
- a subprocess environment without worker control credentials, provider keys or
  personal CLI overrides, and output capture bounded at 4 MiB.

Local CLIs allow the selected stable version or newer; the launcher passes the
accepted version to the worker profile.

On Linux the worker disables dumpability, so a same-user CLI child cannot read the
parent's environment or memory, and cancellation kills the CLI's process group.
Development uses the host's existing CLI logins; production uses only the dedicated
login in the CLI VM, and subscription credentials never enter Kubernetes. A
headless account probe must succeed before a profile is advertised. Antigravity's
strict tool-permission mode is not used, because it blocked explicitly allowed MCP
calls; explicit allow and deny rules enforce the restrictions instead.

A CLI run succeeds only when its JSON result reports success with no denied actions
**and** the AI gateway holds an accepted plan for the request; otherwise the worker
fails the decision and the engine falls back. CLI prompts must copy the target tick
and version from the context and make a substantive decision; name-only plans are
rejected.

Antigravity writes large MCP responses to local files, which the session cannot
read, so the host fetches the context through MCP and embeds it in the prompt (up
to 64 KiB; larger contexts fall back). The model still submits through MCP.

The Claude profile (`claude-subscription-local`) runs in Claude Code's restricted
mode with no built-in tools, hooks or settings discovery, a strict MCP configuration
and `dontAsk` permissions; its capability lives in a private temporary file. Its
version pin, model alias and personal-memory caveat are in
[Claude operations](../operations/claude-cli.md).

## Prompt provenance and faction memory

Every runtime uses an embedded decision policy assembled by
`internal/ai/prompts/prompts.go`. The persisted `prompts.Version`
(`battlefield-agent-v10.<digest>`) hashes sample tool, fallback-inline and split-inline
layouts, both policy variants, and the rule-deduplication crosswalk. Git keeps earlier
prompt revisions.

Tool-fetch sessions receive shared policy, faction identity, then the request ID.
Prefetched sessions receive policy and the delivery contract, a `REFERENCE` JSON
block, faction identity, then a `CONTEXT` JSON block. Recursively merging the two
JSON objects restores the complete context: the reference contains schema/mode,
frozen knowledge, legal catalogs and terrain; the remainder contains faction/battle
IDs, owned upgrades, current legal sites/recruitment sources and observed state, economy, plans and
memory, with tick/request metadata last. Unknown fields remain in the remainder;
arrays, numbers, nulls and absent values retain their meanings. Both blocks remain
untrusted game data. Contexts without any reference leaves retain the original
single-block inline layout. Background generation keeps its separate prompt.

The shared policy states training-roster selection once. For prefetched continuous
production with the exact reviewed core-v10 mechanics, `rule-duplicates.json` removes
policy sentences whose rules already appear in those frozen mechanics, plus the
inactive legacy recruitment paragraph. A SHA-256 match of the **entire mechanics
text** gates this reduction. Tool-fetch sessions, legacy mode and missing, truncated
or changed mechanics retain the full fallback guidance. Frozen mechanics themselves
are never shortened. Submission, opening, privacy and other unique requirements
remain in both variants, as do generic catalog-dependent exclusivity and industry
guidance whose full scope is not stated explicitly in the mechanics prose.

This deterministic order lengthens the reusable prefix of prefetched prompts.
Tool-fetch sessions still send a changing request ID before receiving context, so
ordering the tool response cannot move its reference data into the initial shared
prefix. Ordering is not evidence of provider cache hits: provider message boundaries,
cache controls and CLI-managed serialization still apply. Measure reported cached
input separately; provider-specific cache configuration remains separate work.

Only the owning faction's name, doctrine and optional background enter its decision
context, observation hash and provenance; opponent doctrines never do. They arrive
JSON-escaped as untrusted data and never extend the system instructions. The model
acts as the faction's commanding voice: core rules, legal choices, tool restrictions
and observation privacy take precedence over doctrine, and doctrine over background.
Scripted controllers and the opening fallback ignore doctrine.

Before hashing an observation, the engine adds up to three earlier resolved
decisions for the same battle, attempt and faction (`BuildFactionMemory`, at most
8 KiB): their economy and unit counts, plans, ticks and accepted or fallback status.
Each entry also carries an `outcome` read from the battle's recorded events while
that plan was active, from its target tick to the next remembered plan or the
current decision (`throughTick`): units trained, lost and destroyed, buildings
constructed, lost and destroyed, and `production_skipped` counts by kind and reason.
The battlefield is fully visible, so enemy losses are faction-visible facts. The
engine reads only these five event types within the remembered ticks.
Memory is part of the frozen, hashed observation; there is no mutable scratchpad,
and attempts never read memory from earlier attempts. A plan is reused only when the
observation hash, prompt and tool versions, and profile, provider, runtime and model
all match. [AI controller comparisons](../developer/ai-comparisons.md) measure these
contracts.

## Decision context

A decision context contains:

- battle, faction, request, target-tick and version identifiers;
- an open battlefield view with all terrain, units, buildings and training queues,
  and only the faction's own gold, upgrades and aggregate production totals;
- current and projected economy values, including `currentBuildings` of
  `maximumBuildings` and `currentUnits` (living plus paid in training) of
  `maximumUnits` when the ruleset sets those limits; projected income reports the
  next gold-mine draw without depleting it;
- legal action kinds, unit and building definitions, construction sites, gold-mine
  sites and owned recruitment sources;
- the currently active plan;
- optional bounded faction memory and retrieved unit knowledge;
- a hash of the observation, excluding request-specific fields.

The opening decision at tick zero is prepared for both factions from castles and
starting gold before either plan is applied. All new decisions use
`battlefield.decision-context.v4`.

### Versioned unit knowledge

Unit knowledge cards are part of the ruleset content
([content libraries](../developer/content-library.md)) and of the scenario content
hash. At each decision window the engine selects cards using only what the faction
can see, then freezes them into the observation before hashing, so API and CLI
delivery read identical knowledge and retries reuse it. Selection never uses
hidden enemy state, needs no external service and does not affect combat or
validation. `engine.knowledgeRetrieval.maxUnitCards` and `maxBytes` bound it
(defaults 16 and 16384), and the persisted context records what was included and
whether it was truncated.

### Unit reference and lookup

The observation also freezes every legal or visible unit definition and its card in
`unitReference`. The model's context lists each legal unit's cost, training ticks, class, role,
strengths and weaknesses, plus `requiresUpgradeId` and `locked: true` for a unit whose
unlock the faction does not own yet; `get_unit_details` returns full base stats and guidance
for a few units at a time, scoped by the request capability to that frozen set.
Hidden and unknown unit IDs both return `available: false`. The tool's description
gives its batch and size limits. Reference content is untrusted data.

### Compact battlefield view

The agent view groups entities by faction and replaces repeated values (identical
stat profiles, visible-cell ranges, an unchanged historical plan) with references,
using each encoding only when it shrinks the output. Complete-map visibility masks
are omitted from current agent context; old partial observations retain their masks. The prompt explains the
encodings. The projection never changes persisted state, validation or hashes, and
older saved observations are projected the same way without rewriting storage.

Model-facing JSON uses deterministic stability ordering instead of alphabetical
top-level keys: schema/mode and knowledge first, faction/battle identity and owned
upgrades next, current state and plans after that, request metadata last. Nested
legal catalogs precede sites and recruitment sources; terrain precedes observed
profiles/state. The MCP response remains one JSON object;
session identity deduplication preserves this order. Only prefetched prompt rendering
splits nested stable leaves into a separate reference block.

The `encoding="tables-v1"` marker enables further lossless model-only
compaction. A table has `fields`, `rows`, and optional `shared` values. Cells follow
column order; null cells and omitted trailing cells mean absent fields. Records
with explicit null fields stay inline. Unit/building catalogs use tables;
upgrade tracks or exclusive groups can use ordered `groups` of tables, retaining
prerequisites, incremental bonuses and catalog order without assuming zero defaults.

Before table encoding, each observed unit, living or in `unitDeaths`, receives a
context-local integer `id`, and unit-valued `attackTargetId` and `healTargetId`
fields use that integer; string targets remain building IDs. `unitDeaths` entries
replace `factionId` with the same `faction` index. Destroyed buildings are omitted
from the projection; faction stats still count them. Building IDs stay complete
because `productionOrders` name them. Numbers are assigned in faction-group order
and never reused within one response; they are not stable across decisions.

`view.unitMap` consolidates every visible unit into one table. `faction` is a
zero-based index into `view.state.factions`; `xy` is position `[x,y]`. All IDs,
profiles, targets, health, movement paths and readiness remain represented.
The map replaces faction `units` lists only when smaller. It stays in the dynamic
block, separate from stable `view.map` terrain. Buildings remain faction-grouped.
`legal.constructionRuns` replaces legal sites with ordered `[y,xStart,xEnd]`
inclusive runs, preserving gaps and duplicates. These authoritative legal sites
cannot be inferred from terrain alone. Submissions still use full IDs and `{x,y}`.

Historical plans may reuse individual exact active-plan fields through
`planFieldsFromActive`; `memoryEconomyShared` supplies economy values identical in
every memory entry. Historical differences remain explicit. Encodings are selected
only when smaller including their keys and version marker; tiny or incompatible
records retain the original shape. All references are local to one frozen response.
Canonical observations, hashes and plan validation use the uncompacted structure. CLI prefetch
checks only transport metadata, allowing compact catalogs without decoding them
as canonical storage structs. Offline round-trip tests reconstruct the original
projection, including large integers, optional fields, unknown values and order.

## Submitted plans

A model or scripted controller can propose:

- a short name and an operator-visible rationale of at most 600 characters, a limit
  the decision prompt and `submit_strategy_plan` schema both state;
- fixed-point attack and defend weights, and target-specific bonuses;
- reserve gold and spend ordering;
- ordered upgrade purchases, including level prerequisites when supplied by the ruleset;
- typed construction orders;
- persistent per-building unit selection and on/off settings (legacy contexts use unit purchases with quantities).

The service validates the decision version, target tick, general plan bounds, and
context-specific units and sites before persisting a plan. The `StrategyPlan`
schema in the [OpenAPI contract](openapi.yaml) gives the field shapes and limits.
API and CLI submissions must be substantive and may not set an action weight to 0,
which the engine reads as the default 1000; a request stops after three rejected
submissions.

Current contexts expose `continuousProduction: true`. Plans use `productionOrders`:
`[{"buildingId":"a:castle","unitType":"vanguard","enabled":true}]` selects a
repeating unit; `[{"buildingId":"a:castle","enabled":false}]` stops new starts.
At most 17 unique sources can be configured per plan; because omitted sources keep
their settings, a faction with more configures the rest in later windows, and
scripted plans stop at 17.
Opening requires an explicit array; `[]` leaves production off. Later omission or
`[]` leaves existing settings unchanged. Every ID must be a living, active owned
`legal.recruitmentSources` entry, and its `trains` roster must support the selected
unit. The service and engine share this frozen legality check. A unit with
`requiresUpgradeId` may be selected only if the faction owns that upgrade or orders it
in the same plan's `upgradeOrders`; otherwise the service reports code `locked` on
`plan.productionOrders` and the engine rejects the plan with the same rule
(`game.ValidateUnitUnlocks`). Legacy nonempty
recruitment orders/priorities are rejected in continuous mode; production orders
are rejected for legacy contexts. Plan schema is v3; MCP tool schema is v8.

Each building pays for one unit at training start, after income and window purchases,
respecting `reserveGold`. It automatically retries if unaffordable. A blocked exit
holds the paid unit without additional charges. Disabling or changing selection
preserves the paid unit; the change affects subsequent purchases. State exposes
`production` (configured, unitType, enabled, waitingForGold, waitingForUnitLimit, waitingForUpgrade), `trainingQueue` (zero or one paid
unit), and `trainingBlocked`. Compact agent observations retain owned production
settings and both sides' paid training progress. Opponent `production` settings are
redacted before freezing the context; physical entities remain visible. Spectators
see both sides' settings in the full battle state. Castles
train the Barracks roster and attack with 15 attack/range 3. New specialized sources
start disabled and become selectable once active; no starting Barracks is granted.

Engine-authored scripted army templates are converted to one compatible selection
per unconfigured active source. Existing selections, including disabled settings,
remain unchanged, except that an enabled source on a type without an unlock moves to
the template's first preferred late-game type once the faction owns its unlock.
Templates select only unlocked types and leave a source unconfigured if its whole
roster is locked. Model factions receive templates only as first-window fallbacks,
before an unlock is affordable. Previous-plan fallback omits selection orders because settings
already persist; it must not reissue IDs of subsequently destroyed sources. Model
instructions explicitly compare more training capacity and unit access with economy,
defenses and upgrades. Empty construction orders remain a deliberate choice; the
engine does not force model expansion. Industry affects economy buildings only.

Frozen contexts with continuousProduction absent/false retain `recruitmentOrders`
(`unitType`, `count`, optional `sourceBuildingId`), six purchases per window and a
12-order FIFO queue. Only its head trains; a blocked exit holds it. Omitted source
IDs select a compatible active building with queue space nearest the enemy castle.
Historical contexts without authored rosters preserve their original validation.
`legal.economySites` is the retained wire name for dynamically
computed legal construction coordinates, not fixed pads. It is empty when the shared
building limit is full, and under contested territory both site lists leave out
ground inside an enemy's territory. When `view.state.goldMines` is present, those sites exclude
mines and every hex beside a mine with gold left, `legal.goldMineSites` lists the mines with gold left where an economy building
may go, and the validator rejects economy orders elsewhere and other structures on
mines. A rejected construction site names the list the order must use and its three
nearest legal coordinates, or says none is left. Model submissions whose construction orders exceed the
remaining `maximumBuildings - currentBuildings` slots, or the remaining economy slots,
are rejected with `limit_reached` and a message naming the counts. Scripted default
construction draws from the same legal sites, so it never orders at a full shared
limit; scripted plans skip the slot-count check because the engine skips extras. Execution rechecks range,
terrain, contested territory, a mine's remaining gold and neighbors, unit occupancy and a minimum building distance of two hexes, so conflicting
later orders skip without charge. Empty `constructionOrders` builds nothing;
omitting it enables default construction. Older plans may use
`recruitmentPriorities`; a plan cannot use both recruitment formats.

## Submission failures and fallback attribution

When a runner fails before the deadline, the engine keeps its error category in the
fallback record instead of replacing it with `timeout`. Malformed tool calls are
logged as `decision_tool_input_rejected` with only the request ID and missing field
names; see [Antigravity troubleshooting](../operations/antigravity-cli.md#missing-plans).
Each rejected submission issue is logged as `decision_validation_rejected` with its
field, code and validator message. Every quoted (`%q`) value, the only way
validators insert submitted text, is masked as `"…"`, text after an unbalanced quote
is dropped, and the result is cut to 240 bytes, so submitted text such as building
types, IDs and upgrade names is never logged. Numbers in the template (coordinates,
counts) and the event's request ID, target tick and expected version remain. The
model still receives the full message. Every submission's `decision_plan_submitted`
log also counts the entries in each plan list (`spend_order`, `construction_orders`,
`construction_priorities`, `production_orders`, `recruitment_orders`,
`recruitment_priorities`, `upgrade_orders`). The `submit_strategy_plan` span records
the same counts as `battlefield.plan.*` attributes and, for a rejection, each issue's
field and code in `battlefield.validation.fields` and `battlefield.validation.codes`,
so Langfuse shows which rules a model broke. Counts, fields and codes carry no
submitted value.

| Error category | Raised by | Meaning |
| --- | --- | --- |
| `timeout`, `refusal`, `rate_limit`, `provider` | API runner | Provider deadline, safety refusal, HTTP 429, or other provider failure |
| `mcp_connect`, `mcp_discovery`, `mcp_tool`, `mcp_context` | API/CLI runner | MCP session, tool-list, tool-call, or CLI context-prefetch failure |
| `unauthorized_tool`, `no_progress`, `step_limit` | API runner | Model requested a tool outside the session set, returned no tool call, or exceeded the profile's step limit |
| `cli`, `cli_permission`, `cli_isolation`, `cli_provider` | CLI runner | Non-success CLI result, denied tool action, sandbox setup failure, or unsupported CLI provider |
| `provider_rate_limited` | CLI runner | The Codex or Claude CLI's terminal error reports HTTP 429, too many requests, or a rate or usage limit; the subscription allowance is exhausted, not the CLI |
| `validation_limit` | Decision service | Third rejected API/CLI submission for the same request |
| `plan_not_submitted` | Agent worker | Runner reported success but MCP holds no accepted plan |
| `profile_unavailable`, `capability`, `provider_unavailable` | Agent worker | Claimed profile is not executable locally, the claim carried no capability, or no runner could be built for the profile |
| `runner` | Agent worker | Runner failed without a more specific category |
| `decision_budget` | PostgreSQL admission trigger | The battle's model execution budget is exhausted at claim time; never for a league battle, which is budget-exempt |
| `ai_disabled_or_budget` | PostgreSQL insert trigger | AI execution was stopped, or the battle's budget was already spent (not for a budget-exempt league battle), when the decision was created; the engine falls back at once |
| `battle_stopped` | Battle stop route / expiry sweep / engine requeue | Battle stopped, expired or requeued by a stopping engine while the decision was open |

Background-generation jobs share the worker and runner categories above except
`validation_limit`, `plan_not_submitted`, `runner`, `decision_budget` and
`battle_stopped`, and add their own:

| Error category | Meaning |
| --- | --- |
| `execution_error` | Runner failed or did not submit, without a more specific category |
| `invalid_output` | Third empty or over-length background submission |
| `timeout` | Execution deadline passed while the job was running |
| `queue_timeout` | Job expired before any worker claimed it (status `expired`) |

## Saved decision context

Historical decision context is scoped by battle, attempt and request. The service
returns only the saved observation's public fields; provider metadata, capability
tokens, private reasoning and tool-call traces are excluded. The displayed context
is a reconstruction, not the original provider payload, and the unit reference
shows what the model could look up, not what it fetched. Rows without a saved
observation return `context: null`. Plans describe intent and events show what
happened; a `running` decision does not prove MCP connectivity.

## Background generation

`internal/generation` admits background-draft jobs durably. PostgreSQL serializes
each actor's admissions and allows one active job per actor; the
[database guide](database.md#admission-triggers) owns the allowance accounting, and
the [HTTP API reference](api.md#faction-background-generation) the client-facing
limits.

Workers reuse the decision infrastructure: they advertise the
`faction_background` job type, battle claims take priority, and a generation job
starts only in a free execution slot. Generation capabilities are a separate kind
that authorizes only `get_background_context` and `submit_faction_background` on
`/generation-mcp`; neither tool sees battle data, and each MCP endpoint rejects the
other kind. The AI gateway's database role can read and update generation jobs but
not factions or battle state.

The writer is configured in `ai.backgroundGeneration` in `backend/config.yaml` or
the deployment ConfigMap; the deployed defaults are:

```yaml
ai:
  backgroundGeneration:
    enabled: true
    profileId: codex-subscription-local
    dailyAllowance: 10
    cooldown: 30s
    queueTimeout: 5m
    executionTimeout: 2m
```

The profile, independent of battle models, uses its existing model, credentials,
version check and availability, so the default writer needs its CLI profile enabled
and a connected worker. An unavailable profile rejects admission before quota is
reserved, and a job whose worker goes offline stays queued until it expires. API
execution allows at most three provider turns; CLI execution is told to use three
and capped at three invalid submissions, within the same deadline. Only a valid,
server-accepted tool submission succeeds, and there is no automatic model
fallback. With generation disabled, players can still write backgrounds by hand.
Provenance uses a `faction-background-v2` digest of the rendered instructions.

### Upgrade-level planning

The frozen legal upgrade catalog includes optional `track`, `level` and
`requiresUpgradeId`. All catalog levels are discoverable, including levels whose
prerequisites are not yet owned. A prerequisite must be owned or ordered earlier
in the same plan. An upgrades-only plan is a substantive decision. Both AI
submission validation and the engine enforce purchase ordering. Duplicate IDs
in one plan are invalid; IDs already owned may be requested again without cost.
Multiple levels can execute in one window, subject to reserve gold and ordered
affordability. Ruleset knowledge supplies prices and effects; the shared prompt
explains ordering without hardcoding a particular content version.

Unlock upgrades (core v8 `gunpowder` and `knighthood`) carry no bonus. The prompt
tells the model that a `locked` unit needs its `requiresUpgradeId` owned or ordered,
and that a building selecting it waits without charge
(`production.waitingForUpgrade`) until the upgrade is bought. Upgrades run before
automatic purchases, so an unlock and its first unit can start in one window.

Specialist choices expose `targetUnit`/`targetClass` and `exclusiveGroup` alongside
incremental effects. Ownership locks out the paired alternative for the battle.
All maps expose the same legal content. Unit reference data includes Scout retreat,
cavalry preference, armor penetration and repair capabilities; remaining repair
charges stay on the individual unit observation. Repair events and shared support
markers remain subject to normal faction visibility.

## Model selection and latency

Battle creation accepts `modelIdentifier` and `reasoningEffort` per model-controlled
faction. A profile's `modelOptions` allowlists models and their supported
`reasoningEfforts`; omitted choices retain the profile model and model-default
effort (or the option's explicit `defaultReasoningEffort`). The backend validates
both fields, freezes them in the scenario and decision row, and includes effort in
retry-reuse matching. Workers validate against their own allowlist before execution;
a removed option fails as `model_selection_unavailable`, rather than substituting
another model. API profiles and Antigravity expose no effort controls.
The setup page never leaves effort implicit: when the chosen model lists efforts
it preselects `low` (or the first listed effort when `low` is absent) and submits
it with the model identifier. API clients that omit both fields still get the
profile default.
API profile model labels remain informational: API runners use their current
configured environment model, preserving behavior across secret rotation.
Codex receives `model_reasoning_effort`; Claude receives `--effort`. The CLI model
identifier is the requested identifier; aliases such as `sonnet` can resolve to a
new provider version and are not an immutable model-version guarantee.

The shipped CLI catalog offers every model the Codex subscription lists:
`gpt-6.1-sol`, `gpt-6.1-terra`, `gpt-6-astra`, `gpt-6-sol`, `gpt-6-luna`,
`gpt-5.6-sol`, `gpt-5.6-terra` and `gpt-5.6-luna` (the published names are, for
example, GPT-6 Luna). The GPT-6.1 models need Codex 0.159 or newer. Claude offers
`haiku`, `sonnet`, `opus` and `fable`, and Antigravity `gemini-3.8-flash-low`,
`-medium` and `-high`. Codex models, Sonnet, Opus and Fable offer low/medium/high
effort; higher efforts are left out so decisions finish within the decision
timeout. Haiku uses model-default effort because the alias can resolve to a model
without effort support on older supported CLIs. Antigravity's Flash variants encode
reasoning in the model ID. Profile defaults are `gpt-5.6-luna`, `sonnet` and
`gemini-3.8-flash-medium`. Battle and league setup list model profiles under
separate API and CLI headings.
See the provider catalogs for [OpenAI](https://developers.openai.com/api/docs/models/gpt-6-luna),
[Claude](https://code.claude.com/docs/en/model-config) and
[Antigravity](https://antigravity.google/docs/models). Subscription availability
and usage allowances depend on the account; these choices do not assert API prices.

The claim request carries `selectionVersion: 1`. An older worker receives HTTP 409
before leasing a job, preventing it from silently ignoring frozen choices. See
[worker rollout ordering](../operations/cli-worker.md#model-selection-rollout).

Context timestamp recording is best-effort: a failed timing write is logged and
the frozen context remains available; its missing timing stays unknown.

The decision inspector shows request-to-resolution elapsed time, queue wait,
claim-to-first-context and claim-to-resolution timings. Acceptance ends the main
wait measurement immediately; `runnerCompletedAt` records the gateway receipt of
the later worker report. Process-launch milliseconds measure only OS process
creation, not provider readiness. Claim-to-context includes CLI initialization,
provider activity and MCP work, and is not pure inference or reasoning time.
A timestamp a record lacks stays unknown. A lease reclaim resets claim/context
and completion timestamps; total wait still starts at the original request.

## CLI context prefetch

Codex and Claude battle profiles prefetch context by default; the worker-only
`prefetchContext: false` setting selects tool-fetched context; an absent setting
means prefetch. The worker calls `get_decision_context` with the same
request capability before launching the CLI, verifies the request identity,
version/tick and 64 KiB limit, and embeds the frozen JSON as untrusted data. The
model is instructed to use that snapshot without fetching it again; submission
and optional unit-detail tools retain the same capabilities and validation.
Prefetch failure stops execution as `mcp_context`; it never launches with missing
or mismatched context. Antigravity
always prefetches, regardless of this option.

Each CLI result stores `contextDelivery` (`tool` or `prefetch`) in provider metadata,
exposed through decision history and the timing panel; the generation span has
`battlefield.context_delivery`; a record without it has unknown delivery. With prefetch,
`contextServedAt` precedes CLI launch: compare request/claim-to-resolution and
success/fallback rates across modes, not just claim-to-context time. Model,
effort, scenario, concurrency and prompt revision must be recorded for comparisons.
The option does not promise a latency reduction; provider variance and prompt
cache state affect results. See the [test procedure](../operations/cli-worker.md#context-prefetch-comparison).
