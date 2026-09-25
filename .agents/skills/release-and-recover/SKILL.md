---
name: release-and-recover
description: Guide Autonomous Battlefield dev-to-main promotion, production delivery, failed rollout recovery, or disaster-recovery drills using the repo's current verified-image and database runbooks. Use for "promote", "release", "deploy production", "roll back", or "recover a failed release".
---

# Release and recover

1. Read `AGENTS.md`, `docs/operations/jenkins.md`, `docs/operations/environments.md`, `docs/operations/deployment.md`, and `docs/operations/supply-chain-runbook.md`; for public launch or Access changes also read `docs/operations/limited-launch.md`. Confirm the live environment and requested action. Current server production uses kind; K3s is only planned until a migration is merged.
2. For promotion, compare the exact `dev` and `main` commits, verify required PR gates, and create a promotion PR targeting `main`. Record the intended release commit and the verified artifacts. Do not confuse PR verification with a completed `main` delivery.
3. Before production action, inspect the registered cluster identity, database and backup status, current deployment and prior known-good **digest pair**, image signature/provenance evidence, migration compatibility, and required Access smoke credentials. Use the existing Jenkins main delivery and `scripts/cd/` entry points; do not invent a parallel deploy path or replace private configuration.
4. Observe migration backup, rollout, and smoke evidence. A failure after a schema migration is not automatically reversible: `scripts/cd/rollback-kind.sh` restores a verified application digest pair, not PostgreSQL data or schema. Use `scripts/cd/production-recovery.sh` to inspect the cluster-bound record of the last verified revision, and follow the runbooks for recovery decisions; require a verified backup/restore path and report when operator intervention is needed.
5. Record before/after commit and digests, cluster identity, backup reference, verification and smoke results, rollback/recovery action, residual risks, and the next operator step. For a drill, use a disposable environment and keep production unchanged. Treat the user-requested release or recovery as the authorization boundary for the requested action; surface any missing credentials or irreversible decision before that step.
