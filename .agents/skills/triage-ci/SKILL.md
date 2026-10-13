---
name: triage-ci
description: Diagnose and fix Autonomous Battlefield Jenkins PR or main pipeline failures and SonarQube quality-gate issues. Use for "why did CI fail", "fix the Jenkins build", "triage Sonar", or a failed coverage, security, build, or deployment gate.
---

# Triage CI

1. Read `docs/developer/ci.md`, `docs/operations/jenkins.md`, and the failing run's exact branch, commit, PR target, stages, console output, and published checks. Match the failing head to the current head before editing. A queued, aborted, or skipped stage is not a passing check.
2. Find the earliest actionable failure and classify it using evidence: code/test regression, threshold or scanner finding, agent/tool/credential problem, external service outage, or pipeline configuration. Inspect downstream failures for shared causes before choosing a fix. Never change coverage thresholds, suppress findings, or claim Sonar is clean from passing local tests.
3. Reproduce the smallest applicable check through the existing `scripts/test/` and `scripts/ci/` entry points; use `with-test-postgres.sh` for backend coverage and `test-deployment.sh` for deployment changes. If `internal/boundaries` fails, a forbidden dependency means fixing the import, not the rule (change a rule only with user approval); a stale component map means regenerating `docs/developer/component-map.md` with the command in its failure message. For Sonar, read and follow [the SonarQube workflow](sonarqube.md) for server access, token handling, issue queries and the PR preview project; never scan unmerged code into the production project.
4. Fix the cause when within the task's scope. Rerun affected checks and inspect a new Jenkins/Sonar result when available. If the failure is environmental, document the failing dependency, evidence, owner/action, and whether a safe retry is warranted; do not disguise it as a source fix.
5. Report the failing run and head, first actionable error, diagnosis, change made, commands/results, and checks still pending or blocked. Do not equate a local reproduction with a green Jenkins gate.
