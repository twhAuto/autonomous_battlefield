# Load tests and failure drills

Run these procedures on demand against a separately registered disposable dev Kind
cluster. Production is forbidden for the failure drills. Record the date, exact
revision, environment, load, recovery timings and artifact locations in the
relevant Notion task or evidence page. Measurements from one host do not establish
current production capacity. The [observability architecture](../architecture/observability.md#proposed-slis-and-slos)
defines the proposed objectives; use the [operations guide](observability.md)
to inspect metrics while testing.

## Run load tests and failure drills

```bash
# Representative load: 20 viewers for 10 minutes against two scripted battles.
# Set the load explicitly; run-k6.sh otherwise applies its 3-viewer, 30 s smoke defaults.
ADMIN_USERNAME=... ADMIN_PASSWORD=... DEPLOY_ENV=dev K6_VUS=20 K6_DURATION=10m \
  ./scripts/perf/run-k6.sh tests/performance/k6-representative.js

# Saturation: K6_STAGES replaces the fixed viewer count and duration.
ADMIN_USERNAME=... ADMIN_PASSWORD=... DEPLOY_ENV=dev \
  K6_STAGES=30s:100,60s:100,30s:300,60s:300,30s:600,60s:600,20s:0 \
  ./scripts/perf/run-k6.sh tests/performance/k6-representative.js

# Failure drills: requires BF_DISPOSABLE=1 in the dev profile; restore it afterwards.
DEPLOY_ENV=dev RUN_DESTRUCTIVE_KIND_DRILLS=I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS \
  ./scripts/ops/run-kind-failure-drills.sh
```

For an AI outage, set `runtime_policy.ai_enabled` to `false` in the dev
database, run a battle with one model controller, and restore it to `true`.
New model decisions are stored as `failed` with `ai_disabled_or_budget` and are
never claimed, so no provider is called even for an API profile.

`tests/performance/k6-representative.js` queues the admitted number of
scripted battles (two per person) and replays the battle page: each viewer
polls status, the next event page, and decision history once a second, and
browses history once the battle settles. It records two battle metrics from
server timestamps: `battle_queue_wait_seconds` (queued to started) and
`battle_pace_ratio` (run time divided by ticks; 1.0 is one tick per second).
`battles_completed` must be 100%, so one failed or unfinished battle fails the
run even when thousands of polling checks pass.
Run the drills while the representative load runs, so the drill shows its
effect on viewers and on a live battle.

The drill script deletes each workload's pods and fails unless replacement pods
with new UIDs become Ready, so a selector that matches nothing cannot report a
passed drill. Its `artifacts/failure-drills/summary.json` records the recovery
seconds for each drill.

The MCP outage drill restores the deployment to exactly one replica; it does not
capture or restore a different starting count. Use a one-replica MCP in the
registered disposable dev environment, and verify its replica count after the drill.
