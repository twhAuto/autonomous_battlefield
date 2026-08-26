#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ "$#" -ne 2 ]]; then
  echo "Usage: $0 <previous-successful-git-sha> <current-git-sha>" >&2
  exit 1
fi

base_revision="$1"
current_revision="$2"

if [[ ! "${current_revision}" =~ ^[0-9a-f]{40}$ ]] \
  || ! git -C "${project_dir}" cat-file -e "${current_revision}^{commit}" 2>/dev/null; then
  echo "The current revision must be an available full Git commit SHA." >&2
  exit 1
fi

if [[ ! "${base_revision}" =~ ^[0-9a-f]{40}$ ]] \
  || ! git -C "${project_dir}" cat-file -e "${base_revision}^{commit}" 2>/dev/null; then
  echo "all"
  exit 0
fi

infrastructure=0
migration=0
backend=0
frontend=0
application=0
observability=0
gateway=0
all=0
unclassified_files=()

# Keep these rules synchronized with "Change-to-deployment mapping" in
# DEPLOYMENT.md and with scripts/ci/test-deployment-components.sh.
while IFS= read -r -d '' changed_file; do
  case "${changed_file}" in
    *.md|LICENSE|LICENSE.*|.gitignore)
      ;;
    quality/*|sonar-project.properties)
      ;;
    scripts/ci/coverage-check-summary.sh|scripts/ci/lint-go.sh|scripts/ci/test-deployment-components.sh|scripts/ci/test-go-coverage.sh)
      ;;
    backend/*)
      backend=1
      migration=1
      application=1
      ;;
    frontend/*)
      frontend=1
      application=1
      ;;
    deploy/base/infrastructure/*|deploy/overlays/local/infrastructure/*)
      infrastructure=1
      ;;
    deploy/base/migration/*|deploy/overlays/local/migration/*)
      migration=1
      ;;
    deploy/base/application/*|deploy/overlays/local/application/*)
      application=1
      ;;
    deploy/overlays/local/observability/*|deploy/overlays/local/logging-collector/*)
      observability=1
      ;;
    deploy/overlays/local/gateway/*)
      gateway=1
      ;;
    VERSION)
      backend=1
      frontend=1
      migration=1
      application=1
      ;;
    scripts/ci/build-images.sh|scripts/ci/scan-images.sh|scripts/cd/*|scripts/lib/*)
      all=1
      ;;
    deploy/kind.yaml)
      all=1
      ;;
    *)
      all=1
      unclassified_files+=("${changed_file}")
      ;;
  esac
done < <(git -C "${project_dir}" diff \
  --name-only \
  --no-renames \
  -z \
  "${base_revision}" \
  "${current_revision}")

if [[ "${all}" == "1" ]]; then
  if ((${#unclassified_files[@]} > 0)); then
    printf 'Unclassified changed path; selecting all deployment components: %s\n' \
      "${unclassified_files[@]}" >&2
  fi
  echo "all"
  exit 0
fi

components=()
for component in infrastructure migration backend frontend application observability gateway; do
  if [[ "${!component}" == "1" ]]; then
    components+=("${component}")
  fi
done

if ((${#components[@]} == 0)); then
  echo "none"
  exit 0
fi

IFS=,
echo "${components[*]}"
