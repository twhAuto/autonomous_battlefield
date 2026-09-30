#!/usr/bin/env python3
"""Validate pinned Terraform/provider configuration without a libvirt connection."""
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('vm_tools_check', ROOT / 'scripts/infra/vm-tools.py')
tools = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tools)
os.umask(0o077)
cache = Path.home() / '.cache/autonomous-battlefield/terraform-check'
binary = tools.terraform(cache)
plugins = tools.private_directory(cache / 'providers')
with tempfile.TemporaryDirectory(prefix='bf-terraform-check-') as temp:
    directory = Path(temp)
    (directory / 'terraform.rc').write_text('disable_checkpoint = true\nplugin_cache_dir = ' + json.dumps(str(plugins)) + '\n')
    env = {k: v for k, v in os.environ.items() if not k.startswith(('TF_', 'LIBVIRT_'))}
    env.update(TF_DATA_DIR=str(directory / 'provider'), TF_CLI_CONFIG_FILE=str(directory / 'terraform.rc'),
               TF_IN_AUTOMATION='1', TF_INPUT='0')
    command = [str(binary), '-chdir=' + str(ROOT / 'infra/libvirt')]
    # Reproduce the long TMPDIR inherited from Jenkins without weakening its
    # workspace isolation. The shared launcher must provide short private IPC.
    long_tmp = directory / ('jenkins-workspace-' * 7) / 'tmp'
    long_tmp.mkdir(parents=True)
    env['TMPDIR'] = str(long_tmp)
    with tools.terraform_environment(env) as environment:
        for arguments in [['fmt', '-check', '-recursive'], ['init', '-backend=false', '-input=false', '-lockfile=readonly', '-no-color'], ['validate', '-no-color'], ['test', '-no-color']]:
            if arguments[0] == 'init':
                # Terraform's shared plugin cache is not concurrency-safe. Only
                # initialization writes it; serialize those writes across PRs.
                descriptor = os.open(cache / 'provider-init.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
                with os.fdopen(descriptor, 'a') as cache_lock:
                    fcntl.flock(cache_lock, fcntl.LOCK_EX)
                    subprocess.run(command + arguments, check=True, env=environment)
            else:
                subprocess.run(command + arguments, check=True, env=environment)

# Exercise the actual pinned local backend's lock on a synthetic built-in
# resource. This writes only disposable local state, never a libvirt resource.
with tempfile.TemporaryDirectory(prefix='bf-state-lock-check-') as temp:
    directory = Path(temp)
    (directory / 'main.tf').write_text('terraform {\n  backend "local" {}\n}\nresource "terraform_data" "fixture" { input = "synthetic" }\n')
    (directory / 'terraform.rc').write_text('disable_checkpoint = true\n')
    env = {k: v for k, v in os.environ.items() if not k.startswith(('TF_', 'LIBVIRT_'))}
    env.update(TF_CLI_CONFIG_FILE=str(directory / 'terraform.rc'), TF_IN_AUTOMATION='1', TF_INPUT='0')
    command = [str(binary), '-chdir=' + str(directory)]
    for arguments in [['init', '-input=false', '-no-color'], ['apply', '-auto-approve', '-input=false', '-no-color']]:
        subprocess.run(command + arguments, check=True, env=env, stdout=subprocess.DEVNULL)
    with (directory / 'terraform.tfstate').open('r+') as state:
        fcntl.lockf(state, fcntl.LOCK_EX | fcntl.LOCK_NB)
        result = subprocess.run(command + ['plan', '-input=false', '-lock-timeout=0s', '-no-color'],
                                env=env, capture_output=True, text=True)
        if result.returncode == 0 or 'state lock' not in result.stderr:
            raise RuntimeError('Terraform did not reject a competing native state lock')
    subprocess.run(command + ['plan', '-input=false', '-lock-timeout=0s', '-no-color'],
                   check=True, env=env, stdout=subprocess.DEVNULL)
print('PASS: native Terraform backend contention and release')
