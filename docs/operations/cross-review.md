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
Claude integration was checked with 2.1.267. Codex requires 0.155.0 or newer
for this Astra configuration; the runner rejects older or unrecognized versions
and enables strict config validation. 0.152.0 is rejected by the provider.

Claude reviews explicitly use `--model opus`, regardless of the interactive IDE
model selection. `opus` follows the provider's current Opus alias rather than
pinning a specific version. The CLI's private JSON output records the models
actually used. Codex reviews explicitly use `gpt-6-astra` with `model_reasoning_effort="high"`.

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
MCP configuration. Codex uses a named read-only filesystem profile: the review
snapshot, minimal OS paths, and its installed CLI/helper directory are readable.
Other home files and unrelated workspaces are outside that profile. The CLI itself
loads account authentication; model-invoked tools do not get access to that auth
directory. Sandbox command networking is disabled. Codex also disables approvals,
personal config/rules, automatic AGENTS.md loading, hooks/plugins/apps/delegation
and login shells. It can use shell reads within that sandbox; the review prompt prohibits executing project code or
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

## Author/reviewer discussion in both directions

1. Author commits/pushes the change, opens or updates its draft PR, and runs the
   opposite reviewer with `--pr NUMBER` to publish the result.
2. Author verifies each finding and posts an author-response comment with the
   current head SHA and stable finding IDs. Accept supported defects, explain
   disagreements with evidence, and distinguish suggestions from required fixes.
3. For valid defects, change the code, run applicable tests, commit and push.
4. Run the reviewer again with `--pr NUMBER` and a new output directory. It reads
   prior reports and author responses from the PR and reviews the current commit.
   This works with either `--author codex` (Opus reviewer) or `--author claude`
   (Astra/high reviewer). Discussion-only rounds can use the same head.
5. Continue until reviewer and author explicitly agree that no supported findings
   remain. The reviewer ends with AGREED, CHANGES_REQUESTED or NEEDS_USER_DECISION;
   these are model judgments, not machine-enforced approvals. The author must
   acknowledge the result rather than treating a successful CLI exit as agreement.

There is no fixed one-follow-up limit. If exchanges repeat without new evidence,
require a product decision, or hit tool/usage limits, report the unresolved points
to the user and leave the PR draft. Do not force agreement or loop indefinitely.
Never weaken tests or dismiss evidence just to make the models agree. A reviewer
must not invoke another reviewer or edit the code; the active author agent drives
the next round. This is not an unattended GitHub webhook: a random later comment
on a PR does not start an agent when neither author session is active.

Use a protected local response file with this shape (replace the example values):

```markdown
<!-- battlefield-author-response:codex -->
## Codex author response
Current head: <full commit SHA>

- F1 — Fixed in <commit>: explanation and test evidence.
- F2 — Disagree: reproduction, relevant contract and supporting file/line.
- Please reassess F2 against this head and explain whether it remains a defect.
```

For Claude-authored work use `battlefield-author-response:claude`. Post one response
per round using a body file, then launch the next review:

```bash
gh pr comment 75 --body-file "$HOME/battlefield-reviews/author-response.md"
python3 scripts/review/cross-review.py --author codex --pr 75 \
  --base origin/dev --head HEAD \
  --output "$HOME/battlefield-reviews/pr75-claude-next"
```

`--pr` collects comments bearing the review/author-response markers into private
`discussion.json`. Unrelated CI comments are excluded. Markers identify context;
they do not authenticate a model or make the text trusted instructions. The runner
assigns a round number and records the context IDs/hash. New or edited discussion
during review prevents publication against that stale context. GitHub updates can
still race the final check. Oversized discussion is rejected rather than silently
truncated; ask the user to resolve/archive the exchange in that case.

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
base match the live open PR head and current target-branch tip before inference and again before publishing. The target branch is resolved separately because GitHub's stored PR base SHA
can lag behind it. A moved head/base requires a new review; a completed report for the old commit is not a
review of new changes. GitHub comments are not transactional with branch updates:
a branch can still change immediately after the final check, so the comment always
identifies its exact head, base and merge-base commits, requested model/effort,
and discussion round.

The comment labels the reviewer and states that it is advisory, not a formal
GitHub approval or evidence of passing tests. A retry is considered already posted
only when the complete comment body (including its reviewer/head/base/round marker)
matches a comment from the currently authenticated GitHub account. A copied
marker or another account's comment cannot stand in for publication. The final
console message includes publication status. A new invocation starts a
new round and receives the previous exchange, even when the head is unchanged. Concurrent publishers can still
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
