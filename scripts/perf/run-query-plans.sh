#!/usr/bin/env bash
# Seeds a disposable PostgreSQL with ~10k battles and 120k events, then prints
# EXPLAIN (ANALYZE, BUFFERS) for battle history and event pagination, including
# the generic plans that pgx's cached prepared statements switch to.
#
# Run through the throwaway database harness, never against a real database:
#   ./scripts/test/with-test-postgres.sh ./scripts/perf/run-query-plans.sh
#
# explain.sql copies the production queries from backend/internal/battle/repo
# (history.go ListBattles, battles.go ListBattleEvents); update it with them.

set -Eeuo pipefail

: "${TEST_DATABASE_URL:?Run through scripts/test/with-test-postgres.sh}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
project_dir="$(cd "${script_dir}/../.." && pwd)"
fixture_dir="${project_dir}/tests/performance/query-plans"

(cd "${project_dir}/backend" && go run github.com/pressly/goose/v3/cmd/goose@v3.27.1 \
  -dir migrations postgres "${TEST_DATABASE_URL}" up >/dev/null)

# Several harness runs can be alive at once (e.g. Jenkins on a shared host), so pick
# the container that publishes this URL's port rather than the first name match.
port="$(sed -E 's#^[^@]*@[^:/]+:([0-9]+)/.*$#\1#' <<<"${TEST_DATABASE_URL}")"
[[ "${port}" =~ ^[0-9]+$ ]] || { echo "Cannot read the port from TEST_DATABASE_URL." >&2; exit 1; }
container=""
for candidate in $(docker ps --filter name=battlefield-ci-postgres --format '{{.ID}}'); do
  if docker port "${candidate}" 5432/tcp | grep -qE ":${port}\$"; then
    container="${candidate}"
    break
  fi
done
[[ -n "${container}" ]] || { echo "No test PostgreSQL container publishes port ${port}." >&2; exit 1; }
psql() { docker exec -i "${container}" psql -U battlefield_test -d battlefield_test -v ON_ERROR_STOP=1 -X -q "$@"; }

psql < "${fixture_dir}/seed.sql"
docker exec "${container}" psql -U battlefield_test -d battlefield_test -Atc 'SELECT version()'
psql < "${fixture_dir}/explain.sql"
