#!/usr/bin/env bash
# Checks changed-path classification and component selection, including mixed changes,
# deletions, and renames.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield-component-test.XXXXXX")"

cleanup() {
  rm -rf -- "${runtime_dir}"
}
trap cleanup EXIT INT TERM

git -C "${runtime_dir}" init --quiet
git -C "${runtime_dir}" config user.email test@example.invalid
git -C "${runtime_dir}" config user.name "Deployment component test"

mkdir -p \
  "${runtime_dir}/deploy/langfuse" \
  "${runtime_dir}/backend" \
  "${runtime_dir}/frontend" \
  "${runtime_dir}/deploy/overlays/local/logging-collector" \
  "${runtime_dir}/deploy/overlays/local/observability" \
  "${runtime_dir}/legacy" \
  "${runtime_dir}/tests/performance" \
  "${runtime_dir}/scripts/ci" \
  "${runtime_dir}/scripts/cd"
cp "${project_dir}/scripts/cd/detect-deployment-components.sh" \
  "${runtime_dir}/scripts/cd/detect-deployment-components.sh"
chmod +x "${runtime_dir}/scripts/cd/detect-deployment-components.sh"
printf 'initial\n' >"${runtime_dir}/README.md"
printf '0.1.0\n' >"${runtime_dir}/VERSION"
printf 'legacy\n' >"${runtime_dir}/legacy/obsolete.conf"
printf 'package legacy\n' >"${runtime_dir}/backend/legacy.go"
printf '// performance scenario\n' >"${runtime_dir}/tests/performance/seed.js"
printf '# coverage wrapper\n' >"${runtime_dir}/scripts/ci/test-go-coverage.sh"
git -C "${runtime_dir}" add .
git -C "${runtime_dir}" commit --quiet -m initial
base_revision="$(git -C "${runtime_dir}" rev-parse HEAD)"

assert_detection() {
  local expected="$1"
  local changed_path="$2"
  local expect_warning="${3:-0}"
  local actual
  local error_file="${runtime_dir}/classifier-error.log"

  mkdir -p "$(dirname -- "${runtime_dir}/${changed_path}")"
  printf 'changed %s\n' "${changed_path}" >>"${runtime_dir}/${changed_path}"
  git -C "${runtime_dir}" add "${changed_path}"
  git -C "${runtime_dir}" commit --quiet -m "change ${changed_path}"
  actual="$("${runtime_dir}/scripts/cd/detect-deployment-components.sh" \
    "${base_revision}" \
    "$(git -C "${runtime_dir}" rev-parse HEAD)" 2>"${error_file}")"

  if [[ "${actual}" != "${expected}" ]]; then
    echo "Expected ${expected} for ${changed_path}, got ${actual}." >&2
    exit 1
  fi

  if [[ "${expect_warning}" == "1" ]] \
    && ! grep -Fq "Unclassified changed path; selecting all deployment components: ${changed_path}" \
      "${error_file}"; then
    echo "Expected an unclassified-path warning for ${changed_path}." >&2
    exit 1
  fi

  git -C "${runtime_dir}" reset --hard --quiet "${base_revision}"
}

assert_mixed_detection() {
  local expected="$1"
  shift
  local changed_path
  local actual

  for changed_path in "$@"; do
    mkdir -p "$(dirname -- "${runtime_dir}/${changed_path}")"
    printf 'changed %s\n' "${changed_path}" >>"${runtime_dir}/${changed_path}"
    git -C "${runtime_dir}" add "${changed_path}"
  done
  git -C "${runtime_dir}" commit --quiet -m "mixed path change"
  actual="$("${runtime_dir}/scripts/cd/detect-deployment-components.sh" \
    "${base_revision}" \
    "$(git -C "${runtime_dir}" rev-parse HEAD)" 2>/dev/null)"

  if [[ "${actual}" != "${expected}" ]]; then
    echo "Expected ${expected} for a mixed path change, got ${actual}." >&2
    exit 1
  fi

  git -C "${runtime_dir}" reset --hard --quiet "${base_revision}"
}

assert_deletion() {
  local expected="$1"
  local deleted_path="$2"
  local actual

  git -C "${runtime_dir}" rm --quiet "${deleted_path}"
  git -C "${runtime_dir}" commit --quiet -m "delete ${deleted_path}"
  actual="$("${runtime_dir}/scripts/cd/detect-deployment-components.sh" \
    "${base_revision}" \
    "$(git -C "${runtime_dir}" rev-parse HEAD)" 2>/dev/null)"

  if [[ "${actual}" != "${expected}" ]]; then
    echo "Expected ${expected} after deleting ${deleted_path}, got ${actual}." >&2
    exit 1
  fi

  git -C "${runtime_dir}" reset --hard --quiet "${base_revision}"
}

assert_rename() {
  local expected="$1"
  local old_path="$2"
  local new_path="$3"
  local actual

  mkdir -p "$(dirname -- "${runtime_dir}/${new_path}")"
  git -C "${runtime_dir}" mv "${old_path}" "${new_path}"
  git -C "${runtime_dir}" commit --quiet -m "rename ${old_path}"
  actual="$("${runtime_dir}/scripts/cd/detect-deployment-components.sh" \
    "${base_revision}" \
    "$(git -C "${runtime_dir}" rev-parse HEAD)" 2>/dev/null)"

  if [[ "${actual}" != "${expected}" ]]; then
    echo "Expected ${expected} after renaming ${old_path}, got ${actual}." >&2
    exit 1
  fi

  git -C "${runtime_dir}" reset --hard --quiet "${base_revision}"
}

assert_detection "frontend,application" "frontend/src/app/App.tsx"
assert_detection "migration,backend,application" "backend/internal/app/app.go"
assert_detection "observability" "deploy/overlays/local/observability/prometheus/prometheus.yml"
assert_detection "observability" "deploy/langfuse/collector-config.yaml"
assert_detection "observability" "deploy/overlays/local/logging-collector/fluent-bit.conf"
assert_detection "none" "README.md"
assert_detection "none" "docs/architecture.md"
assert_detection "none" "quality/sonarqube/rules.properties"
assert_detection "none" "scripts/test/test-go-coverage.sh"
assert_detection "none" "scripts/test/test-command-wrappers.sh"
assert_detection "none" "tests/performance/k6-smoke.js"
assert_detection "none" "scripts/ci/test-go-coverage.sh"
for wrapper in fuzz-smoke test-ai-evals test-backend test-go-race test-kind-deployment with-test-postgres; do
  assert_detection "none" "scripts/ci/${wrapper}.sh"
done
assert_detection "none" "backend/internal/ai/providers/providers_test.go"
assert_detection "none" "frontend/src/features/factions/api/factions.test.ts"
assert_detection "none" "frontend/src/app/App.test.tsx"
assert_detection "none" "frontend/src/test/setup.ts"
assert_mixed_detection "migration,backend,application" "backend/internal/app/app.go" "backend/internal/app/app_test.go"
assert_mixed_detection "frontend,application" "frontend/src/app/App.tsx" "frontend/src/app/App.test.tsx"
assert_mixed_detection "frontend,application" "frontend/src/app/App.tsx" "tests/performance/k6-smoke.js"
assert_detection "all" "scripts/ci/build-images.sh"
assert_detection "all" "scripts/ci/with-ghcr-auth.sh" 1
assert_detection "all" "scripts/cd/deploy-kind.sh"
assert_detection "all" "scripts/cd/lib/kind-deployment.sh"
assert_detection "all" "platform/controllers/reconcile.sh" 1
assert_mixed_detection "all" \
  "frontend/src/app/App.tsx" \
  "platform/controllers/reconcile.sh"
assert_deletion "all" "legacy/obsolete.conf"
assert_deletion "migration,backend,application" "backend/legacy.go"
assert_rename "all" "README.md" "platform/config.yaml"
assert_rename "none" "README.md" "tests/README.md"
assert_rename "migration,backend,application" "backend/legacy.go" "tests/legacy.go"
assert_rename "migration,backend,application" "backend/legacy.go" "backend/legacy_test.go"
assert_rename "frontend,application" "tests/performance/seed.js" "frontend/src/seed.tsx"
assert_rename "frontend,application" "tests/performance/seed.js" "frontend/src/seed.js"
assert_rename "none" "scripts/ci/test-go-coverage.sh" "scripts/test/test-go-coverage.sh"

unknown_base="0000000000000000000000000000000000000000"
actual="$("${runtime_dir}/scripts/cd/detect-deployment-components.sh" \
  "${unknown_base}" \
  "${base_revision}")"
if [[ "${actual}" != "all" ]]; then
  echo "Expected an unavailable baseline to select all components, got ${actual}." >&2
  exit 1
fi

# shellcheck source=scripts/lib/deployment-components.sh
source "${project_dir}/scripts/lib/deployment-components.sh"

deployment_components_select "backend" test
if ! deployment_component_selected backend \
  || ! deployment_component_selected migration \
  || ! deployment_component_selected application \
  || deployment_component_selected frontend; then
  echo "Backend selection did not include the expected deployment dependencies." >&2
  exit 1
fi

deployment_components_select "frontend,observability" test
if ! deployment_component_selected frontend \
  || ! deployment_component_selected application \
  || ! deployment_component_selected observability \
  || deployment_component_selected backend; then
  echo "Frontend and observability selection did not produce the expected components." >&2
  exit 1
fi

for invalid in unknown '' , backend, ,frontend backend,,frontend 'backend frontend' $'backend\nfrontend' all,frontend; do
  if deployment_components_select "${invalid}" test 2>/dev/null; then
    echo "Invalid component selection unexpectedly succeeded: ${invalid}" >&2
    exit 1
  fi
done

deployment_components_select all test
for component in "${DEPLOYMENT_COMPONENT_ORDER[@]}"; do
  deployment_component_selected "${component}"
done
deployment_components_select none test
if deployment_components_any_selected; then
  echo "Selecting none retained previous components." >&2
  exit 1
fi

echo "Deployment component detection and selection tests passed."
