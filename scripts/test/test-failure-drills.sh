#!/usr/bin/env bash
# Exercises the Kind failure drills with command doubles: each drill must replace real
# pods, and a selector that matches nothing or pods that never recover must fail.

set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
python3 - "${project_dir}" <<'PY'
import json, os, pathlib, shutil, subprocess, sys, tempfile
root = pathlib.Path(sys.argv[1])
with tempfile.TemporaryDirectory(prefix='battlefield-drills-') as tmp:
    tmp = pathlib.Path(tmp)
    bins = tmp/'bin'; bins.mkdir()
    state = tmp/'state'; log = tmp/'commands'
    def executable(path, text):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('#!/usr/bin/env bash\nset -eu\n'+text); path.chmod(0o755)
    # Pods report an "old" UID until their selector is deleted or scaled down.
    executable(bins/'kubectl', '''echo "kubectl $*" >>"$TEST_LOG"
selector=''; prev=''
for arg in "$@"; do [[ "$prev" == -l ]] && selector="${arg##*=}"; prev="$arg"; done
case "$*" in
 *"get namespace kube-system"*) printf cluster-dev ;;
 *"get pods"*)
   [[ " ${TEST_EMPTY:-} " == *" ${selector} "* ]] && exit 0
   if [[ -e "$TEST_STATE/${selector}" && -z "${TEST_STUCK:-}" ]]; then printf "new-%s" "$selector"; else printf "old-%s" "$selector"; fi ;;
 *"delete pod"*) touch "$TEST_STATE/${selector}" ;;
 *"--replicas=0"*) touch "$TEST_STATE/mcp" ;;
esac
''')
    executable(bins/'docker', 'exit 0\n')
    executable(bins/'kind', 'echo fixture\n')
    project = tmp/'project'
    for part in ['scripts/ops', 'scripts/lib']:
        shutil.copytree(root/part, project/part)
    executable(project/'scripts/cd/smoke-test.sh', 'echo smoke >>"$TEST_LOG"\n')
    kube = tmp/'kubeconfig'; kube.touch()
    config = tmp/'deployment.env'
    config.write_text(''.join(f'{k}={v}\n' for k, v in dict(
        BF_ENV='dev', BF_CONTEXT='battlefield-dev', BF_CLUSTER='battlefield', BF_UID='cluster-dev',
        BF_DOCKER_HOST='unix:///var/run/docker.sock', BF_URL='http://battlefield.localhost:8888',
        BF_KUBECONFIG=str(kube), BF_DISPOSABLE='1').items()))
    clean = {k: v for k, v in os.environ.items() if not k.startswith(('BF_', 'BATTLEFIELD_', 'DEPLOY_', 'KUBERNETES_', 'KIND_', 'DOCKER_', 'TEST_', 'DRILL_', 'MCP_')) and k != 'KUBECONFIG'}
    clean.update(PATH=f'{bins}:{os.environ["PATH"]}', BATTLEFIELD_ENV_FILE=str(config), TEST_LOG=str(log),
                 TEST_STATE=str(state), DEPLOY_ENV='dev', MCP_OUTAGE_SECONDS='0', DRILL_RECOVERY_TIMEOUT_SECONDS='3',
                 RUN_DESTRUCTIVE_KIND_DRILLS='I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS',
                 REPORT_DIR=str(tmp/'report'))
    def run(ok, **env):
        shutil.rmtree(state, ignore_errors=True); state.mkdir(); log.write_text('')
        shutil.rmtree(tmp/'report', ignore_errors=True)
        result = subprocess.run(['bash', str(project/'scripts/ops/run-kind-failure-drills.sh')],
                                cwd=tmp, env=clean | env, capture_output=True, text=True)
        assert (result.returncode == 0) == ok, (env, result.stdout, result.stderr)
        return log.read_text(), result.stderr

    commands, _ = run(True)
    for expected in ['delete pod --wait=false -l app.kubernetes.io/component=engine',
                     'delete pod --wait=false -l app.kubernetes.io/component=database',
                     'scale deployment/battlefield-mcp --replicas=0', 'scale deployment/battlefield-mcp --replicas=1']:
        assert any(expected in line or set(expected.split()) <= set(line.split()) for line in commands.splitlines()), (expected, commands)
    assert commands.rstrip().endswith('smoke'), commands
    report = json.loads((tmp/'report/summary.json').read_text())
    assert report['schemaVersion'] == 'battlefield.failure-drill-report.v2'
    assert [d['name'] for d in report['drills']] == ['engine-restart', 'postgresql-restart', 'mcp-outage']
    assert all(isinstance(d['recoverySeconds'], int) for d in report['drills'])

    # A selector that matches nothing must fail instead of reporting a restart.
    commands, stderr = run(False, TEST_EMPTY='database')
    assert 'No pods match app.kubernetes.io/component=database' in stderr, stderr
    assert 'smoke' not in commands and not (tmp/'report/summary.json').exists()

    # Pods that are never replaced must fail within the recovery timeout.
    commands, stderr = run(False, TEST_STUCK='1')
    assert 'were not replaced and Ready' in stderr, stderr
    assert 'smoke' not in commands and not (tmp/'report/summary.json').exists()

    # The disposable-cluster guard still refuses a non-disposable profile.
    config.write_text(config.read_text().replace('BF_DISPOSABLE=1', 'BF_DISPOSABLE=0'))
    commands, _ = run(False)
    assert ' delete ' not in commands and ' scale ' not in commands, commands
print('failure drill tests passed')
PY
