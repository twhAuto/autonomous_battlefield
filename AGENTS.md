# OrdinalFrontier agent guidance

## Branches and pull requests

- `dev` is the integration branch. Unless the user chooses otherwise, create every
  work branch from a freshly fetched `origin/dev` and open its PR with an explicit
  `--base dev`. Never base new work on `main` or another work branch by default.
- Inspect the working tree first and preserve existing work; use an isolated
  worktree when needed. If fetching fails, say so rather than using a stale `dev`.
- Promotion from `dev` to `main` is a separate release action, done only when the
  user requests it.
- Run the applicable checks before opening a PR, including
  `./scripts/ci/lint-go.sh` for backend changes (gofmt, vet and SonarQube's
  cognitive-complexity limit) and `./scripts/test/test-deployment.sh` for
  deployment changes. Jenkins verifies PRs
  on the shared `battlefield-production` agent by design, and GitHub Actions also
  verifies PRs into `dev` on GitHub-hosted runners. Skipped or queued jobs and
  stages are not verification.

## Project tracking

Notion is the source of truth for task status:
[project](https://app.notion.com/p/3c1b7a19f8f780aea86efbe505e504a7) and
[backlog](https://app.notion.com/p/5198cc5bdacf44e08a7393a5040e2e3e). Do not copy
the backlog into `README.md`. If Notion MCP is unavailable, continue safe repository
work and report that statuses could not be synchronized.

Every repository change by any agent (Claude, Codex, or another) must be linked to
a Notion ticket. Before the first edit, follow the
[link-ticket skill](.agents/skills/link-ticket/SKILL.md): find the matching ticket
or create one, and carry its `AB-NN` ID in the branch name, every commit subject,
the PR title and the PR Motivation. Never invent an ID; only the user can waive the
requirement for a specific change.

Workflow skills live in `.agents/skills/` (linked from `.claude/skills/`). Use the
matching skill for ticket linking, PRs, CI/SonarQube triage, releases and recovery,
documentation edits, and backlog tickets.

## Documentation maintenance

Docs describe the system as it is now, for someone new to the project. They are
not a change log: commits, PR descriptions and Notion tickets record what changed
and why. Follow the [write-project-docs skill](.agents/skills/write-project-docs/SKILL.md)
for every doc edit.

- **Current state only.** No ticket IDs, PR numbers, before/after measurements, or
  change words such as "adds", "now", "no longer", "existing" or "former". State
  compatibility with older data or clients as part of today's contract (for
  example, "results without per-type figures omit these lists"), once, in the
  owning doc. Rollout steps for one change belong in its PR and ticket; operations
  guides keep only standing procedures.
- **One owner per fact.** State each fact in its owning doc below and link to it
  from elsewhere instead of restating it.
- **Merge, don't append.** Put new behavior into the section that already covers
  its topic and rewrite that section so it still reads as one description. Do not
  add a section per feature, ticket or migration. Delete text the change made stale
  or redundant.
- **Keep each doc at its level.** Architecture docs cover boundaries, data flows,
  invariants and trade-offs. Developer guides say where code lives and how to
  change it, not what each function or test does. Leave UI timings, cache sizes,
  keyboard handling and test inventories to the code unless a reader needs them to
  act.
- **No doc change is a valid outcome.** Internal changes that alter no documented
  behavior need no doc edit; say so in the PR, after checking that the docs remain
  accurate.

Owning docs:

- Gameplay rules and scenario mechanics: `docs/game/`.
- Runtime components, boundaries, data flows, security, configuration and
  deployment topology: `docs/architecture/system-architecture.md`.
- Model runners, prompts, MCP tools, decision context, plan validation and
  background generation: `docs/architecture/ai-decisions.md`.
- Schema, migrations, database roles, grants and triggers:
  `docs/architecture/database.md`.
- API routes and fields: `docs/architecture/openapi.yaml`. Permissions, errors,
  retries and other API behavior: `docs/architecture/api.md`.
- Source layout, entry points and change paths: `docs/developer/codebase-guide.md`
  and the focused developer guides. `docs/developer/component-map.md` is
  generated; never hand-edit it.
- Procedures for operating environments: `docs/operations/`.
- Setup, capabilities, ports and documentation entry points: `README.md`.

Check links, diagrams, examples, paths and commands in every touched doc against
the working tree. `python3 scripts/test/test-doc-links.py` checks every internal
Markdown link and heading anchor; `./scripts/test/test-deployment.sh` runs it on
every PR.

## Test coverage quality gate

Merge-blocking thresholds: backend Go statement coverage of at least 80% in
aggregate across the module, and each frontend metric (statements, branches,
functions, lines) at least 80%. Add meaningful tests for changed behavior. Do not
lower thresholds, exclude production code, or add coverage-only tests without
explicit user approval.

Before declaring implementation work complete, run the side(s) affected:

```bash
./scripts/test/with-test-postgres.sh ./scripts/test/test-go-coverage.sh
(cd frontend && npm test -- --coverage)
```

Always measure backend coverage through `with-test-postgres.sh`; without it,
database-backed tests are skipped. If a coverage command cannot run, report that
and do not claim the gate passed. See
[coverage gate details](docs/developer/ci.md#coverage-gate-details) for aggregate
versus per-package figures and harness limits. Documentation- or
infrastructure-only changes use the checks appropriate to those files.

## SonarQube

Use the triage-ci skill and its
[SonarQube workflow](.agents/skills/triage-ci/sonarqube.md) for findings and gates.
Never print or commit the token, suppress rules or weaken the profile without
explicit approval, or upload unmerged code to the production
`autonomous-battlefield` project. Passing local tests does not verify the gate.

## Cross-review between Codex and Claude

Meaningful agent-authored PRs get one full review from the other agent through the
raise-pr skill and `scripts/review/cross-review.py --pr NUMBER`, then at most one
focused follow-up. Agents may post advisory review and author-response comments on
PRs; the [cross-review guide](docs/developer/cross-review.md) defines the procedure,
comment format and limits.

- Batch changes before review. Ask the user before any extra round; never set
  `--allow-extra-round` automatically, omit `--pr`, delete comments, or retry
  failed model calls to get around the limit.
- Trivial wording, formatting and low-risk preference edits may skip review; record
  the reason. Authentication, permissions, deployment behavior and substantive logic
  always need review, whatever the diff size. If the user asks to skip review for a
  PR, skip it and say so.
- Verify findings before fixing them. Fix or dispute blocking findings; decline a
  non-blocking one in a line when its fix is not worth the added complexity. Test
  the fixes and leave unresolved findings visible. Review is advisory: it does not replace CI or authorize merging or
  deployment. Reviewers must not edit the change or invoke another reviewer.

At completed task boundaries, report branch/PR/head, goal, checks, unresolved
findings and next step.
