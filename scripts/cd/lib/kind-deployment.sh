#!/usr/bin/env bash

# Internal deployment functions. Configuration and runtime state are owned by
# scripts/cd/deploy-kind.sh, which is the supported entry point.

kind_deploy_kube() {
  kubectl --context "${kube_context}" "$@"
}

# Run only in a background subshell: exec makes the tracked PID the kubectl
# process itself, so cancellation does not leave an orphaned rollout watcher.
kind_deploy_wait_rollout() {
  exec kubectl --context "${kube_context}" rollout status "$@"
}

kind_deploy_require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    return 1
  fi
}

kind_deploy_validate_inputs() {
  if [[ "$#" -gt 1 ]]; then
    echo "Usage: ${deploy_command} [full-git-sha]" >&2
    return 1
  fi

  for command in git grep kind kubectl; do
    kind_deploy_require_command "${command}"
  done

  if [[ "${image_source}" != "local" && "${image_source}" != "registry" ]]; then
    echo "IMAGE_SOURCE must be local or registry." >&2
    return 1
  fi
  if [[ "${enforce_supply_chain_policy}" != "0" && "${enforce_supply_chain_policy}" != "1" ]]; then
    echo "ENFORCE_SUPPLY_CHAIN_POLICY must be either 0 or 1." >&2
    return 1
  fi
  if [[ "${image_source}" == "local" ]]; then
    kind_deploy_require_command docker
  fi

  if [[ "${rollback_on_failure}" != "0" && "${rollback_on_failure}" != "1" ]]; then
    echo "ROLLBACK_ON_FAILURE must be either 0 or 1." >&2
    return 1
  fi

  if [[ "${force_restart}" != "0" && "${force_restart}" != "1" ]]; then
    echo "FORCE_RESTART must be either 0 or 1." >&2
    return 1
  fi

  if [[ ! "${rollout_timeout}" =~ ^[1-9][0-9]*s$ ]]; then
    echo "ROLLOUT_TIMEOUT must be a positive number of seconds such as 120s." >&2
    return 1
  fi
  if [[ ! "${observability_rollout_timeout}" =~ ^[1-9][0-9]*s$ ]]; then
    echo "OBSERVABILITY_ROLLOUT_TIMEOUT must be a positive number of seconds such as 900s." >&2
    return 1
  fi

  for image_name in "${backend_image}" "${frontend_image}"; do
    if [[ ! "${image_name}" =~ ^[a-z0-9][a-z0-9._:/-]*$ ]]; then
      echo "Invalid container image name: ${image_name}" >&2
      return 1
    fi
  done

  if [[ ! "${image_tag}" =~ ^[0-9a-f]{40}$ ]]; then
    echo "The image tag must be a full 40-character lowercase Git commit SHA." >&2
    return 1
  fi

  if ! git -C "${project_dir}" cat-file -e "${image_tag}^{commit}" 2>/dev/null; then
    echo "The commit ${image_tag} is not available in this Git repository." >&2
    return 1
  fi

  if [[ "${image_source}" == "local" ]] && deployment_component_selected backend; then
    if [[ ! "${backend_image_tag}" =~ ^[0-9a-f]{40}$ ]] \
      || ! git -C "${project_dir}" cat-file -e "${backend_image_tag}^{commit}" 2>/dev/null; then
      echo "BACKEND_IMAGE_TAG must be an available full Git commit SHA." >&2
      return 1
    fi
  fi

  if [[ "${image_source}" == "local" ]] && deployment_component_selected frontend; then
    if [[ ! "${frontend_image_tag}" =~ ^[0-9a-f]{40}$ ]] \
      || ! git -C "${project_dir}" cat-file -e "${frontend_image_tag}^{commit}" 2>/dev/null; then
      echo "FRONTEND_IMAGE_TAG must be an available full Git commit SHA." >&2
      return 1
    fi
  fi

  if [[ "${image_source}" == "registry" ]]; then
    if deployment_component_selected backend && [[ ! "${backend_image_ref}" =~ ^[a-z0-9][a-z0-9._:/-]+@sha256:[0-9a-f]{64}$ ]]; then
      echo "BACKEND_IMAGE_REF must be a full repository@sha256 reference in registry mode." >&2
      return 1
    fi
    if deployment_component_selected frontend && [[ ! "${frontend_image_ref}" =~ ^[a-z0-9][a-z0-9._:/-]+@sha256:[0-9a-f]{64}$ ]]; then
      echo "FRONTEND_IMAGE_REF must be a full repository@sha256 reference in registry mode." >&2
      return 1
    fi
    if [[ -z "${GHCR_USER:-}" || -z "${GHCR_TOKEN:-}" ]]; then
      echo "GHCR_USER and GHCR_TOKEN are required in registry mode." >&2
      return 1
    fi
  fi
}

kind_deploy_validate_cluster() {
  if ! kind get clusters | grep -Fxq "${cluster_name}"; then
    echo "kind cluster not found: ${cluster_name}" >&2
    echo "Create it with ./scripts/cd/create-kind-cluster.sh" >&2
    return 1
  fi

  if ! kind_deploy_kube get customresourcedefinition/gateways.gateway.networking.k8s.io \
    >/dev/null 2>&1 \
    || ! kind_deploy_kube get customresourcedefinition/envoyproxies.gateway.envoyproxy.io \
      >/dev/null 2>&1; then
    echo "Envoy Gateway and the Gateway API are not installed in ${cluster_name}." >&2
    echo "Install them with ./scripts/cd/create-kind-cluster.sh" >&2
    return 1
  fi

  kind_deploy_kube rollout status deployment/envoy-gateway \
    --namespace envoy-gateway-system \
    --timeout="${rollout_timeout}"
}

kind_deploy_capture_current_images() {
  current_backend_image="$(
    kind_deploy_kube get deployment/battlefield-api \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="api")].image}' \
      2>/dev/null || true
  )"
  current_engine_image="$(
    kind_deploy_kube get deployment/battlefield-engine \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="engine")].image}' \
      2>/dev/null || true
  )"
  current_agent_image="$(
    kind_deploy_kube get deployment/battlefield-agent \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="agent")].image}' \
      2>/dev/null || true
  )"
  current_mcp_image="$(
    kind_deploy_kube get deployment/battlefield-mcp \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="mcp")].image}' \
      2>/dev/null || true
  )"
  current_frontend_image="$(
    kind_deploy_kube get deployment/battlefield-frontend \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="frontend")].image}' \
      2>/dev/null || true
  )"
}

kind_deploy_resolve_images() {
	if [[ "${image_source}" == "registry" ]]; then
		effective_backend_ref="${backend_image_ref}"
		effective_frontend_ref="${frontend_image_ref}"
		if ! deployment_component_selected backend && { deployment_component_selected application || deployment_component_selected migration; }; then
			if [[ -z "${current_backend_image}" || "${current_backend_image}" != "${current_engine_image}" || "${current_backend_image}" != "${current_agent_image}" || "${current_backend_image}" != "${current_mcp_image}" ]]; then
				echo "Cannot preserve the current backend image; include backend in DEPLOY_COMPONENTS." >&2
				return 1
			fi
			effective_backend_ref="${current_backend_image}"
		fi
		if ! deployment_component_selected frontend && deployment_component_selected application; then
			if [[ -z "${current_frontend_image}" ]]; then
				echo "Cannot preserve the current frontend image; include frontend in DEPLOY_COMPONENTS." >&2
				return 1
			fi
			effective_frontend_ref="${current_frontend_image}"
		fi
		return
	fi

  effective_backend_image="${backend_image}"
  effective_backend_tag="${backend_image_tag}"
  effective_frontend_image="${frontend_image}"
  effective_frontend_tag="${frontend_image_tag}"

  if ! deployment_component_selected backend \
    && { deployment_component_selected application || deployment_component_selected migration; }; then
    if [[ -z "${current_backend_image}" \
      || "${current_backend_image}" != "${current_engine_image}" \
      || "${current_backend_image}" != "${current_agent_image}" \
      || "${current_backend_image}" != "${current_mcp_image}" \
      || "${current_backend_image}" == *@sha256:* \
      || "${current_backend_image}" != *:* ]]; then
      echo "Cannot preserve the current backend image; include backend in DEPLOY_COMPONENTS." >&2
      return 1
    fi
    effective_backend_image="${current_backend_image%:*}"
    effective_backend_tag="${current_backend_image##*:}"
  fi

  if ! deployment_component_selected frontend && deployment_component_selected application; then
    if [[ -z "${current_frontend_image}" \
      || "${current_frontend_image}" == *@sha256:* \
      || "${current_frontend_image}" != *:* ]]; then
      echo "Cannot preserve the current frontend image; include frontend in DEPLOY_COMPONENTS." >&2
      return 1
    fi
    effective_frontend_image="${current_frontend_image%:*}"
    effective_frontend_tag="${current_frontend_image##*:}"
  fi
}

kind_deploy_validate_artifacts() {
  if [[ "${image_source}" == "local" ]] && deployment_component_selected backend \
    && ! docker image inspect "${backend_image}:${backend_image_tag}" >/dev/null 2>&1; then
    echo "Local image not found: ${backend_image}:${backend_image_tag}" >&2
    echo "Build the backend revision first with BUILD_COMPONENTS=backend ./scripts/ci/build-images.sh" >&2
    return 1
  fi

  if [[ "${image_source}" == "local" ]] && deployment_component_selected frontend \
    && ! docker image inspect "${frontend_image}:${frontend_image_tag}" >/dev/null 2>&1; then
    echo "Local image not found: ${frontend_image}:${frontend_image_tag}" >&2
    echo "Build the frontend revision first with BUILD_COMPONENTS=frontend ./scripts/ci/build-images.sh" >&2
    return 1
  fi

  if deployment_component_selected infrastructure && [[ ! -f "${secret_file}" ]]; then
    echo "Database secret file not found: ${secret_file}" >&2
    echo "See docs/operations/environments.md to prepare the private database file; .env.example is reference-only." >&2
    return 1
  fi
}

kind_deploy_prepare_database_secret() {
  local target_file="$1"
  local database_url=""
  local mcp_database_url=""
  local line

  node "${project_dir}/scripts/lib/application-env.cjs" extract "${secret_file}" "${target_file}" database
  # Only the private staged copy receives the derived MCP connection string.
  chmod 600 "${target_file}"
  while IFS= read -r line; do
    case "${line}" in
      DATABASE_URL=*) database_url="${line#DATABASE_URL=}" ;;
      MCP_DATABASE_URL=*) mcp_database_url="${line#MCP_DATABASE_URL=}" ;;
    esac
  done <"${target_file}"

  if [[ -z "${mcp_database_url}" ]]; then
    if [[ -z "${database_url}" ]]; then
      echo "DATABASE_URL must be set in ${secret_file}." >&2
      return 1
    fi
    printf '\nMCP_DATABASE_URL=%s\n' "${database_url}" >>"${target_file}"
    echo "No MCP_DATABASE_URL supplied; reusing DATABASE_URL for this local deployment."
  fi
}

kind_deploy_prepare_ai_secret() {
  node "${project_dir}/scripts/lib/application-env.cjs" extract "${ai_secret_file}" "$1" ai
}

kind_deploy_prepare_overlays() {
  staged_auth_secret_file=""
  runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield-kustomize.XXXXXX")"
  mkdir -p \
    "${runtime_dir}/deploy/base" \
    "${runtime_dir}/deploy/overlays/local"

  local component
  for component in infrastructure migration application observability gateway; do
    deployment_component_selected "${component}" || continue
    if [[ -d "${project_dir}/deploy/base/${component}" ]]; then
      cp -R "${project_dir}/deploy/base/${component}" "${runtime_dir}/deploy/base/"
    fi
    if [[ "${component}" == "infrastructure" ]]; then
      mkdir -p "${runtime_dir}/deploy/overlays/local/infrastructure"
      # Stage only manifests. Secret inputs are copied explicitly below; optional
      # local authentication files do not belong in deployment scratch space.
      cp "${project_dir}/deploy/overlays/local/infrastructure/"*.yaml \
        "${runtime_dir}/deploy/overlays/local/infrastructure/"
    else
      cp -R "${project_dir}/deploy/overlays/local/${component}" \
        "${runtime_dir}/deploy/overlays/local/"
    fi
  done
  if deployment_component_selected observability; then
    cp -R "${project_dir}/deploy/overlays/local/logging-collector" \
      "${runtime_dir}/deploy/overlays/local/"
  fi
  if deployment_component_selected application && [[ "${DEPLOY_ENV:-dev}" == dev ]]; then
    sed -i 's/value: production/value: dev/' "${runtime_dir}/deploy/base/application/api-deployment.yaml"
  fi
  if deployment_component_selected migration && [[ "${DEPLOY_ENV:-dev}" == dev ]]; then
    sed -i 's/value: production/value: dev/' "${runtime_dir}/deploy/base/migration/job.yaml"
  fi
  if deployment_component_selected application; then
    cp -R "${project_dir}/deploy/overlays/local/application" \
      "${runtime_dir}/deploy/overlays/local/rollback-application"
  fi

  if deployment_component_selected infrastructure; then
    kind_deploy_prepare_database_secret \
      "${runtime_dir}/deploy/overlays/local/infrastructure/secret.env"
    kind_deploy_prepare_ai_secret \
      "${runtime_dir}/deploy/overlays/local/infrastructure/ai-secret.env"
    node "${project_dir}/scripts/lib/application-env.cjs" extract "${secret_file}" "${runtime_dir}/deploy/overlays/local/infrastructure/runtime.env" runtime
    node "${project_dir}/scripts/lib/application-env.cjs" extract "${secret_file}" "${runtime_dir}/deploy/overlays/local/infrastructure/cache.env" cache
    if [[ -f "${BATTLEFIELD_AUTH_SECRET_FILE:-}" ]]; then
      node "${project_dir}/scripts/lib/application-env.cjs" extract "${BATTLEFIELD_AUTH_SECRET_FILE}" "${runtime_dir}/auth.env" auth
      staged_auth_secret_file="${runtime_dir}/auth.env"
    fi
  fi

  if deployment_component_selected observability; then
    if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
      : "${BF_KUBELET_CA_FILE:?Production monitoring requires a trusted kubelet CA bundle}"
      python3 "${project_dir}/scripts/ops/configure-production-prometheus.py" \
        "${runtime_dir}/deploy/overlays/local/observability/prometheus" "${BF_KUBELET_CA_FILE}" || return
    fi
    LANGFUSE_ENABLED="$(battlefield_env_langfuse_enabled)" || return
    if [[ "${LANGFUSE_ENABLED}" == 1 ]]; then
      cp "${project_dir}/deploy/langfuse/collector-config.yaml" "${runtime_dir}/deploy/overlays/local/observability/otel-collector/config.yaml"
    fi
  fi

  rollback_overlay="${runtime_dir}/deploy/overlays/local/rollback-application"

  if deployment_component_selected application && [[ "${image_source}" == "registry" ]]; then
    backend_name="${effective_backend_ref%@*}"
    backend_digest="${effective_backend_ref##*@}"
    frontend_name="${effective_frontend_ref%@*}"
    frontend_digest="${effective_frontend_ref##*@}"
    cat >>"${runtime_dir}/deploy/overlays/local/application/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${backend_name}"
    digest: "${backend_digest}"
  - name: autonomous-battlefield-frontend
    newName: "${frontend_name}"
    digest: "${frontend_digest}"
EOF
  elif deployment_component_selected application; then
    cat >>"${runtime_dir}/deploy/overlays/local/application/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${effective_backend_image}"
    newTag: "${effective_backend_tag}"
  - name: autonomous-battlefield-frontend
    newName: "${effective_frontend_image}"
    newTag: "${effective_frontend_tag}"
EOF
  fi

  if deployment_component_selected migration && [[ "${image_source}" == "registry" ]]; then
    backend_name="${effective_backend_ref%@*}"
    backend_digest="${effective_backend_ref##*@}"
    cat >>"${runtime_dir}/deploy/overlays/local/migration/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${backend_name}"
    digest: "${backend_digest}"
EOF
  elif deployment_component_selected migration; then
    cat >>"${runtime_dir}/deploy/overlays/local/migration/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${effective_backend_image}"
    newTag: "${effective_backend_tag}"
EOF
  fi
}

kind_deploy_load_images() {
  if [[ "${image_source}" == "registry" ]]; then
    return
  fi
  if deployment_component_selected backend || deployment_component_selected frontend; then
    echo "Loading changed commit-addressed images into kind cluster ${cluster_name}..."
  fi
  if deployment_component_selected backend; then
    kind load docker-image "${backend_image}:${backend_image_tag}" --name "${cluster_name}"
  fi
  if deployment_component_selected frontend; then
    kind load docker-image "${frontend_image}:${frontend_image_tag}" --name "${cluster_name}"
  fi
}

kind_deploy_registry_credentials() {
  echo "Configuring private GHCR pull credentials..."
  kind_deploy_kube create secret docker-registry battlefield-ghcr \
    --namespace "${namespace}" \
    --docker-server ghcr.io \
    --docker-username "${GHCR_USER}" \
    --docker-password "${GHCR_TOKEN}" \
    --dry-run=client -o yaml | kind_deploy_kube apply -f -
  kind_deploy_kube patch serviceaccount default \
    --namespace "${namespace}" \
    --type merge \
    --patch '{"imagePullSecrets":[{"name":"battlefield-ghcr"}]}'
}

kind_deploy_infrastructure() {
  echo "Applying ${DEPLOY_ENV:-dev} infrastructure..."
  kind_deploy_kube apply -k "${runtime_dir}/deploy/overlays/local/infrastructure"
  if [[ -f "${staged_auth_secret_file:-}" ]]; then
    kind_deploy_kube create secret generic battlefield-auth --namespace "${namespace}" \
      --from-env-file="${staged_auth_secret_file}" --dry-run=client -o yaml | kind_deploy_kube apply -f -
  fi
  kind_deploy_kube rollout status statefulset/battlefield-postgres \
    --namespace "${namespace}" \
    --timeout="${rollout_timeout}"
}

kind_deploy_migration() {
  if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
    # The ownership migration must never precede a durable, readable backup.
    local backup_dir="${BATTLEFIELD_BACKUP_DIR:-${XDG_STATE_HOME:-${HOME}/.local/state}/battlefield/backups}"
    local backup_file
    install -d -m 700 "${backup_dir}"
    backup_file="$(mktemp "${backup_dir}/pre-migration-$(date -u +%Y%m%dT%H%M%SZ).XXXXXX.dump")"
    chmod 600 "${backup_file}"
    if ! kind_deploy_kube exec --namespace "${namespace}" battlefield-postgres-0 -- sh -c 'exec pg_dump --format=custom --no-owner --username "$POSTGRES_USER" --dbname "$POSTGRES_DB"' >"${backup_file}"; then
      rm -f -- "${backup_file}"
      echo 'Database backup failed; refusing migration.' >&2
      return 1
    fi
    [[ -s "${backup_file}" ]] || { echo 'Database backup is empty; refusing migration.' >&2; return 1; }
    kind_deploy_kube exec -i --namespace "${namespace}" battlefield-postgres-0 -- pg_restore --list <"${backup_file}" >/dev/null || return
    echo "Database backup verified: ${backup_file}"
  fi
  echo "Running the database migration gate..."
  kind_deploy_kube delete job battlefield-migrate \
    --namespace "${namespace}" \
    --ignore-not-found
  kind_deploy_kube apply -k "${runtime_dir}/deploy/overlays/local/migration"

  if ! kind_deploy_kube wait --for=condition=complete \
    job/battlefield-migrate \
    --namespace "${namespace}" \
    --timeout="${rollout_timeout}"; then
    kind_deploy_kube logs job/battlefield-migrate --namespace "${namespace}" || true
    return 1
  fi

  kind_deploy_kube logs job/battlefield-migrate --namespace "${namespace}"
}

kind_deploy_application() {
  local deployment

  echo "Deploying selected application components for revision ${image_tag}..."

  if [[ -n "${current_backend_image}" \
    && "${current_backend_image}" == "${current_engine_image}" \
    && "${current_backend_image}" == "${current_agent_image}" \
    && "${current_backend_image}" == "${current_mcp_image}" \
    && -n "${current_frontend_image}" \
    && "${current_backend_image}" != *@sha256:* \
    && "${current_frontend_image}" != *@sha256:* \
    && "${current_backend_image}" == *:* \
    && "${current_frontend_image}" == *:* ]]; then
    previous_backend_name="${current_backend_image%:*}"
    previous_backend_tag="${current_backend_image##*:}"
    previous_frontend_name="${current_frontend_image%:*}"
    previous_frontend_tag="${current_frontend_image##*:}"

    cat >>"${rollback_overlay}/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${previous_backend_name}"
    newTag: "${previous_backend_tag}"
  - name: autonomous-battlefield-frontend
    newName: "${previous_frontend_name}"
    newTag: "${previous_frontend_tag}"
EOF
    rollback_overlay_ready=1
  elif [[ -n "${current_backend_image}" \
    && "${current_backend_image}" == "${current_engine_image}" \
    && "${current_backend_image}" == "${current_agent_image}" \
    && "${current_backend_image}" == "${current_mcp_image}" \
    && -n "${current_frontend_image}" \
    && "${current_backend_image}" == *@sha256:* \
    && "${current_frontend_image}" == *@sha256:* ]]; then
    previous_backend_name="${current_backend_image%@*}"
    previous_backend_digest="${current_backend_image##*@}"
    previous_frontend_name="${current_frontend_image%@*}"
    previous_frontend_digest="${current_frontend_image##*@}"

    cat >>"${rollback_overlay}/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${previous_backend_name}"
    digest: "${previous_backend_digest}"
  - name: autonomous-battlefield-frontend
    newName: "${previous_frontend_name}"
    digest: "${previous_frontend_digest}"
EOF
    rollback_overlay_ready=1
  fi

  for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-mcp battlefield-frontend; do
    previous_revisions["${deployment}"]="$(
      kind_deploy_kube get "deployment/${deployment}" \
        --namespace "${namespace}" \
        -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}' \
        2>/dev/null || true
    )"
  done

  application_update_started=1
  kind_deploy_kube apply -k "${runtime_dir}/deploy/overlays/local/application"

  if [[ "${force_restart}" == "1" ]] || deployment_component_selected infrastructure; then
    kind_deploy_kube rollout restart \
      deployment/battlefield-api \
      deployment/battlefield-engine \
      deployment/battlefield-agent \
      deployment/battlefield-mcp \
      deployment/battlefield-frontend \
      --namespace "${namespace}"
  fi

  for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-mcp battlefield-frontend; do
    kind_deploy_kube rollout status "deployment/${deployment}" \
      --namespace "${namespace}" \
      --timeout="${rollout_timeout}"
  done
  application_update_started=0
}

kind_deploy_report_workload_failure() {
  local resource="$1"
  local target_namespace="$2"

  echo "Diagnostics for ${target_namespace}/${resource}:" >&2
  kind_deploy_kube get "${resource}" \
    --namespace "${target_namespace}" -o wide >&2 || true
  kind_deploy_kube describe "${resource}" \
    --namespace "${target_namespace}" >&2 || true
  kind_deploy_kube logs "${resource}" \
    --namespace "${target_namespace}" \
    --all-containers=true \
    --prefix=true \
    --tail=100 >&2 || true
}

# Watch both terminal conditions so a failed Job does not consume the full wait.
kind_deploy_wait_kibana_setup() {
  local deadline=$((SECONDS + ${rollout_timeout%s})) conditions
  while ((SECONDS < deadline)); do
    if ! conditions="$(kind_deploy_kube get job battlefield-kibana-setup \
      --namespace "${namespace}" --request-timeout=10s \
      -o 'jsonpath={range .status.conditions[*]}{.type}={.status}{"\n"}{end}')"; then
      break
    fi
    if [[ "${conditions}" == *'Failed=True'* ]]; then
      echo "Kibana setup job failed." >&2
      break
    fi
    if [[ "${conditions}" == *'Complete=True'* ]]; then
      echo "Kibana setup job completed."
      return 0
    fi
    sleep 2
  done
  echo "Kibana setup did not complete; collecting diagnostics." >&2
  kind_deploy_kube describe job battlefield-kibana-setup --namespace "${namespace}" --request-timeout=10s >&2 || true
  kind_deploy_kube get pods --namespace "${namespace}" -l job-name=battlefield-kibana-setup --request-timeout=10s >&2 || true
  kind_deploy_kube logs --namespace "${namespace}" -l job-name=battlefield-kibana-setup \
    --all-containers=true --prefix=true --tail=100 --request-timeout=10s >&2 || true
  return 1
}

kind_deploy_observability() {
  local deployment index
  local rollout_failed=0
  rollout_pids=()
  local failed_deployments=()
  local deployments=(
    battlefield-prometheus
    battlefield-kube-state-metrics
    battlefield-grafana
    battlefield-otel-collector
    battlefield-tempo
    battlefield-elasticsearch
  )

  echo "Deploying local observability stack..."
  kind_deploy_kube delete job battlefield-kibana-setup \
    --namespace "${namespace}" \
    --ignore-not-found
  kind_deploy_kube apply -k "${runtime_dir}/deploy/overlays/local/observability"
  kind_deploy_kube apply -k "${runtime_dir}/deploy/overlays/local/logging-collector"
  # Wait for the new ReplicaSets, not a possibly stale Available condition.
  # Start waits together so cold image pulls share the same rollout budget.
  for deployment in "${deployments[@]}"; do
    kind_deploy_wait_rollout "deployment/${deployment}" \
      --namespace "${namespace}" \
      --timeout="${observability_rollout_timeout}" &
    rollout_pids+=("$!")
  done
  for index in "${!rollout_pids[@]}"; do
    if ! wait "${rollout_pids[${index}]}"; then
      rollout_failed=1
      failed_deployments+=("${deployments[${index}]}")
    fi
  done
  rollout_pids=()
  if [[ "${rollout_failed}" == "1" ]]; then
    for deployment in "${failed_deployments[@]}"; do
      kind_deploy_report_workload_failure "deployment/${deployment}" "${namespace}"
    done
    echo "Recent ${namespace} events:" >&2
    kind_deploy_kube get events \
      --namespace "${namespace}" \
      --sort-by=.lastTimestamp >&2 || true
    return 1
  fi
  if ! kind_deploy_kube rollout status daemonset/battlefield-fluent-bit \
    --namespace "${logging_namespace}" \
    --timeout="${rollout_timeout}"; then
    kind_deploy_report_workload_failure \
      daemonset/battlefield-fluent-bit "${logging_namespace}"
    return 1
  fi

  # Reinitialize Kibana against the now-ready Elasticsearch instance. This also
  # recovers the first transition from disposable storage to the persistent claim.
  kind_deploy_kube rollout restart deployment/battlefield-kibana --namespace "${namespace}" || return
  if ! kind_deploy_kube rollout status deployment/battlefield-kibana \
    --namespace "${namespace}" --timeout="${observability_rollout_timeout}"; then
    kind_deploy_report_workload_failure deployment/battlefield-kibana "${namespace}"
    return 1
  fi
  echo "Creating Kibana saved objects..."
  kind_deploy_kube apply --namespace "${namespace}" \
    -f "${runtime_dir}/deploy/overlays/local/observability/kibana/setup-job.yaml" || return
  kind_deploy_wait_kibana_setup

}

kind_deploy_gateway() {
  echo "Deploying local HTTP gateway routes..."
  kind_deploy_kube apply -k "${runtime_dir}/deploy/overlays/local/gateway"
  kind_deploy_kube wait --for=condition=Programmed \
    gateway/battlefield-local \
    --namespace "${namespace}" \
    --timeout="${rollout_timeout}"
}

kind_deploy_rollback_application() {
  if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
    if [[ "${current_backend_image}" != "${current_engine_image}" || "${current_backend_image}" != "${current_agent_image}" || "${current_backend_image}" != "${current_mcp_image}" ]]; then
      echo 'Previous backend digests are inconsistent; production rollback requires operator recovery.' >&2
      return 1
    fi
    "${project_dir}/scripts/cd/rollback-kind.sh" "${current_backend_image}" "${current_frontend_image}"
    return
  fi
  local rollback_failed=0
  local rollback_started=0
  local deployment

  echo "Deployment failed; restoring the previous application revisions..." >&2

  if [[ "${rollback_overlay_ready}" == "1" ]]; then
    if ! kind_deploy_kube apply -k "${rollback_overlay}"; then
      return 1
    fi

    for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-mcp battlefield-frontend; do
      if ! kind_deploy_kube rollout status "deployment/${deployment}" \
        --namespace "${namespace}" \
        --timeout="${rollout_timeout}"; then
        rollback_failed=1
      fi
    done

    return "${rollback_failed}"
  fi

  for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-mcp battlefield-frontend; do
    local previous_revision="${previous_revisions[${deployment}]:-}"
    local current_revision
    current_revision="$(
      kind_deploy_kube get "deployment/${deployment}" \
        --namespace "${namespace}" \
        -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}' \
        2>/dev/null || true
    )"

    if [[ -n "${previous_revision}" && "${current_revision}" != "${previous_revision}" ]]; then
      rollback_started=1
      if ! kind_deploy_kube rollout undo "deployment/${deployment}" \
        --namespace "${namespace}" \
        --to-revision "${previous_revision}"; then
        rollback_failed=1
      fi
    fi
  done

  if [[ "${rollback_started}" == "1" ]]; then
    for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-mcp battlefield-frontend; do
      if ! kind_deploy_kube rollout status "deployment/${deployment}" \
        --namespace "${namespace}" \
        --timeout="${rollout_timeout}"; then
        rollback_failed=1
      fi
    done
  fi

  return "${rollback_failed}"
}

kind_deploy_cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if ((${#rollout_pids[@]} > 0)); then
    kill "${rollout_pids[@]}" 2>/dev/null || true
    wait "${rollout_pids[@]}" 2>/dev/null || true
  fi

  if [[ "${exit_code}" != "0" \
    && "${rollback_on_failure}" == "1" \
    && "${application_update_started}" == "1" ]]; then
    if ! kind_deploy_rollback_application; then
      echo "Automatic application rollback did not complete successfully." >&2
    fi
  fi

  if [[ -n "${runtime_dir}" && -d "${runtime_dir}" ]]; then
    rm -rf -- "${runtime_dir}"
  fi

  exit "${exit_code}"
}

kind_deploy_print_summary() {
  echo
  echo "Applied components (${deploy_components}) for ${image_tag} to kind cluster ${cluster_name}."
  kind_deploy_kube get deployments \
    --namespace "${namespace}" \
    -o custom-columns='NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas,IMAGE:.spec.template.spec.containers[0].image'
  kind_deploy_kube get job battlefield-migrate \
    --namespace "${namespace}" \
    -o custom-columns='NAME:.metadata.name,SUCCEEDED:.status.succeeded,IMAGE:.spec.template.spec.containers[0].image' \
    2>/dev/null || true
  echo
  echo "Frontend: ${TARGET_URL:-http://battlefield.localhost:8888}"
  echo "Grafana:  http://grafana.localhost:8888"
  echo "Kibana:   http://kibana.localhost:8888"
}
