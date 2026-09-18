# Autonomous Battlefield

Read and follow [AGENTS.md](AGENTS.md) for repository rules, branch targets,
verification requirements and documentation maintenance.

For implementation work, the default PR cross-review workflow is described in
[docs/operations/cross-review.md](docs/operations/cross-review.md).
After committing and pushing the intended change and opening/updating its draft
PR, use `--author claude --pr NUMBER` to request Codex review and publish its
advisory report before declaring the work ready. Read findings and verify them before applying fixes. If you are already
acting as reviewer, do not launch another reviewer or edit the change.
