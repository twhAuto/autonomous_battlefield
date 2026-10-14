#!/usr/bin/env python3
"""Keep the assurance pipeline's modes, its stages and its Job DSL definition in step.

A job runs with the parameter definition it stored at its previous build, so a
retired mode can still arrive. The pipeline must reject any MODE outside its own
list instead of skipping every stage and reporting success.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PIPELINE = ROOT / 'infra/jenkins/Jenkinsfile.assurance'
DSL = ROOT / 'infra/jenkins/assurance.groovy'


def quoted(text: str) -> list[str]:
    return re.findall(r"'([^']+)'", text)


def problems(pipeline: str, dsl: str) -> list[str]:
    found = []
    declared = re.search(r'^def MODES = \[([^\]]*)\]', pipeline, re.M)
    modes = quoted(declared.group(1)) if declared else []
    if not modes:
        found.append('Jenkinsfile.assurance must declare its modes once as `def MODES = [...]`')
    if 'choices: MODES' not in pipeline:
        found.append('the MODE parameter must take its choices from MODES')
    if not re.search(r'if \(!\(params\.MODE in MODES\)\) \{\s*error\(', pipeline):
        found.append('the pipeline must fail on a MODE outside MODES')
    staged = set(re.findall(r"params\.MODE == '([^']+)'", pipeline))
    for mode in modes:
        if mode not in staged:
            found.append(f'mode {mode!r} has no stage')
    for mode in staged - set(modes):
        found.append(f'a stage handles undeclared mode {mode!r}')
    job = re.search(r"choiceParam\('MODE', \[([^\]]*)\]\)", dsl)
    if not job or quoted(job.group(1)) != modes:
        found.append('assurance.groovy must offer exactly the pipeline MODES, in order')
    if re.search(r'\btriggers\s*\{', dsl):
        found.append('the assurance job must not be scheduled: production is not kept running')
    return found


def self_test() -> None:
    pipeline = ("def MODES = ['offline', 'load']\nchoice(name: 'MODE', choices: MODES)\n"
                "if (!(params.MODE in MODES)) {\n error('x')\n}\n"
                "when { expression { params.MODE == 'offline' } }\nwhen { expression { params.MODE == 'load' } }\n")
    dsl = "parameters { choiceParam('MODE', ['offline', 'load']) }\n"
    assert problems(pipeline, dsl) == []
    assert problems(pipeline.replace("if (!(params.MODE in MODES))", "if (false)"), dsl)
    assert problems(pipeline.replace("params.MODE == 'load'", "params.MODE == 'nightly'"), dsl)
    assert problems(pipeline, dsl.replace("'load'", "'nightly'"))
    assert problems(pipeline, dsl + "triggers { cron('H 1 * * *') }\n")


def main() -> int:
    self_test()
    found = problems(PIPELINE.read_text(), DSL.read_text())
    for problem in found:
        print(problem, file=sys.stderr)
    if not found:
        print('Assurance pipeline, stages and job definition share one mode list, and the job has no schedule.')
    return 1 if found else 0


if __name__ == '__main__':
    sys.exit(main())
