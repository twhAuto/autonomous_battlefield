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
python3 "${project_dir}/scripts/test/test-vm.py"
python3 "${project_dir}/scripts/test/check-vm-terraform.py"

ANSIBLE_CONFIG="${project_dir}/infra/ansible/ansible.cfg" "${BATTLEFIELD_TEST_ANSIBLE_BIN}/ansible-playbook" \
  -i "${project_dir}/infra/ansible/syntax-inventory.ini" \
  "${project_dir}/infra/ansible/k3s.yml" --syntax-check
python3 "${project_dir}/scripts/test/test-k3s.py"

ANSIBLE_CONFIG="${project_dir}/infra/ansible/ansible.cfg" "${BATTLEFIELD_TEST_ANSIBLE_BIN}/ansible-playbook" \
  -i "${project_dir}/infra/ansible/syntax-inventory.ini" \
  "${project_dir}/infra/ansible/cli.yml" --syntax-check
python3 "${project_dir}/scripts/test/test-cli-bootstrap.py"
ANSIBLE_CONFIG="${project_dir}/infra/ansible/ansible.cfg" "${BATTLEFIELD_TEST_ANSIBLE_BIN}/ansible-playbook" \
  -i "${project_dir}/infra/ansible/syntax-inventory.ini" \
  "${project_dir}/infra/ansible/cli-vm.yml" --syntax-check
python3 "${project_dir}/scripts/test/test-cli-vm.py"

ANSIBLE_CONFIG="${project_dir}/infra/ansible/ansible.cfg" "${BATTLEFIELD_TEST_ANSIBLE_BIN}/ansible-playbook" \
  -i "${project_dir}/infra/ansible/syntax-inventory.ini" \
  "${project_dir}/infra/ansible/ci.yml" --syntax-check
python3 "${project_dir}/scripts/test/test-ci-bootstrap.py"

ANSIBLE_CONFIG="${project_dir}/infra/ansible/ansible.cfg" "${BATTLEFIELD_TEST_ANSIBLE_BIN}/ansible-playbook" \
  -i localhost, "${project_dir}/infra/ansible/cloudflare.yml" --syntax-check
python3 "${project_dir}/scripts/test/test-cloudflare.py"

ANSIBLE_CONFIG="${project_dir}/infra/ansible/ansible.cfg" "${BATTLEFIELD_TEST_ANSIBLE_BIN}/ansible-playbook" \
  -i "${project_dir}/infra/ansible/syntax-inventory.ini" "${project_dir}/infra/ansible/https.yml" --syntax-check
python3 "${project_dir}/scripts/test/test-local-https.py"
