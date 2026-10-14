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
fixture_dir="${project_dir}/backend/test/performance/query-plans"

# Check the exact harness-owned container and URL before running any migrations or
# fixtures. Do not discover a target from other concurrent test runs.
container="${TEST_POSTGRES_CONTAINER_ID:-}"
harness_url='^postgres://battlefield_test:[0-9a-f]{48}@127\.0\.0\.1:([0-9]+)/battlefield_test\?sslmode=disable$'
if [[ ! "${container}" =~ ^[0-9a-f]{64}$ || ! "${TEST_DATABASE_URL}" =~ ${harness_url} ]]; then
  echo 'Query plans require the URL and container identity from with-test-postgres.sh.' >&2
  exit 1
fi
port="${BASH_REMATCH[1]}"
container_name="$(docker inspect --format '{{.Name}}' "${container}")"
binding="$(docker port "${container}" 5432/tcp)"
if [[ "${container_name}" != /battlefield-ci-postgres-* || "${binding}" != "127.0.0.1:${port}" ]]; then
  echo 'The query-plan database does not match the disposable test container.' >&2
  exit 1
fi

(cd "${project_dir}/backend" && go run github.com/pressly/goose/v3/cmd/goose@v3.27.1 \
  -dir migrations postgres "${TEST_DATABASE_URL}" up >/dev/null)

psql() { docker exec -i "${container}" psql -U battlefield_test -d battlefield_test -v ON_ERROR_STOP=1 -X -q "$@"; }

psql < "${fixture_dir}/seed.sql"
docker exec "${container}" psql -U battlefield_test -d battlefield_test -Atc 'SELECT version()'
psql < "${fixture_dir}/explain.sql"
