---
name: track-backlog
description: Read, select, create, or update Autonomous Battlefield tickets in the Notion backlog, including status moves, acceptance evidence, and new-ticket fields. Use for "what's next", "pick a ticket", "start AB-NN", "mark this done", "track this in Notion", or backlog triage.
---

# Track backlog work

1. Use Notion MCP. The project page is <https://app.notion.com/p/3c1b7a19f8f780aea86efbe505e504a7> and the backlog database is <https://app.notion.com/p/5198cc5bdacf44e08a7393a5040e2e3e>. Tickets are cited as `AB-<Issue ID>`. Notion is the source of truth for status; do not copy the backlog into `README.md` or other repo docs. If Notion MCP is unavailable, continue safe repository work and tell the user statuses were not synchronized.
2. Read the ticket's Acceptance Criteria, Depends On, and page history before acting. Choose new work from the **Ready** view (P0 first, then lower Issue ID) unless the user names a ticket. Check GitHub for an existing branch or PR before starting a duplicate.
3. Move status only along this workflow:

   | Status | Meaning |
   | --- | --- |
   | Backlog | Accepted, no near-term slot. |
   | Ready | Next in queue; criteria are current and dependencies are met. |
   | In Progress | Substantive implementation is underway. Set it when you start. |
   | Blocked | Cannot continue; record the blocker and what unblocks it. |
   | Awaiting Verification | Code or docs are merged, but acceptance evidence is still missing. |
   | Parked | Needs a product, learning, or cost decision to reactivate. |
   | Rejected / Superseded | Will not be done; link the replacement or reason. |
   | Completed | Every acceptance criterion has evidence tied to a revision. |

   Opening or merging a PR is not completion. Move to **Awaiting Verification** when the change merges, and to **Completed** only with evidence such as a merged PR, a green Jenkins build on that head, a SonarQube gate, or a recorded live check. Never mark Completed on local checks alone when the criteria require CI, production, or live-environment proof.
4. When you change a status, add a short dated note in the ticket body: what changed, the PR/commit/build links, and anything left to verify. Keep acceptance-criteria rewrites explicit rather than silently narrowing scope.
5. Create a ticket only when the user asks to track work, or when you are recording completed work that has none. Fill Task (imperative title), Status, Priority, Size, Type, Epic, Area, Acceptance Criteria (observable outcomes), Depends On (ticket IDs where possible), and Source (a `twhAuto/autonomous_battlefield` PR, commit, or doc link). Priority: P0 blocks the current goal; P1 is high-value planned work; P2 is useful without a commitment; P3 is optional or exploratory. Search for an existing ticket first and reuse it rather than duplicating.
6. Report the tickets touched, their old and new status, evidence linked, and any criteria still unverified.
