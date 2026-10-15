#!/usr/bin/env python3
"""Require every Dockerfile base image to be pinned by digest, with its tag noted.

A tag alone can move to different content between builds, so each FROM names the
image by digest only (SonarQube's docker:S8431 rejects tag and digest together,
because Docker ignores the tag). The line above records the tag the digest came
from as `# Base image: name:tag`, which the refresh procedure in the supply-chain
runbook resolves again.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PINNED = re.compile(r'^([a-z0-9][a-z0-9._/-]*)@sha256:[0-9a-f]{64}$')
NOTED = re.compile(r'^# Base image: ([a-z0-9][a-z0-9._/-]*):[A-Za-z0-9._-]+$')


def unpinned(dockerfile: str) -> list[str]:
    """Return the FROM images that are not earlier stages and are not digest-pinned
    under a matching `# Base image: name:tag` comment."""
    stages, problems, previous = set(), [], ''
    for line in dockerfile.splitlines():
        words = line.split()
        if words and words[0].upper() == 'FROM':
            arguments = [word for word in words[1:] if not word.startswith('--')]
            image = arguments[0] if arguments else ''
            if image.lower() not in stages and image != 'scratch' and not noted_pin(image, previous):
                problems.append(image)
            if len(arguments) == 3 and arguments[1].upper() == 'AS':
                stages.add(arguments[2].lower())
        previous = line.strip()
    return problems


def noted_pin(image: str, comment: str) -> bool:
    pinned, noted = PINNED.match(image), NOTED.match(comment)
    return bool(pinned and noted and pinned.group(1) == noted.group(1))


def self_test() -> None:
    digest = 'sha256:' + 'a' * 64
    noted = f'# Base image: golang:1.26\nFROM golang@{digest} AS build\n'
    assert unpinned(noted + 'FROM build\nFROM scratch\n') == []
    assert unpinned('FROM alpine:3.23\n') == ['alpine:3.23']
    assert unpinned(f'FROM golang@{digest}\n') == [f'golang@{digest}'], 'tag comment required'
    assert unpinned(f'# Base image: node:24\nFROM golang@{digest}\n') == [f'golang@{digest}'], 'comment names another image'
    assert unpinned(f'# Base image: golang:1.26\nFROM golang:1.26@{digest}\n') == [f'golang:1.26@{digest}'], 'tag and digest together'
    assert unpinned('# Base image: alpine:3\nFROM alpine@sha256:short AS x\n') == ['alpine@sha256:short']


def main() -> int:
    self_test()
    dockerfiles = sorted(path for path in ROOT.glob('*/Dockerfile*'))
    if not dockerfiles:
        print('No Dockerfiles found.', file=sys.stderr)
        return 1
    failed = False
    for path in dockerfiles:
        for image in unpinned(path.read_text()):
            print(f'{path.relative_to(ROOT)}: base image {image} must be name@sha256:<digest> under a "# Base image: name:tag" comment', file=sys.stderr)
            failed = True
    if not failed:
        print(f'{len(dockerfiles)} Dockerfiles pin every base image by digest and record its tag.')
    return 1 if failed else 0


if __name__ == '__main__':
    sys.exit(main())
