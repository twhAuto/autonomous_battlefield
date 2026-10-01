#!/usr/bin/env python3
"""Exercise CLI adoption identity, isolation, immutable releases and secret handling."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import sys
import subprocess
import types
import unittest
from unittest.mock import patch
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('cli_bootstrap', ROOT / 'scripts/infra/cli-bootstrap.py')
cli = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cli)
boundary = cli.boundary
UUID = '11111111-1111-1111-1111-111111111111'


def config(bundle='/private/bundle'):
    return {'schema': 1, 'host': {'machine_id': 'a'*32, 'operator': 'operator', 'home': '/home/operator',
            'domain_uuid': UUID, 'network_uuid': UUID, 'kube_system_uid': UUID,
            'kubeconfig': '/private/kubeconfig', 'context': 'battlefield-production', 'bridge_port': 28084},
            'bundle': str(bundle), 'guest': None}


def network():
    tree = ET.fromstring((ROOT / 'deploy/agent-vm/network.xml').read_text())
    ET.SubElement(tree, 'uuid').text = UUID
    return tree


def domain():
    return ET.fromstring(f"""<domain><name>battlefield-agent</name><uuid>{UUID}</uuid><devices>
    <interface type='network'><mac address='52:54:00:ba:77:01'/><source network='battlefield-agent'/>
    <filterref filter='battlefield-agent'><parameter name='IP' value='192.168.234.2'/></filterref></interface>
    </devices></domain>""")


class CLIBootstrapTests(unittest.TestCase):
    def bundle(self, path):
        cli.renderer.render(path, 'operator', '/home/operator', '/private/kubeconfig', 'battlefield-production')
        return config(path)

    def test_inventory_rejects_smoke_port_identity_and_path_injection(self):
        cli.validate(config())
        for key, value in [('bridge_port', 18085), ('bridge_port', True), ('bridge_port', 3128),
                           ('machine_id', 'wrong'), ('domain_uuid', 'wrong'),
                           ('operator', 'operator\nroot'), ('kubeconfig', '/tmp/../secret'),
                           ('context', 'prod;id')]:
            value_config = config(); value_config['host'][key] = value
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                cli.validate(value_config)
        for value in [True, 2, '1']:
            value_config = config(); value_config['schema'] = value
            with self.assertRaises(ValueError): cli.validate(value_config)

    def test_rendered_bundle_and_matched_loopback_override_can_be_adopted(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'bundle'
            value = self.bundle(path)
            self.assertEqual(len(cli.host_files(value)), 8)
            bridge = path / 'battlefield-mcp-bridge.service'
            command = next(line for line in bridge.read_text().splitlines() if line.startswith('ExecStart='))
            bridge.write_text(bridge.read_text().replace('--address=127.0.0.1', '--address=192.168.234.1').replace('28084:8084', '18084:8084'))
            (path / '20-loopback.conf').write_text('[Service]\nExecStart=\n' + command + '\n')
            self.assertEqual(len(cli.host_files(value)), 9)
            (path / '20-loopback.conf').write_text('[Service]\nExecStart=\n' + command.replace('28084', '18085') + '\n')
            with self.assertRaisesRegex(ValueError, 'loopback'):
                cli.host_files(value)

    def test_preserves_provider_domains_and_rejects_wildcard_proxy(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'bundle'; value = self.bundle(path)
            domains = path / 'provider-domains.txt'
            domains.write_text(domains.read_text() + '\nexample.com\n')
            before = domains.read_bytes()
            cli.host_files(value)
            self.assertEqual(domains.read_bytes(), before)
            domains.write_text('.example.com\n')
            with self.assertRaisesRegex(ValueError, 'explicit DNS'):
                cli.host_files(value)

    def test_firewall_adoption_tolerates_comments_whitespace_and_counters_only(self):
        desired = (ROOT / 'deploy/agent-vm/host/firewall.nft').read_text()
        live = '\n'.join(line for line in desired.splitlines() if not line.startswith(('add ', 'flush ')))
        live = live.replace('counter ', 'counter packets 14 bytes 1200 ')
        self.assertTrue(boundary.nft_equivalent(live, desired))
        self.assertTrue(boundary.nft_equivalent(live.replace('priority -10', 'priority filter - 10'), desired))
        self.assertFalse(boundary.nft_equivalent(live.replace('counter packets 14 bytes 1200 drop', 'accept'), desired))
        self.assertFalse(boundary.nft_equivalent(live.replace('3128, 18084', '22, 3128, 18084'), desired))

    def test_rejects_unknown_bundle_files_symlinks_and_boundary_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'bundle'; value = self.bundle(path)
            extra = path / 'evil.conf'; extra.write_text('bad')
            with self.assertRaises(ValueError): cli.host_files(value)
            extra.unlink()
            proxy = path / 'mcp-proxy.Caddyfile'; original = proxy.read_text()
            proxy.write_text(original.replace('127.0.0.1:28084', '192.168.1.1:22'))
            with self.assertRaisesRegex(ValueError, 'boundary'): cli.host_files(value)
            proxy.unlink(); proxy.symlink_to(path / 'squid.conf')
            with self.assertRaisesRegex(ValueError, 'redirected'): cli.host_files(value)

    def test_live_and_persistent_vm_boundary_identity(self):
        expected = (ROOT / 'deploy/agent-vm/filter.xml').read_text()
        boundary.validate_boundary(ET.tostring(domain()), ET.tostring(network()), expected, expected, config()['host'])
        for changed in ('forward', 'dhcp', 'uuid', 'address', 'dns'):
            net = network()
            if changed == 'forward': ET.SubElement(net, 'forward', mode='nat')
            if changed == 'dhcp': ET.SubElement(net.find('ip'), 'dhcp')
            if changed == 'uuid': net.find('uuid').text = 'wrong'
            if changed == 'address': ET.SubElement(net, 'ip', family='ipv6', address='fd00::1')
            if changed == 'dns': net.find('dns').set('enable', 'yes')
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                boundary.validate_boundary(ET.tostring(domain()), ET.tostring(net), expected, expected, config()['host'])
        for changed in ('extra-nic', 'shared-dir', 'wrong-filter', 'wrong-ip'):
            dom = domain()
            if changed == 'extra-nic': ET.SubElement(dom.find('devices'), 'interface', type='bridge')
            if changed == 'shared-dir': ET.SubElement(dom.find('devices'), 'filesystem')
            if changed == 'wrong-filter': dom.find('devices/interface/filterref').set('filter', 'other')
            if changed == 'wrong-ip': dom.find('devices/interface/filterref/parameter').set('value', '192.168.234.3')
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                boundary.validate_boundary(ET.tostring(dom), ET.tostring(network()), expected, expected, config()['host'])
        with self.assertRaisesRegex(ValueError, 'filter differs'):
            boundary.validate_boundary(ET.tostring(domain()), ET.tostring(network()), expected.replace("action='drop'", "action='accept'"), expected, config()['host'])

    def test_first_adoption_resume_repeat_drift_and_changed_release(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(boundary, 'safe_components', side_effect=Path):
            root = Path(directory); target = root / 'unit'; marker = root / 'owned.json'
            target.write_bytes(b'known-content'); target.chmod(0o600)
            files = [{'dest': str(target), 'sha256': boundary.sha(b'known-content'), 'mode': '0600'}]
            # Fixture runs as an ordinary CI user; mock only the expected root group for metadata.
            with patch.object(os, 'getgid', return_value=0):
                first = boundary.inspect_files(files, marker, {'machine_id': 'a'*32})
            self.assertFalse(first['owned']); self.assertEqual(first['content_changes'], [])
            marker.write_text(json.dumps(first['marker']))
            repeated = boundary.inspect_files(files, marker, {'machine_id': 'a'*32})
            self.assertTrue(repeated['owned']); self.assertEqual(repeated['content_changes'], [])
            target.unlink()
            self.assertEqual(boundary.inspect_files(files, marker, {'machine_id': 'a'*32})['content_changes'], [str(target)])
            target.write_bytes(b'drift')
            self.assertEqual(boundary.inspect_files(files, marker, {'machine_id': 'a'*32})['content_changes'], [str(target)])
            with self.assertRaisesRegex(ValueError, 'reconciliation'):
                boundary.inspect_files(files, marker, {'machine_id': 'a'*32}, verify=True)
            with self.assertRaisesRegex(ValueError, 'contract differs'):
                boundary.inspect_files(files, marker, {'machine_id': 'b'*32})
            files[0]['sha256'] = boundary.sha(b'new-release')
            with self.assertRaisesRegex(ValueError, 'update workflow'):
                boundary.inspect_files(files, marker, {'machine_id': 'a'*32})

    def test_unknown_conflicting_file_is_not_adopted_or_written(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(boundary, 'safe_components', side_effect=Path):
            target = Path(directory) / 'unit'; marker = Path(directory) / 'marker'
            target.write_bytes(b'unrelated-service')
            files = [{'dest': str(target), 'sha256': boundary.sha(b'expected'), 'mode': '0600'}]
            with self.assertRaisesRegex(ValueError, 'conflicting'):
                boundary.inspect_files(files, marker, {})
            self.assertEqual(target.read_bytes(), b'unrelated-service')
            self.assertFalse(marker.exists())

    def test_marker_is_required_for_verification(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(boundary, 'safe_components', side_effect=Path):
            with self.assertRaisesRegex(ValueError, 'registered'):
                boundary.inspect_files([], Path(directory) / 'marker', {}, verify=True)

    def test_drift_activates_only_the_affected_services(self):
        self.assertEqual(boundary.affected_services('host', ['/etc/battlefield-agent/provider-domains.txt']), ['squid'])
        self.assertEqual(boundary.affected_services('host', ['/etc/battlefield-agent/firewall.nft']), ['battlefield-agent-firewall'])
        self.assertEqual(boundary.affected_services('host', []), [])
        self.assertEqual(boundary.affected_services('guest', ['/usr/local/bin/battlefield-agent']), ['battlefield-worker'])

    def test_source_hash_pins_actual_copy_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'binary'; path.write_bytes(b'one')
            first = cli.file_entry(path, '/usr/local/bin/codex', '0755')
            path.write_bytes(b'two')
            second = cli.file_entry(path, '/usr/local/bin/codex', '0755')
            self.assertNotEqual(first['sha256'], second['sha256'])
            self.assertNotEqual(first['checksum'], second['checksum'])


    def test_guest_unit_preserves_worker_boundary_and_rejects_credential_injection(self):
        unit = (ROOT / 'deploy/agent-vm/worker.service').read_text()
        cli.validate_guest_unit(unit)
        for modified in [unit.replace('User=battlefield-worker','User=root'),
                         unit+'\nExecStartPre=/bin/sh\n', unit+'\nEnvironment=SECRET=value\n',
                         unit+'\nReadWritePaths=/etc\n', unit+'\nBindReadOnlyPaths=/root\n']:
            with self.assertRaises(ValueError): cli.validate_guest_unit(modified)

    def test_guest_inputs_reject_application_secrets_and_premature_enablement(self):
        errors = types.ModuleType('ansible.errors'); errors.AnsibleFilterError = ValueError
        with patch.dict(sys.modules, {'ansible.errors': errors}):
            module = cli.load('cli_input_filter', ROOT / 'infra/ansible/filter_plugins/cli_bootstrap.py')
        env = 'BATTLEFIELD_AGENT_SECRET='+'a'*32+'\nBATTLEFIELD_ENV=production\nBATTLEFIELD_CLI_ISOLATION_VERIFIED=false\n'
        cfg = {'ai': {'mcpUrl':'http://192.168.234.1:18084/mcp','agentHealthAddress':'127.0.0.1:8083',
                      'agentConcurrency':1,'profiles':[{'runtime':'cli','provider':'codex-cli','enabled':False}]}}
        self.assertTrue(module.validate_guest(env, cfg, False, ['codex','codex-code-mode-host']))
        for modified in [env+'SIGNING_SECRET=must-not-be-copied\n',env.replace('false','true'),
                         env.replace('a'*32,'$(shell-value)')]:
            with self.assertRaises(ValueError): module.validate_guest(modified,cfg,False,['codex','codex-code-mode-host'])
        cfg['ai']['profiles'][0]['enabled']=True
        with self.assertRaises(ValueError): module.validate_guest(env,cfg,False,['codex','codex-code-mode-host'])
        with self.assertRaises(ValueError): module.validate_guest(env.replace('false','true'),cfg,True,['codex'])
        self.assertTrue(module.validate_guest(env.replace('false','true'),cfg,True,['codex','codex-code-mode-host']))
        cfg['ai']['mcpUrl']='https://public.example/mcp'
        with self.assertRaises(ValueError): module.validate_guest(env.replace('false','true'),cfg,True,['codex','codex-code-mode-host'])

    def test_safe_components_reject_symlink_and_writable_parent(self):
        real = types.SimpleNamespace(st_mode=0o040755,st_uid=0)
        link = types.SimpleNamespace(st_mode=0o120777,st_uid=0)
        writable = types.SimpleNamespace(st_mode=0o040777,st_uid=0)
        def metadata(path): return link if str(path)=='/owned/link' else real
        with patch.object(Path,'lstat',metadata), self.assertRaisesRegex(ValueError,'Redirected'):
            boundary.safe_components('/owned/link/file')
        def unsafe(path): return writable if str(path)=='/owned' else real
        with patch.object(Path,'lstat',unsafe), self.assertRaisesRegex(ValueError,'not writable'):
            boundary.safe_components('/owned/file')


    @unittest.skipUnless(os.environ.get('BATTLEFIELD_TEST_ANSIBLE_BIN'), 'real Ansible supplied by test-server-bootstrap.sh')
    def test_guest_filter_through_real_ansible_tagged_values(self):
        env_value = 'BATTLEFIELD_AGENT_SECRET='+'a'*32+'\nBATTLEFIELD_ENV=production\nBATTLEFIELD_CLI_ISOLATION_VERIFIED=false\n'
        cfg = {'ai': {'mcpUrl':'http://192.168.234.1:18084/mcp','agentHealthAddress':'127.0.0.1:8083',
                      'agentConcurrency':1,'profiles':[{'runtime':'cli','provider':'codex-cli','enabled':False}]}}
        play = [{'hosts':'all','gather_facts':False,'connection':'local',
                 'vars':{'cli_env':env_value,'cli_config':cfg},'tasks':[{'ansible.builtin.assert':{'that':[
                     "cli_env | cli_guest_inputs_valid(cli_config, false, ['codex', 'codex-code-mode-host'])"]},'no_log':True}]}]
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/'filter.json'; path.write_text(json.dumps(play))
            environment=dict(os.environ, ANSIBLE_CONFIG=str(ROOT/'infra/ansible/ansible.cfg'),
                             ANSIBLE_FILTER_PLUGINS=str(ROOT/'infra/ansible/filter_plugins'))
            process=subprocess.run([os.environ['BATTLEFIELD_TEST_ANSIBLE_BIN']+'/ansible-playbook','-i','localhost,',str(path)],
                                   capture_output=True,text=True,env=environment,timeout=60)
            self.assertEqual(process.returncode,0,process.stdout+process.stderr)
            self.assertNotIn('a'*32,process.stdout+process.stderr)


if __name__ == '__main__':
    unittest.main()
