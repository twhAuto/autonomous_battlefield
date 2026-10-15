#!/usr/bin/env python3
"""Report whether each pinned Dockerfile base image still matches its recorded tag.

Run on demand before the base-image refresh in the supply-chain runbook. For every
`# Base image: name:tag` comment above a `FROM name@sha256:<digest>` line, resolve the
tag's current multi-platform index digest with `docker buildx imagetools inspect` and
compare. Nothing is edited. Exit status: 0 when every pin is current, 1 when any pin
is stale, 2 when a tag could not be resolved or no pins were found.
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
NOTED = re.compile(r'^# Base image: (?P<name>[a-z0-9][a-z0-9._/-]*):(?P<tag>[A-Za-z0-9._-]+)$')
PINNED = re.compile(r'^FROM\s+(?:--\S+\s+)*(?P<name>[a-z0-9][a-z0-9._/-]*)@(?P<digest>sha256:[0-9a-f]{64})\b', re.IGNORECASE)
DIGEST = re.compile(r'^Digest:\s+(sha256:[0-9a-f]{64})\s*$', re.MULTILINE)


def pins(path: Path) -> list[tuple[int, str, str, str]]:
    """Return (line number, name, tag, digest) for each noted digest pin."""
    found, previous = [], ''
    for number, line in enumerate(path.read_text().splitlines(), start=1):
        noted, pinned = NOTED.match(previous), PINNED.match(line.strip())
        if noted and pinned and noted['name'] == pinned['name']:
            found.append((number, pinned['name'], noted['tag'], pinned['digest']))
        previous = line.strip()
    return found


def resolve(reference: str) -> str | None:
    result = subprocess.run(['docker', 'buildx', 'imagetools', 'inspect', reference],
                            capture_output=True, text=True, timeout=120)
    match = DIGEST.search(result.stdout) if result.returncode == 0 else None
    return match.group(1) if match else None


def main() -> int:
    stale = unresolved = checked = 0
    for path in sorted(ROOT.glob('*/Dockerfile*')):
        for number, name, tag, digest in pins(path):
            checked += 1
            location = f'{path.relative_to(ROOT)}:{number}'
            current = resolve(f'{name}:{tag}')
            if current is None:
                unresolved += 1
                print(f'UNRESOLVED {location} {name}:{tag}', file=sys.stderr)
            elif current != digest:
                stale += 1
                print(f'STALE      {location} {name}:{tag} pinned {digest}, tag now {current}')
            else:
                print(f'CURRENT    {location} {name}:{tag}')
    if checked == 0:
        print('No noted base-image pins found.', file=sys.stderr)
        return 2
    if unresolved:
        return 2
    return 1 if stale else 0


if __name__ == '__main__':
    sys.exit(main())
