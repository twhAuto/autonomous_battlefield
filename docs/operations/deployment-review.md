# Deployment structure review

Static review: 2026-09-05; findings rechecked against the working tree on
2026-09-07. This is a review record, not the project backlog or evidence of a
successful server deployment. Project task status remains in Notion.

## Current structure

The deployment entry points share environment identity checks and component
selection. Local dev loads working-tree images; Jenkins production deploys
verified GHCR digests. Kustomize stages infrastructure, migrations, application,
observability, and gateway resources. See [deployment](deployment.md),
[environments](environments.md), and [Jenkins](jenkins.md) for current procedures.

Earlier cleanup centralized scanner pins and manifest rendering, limited staging
to selected overlays and secret inputs, rejected malformed component selections,
and made observability checks wait for new rollouts. These behaviors remain in
the scripts and their regression checks.

## Cleanup from the pipeline review

- Go and frontend test-only changes previously selected image publication and
  rollout. The classifier now excludes those paths after CI gates have run,
  including the existing CI test wrappers. Production-to-test renames still
  select deployment because both paths are examined.
- Five copies of temporary Docker authentication setup across the Jenkinsfiles
  and rollback script now use `scripts/ci/with-ghcr-auth.sh`. Credential cleanup,
  failure propagation, input/argument forwarding, and cancellation have regression
  coverage using a stub Docker command.
- Repeated child-stage deployment conditions were removed where the enclosing
  production-rollout stage already applies the identical condition. A duplicate
  project-directory assignment was also removed from the deployment launcher.

PR verification and main quality checks remain separate because they verify
separate revisions. Local-image scans on PRs and published-digest scans on main
cover different artifacts. The two Cosign evidence formats remain required by
the repository's promotion and admission paths. AWS, failure drills, and the
observability stack have documented entry points; this review does not establish
that they are unused.

## Findings still present

| Priority | Finding | Implication |
| --- | --- | --- |
| High | `Jenkinsfile.assurance` invokes AWS promotion without producing or selecting `artifacts/build/images.env`; the job also retains its artifact workspace. | Promotion needs explicitly selected successful-build evidence and clean artifact handling before routine use. |
| High | The PostgreSQL failure drill selects `app.kubernetes.io/name=postgres`, but the StatefulSet pods use `app.kubernetes.io/name=autonomous-battlefield` and `app.kubernetes.io/component=database`. | The delete can match no pods, so the report does not prove a PostgreSQL restart. |
| Medium | The failure drill restores MCP to one replica without capturing the prior count or holding an outage interval. | Its report does not establish representative outage/recovery behavior. Keep it confined to the registered disposable dev environment. |
| Medium | The API HPA exists but bootstrap does not install metrics-server. | CPU scaling remains an exercise until a resource metrics provider is installed and verified. |
| Medium | Source-security rendering uses base infrastructure rather than the local PostgreSQL composition. | Script render tests cover local composition, but it still needs a dedicated schema/policy scan with non-secret fixtures. |
| Medium | AWS has Terraform infrastructure but no application overlay, workload secret integration, or recorded account validation. | Treat AWS as a reference, not a completed deployment path. |

## Earlier findings superseded by the current code

- Both Jenkinsfiles now select `battlefield-production`; failure drills require
  a separately registered disposable dev profile and reject production.
- PostgreSQL uses a 1 GiB PVC, not `emptyDir`. Pod replacement preserves that
  claim; losing the Kind cluster can still lose its data.
- The environment resolver fixes the namespace to `battlefield` and rejects
  conflicting overrides, matching the checked-in manifests.
- `deploy-local.sh` defaults to application dependencies and gateway resources;
  dev smoke checks omit observability unless explicitly enabled.

Deployment classifier, registry authentication, overlay rendering, environment,
command-wrapper, runtime-assurance, and security-launcher regression checks passed.
These checks use disposable fixtures and stub external mutations. Shell syntax
and documentation links were checked. A live Jenkins pipeline validation/run was
not performed.

This recheck did not run a deployment, registry promotion, failure drill, or AWS
apply. Server identity, access, backups, and successful rollout still require
operator evidence from those environments.
