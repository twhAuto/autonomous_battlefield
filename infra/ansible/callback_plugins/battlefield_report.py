"""Emit only task outcomes and an opaque change digest, never module output."""
import hashlib
import json
import os
from ansible.plugins.callback import CallbackBase


def change_values(value):
    # Template diff headers contain random controller temporary filenames.
    # Bind approval to content/metadata changes, not those display-only labels.
    if isinstance(value, dict):
        return {key: change_values(item) for key, item in value.items()
                if key not in ('before_header', 'after_header')}
    if isinstance(value, list):
        return [change_values(item) for item in value]
    return value


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'stdout'
    CALLBACK_NAME = 'battlefield_report'

    def __init__(self):
        super().__init__()
        self.tasks = []
        self.digest = hashlib.sha256()

    def record(self, result, status):
        data = result._result
        task = {'task': result._task.get_name(), 'status': status, 'changed': bool(data.get('changed', False))}
        self.tasks.append(task)
        # Secret tasks contribute their outcome only. Diff values are hashed, never emitted.
        changes = None if data.get('_ansible_no_log') or result._task.no_log else change_values(data.get('diff'))
        self.digest.update(json.dumps([task, changes], sort_keys=True, default=str).encode())

    def v2_runner_on_ok(self, result):
        self.record(result, 'ok')

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self.record(result, 'failed')

    def v2_runner_on_unreachable(self, result):
        self.record(result, 'unreachable')

    def v2_runner_on_skipped(self, result):
        self.record(result, 'skipped')

    def v2_playbook_on_stats(self, stats):
        report = {'schema': 1, 'tasks': self.tasks, 'change_digest': self.digest.hexdigest(),
                  'failed': any(t['status'] in ('failed', 'unreachable') for t in self.tasks)}
        path = os.environ['BATTLEFIELD_BOOTSTRAP_REPORT']
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as output:
            json.dump(report, output, indent=2)
            output.write('\n')
