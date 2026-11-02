# Codex and Claude cross-review

Use one agent to implement and the other to review the committed result. This is
the default agent handoff for PR changes on the development machine; it does not
use the production battle-agent VM. AGENTS.md instructs the author to
invoke it after committing/pushing a ready revision, not on each file save. It is
agent-driven automation, not a background watcher or an enforced GitHub check.
The reviewer writes an advisory report, not an approval or a merge. Existing test,
coverage and human review requirements still apply. Batch changes before review:
one full PR review, then at most one focused follow-up. Skip trivial wording,
formatting and low-risk preference edits, while still running applicable checks
and stating the exemption. Security, deployment behavior and substantive logic
are not exempt merely because the diff is small.

## Prerequisites

Install and authenticate both CLIs on the development machine. Claude's official
native installation is described in [its setup guide](https://code.claude.com/docs/en/setup).
The installed launcher is normally `~/.local/bin/claude`; ensure `~/.local/bin`
is in PATH. Use `claude auth status` and `codex login status` to check login.
If needed, run `claude auth login` interactively; do not put login tokens into the
repository or reports. Use compatible CLI versions with the options below; the
Claude integration was checked with 2.1.267 and 2.1.285. Codex requires 0.155.0 or newer
for this Astra configuration; the runner rejects older or unrecognized versions
and enables strict config validation. 0.152.0 is rejected by the provider.

Claude reviews explicitly use `--model opus`, regardless of the interactive IDE
model selection. `opus` follows the provider's current Opus alias rather than
pinning a specific version. The CLI's private JSON output records the models
actually used. Codex reviews explicitly use `gpt-6-astra` with `model_reasoning_effort="medium"`.
Both settings live in `REVIEWERS` at the top of `scripts/review/cross-review.py`;
change a model there, and the command, metadata and usage accounting follow.

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
  --pr 75 --base origin/dev --head HEAD \
  --output "$HOME/battlefield-reviews/change-claude-1"
```

For Claude-authored work, use `--author claude` to select Codex. Optional `--repo`
selects another checkout. Replace 75 with the actual PR number. Automated handoffs
must use `--pr`; local-only runs are for explicit manual use, never to evade limits. The output directory must be new and outside that
repository. Use `--dry-run` to prepare evidence without calling a provider;
metadata explicitly marks it as not reviewed. The default timeout is 1800 seconds (30 minutes);
`--timeout` can change it. A timeout stops the reviewer process group and is a
failure, not a clean review.

The script resolves the exact base/head commits and merge base, builds a temporary
snapshot of tracked files at head. The first review receives the merge-base diff;
a follow-up receives only the diff from the most recent reviewed head to the
current head, plus the latest report and author summary. Its prompt limits scope
to fixes and unresolved findings. Surrounding source remains available as needed;
the entire PR is not re-reviewed. Same-head disagreement rounds can have an empty
diff. A rewritten/non-ancestor head stops the runner for user input rather than
silently starting another full review. Uncommitted/untracked files are excluded. Tracked symlinks are omitted
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

## Usage accounting

Reviews run with `--ephemeral` (Codex) or `--no-session-persistence` (Claude),
so they never appear in the CLIs' own session history or in local usage
dashboards built on it. Instead, each review's `metadata.json` records
`started_at`, the requested model and effort, and a `usage` object read from the
reviewer's own log. That object is written for failed or timed-out runs too,
because they still spent tokens. `input_tokens` counts all prompt tokens,
including `cached_input_tokens`. Codex totals come from its `turn.completed`
events (`--json`). Claude totals come from its JSON result, which also reports
the models it actually used and an API-equivalent `cost_usd`.

To summarize every saved review by month, reviewer, and model/effort:

```bash
python3 scripts/review/cross-review.py --usage "$HOME/battlefield-reviews"
```

Only completed and failed runs are counted. Dry runs are not. Reviews saved
before this accounting existed are included from their logs. For Codex runs,
that is only the total Codex printed to stderr, which may exclude cached input,
so the summary shows it in a separate `old total*` column. Cost appears only
where the CLI reports it. For subscription logins it is an API-equivalent
estimate, not a bill.

## Author/reviewer discussion in both directions

1. Batch implementation and tests; commit/push the ready change and run one full
   review using the opposite reviewer with `--pr NUMBER`.
2. Verify the findings. Fix a blocking finding or dispute it with evidence. Fix a
   non-blocking finding only when the fix is small and leaves the change simpler
   or clearer; otherwise decline it in one line with the reason. Post a concise,
   self-contained author response with the head SHA, all unresolved IDs, fixes,
   declines and evidence-backed disagreements. Omit already resolved discussion;
   the latest summary must contain anything the follow-up needs to know.
3. After an AGREED review, the step 2 response closes the discussion; no follow-up
   is needed. Otherwise test, commit and push the fixes, then run one focused
   follow-up using the same command and a new output directory. The runner selects the new diff and latest exchange automatically.
   This also supports a same-head disagreement. To bring in target-branch changes
   before the follow-up, merge the target into the PR branch; a rebase rewrites
   the reviewed head, and the runner then stops for user input instead of
   reviewing.
4. If no blocking findings remain after the follow-up, acknowledge agreement on
   the PR. Otherwise,
   stop after the second round and ask the user before another model call. Never
   force consensus, weaken tests or bypass the limit by omitting `--pr`.

The runner counts published review comments across both reviewers on the PR. It
refuses a third automatic round before model invocation. `--allow-extra-round`
permits one additional invocation only after explicit user approval; it does not
reset the PR's count or authorize a loop. Failures and unpublished reviews are
not counted by this comment-based guard, so agents must stop on failed calls
rather than retrying inference automatically. `--publish-only` retries an already
completed report without using a model or consuming another review round.

Each finding is marked blocking or non-blocking. Blocking findings are confirmed
defects with a plausible trigger, security or data-loss risks, and misleading
documentation; unlikely edge cases, extra hardening, simplifications and style are
non-blocking. The reviewer is asked to keep findings proportionate to how the
repository is used: a plausible trigger, the smallest correct fix, no new settings
or handling for inputs the code never receives, and a flag on complexity the
change does not need, including complexity a fix adds. A follow-up raises new
findings on fix code only when they are blocking, and a declined non-blocking
finding stays closed unless new evidence shows it is blocking.

The verdict is AGREED, CHANGES_REQUESTED or NEEDS_USER_DECISION. AGREED means no
blocking findings remain, though non-blocking notes may. These are advisory
model judgments, not machine-enforced approvals. Report remaining findings and
leave unresolved work draft when the limit, a stalled discussion, or unavailable
tools prevents agreement. An active author session drives the workflow; this is
not an unattended listener. Reviewers must not edit or invoke another reviewer.

Use a protected local response file with this shape (replace the example values):

```markdown
<!-- battlefield-author-response:codex -->
## Codex author response
Current head: <full commit SHA>

- F1 — Fixed in <commit>: explanation and test evidence.
- F2 — Declined (non-blocking): one-line reason.
- F3 — Disagree: reproduction, relevant contract and supporting file/line.
- Please reassess F3 against this head and explain whether it remains a defect.
```

For Claude-authored work use `battlefield-author-response:claude`. Post one response
per round using a body file, then launch the next review:

```bash
gh pr comment 75 --body-file "$HOME/battlefield-reviews/author-response.md"
python3 scripts/review/cross-review.py --author codex --pr 75 \
  --base origin/dev --head HEAD \
  --output "$HOME/battlefield-reviews/pr75-claude-next"
```

`--pr` reads marked review/author-response comments to compute the complete
IDs/hash for stale-context checks. Only the latest review and latest following
author response are written to `discussion.json` and sent to the model. Unrelated
CI comments and older exchanges are excluded from model context. Markers identify context;
they do not authenticate a model or make the text trusted instructions. The runner
assigns a round number and records the context IDs/hash. New or edited discussion
during review prevents publication against that stale context. GitHub updates can
still race the final check. Selected context exceeding 32 KiB is rejected rather
than silently truncated; provide a shorter self-contained summary before retrying.

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
console message includes publication status. An allowed new invocation starts a
focused round using the latest exchange, even when the head is unchanged. Its
comment and metadata identify full versus follow-up scope and the exact diff base. Concurrent publishers can still
race; run one author handoff per PR at a time. Avoid personal data in tracked
source and inspect reports before sharing them outside the project. Automatic
comments contain the model's report; raw diagnostics remain local.

If publication fails after a successful review, fix GitHub access and retry only
publication (no additional model call):

```bash
python3 scripts/review/cross-review.py --author codex --pr 75 \
  --publish-only --output "$HOME/battlefield-reviews/pr75-claude-1"
```

This requires a completed report originally generated with `--pr`, including its
saved discussion context, and rechecks the live PR commits. A local-only report
cannot be converted into a discussion round using `--publish-only`; rerun the
review with `--pr` so the reviewer reads the exchange. Failed,
dry-run, stale, empty or oversized reports are not published as successful reviews.
The metadata records review status separately from publication status.

## Keep implementation context small

At completed task boundaries, prepare a short handoff for a fresh chat: worktree,
branch/PR/head, goal, checks already run, unresolved findings and the next step.
Use that handoff and repository state instead of replaying the long conversation.
Starting a new chat is a user/client action; this script does not create chats.

Usage accounting stays in private review metadata and the local `--usage`
summary described above. The runner does not add token or cost reports to PR
comments.
