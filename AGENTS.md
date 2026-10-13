# Autonomous Battlefield agent guidance

## Branches and pull requests

- `dev` is the integration branch. Unless the user chooses otherwise, create every
  work branch from a freshly fetched `origin/dev` and open its PR with an explicit
  `--base dev`. Never base new work on `main` or another work branch by default.
- Inspect the working tree first and preserve existing work; use an isolated
  worktree when needed. If fetching fails, say so rather than using a stale `dev`.
- Promotion from `dev` to `main` is a separate release action, done only when the
  user requests it.
- Run the applicable checks before opening a PR, including
  `./scripts/test/test-deployment.sh` for deployment changes. Jenkins verifies PRs
  on the shared `battlefield-production` agent by design. Skipped or queued stages
  are not verification.

## Project tracking

Notion is the source of truth for task status:
[project](https://app.notion.com/p/3c1b7a19f8f780aea86efbe505e504a7) and
[backlog](https://app.notion.com/p/5198cc5bdacf44e08a7393a5040e2e3e). Do not copy
the backlog into `README.md`. If Notion MCP is unavailable, continue safe repository
work and report that statuses could not be synchronized.

Workflow skills live in `.agents/skills/` (linked from `.claude/skills/`). Use the
matching skill for PRs, CI/SonarQube triage, releases and recovery, major
documentation work, and backlog tickets.

## Documentation maintenance

Keep documentation synchronized with every implementation change:

- Runtime components, boundaries, data flows, persistence, security, configuration,
  deployment or operational behavior: update
  `docs/architecture/system-architecture.md` and affected operations guides.
- Schema, migrations, database roles, grants or triggers: update
  `docs/architecture/database.md`.
- Packages, entry points, important types, routes, migrations, frontend areas,
  tests or common workflows: update `docs/developer/codebase-guide.md`. When a
  backend change adds, moves or newly imports an `internal` package, regenerate
  `docs/developer/component-map.md` with
  `go test ./internal/boundaries -run TestComponentMapIsCurrent -update` from `backend/`.
- Setup steps, capabilities, commands, ports or documentation entry points: update
  `README.md`.
- Skip mechanical edits for internal changes that alter no documented behavior,
  but verify the docs remain accurate. Check links, diagrams, examples, paths and
  commands in every affected document against the working tree.

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
- Verify findings before fixing them, test the fixes, and leave unresolved findings
  visible. Review is advisory: it does not replace CI or authorize merging or
  deployment. Reviewers must not edit the change or invoke another reviewer.

At completed task boundaries, report branch/PR/head, goal, checks, unresolved
findings and next step.
