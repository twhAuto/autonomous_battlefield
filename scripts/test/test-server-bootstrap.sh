#!/usr/bin/env bash
# Real Vault/Ansible tests only touch synthetic inputs under a private temp directory.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
BATTLEFIELD_TEST_ANSIBLE_BIN="$("${project_dir}/scripts/infra/server.sh" tools)"
export BATTLEFIELD_TEST_ANSIBLE_BIN
ANSIBLE_CONFIG="${project_dir}/infra/ansible/ansible.cfg" "${BATTLEFIELD_TEST_ANSIBLE_BIN}/ansible-playbook" \
  -i "${project_dir}/infra/ansible/syntax-inventory.ini" \
  "${project_dir}/infra/ansible/site.yml" --syntax-check
python3 "${project_dir}/scripts/test/test-server-bootstrap.py"
python3 "${project_dir}/scripts/test/test-server-rehearsal.py"
python3 "${project_dir}/scripts/test/test-data-storage.py"
python3 "${project_dir}/scripts/test/test-platform.py"
