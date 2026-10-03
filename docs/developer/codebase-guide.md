# OrdinalFrontier Codebase Guide

The public product name is OrdinalFrontier. Its wordmark and O emblem appear in
the application shell and login page; `frontend/index.html` owns browser metadata
and `frontend/public/ordinalfrontier.svg` is the matching favicon. Historical
repository/package paths and deployment identifiers remain compatible.

| Field | Value |
| --- | --- |
| Status | Current |
| Last reviewed | 2026-10-03 |
| Audience | Contributors navigating or modifying the implementation |
| Scope | Repository layout, entry points, packages, dependencies, tests, and reading paths |

For runtime boundaries, data flows, durability, and security decisions, see the [system architecture](../architecture/system-architecture.md).

AI-created work branches start from the latest fetched `origin/dev` and target
`dev` in pull requests, unless the user explicitly requests otherwise. Promotion
to `main` is separate. See [agent branch guidance](../../AGENTS.md#branches-and-pull-requests)
and the [Jenkins workflow](../operations/jenkins.md#configure-github-integration-branch-protection).
Run applicable local checks before opening a PR, and complete the PR template's
AI-assistance section required by the
[AI development policy](ai-development-policy.md). The repo's
[raise-pr](../../.agents/skills/raise-pr/SKILL.md),
[triage-ci](../../.agents/skills/triage-ci/SKILL.md),
[release-and-recover](../../.agents/skills/release-and-recover/SKILL.md),
[write-project-docs](../../.agents/skills/write-project-docs/SKILL.md), and
[track-backlog](../../.agents/skills/track-backlog/SKILL.md) skills cover
PRs, CI, releases, docs, and backlog work. Claude Code discovers the same
skills through `.claude/skills/` symlinks. Jenkins verifies both `dev` and `main`
PR targets on the existing shared `battlefield-production` agent.

Use the focused guides for implementation details:

- [Local setup](local-setup.md) and [continuous integration](ci.md): development environment and verification workflow.
- [Backend guide](backend-guide.md): package ownership, persistence, configuration, AI execution, and membership.
- [Frontend guide](frontend-guide.md): feature ownership, shared contracts, styles, and browser behavior.
- [Tooling guide](tooling-guide.md): deployment scripts, test commands, assurance, and cross-review.

## Repository map

```text
autonomous_battlefield/
├── .agents/skills/             # Canonical PR, CI, release, docs, and backlog skills
├── .claude/skills/             # Links to the canonical skills for Claude Code
├── .github/pull_request_template.md # PR sections, including the required AI-assistance record
├── backend/
│   ├── cmd/{server,engine,mcp,agent,aieval}/ # Startup and command wiring
│   ├── internal/
│   │   ├── battle/{api,service,catalog,worker,repo,resultcache}/
│   │   ├── faction/{api,service,repo}/
│   │   ├── game/               # Deterministic state transitions
│   │   │   └── controller/     # Infrastructure-free controller contracts
│   │   ├── ai/                 # Decision execution, providers, MCP, evaluations
│   │   ├── admin/              # Model-access handler, service, and repository
│   │   ├── apicontract/        # Tests keeping openapi.yaml in step with served routes
│   │   ├── members/            # Player membership and reconciliation
│   │   ├── generation/         # Faction background generation jobs and quotas
│   │   ├── testutil/           # Shared assertions with lazy failure diagnostics
│   │   ├── apierror/           # Stable JSON error envelope and codes for /v1
│   │   ├── requestid/          # Request correlation ID carried in the context
│   │   └── ...                 # app, authn, config, httpapi, postgres, logging, telemetry
│   ├── migrations/             # Embedded SQL migrations
│   ├── content/                # Shared versioned unit and building libraries
│   ├── rulesets/               # Versioned game rules
│   ├── scenarios/              # Authored battle templates
│   └── evals/                  # Evaluation cases and pricing configuration
├── frontend/src/
│   ├── app/                    # Application shell, routes, integration tests
│   ├── features/{battles,factions,auth,admin}/ # Feature pages, API clients, unit tests
│   ├── shared/{api,domain,components,hooks,styles}/ # Shared contracts, visuals, and foundations
│   ├── pages/                  # General pages, including Home
│   └── test/                   # Shared test setup
├── infra/
│   ├── deploy/                # Kubernetes bases, overlays, policy, supply-chain and CLI VM assets
│   ├── ansible/               # Ubuntu host foundation, Vault inputs, control dependencies
│   ├── libvirt/               # Pinned Terraform VM, network and retained disks
│   ├── k3s/                   # Cluster settings and pinned platform assets
│   ├── jenkins/               # Auxiliary pipelines, job definitions and plugin pins
│   ├── local/                 # Standalone PostgreSQL, optional Redis and DBHub
│   ├── sonarqube/             # Local analysis service
│   └── aws/                   # AWS provisioning with Terraform
├── scripts/
│   ├── infra/                  # Server registration, inspection and Ansible entry point
│   ├── dev/                    # Local application and host-agent launchers
│   ├── test/                   # Test runners, deployment and runtime assurance script tests
│   ├── ci/                     # Lint, builds, coverage summaries, image assurance
│   ├── cd/                     # Deployment, smoke checks, rollback
│   ├── perf/                   # k6 launcher and PostgreSQL query-plan measurement
│   ├── security/               # Security-test launcher
│   ├── ops/                    # Operational reports and failure drills
│   ├── review/                 # Codex/Claude cross-review runner
│   └── lib/                    # Shared script functions
├── tests/performance/          # k6 scenarios and query-plan SQL fixtures
└── docs/{game,developer,architecture,operations}/
```

## Executable entry points

The backend has five commands in one Go module: four runtime processes and the offline AI evaluation command. The application image contains the four runtime binaries.

| Process | Entry point | What the entry point owns |
| --- | --- | --- |
| API | [`backend/cmd/server/main.go`](../../backend/cmd/server/main.go) | Flags, configuration, migrations, PostgreSQL startup, catalog loading, application wiring, HTTP lifecycle |
| Engine | [`backend/cmd/engine/main.go`](../../backend/cmd/engine/main.go) | Configuration, PostgreSQL startup, worker construction, health server, signals, graceful requeue/shutdown |
| MCP | [`backend/cmd/mcp/main.go`](../../backend/cmd/mcp/main.go) | Restricted repository construction, PostgreSQL notification listener, MCP and agent-control routes, HTTP lifecycle |
| Agent | [`backend/cmd/agent/main.go`](../../backend/cmd/agent/main.go) | Profile registry, decision worker, health endpoint, signals, shutdown |
| AI evaluation | [`backend/cmd/aieval/main.go`](../../backend/cmd/aieval/main.go) | Evaluation case execution, pricing inputs, reports, and runtime provenance via `-mode metadata` |

`cmd` packages should remain thin. Domain and use-case behavior belongs under `internal`; entry points should focus on composition and process lifecycle.

The [server rebuild contract](../operations/server-rebuild-contract.md) connects
the host, CI, VM, K3s and CLI entry points to state ownership, rebuild prerequisites
and recovery verification. Its linked runbooks own the operational procedures.

Host bootstrap enters through [`scripts/infra/server.sh`](../../scripts/infra/server.sh).
Its Python wrapper validates private inventory, SSH/machine identity, secret imports
and the host lock; `control-tools.py` installs the hash-locked Ansible environment.
`inspect-host.py` discovers the target's traditional sudo executable, which
`server.py` binds into both SSH and local Ansible inventories. Synthetic prompt
regressions exercise the pinned Ansible password handshake without root access.
`infra/ansible/site.yml` separates foundation setup, private input reconciliation
and verification. The optional `data_storage` role owns the existing ext4 mount,
scoped directories and future K3s libvirt guard; its standalone `files/storage.py`
handles mount identity, fstab preservation and the immutable storage contract.
`test-data-storage.py` covers negative paths; `rehearse-data-storage.py` verifies
privileged behavior only in an owned disposable guest.
`scripts/test/test-server-bootstrap.sh` exercises real Ansible
with synthetic inputs, the shared `foundation/tasks/virtualization-preflight.yml`
package guard, the interruption checkpoint, offline rehearsal safety tests and
storage regressions. The opt-in `scripts/infra/rehearse-server.py` owns disposable
VM creation/cleanup; `scripts/test/rehearse-server-guest.py` runs the privileged
fixture only inside its UUID-matched guest and temporarily installs its embedded
Ansible interruption callback there. The host runner waits for a changed boot ID
before resuming checks. The pinned Ubuntu image is in
`infra/ansible/rehearsal-image.json`. See the
[bootstrap guide](../operations/server-bootstrap.md) for the rehearsal command.

`server.sh ci-*` dispatches to `scripts/infra/ci-bootstrap.py` for private CI
registration, source fingerprints and external Ansible execution. `infra/ansible/ci.yml`
and its `ci_services` role manage the pinned controller, Sonar Compose service,
agent and tools. The role's `tasks/agent-launcher.yml` checks the installed group
launcher as the operator before any controller drain. `ci-runtime.py` implements
secret-safe preflight, build draining,
maintenance backups and service API initialization/verification. `infra/jenkins`
contains the checksum/dependency lock, scoped JCasC/bootstrap definitions and
missing-job-only Job DSL. Platform jobs reuse `seed-platform.py` rather than a
second pipeline definition. `test-ci-bootstrap.py` covers the boundaries;
`run-ci-bootstrap-rehearsal.py` and its UUID-guarded guest fixture exercise actual
services. See [CI bootstrap](../operations/ci-bootstrap.md).

`infra/jenkins/Jenkinsfile.platform` and `scripts/ci/run-platform.sh` enter the reviewed host
workflow in `scripts/infra/platform.py`. It binds a private plan receipt to the
trusted revision and inputs and uses `operation-lock.py` for exclusion and child
cancellation through a shared supervisor that defers startup signals until the
child can be reaped. Deployment scripts validate inherited locks with its `--check`
mode. `infra/ansible/callback_plugins/battlefield_report.py` emits task outcomes
without module output and hashes available task/loop diffs. Reports use an
application-specific machine fingerprint. `setup-platform.py` registers/seals inputs and
opens the SSH tunnel; `seed-platform.py` validates plugins/pipeline and reconciles
owned folder/job/file credentials without triggering a build. Plugin pins live in
`infra/jenkins/platform-plugins.json`; `test-platform.py` exercises lock, receipt,
setup and real callback behavior. See [platform jobs](../operations/platform-jobs.md).

`infra/libvirt` defines the dedicated VM/network and separate OS/data lifecycles.
`infra/jenkins/Jenkinsfile.vm` and `scripts/ci/run-vm.sh` invoke `scripts/infra/vm.py`, which shares
the host operation lock and trusted-main check. `vm-policy.py` validates capacity
settings, resource actions and approval receipts; `vm-tools.py` installs verified
tools/images in a private cache and resolves the shared Ubuntu release pin. `vm-state.py` copies encrypted snapshots to the
registered second disk and validates resource identity during state recovery.
`test-vm.py` exercises negative paths; `check-vm-terraform.py` verifies format and
the pinned provider schema, mocked-provider contracts and real local-backend
lock contention without contacting libvirt; provider-cache initialization is serialized
across concurrent CI checks. The opt-in `rehearse-vm.py` uses the shared
rehearsal VM lifecycle with a larger outer guest, while `rehearse-vm-guest.py`
exercises actual Terraform, replacement, recovery and reboot inside it. The foundation, VM
and K3s rehearsal entry points remove their own script directory from Python's
import path before loading `uuid` in the shared fixture, so local `platform.py` cannot
shadow the standard library. `test-server-rehearsal.py` runs a shared real
subprocess `--help` check for foundation, VM, K3s and delivery entry points with
ordinary Python path settings and a separate working directory. See the
[VM runbook](../operations/k3s-vm.md) for Jenkins and Ansible handoff details.

`infra/jenkins/Jenkinsfile.k3s` and `scripts/ci/run-k3s.sh` run the reviewed guest workflow in
`scripts/infra/k3s.py`; `k3s-config.py` validates cluster settings and renders the
private platform/probe manifests. `infra/k3s/` holds settings and asset checksums.
`infra/ansible/k3s.yml` combines `k3s_storage` (stable disk ownership, fstab,
first-use initialization and startup guard) and `k3s_runtime` (managed K3s,
firewall, pinned controllers and readiness/identity probes with bounded API
reconnect retries). Its firewall helper
records the applied table and detects live rule drift without relying on service
activity alone. The custom
`battlefield_k3s_manifest` module uses server-side Kubernetes diff/apply, including
check-mode drift detection and temporary-path-independent diff headers. Its
controller filter rejects unknown Jobs and retains the Envoy certificate
Job under a specification-derived name for convergence and safe Job upgrades. Registry tasks are opaque and marked no-log.

`test-k3s.py` tests destructive-operation guards, network validation and review
boundaries. `rehearse-k3s.py` runs the actual roles in a generated disposable VM;
`k3s-registry-fixture.py` supplies an authenticated TLS registry with synthetic
credentials and verified public test-image bytes. Its private diagnostic callback
extends the normal sanitized report only for fixture failures. See the
[K3s platform runbook](../operations/k3s-platform.md) for job use and acceptance.

`scripts/ci/verify-frontend-image.py` starts an isolated frontend image with the
production filesystem/user restrictions and verifies HTTP probes, index and entry
assets. Local/PR builds and published-digest scanning invoke it before delivery.
`scripts/test/test-frontend-image.sh` builds tracked frontend inputs with private
checkout permissions and also exercises the unreadable-template rejection in a
real container. Jenkins runs this Docker-backed regression separately from the
command-double deployment suite; see [Jenkins checks](ci.md#pull-request-verification).

`scripts/cd/deploy.sh`, `smoke-test.sh` and `rollback.sh` are the driver-neutral
entry points used by Jenkins; the old Kind-named scripts forward arguments and
signals. `cd/lib/kubernetes-deployment.sh` stages selected shared overlays and
composes `infra/deploy/overlays/k3s` when the profile selects K3s. `lib/k3s-delivery.py`
performs read-only platform/retained-volume checks. `production_recovery.py`
binds verified images to the applied database migration ledger. The required
`test-k3s-delivery.py` suite exercises the real commands with isolated external
command doubles, alongside the existing Kind and registry rejection suites.
See [K3s delivery](../operations/k3s-delivery.md) for registration and migration.

The disposable K3s application rehearsal is `scripts/infra/rehearse-k3s-delivery.py`.
It reuses the platform roles and owned-VM fixture, runs signed-image delivery with
synthetic application inputs, and optionally deploys two distinct signed releases
before restoring the previous exact image pair. Checkpoints verify healthy image
convergence, private readiness, migration state and retained claims. The offline
`test-k3s-delivery-rehearsal.py` suite rejects false recovery evidence, including
unchanged images after rollback, missing claims and changed migrations; overall
acceptance also requires owned-VM cleanup. See [its runbook](../operations/k3s-delivery.md#disposable-delivery-rehearsal)
for the remaining public and CLI bridge acceptance checks.

## Common change paths

### Add a battle API field

1. Decide whether the source of truth is `game.State`, `BattleRun`, or derived presentation data.
2. Update the service type/use case.
3. Update repository serialization or query scanning if persisted.
4. Update the handler response.
5. Update `frontend/src/features/battles/api/battles.ts`.
6. Update the consuming page/component and both backend/frontend tests.

### Add a plan field

1. Add it to `controller.PlanProposal`.
2. Add bounds and semantic validation.
3. Add it to the MCP submission schema/input mapping.
4. Apply it deterministically in `game.Match`.
5. Update scenario YAML if scripted/fallback values need it.
6. Update decision persistence, API/frontend types, display, and tests.

### Change model role access

Admin model-access role validation applies equally to API and CLI profiles.
`backend/internal/admin/service_test.go` covers saving and revoking Player CLI
access through the durable policy; the admin page tests cover the Player checkbox
and persistence across reloads. Player catalog tests retain gameplay-only projection
while exposing explicitly approved, available CLI profiles.

### Add a model provider

1. Add a provider adapter implementing `ai.TurnProvider`, or a CLI branch implementing `DecisionRunner` behavior.
2. Extend profile validation and availability rules.
3. Extend the agent's default runner factory.
4. Add environment/configuration and deployment secret fields.
5. Add provider contract, refusal, usage, and tool-call tests.
6. Keep the MCP tool surface unchanged unless the domain contract itself changes.

### Add durable battle behavior

1. Add a forward-only migration.
2. Update repository interfaces only with operations required by a use case.
3. Implement guarded SQL using battle attempt and lease ownership where applicable.
4. Update service/worker behavior.
5. Add stale-owner, retry, and shutdown tests.

## Suggested reading order

### Learn the whole request-to-battle path

1. [`backend/internal/game/types.go`](../../backend/internal/game/types.go)
2. [`backend/internal/game/match.go`](../../backend/internal/game/match.go)
3. [`backend/internal/battle/service/service.go`](../../backend/internal/battle/service/service.go)
4. [`backend/internal/battle/worker/worker.go`](../../backend/internal/battle/worker/worker.go)
5. [`backend/internal/battle/repo/battles.go`](../../backend/internal/battle/repo/battles.go) and [`queue.go`](../../backend/internal/battle/repo/queue.go)
6. [`features/battles/pages/BattlePage.tsx`](../../frontend/src/features/battles/pages/BattlePage.tsx)

### Learn the AI decision path

1. [`backend/internal/ai/types.go`](../../backend/internal/ai/types.go)
2. [`backend/internal/battle/worker/decisions.go`](../../backend/internal/battle/worker/decisions.go), starting at `resolveDecisionWindow`
3. [`backend/internal/battle/repo/decisions.go`](../../backend/internal/battle/repo/decisions.go)
4. [`backend/internal/ai/mcpserver/server.go`](../../backend/internal/ai/mcpserver/server.go)
5. [`backend/internal/ai/worker/worker.go`](../../backend/internal/ai/worker/worker.go)
6. [`backend/internal/ai/runner/api.go`](../../backend/internal/ai/runner/api.go)
7. [`backend/internal/ai/providers/openai.go`](../../backend/internal/ai/providers/openai.go) or [`gemini.go`](../../backend/internal/ai/providers/gemini.go)

### Learn application composition

1. [`backend/cmd/server/main.go`](../../backend/cmd/server/main.go)
2. [`backend/internal/app/app.go`](../../backend/internal/app/app.go)
3. A feature's `api`, `service`, and `repo` packages in that order
4. The matching frontend API client and page

### CLI VM lifecycle and host bundle

`scripts/infra/cli-vm.py` is the trusted-main operator entry point for reviewed
VM plan/apply/verify. `infra/ansible/cli-vm.yml` calls `library/battlefield_cli_vm.py`;
`module_utils/battlefield_cli_vm.py` owns privileged creation/adoption and immutable
local state, while `battlefield_cli_vm_policy.py` checks layout and receipts.
`infra/deploy/agent-vm/domain.xml`, `network.xml` and `filter.xml` declare the fixed
layout. The wrapper uses the shared operation lock and existing sudo selection.
Its generated host-local handoff is consumed by `cli-bootstrap.py` before guest
configuration validation. `test-cli-vm.py` exercises layout, approval and handoff
failures; `rehearse-cli-vm.py`/`rehearse-cli-vm-guest.py` run the actual nested VM
lifecycle with UUID-bound disposable cleanup. See [CLI VM platform](../operations/cli-vm-platform.md).


`scripts/infra/cli-bootstrap.py`, dispatched by `server.sh cli-*`, registers the
public host bundle, captures existing guest artifacts with an encrypted worker
environment and installed isolation helper, and builds the two-target Ansible inventory. `infra/ansible/cli.yml`
preflights both targets before the `cli_host`/`cli_guest` roles and stops guest
installation on host failure. The shared
`module_utils/battlefield_cli.py` validates immutable ownership, managed files and
libvirt isolation; `battlefield_cli_guard.py` checks live target metadata and the
firewall, including the narrowly scoped unmodified Squid package-default
exception. `filter_plugins/cli_bootstrap.py` enforces worker-only inputs, fresh
profile gating and safe version failure labels. `test-cli-bootstrap.py` covers the guards; the optional
`run-cli-bootstrap-rehearsal.py`/`rehearse-cli-bootstrap.py` pair verifies actual
Ansible service lifecycle only in a UUID-bound disposable guest.


`scripts/ops/render-agent-vm-host.py` renders the dedicated systemd, Caddy and
firewall files from `infra/deploy/agent-vm/host` into a new directory without touching
services. The loopback bridge defaults to 28084, separate from Jenkins smoke
ports; `--bridge-port` validates an unprivileged port and substitutes the same
value in the Kubernetes forward and Caddy upstream. Guest-facing ports and the
MCP Host header remain fixed. `scripts/test/test-agent-vm-host.py` checks the
matched default/CLI override, invalid-input rejection, isolation configuration
and refusal to overwrite a reviewed bundle. See the [CLI VM runbook](../operations/agent-vm.md).

### Cloudflare capture and lifecycle

`scripts/infra/cloudflare.py` handles encrypted capture, fresh-create preparation
and trusted-main plan/apply/verify under the controller and host locks.
`infra/ansible/cloudflare.yml` invokes `library/battlefield_cloudflare.py` locally;
`module_utils/battlefield_cloudflare.py` owns the bounded API client, paginated
inventory, receipts, journal and private handoff. Its companion
`battlefield_cloudflare_policy.py` validates the narrow Access/tunnel/DNS contract.
Existing delivery, runtime player memberships and service-token secrets stay in
their existing ownership boundaries. `scripts/test/test-cloudflare.py` covers the
synthetic lifecycle, sanitized real API response shapes and negative cases, including
actual Ansible checks that remote strings remain literal JSON. The playbook passes
only a private input-file path into the module. `scripts/infra/rehearse-cloudflare.py`
exercises a uniquely named idle cloud fixture with ID-bound cleanup and preservation
checks. See [Cloudflare platform](../operations/cloudflare-platform.md).

### Native local HTTPS lifecycle

`scripts/infra/https.py` provides host-local encrypted capture and reviewed
plan/apply/verify through `infra/ansible/https.yml`. Its root-side
`https-runtime.py` owns the narrow Caddyfile parser, retained CA validation,
root-only ownership/receipts and install/adopt/migrate operations. Literal
private JSON enters Ansible only as a file path. Migration derives routing from
the registered K3s production profile, redirects the application alias to the
public Access-protected origin and retargets monitoring without replacing the
shared Caddy binary or touching the CLI proxy. `test-local-https.py` covers
policy, registered K3s routing and failure paths; `rehearse-https.py` and
`scripts/test/rehearse-https-guest.py` exercise the actual lifecycle and reboot
with synthetic secrets in a disposable VM, including the runbook's exact
ownership archival command and re-registration after a capture/node change. See the
[local HTTPS runbook](../operations/local-https-platform.md).
