#!/usr/bin/env bash

set -uo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
go_summary_file="${GO_COVERAGE_SUMMARY_FILE:-${project_dir}/backend/coverage-total.txt}"
frontend_summary_file="${FRONTEND_COVERAGE_SUMMARY_FILE:-${project_dir}/frontend/coverage/coverage-summary.json}"
go_minimum="${GO_COVERAGE_MIN:-80}"
frontend_minimum=80
summary_complete=1

meets_minimum() {
  local actual="$1"
  local minimum="$2"

  awk -v actual="${actual}" -v minimum="${minimum}" \
    'BEGIN { exit !(actual + 0 >= minimum + 0) }'
}

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

coverage_requirements_met=1
if [[ "${go_coverage}" != "Unavailable" ]]; then
  go_actual="${go_coverage%\%}"
  if ! meets_minimum "${go_actual}" "${go_minimum}"; then
    coverage_requirements_met=0
  fi
fi

for frontend_coverage in \
  "${frontend_statements}" \
  "${frontend_branches}" \
  "${frontend_functions}" \
  "${frontend_lines}"; do
  if [[ "${frontend_coverage}" != "Unavailable" ]]; then
    frontend_actual="${frontend_coverage%\%}"
    if ! meets_minimum "${frontend_actual}" "${frontend_minimum}"; then
      coverage_requirements_met=0
    fi
  fi
done

printf '%s\n' \
  'Coverage reports from this pull request build:' \
  '' \
  '| Component | Statements | Branches | Functions | Lines |' \
  '| --- | ---: | ---: | ---: | ---: |' \
  "| Backend (Go) | ${go_coverage} | — | — | — |" \
  "| Frontend | ${frontend_statements} | ${frontend_branches} | ${frontend_functions} | ${frontend_lines} |" \
  '' \
  "Required minimum: ${go_minimum}% for backend statements and ${frontend_minimum}% for every frontend metric." \
  '' \
  'Go reports statement coverage. The full test output is available from the Jenkins details link.'

if ((summary_complete == 0 || coverage_requirements_met == 0)); then
  exit 1
fi
