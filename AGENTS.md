# Autonomous Battlefield agent guidance

## Project tracking

The authoritative project backlog is maintained in Notion:

- Project: [Autonomous Battlefield](https://app.notion.com/p/3c1b7a19f8f780aea86efbe505e504a7)
- Backlog: [Autonomous Battlefield Backlog](https://app.notion.com/p/5198cc5bdacf44e08a7393a5040e2e3e)
- Workflow: `Not Started` -> `In Progress` -> `Completed`
- Use `Blocked` only when work cannot continue.

When the user asks to plan, select, or implement tracked project work:

1. Use Notion MCP to read the relevant backlog tickets and their acceptance criteria.
2. Treat Notion as the source of truth for task status; do not duplicate the backlog in `README.md`.
3. When beginning a specific tracked ticket, move it to `In Progress`.
4. Mark a ticket `Completed` only after its acceptance criteria have been satisfied and the implementation has been verified.
5. Record newly discovered project work in the Notion backlog when the user asks to track it.

If Notion MCP is unavailable, continue with safe repository work when possible and tell the user that task statuses could not be synchronized.

## Documentation maintenance

Keep repository documentation synchronized with every implementation change.

- When a change affects runtime components, boundaries, data flows, persistence,
  security, configuration, deployment, or operational behavior, update
  `docs/design/system-architecture.md` and any affected operational guide in the
  same change.
- When a change adds, removes, renames, moves, or materially changes packages,
  entry points, important types, routes, migrations, frontend areas, tests, or
  common development workflows, update `docs/design/codebase-guide.md` in the
  same change.
- Update `README.md` when setup steps, capabilities, commands, ports, or
  documentation entry points change.
- Do not make mechanical documentation edits for an internal change that does
  not alter documented behavior or structure. Instead, verify that the current
  documentation remains accurate.
- Before completing a change, check links, diagrams, examples, file paths, and
  commands in every affected document against the current working tree,
  including uncommitted changes.

## Test coverage quality gate

Implementation work must preserve the repository's merge-blocking coverage
requirements:

- Backend Go statement coverage must be at least 80% across all packages.
- Frontend statements, branches, functions, and lines must each be at least 80%.
- Add or update meaningful tests for changed behavior. Do not lower thresholds,
  exclude production code from coverage, or add tests that only inflate coverage
  without checking behavior unless the user explicitly approves the tradeoff.

Before declaring relevant implementation work complete, run:

```bash
./scripts/ci/test-go-coverage.sh
(cd frontend && npm test -- --coverage)
```

Run the applicable command when a change affects only one side of the
application. For documentation- or infrastructure-only changes, use the checks
appropriate to those files instead. If a required coverage command cannot run,
report that limitation and do not claim the coverage gate passed.
