# Supply-chain and recovery runbook

## Artifact identity and retention

The deployed unit is a full `repository@sha256:...` reference. Jenkins archives reports, SBOM copies, provenance summaries, AI manifests, deployment digests, and test evidence; it does not archive image tarballs. GHCR is the source of truth. Release subjects are permanent, while non-release main subjects remain for at least 90 days or the last 20 successful builds, whichever is greater.

AI manifest generation requires the backend Go toolchain and module dependencies.
`scripts/ci/generate-ai-manifest.sh` runs `go run ./cmd/aieval -mode metadata`
from `backend/` to obtain the runtime prompt version (including its content digest),
the SHA-256 of the battle prompts as assembled for a fixed sample session
(`promptSourceSha256`), the tool schema version, and the digest of the game content
embedded in the binary (`gameDataBundleSha256`, schema `battlefield.ai-build-manifest.v2`).
It does not run evaluations or call a model provider.
`evaluationBundleSha256` hashes the corpus contents and relative filenames in
byte-sorted order. Identical corpora produce the same digest across checkout
paths; changing a file's content or relative name changes its identity. This
replaces the former checkout-path-dependent digest for newly generated manifests;
existing attestations remain attached to their original image subjects.

Retention automation must begin in report-only mode. Before deletion it must subtract every digest referenced by a live environment, the recorded rollback pair, a release, an active incident, or a legal hold. Package deletion is not enabled by this repository.
Cosign v3 OCI referrers and the Kyverno-compatible legacy `.sig` and `.att`
digest tags are evidence belonging to their subject; retention must preserve or
remove them as one unit and must never orphan a deployable subject from either
verification layout.

### Run a retention inventory

Run `scripts/ops/ghcr-retention-report.sh` with a
registered deployment environment and `GH_TOKEN` carrying `read:packages` access.
The shared environment resolver verifies the selected cluster. The script writes
package-version inventories and protected running/rollback digests under
the checkout's `artifacts/retention/` (`REPORT_DIR` overrides the destination;
relative overrides resolve from the caller's directory; `GHCR_OWNER` overrides
the package owner). The rollback file also resolves from the checkout. A failed
cluster or registry read stops collection before publishing any report files,
preserving the previous inventory and its timestamp. A successful cluster read
with no digest-pinned running images also fails before publication, even if rollback
digests exist; tag-only or empty deployments cannot establish a protected set.
Publication copies files into the report directory and is not an atomic directory
replacement. A local write failure can leave mixed files; use only a completed
command result and inspect all inputs before a retention decision. It does not
delete registry versions. Treat missing
cluster or rollback evidence as incomplete inventory before making any deletion
decision; the report is advisory, not an automated retention policy.

## Base-image pins

Every `FROM` in `backend/Dockerfile` and `frontend/Dockerfile` names its base image
by digest only (`name@sha256:<digest>`), so rebuilding an old revision uses the same
base content. The line above records the tag the digest was resolved from, as
`# Base image: name:tag`. Tag and digest are not combined on the `FROM` line:
Docker ignores the tag there, and SonarQube's `docker:S8431` rejects it.
`scripts/test/test-base-image-pins.py`, part of `scripts/test/test-deployment.sh`,
fails on an unpinned base image or a missing or mismatched tag comment.

Nothing updates the digests automatically. Refresh them when
`scripts/ci/scan-published-images.sh` reports a fixable vulnerability in a base
image, when moving to a new tag, and at least monthly:

1. Resolve the multi-platform index digest of each tag, for example
   `docker buildx imagetools inspect golang:1.26.9-alpine`, and take its `Digest:`.
2. Replace the digest on the `FROM` line, and the tag in the comment above it if
   moving to a new tag.
3. Run `./scripts/test/test-deployment.sh`, then raise the change as a normal PR
   so Jenkins rebuilds and rescans both images.

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

Jenkins records the previous backend/frontend digest pair before rollout.
Application rollout or smoke-test failure can restore that verified pair with
`scripts/cd/rollback.sh`; database migrations are not reversed. See
[Jenkins production delivery](jenkins.md#production-delivery).

A lost registered cluster requires explicit recovery. Follow
[environment validation and recovery](environments.md#other-managed-credentials-and-validation)
to restore the database and verify the replacement cluster before registration.
Then authenticate to GHCR and deploy the verified stored digest pair. Restoring
images does not restore PostgreSQL data; normal bootstrap refuses to recreate a
missing registered cluster.

## Disaster-recovery exercises

- Quarterly, in a disposable environment: restore database backups into a replacement
  Kind cluster, register its verified identity, and restore the last known-good digest pair.
- Quarterly: exercise MCP capability-secret rotation and authentication credential
  rotation for the configured mode. Production uses the
  [Access service-token rotation procedure](limited-launch.md#expiry-monitoring-and-rotation);
  password/session-signing secrets belong only to local-password development.
- Quarterly: exercise engine lease reclaim, duplicate submission, PostgreSQL restart, AI gateway outage, provider timeout/429, failed migration, failed rollout, and rollback.
- Before an AWS production deployment: restore RDS from backup, disrupt an EKS
  pod and node, and complete Terraform teardown in a disposable account. This
  applies to the [AWS reference](deployment.md#moving-the-deployment-to-aws), not
  the current Kind/K3s delivery gate.

Archive the command transcript, timestamps, digest pair, recovery point/recovery time, smoke/load results, failures, and follow-up tickets. The included Kind failure script automates only the safe disposable-cluster subset; failed-migration and RDS restore drills remain explicitly reviewed manual operations.
