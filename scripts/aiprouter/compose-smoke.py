#!/usr/bin/env python3
"""Disposable Compose install/backup/restore fixture. Never run against user data."""
import base64
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.request
import maintenance

ROOT = maintenance.ROOT
DIRECTORY = ROOT / 'packaging/aiprouter'


def main():
    envfile = DIRECTORY / '.env'
    if envfile.exists():
        raise RuntimeError('Refusing to overwrite an existing deployment environment')
    # A distinct project prevents this test from touching the normal Compose project.
    os.environ['COMPOSE_PROJECT_NAME'] = 'aiprouter-ci-' + secrets.token_hex(4)
    base = maintenance.COMPOSE + ['-p', os.environ['COMPOSE_PROJECT_NAME']]
    maintenance.COMPOSE = base
    compose = base + ['-f', str(DIRECTORY / 'compose.fixture.yml')]
    values = (DIRECTORY / '.env.example').read_text().replace('replace-with-random-hex', secrets.token_hex(24)).replace('replace-with-random-secret', secrets.token_hex(24)).replace('replace-with-base64-key', base64.b64encode(secrets.token_bytes(32)).decode())
    descriptor = os.open(envfile, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w') as output:
        output.write(values)
    def run(args, **kwargs):
        return subprocess.run(compose + args, check=True, **kwargs)
    try:
        run(['build'])
        run(['up', '-d', '--wait', 'postgres'])
        run(['up', '-d', 'fixtures'])
        run(['run', '--rm', '--no-deps', 'aiprouter', 'migrate'])
        run(['up', '-d', 'aiprouter', 'fixtures'])
        for attempt in range(90):
            try:
                with urllib.request.urlopen('http://127.0.0.1:8080/readyz', timeout=2) as response:
                    if response.status == 200:
                        break
            except OSError:
                pass
            time.sleep(1)
        else:
            raise RuntimeError('Service did not become ready')
        # Synthetic marker proves durable restore and temporary exclusion.
        sql = 'CREATE TABLE aiprouter.fixture_marker(value text); INSERT INTO aiprouter.fixture_marker VALUES (\'durable\'); CREATE TABLE aiprouter_temp.fixture_marker(value text); INSERT INTO aiprouter_temp.fixture_marker VALUES (\'temporary\');'
        run(['exec', '-T', 'postgres', 'psql', '-U', 'aiprouter', '-d', 'aiprouter', '-v', 'ON_ERROR_STOP=1', '-c', sql], stdout=subprocess.DEVNULL)
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / 'backup.dump'
            maintenance.backup(archive)
            with archive.open('rb') as source:
                listing = run(['exec', '-T', 'postgres', 'pg_restore', '--list'], stdin=source, stdout=subprocess.PIPE, text=False).stdout
            if b'aiprouter_temp' in listing:
                raise RuntimeError('Temporary schema leaked into backup')
            # Maintenance uses the base file with its own offline network namespace.
            maintenance.offline('restore', archive)
            actual = run(['exec', '-T', 'postgres', 'psql', '-U', 'aiprouter', '-d', 'aiprouter', '-Atc', 'SELECT value FROM aiprouter.fixture_marker'], stdout=subprocess.PIPE, text=True).stdout.strip()
            if actual != 'durable':
                raise RuntimeError('Durable restore marker missing')
            running = run(['ps', '--status', 'running', '--services'], stdout=subprocess.PIPE, text=True).stdout.splitlines()
            if 'aiprouter' in running:
                raise RuntimeError('Restore resumed admission automatically')
        print('Compose installation and backup/restore fixture passed; no live-account acceptance.')
    finally:
        try:
            run(['down', '--volumes', '--remove-orphans'])
        finally:
            envfile.unlink()


if __name__ == '__main__':
    main()
