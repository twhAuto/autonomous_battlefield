#!/usr/bin/env bash
# Exercises environment registration, override rejection, cluster identity, and dev
# bootstrap using isolated command doubles.

set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
node --test "${project_dir}/scripts/test/dev-env.test.cjs"
python3 - "${project_dir}" <<'PY'
import os, pathlib, re, shutil, subprocess, sys, tempfile
root = pathlib.Path(sys.argv[1])
with tempfile.TemporaryDirectory(prefix='battlefield-environments-') as tmp:
    tmp = pathlib.Path(tmp)
    bins = tmp/'bin'; bins.mkdir()
    log = tmp/'commands'
    def executable(name, text):
        f = bins/name; f.write_text('#!/usr/bin/env bash\nset -eu\n'+text); f.chmod(0o755)
    executable('kubectl', '''echo "kubectl $*" >>"$TEST_LOG"
if [[ "$*" == *"get namespace kube-system"* ]]; then printf '%s' "${TEST_UID:-cluster-dev}"; fi
''')
    executable('docker', '''echo "docker $*" >>"$TEST_LOG"
case "$1" in
 info) exit "${TEST_DOCKER_STATUS:-0}" ;;
 context) echo unix:///var/run/docker.sock ;;
 ps) [[ "${TEST_STATE:-healthy}" == absent ]] || echo battlefield-control-plane ;;
 inspect) [[ "${TEST_STATE:-healthy}" == stopped ]] && echo false || echo true ;;
esac
''')
    executable('kind', '''echo "kind $*" >>"$TEST_LOG"
if [[ "$1 $2" == 'get kubeconfig' ]]; then echo fixture; fi
''')
    config = tmp/'deployment.env'; kube = tmp/'kubeconfig'; kube.touch()
    def profile(env='dev', **overrides):
        fields = dict(BF_ENV=env, BF_CONTEXT='battlefield-'+env, BF_CLUSTER='battlefield', BF_UID='cluster-dev',
                      BF_DOCKER_HOST='unix:///var/run/docker.sock', BF_URL='http://battlefield.localhost:8888',
                      BF_KUBECONFIG=str(kube), BF_DISPOSABLE='0')
        fields.update(overrides)
        config.write_text(''.join(f'{k}={v}\n' for k,v in fields.items()))
    clean = {k:v for k,v in os.environ.items() if not k.startswith(('BF_', 'BATTLEFIELD_', 'DEPLOY_', 'KUBERNETES_', 'KIND_', 'DOCKER_', 'SMOKE_TEST_')) and k not in ['KUBECONFIG','IMAGE_SOURCE','ENFORCE_SUPPLY_CHAIN_POLICY','TARGET_URL','ZAP_TARGET_URL']}
    clean.update(PATH=str(bins)+':'+os.environ['PATH'], BATTLEFIELD_ENV_FILE=str(config), TEST_LOG=str(log))
    helper = root/'scripts/lib/deployment-environment.sh'
    check = ['bash','-c', 'source "$1"; battlefield_env_preflight', '_', str(helper)]
    # Run production wrappers against a fixture whose lock resolves only this temp directory.
    fixture=tmp/'project'
    shutil.copytree(root/'scripts/cd',fixture/'scripts/cd')
    shutil.copytree(root/'scripts/lib',fixture/'scripts/lib')
    (fixture/'scripts/infra').mkdir()
    (fixture/'scripts/infra/operation-lock.py').write_text(
        'import pathlib,runpy\npathlib.Path.home=lambda:pathlib.Path('+repr(str(tmp))+')\n'
        'runpy.run_path('+repr(str(root/'scripts/infra/operation-lock.py'))+',run_name="__main__")\n')
    count=0
    def run(args=check, ok=True, no_mutation=False, error=None, **env):
        global count
        log.write_text('')
        result=subprocess.run(args, env=clean|env, capture_output=True, text=True)
        assert (result.returncode == 0) == ok, (args,result.stdout,result.stderr)
        if error is not None:
            assert error in result.stderr, result.stderr
        if no_mutation:
            assert not any(x in log.read_text() for x in [' apply ', ' delete ', ' set image ', ' start ', ' create cluster ', ' build ']), log.read_text()
        count+=1
        return log.read_text()
    profile(); run()
    for override in [dict(DEPLOY_ENV='invalid'),dict(KUBERNETES_CONTEXT='battlefield-production'),dict(KIND_CLUSTER_NAME='other'),
                     dict(KUBERNETES_NAMESPACE='other'),dict(DOCKER_HOST='tcp://remote:2375'),dict(TEST_UID='wrong'),
                     dict(IMAGE_SOURCE='registry'),dict(BATTLEFIELD_AUTH_SECRET_FILE='/production/auth'),dict(TARGET_URL='http://production')]:
        run(ok=False,no_mutation=True,**override)
    profile(BF_DOCKER_HOST='tcp://remote:2375');run(ok=False,no_mutation=True)
    profile();run(ok=False,no_mutation=True,BATTLEFIELD_ENV_FILE=str(tmp/'missing'))
    profile('production');run(DEPLOY_ENV='production')
    run(ok=False,no_mutation=True,DEPLOY_ENV='production',IMAGE_SOURCE='local')
    run(ok=False,no_mutation=True,DEPLOY_ENV='production',ENFORCE_SUPPLY_CHAIN_POLICY='0')
    run(['bash',str(root/'scripts/cd/deploy-local.sh')],ok=False,no_mutation=True,DEPLOY_ENV='production')
    run(['bash',str(root/'scripts/ops/run-kind-failure-drills.sh')],ok=False,no_mutation=True,DEPLOY_ENV='production',RUN_DESTRUCTIVE_KIND_DRILLS='I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS')
    run(['bash',str(fixture/'scripts/cd/rollback.sh'),'a'*40,'b'*40],ok=False,no_mutation=True,DEPLOY_ENV='production',error='requires both exact image digests')
    run(['bash',str(fixture/'scripts/cd/rollback.sh'),'ghcr.io/test/backend@sha256:'+'a'*64,'ghcr.io/test/frontend@sha256:'+'b'*64],
        ok=False,no_mutation=True,DEPLOY_ENV='production',error='has not been attested as Access-compatible',GHCR_USER='test',GHCR_TOKEN='dummy',COSIGN_PUBLIC_KEY_FILE=str(kube),VERIFY_ARTIFACT_DIR=str(tmp/'verification'))
    profile();run(['bash',str(root/'scripts/cd/deploy-local.sh')],ok=False,no_mutation=True,TEST_UID='wrong')
    run(['bash','-c','source "$1"; battlefield_env_load && battlefield_env_verify_docker','_',str(helper)],ok=False,no_mutation=True,TEST_DOCKER_STATUS='1')
    # Exercise real bootstrap with mocked external commands; profile already exists.
    bootstrap=['bash',str(root/'scripts/cd/create-kind-cluster.sh')]
    commands=run(bootstrap);assert 'docker start' not in commands and 'create cluster' not in commands
    commands=run(bootstrap,TEST_STATE='stopped');assert 'docker start battlefield-control-plane' in commands and 'create cluster' not in commands
    run(bootstrap,ok=False,no_mutation=True,TEST_STATE='absent')
    run(bootstrap+['--recreate'],ok=False,no_mutation=True)
    run(bootstrap,ok=False,no_mutation=True,DEPLOY_ENV='production')
    # First bootstrap creates only when no profile or cluster exists. Stub registration,
    # whose real identity verification is exercised separately below.
    setup=fixture/'scripts/cd/setup-environment.sh'
    setup.write_text('#!/usr/bin/env bash\ncp "$TEST_PROFILE" "$BATTLEFIELD_ENV_FILE"\n');setup.chmod(0o755)
    commands=run(['bash',str(fixture/'scripts/cd/create-kind-cluster.sh')],TEST_STATE='absent',BATTLEFIELD_ENV_FILE=str(tmp/'new.env'),TEST_PROFILE=str(config))
    assert 'kind create cluster --name battlefield' in commands
    # Setup refuses re-registration; it must never silently replace the pinned UID.
    run(['bash',str(root/'scripts/cd/setup-environment.sh'),'dev'],ok=False,no_mutation=True,BATTLEFIELD_CONFIG_DIR=str(tmp))
    # Provisioning must produce all required keyless provider fields and preserve files on rerun.
    profile()
    provision=['bash',str(fixture/'scripts/cd/prepare-dev-secrets.sh')]
    run(provision)
    ai=dict(line.split('=',1) for line in (fixture/'.env').read_text().splitlines())
    assert all(key in ai for key in ['OPENAI_API_KEY','OPENAI_MODEL','GEMINI_API_KEY','GEMINI_MODEL','BATTLEFIELD_MCP_CAPABILITY_SECRET'])
    assert ai['OPENAI_PROFILE_AVAILABLE'] == 'false' and ai['GEMINI_PROFILE_AVAILABLE'] == 'false'
    before=(fixture/'.env').read_bytes()
    run(provision)
    assert (fixture/'.env').read_bytes() == before
    library=root/'scripts/cd/lib/kubernetes-deployment.sh'
    # Missing production configuration must fail, never fall back to live Secrets.
    missing=['bash','-ec', r'''source "$1"
project_dir="$3"; DEPLOY_ENV=production; namespace=battlefield; ai_secret_file=/missing
cluster_deploy_kube() { exit 90; }
cluster_deploy_prepare_ai_secret "$2"
''','_',str(library),str(tmp/'retained.env'),str(root)]
    run(missing,ok=False,no_mutation=True)
    rollback=['bash','-ec', r'''source "$1"
DEPLOY_ENV=production; current_backend_image=a; current_engine_image=b
current_agent_image=a; current_mcp_image=a
cluster_deploy_rollback_application
''','_',str(library)]
    run(rollback,ok=False,no_mutation=True)
    # Main-only nested delivery and stage-scoped credential regression checks.
    pipeline=(root/'Jenkinsfile').read_text()
    main=pipeline.index("stage('Main delivery')")
    rollout=pipeline.index("stage('Production rollout')")
    assert main < rollout < pipeline.index("credentials('battlefield-production-kubeconfig')")
    assert "branch 'main'" in pipeline[main:rollout]
    assert 'battlefield-production-kubeconfig' not in pipeline[:rollout]
    # MCP readiness pings PostgreSQL; the probe must outlast that ping deadline.
    mcp_probe=re.search(r'readinessProbe: \{httpGet: \{path: /readyz, port: http\}.*timeoutSeconds: (\d+)\}',
        (root/'infra/deploy/base/application/mcp-deployment.yaml').read_text())
    ping_deadline=re.search(r'readinessHandler\(db, (\d+)\*time\.Second\)', (root/'backend/cmd/mcp/main.go').read_text())
    assert mcp_probe and ping_deadline and int(mcp_probe.group(1)) > int(ping_deadline.group(1)), 'MCP readiness probe must outlast its database ping'
    # A fresh forwarded replica has no HTTP series, even after Gateway traffic.
    smoke=(root/'scripts/cd/smoke-test.sh').read_text()
    metrics_check=smoke[smoke.index('# Gateway traffic may reach'):smoke.index('\ncurl --fail --silent --max-time 2 \\\n  --noproxy', smoke.index('# Gateway traffic may reach'))]
    harness=r'''
set -Eeuo pipefail
runtime_dir="$1"; api_port=18081
curl() {
  local output="" url="${!#}"
  while (($#)); do
    if [[ "$1" == --output ]]; then output="$2"; shift; fi
    shift
  done
  case "$url" in
    http://127.0.0.1:18081/v1/auth/session)
      touch "$runtime_dir/probed"
      printf '%s' "$SESSION_STATUS" ;;
    http://127.0.0.1:18081/metrics)
      test -f "$runtime_dir/probed"
      if [[ -f "$runtime_dir/scraped" && "$MISSING_METRIC" == 0 ]]; then
        echo 'battlefield_http_server_requests_total{code="401"} 1' >"$output"
      else
        echo 'go_goroutines 4' >"$output"
      fi
      touch "$runtime_dir/scraped" ;;
    *) return 1 ;;
  esac
}
sleep() { :; }
'''
    for status, missing, expected in [('200','0',0), ('401','0',0), ('503','0',1), ('401','1',1)]:
        with tempfile.TemporaryDirectory(dir=tmp) as case:
            result=subprocess.run(['bash','-c',harness+metrics_check,'_',case],
                env={**os.environ,'SESSION_STATUS':status,'MISSING_METRIC':missing},
                capture_output=True,text=True)
            assert result.returncode == expected, result.stderr
            if expected == 0:
                assert pathlib.Path(case,'scraped').exists()
            elif missing == '1':
                assert 'API metrics did not contain' in result.stderr
            else:
                assert 'session probe failed with HTTP 503' in result.stderr
    print(f'{count} deployment environment and bootstrap scenarios plus 4 metrics smoke scenarios passed.')
PY
