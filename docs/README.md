# Documentation

Start with [local setup](developer/local-setup.md) to run the application and the
[codebase guide](developer/codebase-guide.md) to find the implementation.
[System architecture](architecture/system-architecture.md) explains how the
runtime components fit together. Return to the [project overview](../README.md)
for capabilities and the repository map.

Each guide has one primary audience: developer guides explain working on the
repository; architecture guides describe system behavior and contracts; operations
runbooks explain configuring, deploying, observing and recovering environments.

## Developer guides

| Guide | Use it for |
| --- | --- |
| [Local setup](developer/local-setup.md) | Kind development, credentials, model controllers and standalone services |
| [Codebase guide](developer/codebase-guide.md) | Source ownership, entry points and change paths |
| [Backend](developer/backend-guide.md) | Go packages, persistence, worker behavior and backend verification |
| [Frontend](developer/frontend-guide.md) | React features, state, shared components and browser tests |
| [Tooling](developer/tooling-guide.md) | Script ownership, test layout and CI implementation |
| [Contributor CI](developer/ci.md) | Reproducing build, coverage, security and browser checks |
| [Content authoring](developer/content-library.md) | Versioned unit libraries, rulesets and scenario selections |
| [AI comparisons](developer/ai-comparisons.md) | Scripted evaluations, live-provider opt-ins and report interpretation |
| [AI development policy](developer/ai-development-policy.md) | Contribution and artifact-assurance requirements |
| [Cross-review](developer/cross-review.md) | Bounded Codex/Claude review and PR handoffs |

## Architecture and contracts

| Guide | Use it for |
| --- | --- |
| [System architecture](architecture/system-architecture.md) | Runtime components, data flows, durability, security and topology |
| [Configuration](architecture/configuration.md) | Environment files, identity and Secret boundaries |
| [Observability](architecture/observability.md) | Signal paths, telemetry contracts and reliability objectives |
| [Game rules](architecture/game-rules.md) | Battle lifecycle, combat and win conditions |
| [Frontier Siege](architecture/frontier-siege.md) | Expanded roster, structures, map and balance probes |
| [HTTP API](architecture/api.md) | Authentication, routes, errors and examples |
| [OpenAPI](architecture/openapi.yaml) | Machine-readable HTTP contract |

## Environment and application operations

| Guide | Use it for |
| --- | --- |
| [Environments](operations/environments.md) | Registering environments and managing configuration |
| [Deployment](operations/deployment.md) | Staged delivery, migration gates, smoke checks and rollback |
| [Limited-access launch](operations/limited-launch.md) | Access protection, roles, CLI isolation and CI service authentication |
| [Player invitations](operations/player-invitations.md) | Activating and recovering managed Player membership |
| [Observability operations](operations/observability.md) | Metrics, dashboards, logs and diagnostics |
| [Reliability testing](operations/reliability-testing.md) | Disposable load tests, capacity checks and failure drills |
| [Redis](operations/redis.md) | Optional completed-battle caching and recovery |
| [Langfuse](operations/langfuse.md) | Optional AI traces and self-hosted service setup |
| [Supply chain](operations/supply-chain-runbook.md) | Image signing, evidence, admission and recovery |

## Platform and worker operations

| Guide | Use it for |
| --- | --- |
| [Server rebuild contract](operations/server-rebuild-contract.md) | State locations, ownership and recovery boundaries |
| [Server bootstrap](operations/server-bootstrap.md) | Ubuntu host foundation, storage and disposable rehearsal |
| [K3s VM](operations/k3s-vm.md) | Terraform VM provisioning and state recovery |
| [K3s platform](operations/k3s-platform.md) | Cluster controllers, retained storage and network verification |
| [K3s delivery](operations/k3s-delivery.md) | Application delivery, data cutover and recovery |
| [Cloudflare platform](operations/cloudflare-platform.md) | Tunnel, DNS and Access resource ownership |
| [Local HTTPS](operations/local-https-platform.md) | Private TLS trust, routes and restoration |
| [Jenkins operations](operations/jenkins.md) | CI administration, SonarQube and delivery diagnostics |
| [CI bootstrap](operations/ci-bootstrap.md) | Jenkins, agent and SonarQube adoption or reconstruction |
| [Platform jobs](operations/platform-jobs.md) | Auxiliary Jenkins job setup and approved execution |
| [CLI VM platform](operations/cli-vm-platform.md) | Isolated worker VM creation and adoption |
| [CLI bootstrap](operations/cli-bootstrap.md) | Worker service, proxy and firewall configuration |
| [Agent VM](operations/agent-vm.md) | Worker isolation verification, concurrency and recovery |
| [Antigravity CLI](operations/antigravity-cli.md) | Subscription login, local bridge and troubleshooting |
| [Claude CLI](operations/claude-cli.md) | Subscription login, provider restrictions and troubleshooting |

The [infrastructure index](../infra/README.md) maps these guides to their assets
and includes the separate [AWS reference](../infra/aws/README.md).
