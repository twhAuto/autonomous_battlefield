# Supply-chain and recovery runbook

## Artifact identity and retention

The deployed unit is a full `repository@sha256:...` reference. Jenkins archives
reports, SBOM copies, provenance summaries, AI manifests, deployment digests, and
test evidence; it does not archive image tarballs. GHCR is the source of truth.
Release subjects are permanent, while non-release main subjects remain for at least
90 days or the last 20 successful builds, whichever is greater.

AI manifest generation requires the backend Go toolchain and module dependencies.
`scripts/ci/generate-ai-manifest.sh` runs `go run ./cmd/aieval -mode metadata`
from `backend/` to obtain the runtime prompt version (including its content digest),
the SHA-256 of the battle prompts as assembled for a fixed sample session
(`promptSourceSha256`), the tool schema version, and the digest of the game content
embedded in the binary (`gameDataBundleSha256`), under schema `battlefield.ai-build-manifest.v3`.
It does not run evaluations or call a model provider.
`evaluationBundleSha256` hashes the Git-tracked files under `backend/evals` (contents
and relative filenames, byte-sorted). Identical committed corpora produce the same
digest across checkout paths; untracked local files are ignored, and changing a
tracked file's content or name changes the digest. The generator fails if no
evaluation files are tracked. Schema v3 marks this definition: v2 manifests hashed
every file present, and v1 also included the checkout path, so compare digests only
between manifests with the same schema version. Existing attestations remain
attached to their original image subjects.

Retention automation must begin in report-only mode. Before deletion it must
subtract every digest referenced by a live environment, the recorded rollback pair,
a release, an active incident, or a legal hold. Package deletion is not enabled by
this repository.
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
The report is staged in a sibling directory and swapped into place by two renames,
so the destination never holds a mix of old and new files, though it is briefly
absent between them. Files from earlier runs that the script no longer writes are
removed. A lock beside the destination refuses a concurrent run. If the swap fails,
the previous report is restored. If restoring also fails, or the run is
interrupted mid-swap, the previous report stays beside the destination as
`.retention-retired.*`, and the script names it when it can. It does not delete
registry versions. Treat missing
cluster or rollback evidence as incomplete inventory before making any deletion
decision; the report is advisory, not an automated retention policy.

## Image pins

Every third-party image that a build, CI stage or deployment runs is pinned by
digest. A moved upstream tag, or an image retagged in the shared Jenkins agent's
Docker cache by another build, therefore cannot change what runs, and rebuilding an
old revision uses the same content:

- Every `FROM` in `backend/Dockerfile` and `frontend/Dockerfile` names its base
  image by digest only (`name@sha256:<digest>`). The line above records the tag the
  digest was resolved from, as `# Base image: name:tag`. Tag and digest are not
  combined on the `FROM` line: Docker ignores the tag there, and SonarQube's
  `docker:S8431` rejects it.
- Every `*_IMAGE` in `scripts/ci/tool-versions.env` and every third-party `image:`
  in the `infra/deploy` manifests is `name:tag@sha256:<digest>`. The tool images
  include Cosign, Syft, ORAS and Crane, which receive the signing key and registry
  credentials. Images built from this repository are exempt: deployments replace
  them with signed digests that admission verifies.
- The kind cluster applies Kyverno's release manifest only when its SHA-256 matches
  `KYVERNO_INSTALL_SHA256` in `tool-versions.env`.

`scripts/test/test-base-image-pins.py`, part of `scripts/test/test-deployment.sh`,
fails on any unpinned image above or on a missing or mismatched base-image tag
comment.

Nothing updates the digests automatically. Refresh them when
`scripts/ci/scan-published-images.sh` reports a fixable vulnerability in a base
image, when moving to a new tag, and at least monthly:

1. Run `scripts/ops/check-base-image-pins.py` on a machine with Docker Buildx and
   registry access. It resolves each recorded tag's multi-platform index digest
   (`docker buildx imagetools inspect name:tag`) and prints `CURRENT` or `STALE`
   for every Dockerfile, tool and manifest pin without editing files. It exits 1 if
   any pin is stale and 2 if a tag cannot be resolved. It is an on-demand check,
   not a scheduled job.
2. Replace the digest, and the tag if moving to a new one; `docker buildx
   imagetools inspect name:tag` prints a new tag's digest. When changing
   `KYVERNO_VERSION`, set `KYVERNO_INSTALL_SHA256` to the `sha256sum` of that
   release's `install.yaml`.
3. Run `./scripts/test/test-deployment.sh`, then raise the change as a normal PR
   so Jenkins rebuilds and rescans the images.

## Signing-key rotation

1. Open a reviewed security ticket and record the old public-key fingerprint.
2. Generate an encrypted Cosign key pair in an isolated administrative environment.
3. Add the new private key and password as masked Jenkins credentials; keep the
   private key out of Git and agent workspaces.
4. Commit and review the new public key, then update admission and verification
   jobs to trust both keys during the overlap.
5. Sign and verify a canary digest, including all three required attestations.
6. Switch signing to the new key, retain old-key verification through the
   rollback-retention window, then remove it.
7. Record fingerprints, dates, evidence, and credential owners in the ticket.

For suspected compromise, stop promotion, revoke Jenkins access, remove the key from
admission trust, inventory every subject it signed, and restore only digests
independently verified and re-signed by the incident key. Never delete evidence
during the investigation.

## Admission verification

The Kyverno image-verification webhook allows 30 seconds per request to fetch
registry signatures and attestations (`webhookConfiguration.timeoutSeconds`).
Verification stays enforced with `failurePolicy: Fail`; the timeout never waives
missing or invalid evidence.

## Rollback and cluster recreation

Jenkins records the previous backend/frontend digest pair before rollout.
Application rollout or smoke-test failure can restore that verified pair with
`scripts/cd/rollback.sh`; database migrations are not reversed. See
[Jenkins production delivery](jenkins.md#production-delivery).

A lost registered cluster requires explicit recovery. The
[server rebuild contract](server-rebuild-contract.md#ownership-locking-and-recovery-objectives)
defines what can be recovered. There is no separate lost-cluster restore runbook:
register the replacement cluster and restore a verified database dump using the
registration and dump/restore steps in
[K3s delivery](k3s-delivery.md#move-production-to-a-new-cluster).
Then authenticate to GHCR and deploy the verified stored digest pair. Restoring
images does not restore PostgreSQL data; normal bootstrap refuses to recreate a
missing registered cluster.

## Disaster-recovery exercises

Nothing runs on a schedule, because production is often switched off. Run these
exercises on demand, before relying on a recovery path and after changing
delivery, migration or credential handling.

- In a disposable environment, restore a database backup into a replacement
  cluster, register its verified identity, and restore the last known-good digest
  pair.
- Exercise MCP capability-secret rotation and authentication credential
  rotation for the configured mode. Production uses the
  [Access service-token rotation procedure](access.md#expiry-and-rotation);
  password/session-signing secrets belong only to local-password development.
- Exercise engine lease reclaim, duplicate submission, PostgreSQL restart, AI
  gateway outage, provider timeout/429, failed migration, failed rollout, and
  rollback.
- Before an AWS production deployment: restore RDS from backup, disrupt an EKS
  pod and node, and complete Terraform teardown in a disposable account. This
  applies to the [AWS reference](deployment.md#moving-the-deployment-to-aws), not
  the current Kind/K3s delivery gate.

Archive the command transcript, timestamps, digest pair, recovery point/recovery
time, smoke/load results, failures, and follow-up tickets. The included Kind failure
script automates only the safe disposable-cluster subset; failed-migration and RDS
restore drills remain explicitly reviewed manual operations.
