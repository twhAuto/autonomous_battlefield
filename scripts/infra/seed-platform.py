#!/usr/bin/env python3
"""Seed a dedicated Jenkins folder/job and its file credentials; never run a build."""
import argparse
import base64
import getpass
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('bootstrap', ROOT / 'scripts/infra/server.py')
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)
MARKER = 'battlefield-platform-managed-v1'
FOLDER = 'battlefield-platform'
REPOSITORY = 'https://github.com/twhAuto/autonomous_battlefield.git'


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Jenkins:
    def __init__(self, url, username, token):
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme not in ('http', 'https') or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError('Use a Jenkins HTTP(S) URL without credentials, query or fragment')
        if parsed.scheme == 'http' and parsed.hostname not in ('127.0.0.1', 'localhost', '::1'):
            raise ValueError('Plain HTTP is limited to loopback; use an SSH tunnel or trusted HTTPS')
        self.url = url.rstrip('/') + '/'
        self.auth = 'Basic ' + base64.b64encode(f'{username}:{token}'.encode()).decode()
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, path, data=None, missing=False, content_type='application/xml'):
        request = urllib.request.Request(self.url + path, data=data,
                    headers={'Authorization': self.auth, 'Content-Type': content_type, 'Accept': 'application/json'})
        try:
            with self.opener.open(request, timeout=30) as response:
                return response.read()
        except urllib.error.HTTPError as error:
            if data is not None and path == 'pluginManager/installNecessaryPlugins' and error.code in (302, 303):
                location = urllib.parse.urljoin(request.full_url, error.headers.get('Location', ''))
                if location == urllib.parse.urljoin(self.url, 'pluginManager/updates/'):
                    return b''  # Jenkins accepted the POST. Do not follow any redirect with credentials.
            if missing and error.code == 404:
                return None
            # Error pages may include submitted credentials. Never include their bodies.
            raise ValueError(f'Jenkins request failed with HTTP {error.code}; verify API permissions and URL') from None
        except urllib.error.URLError:
            raise ValueError('Jenkins connection failed; verify tunnel or trusted HTTPS') from None


def xml(tag, **values):
    root = ET.Element(tag)
    for key, value in values.items():
        ET.SubElement(root, key).text = str(value)
    return root


def folder_xml():
    return ET.tostring(xml('com.cloudbees.hudson.plugins.folder.Folder', description=MARKER), encoding='utf-8')


def job_xml():
    root = xml('flow-definition', description=MARKER + ': AB-131 host plan/apply/verify; manually triggered', keepDependencies='false')
    properties = ET.SubElement(root, 'properties')
    parameter = ET.SubElement(ET.SubElement(ET.SubElement(properties, 'hudson.model.ParametersDefinitionProperty'), 'parameterDefinitions'), 'hudson.model.ChoiceParameterDefinition')
    ET.SubElement(parameter, 'name').text = 'ACTION'
    choices = ET.SubElement(parameter, 'choices', {'class': 'java.util.Arrays$ArrayList'})
    items = ET.SubElement(choices, 'a', {'class': 'string-array'})
    for value in ('plan', 'apply', 'verify'):
        ET.SubElement(items, 'string').text = value
    definition = ET.SubElement(root, 'definition', {'class': 'org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition'})
    scm = ET.SubElement(definition, 'scm', {'class': 'hudson.plugins.git.GitSCM'})
    ET.SubElement(scm, 'configVersion').text = '2'
    remote = ET.SubElement(ET.SubElement(scm, 'userRemoteConfigs'), 'hudson.plugins.git.UserRemoteConfig')
    ET.SubElement(remote, 'url').text = REPOSITORY
    ET.SubElement(remote, 'credentialsId').text = 'github-app-battlefield'
    branch = ET.SubElement(ET.SubElement(scm, 'branches'), 'hudson.plugins.git.BranchSpec')
    ET.SubElement(branch, 'name').text = '*/main'
    ET.SubElement(scm, 'extensions')
    ET.SubElement(definition, 'scriptPath').text = 'Jenkinsfile.platform'
    ET.SubElement(definition, 'lightweight').text = 'false'
    ET.SubElement(root, 'disabled').text = 'false'
    ET.SubElement(root, 'triggers')
    return ET.tostring(root, encoding='utf-8')


def ensure_item(client, path, create, definition):
    current = client.request(path + '/config.xml', missing=True)
    if current is None:
        client.request(create, definition)
        return
    check_owner(current, definition)
    root = ET.fromstring(current)
    if root.tag.endswith('.Folder'):
        return  # Preserve the existing credential store.
    client.request(path + '/config.xml', definition)


def check_owner(current, definition):
    root = ET.fromstring(current)
    if root.tag != ET.fromstring(definition).tag or not (root.findtext('description') or '').startswith(MARKER):
        raise ValueError('Refusing to adopt an unowned Jenkins folder/job')


def credential_xml(identifier, content):
    root = xml('org.jenkinsci.plugins.plaincredentials.impl.FileCredentialsImpl',
               scope='GLOBAL', id=identifier, description=MARKER,
               fileName=identifier, secretBytes=base64.b64encode(content).decode())
    return ET.tostring(root, encoding='utf-8')


def validate_pipeline(client):
    boundary = 'battlefield-platform-validation'
    data = ('--' + boundary + '\r\nContent-Disposition: form-data; name="jenkinsfile"\r\n\r\n' +
            (ROOT / 'Jenkinsfile.platform').read_text() + '\r\n--' + boundary + '--\r\n').encode()
    result = client.request('pipeline-model-converter/validate', data,
                            content_type='multipart/form-data; boundary=' + boundary)
    if result.strip() != b'Jenkinsfile successfully validated.':
        raise ValueError('Jenkins cannot compile the platform pipeline; check plugin compatibility and arrange an external maintenance restart if a plugin was just installed')


def check_plugins(client, install=False):
    pins = json.loads((ROOT / 'infra/jenkins/platform-plugins.json').read_text())['plugins']
    for attempt in range(31):
        plugins = json.loads(client.request('pluginManager/api/json?tree=plugins[shortName,active]'))
        present = {x['shortName'] for x in plugins['plugins'] if x['active']}
        missing = sorted(set(pins) - present)
        if not missing:
            validate_pipeline(client)
            return
        if not install:
            raise ValueError('Required Jenkins plugins are missing/inactive: ' + ', '.join(missing))
        if attempt == 0:
            root = ET.Element('jenkins')
            for name in missing:
                ET.SubElement(root, 'install', {'plugin': name + '@' + pins[name]})
            client.request('pluginManager/installNecessaryPlugins', ET.tostring(root))
        if attempt == 30:
            raise ValueError('Required plugins are not active yet; inspect plugin installation and arrange an external maintenance restart if required')
        time.sleep(2)


def seed(client, files):
    check_plugins(client)
    # Verify all data before writing anything; caller supplies private files outside Git.
    contents = {key: bootstrap.private_file(value).read_bytes() for key, value in files.items()}
    bootstrap.load_inventory(files['host-inventory'])
    if not contents['host-inputs'].startswith(b'$ANSIBLE_VAULT;') or any(not x for x in contents.values()):
        raise ValueError('Encrypted inputs and all credential files must be nonempty')
    for value in files.values():
        bootstrap.outside_repo(bootstrap.safe_path(value))
    # Preflight ownership of every object before sending any credential content.
    for path, definition in [('job/' + FOLDER, folder_xml()), ('job/' + FOLDER + '/job/host', job_xml())]:
        current = client.request(path + '/config.xml', missing=True)
        if current is not None:
            check_owner(current, definition)
    store = 'job/' + FOLDER + '/credentials/store/folder/domain/_/'
    retained = set()
    for identifier in contents:
        existing = client.request(store + 'credential/' + identifier + '/config.xml', missing=True)
        if existing is not None:
            root = ET.fromstring(existing)
            if root.tag != 'org.jenkinsci.plugins.plaincredentials.impl.FileCredentialsImpl' or root.findtext('description') != MARKER:
                raise ValueError(f'Existing {identifier} has a different type or owner; inspect before adoption')
            retained.add(identifier)
    ensure_item(client, 'job/' + FOLDER, 'createItem?name=' + FOLDER, folder_xml())
    for identifier, content in contents.items():
        if identifier in retained:
            print(f'Retained existing {identifier}; credential values are never rotated by seeding')
        else:
            client.request(store + 'createCredentials', credential_xml(identifier, content))
    ensure_item(client, 'job/' + FOLDER + '/job/host', 'job/' + FOLDER + '/createItem?name=host', job_xml())
    print('Configured battlefield-platform/host. No build or host apply was triggered.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', default='http://127.0.0.1:8080')
    parser.add_argument('--user', default='whtan')
    parser.add_argument('--token-file', type=bootstrap.safe_path)
    for name in ('inventory', 'vault', 'vault-password-file', 'become-password-file'):
        parser.add_argument('--' + name, required=True, type=bootstrap.safe_path)
    args = parser.parse_args()
    token = bootstrap.private_file(args.token_file).read_text().strip() if args.token_file else getpass.getpass('Jenkins API token: ')
    if not token or ':' in args.user or not re.fullmatch('[A-Za-z0-9_.@-]+', args.user):
        raise ValueError('A Jenkins user and API token are required')
    seed(Jenkins(args.url, args.user, token), {'host-inventory': args.inventory, 'host-inputs': args.vault,
         'host-vault-password': args.vault_password_file, 'host-sudo-password': args.become_password_file})


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, ET.ParseError) as error:
        print('Platform setup: ' + (str(error) if isinstance(error, ValueError) else 'Setup failed; no private response published'), file=sys.stderr)
        sys.exit(1)
