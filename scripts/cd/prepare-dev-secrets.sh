#!/usr/bin/env bash
# Preserve dev PostgreSQL credentials; create independent dev login/capability secrets.
set -Eeuo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "${DEPLOY_ENV:-dev}" == dev ]] || { echo 'Dev secret preparation cannot target production.' >&2; exit 1; }
battlefield_env_preflight
dev_env="${BATTLEFIELD_SECRET_FILE}"
if [[ -f "${dev_env}" ]]; then
  node "${project_dir}/scripts/lib/application-env.cjs" validate "${dev_env}"
  echo 'Project .env already exists; keeping its settings unchanged.'
  exit 0
fi
staging="$(mktemp -d)"
trap 'rm -rf -- "${staging}"' EXIT
BATTLEFIELD_SECRET_FILE="${staging}/database.env"
BATTLEFIELD_AUTH_SECRET_FILE="${staging}/auth.env"
BATTLEFIELD_AI_SECRET_FILE="${staging}/ai.env"
# If a private file was lost, preserve the live login/provider secrets too.
for component in auth ai; do
  if [[ ! -f "${staging}/${component}.env" ]]; then
    kubectl --context "${KUBERNETES_CONTEXT}" -n battlefield get secret "battlefield-${component}" --ignore-not-found -o json >"${staging}/${component}.json"
    if [[ -s "${staging}/${component}.json" ]]; then
      node "${project_dir}/scripts/lib/application-env.cjs" decode "${staging}/${component}.json" "${staging}/${component}.env"
    fi
  fi
done
for target in "${BATTLEFIELD_SECRET_FILE}" "${BATTLEFIELD_AUTH_SECRET_FILE}" "${BATTLEFIELD_AI_SECRET_FILE}"; do
  mkdir -p "$(dirname -- "${target}")"
done
if [[ ! -f "${BATTLEFIELD_SECRET_FILE}" ]]; then
  existing="${staging}/existing.json"
  kubectl --context "${KUBERNETES_CONTEXT}" -n battlefield get secret battlefield-database --ignore-not-found -o json >"${existing}"
  if [[ -s "${existing}" ]]; then
    node - "${existing}" "${BATTLEFIELD_SECRET_FILE}" <<'JS'
const fs = require('fs');
const data = JSON.parse(fs.readFileSync(process.argv[2], 'utf8')).data;
if (!data?.DATABASE_URL || !data?.POSTGRES_PASSWORD) throw new Error('Existing database Secret is incomplete; refusing to replace it.');
const lines = Object.entries(data).map(([key,value]) => {
  const decoded = Buffer.from(value, 'base64').toString('utf8');
  if (!/^[A-Z_][A-Z0-9_]*$/.test(key) || /[\r\n\0]/.test(decoded)) throw new Error('Secret cannot be represented as env file.');
  return `${key}=${decoded}\n`;
});
fs.writeFileSync(process.argv[3], lines.join(''), {mode:0o600, flag:'wx'});
JS
  else
    # Never invent credentials for an existing persistent database.
    pvcs="$(kubectl --context "${KUBERNETES_CONTEXT}" -n battlefield get pvc -o name)"
    [[ -z "${pvcs}" ]] || { echo 'Database Secret missing while PVCs exist; recover credentials instead of replacing them.' >&2; exit 1; }
    password="$(node -e 'process.stdout.write(require("crypto").randomBytes(24).toString("hex"))')"
    printf 'POSTGRES_DB=battlefield\nPOSTGRES_USER=battlefield\nPOSTGRES_PASSWORD=%s\nDATABASE_URL=postgres://battlefield:%s@battlefield-postgres:5432/battlefield?sslmode=disable\n' "${password}" "${password}" >"${BATTLEFIELD_SECRET_FILE}"
  fi
fi
node - "${BATTLEFIELD_AUTH_SECRET_FILE}" "${BATTLEFIELD_AI_SECRET_FILE}" <<'JS'
const fs = require('fs'), crypto = require('crypto');
const random = () => crypto.randomBytes(32).toString('hex');
if (!fs.existsSync(process.argv[2])) fs.writeFileSync(process.argv[2],
  `BATTLEFIELD_AUTH_MODE=local-password\nBATTLEFIELD_AUTH_ENABLED=true\nBATTLEFIELD_ADMIN_USERNAME=admin\nBATTLEFIELD_ADMIN_PASSWORD=${random()}\nBATTLEFIELD_SESSION_SECRET=${random()}\nBATTLEFIELD_AUTH_COOKIE_SECURE=false\n`, {mode:0o600, flag:'wx'});
if (!fs.existsSync(process.argv[3])) fs.writeFileSync(process.argv[3],
  `BATTLEFIELD_AGENT_SECRET=${random()}\nBATTLEFIELD_MCP_CAPABILITY_SECRET=${random()}\nOPENAI_API_KEY=\nOPENAI_MODEL=\nGEMINI_API_KEY=\nGEMINI_MODEL=\nANTHROPIC_API_KEY=\nANTHROPIC_MODEL=\nOPENAI_PROFILE_AVAILABLE=false\nGEMINI_PROFILE_AVAILABLE=false\nANTHROPIC_PROFILE_AVAILABLE=false\nCODEX_CLI_PROFILE_AVAILABLE=false\nGEMINI_CLI_PROFILE_AVAILABLE=false\nCLAUDE_CLI_PROFILE_AVAILABLE=false\n`, {mode:0o600, flag:'wx'});
const existingAI = fs.readFileSync(process.argv[3], 'utf8');
if (!/^BATTLEFIELD_AGENT_SECRET=.+$/m.test(existingAI)) fs.appendFileSync(process.argv[3], `\nBATTLEFIELD_AGENT_SECRET=${random()}\n`);
JS
chmod 600 "${BATTLEFIELD_SECRET_FILE}" "${BATTLEFIELD_AUTH_SECRET_FILE}" "${BATTLEFIELD_AI_SECRET_FILE}"
node "${project_dir}/scripts/lib/application-env.cjs" merge "${staging}/combined.env" "${BATTLEFIELD_SECRET_FILE}" "${BATTLEFIELD_AUTH_SECRET_FILE}" "${BATTLEFIELD_AI_SECRET_FILE}"
node "${project_dir}/scripts/lib/application-env.cjs" validate "${staging}/combined.env"
node "${project_dir}/scripts/lib/application-env.cjs" merge "${dev_env}" "${staging}/combined.env"
echo 'Project .env is ready. Existing cluster credentials were preserved.'
