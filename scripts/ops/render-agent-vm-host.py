#!/usr/bin/env python3
"""Render a credential-free, reviewable host bundle; never change live services."""
import argparse
from pathlib import Path
import re
import shutil

ROOT = Path(__file__).resolve().parents[2]


def render(output, user, home, kubeconfig, context, bridge_port=28084):
    # These values enter systemd directives. Reject escapes/specifiers and whitespace
    # instead of allowing injected units or ambiguous command-line arguments.
    if not re.fullmatch(r"[a-z_][a-z0-9_-]*", user):
        raise ValueError("Expected a Unix service user name")
    for value in (home, kubeconfig):
        if not re.fullmatch(r"/[A-Za-z0-9_./-]+", value):
            raise ValueError("Use absolute paths without spaces, escapes or systemd specifiers")
    if not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]*", context):
        raise ValueError("Invalid Kubernetes context name")
    if type(bridge_port) is not int or not 1024 <= bridge_port <= 65535:
        raise ValueError("Bridge port must be an integer from 1024 to 65535")
    output = Path(output)
    # Re-render to a new directory and diff; never silently replace reviewed files.
    output.mkdir(parents=True, exist_ok=False)
    values = {"USER": user, "HOME": home, "KUBECONFIG": kubeconfig, "CONTEXT": context, "BRIDGE_PORT": str(bridge_port)}
    for source in sorted((ROOT / "infra/deploy/agent-vm/host").iterdir()):
        text = source.read_text()
        for key, value in values.items():
            text = text.replace("@" + key + "@", value)
        (output / source.name).write_text(text)
    for name in ("squid.conf", "provider-domains.txt"):
        shutil.copyfile(ROOT / "infra/deploy/agent-vm" / name, output / name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, help="New directory; must not exist")
    parser.add_argument("--user", required=True, help="Host user with access to the private kubeconfig")
    parser.add_argument("--home", required=True)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--context", default="battlefield-production")
    parser.add_argument("--bridge-port", type=int, default=28084,
                        help="Free host loopback port (default: 28084); keep separate from Jenkins smoke ports")
    args = parser.parse_args()
    render(args.output, args.user, args.home, args.kubeconfig, args.context, args.bridge_port)
    print(f"Rendered {args.output}. No services changed. Follow docs/operations/agent-vm.md.")


if __name__ == "__main__":
    main()
