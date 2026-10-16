# Self-hosted AI observability

Langfuse receives AI observations through the existing OpenTelemetry Collector.
Tempo retains the infrastructure trace pipeline. The Langfuse pipeline exports
only spans with `langfuse.observation.type`; prompts, observations, tool payloads,
capabilities, CLI diagnostics, and private reasoning are not exported.

## Install and enable

Deploy the updated backend, AI gateway, agent, frontend and collector manifests
first. Then, from the repository root on the registered deployment machine:

```bash
DEPLOY_ENV=dev ./scripts/cd/deploy-langfuse.sh
```

The script requires kubectl, Python 3, Kubernetes 1.28+, and a default
StorageClass. It uses installed Helm when available. On Linux amd64, missing
Helm is automatically downloaded from get.helm.sh at version 3.19.0, checked
against a pinned SHA-256 digest, and removed with the temporary workspace on exit.
This requires no sudo or system installation. On other platforms, install Helm first. It uses the existing environment identity preflight. It installs
cert-manager 1.20.2 and ClickHouse operator 0.0.5 only when their CRDs are absent,
then the official Langfuse chart 2.1.0 / application 4.24.0. Existing operators
remain managed by their current owner. An existing operator must support both
ClickHouseCluster and KeeperCluster.

The single-node deployment has dedicated PostgreSQL, Valkey, ClickHouse, Keeper,
and SeaweedFS volumes totaling 54 GiB of requested storage. Allow approximately
5 GiB of additional requested memory, plus headroom for startup and ingestion.
This is a home-lab configuration with one replica per store, not an HA deployment.
Back up the Helm-managed credential Secrets, bootstrap Secret and persistent
volumes together. Trace retention is unlimited by default; monitor disk usage.

The script generates the initial user and project credentials once in the
`battlefield-langfuse-bootstrap` Secret. Upgrades reuse it. Only the ingestion
key pair is copied to `battlefield-langfuse-export`, which the collector reads.
The application never receives Langfuse API keys. Temporary credential files
have private permissions and are removed on exit. Do not delete bootstrap or
Helm-managed Secrets when upgrading or restoring existing volumes.

Installation immediately enables the collector pipeline, adds the Langfuse route
to the shared Envoy gateway, and restarts the API for trace links. Subsequent
observability deployments and host CLI launches detect the installed project via
`battlefield-langfuse-links`; no shell export is required. An explicit
`LANGFUSE_ENABLED=0` or `1` overrides detection. A cluster connection error stops
the command rather than silently disabling tracing.
Langfuse is a separate Helm release; normal application deployment does not
upgrade or uninstall its stores.

Open http://langfuse.localhost:8888 directly, just like Grafana and Kibana; no
port-forward is needed. Log in as `admin@battlefield.local`. Retrieve the
initial password privately in your terminal; do not paste it into logs or chat:

```bash
kubectl --context "$KUBERNETES_CONTEXT" -n battlefield get secret battlefield-langfuse-bootstrap \
  -o jsonpath='{.data.LANGFUSE_INIT_USER_PASSWORD}' | base64 --decode
```

Use the context from your registered environment if `$KUBERNETES_CONTEXT` is
not exported in the current shell. Public signup is disabled. For a different
URL, configure the gateway/TLS route, update `langfuse.nextauth.url` in the
Helm values and `LANGFUSE_BASE_URL` in `battlefield-langfuse-links`, then restart
the API. The default login callback URL and decision trace links both use
`http://langfuse.localhost:8888`.

## What the numbers mean

- One decision trace is keyed by the immutable request ID. It includes worker
  duration, model observations, MCP tools, and submission acceptance/rejection.
  Request IDs correlate all observations. Worker/model metadata also identifies
  battle, faction, attempt, target tick and profile; context spans add the context
  schema and section byte counts. Model spans identify prompt/tool versions.
- API generations represent individual `NextTurn` attempts. Their reported token
  usage is summed for the saved decision. Requests that fail without returning
  usage cannot contribute known token totals.
- Codex runs with `--json`; terminal `turn.completed` events supply input, cached
  input, output and reasoning counts when present. Its observation represents a
  CLI session, not an individual internal model call.
- Antigravity uses its terminal JSON usage envelope. Its input/output totals are
  session aggregates. Internal model-call counts, cache and reasoning counts
  remain unavailable because the supported envelope does not report them.
- `modelCalls` and `cliInvocations` are distinct. The old `requests` field remains
  for historical compatibility and is labeled as legacy in the inspector.
  Missing optional counters display `—`; explicitly reported zero displays zero.
- Input includes cached input. Langfuse receives mutually exclusive `input` and
  `input_cached_tokens` categories, so cached tokens are not counted twice. Output
  includes reasoning; Langfuse receives mutually exclusive `output` and
  `output_reasoning_tokens` categories, not the subset added again.
  Gemini's candidates and thoughts are normalized into total output.
- MCP spans measure tool latency and serialized response bytes. Successful unit
  lookups include authorized unit IDs and requested count. Raw input arguments
  and guidance are excluded. Initial-context spans record bytes by section;
  bytes are not token estimates. Model spans also record submitted prompt bytes
  (CLI), or system/message/tool-schema bytes (API); hidden CLI scaffolding is
  outside those byte measurements.

For tools v7, `get_decision_context` returns one JSON text block instead of text
plus a duplicate structured result. `battlefield.tool.response_bytes` measures
the serialized MCP result (including escaping); `battlefield.tool.text_bytes`
measures readable text and `battlefield.tool.structured_bytes` measures the
structured result, or zero when absent. These are byte measurements, not token
counts. Provider-reported generation usage remains the source for input tokens.
Count `get_decision_context` spans within a decision to detect repeated reads.
For Antigravity/Gemini CLI, the first read is normally the host's prefetch: the
runner embeds its JSON into the initial prompt to avoid large-tool file
offloading. A read span alone therefore does not establish a model-issued call
or duplicate model input. API runners likewise select one readable result.
The prompt instructs agents to reuse a successful snapshot; this does not impose
a new hard call limit or prevent retries after delivery failures. Deploy the AI gateway
service and rebuild/restart API and host CLI agents to pick up the new transport
and embedded prompt; existing traces remain unchanged.

Thus a Gemini CLI decision reporting 30k input tokens may include repeated
context across its internal model calls. It is not evidence of a 30k initial
prompt. Compare complete reported input totals, lookup counts, duration, accepted
submissions and fallback rates when measuring compact-context savings. Langfuse
model pricing estimates are not subscription billing statements. Existing eval
reports remain the source of decision-quality comparisons; this change does not
introduce LLM judges or claim measured token savings.

## Verify and troubleshoot

1. Start the host CLI agent normally in the registered environment.
   The bridge forwards the collector in addition to the AI gateway. Kubernetes API agents
   already have the collector endpoint configured.
2. Run a short model battle. Inspect the saved decision's usage scope and click
   **View in Langfuse**. New records get this link only when the worker recorded
   a trace and the API has a configured base URL/project. Legacy decisions do not
   acquire fictitious trace links.
3. Check the trace contains model observations and `get_decision_context`,
   optional `get_unit_details`, and `submit_strategy_plan` tool observations.
   A direct submission is allowed; a lookup is optional.
4. Verify failed tools are visible and accepted submission metadata matches the
   application record. Check collector logs for rejected batches, queue pressure,
   or authentication failures; API keys must never appear in logs.

AI observations are sampled at 100% independently of infrastructure's default
10% sampling, including when the upstream battle span was unsampled. Trace IDs
and parent IDs are derived from the authenticated request ID on the AI gateway, so opaque
CLI transports cannot lose correlation by dropping trace headers. Export runs
asynchronously with bounded queues. Collector failure does not prevent strategy
submission, but queue overflow or shutdown can lose telemetry. No sampling or
export mechanism can reconstruct usage missing from a CLI response.

Langfuse runs only on Kind; the K3s delivery profile rejects it. Deployment
enables export whenever the `battlefield-langfuse-links` ConfigMap exists. To stop
export, delete that ConfigMap (which also removes inspector trace links), restart
the API, and redeploy observability, for example with
`DEPLOY_COMPONENTS=observability ./scripts/cd/deploy-local.sh`.
`LANGFUSE_ENABLED` is not a profile setting: the deployment profile rejects it.
Setting `LANGFUSE_ENABLED=0` in a deployment command's environment disables export
for that run only. Keep the Helm release, Secrets and volumes for historical
inspection.

## Validation commands

```bash
helm template langfuse langfuse --repo https://langfuse.github.io/langfuse-k8s --version 2.1.0 \
  -n battlefield -f infra/deploy/langfuse/values.yaml \
  --api-versions clickhouse.com/v1alpha1/ClickHouseCluster \
  --api-versions clickhouse.com/v1alpha1/KeeperCluster >/dev/null

docker run --rm --network none -e LANGFUSE_AUTHORIZATION='Basic dGVzdDp0ZXN0' \
  -v "$PWD/infra/deploy/langfuse/collector-config.yaml:/etc/otel/config.yaml:ro" \
  otel/opentelemetry-collector-contrib:0.135.0 validate --config=/etc/otel/config.yaml
```

These checks validate chart rendering and the actual collector configuration.
They do not prove live ingestion; the short-battle check above is required after
installation. Installation follows the [official Helm deployment](https://langfuse.com/self-hosting/deployment/kubernetes-helm),
[headless initialization](https://langfuse.com/self-hosting/administration/headless-initialization),
and [OTLP attribute mapping](https://langfuse.com/integrations/native/opentelemetry).

The bundled values explicitly configure restricted Pod Security settings for
Langfuse web/worker and SeaweedFS: non-root numeric users, dropped capabilities,
no privilege escalation and RuntimeDefault seccomp. PostgreSQL inherits its
chart's non-root container settings and adds pod-level RuntimeDefault seccomp.
Volume fsGroup settings configure datastore access. Existing SeaweedFS volumes
created by the original root-based chart require a one-time ownership migration
to UID/GID 1000 before switching to non-root. Kind hostPath storage does not
recursively repair existing file ownership through fsGroup. Preserve the volume
and all files; change ownership only on the verified SeaweedFS PVC backing path. The namespace policy
is not relaxed. On the first installation, web pods may restart while PostgreSQL
and ClickHouse initialize; use pod readiness and Helm release status to distinguish
this from a failed rollout.

The SeaweedFS bucket-creation hook uses `s3.filer` security contexts even with
all-in-one storage; those are explicitly hardened too. Validate rendered
workloads and hook Jobs with `scripts/test/test-langfuse-security.py` (PyYAML).
