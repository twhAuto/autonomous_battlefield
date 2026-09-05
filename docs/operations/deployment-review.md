# Deployment structure review — 2026-09-05

Static review covered the deployment entry points and library, image build/scan/
publication/signing scripts, both Jenkins pipelines, operational drill and
retention scripts, Kustomize compositions, and the AWS Terraform reference.
Validation used local rendering and script tests; no cluster, registry, or AWS
resources were changed.

## Improvements made

- Removed Jenkins's duplicate manifest-render list. Source security already
  renders and validates the same manifests for both PR and main.
- Removed duplicate Trivy version literals from Jenkins and the local scanner.
  `scripts/ci/tool-versions.env` supplies the default pin.
- Limited deployment scratch space to selected overlays and their dependencies.
  Infrastructure copies manifests and explicitly selected secret inputs instead
  of copying every local credential file with the directory.
- Rejected malformed component lists that could otherwise silently select
  nothing or accept a trailing comma.
- Changed observability verification from `Available` checks to completion of
  the new rollouts, with concurrent waits, failure diagnostics, and cancellation
  cleanup.
- Fixed Kubernetes scanners to consume the configured `SECURITY_ARTIFACT_DIR`
  instead of a hardcoded artifact location.

## Structure worth keeping

| Area | Reason |
| --- | --- |
| `scripts/cd/deploy-kind.sh` and `lib/kind-deployment.sh` | One supported entry point with internal deployment stages; splitting each function into a launcher would add indirection. |
| `deploy/base` and `deploy/overlays/local` | Separate application resources from local PostgreSQL, observability, and gateway composition. |
| Infrastructure, migration, application stages | Preserve migration ordering and targeted application updates. |
| Local builds versus registry publication | Local dirty-worktree iteration and signed immutable delivery have different requirements. |
| Root and `scripts/ci` compatibility wrappers | Documented aliases with argument and signal tests; no duplicated implementation. |
| Modern and legacy Cosign evidence | The existing Kyverno integration explicitly requires the compatibility publication. |

## Remaining findings and choices

| Priority | Finding | Next change to consider |
| --- | --- | --- |
| High | AWS promotion in `Jenkinsfile.assurance` expects `artifacts/build/images.env`, but this separate job has no producer, artifact-copy step, or explicit digest parameters. Its workspace also retains artifacts across builds. | Select an exact successful build's artifacts or accept validated backend/frontend digest inputs, and clean the workspace before execution. |
| High | Both Jenkinsfiles use `agent any`; job-level concurrency settings do not prevent the assurance failure-drill job from disrupting the main job's kind cluster. | Bind cluster work to the intended node and share a cluster lock, or give drills their own cluster. |
| High | `run-kind-failure-drills.sh` deletes the PostgreSQL pod, whose data volume is `emptyDir`, and restores MCP to a hardcoded single replica. | Treat the drill as data-destructive; isolate it and capture/restore the original replica count before expanding its use. |
| Medium | Local CPU autoscaling is declared, but cluster bootstrap does not install a resource metrics API provider. kube-state-metrics serves a different purpose. | Omit the HPA in the local overlay or add and verify the required metrics API. |
| Medium | Namespace overrides in scripts do not rewrite namespaces hardcoded in the manifests and RBAC. | Either explicitly support only the checked-in namespaces or implement consistent namespace transformation. |
| Medium | The source-security render list uses base infrastructure, so it does not schema/policy-check the local PostgreSQL composition. Script tests now render that composition with disposable inputs. | Add a dedicated CI composition with non-secret placeholders to the scanner render stage. |
| Medium | The AWS reference has no application overlay, workload secret integration, or completed account-level validation. | Keep it separate from the working local delivery path until those pieces are implemented and verified. |
| Optional simplification | Full local deployment includes metrics, traces, and Elasticsearch/Kibana logging; smoke verification requires all of them. | Add an explicit minimal-local profile and matching smoke checks if daily development does not need the full observability stack. |

The existing wrappers and observability resources are referenced by documented
workflows. Removing them wholesale would break those workflows. Prefer the
specific cleanup above and explicit profiles over another directory reshuffle.
