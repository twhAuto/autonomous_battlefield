#!/usr/bin/python
"""Manage only the reviewed immutable CLI VM layout, never an application release."""
from ansible.module_utils.basic import AnsibleModule
from ansible.module_utils.battlefield_cli_vm import Engine
import subprocess


def main():
    module = AnsibleModule(argument_spec={
        'action': {'type': 'str', 'choices': ['plan', 'apply', 'verify', 'finish'], 'required': True},
        'request': {'type': 'dict', 'required': True},
        'assets': {'type': 'dict', 'required': True},
        'review_id': {'type': 'str', 'default': ''},
        'guest': {'type': 'dict', 'default': {}},
    }, supports_check_mode=False)
    try:
        engine = Engine(module.params['request'], module.params['assets'])
        action = module.params['action']
        if action == 'plan':
            result = {'changed': False, 'report': engine.plan()}
        elif action == 'apply':
            result = engine.apply(module.params['review_id'])
        elif action == 'finish':
            result = engine.finish(module.params['guest'])
        else:
            result = engine.verify()
        module.exit_json(**result)
    except (ValueError, OSError, KeyError, TypeError, AttributeError, subprocess.SubprocessError) as error:
        module.fail_json(msg=str(error) if isinstance(error, ValueError) else
                         'CLI VM validation failed; inspect the host metadata and retained creation journal')


if __name__ == '__main__':
    main()
