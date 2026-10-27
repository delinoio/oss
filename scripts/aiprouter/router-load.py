#!/usr/bin/env python3
"""Authenticated router workload using an isolated DB and synthetic upstream."""
import argparse
import base64
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def ready(url, process):
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError('Fixture process exited before readiness')
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                if response.status == 200:
                    return
        except OSError:
            pass
        time.sleep(.1)
    raise RuntimeError('Fixture readiness timeout')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True)
    parser.add_argument('--diagnostics', help='Optional private file for synthetic-process logs on failure')
    parser.add_argument('--requests', type=int, default=200)
    parser.add_argument('--concurrency', type=int, default=4)
    args = parser.parse_args()
    if not os.environ.get('AIPROUTER_TEST_DATABASE_URL'):
        parser.error('AIPROUTER_TEST_DATABASE_URL must point to a disposable test PostgreSQL server with CREATE DATABASE permission')
    clean = {key: value for key, value in os.environ.items() if not key.startswith(('AIPROUTER_', 'OTEL_'))}
    clean['AIPROUTER_TEST_DATABASE_URL'] = os.environ['AIPROUTER_TEST_DATABASE_URL']
    env = dict(clean, AIPROUTER_LOAD_DATABASE_NAME='aiprouter_load_' + secrets.token_hex(8), AIPROUTER_LOAD_MASTER_KEY=base64.b64encode(secrets.token_bytes(32)).decode())
    with tempfile.TemporaryDirectory(prefix='aiprouter-load-') as directory:
        directory = Path(directory)
        for name, source in [('fixture', './scripts/aiprouter/fixture'), ('seed', './scripts/aiprouter/loadseed'), ('server', './servers/aiprouter')]:
            subprocess.run(['go', 'build', '-o', str(directory / name), source], cwd=ROOT, check=True)
        processes = []
        with (directory / 'runtime.log').open('wb') as log:
            try:
                fixture = subprocess.Popen([str(directory / 'fixture')], stdout=log, stderr=log)
                processes.append(fixture)
                ready('http://127.0.0.1:18090/healthz', fixture)
                # Raw fixture credentials remain in memory and child environment only.
                seeded = json.loads(subprocess.check_output([str(directory / 'seed')], cwd=ROOT, env=env))
                env.update(AIPROUTER_DATABASE_URL=seeded['database_url'], AIPROUTER_LISTEN_ADDR='127.0.0.1:8080', AIPROUTER_PUBLIC_URL='http://127.0.0.1:8080', AIPROUTER_OIDC_ISSUER='http://127.0.0.1:18090', AIPROUTER_OIDC_CLIENT_ID='aiprouter-fixture', AIPROUTER_SMTP_ADDR='127.0.0.1:1025', AIPROUTER_SMTP_FROM='fixture@example.invalid', AIPROUTER_R2_ENDPOINT='https://r2.example.invalid', AIPROUTER_R2_BUCKET='fixture', AIPROUTER_R2_ACCESS_KEY_ID='synthetic', AIPROUTER_R2_SECRET_ACCESS_KEY='synthetic', AIPROUTER_ENCRYPTION_KEYS=json.dumps({'v1': env['AIPROUTER_LOAD_MASTER_KEY']}), AIPROUTER_ENCRYPTION_ACTIVE_KEY='v1', AIPROUTER_OPERATORS='[]', AIPROUTER_LOAD_KEY=seeded['key'])
                server = subprocess.Popen([str(directory / 'server'), 'serve'], env=env, stdout=log, stderr=log)
                processes.append(server)
                ready('http://127.0.0.1:8080/readyz', server)
                completed = subprocess.run(['python3', str(ROOT / 'scripts/aiprouter/load.py'), '--url', 'http://127.0.0.1:8080/v1/chat/completions', '--requests', str(args.requests), '--concurrency', str(args.concurrency), '--server-pid', str(server.pid), '--output', str(Path(args.output).resolve())], cwd=ROOT, env=env)
                report = json.loads(Path(args.output).read_text())
                report['workload'].update(target='authenticated-router', upstream='synthetic HTTP provider', seeded_users=seeded['users'], seeded_organizations=seeded['organizations'], active_user_scopes=1, rpm_limit=seeded['rpm'], concurrency_limit=seeded['concurrency_limit'])
                Path(args.output).write_text(json.dumps(report, indent=2) + '\n')
                if completed.returncode:
                    raise RuntimeError('Router load reported errors; inspect the sanitized report')
            except Exception:
                if args.diagnostics:
                    log.flush()
                    fd = os.open(args.diagnostics, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                    with os.fdopen(fd, 'wb') as destination, (directory / 'runtime.log').open('rb') as source:
                        shutil.copyfileobj(source, destination)
                raise
            finally:
                for process in reversed(processes):
                    if process.poll() is None:
                        process.terminate()
                        try:
                            process.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            process.kill()
                            process.wait()
                subprocess.run([str(directory / 'seed'), 'drop'], cwd=ROOT, env=env, check=True)


if __name__ == '__main__':
    main()
