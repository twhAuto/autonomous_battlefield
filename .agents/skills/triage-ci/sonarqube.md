# SonarQube issue workflow

Loaded by the [triage-ci skill](SKILL.md) when inspecting or fixing SonarQube findings.

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
   `SONAR_HOST_URL` and use
   `scripts/ci/run-sonarqube-pr-gate.sh PR_NUMBER origin/dev`; it deletes, seeds,
   and scans that PR's isolated preview project. Its reset call uses `curl`, so
   point `CURL_CA_BUNDLE` at the server's CA certificate. If the scanner is absent from
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

