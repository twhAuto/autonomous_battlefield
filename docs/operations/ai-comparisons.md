# AI prompt and memory comparisons

## Shared prompts

All API and CLI workers use the Git-versioned templates in
`backend/internal/ai/prompts/`. `prompts.go` embeds the shared role, decision
instructions, and the two context-delivery instructions. `PromptVersion`
contains `battlefield-agent-v3` and a SHA-256 digest of all four templates, so
editing any instruction changes the provenance recorded on decisions and reports.
Rebuild the backend binaries after editing a template. The v2 file is retained
as historical source; it is not used by the current workers.

API workers and Codex fetch context with MCP. Antigravity receives the same
shared instructions with host-prefetched context because its CLI offloads large
MCP results to files. Both model runtimes reject placeholder plans and close a
decision after three rejected submissions. CLI tools may supply their own
internal instructions in addition to the application's templates.

## Faction memory

Each frozen decision context includes up to three prior resolved decisions from
the same battle, attempt, and faction, ordered oldest first. Each entry contains
the observation tick, decision target tick, accepted/fallback status, historical
economy, own/visible enemy unit counts, and the resolved plan. These observations
precede the associated plan; they are not measurements of that plan's effects.
Compare them with later entries and current context to assess changes.

The database query scopes and limits records before retrieval. Memory is capped
at 8 KiB; whole oversized entries are omitted. It contains no nested memory,
raw provider output, or another faction's private observations. Historical plan
names and rationales remain untrusted data. Current context and legal choices
always take precedence. There is no independent model-written scratchpad.

Memory is reconstructed from decision rows, then persisted inside the next
observation JSON. No schema migration is needed. Failed reads emit
`faction_memory_unavailable` and allow the current decision to proceed without
memory. New attempts start with no history from earlier attempts. The memory
participates in the observation hash; retry reuse also requires matching prompt,
tool, profile, provider, runtime, and model identifiers.

## Free verification

From `backend/`:

```bash
go run ./cmd/aieval -mode compare   -comparison-config evals/comparison.yaml   -cases evals/comparison-cases   -corpus-version 2026-09-06.comparison-v1   -repetitions 3 -max-steps 4 -max-tokens 1024 -timeout 30s   -json ../artifacts/ai-eval/comparison.json
```

The checked-in configuration compares a scripted provider with and without
memory. Both traverse the production API runner, loopback MCP server, and
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

Supported providers are `scripted`, `openai`, `gemini`, `codex-cli`, and
`antigravity-cli`. Set `withoutMemory: true` on a variant for a controlled memory
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

To add a unit, add its numerical definition and card to the ruleset YAML, using
existing movement, targeting, and combat mechanics. Publish a new ruleset ID and
version and a scenario referencing it when changing a published roster. Existing
faction blueprints remain associated with their ruleset. No engine switch or UI
unit list needs editing. New abilities still require engine implementation.
Knowledge and its ruleset are copied into the persisted scenario at battle
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

Selection uses visible enemy types, own deployed types, current recruitment
priority order, then other legal units by increasing cost, with unit ID breaking
ties. It never uses hidden enemy state. All legal unit statistics remain present.
Selection is performed once before decision hashing; persisted contexts include
source IDs, a ruleset-content digest, effective limits, serialized size, and
truncation. MCP and inline CLI delivery use the same stored context. Engine
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

MCP tool schema is now `battlefield.mcp-tools.v3`; existing clients may omit the
optional orders field. Both model providers share the updated purchasing prompt.
Deploy API, MCP, engine, and agent together and restart the host CLI agent so tool
schemas and prompts agree. Existing battle JSON requires no database migration.
