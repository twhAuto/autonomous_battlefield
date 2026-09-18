# Autonomous Battlefield

Read and follow [AGENTS.md](AGENTS.md) for repository rules, branch targets,
verification requirements and the bounded cross-review policy.

For meaningful implementation work, use `--author claude --pr NUMBER` to request
Codex GPT-6 Astra at medium reasoning effort. Codex-authored work uses Claude
Opus. Run one full review when ready, then at most one focused follow-up on fixes
and unresolved findings. Ask the user before any additional round. Trivial
wording/formatting and low-risk preference changes are exempt; explain the
exemption and run applicable checks. Do not bypass the limit with local reviews.

Post concise, self-contained author responses as described in
[the cross-review guide](docs/operations/cross-review.md). At task boundaries,
prepare a short handoff for a fresh chat. Do not add usage tracking. When acting
as reviewer, do not edit the change or invoke another reviewer.
