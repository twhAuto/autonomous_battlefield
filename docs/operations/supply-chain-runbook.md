# Supply-chain and recovery runbook

## Artifact identity and retention

The deployed unit is a full `repository@sha256:...` reference. Jenkins archives reports, SBOM copies, provenance summaries, AI manifests, deployment digests, and test evidence; it does not archive image tarballs. GHCR is the source of truth. Release subjects are permanent, while non-release main subjects remain for at least 90 days or the last 20 successful builds, whichever is greater.

Retention automation must begin in report-only mode. Before deletion it must subtract every digest referenced by a live environment, the recorded rollback pair, a release, an active incident, or a legal hold. Package deletion is not enabled by this repository.
Cosign v3 OCI referrers and the Kyverno-compatible legacy `.sig` and `.att`
digest tags are evidence belonging to their subject; retention must preserve or
remove them as one unit and must never orphan a deployable subject from either
verification layout.

## Signing-key rotation

1. Open a reviewed security ticket and record the old public-key fingerprint.
2. Generate an encrypted Cosign key pair in an isolated administrative environment.
3. Add the new private key and password as masked Jenkins credentials; keep the private key out of Git and agent workspaces.
4. Commit and review the new public key, then update admission and verification jobs to trust both keys during the overlap.
5. Sign and verify a canary digest, including all three required attestations.
6. Switch signing to the new key, retain old-key verification through the rollback-retention window, then remove it.
7. Record fingerprints, dates, evidence, and credential owners in the ticket.

For suspected compromise, stop promotion, revoke Jenkins access, remove the key from admission trust, inventory every subject it signed, and restore only digests independently verified and re-signed by the incident key. Never delete evidence during the investigation.

## Rollback and cluster recreation

Before every rollout Jenkins records both previous image digest references. A failed rollout runs `scripts/cd/rollback-kind.sh` with that exact pair. If the Kind cluster is lost, recreate it, authenticate to private GHCR, verify the stored signature and attestations, then deploy with `IMAGE_SOURCE=registry` and the stored `BACKEND_IMAGE_REF` and `FRONTEND_IMAGE_REF`. Tags must not be substituted.

## Disaster-recovery exercises

- Quarterly: recreate Kind and restore the last known-good digest pair.
- Quarterly: rotate the MCP capability secret and application authentication secret.
- Quarterly: exercise engine lease reclaim, duplicate submission, PostgreSQL restart, MCP outage, provider timeout/429, failed migration, failed rollout, and rollback.
- Before production: restore RDS from backup, disrupt an EKS pod and node, and complete Terraform teardown in a disposable account.

Archive the command transcript, timestamps, digest pair, recovery point/recovery time, smoke/load results, failures, and follow-up tickets. The included Kind failure script automates only the safe disposable-cluster subset; failed-migration and RDS restore drills remain explicitly reviewed manual operations.
