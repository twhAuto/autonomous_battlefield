#!/usr/bin/env bash

# Internal deployment functions. Configuration and runtime state are owned by
# scripts/cd/deploy.sh, which is the supported entry point.

cluster_deploy_kube() {
  kubectl --context "${kube_context}" "$@"
}

# Run only in a background subshell: exec makes the tracked PID the kubectl
# process itself, so cancellation does not leave an orphaned rollout watcher.
cluster_deploy_wait_rollout() {
  exec kubectl --context "${kube_context}" rollout status "$@"
}

cluster_deploy_require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    return 1
  fi
}

cluster_deploy_validate_inputs() {
  if [[ "$#" -gt 1 ]]; then
    echo "Usage: ${deploy_command} [full-git-sha]" >&2
    return 1
  fi

  for command in git grep kubectl; do
    cluster_deploy_require_command "${command}"
  done

  if [[ "${CLUSTER_DRIVER:-kind}" == kind ]]; then
    cluster_deploy_require_command kind
  elif [[ "${image_source}" != registry ]]; then
    echo 'K3s delivery requires verified registry images.' >&2; return 1
  fi

  if [[ "${image_source}" != "local" && "${image_source}" != "registry" ]]; then
    echo "IMAGE_SOURCE must be local or registry." >&2
    return 1
  fi
  if [[ "${enforce_supply_chain_policy}" != "0" && "${enforce_supply_chain_policy}" != "1" ]]; then
    echo "ENFORCE_SUPPLY_CHAIN_POLICY must be either 0 or 1." >&2
    return 1
  fi
  if [[ "${image_source}" == "local" ]]; then
    cluster_deploy_require_command docker
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
  if [[ ! "${backup_timeout}" =~ ^[1-9][0-9]*s$ ]]; then
    echo "BACKUP_TIMEOUT must be a positive number of seconds such as 300s." >&2
    return 1
  fi
  if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
    cluster_deploy_require_command timeout
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

cluster_deploy_validate_cluster() {
  if [[ "${CLUSTER_DRIVER:-kind}" == kind ]] && ! kind get clusters | grep -Fxq "${cluster_name}"; then
    echo "kind cluster not found: ${cluster_name}" >&2
    echo "Create it with ./scripts/cd/create-kind-cluster.sh" >&2
    return 1
  fi

  if ! cluster_deploy_kube get customresourcedefinition/gateways.gateway.networking.k8s.io \
    >/dev/null 2>&1 \
    || ! cluster_deploy_kube get customresourcedefinition/envoyproxies.gateway.envoyproxy.io \
      >/dev/null 2>&1; then
    echo "Envoy Gateway and the Gateway API are not installed in ${cluster_name}." >&2
    if [[ "${CLUSTER_DRIVER:-kind}" == kind ]]; then
      echo "Install them with ./scripts/cd/create-kind-cluster.sh" >&2
    else
      echo "Verify the registered platform before application delivery." >&2
    fi
    return 1
  fi

  cluster_deploy_kube rollout status deployment/envoy-gateway \
    --namespace envoy-gateway-system \
    --timeout="${rollout_timeout}"
}

cluster_deploy_capture_current_images() {
  current_backend_image="$(
    cluster_deploy_kube get deployment/battlefield-api \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="api")].image}' \
      2>/dev/null || true
  )"
  current_engine_image="$(
    cluster_deploy_kube get deployment/battlefield-engine \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="engine")].image}' \
      2>/dev/null || true
  )"
  current_agent_image="$(
    cluster_deploy_kube get deployment/battlefield-agent \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="agent")].image}' \
      2>/dev/null || true
  )"
  current_gateway_image="$(
    cluster_deploy_kube get deployment/battlefield-ai-gateway \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="ai-gateway")].image}' \
      2>/dev/null || true
  )"
  if [[ -z "${current_gateway_image}" ]]; then
    # First rollout after the rename: the gateway still runs as battlefield-mcp.
    current_gateway_image="$(
      cluster_deploy_kube get deployment/battlefield-mcp \
        --namespace "${namespace}" \
        -o jsonpath='{.spec.template.spec.containers[?(@.name=="mcp")].image}' \
        2>/dev/null || true
    )"
  fi
  current_frontend_image="$(
    cluster_deploy_kube get deployment/battlefield-frontend \
      --namespace "${namespace}" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="frontend")].image}' \
      2>/dev/null || true
  )"
}

cluster_deploy_resolve_images() {
	if [[ "${image_source}" == "registry" ]]; then
		effective_backend_ref="${backend_image_ref}"
		effective_frontend_ref="${frontend_image_ref}"
		if ! deployment_component_selected backend && { deployment_component_selected application || deployment_component_selected migration; }; then
			if [[ -z "${current_backend_image}" || "${current_backend_image}" != "${current_engine_image}" || "${current_backend_image}" != "${current_agent_image}" || "${current_backend_image}" != "${current_gateway_image}" ]]; then
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
      || "${current_backend_image}" != "${current_gateway_image}" \
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

cluster_deploy_validate_artifacts() {
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
    echo "See docs/operations/deployment.md#production-setup to prepare the private database file; .env.example is reference-only." >&2
    return 1
  fi
}

cluster_deploy_prepare_database_secret() {
  local target_file="$1"
  local database_url=""
  local gateway_database_url=""
  local legacy_database_url=""
  local line
  local staged

  node "${project_dir}/scripts/lib/application-env.cjs" extract "${secret_file}" "${target_file}" database
  # Only the private staged copy receives the derived gateway connection string.
  chmod 600 "${target_file}"
  while IFS= read -r line; do
    case "${line}" in
      DATABASE_URL=*) database_url="${line#DATABASE_URL=}" ;;
      AI_GATEWAY_DATABASE_URL=*) gateway_database_url="${line#AI_GATEWAY_DATABASE_URL=}" ;;
      MCP_DATABASE_URL=*) legacy_database_url="${line#MCP_DATABASE_URL=}" ;;
    esac
  done <"${target_file}"

  if [[ -n "${gateway_database_url}" && -n "${legacy_database_url}" && "${gateway_database_url}" != "${legacy_database_url}" ]]; then
    echo "AI_GATEWAY_DATABASE_URL and legacy MCP_DATABASE_URL disagree in ${secret_file}; set only AI_GATEWAY_DATABASE_URL." >&2
    return 1
  fi
  if [[ -z "${gateway_database_url}" && -n "${legacy_database_url}" ]]; then
    gateway_database_url="${legacy_database_url}"
    echo "Using legacy MCP_DATABASE_URL as AI_GATEWAY_DATABASE_URL; rename it in ${secret_file}."
  fi
  if [[ -z "${gateway_database_url}" ]]; then
    if [[ -z "${database_url}" ]]; then
      echo "DATABASE_URL must be set in ${secret_file}." >&2
      return 1
    fi
    gateway_database_url="${database_url}"
    echo "No AI_GATEWAY_DATABASE_URL supplied; reusing DATABASE_URL for this local deployment."
  fi
  # Stage both names with one value: a legacy gateway pod that restarts before the
  # application rollout (for example after a failed migration) still needs the old key.
  staged="$(grep -v -e '^AI_GATEWAY_DATABASE_URL=' -e '^MCP_DATABASE_URL=' "${target_file}" || true)"
  printf '%s\nAI_GATEWAY_DATABASE_URL=%s\nMCP_DATABASE_URL=%s\n' \
    "${staged}" "${gateway_database_url}" "${gateway_database_url}" >"${target_file}"
}

cluster_deploy_prepare_ai_secret() {
  node "${project_dir}/scripts/lib/application-env.cjs" extract "${ai_secret_file}" "$1" ai
}

cluster_deploy_prepare_overlays() {
  staged_auth_secret_file=""
  runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield-kustomize.XXXXXX")"
  mkdir -p \
    "${runtime_dir}/infra/deploy/base" \
    "${runtime_dir}/infra/deploy/overlays/local"

  local component
  for component in infrastructure migration application observability gateway; do
    deployment_component_selected "${component}" || continue
    if [[ -d "${project_dir}/infra/deploy/base/${component}" ]]; then
      cp -R "${project_dir}/infra/deploy/base/${component}" "${runtime_dir}/infra/deploy/base/"
    fi
    if [[ "${component}" == "infrastructure" ]]; then
      mkdir -p "${runtime_dir}/infra/deploy/overlays/local/infrastructure"
      # Stage only manifests. Secret inputs are copied explicitly below; optional
      # local authentication files do not belong in deployment scratch space.
      cp "${project_dir}/infra/deploy/overlays/local/infrastructure/"*.yaml \
        "${runtime_dir}/infra/deploy/overlays/local/infrastructure/"
    else
      cp -R "${project_dir}/infra/deploy/overlays/local/${component}" \
        "${runtime_dir}/infra/deploy/overlays/local/"
    fi
  done
  if deployment_component_selected observability; then
    cp -R "${project_dir}/infra/deploy/overlays/local/logging-collector" \
      "${runtime_dir}/infra/deploy/overlays/local/"
  fi
  # Base manifests describe production; a dev deployment rewrites BATTLEFIELD_ENV and the
  # trace environment label (BATTLEFIELD_ENVIRONMENT) on every application workload.
  if deployment_component_selected application && [[ "${DEPLOY_ENV:-dev}" == dev ]]; then
    sed -i 's/value: production/value: dev/' "${runtime_dir}"/infra/deploy/base/application/{api,engine,ai-gateway,agent}-deployment.yaml
  fi
  if deployment_component_selected migration && [[ "${DEPLOY_ENV:-dev}" == dev ]]; then
    sed -i 's/value: production/value: dev/' "${runtime_dir}/infra/deploy/base/migration/job.yaml"
  fi
  if deployment_component_selected application; then
    cp -R "${project_dir}/infra/deploy/overlays/local/application" \
      "${runtime_dir}/infra/deploy/overlays/local/rollback-application"
  fi

  if deployment_component_selected infrastructure; then
    cluster_deploy_prepare_database_secret \
      "${runtime_dir}/infra/deploy/overlays/local/infrastructure/secret.env"
    cluster_deploy_prepare_ai_secret \
      "${runtime_dir}/infra/deploy/overlays/local/infrastructure/ai-secret.env"
    node "${project_dir}/scripts/lib/application-env.cjs" extract "${secret_file}" "${runtime_dir}/infra/deploy/overlays/local/infrastructure/runtime.env" runtime
    node "${project_dir}/scripts/lib/application-env.cjs" extract "${secret_file}" "${runtime_dir}/infra/deploy/overlays/local/infrastructure/cache.env" cache
    node "${project_dir}/scripts/lib/application-env.cjs" extract "${secret_file}" "${runtime_dir}/members.env" members
    if [[ -f "${BATTLEFIELD_AUTH_SECRET_FILE:-}" ]]; then
      node "${project_dir}/scripts/lib/application-env.cjs" extract "${BATTLEFIELD_AUTH_SECRET_FILE}" "${runtime_dir}/auth.env" auth
      staged_auth_secret_file="${runtime_dir}/auth.env"
    fi
  fi

  if deployment_component_selected observability; then
    if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
      : "${BF_KUBELET_CA_FILE:?Production monitoring requires a trusted kubelet CA bundle}"
      python3 "${project_dir}/scripts/ops/configure-production-prometheus.py" \
        "${runtime_dir}/infra/deploy/overlays/local/observability/prometheus" "${BF_KUBELET_CA_FILE}" || return
    fi
    LANGFUSE_ENABLED="$(battlefield_env_langfuse_enabled)" || return
    if [[ "${LANGFUSE_ENABLED}" == 1 ]]; then
      cp "${project_dir}/infra/deploy/langfuse/collector-config.yaml" "${runtime_dir}/infra/deploy/overlays/local/observability/otel-collector/config.yaml"
    fi
  fi

  if [[ "${CLUSTER_DRIVER:-kind}" == k3s ]]; then
    mkdir -p "${runtime_dir}/infra/deploy/overlays/k3s"
    for component in infrastructure migration application observability gateway logging-collector; do
      [[ -d "${runtime_dir}/infra/deploy/overlays/local/${component}" ]] || continue
      cp -R "${project_dir}/infra/deploy/overlays/k3s/${component}" "${runtime_dir}/infra/deploy/overlays/k3s/"
    done
  fi
  rollout_overlays="${runtime_dir}/infra/deploy/overlays/$([[ "${CLUSTER_DRIVER:-kind}" == k3s ]] && echo k3s || echo local)"
  rollback_overlay="${runtime_dir}/infra/deploy/overlays/local/rollback-application"

  if deployment_component_selected application && [[ "${image_source}" == "registry" ]]; then
    backend_name="${effective_backend_ref%@*}"
    backend_digest="${effective_backend_ref##*@}"
    frontend_name="${effective_frontend_ref%@*}"
    frontend_digest="${effective_frontend_ref##*@}"
    cat >>"${runtime_dir}/infra/deploy/overlays/local/application/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${backend_name}"
    digest: "${backend_digest}"
  - name: autonomous-battlefield-frontend
    newName: "${frontend_name}"
    digest: "${frontend_digest}"
EOF
  elif deployment_component_selected application; then
    cat >>"${runtime_dir}/infra/deploy/overlays/local/application/kustomization.yaml" <<EOF

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
    cat >>"${runtime_dir}/infra/deploy/overlays/local/migration/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${backend_name}"
    digest: "${backend_digest}"
EOF
  elif deployment_component_selected migration; then
    cat >>"${runtime_dir}/infra/deploy/overlays/local/migration/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${effective_backend_image}"
    newTag: "${effective_backend_tag}"
EOF
  fi
}

cluster_deploy_load_images() {
  if [[ "${image_source}" == "registry" ]]; then
    return
  fi
  if deployment_component_selected backend || deployment_component_selected frontend; then
    echo "Loading changed commit-addressed images into ${CLUSTER_DRIVER:-kind} cluster ${cluster_name}..."
  fi
  if deployment_component_selected backend; then
    kind load docker-image "${backend_image}:${backend_image_tag}" --name "${cluster_name}"
  fi
  if deployment_component_selected frontend; then
    kind load docker-image "${frontend_image}:${frontend_image_tag}" --name "${cluster_name}"
  fi
}

cluster_deploy_registry_credentials() {
  echo "Configuring private GHCR pull credentials..."
  cluster_deploy_kube create secret docker-registry battlefield-ghcr \
    --namespace "${namespace}" \
    --docker-server ghcr.io \
    --docker-username "${GHCR_USER}" \
    --docker-password "${GHCR_TOKEN}" \
    --dry-run=client -o yaml | cluster_deploy_kube apply -f -
  cluster_deploy_kube patch serviceaccount default \
    --namespace "${namespace}" \
    --type merge \
    --patch '{"imagePullSecrets":[{"name":"battlefield-ghcr"}]}'
}

cluster_deploy_infrastructure() {
  echo "Applying ${DEPLOY_ENV:-dev} infrastructure..."
  cluster_deploy_kube apply -k "${rollout_overlays:-${runtime_dir}/infra/deploy/overlays/local}/infrastructure"
  if [[ -f "${staged_auth_secret_file:-}" ]]; then
    cluster_deploy_kube create secret generic battlefield-auth --namespace "${namespace}" \
      --from-env-file="${staged_auth_secret_file}" --dry-run=client -o yaml | cluster_deploy_kube apply -f -
  fi
  cluster_deploy_kube create secret generic battlefield-members --namespace "${namespace}" \
    --from-env-file="${runtime_dir}/members.env" --dry-run=client -o yaml | cluster_deploy_kube apply -f -
  cluster_deploy_kube rollout status statefulset/battlefield-postgres \
    --namespace "${namespace}" \
    --timeout="${rollout_timeout}"
}

# Runs a backup command in the Postgres pod under BACKUP_TIMEOUT, so the remote process
# ends on time. The local deadline adds grace for that to be reported and stops a stalled
# exec stream, which would otherwise hold the production lock until the pipeline timeout.
cluster_deploy_backup_exec() {
  local stdin=()
  if [[ "$1" == -i ]]; then
    stdin=(-i)
    shift
  fi
  timeout --kill-after=10s "$((${backup_timeout%s} + 15))" kubectl --context "${kube_context}" exec "${stdin[@]}" \
    --namespace "${namespace}" battlefield-postgres-0 -- timeout "${backup_timeout%s}" sh -c "$1"
}

# 124 and 137 come from the local timeout, 143 from the pod's BusyBox timeout.
cluster_deploy_backup_outcome() {
  if (($1 == 124 || $1 == 137 || $1 == 143)); then
    echo "did not finish within ${backup_timeout}"
  else
    echo "exited with status $1"
  fi
}

# Read-only evidence for a failed backup. Each read is bounded, so diagnostics cannot stall too.
cluster_deploy_database_diagnostics() {
  echo "Collecting PostgreSQL diagnostics." >&2
  cluster_deploy_kube get pod battlefield-postgres-0 --namespace "${namespace}" -o wide --request-timeout=10s >&2 || true
  cluster_deploy_kube get events --namespace "${namespace}" --field-selector involvedObject.name=battlefield-postgres-0 \
    --sort-by=.lastTimestamp --request-timeout=10s >&2 || true
  timeout --kill-after=5s 20 kubectl --context "${kube_context}" exec --namespace "${namespace}" battlefield-postgres-0 -- \
    sh -c 'exec psql --no-psqlrc --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --command "select pid, application_name, state, wait_event_type, wait_event, now() - xact_start as transaction_age, pg_blocking_pids(pid) as blocked_by from pg_stat_activity where datname = current_database() and pid <> pg_backend_pid() order by xact_start"' >&2 || true
}

# Dumps are encrypted to this age public key; its private key stays off this host.
backup_recipient_pattern='^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$'
# Each dump holds a full copy of the database, member emails included.
backup_keep=3

# Keeps the newest encrypted dumps and deletes the rest, including plaintext dumps that
# predate encryption. Runs only after a verified backup, so a newer dump always remains.
cluster_deploy_prune_backups() {
  local backup_dir="$1" dump
  local -a dumps
  mapfile -t dumps < <(find "${backup_dir}" -maxdepth 1 -type f -name 'pre-migration-*.dump.age' -printf '%f\n' | sort -r)
  for dump in "${dumps[@]:backup_keep}"; do
    rm -f -- "${backup_dir}/${dump}" || echo "Warning: could not delete the old database dump ${dump}." >&2
  done
  find "${backup_dir}" -maxdepth 1 -type f -name 'pre-migration-*.dump' -delete ||
    echo "Warning: could not delete plaintext database dumps in ${backup_dir}." >&2
}

# The ownership migration must never precede a durable, readable backup. The dump streams
# through tee: age writes only ciphertext to disk, and pg_restore --list checks the
# plaintext stream in the pod. Query text stays out of the diagnostics because build logs
# are retained. A stopped dump's server session ends when it next writes; pg_dump gives up
# on table locks after a minute, so a session waiting on a lock ends by then too.
cluster_deploy_backup_database() {
  local backup_dir="${BATTLEFIELD_BACKUP_DIR:-${XDG_STATE_HOME:-${HOME}/.local/state}/battlefield/backups}"
  local recipient="${BF_BACKUP_RECIPIENT:-}" backup_file stream encryptor encrypted=0 failure=''
  local -a status
  if [[ ! "${recipient}" =~ ${backup_recipient_pattern} ]]; then
    failure='BF_BACKUP_RECIPIENT in the deployment profile is not an age public key'
  elif ! command -v age >/dev/null; then
    failure='age is not installed on this agent'
  fi
  if [[ -n "${failure}" ]]; then
    echo "Database backup failed: ${failure}; refusing migration." >&2
    return 1
  fi
  install -d -m 700 "${backup_dir}"
  backup_file="$(mktemp "${backup_dir}/pre-migration-$(date -u +%Y%m%dT%H%M%SZ).XXXXXX.dump.age")"
  chmod 600 "${backup_file}"
  stream="$(mktemp -d "${backup_dir}/.stream.XXXXXX")"
  mkfifo -m 600 "${stream}/dump"
  # The deadline also covers opening the FIFO, which blocks until tee opens it.
  timeout --kill-after=10s "$((${backup_timeout%s} + 30))" \
    sh -c 'exec age --encrypt --recipient "$1" <"$2" >"$3"' age "${recipient}" "${stream}/dump" "${backup_file}" &
  encryptor=$!
  # pg_restore --list reads only the archive's table of contents. Drain the rest, or
  # kubectl blocks sending a dump larger than the stream buffers.
  {
    cluster_deploy_backup_exec \
      'exec pg_dump --format=custom --no-owner --lock-wait-timeout=60s --username "$POSTGRES_USER" --dbname "$POSTGRES_DB"' |
      tee "${stream}/dump" |
      cluster_deploy_backup_exec -i 'pg_restore --list >/dev/null; status=$?; cat >/dev/null; exit "$status"'
    status=("${PIPESTATUS[@]}")
  } || true
  wait "${encryptor}" || encrypted=$?
  rm -rf -- "${stream}"
  # A stage killed by SIGPIPE (141) only shows that a later stage stopped reading, so
  # age is checked first and pg_restore before a pg_dump that lost its reader.
  if ((encrypted != 0)); then
    failure='age could not encrypt the dump'
  elif ((status[0] != 0 && (status[0] != 141 || status[2] == 0))); then
    failure="pg_dump $(cluster_deploy_backup_outcome "${status[0]}")"
  elif ((status[2] != 0)); then
    failure="pg_restore --list $(cluster_deploy_backup_outcome "${status[2]}")"
  elif ((status[1] != 0)); then
    failure='age could not encrypt the dump'
  fi
  if [[ -n "${failure}" ]]; then
    rm -f -- "${backup_file}"
    echo "Database backup failed: ${failure}; refusing migration." >&2
    cluster_deploy_database_diagnostics
    return 1
  fi
  cluster_deploy_prune_backups "${backup_dir}"
  echo "Database backup verified and encrypted: ${backup_file}"
}

cluster_deploy_migration() {
  if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
    cluster_deploy_backup_database
  fi
  echo "Running the database migration gate..."
  cluster_deploy_kube delete job battlefield-migrate \
    --namespace "${namespace}" \
    --ignore-not-found
  cluster_deploy_kube apply -k "${rollout_overlays:-${runtime_dir}/infra/deploy/overlays/local}/migration"

  if ! cluster_deploy_kube wait --for=condition=complete \
    job/battlefield-migrate \
    --namespace "${namespace}" \
    --timeout="${rollout_timeout}"; then
    cluster_deploy_kube logs job/battlefield-migrate --namespace "${namespace}" || true
    return 1
  fi

  cluster_deploy_kube logs job/battlefield-migrate --namespace "${namespace}"
}

cluster_deploy_application() {
  local deployment
  local legacy_gateway

  echo "Deploying selected application components for revision ${image_tag}..."

  if [[ -n "${current_backend_image}" \
    && "${current_backend_image}" == "${current_engine_image}" \
    && "${current_backend_image}" == "${current_agent_image}" \
    && "${current_backend_image}" == "${current_gateway_image}" \
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
    && "${current_backend_image}" == "${current_gateway_image}" \
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

  for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-ai-gateway battlefield-frontend; do
    previous_revisions["${deployment}"]="$(
      cluster_deploy_kube get "deployment/${deployment}" \
        --namespace "${namespace}" \
        -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}' \
        2>/dev/null || true
    )"
  done

  application_update_started=1
  cluster_deploy_kube apply -k "${rollout_overlays:-${runtime_dir}/infra/deploy/overlays/local}/application"
  # Without a legacy gateway there is nothing to drain, so route the alias now. Only
  # a successful empty lookup means absent; an API error must not reroute traffic.
  if ! legacy_gateway="$(cluster_deploy_kube get deployment/battlefield-mcp \
    --namespace "${namespace}" --ignore-not-found -o name)"; then
    echo "Could not determine whether the legacy battlefield-mcp gateway exists." >&2
    return 1
  fi
  if [[ -z "${legacy_gateway}" ]]; then
    cluster_deploy_route_gateway_alias
  fi

  if [[ "${force_restart}" == "1" ]] || deployment_component_selected infrastructure; then
    cluster_deploy_kube rollout restart \
      deployment/battlefield-api \
      deployment/battlefield-engine \
      deployment/battlefield-agent \
      deployment/battlefield-ai-gateway \
      deployment/battlefield-frontend \
      --namespace "${namespace}"
  fi

  for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-ai-gateway battlefield-frontend; do
    cluster_deploy_kube rollout status "deployment/${deployment}" \
      --namespace "${namespace}" \
      --timeout="${rollout_timeout}"
  done
  # Route the alias and retire the pre-rename gateway only once its replacement is
  # ready; until then the alias keeps selecting the legacy pods.
  cluster_deploy_route_gateway_alias
  cluster_deploy_kube delete deployment/battlefield-mcp networkpolicy/battlefield-mcp-agent-only \
    --namespace "${namespace}" \
    --ignore-not-found
  application_update_started=0
}

cluster_deploy_route_gateway_alias() {
  cluster_deploy_kube apply --namespace "${namespace}" \
    -f "${project_dir}/infra/deploy/transition/ai-gateway-alias-service.yaml"
}

cluster_deploy_report_workload_failure() {
  local resource="$1"
  local target_namespace="$2"

  echo "Diagnostics for ${target_namespace}/${resource}:" >&2
  cluster_deploy_kube get "${resource}" \
    --namespace "${target_namespace}" -o wide >&2 || true
  cluster_deploy_kube describe "${resource}" \
    --namespace "${target_namespace}" >&2 || true
  cluster_deploy_kube logs "${resource}" \
    --namespace "${target_namespace}" \
    --all-containers=true \
    --prefix=true \
    --tail=100 >&2 || true
}

# Watch both terminal conditions so a failed Job does not consume the full wait.
cluster_deploy_wait_kibana_setup() {
  local deadline=$((SECONDS + ${rollout_timeout%s})) conditions
  while ((SECONDS < deadline)); do
    if ! conditions="$(cluster_deploy_kube get job battlefield-kibana-setup \
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
  cluster_deploy_kube describe job battlefield-kibana-setup --namespace "${namespace}" --request-timeout=10s >&2 || true
  cluster_deploy_kube get pods --namespace "${namespace}" -l job-name=battlefield-kibana-setup --request-timeout=10s >&2 || true
  cluster_deploy_kube logs --namespace "${namespace}" -l job-name=battlefield-kibana-setup \
    --all-containers=true --prefix=true --tail=100 --request-timeout=10s >&2 || true
  return 1
}

cluster_deploy_observability() {
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
  cluster_deploy_kube delete job battlefield-kibana-setup \
    --namespace "${namespace}" \
    --ignore-not-found
  cluster_deploy_kube apply -k "${rollout_overlays:-${runtime_dir}/infra/deploy/overlays/local}/observability"
  cluster_deploy_kube apply -k "${rollout_overlays:-${runtime_dir}/infra/deploy/overlays/local}/logging-collector"
  # Wait for the new ReplicaSets, not a possibly stale Available condition.
  # Start waits together so cold image pulls share the same rollout budget.
  for deployment in "${deployments[@]}"; do
    cluster_deploy_wait_rollout "deployment/${deployment}" \
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
      cluster_deploy_report_workload_failure "deployment/${deployment}" "${namespace}"
    done
    echo "Recent ${namespace} events:" >&2
    cluster_deploy_kube get events \
      --namespace "${namespace}" \
      --sort-by=.lastTimestamp >&2 || true
    return 1
  fi
  if ! cluster_deploy_kube rollout status daemonset/battlefield-fluent-bit \
    --namespace "${logging_namespace}" \
    --timeout="${rollout_timeout}"; then
    cluster_deploy_report_workload_failure \
      daemonset/battlefield-fluent-bit "${logging_namespace}"
    return 1
  fi

  # Reinitialize Kibana against the now-ready Elasticsearch instance. This also
  # recovers the first transition from disposable storage to the persistent claim.
  cluster_deploy_kube rollout restart deployment/battlefield-kibana --namespace "${namespace}" || return
  if ! cluster_deploy_kube rollout status deployment/battlefield-kibana \
    --namespace "${namespace}" --timeout="${observability_rollout_timeout}"; then
    cluster_deploy_report_workload_failure deployment/battlefield-kibana "${namespace}"
    return 1
  fi
  echo "Creating Kibana saved objects..."
  cluster_deploy_kube apply --namespace "${namespace}" \
    -f "${runtime_dir}/infra/deploy/overlays/local/observability/kibana/setup-job.yaml" || return
  cluster_deploy_wait_kibana_setup

}

cluster_deploy_gateway() {
  echo "Deploying local HTTP gateway routes..."
  cluster_deploy_kube apply -k "${rollout_overlays:-${runtime_dir}/infra/deploy/overlays/local}/gateway"
  cluster_deploy_kube wait --for=condition=Programmed \
    gateway/battlefield-local \
    --namespace "${namespace}" \
    --timeout="${rollout_timeout}"
}

cluster_deploy_rollback_application() {
  if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
    if [[ "${current_backend_image}" != "${current_engine_image}" || "${current_backend_image}" != "${current_agent_image}" || "${current_backend_image}" != "${current_gateway_image}" ]]; then
      echo 'Previous backend digests are inconsistent; production rollback requires operator recovery.' >&2
      return 1
    fi
    "${project_dir}/scripts/cd/rollback.sh" "${current_backend_image}" "${current_frontend_image}"
    return
  fi
  local rollback_failed=0
  local rollback_started=0
  local deployment

  echo "Deployment failed; restoring the previous application revisions..." >&2

  if [[ "${rollback_overlay_ready}" == "1" ]]; then
    if ! cluster_deploy_kube apply -k "${rollback_overlay}"; then
      return 1
    fi

    for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-ai-gateway battlefield-frontend; do
      if ! cluster_deploy_kube rollout status "deployment/${deployment}" \
        --namespace "${namespace}" \
        --timeout="${rollout_timeout}"; then
        rollback_failed=1
      fi
    done

    return "${rollback_failed}"
  fi

  for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-ai-gateway battlefield-frontend; do
    local previous_revision="${previous_revisions[${deployment}]:-}"
    local current_revision
    current_revision="$(
      cluster_deploy_kube get "deployment/${deployment}" \
        --namespace "${namespace}" \
        -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}' \
        2>/dev/null || true
    )"

    if [[ -n "${previous_revision}" && "${current_revision}" != "${previous_revision}" ]]; then
      rollback_started=1
      if ! cluster_deploy_kube rollout undo "deployment/${deployment}" \
        --namespace "${namespace}" \
        --to-revision "${previous_revision}"; then
        rollback_failed=1
      fi
    fi
  done

  if [[ "${rollback_started}" == "1" ]]; then
    for deployment in battlefield-api battlefield-engine battlefield-agent battlefield-ai-gateway battlefield-frontend; do
      if ! cluster_deploy_kube rollout status "deployment/${deployment}" \
        --namespace "${namespace}" \
        --timeout="${rollout_timeout}"; then
        rollback_failed=1
      fi
    done
  fi

  return "${rollback_failed}"
}

cluster_deploy_cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if ((${#rollout_pids[@]} > 0)); then
    kill "${rollout_pids[@]}" 2>/dev/null || true
    wait "${rollout_pids[@]}" 2>/dev/null || true
  fi

  if [[ "${exit_code}" != "0" \
    && "${rollback_on_failure}" == "1" \
    && "${application_update_started}" == "1" ]]; then
    if ! cluster_deploy_rollback_application; then
      echo "Automatic application rollback did not complete successfully." >&2
    fi
  fi

  if [[ -n "${runtime_dir}" && -d "${runtime_dir}" ]]; then
    rm -rf -- "${runtime_dir}"
  fi

  exit "${exit_code}"
}

cluster_deploy_print_summary() {
  echo
  echo "Applied components (${deploy_components}) for ${image_tag} to ${CLUSTER_DRIVER:-kind} cluster ${cluster_name}."
  cluster_deploy_kube get deployments \
    --namespace "${namespace}" \
    -o custom-columns='NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas,IMAGE:.spec.template.spec.containers[0].image'
  cluster_deploy_kube get job battlefield-migrate \
    --namespace "${namespace}" \
    -o custom-columns='NAME:.metadata.name,SUCCEEDED:.status.succeeded,IMAGE:.spec.template.spec.containers[0].image' \
    2>/dev/null || true
  echo
  echo "Frontend: ${TARGET_URL:-http://battlefield.localhost:8888}"
  if [[ "${CLUSTER_DRIVER:-kind}" == k3s ]]; then
    echo 'Monitoring: use the selected K3s gateway through a private port forward (see docs/operations/k3s-delivery.md).'
  else
    echo "Grafana:  http://grafana.localhost:8888"
    echo "Kibana:   http://kibana.localhost:8888"
  fi
}
