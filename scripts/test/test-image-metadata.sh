#!/usr/bin/env bash
# Checks image source labels, registry repository selection, and admission-policy inputs
# using isolated fixtures.

set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
python3 - "${project_dir}" <<'PY'
import hashlib, json, os, pathlib, re, shutil, subprocess, sys, tempfile
root = pathlib.Path(sys.argv[1])
with tempfile.TemporaryDirectory(prefix='battlefield-images-') as tmp:
    tmp = pathlib.Path(tmp)
    project = tmp/'project with spaces'
    for name in ['scripts/lib/image-metadata.sh', 'scripts/lib/deployment-components.sh',
                 'scripts/ci/publish-images.sh', 'scripts/ci/build-images.sh', 'scripts/ci/scan-published-images.sh', 'scripts/ci/tool-versions.env',
                 'scripts/cd/apply-kyverno-policy.sh', 'infra/deploy/overlays/jenkins/kyverno/verify-images.yaml.template']:
        target = project/name; target.parent.mkdir(parents=True, exist_ok=True); shutil.copy(root/name, target)
    # The fixture pins the synthetic Kyverno manifest that the fake curl serves.
    kyverno_manifest = 'fixture kyverno install manifest\n'
    versions = project/'scripts/ci/tool-versions.env'
    versions.write_text(re.sub(r'^KYVERNO_INSTALL_SHA256=.*$', 'KYVERNO_INSTALL_SHA256=' + hashlib.sha256(kyverno_manifest.encode()).hexdigest(),
                               versions.read_text(), count=1, flags=re.M))
    (project/'scripts/lib/deployment-environment.sh').write_text('battlefield_env_preflight() { :; }\n')
    (project/'scripts/ci/verify-frontend-image.py').write_text('''import json, os, sys
with open(os.environ['TEST_COMMAND_LOG'], 'a') as f:
    f.write(json.dumps(['verify-frontend', *sys.argv[1:]]) + '\\n')
raise SystemExit(int(os.environ.get('TEST_FRONTEND_PROBE_EXIT', '0')))
''')
    (project/'VERSION').write_text('0.1.0\n')
    (project/'.gitignore').write_text('artifacts/\n')
    def git(*args):
        return subprocess.check_output(['git', '-C', str(project), *args], text=True).strip()
    git('init', '--quiet'); git('config', 'user.name', 'Test'); git('config', 'user.email', 'test@example.invalid')
    git('add', '.'); git('commit', '--quiet', '-m', 'fixture')
    git('remote', 'add', 'origin', 'git@github.com:fork-owner/fork.git')
    bins = tmp/'bin'; bins.mkdir()
    docker = bins/'docker'
    docker.write_text('''#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:]
with open(os.environ['TEST_COMMAND_LOG'],'a') as f: f.write(json.dumps(args)+'\\n')
if args[:2] == ['image', 'inspect']: sys.exit(0 if os.environ.get('TEST_PUBLISHED_CACHED') == '1' else 1)
if args[0] == 'pull': sys.exit(int(os.environ.get('TEST_PULL_EXIT', '0')))
if args[0] == 'run': sys.exit(int(os.environ.get('TEST_SCAN_EXIT', '0')))
if '--metadata-file' in args:
 pathlib.Path(args[args.index('--metadata-file')+1]).write_text(json.dumps({'containerimage.digest':'sha256:'+'a'*64}))
''')
    kube = bins/'kubectl'
    kube.write_text('''#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:]
with open(os.environ['TEST_COMMAND_LOG'],'a') as f: f.write(json.dumps(args)+'\\n')
if '-f' in args:
 path=args[args.index('-f')+1]
 if path == '-': sys.stdin.read()
 elif pathlib.Path(path).is_file(): pathlib.Path(os.environ['TEST_POLICY']).write_text(pathlib.Path(path).read_text())
''')
    curl = bins/'curl'
    curl.write_text('''#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:]
with open(os.environ['TEST_COMMAND_LOG'],'a') as f: f.write(json.dumps(['curl',*args])+'\\n')
pathlib.Path(args[args.index('--output')+1]).write_text(os.environ['TEST_KYVERNO_MANIFEST'])
''')
    docker.chmod(0o755); kube.chmod(0o755); curl.chmod(0o755)
    config = tmp/'docker config'; config.mkdir(); (config/'config.json').write_text('{}')
    key = tmp/'key.pem'; key.write_text('-----BEGIN PUBLIC KEY-----\nfixture\n-----END PUBLIC KEY-----\n')
    log = tmp/'commands'; policy = tmp/'policy.yaml'
    env = {k:v for k,v in os.environ.items() if not k.startswith(('PUBLISHED_', 'POLICY_', 'REGISTRY_', 'SOURCE_', 'IMAGE_', 'BUILD_', 'CI_BUILD_', 'GITHUB_', 'BACKEND_', 'FRONTEND_')) and k not in ['VERSION_FILE','ALLOW_DIRTY','DEPLOY_COMPONENTS','BUILDX_BUILDER','BUILD_NUMBER']}
    env.update(PATH=str(bins)+':'+os.environ['PATH'], TEST_COMMAND_LOG=str(log), TEST_POLICY=str(policy),
               DOCKER_CONFIG=str(config), COSIGN_PUBLIC_KEY_FILE=str(key), KUBERNETES_CONTEXT='fixture',
               TEST_KYVERNO_MANIFEST=kyverno_manifest)
    def run(script, ok=True, **overrides):
        log.write_text('')
        result = subprocess.run(['bash', str(project/script)], env=env|overrides, capture_output=True, text=True)
        assert (result.returncode == 0) == ok, (script, result.stderr)
        return [json.loads(line) for line in log.read_text().splitlines()]
    def helper(command, **overrides):
        return subprocess.run(['bash','-ec','source "$1"; '+command,'_',str(project/'scripts/lib/image-metadata.sh'),str(project)],env=env|overrides,capture_output=True,text=True)
    for remote in ['git@github.com:fork-owner/fork.git','https://github.com/fork-owner/fork.git','ssh://git@github.com/fork-owner/fork.git']:
        git('remote','set-url','origin',remote)
        result=helper('image_source_url "$2"')
        assert result.returncode == 0 and result.stdout.strip() == 'https://github.com/fork-owner/fork'
    for bad in ['https://user:secret@example.test/repo','https://example.test/repo?token=secret','/tmp/repo']:
        result=helper('image_source_url "$2"', SOURCE_URL=bad)
        assert result.returncode != 0 and 'secret' not in result.stderr
    for script in ['scripts/ci/build-images.sh','scripts/ci/publish-images.sh']:
        for overrides, source in [({}, 'https://github.com/fork-owner/fork'), ({'SOURCE_URL':'https://git.example.test/team/repo'}, 'https://git.example.test/team/repo')]:
            calls=run(script, **overrides)
            builds=[args for args in calls if '--label' in args]
            assert len(builds)==2 and all('org.opencontainers.image.source='+source in args for args in builds)
            if script == 'scripts/ci/build-images.sh':
                assert ['verify-frontend', 'autonomous-battlefield-frontend:'+git('rev-parse', 'HEAD')] in calls
                assert all(args[args.index('--builder')+1] == 'default' for args in builds)
    run('scripts/ci/build-images.sh', ok=False, TEST_FRONTEND_PROBE_EXIT='23')
    run('scripts/ci/build-images.sh', BUILD_COMPONENTS='backend', TEST_FRONTEND_PROBE_EXIT='23')
    image_file = tmp/'published.env'
    backend_ref = 'ghcr.io/test/backend@sha256:' + 'a'*64
    frontend_ref = 'ghcr.io/test/frontend@sha256:' + 'b'*64
    image_file.write_text(f'BACKEND_IMAGE_REF={backend_ref}\nFRONTEND_IMAGE_REF={frontend_ref}\n')
    scan_script = 'scripts/ci/scan-published-images.sh'
    calls = run(scan_script, BUILD_IMAGES_FILE=str(image_file))
    assert calls[-3:] == [['pull', frontend_ref], ['verify-frontend', frontend_ref], ['image', 'rm', frontend_ref]]
    cached_calls = run(scan_script, BUILD_IMAGES_FILE=str(image_file), TEST_PUBLISHED_CACHED='1')
    assert cached_calls[-2:] == [['pull', frontend_ref], ['verify-frontend', frontend_ref]]
    assert not any(call[:2] == ['image', 'rm'] for call in cached_calls)
    for failure in ['TEST_PULL_EXIT', 'TEST_SCAN_EXIT', 'TEST_FRONTEND_PROBE_EXIT']:
        calls = run(scan_script, ok=False, BUILD_IMAGES_FILE=str(image_file), **{failure: '23'})
        if failure != 'TEST_FRONTEND_PROBE_EXIT':
            assert not any(call[0] == 'verify-frontend' for call in calls)
            assert not any(call[:2] == ['image', 'rm'] for call in calls)
        else:
            assert calls[-1] == ['image', 'rm', frontend_ref]
    image_file.write_text(f'BACKEND_IMAGE_REF={backend_ref}\n')
    calls = run(scan_script, BUILD_IMAGES_FILE=str(image_file), TEST_FRONTEND_PROBE_EXIT='23')
    assert not any(call[0] in ['pull', 'verify-frontend'] for call in calls)
    image_file.write_text('')
    assert not run(scan_script, BUILD_IMAGES_FILE=str(image_file))
    for overrides, backend, frontend in [
        ({}, 'ghcr.io/twh09/autonomous-battlefield-backend', 'ghcr.io/twh09/autonomous-battlefield-frontend'),
        ({'REGISTRY_NAMESPACE':'ghcr.io/fork'}, 'ghcr.io/fork/autonomous-battlefield-backend', 'ghcr.io/fork/autonomous-battlefield-frontend'),
        ({'PUBLISHED_BACKEND_IMAGE':'ghcr.io/custom/api','PUBLISHED_FRONTEND_IMAGE':'ghcr.io/custom/web'}, 'ghcr.io/custom/api','ghcr.io/custom/web')]:
        calls=run('scripts/ci/publish-images.sh', **overrides)
        tags=[args[args.index('--tag')+1].rsplit(':',1)[0] for args in calls if '--tag' in args]
        assert tags == [backend,frontend]
        run('scripts/cd/apply-kyverno-policy.sh', **overrides)
        text=policy.read_text()
        assert re.findall(r'^            - "(.*)"$',text,re.M)==[backend+':*',backend+'@sha256:*',frontend+':*',frontend+'@sha256:*']
        assert text.count('-----BEGIN PUBLIC KEY-----') == 4 and '__' not in text
    run('scripts/cd/apply-kyverno-policy.sh', REGISTRY_NAMESPACE='ghcr.io/new',
        POLICY_BACKEND_IMAGE_REF='ghcr.io/old/backend@sha256:'+'b'*64,
        POLICY_FRONTEND_IMAGE_REF='ghcr.io/new/frontend@sha256:'+'a'*64)
    assert 'ghcr.io/old/backend@sha256:*' in policy.read_text() and 'ghcr.io/new/frontend:*' in policy.read_text()
    for overrides in [{'PUBLISHED_BACKEND_IMAGE':'ghcr.io/*'}, {'PUBLISHED_FRONTEND_IMAGE':'ghcr.io/team/image:latest'},
                      {'REGISTRY_NAMESPACE':'ghcr.io/team\nmalicious'}, {'POLICY_BACKEND_IMAGE_REF':'ghcr.io/team/image:latest'}]:
        assert not run('scripts/cd/apply-kyverno-policy.sh', ok=False, **overrides), 'invalid input reached kubectl'
    calls = run('scripts/cd/apply-kyverno-policy.sh')
    downloads = [call for call in calls if call[0] == 'curl']
    assert len(downloads) == 1 and '=https' in downloads[0], downloads
    assert downloads[0][-1].startswith('https://github.com/kyverno/kyverno/releases/download/v'), downloads
    installed = downloads[0][downloads[0].index('--output')+1]
    assert ['--context', 'fixture', 'apply', '--server-side', '--force-conflicts', '--field-manager=battlefield-jenkins', '-f', installed] in calls
    calls = run('scripts/cd/apply-kyverno-policy.sh', ok=False, TEST_KYVERNO_MANIFEST='tampered manifest\n')
    assert [call for call in calls if call[0] != 'curl'] == [], 'a mismatched Kyverno manifest reached kubectl'
    assert not any(call[0] == 'curl' for call in run('scripts/cd/apply-kyverno-policy.sh', CLUSTER_DRIVER='k3s'))
    print('Image metadata, registry overrides, frontend runtime gate propagation, policy rejection, and Kyverno manifest checks passed.')
PY
