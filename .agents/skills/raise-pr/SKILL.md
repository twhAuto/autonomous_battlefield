---
name: raise-pr
description: Prepare and open an Autonomous Battlefield pull request, including branch and diff checks, applicable verification, a draft PR description, and the bounded cross-review handoff when requested or required. Use for "raise a PR", "open a pull request", or "prepare this branch for review".
---

# Raise a pull request

1. Read the current `AGENTS.md` branch, check, and review rules and `docs/operations/cross-review.md`. Inspect the worktree, fetch the remote, confirm the branch was based on the latest `origin/dev` unless the user chose another base, and check the diff against that base. Preserve unrelated work; resolve an unexpected base or uncommitted changes before publishing.
2. Check that the diff matches the request, contains no secrets or generated debris, and updates affected docs. Run checks relevant to the changed files as specified by `AGENTS.md`; include `scripts/test/test-deployment.sh` for deployment changes. Record commands and actual results, distinguishing unavailable checks from passes.
3. Commit and push the intended changes. Open a **draft** PR targeting `dev` explicitly unless the user selected another target. Describe motivation, changes, risks, validation, and outstanding checks. For `dev` to `main` promotion, use the release workflow instead.
4. For meaningful agent-authored changes, invoke the existing `scripts/review/cross-review.py` with `--author codex --pr NUMBER` or `--author claude --pr NUMBER`, according to the actual author. Follow the guide's evidence and response flow. Do not reproduce reviewer/model logic here, run on intermediate commits, or bypass its round limit. If the user explicitly skips cross-review for this PR, honor that instruction and record the omission in the handoff.
5. Verify findings before fixing them; rerun applicable checks and use at most one focused follow-up under the runner's limit. If review cannot complete, report why and leave unresolved work in draft. Never treat an advisory review as CI approval or merge/deploy authorization.
6. Report the PR URL, head, checks, review status, unresolved findings, and next step. A skill provides workflow instructions; select the author model in agent/CLI configuration, not in this file.
