"""Persist successful production recovery images outside the checkout, bound to a cluster."""
import json
import os
from pathlib import Path
import re
import sys
import tempfile

BACKENDS = ('battlefield-api', 'battlefield-engine', 'battlefield-agent', 'battlefield-ai-gateway')
DIGEST = re.compile(r'[a-z0-9][a-z0-9._:/-]+@sha256:[a-f0-9]{64}')


def approved(path, cluster, backend, frontend, migrations):
    if not path.exists():
        return ''
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
        raise ValueError('Recovery record must be a private regular file')
    record = json.loads(path.read_text())
    if (record.get('cluster_uid'), record.get('backend'), record.get('frontend')) != (cluster, backend, frontend):
        return ''
    try:
        live_migrations = migration_state(migrations)
    except ValueError:
        return ''
    if record.get('database_migrations') != live_migrations:
        return ''
    return backend if DIGEST.fullmatch(backend) and DIGEST.fullmatch(frontend) else ''


def migration_state(values):
    if (not isinstance(values, list) or not values or
            any(type(value) is not int or value < 0 for value in values) or values != sorted(set(values))):
        raise ValueError('Expected a nonempty ordered set of applied Goose migration versions')
    return values


def record(path, cluster, revision, state, migrations):
    if not cluster or not re.fullmatch(r'[a-f0-9]{40}', revision):
        raise ValueError('A verified cluster and full source revision are required')
    deployments = {d['metadata']['name']: d for d in state['items']}
    images = {}
    for name in (*BACKENDS, 'battlefield-frontend'):
        d = deployments[name]
        desired = d['spec'].get('replicas', 1)
        status = d['status']
        if (status.get('observedGeneration', 0) < d['metadata']['generation'] or
                status.get('updatedReplicas', 0) != desired or status.get('readyReplicas', 0) != desired):
            raise ValueError('Recovery requires completed healthy rollouts')
        if name in ('battlefield-api', 'battlefield-frontend') and desired < 1:
            raise ValueError('Recovery requires a running API and frontend')
        container = name.removeprefix('battlefield-')
        images[name] = next(c['image'] for c in d['spec']['template']['spec']['containers'] if c['name'] == container)
        if not DIGEST.fullmatch(images[name]):
            raise ValueError('Recovery requires immutable image digests')
    backend = images[BACKENDS[0]]
    if any(images[name] != backend for name in BACKENDS):
        raise ValueError('Recovery backend deployments must use the same verified digest')
    data = dict(cluster_uid=cluster, backend=backend, frontend=images['battlefield-frontend'], verified_revision=revision,
                database_migrations=migration_state(migrations))
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix='.recovery-')
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(data, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


if __name__ == '__main__':
    try:
        action, filename, cluster, first, second, database = sys.argv[1:]
        if action == 'read':
            try:
                migrations = json.loads(Path(database).read_text())
            except (ValueError, OSError):
                migrations = None
            print(approved(Path(filename), cluster, first, second, migrations))
        elif action == 'record':
            record(Path(filename), cluster, first, json.loads(Path(second).read_text()), json.loads(Path(database).read_text()))
        else:
            raise ValueError('Unknown recovery action')
    except (ValueError, KeyError, OSError, StopIteration):
        raise SystemExit('Cannot validate the production recovery record; operator recovery is required.')
