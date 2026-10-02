#!/usr/bin/python
"""Reconcile the explicitly registered Cloudflare boundary; never rotate credentials."""
from pathlib import Path
from ansible.module_utils.basic import AnsibleModule
from ansible.module_utils.battlefield_cloudflare import API, APIError, Engine, read_private, write_private
from ansible.module_utils.battlefield_cloudflare_policy import require


def main():
    module = AnsibleModule(argument_spec={
        'inputs_file': {'type': 'path', 'required': True},
    }, supports_check_mode=False)
    try:
        # Remote API strings are JSON data, never Ansible/Jinja variables.
        inputs = read_private(Path(module.params['inputs_file']))
        require(set(inputs) == {'cloudflare_' + k for k in ('action', 'token', 'config', 'state_root',
                'revision', 'machine_id', 'review_id', 'report')}, 'Malformed private Cloudflare inputs')
        args = {k.removeprefix('cloudflare_'): v for k, v in inputs.items()}
        require(args['action'] in ('plan', 'apply', 'verify'), 'Unsupported Cloudflare action')
        require(not Path(args['report']).exists() and not Path(args['report']).is_symlink(), 'Report already exists')
        engine = Engine(API(args['token']), args['config'], args['state_root'], args['revision'], args['machine_id'])
        result = engine.apply(args['review_id']) if args['action'] == 'apply' else getattr(engine, args['action'])()
        write_private(Path(args['report']), result)
        module.exit_json(changed=result.get('changed', False))
    except (ValueError, OSError, KeyError, TypeError, AttributeError) as error:
        # Only locally authored messages may reach operator logs; never serialize API bodies.
        message = str(error) if type(error) is ValueError or isinstance(error, APIError) else 'Cloudflare validation failed (' + type(error).__name__ + ')'
        module.fail_json(msg=message)


if __name__ == '__main__':
    main()
