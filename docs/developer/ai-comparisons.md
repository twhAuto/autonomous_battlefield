# AI controller comparisons

`aieval -mode compare` runs matched, repeated decisions for different controller
variants through the production runner, a loopback AI gateway tool server and the
submission validator, then reports how each variant behaved. This guide covers
running comparisons and reading their reports. The
[decision contracts](../architecture/ai-decisions.md)
own the prompt, memory, knowledge and context behavior being compared.

## Before comparing

The embedded prompt templates live in `backend/internal/ai/prompts/`; each purpose
has one current file, with older revisions kept in Git, and `prompts.go` computes
the version and digest that reports record. Rebuild backend binaries after editing
templates, and restart host agents so recorded prompt digests match the deployed
policy. CLI tools may add their own internal instructions.

To inspect a single decision in a running or saved battle, use the battle
inspector's decision history and saved context; see the
[frontend guide](frontend-guide.md#inspector-and-historical-context).

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
memory, and a variant without knowledge. All three traverse the production API
runner, loopback AI gateway tool server, and submission validator. This is a free
integration check, not evidence that memory improves a real model's strategy.

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
`antigravity-cli`, and `claude-cli`. Use explicit model identifiers. API models
must appear in the reviewed `evals/pricing.yaml` snapshot and their provider
credentials must be available. CLI variants use the existing host login and the
production runner's isolation.

Live execution requires `AI_EVAL_ALLOW_LIVE=1`; API variants additionally require
`AI_EVAL_ALLOW_PAID=1`. Invoke the same command with your configuration file.
Do not put credentials into YAML or reports.

For a context-delivery comparison, use two Codex or Claude variants with the same
`model` and `reasoningEffort` (for example `low`), and set `prefetchContext: true`
on only one. Omitted prefetch is false; Antigravity always prefetches. The runner
validates reasoning effort against the provider's supported values. These options
are rejected for API and scripted variants. Keep the corpus and memory/knowledge
settings identical. Reports record both options, and `durationMs` includes host
prefetch, CLI startup and completion; it is not the battle's claim-to-resolution
interval. See the [cluster comparison procedure](../operations/cli-worker.md#context-prefetch-comparison)
for measurements from real battles.

Comparisons accept at most 20 cases, 8 variants, and 10 repetitions, with a
per-sample timeout of at most five minutes. API turns and output tokens use the
specified limits. CLI subprocesses have the per-sample deadline and submission
rejection limit, but their internal turns/tokens are not controlled by these
API flags. Reports state this difference; do not describe such a comparison as
having identical token budgets. CLI subscription usage has no API cost estimate.

## Memory and knowledge ablations

Set `withoutMemory: true` on a variant for a controlled memory ablation, and
`withoutKnowledge: true` to drop retrieved knowledge and the optional card guidance
in the unit reference, independently of each other and without changing the
baseline. The comparison YAML accepts the same top-level `knowledgeRetrieval`
block as the engine, and a case can set `knowledgeRuleset` to prepare its context
with the production selector. The large-roster fixture in `evals/comparison-cases/`
contains 23 units, exercising selection without changing the production roster.
Run the same corpus with different configuration files to compare budgets.

Memory observations precede their associated plans; they do not measure the plans'
effects. Historical names and rationales are untrusted data, and current legal
choices take precedence. A `faction_memory_unavailable` log means the optional
history read failed and the decision proceeded without memory.

## Reading reports

Schema `battlefield.ai-comparison.v1` records the prompt digest, corpus version,
variant/provider/model/runtime, memory mode, budgets, repetition, context hash,
validation failures, acceptance, latency, reported usage/cost, and tactical plan
fingerprints. Names, rationales, and request metadata do not affect tactical
diversity. Raw observations, prompts, capabilities, provider output and full
knowledge text are excluded; retrieval metadata is included. Submission attempts
are recorded in each sample's `toolCalls`.

Each case also records `contextBytes` and `unitDetailCalls`; aggregates include
`totalInputTokens`, `unitDetailCalls` and `lookupRate` (the fraction of cases that
fetched unit details), and provider-versus-scripted comparisons include input-token
and lookup-rate deltas. Scripted token counts are fixture values, not measured
provider savings. Each case keeps the runner's `usage` object: its scope, reported
input/output, optional cached/reasoning subsets, model-call count and
CLI-invocation count. A missing optional counter means unavailable, not zero. CLI
totals aggregate the CLI session, while API counts sum reported model attempts;
neither is an initial-context token count.

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
estimated cost can mean missing pricing/CLI usage, not a free live run. Use
[Langfuse](../operations/langfuse.md) for production model and tool timing.

## Related checks

From `backend/`:

```bash
go test ./internal/ai/agentview -run TestCompactContextSizeByBattleStage -v
```

checks that the synthetic opening, midgame and large contexts keep identical
battlefield data while the compact encodings do not grow the initial response.
The opt-in real-CLI transport checks (`BATTLEFIELD_LIVE_AGY`, `BATTLEFIELD_LIVE_CODEX`,
`BATTLEFIELD_LIVE_CLAUDE`) use only synthetic data and the existing subscription
login; see the [Antigravity](../operations/antigravity-cli.md) and
[Claude](../operations/claude-cli.md) guides. Run the
[coverage gates](ci.md#coverage-gate-details) before declaring a change complete.

For Codex, run from `backend/` (uses subscription allowance):

```bash
PATH="$HOME/.local/bin:$PATH" BATTLEFIELD_LIVE_CODEX=1 go test ./internal/ai/mcpserver -run '^TestCodexLiveDecision$' -count=1 -v
```
