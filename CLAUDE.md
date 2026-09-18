# Autonomous Battlefield

Read and follow [AGENTS.md](AGENTS.md) for repository rules, branch targets,
verification requirements and documentation maintenance.

For implementation work, the default PR cross-review workflow is described in
[docs/operations/cross-review.md](docs/operations/cross-review.md).
After committing and pushing the intended change and opening/updating its draft
PR, use `--author claude --pr NUMBER` to request Codex review and publish its
advisory report before declaring the work ready. Codex reviews use GPT-6 Astra
with high reasoning effort; Claude reviews use Opus. Post your finding-by-finding
response with the author-response marker described in the guide, then request
another review when you dispute a finding or change code. Continue the evidence-based
exchange until agreement or a clearly reported need for user input. Read findings and verify them before applying fixes. If you are already
acting as reviewer, do not launch another reviewer or edit the change.
