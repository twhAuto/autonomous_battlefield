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
spec = importlib.util.spec_from_file_location('launch', ROOT / 'scripts/ops/render-friends-launch.py')
launch = importlib.util.module_from_spec(spec)
spec.loader.exec_module(launch)


class LaunchTests(unittest.TestCase):
    def test_tunnel_has_only_application_routes_and_requires_access(self):
        with tempfile.TemporaryDirectory() as out:
            args = SimpleNamespace(hostname='battle.example.com', team='friends', audience='a'*64,
                tunnel_id='12345678-1234-1234-1234-123456789012', cloudflared_image='cloudflare/cloudflared@sha256:'+'b'*64,
                output=out, admin=['owner@example.com'], developer=[], player=['friend@example.com'])
            config = launch.render(args)
            routes = config['ingress']
            self.assertEqual(routes[-1], {'service': 'http_status:404'})
            self.assertEqual([r['service'] for r in routes[:-1]], ['http://battlefield-api:8080', 'http://battlefield-frontend:8080'])
            for route in routes[:-1]:
                self.assertEqual(route['hostname'], args.hostname)
                self.assertEqual(route['originRequest']['access'], {'required': True, 'teamName': 'friends', 'audTag': ['a'*64]})
            auth = (Path(out)/'auth.env').read_text()
            self.assertNotIn('PASSWORD', auth)
            self.assertNotIn('SESSION_SECRET', auth)
            self.assertIn('AUTH_MODE=cloudflare', auth)
            resources = json.loads((Path(out)/'tunnel.json').read_text())['items']
            self.assertEqual(sum(r['kind']=='NetworkPolicy' for r in resources), 2)
            args.player = ['OWNER@example.com']
            with self.assertRaises(ValueError): launch.render(args)
            args.player = []
            args.cloudflared_image = 'cloudflare/cloudflared:latest'
            with self.assertRaises(ValueError): launch.render(args)

    def test_vm_has_no_forwarding_and_explicit_egress(self):
        network = ET.parse(ROOT/'deploy/agent-vm/network.xml').getroot()
        self.assertIsNone(network.find('forward'))
        self.assertEqual(network.find('dns').get('enable'), 'no')
        rules = ET.parse(ROOT/'deploy/agent-vm/filter.xml').getroot().findall('rule')
        outbound = [r.find('tcp') for r in rules if r.get('action')=='accept' and r.get('direction')=='out']
        self.assertTrue(all(r.get('dstipaddr')=='192.168.234.1' for r in outbound))
        self.assertEqual({r.get('dstportstart') for r in outbound if r.get('state')!='ESTABLISHED'}, {'3128','18084'})
        self.assertTrue(any(r.get('action')=='drop' and r.find('all-ipv6') is not None for r in rules))
        proxy = (ROOT/'deploy/agent-vm/squid.conf').read_text()
        self.assertLess(proxy.index('http_access deny private'), proxy.index('http_access allow'))
        self.assertIn('http_access deny all', proxy)
        worker = (ROOT/'deploy/agent-vm/worker.service').read_text()
        self.assertIn('User=battlefield-worker', worker)
        self.assertIn('KillMode=control-group', worker)
        self.assertNotIn('CAPABILITY_SECRET', worker)


if __name__ == '__main__':
    unittest.main()
