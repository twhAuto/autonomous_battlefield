#!/usr/bin/env python3
"""Validate rendered public routes and VM boundary artifacts without changing a host."""
import importlib.util
import json
import sys
sys.dont_write_bytecode = True
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('launch', ROOT / 'scripts/ops/render-limited-launch.py')
launch = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launch)


class LaunchTests(unittest.TestCase):
    def test_tunnel_has_only_application_routes_and_requires_access(self):
        with tempfile.TemporaryDirectory() as out:
            args = SimpleNamespace(hostname='battle.example.com', team='friends', audience='a'*64,
                tunnel_id='12345678-1234-1234-1234-123456789012', cloudflared_image='cloudflare/cloudflared@sha256:'+'b'*64,
                output=out, admin=['owner@example.com'], tester=[], player=['friend@example.com'])
            config = launch.render(args)
            routes = config['ingress']
            self.assertEqual(routes[-1], {'service': 'http_status:404'})
            self.assertEqual([r['service'] for r in routes[:-1]], ['http://battlefield-api:8080', 'http://battlefield-frontend:8080'])
            for route in routes[:-1]:
                self.assertEqual(route['hostname'], args.hostname)
                self.assertEqual(route['originRequest']['access'], {'required': True, 'teamName': 'friends', 'audTag': ['a'*64]})
            self.assertFalse((Path(out)/'auth.env').exists())
            self.assertEqual((Path(out)/'tunnel.json').stat().st_mode & 0o777, 0o600)
            resources = json.loads((Path(out)/'tunnel.json').read_text())['items']
            self.assertEqual([r['kind'] for r in resources], ['ConfigMap', 'Deployment'])
            policies = json.loads((Path(out)/'network-policies.json').read_text())['items']
            self.assertEqual(len(policies), 2)
            self.assert_scrape_policy_boundaries(policies)
            args.player = ['OWNER@example.com']
            with self.assertRaises(ValueError): launch.render(args)
            args.player = []
            args.cloudflared_image = 'cloudflare/cloudflared:latest'
            with self.assertRaises(ValueError): launch.render(args)

    def test_guest_hostname_has_its_own_access_validated_routes(self):
        with tempfile.TemporaryDirectory() as out:
            args = SimpleNamespace(hostname='battle.example.com', team='friends', audience='a'*64,
                tunnel_id='12345678-1234-1234-1234-123456789012', cloudflared_image='cloudflare/cloudflared@sha256:'+'b'*64,
                output=out, admin=['owner@example.com'], tester=[], player=[],
                guest_hostname='guest.example.com', guest_audience='c'*64)
            routes = launch.render(args)['ingress']
            self.assertEqual(routes[-1], {'service': 'http_status:404'})
            self.assertEqual([(r['hostname'], r['service'], r['originRequest']['access']['audTag']) for r in routes[:-1]], [
                ('battle.example.com', 'http://battlefield-api:8080', ['a'*64]),
                ('battle.example.com', 'http://battlefield-frontend:8080', ['a'*64]),
                ('guest.example.com', 'http://battlefield-api:8080', ['c'*64]),
                ('guest.example.com', 'http://battlefield-frontend:8080', ['c'*64])])
            self.assertTrue(all(r['originRequest']['access']['required'] for r in routes[:-1]))
            for hostname, audience in [('guest.example.com', ''), ('', 'c'*64), ('battle.example.com', 'c'*64), ('guest.example.com', 'a'*64), ('guest', 'c'*64)]:
                args.guest_hostname, args.guest_audience = hostname, audience
                with self.assertRaises(ValueError): launch.render(args)

    def assert_scrape_policy_boundaries(self, resources):
        policies = {r['metadata']['name']: r['spec'] for r in resources if r['kind'] == 'NetworkPolicy'}
        tunnel = {'podSelector': {'matchLabels': {'app': 'battlefield-tunnel'}}}
        frontend = {'podSelector': {'matchLabels': {
            'app.kubernetes.io/name': 'autonomous-battlefield',
            'app.kubernetes.io/component': 'frontend'}}}
        prometheus = {'podSelector': {'matchLabels': {
            'app.kubernetes.io/name': 'prometheus',
            'app.kubernetes.io/component': 'observability',
            'app.kubernetes.io/part-of': 'autonomous-battlefield'}}}
        for component, allowed in [('api', [tunnel, frontend, prometheus]), ('frontend', [tunnel])]:
            policy = policies[f'battlefield-{component}-tunnel-only']
            self.assertEqual(policy['podSelector'], {'matchLabels': {
                'app.kubernetes.io/name': 'autonomous-battlefield',
                'app.kubernetes.io/component': component}})
            self.assertEqual(policy['policyTypes'], ['Ingress'])
            # Exact peers exclude namespace-wide, cross-namespace and IP-based allowances.
            self.assertEqual(policy['ingress'], [{'from': allowed,
                'ports': [{'protocol': 'TCP', 'port': 8080}]}])

    def test_vm_has_no_forwarding_and_explicit_egress(self):
        network = ET.parse(ROOT/'infra/deploy/agent-vm/network.xml').getroot()
        self.assertIsNone(network.find('forward'))
        self.assertEqual(network.find('dns').get('enable'), 'no')
        rules = ET.parse(ROOT/'infra/deploy/agent-vm/filter.xml').getroot().findall('rule')
        outbound = [r.find('tcp') for r in rules if r.get('action')=='accept' and r.get('direction')=='out']
        self.assertTrue(all(r.get('dstipaddr')=='192.168.234.1' for r in outbound))
        self.assertEqual({r.get('dstportstart') for r in outbound if r.get('state')!='ESTABLISHED'}, {'3128','18084'})
        self.assertTrue(any(r.get('action')=='drop' and r.find('all-ipv6') is not None for r in rules))
        proxy = (ROOT/'infra/deploy/agent-vm/squid.conf').read_text()
        self.assertLess(proxy.index('http_access deny private'), proxy.index('http_access allow'))
        self.assertIn('http_access deny all', proxy)
        worker = (ROOT/'infra/deploy/agent-vm/worker.service').read_text()
        self.assertIn('User=battlefield-worker', worker)
        self.assertIn('KillMode=control-group', worker)
        self.assertNotIn('CAPABILITY_SECRET', worker)


if __name__ == '__main__':
    unittest.main()
