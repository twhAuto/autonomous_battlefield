"""Validate guest-only inputs without passing application credentials to the VM."""
import re
from ansible.errors import AnsibleFilterError


def validate_guest(environment, config, adopt, artifacts):
    try:
        if not isinstance(environment, str) or len(environment) > 2048:
            raise ValueError()
        pairs = [line.split('=', 1) for line in environment.splitlines() if line and not line.startswith('#')]
        if any(len(pair) != 2 for pair in pairs) or len(pairs) != 3:
            raise ValueError()
        values = dict(pairs)
        if set(values) != {'BATTLEFIELD_AGENT_SECRET', 'BATTLEFIELD_ENV', 'BATTLEFIELD_CLI_ISOLATION_VERIFIED'}:
            raise ValueError()
        if (not re.fullmatch(r'[A-Za-z0-9_+/=-]{32,512}', values['BATTLEFIELD_AGENT_SECRET'])
                or values['BATTLEFIELD_ENV'] != 'production'
                or values['BATTLEFIELD_CLI_ISOLATION_VERIFIED'] not in ('true', 'false')):
            raise ValueError()
        ai = config['ai']
        if (set(config) != {'ai'} or ai['mcpUrl'] != 'http://192.168.234.1:18084/mcp'
                or ai['agentHealthAddress'] != '127.0.0.1:8083'
                or not isinstance(ai.get('agentConcurrency', 1), int) or isinstance(ai.get('agentConcurrency', 1), bool)
                or not 1 <= ai.get('agentConcurrency', 1) <= 4):
            raise ValueError()
        profiles = ai['profiles']
        if not isinstance(profiles, list) or not profiles:
            raise ValueError()
        providers = {'codex-cli': {'codex', 'codex-code-mode-host'}, 'claude-cli': {'claude'}, 'antigravity-cli': {'agy'}}
        for profile in profiles:
            if profile['runtime'] != 'cli' or profile['provider'] not in providers or type(profile['enabled']) is not bool:
                raise ValueError()
            if profile['enabled'] and (not adopt or values['BATTLEFIELD_CLI_ISOLATION_VERIFIED'] != 'true'
                                       or not providers[profile['provider']] <= set(artifacts)):
                raise ValueError()
        if not adopt and values['BATTLEFIELD_CLI_ISOLATION_VERIFIED'] != 'false':
            raise ValueError()
        return True
    except (ValueError, TypeError, KeyError):
        raise AnsibleFilterError('Invalid guest inputs: require only the worker control secret, fixed private transport, declared tools, and disabled profiles/isolation for a fresh install') from None


class FilterModule:
    def filters(self):
        return {'cli_guest_inputs_valid': validate_guest}
