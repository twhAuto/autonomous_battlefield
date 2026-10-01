#!/usr/bin/env python3
"""CI acceptance fixture; runs only inside its UUID-bound disposable Ubuntu VM."""
import argparse
import base64
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.request
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = Path.home() / 'ci-fixture'
EVIDENCE = Path.home() / 'evidence'
SECRET = 'Synthetic-CI-bootstrap-password-0123456789!'


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


fixture = load('foundation_fixture', ROOT / 'scripts/test/rehearse-server-guest.py')
runtime = load('ci_runtime', ROOT / 'scripts/infra/ci-runtime.py')


def run(*args, **kwargs):
    output = subprocess.run(args, capture_output=True, text=True, timeout=kwargs.pop('timeout', 120), **kwargs)
    if output.returncode:
        raise ValueError('CI fixture command failed: ' + args[0])
    return output.stdout.strip()


def server(action, *, success=True):
    command = [str(ROOT / 'scripts/infra/server.sh'), action, '--local', '--inventory', str(PRIVATE / 'inventory.json')]
    if action == 'ci-register':
        command += ['--ci-install', '--ci-admin', 'fixture-admin']
    else:
        command += ['--vault', str(PRIVATE / 'inputs.vault'), '--vault-password-file', str(PRIVATE / 'password')]
    result = subprocess.run(command, capture_output=True, text=True, timeout=2400)
    output = result.stdout + result.stderr
    for value in (SECRET, base64.b64encode(SECRET.encode()).decode()):
        if value in output:
            raise ValueError('Synthetic secret leaked; refusing to archive output')
    sequence = len(list(EVIDENCE.glob(action + '-*.log')))
    (EVIDENCE / (action + '-' + str(sequence) + '.log')).write_text(output)
    if (result.returncode == 0) != success:
        raise ValueError('Unexpected ' + action + ' result; inspect private guest evidence')
    return output


def prepare():
    PRIVATE.mkdir(mode=0o700, exist_ok=True)
    EVIDENCE.mkdir(mode=0o700, exist_ok=True)
    machine = Path('/etc/machine-id').read_text().strip()
    (PRIVATE / 'inventory.json').write_text(json.dumps({'schema': 1, 'host': '127.0.0.1', 'user': 'rehearsal',
        'identity_file': str(PRIVATE / 'unused-key'), 'machine_id': machine}))
    (PRIVATE / 'password').write_text(SECRET)
    key = PRIVATE / 'github.key'
    if not key.exists():
        run('openssl', 'genrsa', '-out', str(key), '2048')
    values = {}
    for name, kind in runtime.CREDENTIALS.items():
        entry = {'type': kind}
        if kind == 'githubApp':
            entry.update(appId='1', privateKey=key.read_text())
        elif kind == 'usernamePassword':
            entry.update(username='synthetic', password=SECRET)
        else:
            entry['value'] = SECRET
        values[name] = entry
    for name in runtime.FOLDER_CREDENTIALS:
        values[name] = {'type': 'file', 'value': '$ANSIBLE_VAULT;1.1;AES256\nfixture-only\n' if name == 'host-inputs' else SECRET}
    (PRIVATE / 'plain-inputs.json').write_text(json.dumps({'bootstrap_inputs': {
        'ci_admin_password': base64.b64encode((SECRET + '\n').encode()).decode(),
        'ci_sonar_admin_password': base64.b64encode(SECRET.encode()).decode(),
        'ci_database_password': base64.b64encode(SECRET.encode()).decode(),
        'ci_credentials': base64.b64encode(json.dumps(values).encode()).decode(),
    }}))
    binary = run(str(ROOT / 'scripts/infra/server.sh'), 'tools') + '/ansible-vault'
    run(binary, 'encrypt', '--output', str(PRIVATE / 'inputs.vault'), '--vault-password-file', str(PRIVATE / 'password'), str(PRIVATE / 'plain-inputs.json'))
    (PRIVATE / 'plain-inputs.json').unlink()
    for path in PRIVATE.iterdir():
        if path.is_file(): path.chmod(0o600)
    server('ci-register')
    fixture.assert_recap(server('ci-plan'), changed=0)
    if Path('/var/lib/battlefield-ci').exists():
        raise ValueError('CI plan mutated host state')


def client():
    token = run('sudo', 'cat', '/var/lib/jenkins/battlefield-bootstrap-api-token')
    return runtime.API(runtime.JENKINS, 'fixture-admin', token)


def snapshot():
    # Hash identity without serializing any secret value into evidence.
    script = '''import jenkins.model.Jenkins
import java.security.MessageDigest
def j=Jenkins.get()
def digest={v -> MessageDigest.getInstance('SHA-256').digest(v instanceof byte[] ? v : v.getBytes('UTF-8')).encodeHex().toString()}
def fingerprint={ c ->
    def value
    switch(c.class.simpleName) {
      case 'FileCredentialsImpl': value=c.secretBytes.plainData; break
      case 'StringCredentialsImpl': value=c.secret.plainText; break
      case 'UsernamePasswordCredentialsImpl': value=c.username+':'+c.password.plainText; break
      case 'GitHubAppCredentials': value=c.appID+':'+c.privateKey.plainText; break
      default: throw new IllegalStateException('Unknown fixture credential class')
    }
    [c.id,digest(value)]
}
def global=com.cloudbees.plugins.credentials.SystemCredentialsProvider.getInstance().credentials.collectEntries(fingerprint)
def property=j.getItem('battlefield-platform').properties.get(com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider.FolderCredentialsProperty)
def folder=property.store.getCredentials(com.cloudbees.plugins.credentials.domains.Domain.global()).collectEntries(fingerprint)
println groovy.json.JsonOutput.toJson([agent:digest(j.getNode('battlefield-local').toComputer().jnlpMac),
 users:hudson.model.User.getAll().collect{it.id}.sort(),jobs:j.items.collect{it.name}.sort(),
 credentials:global,platform_credentials:folder])
'''
    return json.loads(client().call('scriptText', {'script': script}, raw=True))


def probe_job():
    api = client()
    script = """node('battlefield-production') {
  deleteDir()
  writeFile file: 'hello.py', text: 'def greet(name):\\n    return "Hello " + name\\n'
  withCredentials([file(credentialsId: 'battlefield-cosign-key', variable: 'KEY_FILE')]) {
    sh 'set +x; test -s "$KEY_FILE"; go version; node --version; docker info >/dev/null; docker buildx version'
  }
  def scanner = tool 'SonarScanner'
  withSonarQubeEnv('battlefield-sonarqube') {
    withEnv(['PATH+SCANNER=' + scanner + '/bin']) {
      sh 'set +x; export SONAR_TOKEN="$SONAR_AUTH_TOKEN"; sonar-scanner -Dsonar.projectKey=ab127-ci-fixture -Dsonar.sources=. -Dsonar.host.url="$SONAR_HOST_URL" -Dsonar.qualitygate.wait=true'
    }
  }
  archiveArtifacts artifacts: '.scannerwork/report-task.txt'
}
"""
    job = ET.Element('flow-definition')
    ET.SubElement(job, 'description').text = 'Disposable CI service integration fixture'
    definition = ET.SubElement(job, 'definition', {'class': 'org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition'})
    ET.SubElement(definition, 'script').text = script
    ET.SubElement(definition, 'sandbox').text = 'true'
    name = 'ab127-fixture-verification'
    request = urllib.request.Request(runtime.JENKINS + 'createItem?name=' + name,
              data=ET.tostring(job), headers={'Authorization': api.authorization, 'Content-Type': 'application/xml'})
    if api.call('job/' + name + '/api/json?tree=name', missing=True) is None:
        with api.opener.open(request, timeout=20): pass
    number = api.call('job/' + name + '/api/json?tree=nextBuildNumber')['nextBuildNumber']
    api.call('job/' + name + '/build', {}, raw=True)
    for _ in range(180):
        status = api.call('job/' + name + '/' + str(number) + '/api/json?tree=result,building', missing=True)
        if status and not status.get('building'):
            console = api.call('job/' + name + '/lastBuild/consoleText', raw=True).decode()
            if SECRET in console:
                raise ValueError('Probe job disclosed a synthetic credential; refusing to archive')
            (EVIDENCE / 'verification-job.log').write_text(console)
            if status['result'] != 'SUCCESS':
                raise ValueError('Fixture verification pipeline failed; inspect private evidence')
            break
        time.sleep(3)
    else:
        raise ValueError('Fixture verification pipeline timed out')


def adoption():
    api = client()
    api.call('scriptText', {'script': """import jenkins.model.Jenkins
import hudson.model.FreeStyleProject
import hudson.tasks.Shell
def j=Jenkins.get()
def user=j.securityRealm.createAccount('unmanaged-owner','fixture-unmanaged-password')
user.save()
def job=j.createProject(FreeStyleProject,'unmanaged-history')
job.setAssignedLabel(j.getLabel('battlefield-production'))
job.buildersList.add(new Shell('true'))
job.save()
job.scheduleBuild2(0).get()
println 'created unmanaged fixture'
"""}, raw=True)
    before = snapshot()
    xml_before = api.call('job/autonomous-battlefield/config.xml', raw=True)
    # Stop, archive, rename and restore only this disposable guest's controller.
    run('sudo', 'systemctl', 'stop', 'jenkins')
    run('sudo', 'tar', '-cpf', str(PRIVATE / 'restored-controller.tar'), '-C', '/var/lib', 'jenkins', timeout=300)
    run('sudo', 'mv', '/var/lib/jenkins', '/var/lib/jenkins.before-restore-fixture')
    run('sudo', 'tar', '-xpf', str(PRIVATE / 'restored-controller.tar'), '-C', '/var/lib', timeout=300)
    run('sudo', 'mv', '/var/lib/jenkins/plugins/structs.jpi', '/var/lib/jenkins/plugins/structs.hpi')
    config = json.loads((PRIVATE / 'ci.json').read_text())
    config['mode'] = 'adopt'
    (PRIVATE / 'ci.json').write_text(json.dumps(config))
    server('ci-apply')
    run('sudo', 'test', '!', '-e', '/var/lib/jenkins/plugins/structs.hpi')
    run('sudo', 'test', '-f', '/var/lib/jenkins/plugins/structs.jpi')
    if snapshot() != before:
        raise ValueError('Restored-controller adoption lost users, jobs or credential identity')
    api = client()
    if api.call('job/autonomous-battlefield/config.xml', raw=True) != xml_before:
        raise ValueError('Adoption replaced repository folder configuration')
    if api.call('job/unmanaged-history/lastBuild/api/json?tree=result')['result'] != 'SUCCESS':
        raise ValueError('Adoption erased unmanaged build history')
    fixture.assert_recap(server('ci-apply'), changed=0)
    (EVIDENCE / 'identity.json').write_text(json.dumps(snapshot(), indent=2) + '\n')
    evidence = json.loads((EVIDENCE / 'acceptance.json').read_text())
    evidence['restored_adoption_preserves_unmanaged_history'] = True
    evidence['legacy_hpi_normalized'] = True
    evidence['sonar_analysis_pipeline'] = True
    (EVIDENCE / 'acceptance.json').write_text(json.dumps(evidence) + '\n')


def exercise():
    server('ci-apply')
    fixture.assert_recap(server('ci-verify'), changed=0)
    identity = runtime.API(runtime.JENKINS, 'fixture-admin', SECRET).call('whoAmI/api/json')
    if not identity.get('authenticated') or identity.get('name') != 'fixture-admin':
        raise ValueError('Generated password file did not produce the expected administrator login')
    try:
        runtime.API(runtime.JENKINS, '', '').call('api/json')
    except runtime.Failure:
        pass
    else:
        raise ValueError('Anonymous Jenkins access is enabled')
    try:
        default_valid = runtime.API(runtime.SONAR, 'admin', 'admin').call('api/authentication/validate').get('valid')
    except runtime.Failure:
        default_valid = False
    if default_valid:
        raise ValueError('Sonar default password remains active')
    bootstrap = json.loads(run('sudo', 'cat', '/etc/battlefield-ci/bootstrap.json'))
    if set(bootstrap) != {'admin', 'home', 'mode', 'scannerHome'}:
        raise ValueError('Temporary bootstrap credentials were retained after initialization')
    probe_job()
    before = snapshot()
    invocation = run('systemctl', 'show', 'jenkins', '-p', 'InvocationID', '--value')
    run('sudo', 'touch', '/opt/battlefield-ci/tools/vendor-cache-probe')
    fixture.assert_recap(server('ci-apply'), changed=0)
    fixture.assert_recap(server('ci-plan'), changed=0)
    if snapshot() != before or invocation != run('systemctl', 'show', 'jenkins', '-p', 'InvocationID', '--value'):
        raise ValueError('Second apply changed controller/credential identity or restarted Jenkins')
    # Fail before configuration changes, after stopping the controller for backup.
    # An existing private backup root with unsafe mode must be refused and the
    # unchanged controller automatically restored to its original running state.
    run('sudo', 'sh', '-c', "printf ' ' >> /etc/battlefield-ci/bootstrap.json")
    run('sudo', 'chmod', '0755', '/var/backups/battlefield-ci')
    failure = server('ci-apply', success=False)
    if 'Maintenance backup directory must be root-owned and private' not in failure:
        raise ValueError('Backup guard did not emit its safe diagnostic')
    if run('systemctl', 'is-active', 'jenkins') != 'active' or client().call('api/json?tree=quietingDown')['quietingDown']:
        raise ValueError('Backup failure left the unchanged controller unavailable')
    run('sudo', 'test', '!', '-e', '/var/lib/battlefield-ci/maintenance.json')
    run('sudo', 'chmod', '0700', '/var/backups/battlefield-ci')
    run('sudo', 'docker', 'stop', 'sonarqube-sonarqube-1', 'sonarqube-database-1')
    plan = server('ci-plan')
    fixture.assert_recap(plan, changed=0)
    if '"needs_maintenance": true' not in plan:
        raise ValueError('Stopped CI services were incorrectly considered converged')
    server('ci-apply')
    if snapshot() != before:
        raise ValueError('Repairing stopped services changed credential identity')
    fixture.assert_recap(server('ci-apply'), changed=0)
    (EVIDENCE / 'identity.json').write_text(json.dumps(before, indent=2) + '\n')
    (EVIDENCE / 'acceptance.json').write_text(json.dumps({'fresh_install': True, 'repeat_no_changes': True,
        'plan_read_only': True, 'service_verify': True, 'stopped_services_repaired': True,
        'administrator_password_file': True, 'bootstrap_secrets_removed': True,
        'backup_failure_resumes_controller': True}) + '\n')


def after_reboot():
    for _ in range(60):
        try:
            if not client().call('computer/battlefield-local/api/json?tree=offline')['offline'] and runtime.API(runtime.SONAR, '', '').call('api/system/status').get('status') == 'UP':
                break
        except runtime.Failure:
            pass
        time.sleep(3)
    fixture.assert_recap(server('ci-verify'), changed=0)
    if snapshot() != json.loads((EVIDENCE / 'identity.json').read_text()):
        raise ValueError('Reboot changed controller/agent/credential identity')
    evidence = json.loads((EVIDENCE / 'acceptance.json').read_text())
    evidence['reboot_reconnect'] = True
    (EVIDENCE / 'acceptance.json').write_text(json.dumps(evidence) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('phase', choices=['prepare', 'exercise', 'adoption', 'after-reboot'])
    parser.add_argument('--identity', required=True)
    args = parser.parse_args()
    fixture.require_guest(args.identity)
    os.umask(0o077)
    {'prepare': prepare, 'exercise': exercise, 'adoption': adoption, 'after-reboot': after_reboot}[args.phase]()
    print('PASS: CI fixture ' + args.phase)


if __name__ == '__main__':
    main()
