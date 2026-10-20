#!/usr/bin/env python3
"""Fail when a tracked Markdown file links to a missing file or heading anchor.

Checks relative links and images, including `#anchor` fragments, against GitHub's
heading slugs (lowercase, punctuation removed, spaces to hyphens, `-1`, `-2` for
repeats). External URLs and links inside code spans, fenced code blocks and HTML
comments are skipped, so placeholders in examples and templates are not links.
"""

import re
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
LINK = re.compile(r'!?\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)')
HEADING = re.compile(r'^(#{1,6})\s+(.*?)\s*#*\s*$')
FENCE = re.compile(r'^\s*(```|~~~)')


def prose_lines(text: str):
    """Yield (line number, line) outside fenced code blocks and HTML comments."""
    fenced = False
    text = re.sub(r'<!--.*?-->', lambda m: '\n' * m.group(0).count('\n'), text, flags=re.S)
    for number, line in enumerate(text.splitlines(), 1):
        if FENCE.match(line):
            fenced = not fenced
            continue
        if not fenced:
            yield number, line


def slug(heading: str) -> str:
    text = re.sub(r'`([^`]*)`', r'\1', heading)
    text = re.sub(r'!?\[([^\]]*)\]\([^)]*\)', r'\1', text)
    text = re.sub(r'<[^>]+>', '', text)
    return re.sub(r'[^\w\- ]', '', text.strip().lower()).replace(' ', '-')


def anchors(text: str) -> set[str]:
    seen, result = {}, set()
    for _, line in prose_lines(text):
        match = HEADING.match(line)
        if match:
            base = slug(match.group(2))
            count = seen.get(base, 0)
            seen[base] = count + 1
            result.add(base if count == 0 else f'{base}-{count}')
    return result


def broken_links(files: list[Path], root: Path) -> list[str]:
    cache, problems = {}, []
    for path in files:
        for number, line in prose_lines(path.read_text(encoding='utf-8')):
            for target in LINK.findall(re.sub(r'`[^`]*`', '', line)):
                if re.match(r'^[a-z][a-z0-9+.-]*:', target, re.I):
                    continue
                file_part, _, fragment = target.partition('#')
                destination = (path.parent / file_part).resolve() if file_part else path
                where = f'{path.relative_to(root)}:{number}'
                if not destination.exists():
                    problems.append(f'{where}: missing target {target}')
                elif fragment and destination.suffix == '.md':
                    if destination not in cache:
                        cache[destination] = anchors(destination.read_text(encoding='utf-8'))
                    if fragment not in cache[destination]:
                        problems.append(f'{where}: missing anchor {target}')
    return problems


def self_test() -> None:
    assert slug('5.2 Strategy decision window') == '52-strategy-decision-window'
    assert slug('The `battlefield_ai_gateway` role') == 'the-battlefield_ai_gateway-role'
    assert slug('AB-144 — Phase 2: owner walkthrough') == 'ab-144--phase-2-owner-walkthrough'
    assert anchors('# A\n## Setup\n## Setup\n```\n# not a heading\n```\n### `main` branch\n') == {'a', 'setup', 'setup-1', 'main-branch'}
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / 'docs').mkdir()
        (root / 'docs' / 'guide.md').write_text('# Guide\n## Run it\n')
        page = root / 'docs' / 'page.md'
        page.write_text(
            '[ok](guide.md#run-it) [self](#page) [web](https://example.com/x#y)\n'
            '# Page\n`[code](missing.md)`\n<!-- [comment](NOTION_URL) -->\n'
            '```\n[fenced](missing.md)\n```\n'
            '[gone](missing.md) [bad](guide.md#nope) ![image](missing.png)\n')
        assert broken_links([page], root) == [
            'docs/page.md:8: missing target missing.md',
            'docs/page.md:8: missing anchor guide.md#nope',
            'docs/page.md:8: missing target missing.png',
        ], broken_links([page], root)


def main() -> int:
    self_test()
    listed = subprocess.run(['git', 'ls-files', '-z', '--', '*.md'], cwd=ROOT,
                            capture_output=True, check=True).stdout.decode().split('\0')
    files = [ROOT / name for name in listed if name and (ROOT / name).is_file()]
    problems = broken_links(files, ROOT)
    for problem in problems:
        print(problem, file=sys.stderr)
    if problems:
        return 1
    print(f'{len(files)} tracked Markdown files have no broken internal links or anchors.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
