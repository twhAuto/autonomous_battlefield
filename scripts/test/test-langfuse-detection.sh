#!/usr/bin/env bash
# Verify automatic installation detection and explicit overrides without cluster writes.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
export KUBERNETES_CONTEXT=test-context KUBERNETES_NAMESPACE=battlefield
unset LANGFUSE_ENABLED
kubectl() { printf 'configmap/battlefield-langfuse-links\n'; }
[[ "$(battlefield_env_langfuse_enabled)" == 1 ]]
kubectl() { return 0; }
[[ "$(battlefield_env_langfuse_enabled)" == 0 ]]
kubectl() { return 7; }
if battlefield_env_langfuse_enabled >/dev/null; then
  echo 'Cluster errors must not silently disable tracing.' >&2; exit 1
fi
for setting in 0 1; do
  [[ "$(LANGFUSE_ENABLED="${setting}" battlefield_env_langfuse_enabled)" == "${setting}" ]]
done
if LANGFUSE_ENABLED=invalid battlefield_env_langfuse_enabled >/dev/null 2>&1; then
  echo 'Invalid override accepted.' >&2; exit 1
fi
echo 'Langfuse auto-detection, overrides and connection errors passed.'
