# Documentation

Start with [game rules](game/game-rules.md) to understand battles,
[local setup](developer/local-setup.md) to run the application, or the
[codebase guide](developer/codebase-guide.md) to find the implementation.
[System architecture](architecture/system-architecture.md) explains runtime
components. Operators can start with [environment operations](operations/environments.md)
for application configuration and delivery, or the
[server rebuild contract](operations/server-rebuild-contract.md) for host and
platform recovery. Return to the [project overview](../README.md) for capabilities
and the repository map.

Guides are grouped by purpose: game guides explain battle rules and scenario
mechanics; developer guides explain working on the repository; architecture guides
describe runtime components and technical contracts; operations runbooks explain
configuring, deploying, observing and recovering environments.

## Game rules and scenarios

| Guide | Use it for |
| --- | --- |
| [Game rules](game/game-rules.md) | Battle lifecycle, combat and win conditions |
| [Frontier Siege](game/frontier-siege.md) | Expanded roster, structures, map and balance probes |

## Developer guides

| Guide | Use it for |
| --- | --- |
| [Local setup](developer/local-setup.md) | Kind development, credentials, model controllers and standalone services |
| [Codebase guide](developer/codebase-guide.md) | Source ownership, entry points and change paths |
| [Component map](developer/component-map.md) | Generated table of which internal packages each backend binary links |
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
| [Database](architecture/database.md) | Schema, process access, claim and admission rules, and migrations |
| [Configuration](architecture/configuration.md) | Environment files, identity and Secret boundaries |
| [Observability](architecture/observability.md) | Signal paths, telemetry contracts and reliability objectives |
| [HTTP API](architecture/api.md) | Authentication, permissions, errors, retries and battle-data behavior |
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
| [CLI worker operations](operations/cli-worker.md) | Jenkins job that keeps the CLI VM, bridge, worker secret and worker release in order |
| [CLI worker verification](operations/agent-vm.md) | Pinned artifacts, provider login, isolation, concurrency and recovery |
| [Antigravity CLI](operations/antigravity-cli.md) | Subscription login, local bridge and troubleshooting |
| [Claude CLI](operations/claude-cli.md) | Subscription login, provider restrictions and troubleshooting |

The [infrastructure index](../infra/README.md) maps these guides to their assets
and includes the separate [AWS reference](../infra/aws/README.md).
