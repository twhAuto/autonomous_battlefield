---
name: link-ticket
description: Resolve the Autonomous Battlefield Notion ticket that a repository change belongs to, creating one when none fits, and carry its AB-NN ID through the branch, commits, and PR. Required for every agent (Claude, Codex, or any other) before editing code, docs, config, scripts, or skills. Use at the start of any change, "fix this", "implement", "update the docs", or when a branch, commit, or PR has no ticket.
---

# Link every change to a ticket

Every repository change made by an agent belongs to a Notion backlog ticket, whichever agent makes it. This applies to code, tests, docs, configuration, scripts, CI, and skills, however small. Read-only work (answering questions, investigating, reviewing) and edits made only in Notion need no ticket.

1. **Before the first edit**, find the ticket. In order: a ticket the user named; the `ab-NN` in the current branch name; a search of the [backlog](https://app.notion.com/p/5198cc5bdacf44e08a7393a5040e2e3e) by title and body. Fetch the candidate and confirm the change fits its Goal and Acceptance Criteria. Reuse a ticket only when the change serves that outcome; do not stretch an unrelated ticket or reopen a **Done** or **Won't Do** ticket for new work.
2. **If none fits, create one** with the track-backlog skill before editing: one measurable outcome, an action-verb title, the live properties, and a short Goal/Current state/Scope/Acceptance criteria body. A user request to make the change authorizes creating its ticket; do not pause for routine wording. Read the page back to get its Issue ID.
3. **Start it.** Move a **Backlog** or **To Do** ticket to **In Progress** with a dated note naming the branch. Leave other statuses to track-backlog's workflow.
4. **Carry the ID** (`AB-NN`) everywhere the change travels:
   - branch: `<type>/ab-NN-short-slug`, created from `origin/dev` as `AGENTS.md` requires;
   - every commit subject: `AB-NN: imperative summary`;
   - PR title and **Motivation** link, as the raise-pr skill specifies;
   - each ticket's ID when a change genuinely spans several.
5. **Never invent an ID.** If Notion is unavailable, proceed only with an ID the user supplies or the current branch already carries, and report that the ticket status could not be synchronized. Otherwise ask the user for the ticket before committing. Only the user can waive the requirement for a specific change; record the waiver in the PR.
6. At each task boundary, report the ticket ID, its link, and any status change you made.
