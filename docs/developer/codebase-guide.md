# OrdinalFrontier Codebase Guide

Use this guide to find where code lives and where to start a change. For runtime
boundaries, data flows, durability and security, see the
[system architecture](../architecture/system-architecture.md). The focused guides
own the detail:

- [Local setup](local-setup.md) and [continuous integration](ci.md): development environment and verification.
- [Backend guide](backend-guide.md): package ownership, persistence, configuration and AI execution.
- [Frontend guide](frontend-guide.md): feature ownership, shared contracts and styles.
- [Tooling guide](tooling-guide.md): scripts, test layout, platform automation and CI implementation.

On this page: [repository map](#repository-map) · [executables](#executable-entry-points) ·
[infrastructure](#infrastructure-and-delivery-entry-points) · [change paths](#common-change-paths) ·
[reading order](#suggested-reading-order) · [contribution workflow](#contribution-workflow)

## Repository map

```text
autonomous_battlefield/
├── .agents/skills/             # Canonical ticket-linking, PR, CI, release, docs, and backlog skills
├── .claude/skills/             # Links to the canonical skills for Claude Code
├── .github/pull_request_template.md # PR sections, including the required AI-assistance record
├── backend/
│   ├── cmd/{server,engine,aigateway,agent,aieval}/ # Startup and command wiring
│   │   └── internal/startup/   # Flags, tracing, listener and pool wiring shared by cmd/*
│   ├── internal/
│   │   ├── battle/{api,service,catalog,worker,repo,resultcache}/
│   │   ├── faction/{api,service,repo}/
│   │   ├── game/               # Deterministic state transitions
│   │   │   └── controller/     # Infrastructure-free controller contracts
│   │   ├── ai/                 # Shared decision contracts; subpackages for capability tokens,
│   │   │                       # validation, profiles, prompts, model views, runners, providers,
│   │   │                       # the AI gateway server, the agent worker and evaluations
│   │   ├── admin/              # Model-access handler, service, and repository
│   │   ├── members/            # Player membership and reconciliation
│   │   ├── generation/         # Faction background generation jobs and quotas
│   │   ├── apierror/           # Stable JSON error envelope and codes for /v1
│   │   ├── observability/      # logging, metrics, tracing, requestid, redact (leaf packages)
│   │   └── ...                 # app, authn, config, httpapi, postgres
│   ├── test/                   # Cross-package tests and fixtures; no runtime code
│   │   ├── apicontract/        # openapi.yaml stays in step with served routes
│   │   ├── boundaries/         # Forbidden dependencies and the generated component map
│   │   ├── conventions/        # Source checks, such as span errors going through tracing.RecordError
│   │   ├── integration/        # End-to-end API and engine run against PostgreSQL
│   │   └── performance/        # k6 scenarios and query-plan SQL fixtures
│   ├── migrations/             # Embedded SQL migrations and their PostgreSQL tests
│   ├── gamedata/               # Game content embedded in the server: content libraries,
│   │                           # rulesets and scenarios, plus their bundle digest
│   └── evals/                  # Evaluation cases and pricing configuration
├── frontend/src/
│   ├── app/                    # Application shell, routes, integration tests
│   ├── features/{battles,factions,auth,admin}/ # Feature pages, API clients, unit tests
│   ├── shared/{api,domain,components,hooks,styles}/ # Shared contracts, visuals, and foundations
│   ├── pages/                  # General pages, including Home
│   └── test/                   # Shared test setup
├── infra/
│   ├── deploy/                # Kubernetes bases, overlays, policy, supply-chain and CLI VM assets
│   ├── ansible/               # Ubuntu host foundation, Vault inputs, control dependencies
│   ├── libvirt/               # Pinned Terraform VM, network and retained disks
│   ├── k3s/                   # Cluster settings and pinned platform assets
│   ├── jenkins/               # Auxiliary pipelines, job definitions and plugin pins
│   ├── local/                 # Standalone PostgreSQL, optional Redis and DBHub
│   ├── sonarqube/             # Local analysis service
│   └── aws/                   # AWS provisioning with Terraform
├── scripts/
│   ├── infra/                  # Server registration, inspection and Ansible entry point
│   ├── dev/                    # Local application and host-agent launchers
│   ├── test/                   # Test runners, deployment and runtime assurance script tests
│   ├── ci/                     # Lint, builds, coverage summaries, image assurance
│   ├── cd/                     # Deployment, smoke checks, rollback
│   ├── perf/                   # k6 launcher and PostgreSQL query-plan measurement
│   ├── security/               # Security-test launcher
│   ├── ops/                    # Operational reports and failure drills
│   ├── review/                 # Codex/Claude cross-review runner
│   └── lib/                    # Shared script functions
└── docs/{game,developer,architecture,operations}/
```

## Executable entry points

The backend has five commands in one Go module: four runtime processes and the offline AI evaluation command. The application image contains the four runtime binaries.

| Process | Entry point | What the entry point owns |
| --- | --- | --- |
| API | [`backend/cmd/server/main.go`](../../backend/cmd/server/main.go) | Flags, configuration, migrations, PostgreSQL startup, catalog loading, application wiring, HTTP lifecycle |
| Engine | [`backend/cmd/engine/main.go`](../../backend/cmd/engine/main.go) | Configuration, PostgreSQL startup, worker construction, health server, signals, graceful requeue/shutdown |
| AI gateway | [`backend/cmd/aigateway/main.go`](../../backend/cmd/aigateway/main.go) | Restricted repository construction, PostgreSQL notification listener, MCP tool and agent dispatch routes, HTTP lifecycle |
| Agent | [`backend/cmd/agent/main.go`](../../backend/cmd/agent/main.go) | Profile registry, decision worker, health and drain-status endpoints, `-check-config`, drain/resume on `SIGUSR1`/`SIGUSR2` ([`drain_unix.go`](../../backend/cmd/agent/drain_unix.go)), and a shutdown that waits up to 20 seconds for in-flight jobs to report |
| AI evaluation | [`backend/cmd/aieval/main.go`](../../backend/cmd/aieval/main.go) | Evaluation case execution, pricing inputs, reports, and runtime provenance via `-mode metadata` |

`cmd` packages should remain thin. Domain and use-case behavior belongs under `internal`; entry points should focus on composition and process lifecycle.
[`backend/cmd/internal/startup`](../../backend/cmd/internal/startup) holds the wiring they
share. `startup.Command` is each binary's whole lifecycle: it parses `-config` and the
binary's own flags (rejecting positional arguments), loads the configuration, cancels
the context passed to the binary's `Run` on SIGINT or SIGTERM, logs
`<name>_process_stopped` on failure, and returns the exit code, so every `main` is one
line and every `Run` is testable with an ordinary context. The package also covers
tracing setup and flush, serving a listener in the background, and stopping a
half-started process. Its `database` subpackage opens the PostgreSQL pool, kept separate
so the agent, which has no database access, never imports it.

### Which packages each binary links

Most `internal` packages are shared, so a package's folder doesn't say which process
runs it. The generated [component map](component-map.md) lists the binaries that link
each internal package, and the rules that keep them apart.

## Infrastructure and delivery entry points

Each area has one entry point and one runbook. The
[server rebuild contract](../operations/server-rebuild-contract.md) connects them to
state ownership and recovery; the [tooling guide](tooling-guide.md#platform-automation)
lists their helpers, offline tests and disposable rehearsals.

| Area | Entry point | Jenkins job | Runbook |
| --- | --- | --- | --- |
| Ubuntu host foundation | [`scripts/infra/server.sh`](../../scripts/infra/server.sh) → `infra/ansible/site.yml` | `battlefield-platform/host` (`infra/jenkins/Jenkinsfile.platform`) | [Server bootstrap](../operations/server-bootstrap.md), [platform jobs](../operations/platform-jobs.md) |
| Jenkins and SonarQube | `server.sh ci-*` → `scripts/infra/ci-bootstrap.py`, `infra/ansible/ci.yml` | None; runs outside Jenkins | [CI bootstrap](../operations/ci-bootstrap.md) |
| K3s VM | `scripts/infra/vm.py` → `infra/libvirt` (Terraform) | `battlefield-platform/vm` (`Jenkinsfile.vm`) | [K3s VM](../operations/k3s-vm.md) |
| K3s platform | `scripts/infra/k3s.py` → `infra/ansible/k3s.yml`, `infra/k3s/` | `battlefield-platform/k3s` (`Jenkinsfile.k3s`) | [K3s platform](../operations/k3s-platform.md) |
| Application delivery | `scripts/cd/deploy.sh`, `smoke-test.sh`, `rollback.sh` | Main pipeline (`Jenkinsfile`) | [Deployment](../operations/deployment.md), [K3s delivery](../operations/k3s-delivery.md) |
| Image signing and evidence | `scripts/ci/` publish, scan, attest and sign scripts | Main pipeline | [Supply chain](../operations/supply-chain-runbook.md) |
| CLI worker VM | `scripts/infra/cli-vm.py` → `infra/ansible/cli-vm.yml` | None | [CLI VM platform](../operations/cli-vm-platform.md) |
| CLI worker services | `server.sh cli-*` → `scripts/infra/cli-bootstrap.py`, `infra/ansible/cli.yml` | None | [CLI bootstrap](../operations/cli-bootstrap.md) |
| CLI worker repair and upgrade | `scripts/infra/cli-worker.py` | `battlefield-platform/cli` (`Jenkinsfile.cli`) | [CLI worker operations](../operations/cli-worker.md) |
| Cloudflare tunnel, DNS and Access | `scripts/infra/cloudflare.py` → `infra/ansible/cloudflare.yml` | None | [Cloudflare platform](../operations/cloudflare-platform.md) |
| Local HTTPS and retained CA | `scripts/infra/https.py` → `infra/ansible/https.yml` | None | [Local HTTPS](../operations/local-https-platform.md) |

Main delivery never runs host, VM, K3s, CLI, Cloudflare or HTTPS provisioning; it
only runs a read-only CLI worker check. Those are reviewed, operator-started
workflows that coordinate with production rollout through the shared host operator
lock (`scripts/infra/operation-lock.py`).

## Common change paths

### Add a battle API field

1. Decide whether the source of truth is `game.State`, `BattleRun`, or derived presentation data.
2. Update the service type/use case.
3. Update repository serialization or query scanning if persisted.
4. Update the handler response.
5. Document the field in [`openapi.yaml`](../architecture/openapi.yaml) and, when its
   behavior needs explanation, in the [HTTP API reference](../architecture/api.md).
   The contract test checks routes, not field shapes.
6. Update `frontend/src/features/battles/api/battles.ts`.
7. Update the consuming page/component and both backend/frontend tests.

### Add a plan field

1. Add it to `controller.PlanProposal`.
2. Add bounds and semantic validation.
3. Add it to the MCP submission schema/input mapping.
4. Apply it deterministically in `game.Match`.
5. Update scenario YAML if scripted/fallback values need it.
6. Update decision persistence, API/frontend types, display, and tests.

### Add a model provider

1. Add a provider adapter implementing `ai.TurnProvider`, or a CLI branch implementing `DecisionRunner` behavior.
2. Extend profile validation and availability rules.
3. Extend the agent's default runner factory.
4. Add environment/configuration and deployment secret fields.
5. Add provider contract, refusal, usage, and tool-call tests.
6. Keep the MCP tool surface unchanged unless the domain contract itself changes.

### Add durable battle behavior

1. Add a new numbered migration with Up and Down sections; never edit one that may
   already be applied (see the [migration conventions](../architecture/database.md#migrations)).
2. Update repository interfaces only with operations required by a use case.
3. Implement guarded SQL using battle attempt and lease ownership where applicable.
4. Update service/worker behavior.
5. Add stale-owner, retry, and shutdown tests.

## Suggested reading order

### Learn the whole request-to-battle path

1. [`backend/internal/game/types.go`](../../backend/internal/game/types.go)
2. [`backend/internal/game/match.go`](../../backend/internal/game/match.go)
3. [`backend/internal/battle/service/service.go`](../../backend/internal/battle/service/service.go)
4. [`backend/internal/battle/worker/worker.go`](../../backend/internal/battle/worker/worker.go)
5. [`backend/internal/battle/repo/battles.go`](../../backend/internal/battle/repo/battles.go) and [`queue.go`](../../backend/internal/battle/repo/queue.go)
6. [`features/battles/pages/BattlePage.tsx`](../../frontend/src/features/battles/pages/BattlePage.tsx)

### Learn the AI decision path

1. [`backend/internal/ai/types.go`](../../backend/internal/ai/types.go)
2. [`backend/internal/battle/worker/decisions.go`](../../backend/internal/battle/worker/decisions.go), starting at `resolveDecisionWindow`
3. [`backend/internal/battle/repo/decisions.go`](../../backend/internal/battle/repo/decisions.go)
4. [`backend/internal/ai/mcpserver/dispatch.go`](../../backend/internal/ai/mcpserver/dispatch.go) (claims), then [`server.go`](../../backend/internal/ai/mcpserver/server.go) (tools)
5. [`backend/internal/ai/agent/executor.go`](../../backend/internal/ai/agent/executor.go)
6. [`backend/internal/ai/runner/api.go`](../../backend/internal/ai/runner/api.go)
7. [`backend/internal/ai/providers/openai.go`](../../backend/internal/ai/providers/openai.go) or [`gemini.go`](../../backend/internal/ai/providers/gemini.go)

### Learn application composition

1. [`backend/cmd/server/main.go`](../../backend/cmd/server/main.go)
2. [`backend/internal/app/app.go`](../../backend/internal/app/app.go)
3. A feature's `api`, `service`, and `repo` packages in that order
4. The matching frontend API client and page

## Contribution workflow

Branch and pull-request rules live in [`AGENTS.md`](../../AGENTS.md#branches-and-pull-requests):
work branches start from the latest `origin/dev` and target `dev`; promotion to
`main` is a separate release. Run the checks in [continuous integration](ci.md)
before opening a PR, and complete the PR template's AI-assistance section required
by the [AI development policy](ai-development-policy.md). The skills in
[`.agents/skills/`](../../.agents/skills/) (linked for Claude Code from
`.claude/skills/`) cover ticket linking, PRs, CI triage, releases, docs and backlog
work, and [cross-review](cross-review.md) covers agent review. Every agent-authored
change is linked to a Notion ticket whose `AB-NN` ID appears in the branch name,
commit subjects and PR title.

For a PR stack, each PR's review and Sonar preview use its actual target branch.
Jenkins verifies only PRs targeting `dev` or `main`, so a stacked PR needs
verification after it is retargeted to `dev`. CI evidence names both the tested
head and target commit; a pass for an older pair does not verify the current tree.

## Shared battlefield content

`backend/gamedata/battlefield.yaml` owns shared timing and controller plans;
`rulesets/battlefield.yaml` references the full `content/core-v8.yaml` library.
`scenarios/castle-siege.yaml` and `scenarios/frontier-siege.yaml` contain maps only.
`battle/catalog/shared_content.go` validates shared plans, while `library.go`
resolves every library entry. Historical content lives only in catalog test fixtures.

`game/upgrade_levels.go` checks prerequisites and exclusive groups;
`upgrade_effects.go` validates scopes; `upgrades.go` applies effects to current and
future units. `specialists.go` owns Scout retreat, cavalry pursuit and repairs;
combat/support code handles armor penetration and extended treatment range.
`unit_unlocks.go` owns unit types locked behind an upgrade (`requiresUpgradeId`):
ruleset validation, `ValidateUnitUnlocks` (shared by the engine and
`ai/decision/validate.go`), the production and recruitment checks, and the
scripted rule that moves an enabled source to a preferred type once it is unlocked.
`battle/catalog/late_game_test.go` probes whether unlocked units break a fully
upgraded stalemate on the live ruleset and checks that scripted bots reach them.
Frontend `presentation/upgrades.ts` formats owned levels, unlocks and branch effects;
`components/UpgradeIcon.tsx` draws their icons for faction strips, the inspector and
decision cards.
Tests cover every map's full catalog, specialist actions, branch locks, reserves,
replay and UI presentation. The PostgreSQL-backed coverage gate includes persistence.

## Battle selection and latency implementation

`internal/ai/selection.go` validates operator model/effort options and resolves
selections. Battle API/service code freezes them in `game.ControllerConfig`, and
`internal/ai/agent/executor.go` validates the claimed selection before constructing
a runner. `internal/ai/timing.go` validates worker duration metadata.
Migration `00018_decision_selection_timing.sql` and the battle decision repository
persist lifecycle times. `DecisionTiming.tsx` renders the elapsed/completed
breakdown in `AIDecisionPanel`; `CreateBattlePage.tsx` renders dependent model/effort
selectors. Selection tests, PostgreSQL lifecycle integration tests, and component
tests cover allowlists, retry identity, terminal timing and missing history.

`scripts/dev/run-k8s-cli-agent.sh` accepts the selected stable CLI version or newer;
`scripts/test/test-cli-agent.py` covers version checks, login checks and generated profiles.
`scripts/test/test-cli-worker.py` checks that model defaults and choices agree
across backend configuration, the application ConfigMap, VM configuration,
managed settings and the generated host-agent configuration.

[`cli_failure.go`](../../backend/internal/ai/runner/cli_failure.go) classifies the
Codex and Claude CLIs' terminal error messages: rate and usage limits become
`provider_rate_limited`, other errors stay `cli`. The worker logs the runner error
as the `error` field of `decision_runner_failed`, after `redact.Error` removes
network identities and `CLIFailureDetail` bounds it and redacts bearer credentials.

CLI context prefetch is implemented in `internal/ai/runner/cli.go` and covered by
`internal/ai/runner/prefetch_test.go`. CLI battle sessions use host-fetched inline
context unless `ModelProfile.PrefetchContext` is explicitly false
(`PrefetchesContext()` applies the default); the host dev script exposes
`HOST_CLI_PREFETCH_CONTEXT` (default `true`). Decision history and
`DecisionTiming.tsx` display the recorded delivery mode. See [comparison
procedure](../operations/cli-worker.md#context-prefetch-comparison).
`cmd/aieval/comparison.go` also accepts CLI `reasoningEffort` and `prefetchContext`
variants, recorded in comparison reports for matched offline-context experiments.
`internal/ai/agentview/context_order.go` orders MCP JSON and partitions prefetched
context into lossless reference/state blocks. Its tests reconstruct optional, null,
unknown and large-number values and compare stable references across ticks.
`internal/ai/agentview/compact_tables.go` and `compact_state.go` encode catalog
and upgrade tables, a shared unit map, construction runs and exact memory sharing.
`compact_data_test.go` independently expands the documented encoding; set
`BATTLEFIELD_DECISIONS_FIXTURE` to a private JSON array of saved `observation`
records for offline round-trip and size checks. Never commit private battle data.
`compact_ids.go` runs before table encoding: it assigns context-local integer unit
IDs, rewrites unit target references and omits destroyed buildings; it is lossy by
design, so `compact_ids_test.go` checks it separately from the round-trip tests.
The free comparison baseline and CLI prefetch read only the metadata they need,
so model-facing catalog tables are not decoded as canonical observation structs.
`internal/ai/prompts/context_policy.go` selects reduced guidance only for the exact
mechanics digest reviewed in `rule-duplicates.json`; prompt tests verify every
removal against embedded game content and retain historical/missing-data fallbacks.

### Battle setup timing and faction summaries

`frontend/src/features/battles/pages/CreateBattlePage.tsx` offers map-default or
preset tick duration and decision interval, resets overrides on a map change,
and keeps combatant columns aligned with mixed controller types. It preselects
the `frontier-siege` map when offered and Low reasoning effort for models that
list efforts. `components/MapPreview.tsx` draws the selected scenario's terrain
and castle spawns from the `map` field of `GET /v1/scenarios`, reusing the hex
geometry exported by `HexBattleMap.tsx`. Availability
still filters controller choices; setup has no integration diagnostic panel.
`backend/internal/battle/service/timing.go` validates optional overrides before
snapshot hashing and persistence; `timing_test.go` covers bounds, defaults and
retry identity. The existing engine reads timing from that frozen snapshot.
`FactionSummaries.tsx` shows the applied plan and rationale inside each status
box for viewers with inspection permission. Component and staging browser tests
cover timing payloads, mixed-controller alignment and plan placement.

Gameplay construction and training are implemented in
[`construction.go`](../../backend/internal/game/construction.go) and
[`training.go`](../../backend/internal/game/training.go). Specialized source selection,
rosters, and historical starting-Barracks compatibility live in
[`training_sources.go`](../../backend/internal/game/training_sources.go). The map renderer shows
terrain bonuses, selected-building range and training progress.
[`continuous_production.go`](../../backend/internal/game/continuous_production.go) validates persistent AI selections and purchases one unit at a time for enabled sources.
[`BuildingProduction.tsx`](../../frontend/src/features/battles/components/BuildingProduction.tsx) renders each living source as a compact chip in its faction strip for every viewer, with gold waits, disabled production and blocked exits in each chip's disclosure (opened by hover title, click, tap or keyboard); selected map details reuse its status display.
`initialization.go` copies authored castle training and attack capabilities into
runtime state; `structures.go` runs castle and tower attacks together. The full map
uses the main battle snapshot; the former faction-perspective polling hook is removed.

## Battle replay implementation

[`game/checkpoint.go`](../../backend/internal/game/checkpoint.go) defines
`Checkpoint`, `Match.Checkpoint` and `RestoreMatch`. `AdvanceWithPlans` and the
opening attach a checkpoint to every decision-window `Transition`; the field is
excluded from the transition's JSON so gameplay trace hashes stay stable. A new
`factionRuntime` or `Match` field that survives between ticks must join the
checkpoint. `game/checkpoint_test.go` and `battle/catalog/checkpoint_test.go`
restore every checkpoint after a JSON round trip and fail on any divergence before
the next window.

[`repo/ticks.go`](../../backend/internal/battle/repo/ticks.go) writes
`battle_replay_frames` (migration `00020`) inside the guarded `SaveTick` and
`CompleteBattle` statements, and [`repo/replay.go`](../../backend/internal/battle/repo/replay.go)
reads the nearest earlier frame. [`service/replay.go`](../../backend/internal/battle/service/replay.go)
restores the match and re-simulates one segment;
[`api/handler.go`](../../backend/internal/battle/api/handler.go) serves
`GET /v1/battles/{id}/replay`, and the route is registered as a Play permission in
`authn/authorization.go`. `repo/replay_integration_test.go` covers lease-guarded and
retried frame writes, and `test/integration/api_test.go` replays a completed battle
end to end as a Player.
