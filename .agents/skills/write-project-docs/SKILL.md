---
name: write-project-docs
description: Write, update or revise Autonomous Battlefield documentation (game rules, architecture, API, database, codebase, setup, deployment or operations), including the doc edits inside a feature or fix PR. Use for "write docs", "update the docs", "update the runbook", "document this", or whenever a change touches docs/, README.md or openapi.yaml.
---

# Write project documentation

Docs describe the system as it is now. Apply the rules and owning-doc list in the Documentation maintenance section of `AGENTS.md`.

1. Decide whether docs change. Name the behavior a player, operator or contributor meets differently after the change. If there is none, make no doc edit and say so in the PR. Otherwise find the owning doc and the section that already covers each topic.
2. Read before writing. Read the whole owning section and the relevant code, manifests, scripts and tests. Search the docs for the topic's main identifiers to find statements elsewhere that the change makes stale or that your text would duplicate.
3. Edit the owning section. Rewrite it so it reads as one description of current behavior, as though it had always worked this way, and replace stale statements instead of qualifying them. Replace duplicates elsewhere with a link. Write for a developer new to the project: state prerequisites, inputs, effects, verification and recovery where relevant. Label proposals separately from current behavior, and distinguish repository support from verified live deployment.
4. Check the diff before committing. `git diff -U0 origin/dev -- docs README.md | grep -nE '^\+.*(AB-[0-9]+|PR #?[0-9]+|\b(adds|now|no longer|existing|former|previously)\b)'` lists lines to reread; not every hit is wrong. Also confirm that the diff:
   - appends no section for this feature, ticket or migration;
   - holds nothing that matters only during review or rollout (move it to the PR description);
   - does not restate code function by function or test by test;
   - adds only sentences a newcomer would miss if they were gone;
   - holds no figure worked out from a setting or default, and repeats no default outside its owning doc;
   - removes what it made stale, especially when it only adds lines.
5. Verify every path, command, option, environment variable, link target, example response and diagram against the current tree. For changed procedures, run safe render, dry-run or targeted checks when possible; never execute a production change just to validate prose. Run `python3 scripts/test/test-doc-links.py`. After changing a configuration default or the settings table in `docs/architecture/system-architecture.md`, run `(cd backend && go test ./internal/config -run TestDocumentedDefaults)`. Report the sources consulted, the examples exercised, and any live-environment detail left unverified.
