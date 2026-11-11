#!/usr/bin/env python3
"""Keep GitHub Actions workflows safe to run on pull-request code.

Workflows run code from pull requests, so every file under .github/workflows must:

- trigger only on `pull_request`, which gives the run a read-only token and no secrets;
  `pull_request_target`, `workflow_run` and `workflow_dispatch` run with the base
  repository's privileges;
- declare `permissions: contents: read` for the whole workflow and grant no write scope;
- read no secret;
- run only on GitHub-hosted runners, never on a self-hosted one;
- name each action by its full commit SHA, because a tag can be moved to other code.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PINNED_ACTION = re.compile(r'^[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+@[0-9a-f]{40}$')
HOSTED_RUNNER = re.compile(r'^ubuntu-[0-9]{2}\.[0-9]{2}$')
PRIVILEGED_TRIGGER = re.compile(r'^\s*-?\s*(pull_request_target|workflow_run|workflow_dispatch)\b')


def value(line: str, key: str) -> str | None:
    """Return the scalar after `key:` on a line, without quotes or a trailing comment."""
    match = re.match(rf'^\s*(?:-\s*)?{key}:\s*(.*)$', line)
    return None if match is None else match.group(1).split(' #')[0].strip().strip('"\'')


def problems(workflow: str) -> list[str]:
    found = []
    lines = [line for line in workflow.splitlines() if not line.lstrip().startswith('#')]
    if not re.search(r'^on:\n  pull_request:', workflow, re.MULTILINE):
        found.append('must trigger on pull_request')
    if not re.search(r'^permissions:\n  contents: read\n(?!  )', workflow, re.MULTILINE):
        found.append('must declare workflow-level permissions of contents: read only')
    for line in lines:
        trigger = PRIVILEGED_TRIGGER.match(line)
        if trigger:
            found.append(f'privileged trigger {trigger.group(1)}')
        if re.search(r':\s*write\b', line):
            found.append(f'write permission: {line.strip()}')
        if 'secrets.' in line or 'secrets[' in line:
            found.append(f'secret reference: {line.strip()}')
        action = value(line, 'uses')
        if action is not None and not action.startswith('./') and not PINNED_ACTION.match(action):
            found.append(f'action {action} must be pinned to a full commit SHA')
        runner = value(line, 'runs-on')
        if runner is not None and not HOSTED_RUNNER.match(runner):
            found.append(f'runner {runner} must be a GitHub-hosted ubuntu image')
    return found


def self_test() -> None:
    sha = 'a' * 40
    good = ('name: x\n\non:\n  pull_request:\n    branches: [dev]\n\npermissions:\n  contents: read\n\njobs:\n  a:\n'
            f'    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/checkout@{sha} # v1\n      - run: ./x.sh\n')
    assert problems(good) == [], problems(good)
    for change, expected in (
        (('  pull_request:', '  pull_request_target:'), 'privileged trigger pull_request_target'),
        (('    branches: [dev]', '    branches: [dev]\n  workflow_dispatch:'), 'privileged trigger workflow_dispatch'),
        (('  contents: read', '  contents: read\n  packages: write'), 'write permission'),
        (('permissions:\n  contents: read\n\n', ''), 'workflow-level permissions'),
        (('    runs-on: ubuntu-24.04', '    permissions:\n      contents: write\n    runs-on: ubuntu-24.04'), 'write permission'),
        ((f'actions/checkout@{sha}', 'actions/checkout@v7'), 'pinned to a full commit SHA'),
        ((f'actions/checkout@{sha}', f'actions/checkout@{sha[:12]}'), 'pinned to a full commit SHA'),
        (('ubuntu-24.04', 'self-hosted'), 'GitHub-hosted'),
        (('ubuntu-24.04', '[self-hosted, linux]'), 'GitHub-hosted'),
        (('- run: ./x.sh', '- run: ./x.sh\n        env:\n          T: ${{ secrets.TOKEN }}'), 'secret reference'),
    ):
        bad = good.replace(*change)
        assert bad != good, change
        assert any(expected in problem for problem in problems(bad)), (change, problems(bad))


def main() -> int:
    self_test()
    paths = sorted((ROOT / '.github/workflows').glob('*.y*ml'))
    if not paths:
        print('No workflow found under .github/workflows.', file=sys.stderr)
        return 1
    failed = False
    for path in paths:
        for problem in problems(path.read_text()):
            print(f'{path.relative_to(ROOT)}: {problem}', file=sys.stderr)
            failed = True
    if failed:
        return 1
    print(f'{len(paths)} GitHub Actions workflow(s) use read-only pull_request runs, hosted runners and SHA-pinned actions.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
