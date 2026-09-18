#!/usr/bin/env python3
"""Render a credential-free, reviewable host bundle; never change live services."""
import argparse
from pathlib import Path
import re
import shutil

ROOT = Path(__file__).resolve().parents[2]


def render(output, user, home, kubeconfig, context):
    # These values enter systemd directives. Reject escapes/specifiers and whitespace
    # instead of allowing injected units or ambiguous command-line arguments.
    if not re.fullmatch(r"[a-z_][a-z0-9_-]*", user):
        raise ValueError("Expected a Unix service user name")
    for value in (home, kubeconfig):
        if not re.fullmatch(r"/[A-Za-z0-9_./-]+", value):
            raise ValueError("Use absolute paths without spaces, escapes or systemd specifiers")
    if not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]*", context):
        raise ValueError("Invalid Kubernetes context name")
    output = Path(output)
    # Re-render to a new directory and diff; never silently replace reviewed files.
    output.mkdir(parents=True, exist_ok=False)
    values = {"USER": user, "HOME": home, "KUBECONFIG": kubeconfig, "CONTEXT": context}
    for source in sorted((ROOT / "deploy/agent-vm/host").iterdir()):
        text = source.read_text()
        for key, value in values.items():
            text = text.replace("@" + key + "@", value)
        (output / source.name).write_text(text)
    for name in ("squid.conf", "provider-domains.txt"):
        shutil.copyfile(ROOT / "deploy/agent-vm" / name, output / name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, help="New directory; must not exist")
    parser.add_argument("--user", required=True, help="Host user with access to the private kubeconfig")
    parser.add_argument("--home", required=True)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--context", default="battlefield-production")
    args = parser.parse_args()
    render(args.output, args.user, args.home, args.kubeconfig, args.context)
    print(f"Rendered {args.output}. No services changed. Follow docs/operations/agent-vm.md.")


if __name__ == "__main__":
    main()
