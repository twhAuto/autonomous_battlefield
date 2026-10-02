#!/usr/bin/python
"""Reconcile the explicitly registered Cloudflare boundary; never rotate credentials."""
from pathlib import Path
from ansible.module_utils.basic import AnsibleModule
from ansible.module_utils.battlefield_cloudflare import API, Engine, write_private


def main():
    module = AnsibleModule(argument_spec={
        'action': {'type': 'str', 'choices': ['plan', 'apply', 'verify'], 'required': True},
        'token': {'type': 'str', 'no_log': True, 'required': True},
        'config': {'type': 'dict', 'no_log': True, 'required': True},
        'state_root': {'type': 'path', 'required': True},
        'revision': {'type': 'str', 'required': True},
        'machine_id': {'type': 'str', 'no_log': True, 'required': True},
        'review_id': {'type': 'str', 'default': ''},
        'report': {'type': 'path', 'required': True},
    }, supports_check_mode=False)
    args = module.params
    try:
        engine = Engine(API(args['token']), args['config'], args['state_root'], args['revision'], args['machine_id'])
        result = engine.apply(args['review_id']) if args['action'] == 'apply' else getattr(engine, args['action'])()
        write_private(Path(args['report']), result)
        module.exit_json(changed=result.get('changed', False))
    except (ValueError, OSError, KeyError, TypeError, AttributeError) as error:
        # Only locally authored messages may reach operator logs; never serialize API bodies.
        message = str(error) if isinstance(error, ValueError) and not isinstance(error, (UnicodeError,)) else 'Cloudflare validation failed'
        module.fail_json(msg=message)


if __name__ == '__main__':
    main()
