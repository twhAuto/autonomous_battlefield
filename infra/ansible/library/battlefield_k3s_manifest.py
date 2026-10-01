#!/usr/bin/python3
"""Ansible SSA with a real, normalized Kubernetes diff in check mode."""
from ansible.module_utils.basic import AnsibleModule


def stable_diff(text):
    lines, headers = [], 0
    for line in text.splitlines():
        if line.startswith('diff -u '):
            headers = 2
            continue
        if headers and line.startswith(('--- ', '+++ ')):
            headers -= 1
            continue
        headers = 0
        lines.append(line)
    return '\n'.join(lines)


def main():
    module = AnsibleModule(argument_spec={
        'content': {'type': 'str', 'required': True},
        'sensitive': {'type': 'bool', 'default': False},
    }, supports_check_mode=True)
    # Callers mark secret-bearing tasks no_log; never echo the request or errors.
    command = ['/usr/local/bin/k3s', 'kubectl', '--kubeconfig=/etc/rancher/k3s/k3s.yaml', '--request-timeout=60s']
    rc, out, err = module.run_command(command + ['diff', '--server-side', '--field-manager=battlefield-platform', '-f', '-'], data=module.params['content'])
    if rc not in (0, 1):
        module.fail_json(msg='Kubernetes manifest diff failed', detail='[private]' if module.params['sensitive'] else err[-2000:])
    changed = rc == 1
    result = {'changed': changed}
    if not module.params['sensitive']:
        result['diff'] = {'before': '', 'after': stable_diff(out)}
    if changed and not module.check_mode:
        rc, _, _ = module.run_command(command + ['apply', '--server-side', '--field-manager=battlefield-platform', '-f', '-'], data=module.params['content'])
        if rc:
            module.fail_json(msg='Kubernetes manifest apply failed; no private output published')
    module.exit_json(**result)


if __name__ == '__main__':
    main()
