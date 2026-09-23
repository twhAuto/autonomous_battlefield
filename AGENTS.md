# Autonomous Battlefield agent guidance

## Branches and pull requests

`dev` is the integration branch for AI-assisted work.

- Unless the user explicitly requests a different base or target, create every
  new AI work branch (features, fixes, refactors, documentation, and maintenance)
  from the latest fetched `origin/dev`, and target its pull request to `dev`.
- Fetch `origin` before branching and inspect the working tree. Preserve existing
  work; do not reset or discard changes to establish the branch base. Use an
  isolated worktree when needed. If fetching fails, report the limitation rather
  than silently treating a stale local `dev` as current.
- Do not base new work on `main`, the current feature branch, or another work
  branch by default. Specify the PR base explicitly (for example, `gh pr create
  --base dev`) rather than relying on the repository's default branch.
- Promotion from `dev` to `main` is a separate release action. Create a PR to
  `main` only when the user requests that promotion or explicitly chooses it as
  the target.
- Run the applicable checks below before opening a PR. Jenkins runs PR
  verification for targets `dev` and `main` on the existing shared
  `battlefield-production` agent. The owner accepts shared-host CI for this small,
  trusted project; do not introduce a separate-agent/VM prerequisite without a
  requested infrastructure change. Run `./scripts/test/test-deployment.sh` for deployment
  changes. Do not treat skipped or queued Jenkins stages as verification.

## Project tracking

The authoritative project backlog is maintained in Notion:

- Project: [Autonomous Battlefield](https://app.notion.com/p/3c1b7a19f8f780aea86efbe505e504a7)
- Backlog: [Autonomous Battlefield Backlog](https://app.notion.com/p/5198cc5bdacf44e08a7393a5040e2e3e)

Notion is the source of truth for task status; do not duplicate the backlog in
`README.md`. When the user asks to plan, select, implement, or track project
work, follow the [track-backlog skill](.agents/skills/track-backlog/SKILL.md)
for the status workflow, completion evidence, and new-ticket fields. If Notion
MCP is unavailable, continue with safe repository work when possible and tell
the user that task statuses could not be synchronized.

## Documentation maintenance

Keep repository documentation synchronized with every implementation change.

- When a change affects runtime components, boundaries, data flows, persistence,
  security, configuration, deployment, or operational behavior, update
  `docs/design/system-architecture.md` and any affected operational guide in the
  same change.
- When a change adds, removes, renames, moves, or materially changes packages,
  entry points, important types, routes, migrations, frontend areas, tests, or
  common development workflows, update `docs/design/codebase-guide.md` in the
  same change.
- Update `README.md` when setup steps, capabilities, commands, ports, or
  documentation entry points change.
- Do not make mechanical documentation edits for an internal change that does
  not alter documented behavior or structure. Instead, verify that the current
  documentation remains accurate.
- Before completing a change, check links, diagrams, examples, file paths, and
  commands in every affected document against the current working tree,
  including uncommitted changes.

## Test coverage quality gate

Implementation work must preserve the repository's merge-blocking coverage
requirements:

- Backend Go statement coverage must be at least 80% in aggregate across the
  module, as reported by the `total:` line from `go tool cover`. The gate is a
  single repository-wide number, not a per-package floor, so thin wiring in
  `cmd/*` and `internal/postgres` may sit below 80% while the module passes.
  Do not read a low per-package figure as a gate failure, and do not treat the
  aggregate as licence to leave new behavior untested.
- Backend integration tests skip themselves unless `TEST_DATABASE_URL` is set,
  so code exercised only by those tests remains uncovered. A coverage run
  without that variable omits database-backed coverage, including in
  `internal/generation`. Always measure through `with-test-postgres.sh` (see
  the commands below) before drawing any conclusion about coverage.
- Frontend statements, branches, functions, and lines must each be at least 80%.
- Add or update meaningful tests for changed behavior. Do not lower thresholds,
  exclude production code from coverage, or add tests that only inflate coverage
  without checking behavior unless the user explicitly approves the tradeoff.

Before declaring relevant implementation work complete, run:

```bash
./scripts/test/with-test-postgres.sh ./scripts/test/test-go-coverage.sh
(cd frontend && npm test -- --coverage)
```

Run the backend command through `with-test-postgres.sh`, which starts a
throwaway PostgreSQL container and exports `TEST_DATABASE_URL` for the wrapped
command. Without `TEST_DATABASE_URL`, invoking `test-go-coverage.sh` directly
skips tests that require the database; it still fails if tests fail or aggregate
coverage falls below 80%. The harness requires Docker. If it cannot run, report
that CI-equivalent coverage was not verified and label any measured coverage
as excluding database-backed tests.

Run the applicable command when a change affects only one side of the
application. For documentation- or infrastructure-only changes, use the checks
appropriate to those files instead. If a required coverage command cannot run,
report that limitation and do not claim the coverage gate passed.

## SonarQube issue workflow

The SonarQube project key is `autonomous-battlefield`. Use
`https://sonarqube.home.arpa` from this workspace. Jenkins logs may show
`http://localhost:9000`, which refers to the Jenkins host, not this workspace.
The dashboard is
`https://sonarqube.home.arpa/dashboard?id=autonomous-battlefield`.
When asked to inspect or fix SonarQube findings:

1. Confirm the server is healthy with
   `curl --fail https://sonarqube.home.arpa/api/system/status`.
   Sandbox DNS/network access may require escalation; a sandbox lookup failure
   alone does not mean the server is down. The server uses a private certificate
   authority. Prefer a trusted CA or a temporary truststore scoped to this server;
   do not disable TLS verification globally.
2. Use `SONAR_TOKEN` if supplied; otherwise read the persistent token from
   `~/.config/sonarqube/token`. The older temporary location
   `/tmp/autonomous-battlefield-sonar-token` is a fallback. Never print the token,
   place it in the repository, add it to command-line arguments, or copy it into
   an application secret file. Keep shell tracing disabled while handling it.
3. Query unresolved findings through
   `/api/issues/search?componentKeys=autonomous-battlefield&resolved=false&ps=500`.
   Authenticate using the token through an in-process HTTP client or a protected
   curl configuration, without exposing it in process arguments. Paginate if
   necessary. Triage every returned issue by rule, file, line, severity, and type
   before editing. Also query
   `/api/qualitygates/project_status?projectKey=autonomous-battlefield` to identify
   the failed conditions; a gate failure does not necessarily mean low coverage.
4. Fix the underlying behavior or structure. Do not suppress rules, add broad
   exclusions, weaken the quality profile, or mark issues resolved manually
   unless the user explicitly requests and justifies that tradeoff.
5. Run the repository checks required by the changed files, including the
   coverage commands above. Generate both coverage reports before a full scan.
6. Run SonarScanner from the repository root with `SONAR_TOKEN` in the
   environment, `-Dsonar.host.url=https://sonarqube.home.arpa`, and
   `-Dsonar.qualitygate.wait=true`. Never upload unmerged code to the production
   `autonomous-battlefield` project. For an open PR, set `SONAR_SCANNER_HOME` and
   use `scripts/ci/run-sonarqube-pr-gate.sh PR_NUMBER origin/dev`; it seeds and
   scans that PR's isolated preview project. If the scanner is absent from
   `PATH`, check `~/jenkins-agent/tools/hudson.plugins.sonar.SonarRunnerInstallation/SonarScanner/bin/sonar-scanner`.
   For a private CA, configure `sonar.scanner.truststorePath` and
   `sonar.scanner.truststorePassword` for a temporary PKCS12 truststore outside
   the repository. Do not pass the token through `-Dsonar.token`.
   A scan uploads repository source and analysis to this server. Honor any
   existing user authorization for that upload; do not ask again within the
   same authorized task. If automatic approval review rejects it, explain the
   rejection and obtain the explicit authorization it requires before retrying.
7. Query unresolved issues again after the analysis completes and compare the
   result with the original issue set. Query the PR preview key for unmerged work
   and the production key for merged `main`. Confirm the quality-gate status
   separately.
   Report remaining issues, failed conditions, or an inability to run the scanner
   instead of claiming SonarQube is clean. Passing local tests alone does not
   verify the SonarQube gate.

Preserve `~/.config/sonarqube/token` for future work. Remove task-created temporary
credentials and truststores when no longer needed; do not delete the persistent
token during cleanup.

## Cross-review between Codex and Claude

For meaningful agent-authored PR changes, follow
[the cross-review guide](docs/operations/cross-review.md) and the
[raise-pr skill](.agents/skills/raise-pr/SKILL.md). Batch implementation and
checks before one full PR review, then at most one focused follow-up. Use the
existing `scripts/review/cross-review.py` with `--pr NUMBER` so it enforces the
round limit. Stop and ask the user before any extra round; never omit `--pr`,
delete comments, or retry failed model calls to evade the limit.

Trivial wording, formatting, and low-risk preference edits may skip model
review with the reason recorded in the handoff. Do not infer low risk from file
extension or line count: authentication, permissions, deployment behavior, and
substantive logic still need review. Honor an explicit user request to skip
cross-review for a particular PR and state that it was skipped.

Verify findings before fixes, test fixes, and leave unresolved findings visible.
Review is advisory and does not replace CI, authorize merging, or authorize
deployment. Reviewers must not edit the change or invoke another reviewer.
At completed task boundaries, report branch/PR/head, goal, checks, unresolved
findings, and next step. Do not add usage ledgers or cost-report comments.
