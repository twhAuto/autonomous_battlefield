# Codex and Claude cross-review

Use one agent to implement and the other to review the committed result. This is
a development-machine workflow; it does not use the production battle-agent VM.
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
sharing. The script never posts comments, pushes, merges or changes production.
A zero exit status means a report completed, not that it contains no findings.
Missing tools, auth errors, empty results and timeouts must be investigated.

## Handoff and bounded follow-up

1. Author commits the change and runs the other reviewer.
2. Author reads the findings, reproduces each claimed defect and fixes valid ones.
3. Run the applicable repository tests; commit the fixes.
4. Run one follow-up review on the new head in a new output directory.
5. Bring unresolved disagreements to the user with evidence. Do not loop forever
   or change sound behavior just because a model requested it.

An agent currently acting as reviewer must not invoke another reviewer. This
prevents recursive review chains. Neither agent should suppress tests or findings
because the other model says the change is safe. Jenkins remains a separate gate.
The runner is explicit, not an automatic editor hook: each invocation selects an
immutable scope and consumes account usage deliberately.
