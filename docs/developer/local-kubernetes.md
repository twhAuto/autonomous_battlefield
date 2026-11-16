# Local Kubernetes deployment

Use this walkthrough to deploy and inspect the application on the development
Kind cluster from WSL. Start with [local setup](local-setup.md) to register the
cluster and prepare `.env`; this page covers the individual stages behind
`./scripts/cd/deploy-local.sh`. How delivery works in general, including
component selection and production differences, is in
[application deployment](../operations/deployment.md).

The recommended local cluster is [`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/),
which runs Kubernetes nodes as Docker containers.

## Prerequisites

From WSL, confirm Docker and `kubectl` are usable:

```bash
docker info
kubectl version --client
```

If `docker info` cannot reach the daemon, start Docker Desktop and enable its
WSL integration for the current distribution before continuing.

### Install kind

With Go already installed, install the current pinned `kind` release:

```bash
go install sigs.k8s.io/kind@v0.32.0
export PATH="$(go env GOPATH)/bin:$PATH"
kind version
```

Add the Go binary directory to the shell profile if it is not already on
`PATH`. The upstream quick-start page should be used when intentionally updating
the pinned version.

## Register or resume the dev cluster

Follow the [local setup guide](local-setup.md) to register the existing
cluster, prepare independent dev secrets, and deploy. For a stopped or absent
dev cluster, `./scripts/cd/create-kind-cluster.sh` starts existing nodes or creates
a first cluster. It never recreates a registered cluster and rejects `--recreate`.

The dedicated kubeconfig uses context `battlefield-dev`. Production uses
`battlefield-production` on the server; neither deployment depends on your
current kubectl context. The dev Kind cluster keeps the name `battlefield`;
production uses the separate K3s VM.

## Prepare dev secrets and model providers

After registering dev, run `./scripts/cd/prepare-dev-secrets.sh`. It preserves
existing PostgreSQL credentials and creates independent authentication and
capability secrets in the project-root `.env`; an existing `.env` is never
overwritten. `./scripts/cd/deploy-local.sh` renders separate Kubernetes Secrets
from `.env` and restarts application pods. [Local setup](local-setup.md#application-settings)
describes the authentication modes and secret sizes, and the
[configuration model](../architecture/configuration.md#file-layout) the file
layouts. Keep `BATTLEFIELD_AUTH_COOKIE_SECURE=false` for the loopback HTTP gateway.

New provider profiles start unavailable, so scripted battles need no provider
credentials. The deployment reuses `DATABASE_URL` for the AI gateway when `.env`
has neither `AI_GATEWAY_DATABASE_URL` nor its pre-rename name `MCP_DATABASE_URL`;
production must use the restricted login described in
[database access](../architecture/database.md#the-battlefield_ai_gateway-role).
To enable an API provider, edit `.env` using [`.env.example`](../../.env.example):
add the provider key and model, and set that provider's `*_PROFILE_AVAILABLE` to
`true`.

### Subscription CLIs

Keep `CODEX_CLI_PROFILE_AVAILABLE`, `GEMINI_CLI_PROFILE_AVAILABLE` (which controls
Antigravity) and `CLAUDE_CLI_PROFILE_AVAILABLE` `false` in `.env`. For a local Kind
battle, `scripts/dev/run-k8s-cli-agent.sh` sets them while an authenticated host
worker is connected:

```bash
./scripts/dev/run-k8s-cli-agent.sh codex
```

The helper supports `auto`, `codex`, `antigravity`, `claude`, `both` and `all`
(`gemini` is a compatibility alias). `both` means Codex plus Antigravity, and
`all` requires all three CLIs. `auto` selects every available authenticated CLI.
Local CLIs allow the selected stable version or newer: Codex `0.159.0+`,
Antigravity `1.5.0+` and Claude `2.1.325+`. A saved login is required.
Name a CLI explicitly to see why it was rejected.

The host agent runs `HOST_AGENT_CONCURRENCY` decision slots (default `8`, range
1–8) shared across the enabled CLIs. A model-vs-model battle needs two at once, so
eight covers the engine's four concurrent battles. Set the database CLI limit to
the same number ([limits and the stop switch](../operations/access.md#limits-and-the-stop-switch)
explains why):

```bash
DEPLOY_ENV=dev BATTLEFIELD_POSTGRES_POD=battlefield-postgres-0 ./scripts/ops/ai-policy.sh cli-slots 8
./scripts/dev/run-k8s-cli-agent.sh auto
```

To reduce load, run fewer slots, for example `HOST_AGENT_CONCURRENCY=2`, and set
`cli-slots 2` to match.

The helper checks the CLI version policy and the existing host login, forwards
only the internal AI gateway service to loopback, runs the decision agent on the
host, and makes selectable through the API, while it runs, only the profiles the
agent's `/status` reports it serves. It prints each profile the agent dropped with
the agent's reason. A CLI that updated itself after the helper checked its version
fails the agent's own check, so the helper then re-pins the installed versions and
restarts the agent once; it exits if the agent serves none of the selected CLIs.
No CLI login directory or provider credential is mounted into the cluster. Keep it
running for the whole battle and press Ctrl+C afterwards. The generated host
profile passes `--model gpt-5.6-luna` to Codex, `--model gemini-3.8-flash-medium`
to Antigravity and `--model sonnet` to Claude, matching the profiles deployed to
the API. Install and sign in with [Claude setup](../operations/claude-cli.md) and
[Antigravity setup](../operations/antigravity-cli.md). Do not reuse these
credentials outside the disposable local cluster.

## Build commit-addressed images

Commit the intended source first and confirm the worktree is clean:

```bash
git status --short
./scripts/ci/build-images.sh
```

To simulate CI build 27 locally after committing the changes:

```bash
CI_BUILD_NUMBER=27 ./scripts/ci/build-images.sh
```

The build refuses a dirty worktree by default because a Git SHA cannot identify
uncommitted source. For an intentional local experiment only, set
`ALLOW_DIRTY=1`; do not publish an image produced that way.

Local dev deploys the full-SHA tag loaded into Kind. Image names, `VERSION` and
build-number aliases are described in
[image identity and releases](../operations/deployment.md#image-identity-and-releases).

## Deploy a revision

The deployment script defaults to a full deployment of the checked-out commit:
it loads both tagged images into `kind`, applies infrastructure, runs the
migration gate, waits for the application rollouts, deploys Prometheus,
Grafana, Fluent Bit, Elasticsearch, and Kibana, and programs the local Gateway
API routes:

```bash
./scripts/cd/deploy.sh
```

Application rollouts default to five minutes. The observability stack has a
separate 15-minute budget because a new single-node Kind cluster may serialize
the Prometheus, Grafana, Elastic, OpenTelemetry, and Tempo image pulls. Override
these with positive seconds values such as `ROLLOUT_TIMEOUT=600s` and
`OBSERVABILITY_ROLLOUT_TIMEOUT=1200s` on a slower connection.
Each observability Deployment must finish its new rollout; waits run concurrently
within that budget. Cancellation stops outstanding rollout waits. Timeout
output includes the failed workload descriptions, container logs,
and recent namespace events.

Pass a previous full SHA to deploy a locally built revision explicitly:

```bash
./scripts/cd/deploy.sh "$(git rev-parse HEAD)"
```

For an incremental manual deployment, set `DEPLOY_COMPONENTS`. The image build
and scan scripts consume the same value and ignore non-image components:

```bash
# Frontend source changed: build, scan, and roll only the frontend.
DEPLOY_COMPONENTS=frontend ./scripts/ci/build-images.sh
DEPLOY_COMPONENTS=frontend ./scripts/ci/scan-images.sh "$(git rev-parse HEAD)"
DEPLOY_COMPONENTS=frontend ./scripts/cd/deploy.sh "$(git rev-parse HEAD)"

# Only dashboards, alert rules, or observability manifests changed.
DEPLOY_COMPONENTS=observability ./scripts/cd/deploy.sh "$(git rev-parse HEAD)"
```

See [deployment components](../operations/deployment.md#deployment-components) for
every component name and how Jenkins selects them.

## Inspect each stage

The following sections show individual stages for diagnosis. Direct `kubectl`
commands do not use the scripts' environment resolver. In a dedicated dev shell,
select the registered kubeconfig before running the inspection or cleanup commands:

```bash
export KUBECONFIG="$HOME/.config/autonomous-battlefield/dev/kubeconfig"
kubectl config current-context  # must be battlefield-dev
```

For production inspection, use the production profile as described in
[production setup](../operations/deployment.md#production-setup); local cleanup commands below are for dev.

### PostgreSQL and configuration

```bash
kubectl rollout status statefulset/battlefield-postgres \
  --namespace battlefield \
  --timeout=120s
```

The local PostgreSQL StatefulSet uses a 1 GiB persistent volume from `kind`'s
default local-path provisioner. It is suitable for learning only.

### Migration gate

The migration Job runs the server with `-migrate` and requires the
`battlefield-auth` Secret because startup still validates API configuration.
Deployment supplies the same dev/production environment as the API; CLI profiles
are disabled in this Job.

The deployment script deletes the previous completed Job, recreates it with the
selected backend image, and waits for success. Inspect the result with:

```bash
kubectl wait --for=condition=complete \
  job/battlefield-migrate \
  --namespace battlefield \
  --timeout=120s

kubectl logs job/battlefield-migrate --namespace battlefield
```

Do not apply the application stage if the Job fails.

### Application

```bash
kubectl rollout status deployment/battlefield-api \
  --namespace battlefield \
  --timeout=120s
kubectl rollout status deployment/battlefield-engine \
  --namespace battlefield \
  --timeout=120s
kubectl rollout status deployment/battlefield-agent \
  --namespace battlefield \
  --timeout=120s
kubectl rollout status deployment/battlefield-ai-gateway \
  --namespace battlefield \
  --timeout=120s
kubectl rollout status deployment/battlefield-frontend \
  --namespace battlefield \
  --timeout=120s

kubectl get pods,services,jobs,persistentvolumeclaims \
  --namespace battlefield
```

The base application retains a portable Ingress for later cloud overlays. The
local overlay removes that unhandled resource and uses Gateway API routes
managed by Envoy Gateway instead. The API HPA remains a scaling exercise; its
CPU target is `<unknown>` until metrics-server is installed.

### Observability

See [observability operations](../operations/observability.md) for deployment, rollout checks,
Grafana/Kibana access, direct service inspection, and storage/security limits.
The dev smoke check skips this stack by default; set `SMOKE_TEST_OBSERVABILITY=1`
to verify it after installing the full overlay.

## Open and verify the application

Open `http://battlefield.localhost:8888`. The Gateway API routes `/v1` directly
to the API and all other paths to the frontend. The `.localhost` domain resolves
to loopback without an `/etc/hosts` entry. The frontend Nginx process retains
its internal `/v1` proxy so direct Service access and cloud deployment overlays
continue to work.

Run the same non-interactive verification used by Jenkins at any time:

```bash
./scripts/cd/smoke-test.sh
```

It verifies frontend access and API readiness, authentication, and scenario
responses. With `SMOKE_TEST_OBSERVABILITY=1` (the production default), it also
checks Grafana, Kibana, metrics, and log ingestion. Each Prometheus query gets
a 120-second startup window (set `SMOKE_TEST_PROMETHEUS_TIMEOUT_SECONDS` to
an integer from 1 to 600 to override). Failed queries remain failures; on timeout,
the check prints up to 50 application/node scrape targets with health, last scrape
time and bounded scrape errors. Fetching those diagnostics takes at most five
additional seconds. Temporary diagnostic
port-forwards are removed automatically. When
the optional `battlefield-auth` Secret enables authentication, the smoke test
reads its administrator credentials without printing them, logs in through the
gateway, and keeps the resulting session cookie only in its private temporary
directory. The check fails with an explicit configuration error if that Secret
enables Secure cookies while verification is using the local HTTP gateway.

## Diagnose a local deployment

Start with workload state, recent events, and the application logs:

```bash
kubectl get deployments,pods,services,endpointslices \
  --namespace battlefield \
  --output wide
kubectl get events --namespace battlefield \
  --sort-by='.metadata.creationTimestamp'
kubectl logs deployment/battlefield-api \
  --namespace battlefield --tail=100
kubectl logs deployment/battlefield-engine \
  --namespace battlefield --tail=100
kubectl logs deployment/battlefield-agent \
  --namespace battlefield --tail=100
kubectl logs deployment/battlefield-ai-gateway \
  --namespace battlefield --tail=100
kubectl logs deployment/battlefield-frontend \
  --namespace battlefield --tail=100
```

Use `kubectl describe pod <pod-name> --namespace battlefield` for probe,
scheduling, image, or volume failures. A completed migration pod is expected;
inspect `job/battlefield-migrate` only when its Job did not reach `Complete`.

## Remove the local deployment

Deleting both local namespaces also deletes the PostgreSQL claim, observability
data, and logging collector. Remove Prometheus's cluster-scoped kubelet access
at the same time:

```bash
kubectl delete clusterrole/battlefield-prometheus-kubelet \
  clusterrolebinding/battlefield-prometheus-kubelet \
  --ignore-not-found
kubectl delete namespace battlefield battlefield-logging
```

Deleting the entire `kind` cluster has the same data-loss consequence.
