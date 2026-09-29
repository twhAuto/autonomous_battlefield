#!/usr/bin/env bash
# Seeds a disposable PostgreSQL with ~10k battles and 120k events, then prints
# EXPLAIN (ANALYZE, BUFFERS) for battle history and event pagination, including
# the generic plans that pgx's cached prepared statements switch to.
#
# Run through the throwaway database harness, never against a real database:
#   ./scripts/test/with-test-postgres.sh ./scripts/perf/query-plans/run-query-plans.sh
#
# explain.sql copies the production queries from backend/internal/battle/repo
# (history.go ListBattles, battles.go ListBattleEvents); update it with them.

set -Eeuo pipefail

: "${TEST_DATABASE_URL:?Run through scripts/test/with-test-postgres.sh}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
project_dir="$(cd "${script_dir}/../../.." && pwd)"

(cd "${project_dir}/backend" && go run github.com/pressly/goose/v3/cmd/goose@v3.27.1 \
  -dir migrations postgres "${TEST_DATABASE_URL}" up >/dev/null)

container="$(docker ps --filter name=battlefield-ci-postgres --format '{{.Names}}' | head -1)"
[[ -n "${container}" ]] || { echo "Test PostgreSQL container not found." >&2; exit 1; }
psql() { docker exec -i "${container}" psql -U battlefield_test -d battlefield_test -v ON_ERROR_STOP=1 -X -q "$@"; }

psql < "${script_dir}/seed.sql"
docker exec "${container}" psql -U battlefield_test -d battlefield_test -Atc 'SELECT version()'
psql < "${script_dir}/explain.sql"
