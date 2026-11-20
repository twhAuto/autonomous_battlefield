---
name: track-backlog
description: Audit, reconcile, prioritize, select, create, split, or refine Autonomous Battlefield tickets in the Notion backlog. Use for "what's next", "triage tickets", "start AB-NN", "mark this done", "add a Notion task", or backlog cleanup.
---

# Track backlog work

1. Use Notion MCP. Start with the **Autonomous Battlefield** project page and its **Autonomous Battlefield Backlog** database, found by those exact titles with Notion search; fetch the live data source schema and relevant views before changing properties. Tickets are cited as `AB-<Issue ID>`. Never assume cached field names, counts, or ticket states remain current. Notion is the source of truth for status; do not copy the backlog into `README.md`. If Notion MCP is unavailable, continue safe repository work and report that statuses could not be synchronized.
2. Read each relevant ticket's Acceptance Criteria, Depends On, body, comments, and related decision records. Choose new work from the **To Do** view (P0 first, then lower Issue ID) unless the user names a ticket. Search for an existing ticket, branch, or PR before duplicating work. Verify code-related claims against the current repository revision; a historical successful build is not evidence for a newer head. Treat inaccessible live systems as unknown.
3. Use the live Status options, with this intended workflow:

   | Status | Meaning |
   | --- | --- |
   | Backlog | Accepted, no near-term slot. |
   | To Do | Next in queue; criteria are current and dependencies are met. |
   | In Progress | Substantive implementation is underway. Set it when you start. |
   | In Verification | Code or docs are merged, but acceptance evidence is still missing. |
   | Blocked | Committed work cannot continue; record what unblocks it. |
   | On Hold | Needs a product, learning, timing, or cost decision to reactivate. |
   | Done | Every acceptance criterion has evidence tied to a revision. |
   | Won't Do | Will not be done; name the replacement or reason. |

   Opening or merging a PR is not completion. Move to **In Verification** when the change merges and acceptance evidence is still missing. Move to **Done** only when every criterion has the required evidence, such as a green Jenkins build on the merged head, a SonarQube gate, or a recorded live check. A merge alone never justifies Done; if all criteria are already verified at merge, Done can follow directly. Never mark Done on local checks alone when the criteria require CI, production, or live-environment proof.
4. When changing status or priority, add a short dated note: what changed, evidence and PR/commit/build links, the exact remaining check, and what would reactivate blocked or held work. Preserve historical criteria and implementation evidence; explicitly explain any rewrite or supersession. A recommendation-only request calls for findings, not ticket edits. An instruction to triage or clean up authorizes the corresponding reversible updates. Do not invent a product decision.
5. Report tickets touched, old and new status/priority, linked evidence, and unverified criteria. After writes, re-fetch changed pages and views; make sure counts and card fields match the reported result.

## Audit and reconcile existing tickets

- Query all relevant rows with pagination, then fetch candidates rather than judging from titles alone. Compare remaining outcomes, duplicates, dependencies, current architecture, and the active PR or deployment. Keep an outcome when a concrete gap remains; use **In Verification** for merged work awaiting evidence, **On Hold** for intentional future experiments or decisions, and **Won't Do** for superseded outcomes with a linked replacement or reason. Do not mark superseded work Done or erase its history. Deferred partially implemented work belongs in Backlog or On Hold with the progress and remaining checks recorded.
- Use Priority for consequence: P0 blocks the current goal; P1 is high-value planned work; P2 is useful without a commitment; P3 is optional or exploratory. A P0 tied to a future milestone or historical Original Sprint is not automatically urgent today. Follow explicit user timing choices. Do not recreate a Horizon field; use the current Status workflow.
- Check free-text dependencies against actual ticket IDs before converting them to relations. Note contradictory names, source links, criteria, and body notes. Make the smallest necessary edits and record a dated triage summary with linked revisions, decisions, and unknowns.

## Create or refine tickets

- Give each ticket one measurable outcome and an action-verb title. Use Story for a user-visible slice, Task for bounded implementation, Bug for a reproduced defect, and Spike for a time-bounded comparison or decision. If asked for execution tasks, split an XL item into linked, sequenced deliverables while retaining its parent goal. A bug needs observed behavior and reproduction; a spike needs options, measured signals, a stopping condition, and a decision output before implementation work is planned.
- Write a compact body with **Goal**, **Current state/evidence**, **Scope and sequence**, **Acceptance criteria**, and **References**. Criteria must measure remaining work against the current architecture, with exact source paths, PRs, and related pages. Date historical findings and tie them to a revision. Do not silently describe obsolete flows as current.
- Fill the live Task, Status, Priority, Size, Type, Epic, Area, Milestone, Acceptance Criteria, Depends On, and Source properties. Default an unscheduled improvement to P2/Backlog and an optional experiment to P3/On Hold unless the user specifies otherwise. Leave Original Sprint unset unless its historical value is known, and leave schedule fields unset without a real plan. Resolve dependencies to existing IDs and native relations only if the live schema supports them; never invent a dependency. Use the fetched data source ID to create pages, then read them back to verify fields, content, and links.
- If the user asks only for recommendations, provide drafts without creating pages. When creation is requested, create and verify without pausing for routine wording choices. For multiple tickets, explain their distinct outcomes and sequence.
