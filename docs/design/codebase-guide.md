# OrdinalFrontier Codebase Guide

The public product name is OrdinalFrontier. Its wordmark and O emblem appear in
the application shell and login page; `frontend/index.html` owns browser metadata
and `frontend/public/ordinalfrontier.svg` is the matching favicon. Historical
repository/package paths and deployment identifiers remain compatible.

| Field | Value |
| --- | --- |
| Status | Current |
| Last reviewed | 2026-09-23 |
| Audience | Contributors navigating or modifying the implementation |
| Scope | Repository layout, entry points, packages, dependencies, tests, and reading paths |

For runtime boundaries, data flows, durability, and security decisions, see the [system architecture](system-architecture.md).

AI-created work branches start from the latest fetched `origin/dev` and target
`dev` in pull requests, unless the user explicitly requests otherwise. Promotion
to `main` is separate. See [agent branch guidance](../../AGENTS.md#branches-and-pull-requests)
and the [Jenkins workflow](../operations/jenkins.md#configure-github-integration-branch-protection).
Run applicable local checks before opening a PR. The repo's
[raise-pr](../../.agents/skills/raise-pr/SKILL.md),
[triage-ci](../../.agents/skills/triage-ci/SKILL.md),
[release-and-recover](../../.agents/skills/release-and-recover/SKILL.md),
[write-project-docs](../../.agents/skills/write-project-docs/SKILL.md), and
[track-backlog](../../.agents/skills/track-backlog/SKILL.md) skills cover
PRs, CI, releases, docs, and backlog work. Claude Code discovers the same
skills through `.claude/skills/` symlinks. Jenkins verifies both `dev` and `main`
PR targets on the existing shared `battlefield-production` agent.

Use the focused guides for implementation details:

- [Backend guide](backend-guide.md): package ownership, persistence, configuration, AI execution, and membership.
- [Frontend guide](frontend-guide.md): feature ownership, shared contracts, styles, and browser behavior.
- [Tooling guide](tooling-guide.md): deployment scripts, test commands, assurance, and cross-review.

## Repository map

```text
autonomous_battlefield/
├── .agents/skills/             # Canonical PR, CI, release, docs, and backlog skills
├── .claude/skills/             # Links to the canonical skills for Claude Code
├── backend/
│   ├── cmd/{server,engine,mcp,agent,aieval}/ # Startup and command wiring
│   ├── internal/
│   │   ├── battle/{api,service,catalog,worker,repo,resultcache}/
│   │   ├── faction/{api,service,repo}/
│   │   ├── game/               # Deterministic state transitions
│   │   │   └── controller/     # Infrastructure-free controller contracts
│   │   ├── ai/                 # Decision execution, providers, MCP, evaluations
│   │   ├── admin/              # Model-access handler, service, and repository
│   │   ├── members/            # Player membership and reconciliation
│   │   ├── generation/         # Faction background generation jobs and quotas
│   │   ├── testutil/           # Shared assertions with lazy failure diagnostics
│   │   ├── apierror/           # Stable JSON error envelope and codes for /v1
│   │   ├── requestid/          # Request correlation ID carried in the context
│   │   └── ...                 # app, authn, config, httpapi, postgres, logging, telemetry
│   ├── migrations/             # Embedded SQL migrations
│   ├── content/                # Shared versioned unit and building libraries
│   ├── rulesets/               # Versioned game rules
│   ├── scenarios/              # Authored battle templates
│   └── evals/                  # Evaluation cases and pricing configuration
├── frontend/src/
│   ├── app/                    # Application shell, routes, integration tests
│   ├── features/{battles,factions,auth,admin}/ # Feature pages, API clients, unit tests
│   ├── shared/{api,domain,components,hooks,styles}/ # Shared contracts, visuals, and foundations
│   ├── pages/                  # General pages, including Home
│   └── test/                   # Shared test setup
├── deploy/                     # Kubernetes bases, overlays, supply-chain assets
├── infra/aws/                  # AWS provisioning with Terraform
├── scripts/
│   ├── dev/                    # Local application and host-agent launchers
│   ├── test/                   # Test runners, deployment and runtime assurance script tests
│   ├── ci/                     # Lint, builds, coverage summaries, image assurance
│   ├── cd/                     # Deployment, smoke checks, rollback
│   ├── perf/                   # Performance-test launcher
│   ├── security/               # Security-test launcher
│   ├── ops/                    # Operational reports and failure drills
│   ├── review/                 # Codex/Claude cross-review runner
│   └── lib/                    # Shared script functions
├── tests/performance/          # k6 scenarios
├── quality/sonarqube/          # Local analysis service
├── policy/                     # Deployment policy
└── docs/{design,operations,reference,security}/
```

## Executable entry points

The backend has five commands in one Go module: four runtime processes and the offline AI evaluation command. The application image contains the four runtime binaries.

| Process | Entry point | What the entry point owns |
| --- | --- | --- |
| API | [`backend/cmd/server/main.go`](../../backend/cmd/server/main.go) | Flags, configuration, migrations, PostgreSQL startup, catalog loading, application wiring, HTTP lifecycle |
| Engine | [`backend/cmd/engine/main.go`](../../backend/cmd/engine/main.go) | Configuration, PostgreSQL startup, worker construction, health server, signals, graceful requeue/shutdown |
| MCP | [`backend/cmd/mcp/main.go`](../../backend/cmd/mcp/main.go) | Restricted repository construction, PostgreSQL notification listener, MCP and agent-control routes, HTTP lifecycle |
| Agent | [`backend/cmd/agent/main.go`](../../backend/cmd/agent/main.go) | Profile registry, decision worker, health endpoint, signals, shutdown |
| AI evaluation | [`backend/cmd/aieval/main.go`](../../backend/cmd/aieval/main.go) | Evaluation case execution, pricing inputs, reports, and runtime provenance via `-mode metadata` |

`cmd` packages should remain thin. Domain and use-case behavior belongs under `internal`; entry points should focus on composition and process lifecycle.

## Common change paths

### Add a battle API field

1. Decide whether the source of truth is `game.State`, `BattleRun`, or derived presentation data.
2. Update the service type/use case.
3. Update repository serialization or query scanning if persisted.
4. Update the handler response.
5. Update `frontend/src/features/battles/api/battles.ts`.
6. Update the consuming page/component and both backend/frontend tests.

### Add a plan field

1. Add it to `controller.PlanProposal`.
2. Add bounds and semantic validation.
3. Add it to the MCP submission schema/input mapping.
4. Apply it deterministically in `game.Match`.
5. Update scenario YAML if scripted/fallback values need it.
6. Update decision persistence, API/frontend types, display, and tests.

### Change model role access

Admin model-access role validation applies equally to API and CLI profiles.
`backend/internal/admin/service_test.go` covers saving and revoking Player CLI
access through the durable policy; the admin page tests cover the Player checkbox
and persistence across reloads. Player catalog tests retain gameplay-only projection
while exposing explicitly approved, available CLI profiles.

### Add a model provider

1. Add a provider adapter implementing `ai.TurnProvider`, or a CLI branch implementing `DecisionRunner` behavior.
2. Extend profile validation and availability rules.
3. Extend the agent's default runner factory.
4. Add environment/configuration and deployment secret fields.
5. Add provider contract, refusal, usage, and tool-call tests.
6. Keep the MCP tool surface unchanged unless the domain contract itself changes.

### Add durable battle behavior

1. Add a forward-only migration.
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
4. [`backend/internal/ai/mcpserver/server.go`](../../backend/internal/ai/mcpserver/server.go)
5. [`backend/internal/ai/worker/worker.go`](../../backend/internal/ai/worker/worker.go)
6. [`backend/internal/ai/runner/api.go`](../../backend/internal/ai/runner/api.go)
7. [`backend/internal/ai/providers/openai.go`](../../backend/internal/ai/providers/openai.go) or [`gemini.go`](../../backend/internal/ai/providers/gemini.go)

### Learn application composition

1. [`backend/cmd/server/main.go`](../../backend/cmd/server/main.go)
2. [`backend/internal/app/app.go`](../../backend/internal/app/app.go)
3. A feature's `api`, `service`, and `repo` packages in that order
4. The matching frontend API client and page
