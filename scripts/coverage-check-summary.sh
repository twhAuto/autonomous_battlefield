#!/usr/bin/env bash

set -uo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
go_summary_file="${GO_COVERAGE_SUMMARY_FILE:-${project_dir}/backend/coverage-total.txt}"
frontend_summary_file="${FRONTEND_COVERAGE_SUMMARY_FILE:-${project_dir}/frontend/coverage/coverage-summary.json}"
summary_complete=1

go_coverage="Unavailable"
if [[ -f "${go_summary_file}" ]]; then
  candidate="$(tr -d '[:space:]' <"${go_summary_file}")"
  if [[ "${candidate}" =~ ^([0-9]+)(\.[0-9]+)?$ ]]; then
    go_coverage="${candidate}%"
  else
    summary_complete=0
  fi
else
  summary_complete=0
fi

frontend_statements="Unavailable"
frontend_branches="Unavailable"
frontend_functions="Unavailable"
frontend_lines="Unavailable"
if [[ -f "${frontend_summary_file}" ]]; then
  if frontend_metrics="$(node -e '
    const fs = require("node:fs");
    const report = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const metricNames = ["statements", "branches", "functions", "lines"];
    const metrics = metricNames.map((name) => report.total?.[name]?.pct);
    if (metrics.some((value) => typeof value !== "number" || !Number.isFinite(value))) {
      process.exit(1);
    }
    process.stdout.write(metrics.join("|"));
  ' "${frontend_summary_file}" 2>/dev/null)"; then
    IFS='|' read -r frontend_statements frontend_branches frontend_functions frontend_lines <<<"${frontend_metrics}"
    frontend_statements="${frontend_statements}%"
    frontend_branches="${frontend_branches}%"
    frontend_functions="${frontend_functions}%"
    frontend_lines="${frontend_lines}%"
  else
    summary_complete=0
  fi
else
  summary_complete=0
fi

printf '%s\n' \
  'Coverage reports from this pull request build:' \
  '' \
  '| Component | Statements | Branches | Functions | Lines |' \
  '| --- | ---: | ---: | ---: | ---: |' \
  "| Backend (Go) | ${go_coverage} | — | — | — |" \
  "| Frontend | ${frontend_statements} | ${frontend_branches} | ${frontend_functions} | ${frontend_lines} |" \
  '' \
  'Go reports statement coverage. The full test output is available from the Jenkins details link.'

if ((summary_complete == 0)); then
  exit 1
fi
