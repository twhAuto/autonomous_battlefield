#!/usr/bin/env python3
"""Require Dockerfile base images, CI tool images and Kubernetes manifest images to be pinned by digest.

A tag alone can move to different content between pulls, and a local `docker tag` on a
shared agent can replace a cached one, so:

- each Dockerfile FROM names its base image by digest only (SonarQube's docker:S8431
  rejects tag and digest together, because Docker ignores the tag), under a
  `# Base image: name:tag` comment recording the tag the digest came from;
- each `*_IMAGE` in scripts/ci/tool-versions.env and each third-party `image:` in the
  infra/deploy manifests is `name:tag@sha256:<digest>`. Repository-built images are
  replaced with verified digests at deploy time.

The refresh procedure in the supply-chain runbook resolves the recorded tags again.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PINNED = re.compile(r'^([a-z0-9][a-z0-9._/-]*)@sha256:[0-9a-f]{64}$')
NOTED = re.compile(r'^# Base image: ([a-z0-9][a-z0-9._/-]*):[A-Za-z0-9._-]+$')
TAGGED = re.compile(r'^[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$')
TOOL_IMAGE = re.compile(r'^[A-Z0-9_]+_IMAGE=(.*)$')
WORKLOAD_IMAGE = re.compile(r'^\s*(?:-\s*)?image:\s*(\S+)\s*$')
REPOSITORY_BUILT = 'autonomous-battlefield-'


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


def unpinned_tools(environment: str) -> list[str]:
    """Return the tool-versions.env image values that are not name:tag@sha256:<digest>."""
    values = (match.group(1) for match in map(TOOL_IMAGE.match, environment.splitlines()) if match)
    return [value for value in values if not TAGGED.match(value)]


def unpinned_workloads(manifest: str) -> list[str]:
    """Return the third-party manifest images that are not name:tag@sha256:<digest>."""
    images = (match.group(1).strip('"\'') for match in map(WORKLOAD_IMAGE.match, manifest.splitlines()) if match)
    return [image for image in images if not image.startswith(REPOSITORY_BUILT) and not TAGGED.match(image)]


def self_test() -> None:
    digest = 'sha256:' + 'a' * 64
    noted = f'# Base image: golang:1.26\nFROM golang@{digest} AS build\n'
    assert unpinned(noted + 'FROM build\nFROM scratch\n') == []
    assert unpinned('FROM alpine:3.23\n') == ['alpine:3.23']
    assert unpinned(f'FROM golang@{digest}\n') == [f'golang@{digest}'], 'tag comment required'
    assert unpinned(f'# Base image: node:24\nFROM golang@{digest}\n') == [f'golang@{digest}'], 'comment names another image'
    assert unpinned(f'# Base image: golang:1.26\nFROM golang:1.26@{digest}\n') == [f'golang:1.26@{digest}'], 'tag and digest together'
    assert unpinned('# Base image: alpine:3\nFROM alpine@sha256:short AS x\n') == ['alpine@sha256:short']
    tagged = f'ghcr.io/sigstore/cosign/cosign:v3.0.2@{digest}'
    assert unpinned_tools(f'# Tools\nGOVULNCHECK_VERSION=v1.1.4\nCOSIGN_IMAGE={tagged}\n') == []
    assert unpinned_tools('SYFT_IMAGE=anchore/syft:v1.33.0\n') == ['anchore/syft:v1.33.0']
    assert unpinned_tools(f'CRANE_IMAGE=crane@{digest}\nK6_IMAGE=\n') == [f'crane@{digest}', ''], 'tag required'
    assert unpinned_workloads(f'      - image: "{tagged}"\n          image: autonomous-battlefield-backend:dev\n  image:\n') == []
    assert unpinned_workloads('          image: postgres:17-alpine\n') == ['postgres:17-alpine']


CHECKS = (
    ('*/Dockerfile*', unpinned, 'base image {} must be name@sha256:<digest> under a "# Base image: name:tag" comment'),
    ('scripts/ci/tool-versions.env', unpinned_tools, 'tool image {!r} must be name:tag@sha256:<digest>'),
    ('infra/deploy/**/*.y*ml', unpinned_workloads, 'third-party image {} must be name:tag@sha256:<digest>'),
)


def main() -> int:
    self_test()
    failed = False
    for pattern, check, message in CHECKS:
        paths = sorted(ROOT.glob(pattern))
        if not paths:
            print(f'No files match {pattern}.', file=sys.stderr)
            return 1
        for path in paths:
            for image in check(path.read_text()):
                print(f'{path.relative_to(ROOT)}: {message.format(image)}', file=sys.stderr)
                failed = True
    if not failed:
        print('Dockerfile base images, CI tool images and third-party workload images are pinned by digest.')
    return 1 if failed else 0


if __name__ == '__main__':
    sys.exit(main())
