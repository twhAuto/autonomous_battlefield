#!/usr/bin/env python3
"""Serialize registered-operator mutations; descendants may inherit the held lock."""
import argparse
from contextlib import contextmanager
import fcntl
import os
from pathlib import Path
import signal
import stat
import subprocess
import sys

ENV = 'BATTLEFIELD_OPERATION_LOCK_FD'


def lock_path():
    directory = Path.home() / '.local/state/battlefield'
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    info = directory.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError('Operation-lock directory must be private and owned by this operator')
    return directory / 'bootstrap.lock'


def inherited():
    value = os.environ.get(ENV, '')
    if not value.isdigit():
        return None
    try:
        fd = int(value)
        info, expected = os.fstat(fd), lock_path().lstat()
        if not stat.S_ISREG(info.st_mode) or (info.st_dev, info.st_ino) != (expected.st_dev, expected.st_ino):
            raise ValueError('Inherited operation lock does not identify this operator lock')
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return fd
    except (OSError, ValueError) as error:
        raise ValueError('Inherited operation lock is invalid or unavailable') from error


@contextmanager
def acquire():
    existing = inherited()
    if existing is not None:
        yield existing
        return
    fd = os.open(lock_path(), os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise ValueError('Operation lock must be a private regular file owned by this operator')
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise ValueError('Another platform/deployment operation owns this host; retry after it finishes') from error
        yield fd
    finally:
        os.close(fd)


@contextmanager
def supervised(command, **kwargs):
    """Install cancellation before spawn, deferring a signal until Popen returns."""
    child = None
    ready = False
    cancelled = False

    def interrupted(signum, frame):
        nonlocal cancelled
        cancelled = True
        if ready:
            raise KeyboardInterrupt

    previous = {sig: signal.signal(sig, interrupted) for sig in (signal.SIGINT, signal.SIGTERM)}
    try:
        child = subprocess.Popen(command, start_new_session=True, **kwargs)
        ready = True
        if cancelled:
            raise KeyboardInterrupt
        yield child
    finally:
        # A second cancellation must not interrupt cleanup or leave the lock owner alive.
        ready = False
        try:
            if child is not None and child.poll() is None:
                try:
                    os.killpg(child.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    child.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)


def run(command, fd, quiet=False):
    env = dict(os.environ, **{ENV: str(fd)})
    with supervised(command, env=env, pass_fds=(fd,),
                    stdout=subprocess.DEVNULL if quiet else None,
                    stderr=subprocess.DEVNULL if quiet else None) as child:
        code = child.wait()
        return 128 - code if code < 0 else code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Validate an inherited lock without running a command')
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.check:
        if args.command:
            parser.error('--check does not accept a command')
        if inherited() is None:
            raise ValueError('Missing inherited operation lock')
        return 0
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not command:
        parser.error('Expected -- COMMAND [ARGS]')
    with acquire() as fd:
        return run(command, fd)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError) as error:
        print('Operation lock: ' + (str(error) if isinstance(error, ValueError) else 'Unable to lock or run command'), file=sys.stderr)
        sys.exit(1)
