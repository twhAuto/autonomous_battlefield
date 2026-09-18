#!/usr/bin/env python3
"""Check generated host boundary configuration without root or a live VM."""
import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("vm_host", ROOT / "scripts/ops/render-agent-vm-host.py")
vm_host = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vm_host)


class HostBundleTests(unittest.TestCase):
    def render(self, output, **overrides):
        values = dict(user="operator", home="/home/operator", kubeconfig="/private/kubeconfig", context="battlefield-production")
        values.update(overrides)
        vm_host.render(output, **values)

    def test_private_bridge_and_firewall_survive_rendering(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory) / "bundle"
            self.render(out)
            bridge = (out / "battlefield-mcp-bridge.service").read_text()
            self.assertIn("User=operator", bridge)
            self.assertIn("--kubeconfig=/private/kubeconfig --context=battlefield-production", bridge)
            self.assertIn("--address=127.0.0.1 service/battlefield-mcp 18085:8084", bridge)
            self.assertNotIn("--address=192.168.", bridge)
            caddy = (out / "mcp-proxy.Caddyfile").read_text()
            self.assertIn("bind 192.168.234.1", caddy)
            self.assertIn("reverse_proxy 127.0.0.1:18085", caddy)
            self.assertIn("header_up Host 127.0.0.1:8084", caddy)
            self.assertIn("admin off", caddy)
            self.assertNotIn("persist_config", caddy)
            firewall = (out / "firewall.nft").read_text()
            loopback = 'iifname "lo" ip daddr 127.0.0.1 tcp dport 18084 counter accept'
            deny = 'tcp dport { 3128, 18084 } counter drop'
            self.assertLess(firewall.index(loopback), firewall.index(deny))
            self.assertIn('ip saddr 192.168.234.2 ip daddr 192.168.234.1', firewall)
            self.assertNotIn("flush ruleset", firewall)
            self.assertIn("flush table inet battlefield_agent", firewall)
            self.assertEqual(len(list(out.iterdir())), 8)
            for file in out.iterdir():
                self.assertNotIn("@USER@", file.read_text())
                self.assertNotIn("@KUBECONFIG@", file.read_text())

    def test_rejects_unit_injection_before_writing(self):
        for key, value in (("user", "root\nExecStart=/bin/false"), ("home", "/home/%u"),
                           ("kubeconfig", "relative/path"), ("kubeconfig", "/path with spaces"),
                           ("context", "prod\n[Service]")):
            with self.subTest(key=key, value=value), tempfile.TemporaryDirectory() as directory:
                out = Path(directory) / "bundle"
                with self.assertRaises(ValueError):
                    self.render(out, **{key: value})
                self.assertFalse(out.exists())

    def test_refuses_to_replace_existing_reviewed_bundle(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory) / "bundle"
            self.render(out)
            config = out / "mcp-proxy.Caddyfile"
            config.write_text("operator-reviewed-content")
            with self.assertRaises(FileExistsError):
                self.render(out)
            self.assertEqual(config.read_text(), "operator-reviewed-content")


if __name__ == "__main__":
    unittest.main()
