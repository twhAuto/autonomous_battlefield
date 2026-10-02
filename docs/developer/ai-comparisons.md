# AI prompt and memory comparisons

## Shared prompts

The [architecture contract](../architecture/system-architecture.md#shared-prompt-provenance-and-faction-memory)
describes shared prompts, provenance, and bounded faction memory. The active
embedded templates live in `backend/internal/ai/prompts/`; each purpose has one current file, with older revisions kept in Git.
The bundle version and content digest are maintained in `internal/ai/prompts.go`. Rebuild backend binaries after editing active templates,
and restart host agents so recorded prompt digests match the deployed policy.
CLI tools may add their own internal instructions.

## Faction memory

In a running or saved battle, select a player in the sidebar’s **Inspect** dropdown and open
**Decisions**. Expand a decision to compare its rationale, orders, and fallback
provenance. Expand **Saved decision context** to load the historical economy,
visible forces, legal choices, prior plan, memory, and knowledge. The observation
tick is distinct from the plan's target tick and the current snapshot displayed
in the player header. Missing legacy observations are labeled explicitly.
**Faction** displays background/doctrine saved at battle creation, even if the
current faction profile has changed or been deleted. **Units** shows the current
living army grouped by type; expand a group for individual HP and coordinates.

Use **System** for comparison rows and the event type filter. Expand **Inspect
event** to check execution: an order does not prove that purchases were fulfilled.
Event filters apply to loaded history only. The event attempt selector does not
change player resources or decision history, which show the current attempt.

Roll out the backend before the frontend: the additive participant fields and
context endpoint require no database migration. During version skew, missing
profile fields and unavailable context appear locally in the inspector and do
not prevent inspecting the rest of the battle.

Memory observations precede their associated plans; they do not measure the
plans' effects. Compare later observations and current context when assessing
changes. Historical names and rationales are untrusted data, and current legal
choices take precedence. A `faction_memory_unavailable` log means the optional
history read failed and the decision proceeded without memory. Use the ablations
below to compare memory with an otherwise matched configuration.

## Free verification

From `backend/`:

```bash
go run ./cmd/aieval -mode compare \
  -comparison-config evals/comparison.yaml \
  -cases evals/comparison-cases \
  -corpus-version 2026-09-06.comparison-v1 \
  -repetitions 3 -max-steps 4 -max-tokens 1024 -timeout 30s \
  -json ../artifacts/ai-eval/comparison.json
```

The checked-in configuration compares a scripted baseline, a variant without
memory, and a variant without knowledge. All three traverse the production API runner, loopback MCP server, and
submission validator. This is a free integration check, not evidence that memory
improves a real model's strategy.

## Live comparisons

Create a separate variant configuration, for example:

```yaml
variants:
  - id: gpt
    provider: openai
    model: YOUR_REVIEWED_API_MODEL
  - id: codex
    provider: codex-cli
    model: YOUR_INSTALLED_CODEX_MODEL
  - id: antigravity
    provider: antigravity-cli
    model: YOUR_INSTALLED_ANTIGRAVITY_MODEL
```

Supported providers are `scripted`, `openai`, `gemini`, `codex-cli`,
`antigravity-cli`, and `claude-cli`. Set `withoutMemory: true` on a variant for a controlled memory
ablation. Use explicit model identifiers. API models must appear in the reviewed
`evals/pricing.yaml` snapshot and their provider credentials must be available.
CLI variants use the existing host login and the production runner's isolation.

Live execution requires `AI_EVAL_ALLOW_LIVE=1`; API variants additionally require
`AI_EVAL_ALLOW_PAID=1`. Invoke the same command with your configuration file.
Do not put credentials into YAML or reports.

Comparisons accept at most 20 cases, 8 variants, and 10 repetitions, with a
per-sample timeout of at most five minutes. API turns and output tokens use the
specified limits. CLI subprocesses have the per-sample deadline and submission
rejection limit, but their internal turns/tokens are not controlled by these
API flags. Reports state this difference; do not describe such a comparison as
having identical token budgets. CLI subscription usage has no API cost estimate.

## Reading reports

Schema `battlefield.ai-comparison.v1` records the prompt digest, corpus version,
variant/provider/model/runtime, memory mode, budgets, repetition, context hash,
validation failures, acceptance, latency, reported usage/cost, and tactical plan
fingerprints. Names, rationales, and request metadata do not affect tactical
diversity. Raw observations, prompts, capabilities, and provider output are
excluded. Submission attempts are recorded in each sample's `toolCalls`.

Each sample uses a fresh runner and isolated decision repository. Cases and
repetitions are matched across variants; execution order rotates by repetition.
The harness checks the repository for acceptance, so a successful CLI exit
without a valid submission fails. A failed sample is retained in JSON and makes
the command exit unsuccessfully. Comparison mode produces JSON; the existing
`deterministic` and `provider` modes retain their JSON/JUnit reports.

Compare context hashes and rules versions when matching reports. Archive each
report with the source revision to compare prompt revisions across builds.
Different memory modes intentionally have different context hashes.

Acceptance and invalid-plan rates measure contract correctness, not battlefield
strength. Win rate is null because this harness evaluates individual decisions,
not complete matches. Use full battle outcomes in `battle_controller_evaluation`
for win rates; do not interpret a successful decision as a win. Zero aggregate
estimated cost can mean missing pricing/CLI usage, not a free live run.

## Troubleshooting a missing Antigravity plan

`plan_not_submitted` means the CLI finished without a plan accepted by MCP.
It does not by itself mean the decision deadline expired. SDK schema rejection
can occur before the plan handler: `decision_tool_input_rejected` now logs the
authenticated request ID, missing required field names, and whether a nested
`plan` wrapper was supplied. It never logs argument values or raw error text.

All workers receive explicit instructions to submit a flat tool-arguments object,
including every required field. Unused objects/arrays must be `{}`/`[]`, not
omitted or null. A written plan or CLI SUCCESS is insufficient; the MCP tool
must return `accepted=true`. The existing three-submission repair limit remains.
The engine preserves the original runner failure category when applying a
fallback; unresolved decisions at the deadline still use `timeout`.

After changing embedded prompts, rebuild/restart the host agent. Deploy the
backend to pick up MCP diagnostics and engine fallback attribution. For dev,
run `./deploy-local.sh`, then stop and restart `./run-k8s-cli-agent.sh` in its
terminal before starting a new battle.

## Unit knowledge retrieval

Rulesets may include a `knowledge` section with `mechanics` and `cards`. Each card
has `unitId`, `role`, `strengths`, `weaknesses`, and `guidance`. Knowledge-enabled
rulesets require exactly one card per unit; legacy rulesets may omit knowledge.
Mechanics text is limited to 4096 UTF-8 bytes and each descriptive field to 1024.
Numerical card facts are generated from unit definitions. Current observed unit
stats may include upgrades; tactical text is advice, not a guaranteed counter.

To add a unit, add its numerical definition and card to a versioned content library, select it in the ruleset, and use
existing movement, targeting, and combat mechanics. Publish a new ruleset ID and
version and a scenario referencing it when changing a published roster. Existing
faction doctrines can be reused across rulesets. No engine switch or UI
unit list needs editing. New abilities still require engine implementation.
Library references and scenario restrictions are resolved before knowledge and its ruleset are copied into the persisted scenario at battle
creation. Editing catalog files cannot alter an existing battle snapshot.

Engine YAML (also available in the deployment ConfigMap):

```yaml
engine:
  knowledgeRetrieval:
    maxUnitCards: 16
    maxBytes: 16384
```

Both settings must be positive. Omitted settings use the defaults above; values
above eight are supported. The byte budget includes mechanics, cards, and metadata.
If even metadata cannot fit, optional knowledge is omitted. If mechanics cannot
fit, it is omitted and truncation is recorded. Otherwise retrieval preserves
priority order and stops before a whole card would exceed either budget.

Selection uses visible enemy types, own deployed types, explicit recruitment
order types, legacy recruitment priorities, then other legal units by increasing cost, with unit ID breaking
ties. It never uses hidden enemy state. Legal unit base statistics are available through optional lookup from saved reference data.
Selection is performed once before decision hashing; persisted contexts include
source IDs, a ruleset-content digest, effective limits, serialized size, and
truncation. MCP and inline CLI delivery use the same versioned projection of the stored context. Engine
configuration changes affect newly prepared decisions after restart; retries
reuse persisted decisions.

Comparison YAML accepts the same `knowledgeRetrieval` block at the top level.
Variants may set `withoutKnowledge: true`, independently of `withoutMemory`.
Cases can supply `knowledgeRuleset` to prepare context using the production
selector. The large-roster fixture contains 23 units; it exercises selection
without changing the production roster. Comparison reports include retrieval
metadata, never full knowledge text. Run the existing compare command with the
same corpus and different configuration files to compare budgets.

Free scripted comparisons validate integration, not tactical improvement. Live
comparisons remain opt-in; full battles are needed for outcome claims. This
worker-context feature does not complete AB-76's separate rules-coach experiment.

## Explicit unit purchases

New model decisions use `recruitmentOrders`, for example:

```json
{
  "reserveGold": 20,
  "spendOrder": ["recruitment", "construction"],
  "recruitmentPriorities": [],
  "recruitmentOrders": [
    {"unitType": "ranger", "count": 2},
    {"unitType": "vanguard", "count": 1}
  ]
}
```

These are requested purchases for each production window, not army composition
ratios. Each order has a positive integer count; at most six units total may be
requested, matching the six possible castle-adjacent spawn positions. The engine
buys in order, respects reserve gold, and stops at the first unaffordable unit or
unavailable spawn. There is no substitution, extra purchasing, or outstanding
queue. Reassess unmet quantities at the next decision. If an existing plan is
reused, its quantities apply again for that production window.

An explicit empty `recruitmentOrders: []` buys nothing. Absence/null preserves the
legacy repeated-first-affordable priority behavior for old saved/scripted plans.
Nonempty priorities and explicit orders together are rejected. Omitting recruitment
from a nonempty spend order also skips buying. Construction and income timing are
unchanged; recruits can act on the following tick. The decision panel displays
requested quantities, while `unit_recruited` events record actual purchases.

MCP tool schema is now `battlefield.mcp-tools.v7`; existing clients may omit the
optional orders field. Both model providers share the updated purchasing prompt.
Deploy API, MCP, engine, and agent together and restart the host CLI agent so tool
schemas and prompts agree. Existing battle JSON requires no database migration.

See [content libraries and scenario selections](../operations/content-library.md) for shared units,
buildings, upgrades, roster filtering, and version publication.

For expanded scenarios, recruitment orders may specify `sourceBuildingId` from
`legal.recruitmentSources`; omitted sources use the castle. The six-unit limit is
shared across sources. Unavailable explicit sources and blocked Barracks orders
are skipped without substitution; blocked castle spawns or an unaffordable order
stop remaining purchases. Typed `constructionOrders` select building definitions
from `legal.buildings` at `legal.economySites`. See
[Frontier Siege](../architecture/frontier-siege.md) for the new mechanics and balance probes.

## Compact unit context

The current agent format uses `view.state.factions[].units` and `.buildings`
instead of flat state entity arrays. Ownership comes from the containing
`factionId`; faction counters are under `stats`. Building profiles are optional
and resolve through `view.buildingProfiles`; inline building stats remain valid.
Consumers should not expect per-entity faction IDs in this agent projection.
Full IDs still work for recruitment and combat references. Stored observations
and spectator APIs keep their original structure. Deploy backend changes and
restart host CLI agents for updated embedded instructions.

Identical memory plans reference `#/activePlan`. Omitted state battle ID/tick and
own-faction gold/upgrades inherit the matching root values described in the
prompt. API/Gemini delivery removes identity text already supplied verbatim in
the system prompt; direct MCP/Codex and inspector JSON retain it. The reviewed
11,883-byte sample becomes 11,235 bytes, with another 704 bytes removed during
API/Gemini delivery. These measure context bytes only, excluding the prompt's
reference instructions and provider/CLI scaffolding; use reported total input
tokens to measure net savings. Shortened authored guidance affects newly frozen
snapshots; existing references never consult edited content.

All decisions use one current compact format, identified as
`battlefield.decision-context.v4` for diagnostics. There are no legacy response
branches or format rollout flags. Deploy the backend and frontend normally and
restart host CLI agents to pick up embedded prompt changes. Stored observations
and validation hashes are never rewritten. The inspector reconstructs the current
format from saved data under “Reconstructed agent context”; it is not a recording
of original input. The inspector uses section labels without explanatory notices.

The reviewed tick-200 sample measured 36,166 bytes before and 26,187 after these
two encodings (9,979 bytes / 27.6% saved), excluding formatting whitespace. This
does not establish token or decision-quality savings: compare complete reported
input usage, latency, lookups, acceptance, fallback and existing quality metrics.

Compact responses contain all recruitable unit overviews and a separate list for
visible non-recruitable types. They retain mechanics and current battlefield
stats. `get_unit_details` takes `requestId` and 1–4 distinct `unitIds`, returning
frozen base stats and guidance. Missing guidance does not hide stats. Unknown and
out-of-scope IDs both return unavailable. The 1800-byte JSON result limit returns
an explicit error for oversized batches; retry with fewer IDs within existing
turn/time limits. For long guidance, copy `nextGuidanceOffset` into a subsequent
call's `guidanceOffset` with the same single unit ID. Offsets refer to UTF-8 bytes in
the frozen guidance; each page retains complete base stats. This explicit
continuation avoids silent truncation and CLI file offloading. Current effective
stats take precedence over base rules.

Reference data is frozen before hashing and does not depend on the automatic
knowledge card count/byte limits. Those existing limits still govern stored
legacy knowledge and mechanics selection. No vector database or external search
service is involved.

All comparison variants use the current projection. Use cases with
`knowledgeRuleset` or a saved unit reference. `withoutKnowledge` also strips optional card guidance from
the reference copy without mutating the baseline. Reports add `contextBytes` and
`unitDetailCalls` per case; aggregates include `totalInputTokens`,
`unitDetailCalls`, and `lookupRate` (fraction of cases that fetched details).
Provider-versus-scripted comparisons include input-token and lookup-rate deltas.
Scripted token counts are fixture values, not measured provider savings. CLI usage
is limited to what that CLI reports. Decision acceptance is not a win-rate measure.

From the repository root:

```bash
(cd backend && go test ./internal/ai -run TestCompactContextSizeByBattleStage -v)
./scripts/test/with-test-postgres.sh ./scripts/test/test-go-coverage.sh
(cd frontend && npm test -- --coverage)
(cd frontend && npm run build)
```

The synthetic opening/midgame/large fixtures retain identical battlefield data
and assert reduced catalog bytes and non-growing complete initial responses.
Their initial bytes were 4036→1556, 8592→6112, and 42308→39828 respectively when
introduced. These are serialized JSON measurements, not token or cost estimates.

The opt-in real CLI transport check uses only synthetic data and the existing
subscription login. It attempts four unit details, retries a smaller batch when
needed, and follows guidance continuation. The accepted rationale must include a
verification word present only at the end of fetched guidance:

```bash
(cd backend && BATTLEFIELD_LIVE_AGY=1 go test ./internal/ai/mcpserver -run '^TestAntigravityLiveDecision$' -v -count=1)
```

The inspector's **Initial agent context** is the initial projection. **Unit details
available on demand** shows stored reference data, not a history of actual tool
reads. Legacy decisions retain their historical inspector presentation.

New per-case reports also retain the runner's `usage` object: its scope, reported
input/output, optional cached/reasoning subsets, model-call count and CLI-invocation
count. Existing top-level totals remain compatible. A missing optional counter
means unavailable, not zero. CLI totals aggregate the CLI session, while API
counts sum reported model attempts; neither is an initial-context token count.
Use [Langfuse](../operations/langfuse.md) for production model/tool timing and context section
bytes alongside these isolated decision-quality comparisons.
