# Codex and Claude cross-review

Use one agent to implement and the other to review the committed result. This is
the default agent handoff for PR changes on the development machine; it does not
use the production battle-agent VM. AGENTS.md and CLAUDE.md instruct the author to
invoke it after committing/pushing a ready revision, not on each file save. It is
agent-driven automation, not a background watcher or an enforced GitHub check.
The reviewer writes an advisory report, not an approval or a merge. Existing test,
coverage and human review requirements still apply.

## Prerequisites

Install and authenticate both CLIs on the development machine. Claude's official
native installation is described in [its setup guide](https://code.claude.com/docs/en/setup).
The installed launcher is normally `~/.local/bin/claude`; ensure `~/.local/bin`
is in PATH. Use `claude auth status` and `codex login status` to check login.
If needed, run `claude auth login` interactively; do not put login tokens into the
repository or reports. Use compatible CLI versions with the options below; the
initial Claude integration was checked with 2.1.267.

The runner uses existing account login. It intentionally does not forward
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, or arbitrary application credentials.
Non-interactive reviews consume provider usage; check your plan's automation
allowance. See [Claude's automation guide](https://code.claude.com/docs/en/headless)
and [Codex review](https://learn.chatgpt.com/docs/code-review).

## Run the other reviewer

Commit the intended changes first. Fetch the chosen base; the script does not
fetch or alter branches. From the checkout you want reviewed:

```bash
git fetch origin
python3 scripts/review/cross-review.py --author codex \
  --base origin/dev --head HEAD \
  --output "$HOME/battlefield-reviews/change-claude-1"
```

For Claude-authored work, use `--author claude` to select Codex. Optional `--repo`
selects another checkout. The output directory must be new and outside that
repository. Use `--dry-run` to prepare evidence without calling a provider;
metadata explicitly marks it as not reviewed. The default timeout is 600 seconds;
`--timeout` can change it. A timeout stops the reviewer process group and is a
failure, not a clean review.

The script resolves the exact base/head commits and merge base, builds a temporary
snapshot of tracked files at head, and supplies the merge-base diff plus a shared
checklist. Uncommitted/untracked files are excluded. Tracked symlinks are omitted
from the snapshot rather than followed; reviewers must report missing context.
Tracked secrets would still be included, so never commit them. Diffs larger than
2 MiB are rejected, not silently truncated. The snapshot is removed after review.

Claude has only Read/Glob/Grep tools, restricted mode, hooks disabled and an empty
MCP configuration. Codex uses its read-only sandbox, no approvals, no personal
config or rules, and disables hooks/plugins/apps/delegation. It can use shell
reads within that sandbox; the review prompt prohibits executing project code or
contacting services. These restrictions do not make AI judgment infallible.
The snapshot protects the working checkout from concurrent edits; there is no
shared editing session and no permission bypass.

Read `review.md` together with `metadata.json` (which identifies the reviewed
commits). Private stdout/stderr are diagnostic evidence; inspect/redact before
sharing. Without `--pr`, the script keeps the report local. With `--pr`, it posts only the
completed review as an ordinary PR comment using the current `gh` account. It
never posts raw stdout/stderr, pushes, merges or changes production.
A zero exit status means a report completed, not that it contains no findings.
Missing tools, auth errors, empty results and timeouts must be investigated.

## Handoff and bounded follow-up

1. Author commits/pushes the change, opens or updates its draft PR, and runs the
   other reviewer with `--pr NUMBER` to publish the result.
2. Author reads the findings, reproduces each claimed defect and fixes valid ones.
3. Run the applicable repository tests; commit the fixes.
4. Run one follow-up review on the new head in a new output directory.
5. Bring unresolved disagreements to the user with evidence. Do not loop forever
   or change sound behavior just because a model requested it.

An agent currently acting as reviewer must not invoke another reviewer. This
prevents recursive review chains. Neither agent should suppress tests or findings
because the other model says the change is safe. Jenkins remains a separate gate.
The authoring agent invokes the runner by default before declaring a PR revision
ready. This is not an editor hook: each invocation selects an immutable scope and
consumes account usage. If review fails, leave the PR draft and report the failure.

## Publish the report to a PR

Authenticate GitHub CLI with the account that should own the comments (`gh auth
status`; use `gh auth login` if needed). For a pushed Codex-authored draft PR:

```bash
git fetch origin
python3 scripts/review/cross-review.py --author codex --pr 75 \
  --base origin/dev --head HEAD \
  --output "$HOME/battlefield-reviews/pr75-claude-1"
```

Replace 75 with the actual PR number. The runner checks that the reviewed head and
base match the live open PR before inference and again before publishing. A moved
head/base requires a new review; a completed report for the old commit is not a
review of new changes. GitHub comments are not transactional with branch updates:
a branch can still change immediately after the final check, so the comment always
identifies its exact head, base and merge-base commits.

The comment labels the reviewer and states that it is advisory, not a formal
GitHub approval or evidence of passing tests. Matching reviewer/head/base markers
avoid duplicate comments on sequential reruns. Concurrent publishers can still
race; run one author handoff per PR at a time. Avoid personal data in tracked
source and inspect reports before sharing them outside the project. Automatic
comments contain the model's report; raw diagnostics remain local.

If publication fails after a successful review, fix GitHub access and retry only
publication (no additional model call):

```bash
python3 scripts/review/cross-review.py --author codex --pr 75 \
  --publish-only --output "$HOME/battlefield-reviews/pr75-claude-1"
```

This requires a completed report and rechecks the live PR commits. Failed,
dry-run, stale, empty or oversized reports are not published as successful reviews.
The metadata records review status separately from publication status.
