#!/usr/bin/env python3
"""Report whether each pinned image still matches its recorded tag.

Run on demand before the image refresh in the supply-chain runbook. Pins are every
`# Base image: name:tag` comment above a Dockerfile `FROM name@sha256:<digest>` line,
and every `name:tag@sha256:<digest>` image in scripts/ci/tool-versions.env and the
infra/deploy manifests. For each, resolve the tag's current multi-platform index digest
with `docker buildx imagetools inspect` and compare. Nothing is edited. Exit status: 0
when every pin is current, 1 when any pin is stale, 2 when a tag could not be resolved
or no pins were found.
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
NOTED = re.compile(r'^# Base image: (?P<name>[a-z0-9][a-z0-9._/-]*):(?P<tag>[A-Za-z0-9._-]+)$')
PINNED = re.compile(r'^FROM\s+(?:--\S+\s+)*(?P<name>[a-z0-9][a-z0-9._/-]*)@(?P<digest>sha256:[0-9a-f]{64})\b', re.IGNORECASE)
DIGEST = re.compile(r'^Digest:\s+(sha256:[0-9a-f]{64})\s*$', re.MULTILINE)
TAGGED = (r'(?P<name>[a-z0-9][a-z0-9._/-]*):(?P<tag>[A-Za-z0-9._-]+)'
          r'@(?P<digest>sha256:[0-9a-f]{64})')
# A tool-versions.env assignment or a manifest `image:` value.
REFERENCE = re.compile(r'^(?:[A-Z0-9_]+_IMAGE=|\s*(?:-\s*)?image:\s*["\']?)' + TAGGED + r'["\']?\s*$')


def pins(path: Path) -> list[tuple[int, str, str, str]]:
    """Return (line number, name, tag, digest) for each noted digest pin."""
    found, previous = [], ''
    for number, line in enumerate(path.read_text().splitlines(), start=1):
        noted, pinned = NOTED.match(previous), PINNED.match(line.strip())
        if noted and pinned and noted['name'] == pinned['name']:
            found.append((number, pinned['name'], noted['tag'], pinned['digest']))
        previous = line.strip()
    return found


def references(path: Path) -> list[tuple[int, str, str, str]]:
    """Return (line number, name, tag, digest) for each name:tag@sha256 image reference."""
    found = []
    for number, line in enumerate(path.read_text().splitlines(), start=1):
        match = REFERENCE.match(line)
        if match:
            found.append((number, match['name'], match['tag'], match['digest']))
    return found


def pinned_images():
    """Yield (path, line number, name, tag, digest) for every recorded image pin."""
    for path in sorted(ROOT.glob('*/Dockerfile*')):
        yield from ((path, *pin) for pin in pins(path))
    for path in [ROOT / 'scripts/ci/tool-versions.env', *sorted(ROOT.glob('infra/deploy/**/*.y*ml'))]:
        if path.is_file():
            yield from ((path, *pin) for pin in references(path))


RESOLVE_TIMEOUT_SECONDS = 120


def resolve(reference: str) -> str | None:
    """Return the tag's current index digest, or None if it cannot be resolved."""
    try:
        result = subprocess.run(['docker', 'buildx', 'imagetools', 'inspect', reference],
                                capture_output=True, text=True, timeout=RESOLVE_TIMEOUT_SECONDS)
    except (OSError, subprocess.TimeoutExpired):
        return None
    match = DIGEST.search(result.stdout) if result.returncode == 0 else None
    return match.group(1) if match else None


def main() -> int:
    stale = unresolved = checked = 0
    for path, number, name, tag, digest in pinned_images():
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
        print('No image pins found.', file=sys.stderr)
        return 2
    if unresolved:
        return 2
    return 1 if stale else 0


if __name__ == '__main__':
    sys.exit(main())
