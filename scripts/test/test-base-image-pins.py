#!/usr/bin/env python3
"""Require every Dockerfile base image to be pinned by tag and digest.

A tag alone can move to different content between builds. The tag stays for
readability; the digest decides what is built. See the supply-chain runbook for
refreshing pins.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PINNED = re.compile(r'^[a-z0-9][a-z0-9._/:-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$')


def unpinned(dockerfile: str) -> list[str]:
    """Return the FROM images that are neither digest-pinned nor earlier stages."""
    stages, problems = set(), []
    for line in dockerfile.splitlines():
        words = line.split()
        if not words or words[0].upper() != 'FROM':
            continue
        arguments = [word for word in words[1:] if not word.startswith('--')]
        image = arguments[0] if arguments else ''
        if image.lower() not in stages and image != 'scratch' and not PINNED.match(image):
            problems.append(image)
        if len(arguments) == 3 and arguments[1].upper() == 'AS':
            stages.add(arguments[2].lower())
    return problems


def self_test() -> None:
    digest = 'sha256:' + 'a' * 64
    assert unpinned(f'FROM golang:1.26@{digest} AS build\nFROM build\nFROM scratch\n') == []
    assert unpinned('FROM alpine:3.23\n') == ['alpine:3.23']
    assert unpinned(f'FROM --platform=linux/amd64 node@{digest}\n') == [f'node@{digest}']
    assert unpinned('FROM alpine@sha256:short AS x\n') == ['alpine@sha256:short']


def main() -> int:
    self_test()
    dockerfiles = sorted(path for path in ROOT.glob('*/Dockerfile*'))
    if not dockerfiles:
        print('No Dockerfiles found.', file=sys.stderr)
        return 1
    failed = False
    for path in dockerfiles:
        for image in unpinned(path.read_text()):
            print(f'{path.relative_to(ROOT)}: base image {image} must be pinned as name:tag@sha256:<digest>', file=sys.stderr)
            failed = True
    if not failed:
        print(f'{len(dockerfiles)} Dockerfiles pin every base image by tag and digest.')
    return 1 if failed else 0


if __name__ == '__main__':
    sys.exit(main())
