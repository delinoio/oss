#!/usr/bin/env python3
"""Offline Compose maintenance. Never restarts admission after a mutation."""
import argparse
import os
import re
import stat
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
COMPOSE = ['docker', 'compose', '--project-directory', str(ROOT / 'packaging/tairouter'),
           '-f', str(ROOT / 'packaging/tairouter/compose.yml')]


def run(args, **kwargs):
    # Do not echo environment values, archive contents, or credentials.
    return subprocess.run(COMPOSE + args, check=True, **kwargs)


def backup(destination):
    destination = Path(destination).resolve()
    if destination.exists():
        raise ValueError('Backup destination already exists')
    fd, name = tempfile.mkstemp(prefix='.tairouter-backup-', dir=destination.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            run(['exec', '-T', 'postgres', 'pg_dump', '-U', 'tairouter', '-d', 'tairouter',
                 '--format=custom', '--exclude-schema=tairouter_temp', '--no-owner', '--no-acl'], stdout=output)
            output.flush()
            os.fsync(output.fileno())
        # Atomic no-clobber publication. Temporary raw state and master keys are excluded.
        os.link(name, destination)
    finally:
        os.unlink(name)


def offline(command, archive=None):
    run(['stop', 'tairouter'])
    if command == 'restore':
        # Temporary foreign keys refer to durable tables; discard them before restore.
        run(['exec', '-T', 'postgres', 'psql', '-U', 'tairouter', '-d', 'tairouter',
             '-v', 'ON_ERROR_STOP=1', '-c', 'DROP SCHEMA IF EXISTS tairouter_temp CASCADE'])
        with Path(archive).open('rb') as source:
            run(['exec', '-T', 'postgres', 'pg_restore', '-U', 'tairouter', '-d', 'tairouter',
                 '--clean', '--if-exists', '--single-transaction', '--exit-on-error',
                 '--no-owner', '--no-acl'], stdin=source)
        # This must succeed before any serve command. A failure leaves admission stopped.
        run(['run', '--rm', '--no-deps', 'tairouter', 'migrate'])
        run(['run', '--rm', '--no-deps', 'tairouter', 'restore-invalidate'])
    elif command == 'rotate':
        run(['run', '--rm', '--no-deps', 'tairouter', 'reencrypt'])
    elif command == 'upgrade':
        run(['run', '--rm', '--no-deps', 'tairouter', 'migrate'])
    print('Maintenance completed. Server remains stopped. Review configuration, then explicitly start it.')


def admission(command, instance=None, proof_file=None, instance_stopped=False, upstreams_stopped=False):
    executable = ['run', '--rm', '--no-deps', '-T', 'tairouter']
    if command == 'admission-status':
        run(executable + [command])
        return
    if not instance or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_-]{0,127}', instance):
        raise ValueError('A valid boot instance ID is required')
    if not proof_file:
        raise ValueError('A private proof file is required')
    path = Path(proof_file).absolute()
    if command == 'admission-drain':
        # Exclusive creation refuses overwrites and symlinks, even when broken.
        fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            with os.fdopen(fd, 'w+b') as output:
                run(executable + [command, instance], stdout=output, stderr=subprocess.PIPE)
                output.flush()
                os.fsync(output.fileno())
                output.seek(0)
                if not re.fullmatch(rb'[A-Za-z0-9_-]{20,4096}\n?', output.read(4098)):
                    raise ValueError('Invalid drain response; drain again to a new file')
        except BaseException:
            path.unlink(missing_ok=True)
            raise
        print('Instance drained. Proof saved privately. Independently stop this boot and verify all upstream executions have stopped before reconciliation.')
        return
    if command != 'admission-reconcile':
        raise ValueError('Unknown admission command')
    if not instance_stopped or not upstreams_stopped:
        raise ValueError('Both instance-stopped and upstreams-stopped confirmations are required')
    # Read from an opened descriptor: refuse symlinks, special files, shared access,
    # or another user's proof before stopping anything or sending recovery data.
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as source:
        metadata = os.fstat(source.fileno())
        if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != os.getuid() or metadata.st_mode & 0o077 or metadata.st_nlink != 1:
            raise ValueError('Proof must be an owner-only regular file owned by this user')
        proof = source.read(4098)
        if not re.fullmatch(rb'[A-Za-z0-9_-]{20,4096}\n?', proof):
            raise ValueError('Invalid proof file')
    run(['stop', 'tairouter'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    # Never place proof in argv/environment or relay process output to logs.
    run(executable + [command, instance, '--upstreams-stopped'], input=proof,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    current = path.stat(follow_symlinks=False)
    if (current.st_dev, current.st_ino) == (metadata.st_dev, metadata.st_ino):
        path.unlink()
    print('Terminated instance reconciled. Proof consumed. Server remains stopped; start a new boot explicitly when ready.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['backup', 'restore', 'rotate', 'upgrade', 'admission-status', 'admission-drain', 'admission-reconcile'])
    parser.add_argument('archive', nargs='?', metavar='TARGET', help='Backup archive path or admission boot instance ID')
    parser.add_argument('--proof-file', help='New private output file for drain; existing private input file for reconcile')
    parser.add_argument('--confirm-instance-stopped', action='store_true', help='Confirm the selected boot instance is stopped and cannot resume')
    parser.add_argument('--confirm-upstreams-stopped', action='store_true', help='Independently confirm all of that boot instance upstream executions have stopped')
    args = parser.parse_args()
    if args.command == 'admission-reconcile' and not (args.confirm_instance_stopped and args.confirm_upstreams_stopped):
        parser.error('reconciliation requires --confirm-instance-stopped and --confirm-upstreams-stopped after independent verification')
    if args.command.startswith('admission-'):
        admission(args.command, args.archive, args.proof_file, args.confirm_instance_stopped, args.confirm_upstreams_stopped)
        return
    if args.command in ('backup', 'restore') and not args.archive:
        parser.error('backup/restore requires an archive path')
    if args.command == 'backup':
        backup(args.archive)
    else:
        offline(args.command, args.archive)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError):
        print('Maintenance failed; do not start admission. Inspect local command diagnostics.', file=sys.stderr)
        sys.exit(1)
