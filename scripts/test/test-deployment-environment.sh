#!/usr/bin/env bash
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
python3 - "${project_dir}" <<'PY'
import os, pathlib, shutil, subprocess, sys, tempfile
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
    config = tmp/'environment.env'; kube = tmp/'kubeconfig'; kube.touch()
    def profile(env='dev', **overrides):
        fields = dict(BF_ENV=env, BF_CONTEXT='battlefield-'+env, BF_CLUSTER='battlefield', BF_UID='cluster-dev',
                      BF_DOCKER_HOST='unix:///var/run/docker.sock', BF_URL='http://battlefield.localhost:8888',
                      BF_KUBECONFIG=str(kube), BF_DATABASE_FILE=str(tmp/'database.env'), BF_AI_FILE=str(tmp/'ai.env'),
                      BF_AUTH_FILE=str(tmp/'auth.env'), BF_DISPOSABLE='0')
        fields.update(overrides)
        import shlex
        config.write_text(''.join(f'{k}={shlex.quote(v)}\n' for k,v in fields.items()))
    clean = {k:v for k,v in os.environ.items() if not k.startswith(('BF_', 'BATTLEFIELD_', 'DEPLOY_', 'KUBERNETES_', 'KIND_', 'DOCKER_', 'SMOKE_TEST_')) and k not in ['KUBECONFIG','IMAGE_SOURCE','ENFORCE_SUPPLY_CHAIN_POLICY','TARGET_URL','ZAP_TARGET_URL']}
    clean.update(PATH=str(bins)+':'+os.environ['PATH'], BATTLEFIELD_ENV_FILE=str(config), TEST_LOG=str(log))
    helper = root/'scripts/lib/deployment-environment.sh'
    check = ['bash','-c', 'source "$1"; battlefield_env_preflight', '_', str(helper)]
    count=0
    def run(args=check, ok=True, no_mutation=False, **env):
        global count
        log.write_text('')
        result=subprocess.run(args, env=clean|env, capture_output=True, text=True)
        assert (result.returncode == 0) == ok, (args,result.stdout,result.stderr)
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
    run(['bash',str(root/'deploy-local.sh')],ok=False,no_mutation=True,DEPLOY_ENV='production')
    run(['bash',str(root/'scripts/ops/run-kind-failure-drills.sh')],ok=False,no_mutation=True,DEPLOY_ENV='production',RUN_DESTRUCTIVE_KIND_DRILLS='I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS')
    run(['bash',str(root/'scripts/cd/rollback-kind.sh'),'a'*40,'b'*40],ok=False,no_mutation=True,DEPLOY_ENV='production')
    run(['bash',str(root/'scripts/cd/rollback-kind.sh'),'ghcr.io/test/backend@sha256:'+'a'*64,'ghcr.io/test/frontend@sha256:'+'b'*64],
        ok=False,no_mutation=True,DEPLOY_ENV='production',GHCR_USER='test',GHCR_TOKEN='dummy',COSIGN_PUBLIC_KEY_FILE=str(kube),VERIFY_ARTIFACT_DIR=str(tmp/'verification'))
    profile();run(['bash',str(root/'deploy-local.sh')],ok=False,no_mutation=True,TEST_UID='wrong')
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
    fixture=tmp/'project';shutil.copytree(root/'scripts/cd',fixture/'scripts/cd');shutil.copytree(root/'scripts/lib',fixture/'scripts/lib')
    setup=fixture/'scripts/cd/setup-environment.sh'
    setup.write_text('#!/usr/bin/env bash\ncp "$TEST_PROFILE" "$BATTLEFIELD_ENV_FILE"\n');setup.chmod(0o755)
    commands=run(['bash',str(fixture/'scripts/cd/create-kind-cluster.sh')],TEST_STATE='absent',BATTLEFIELD_ENV_FILE=str(tmp/'new.env'),TEST_PROFILE=str(config))
    assert 'kind create cluster --name battlefield' in commands
    # Setup refuses re-registration; it must never silently replace the pinned UID.
    run(['bash',str(root/'scripts/cd/setup-environment.sh'),'dev'],ok=False,no_mutation=True,BATTLEFIELD_CONFIG_DIR=str(tmp))
    # Provisioning must produce all required keyless provider fields and preserve files on rerun.
    profile()
    provision=['bash',str(root/'scripts/cd/prepare-dev-secrets.sh')]
    run(provision)
    ai=dict(line.split('=',1) for line in (tmp/'ai.env').read_text().splitlines())
    assert all(key in ai for key in ['OPENAI_API_KEY','OPENAI_MODEL','GEMINI_API_KEY','GEMINI_MODEL','BATTLEFIELD_MCP_CAPABILITY_SECRET'])
    assert ai['OPENAI_PROFILE_AVAILABLE'] == 'false' and ai['GEMINI_PROFILE_AVAILABLE'] == 'false'
    before={name:(tmp/name).read_bytes() for name in ['database.env','auth.env','ai.env']}
    run(provision)
    assert all((tmp/name).read_bytes() == value for name,value in before.items())
    library=root/'scripts/cd/lib/kind-deployment.sh'
    # Production must retain all existing Secret fields, including provider credentials.
    preserve=['bash','-ec', r'''source "$1"
DEPLOY_ENV=production; namespace=battlefield; ai_secret_file=/missing
kind_deploy_kube() { printf '%s' '{"data":{"BATTLEFIELD_MCP_CAPABILITY_SECRET":"ZHVtbXk=","OPENAI_API_KEY":"ZHVtbXkta2V5","OPENAI_PROFILE_AVAILABLE":"dHJ1ZQ=="}}'; }
kind_deploy_prepare_ai_secret "$2"
''','_',str(library),str(tmp/'retained.env')]
    run(preserve)
    assert 'OPENAI_API_KEY=dummy-key' in (tmp/'retained.env').read_text()
    assert 'OPENAI_PROFILE_AVAILABLE=true' in (tmp/'retained.env').read_text()
    failed=preserve.copy();failed[2]=failed[2].replace("printf '%s'", "return 1; printf '%s'")
    run(failed,ok=False)
    rollback=['bash','-ec', r'''source "$1"
DEPLOY_ENV=production; current_backend_image=a; current_engine_image=b
current_agent_image=a; current_mcp_image=a
kind_deploy_rollback_application
''','_',str(library)]
    run(rollback,ok=False,no_mutation=True)
    # Main-only nested delivery and stage-scoped credential regression checks.
    pipeline=(root/'Jenkinsfile').read_text()
    main=pipeline.index("stage('Main delivery')")
    rollout=pipeline.index("stage('Production rollout')")
    assert main < rollout < pipeline.index("credentials('battlefield-production-kubeconfig')")
    assert "branch 'main'" in pipeline[main:rollout]
    assert 'battlefield-production-kubeconfig' not in pipeline[:rollout]
    print(f'{count} deployment environment and bootstrap scenarios passed.')
PY
