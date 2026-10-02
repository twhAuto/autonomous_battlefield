#!/usr/bin/env python3
"""Safely scoped API lifecycle: unique unused hostname, no connector, ID-bound cleanup."""
import argparse
import copy
import importlib.util
import json
import os
from pathlib import Path
import secrets
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path = [p for p in sys.path if Path(p).resolve() != Path(__file__).resolve().parent]
spec = importlib.util.spec_from_file_location('cloudflare_operator', ROOT / 'scripts/infra/cloudflare.py')
operator = importlib.util.module_from_spec(spec); spec.loader.exec_module(operator)
cloud, policy, server = operator.cloud, operator.policy, operator.server


def cleanup(api, engine):
    """Only this fixture's confirmed IDs, after rechecking its exact configuration."""
    if not engine.owner:
        return []
    owner = cloud.read_private(engine.owner_path)
    policy.require(not owner.get('pending') and engine.config['mode'] == 'create', 'Ambiguous fixture state; preserve evidence for manual recovery')
    engine.owner = owner
    engine.observe()  # Refuse cleanup after an identity/configuration change.
    ids, c = owner['ids'], engine.config
    policy.require(c['tunnel']['name'].startswith('battlefield-ab141-test-') and
                   c['hostname'].startswith('battlefield-ab141-test-'), 'Cleanup is limited to named disposable resources')
    operations = []
    if ids['dns']: operations.append(('dns', engine.zone + '/dns_records/' + ids['dns']))
    if ids['tunnel']: operations.append(('tunnel', engine.prefix + '/cfd_tunnel/' + ids['tunnel']))
    if ids['application']: operations.append(('application', engine.prefix + '/access/apps/' + ids['application']))
    operations += [('policy', engine.prefix + '/access/policies/' + p) for p in reversed(ids['policies']) if p]
    deleted = []
    for kind, path in operations:
        api.request('DELETE', path)
        deleted.append(kind)
    for kind, path in operations:
        collection, identity = path.rsplit('/', 1)
        if kind == 'tunnel': collection += '?is_deleted=false'
        policy.require(all(item['id'] != identity for item in api.list(collection)), 'Fixture resource still exists after cleanup')
    return deleted


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--vault', required=True, type=server.safe_path)
    parser.add_argument('--vault-password-file', type=server.safe_path)
    parser.add_argument('--token-file', type=server.safe_path, default=Path.home()/'.config/autonomous-battlefield/bootstrap/cloudflare-api-token')
    parser.add_argument('--inventory', type=server.safe_path, default=Path.home()/'.config/autonomous-battlefield/server/inventory.json')
    parser.add_argument('--evidence-dir', required=True, type=server.safe_path)
    parser.add_argument('--resume', action='store_true', help='Resume only recorded confirmed fixture IDs after a reviewed fix')
    args = parser.parse_args(); os.umask(0o077)
    if not args.resume and (args.evidence_dir.exists() or args.evidence_dir.is_symlink()):
        raise ValueError('Use a new evidence directory')
    server.outside_repo(args.evidence_dir)
    evidence = cloud.private_directory(args.evidence_dir)
    if args.vault_password_file: operator.credential_path(args.vault_password_file)
    config = operator.read_vault(args.vault, server.password_args(args.vault_password_file))
    policy.require(config['mode'] == 'adopt', 'The rehearsal requires captured existing resources as a preservation baseline')
    inventory = server.load_inventory(args.inventory)
    actual = server.remote_python(inventory, 'from pathlib import Path; print(Path("/etc/machine-id").read_text().strip())').decode().strip()
    policy.require(actual == inventory['machine_id'], 'Registered physical host identity changed')
    api = cloud.API(operator.credential_path(args.token_file).read_text().strip())
    name = 'battlefield-ab141-test-' + secrets.token_hex(6)
    fresh = (cloud.read_private(evidence/'fixture.json') if args.resume else
             policy.creation(config, name + '.' + config['zone_name'], name))
    policy.require(fresh['mode'] == 'create' and fresh['hostname'].startswith('battlefield-ab141-test-') and
                   fresh['tunnel']['name'].startswith('battlefield-ab141-test-'), 'Fixture is outside the disposable namespace')
    if not args.resume: cloud.write_private(evidence/'fixture.json', fresh)
    engine = cloud.Engine(api, fresh, evidence / 'state', 'fixture', actual)
    baseline = cloud.Engine(api, config, evidence / 'baseline', 'fixture', actual)
    result = {'schema': 1, 'checks': [], 'cleaned_up': False, 'existing_resources_preserved': False}
    def record(check):
        result['checks'].append(check)
        cloud.write_private(evidence / 'result.json', result)
        print(check, flush=True)
    with server.operation_lock.acquire(), server.host_lock(inventory, False) as lock:
        before, _ = baseline.observe()
        failure = None
        try:
            # Host lock liveness is checked before each phase. An interruption retains
            # evidence; no connector/service or application delivery is ever started.
            def alive(): policy.require(lock.poll() is None, 'Host operation lock connection lost')
            api.before_request = alive
            alive(); review = engine.plan(); cloud.write_private(evidence/'plan.json', review)
            engine.apply(review['review_id']); record('create-access-before-dns')
            alive(); record('verify' if not engine.verify()['changes'] else 'unexpected-drift')
            repeat = engine.plan(); policy.require(not repeat['changes'], 'Repeat plan changed')
            policy.require(not engine.apply(repeat['review_id'])['changed'], 'Repeat apply changed')
            record('repeat-plan-apply-no-change')
            # Adopt the disposable result, with the same credentials and audience.
            adopted = copy.deepcopy(fresh); ids = engine.owner['ids']
            adopted.update(mode='adopt', credentials={**engine.owner['credentials'], 'TunnelID': ids['tunnel']})
            adopted['tunnel']['id'] = ids['tunnel']; adopted['dns']['id'] = ids['dns']
            adopted['application'].update(id=ids['application'], aud=ids['aud'])
            for p, identity in zip(adopted['policies'], ids['policies']): p['id'] = identity
            adopter = cloud.Engine(api, adopted, evidence/'adoption', 'fixture', actual)
            adopter.apply(adopter.plan()['review_id']); adopter.verify(); record('adopt-existing-identities')
        except (ValueError, OSError, KeyError, TypeError) as error:
            failure = str(error) if isinstance(error, ValueError) else type(error).__name__
            result['failure'] = failure
            cloud.write_private(evidence/'result.json', result)
            raise
        finally:
            alive()
            try:
                result['deleted'] = cleanup(api, engine)
            except (ValueError, OSError, KeyError, TypeError) as error:
                result['cleanup_failure'] = str(error) if isinstance(error, ValueError) else type(error).__name__
                cloud.write_private(evidence/'result.json', result)
                raise
            result['cleaned_up'] = True
            after, _ = baseline.observe()
            result['existing_resources_preserved'] = policy.digest(before) == policy.digest(after)
            cloud.write_private(evidence/'result.json', result)
            policy.require(result['existing_resources_preserved'], 'Existing resource baseline changed during the rehearsal')
    print('Scoped Cloudflare lifecycle completed; existing identities preserved and temporary resources removed.')


if __name__ == '__main__':
    try: main()
    except (ValueError, OSError, KeyError, TypeError) as error:
        detail = str(error) if isinstance(error, ValueError) and not isinstance(error, json.JSONDecodeError) else type(error).__name__
        print('Scoped Cloudflare lifecycle stopped: ' + detail + '; inspect private evidence.', file=sys.stderr)
        sys.exit(1)
