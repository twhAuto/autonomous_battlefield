"""Keep synthetic-fixture failures private while emitting the normal safe report."""
import importlib.util
import json
import os
from pathlib import Path

spec = importlib.util.spec_from_file_location('report', Path(__file__).resolve().parents[3] / 'infra/ansible/callback_plugins/battlefield_report.py')
report = importlib.util.module_from_spec(spec)
spec.loader.exec_module(report)


class CallbackModule(report.CallbackModule):
    CALLBACK_NAME = 'rehearsal_report'

    def v2_runner_on_failed(self, result, ignore_errors=False):
        super().v2_runner_on_failed(result, ignore_errors)
        if not result._task.no_log and not result._result.get('_ansible_no_log'):
            path = Path(os.environ['BATTLEFIELD_REHEARSAL_DIAGNOSTIC'])
            fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, 'a') as stream:
                json.dump({'task': result._task.get_name(), 'result': result._result}, stream, default=str)
                stream.write('\n')
