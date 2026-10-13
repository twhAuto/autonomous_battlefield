---
name: write-project-docs
description: Write or substantially revise Autonomous Battlefield architecture, codebase, API, setup, deployment, or operations documentation. Use for "write docs", "update the runbook", "document this architecture", or major developer-guide revisions.
---

# Write project documentation

1. Inspect the relevant current code, manifests, scripts, tests, and nearby docs before writing. Prefer updating the owning page: `docs/game/` for gameplay rules and scenario mechanics, `docs/architecture/system-architecture.md` for runtime boundaries and behavior, `docs/architecture/database.md` for schema, database access and migrations, `docs/developer/codebase-guide.md` and focused design guides for source navigation (never hand-edit the generated `docs/developer/component-map.md`; the command to regenerate it is at the top of that page), `docs/architecture/api.md` for API contracts, `docs/operations/` for procedures, and `README.md` for setup and entry points.
2. Write for a developer new to the project. State prerequisites, inputs, effects, verification, and recovery where relevant. Label **current** behavior separately from proposals; do not describe planned K3s or AWS work as deployed. Keep one authoritative operational procedure and link to it instead of copying commands throughout the repo.
3. Verify every path, command, option, environment variable, link target, example response, and diagram against the current tree and implementation. For changed procedures, exercise safe render/dry-run or targeted checks when possible; never execute a production change just to validate prose.
4. Check the diff for stale statements and contradictions with other guides. Report source files consulted, examples actually exercised, and any external or live-environment detail that remains unverified.
