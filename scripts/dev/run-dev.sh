#!/usr/bin/env bash
# Starts the local database and development processes, tracks child processes, and shuts
# them down together on exit.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
backend_dir="${project_dir}/backend"
frontend_dir="${project_dir}/frontend"
engine_worker_id="${ENGINE_WORKER_ID:-engine-local-1}"
start_postgres="${START_POSTGRES:-1}"
start_agent="${START_AGENT:-0}"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield.XXXXXX")"
pids=()

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if ((${#pids[@]} > 0)); then
    kill "${pids[@]}" 2>/dev/null || true
    wait "${pids[@]}" 2>/dev/null || true
  fi

  rm -rf -- "${runtime_dir}"
  exit "${exit_code}"
}

trap cleanup EXIT INT TERM

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    exit 1
  fi
}

# Read only recognized literal environment values; never execute an env file as shell.
if [[ -f "${backend_dir}/.env" ]]; then
  while IFS='=' read -r key value; do
    case "${key}" in
      BATTLEFIELD_*|OPENAI_API_KEY|OPENAI_MODEL|GEMINI_API_KEY|GEMINI_MODEL|DATABASE_URL)
        [[ "${key}" =~ ^[A-Z_][A-Z0-9_]*$ ]] || continue
        if [[ ! -v "${key}" ]]; then export "${key}=${value%$'\r'}"; fi
        ;;
    esac
  done <"${backend_dir}/.env"
fi
[[ "${BATTLEFIELD_ENV:-dev}" == dev ]] || { echo 'run-dev is a development-only launcher.' >&2; exit 1; }
export BATTLEFIELD_ENV=dev
if [[ -z "${BATTLEFIELD_AUTH_MODE:-}" && -z "${BATTLEFIELD_AUTH_ENABLED:-}" ]]; then
  echo 'Set BATTLEFIELD_AUTH_MODE=local-password (with credentials) or explicitly disabled-development in backend/.env.' >&2
  exit 1
fi

require_command go
require_command npm

if [[ "${start_postgres}" == "1" ]]; then
	if [[ -z "${BATTLEFIELD_POSTGRES_PASSWORD:-}" && -f "${backend_dir}/.env" ]]; then
		while IFS='=' read -r key value; do
			if [[ "${key}" == "BATTLEFIELD_POSTGRES_PASSWORD" ]]; then
				export BATTLEFIELD_POSTGRES_PASSWORD="${value%$'\r'}"
				break
			fi
		done < "${backend_dir}/.env"
	fi
	if [[ -z "${BATTLEFIELD_POSTGRES_PASSWORD:-}" ]]; then
		echo "Set BATTLEFIELD_POSTGRES_PASSWORD in backend/.env or the shell before starting local PostgreSQL." >&2
		exit 1
	fi
	require_command docker
  docker compose -f "${backend_dir}/compose.yaml" up -d postgres

  echo "Waiting for PostgreSQL..."
  postgres_ready=0
  for _ in {1..30}; do
    if docker compose -f "${backend_dir}/compose.yaml" exec -T postgres \
      pg_isready -U battlefield -d battlefield >/dev/null 2>&1; then
      postgres_ready=1
      break
    fi
    sleep 1
  done

  if [[ "${postgres_ready}" != "1" ]]; then
    echo "PostgreSQL did not become ready within 30 seconds." >&2
    exit 1
  fi
fi

database_url="${DATABASE_URL:-postgres://battlefield:${BATTLEFIELD_POSTGRES_PASSWORD:-}@localhost:5432/battlefield?sslmode=disable}"
export DATABASE_URL="${database_url}"
export ENGINE_WORKER_ID="${engine_worker_id}"

echo "Applying database migrations..."
(
  cd "${backend_dir}"
  go run ./cmd/server -migrate
)

echo "Building backend processes..."
(
  cd "${backend_dir}"
  go build -o "${runtime_dir}/api" ./cmd/server
  go build -o "${runtime_dir}/engine" ./cmd/engine
  if [[ "${start_agent}" == "1" ]]; then
    go build -o "${runtime_dir}/agent" ./cmd/agent
    go build -o "${runtime_dir}/mcp" ./cmd/mcp
  fi
)

if [[ ! -d "${frontend_dir}/node_modules" ]]; then
  echo "Installing frontend dependencies..."
  (cd "${frontend_dir}" && npm ci)
fi

echo "Starting API at http://localhost:8080"
(cd "${backend_dir}" && exec "${runtime_dir}/api") &
pids+=("$!")

if [[ "${start_agent}" == "1" ]]; then
  echo "Starting MCP decision service at http://localhost:8084/mcp"
  (cd "${backend_dir}" && exec "${runtime_dir}/mcp") &
  pids+=("$!")
  echo "Starting provider-neutral agent health endpoint at http://localhost:8083"
  (cd "${backend_dir}" && exec "${runtime_dir}/agent") &
  pids+=("$!")
fi

echo "Starting battle engine health endpoint at http://localhost:8081"
(cd "${backend_dir}" && exec "${runtime_dir}/engine") &
pids+=("$!")

echo "Starting frontend at http://localhost:5173"
(cd "${frontend_dir}" && exec "${frontend_dir}/node_modules/.bin/vite") &
pids+=("$!")

echo "All services are running. Press Ctrl+C to stop them."

set +e
wait -n "${pids[@]}"
exit_code=$?
set -e

if [[ "${exit_code}" -eq 0 ]]; then
  echo "A service stopped; shutting down the remaining services."
else
  echo "A service exited with status ${exit_code}; shutting down the remaining services." >&2
fi

exit "${exit_code}"
