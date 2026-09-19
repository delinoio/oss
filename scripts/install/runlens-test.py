#!/usr/bin/env python3
"""Exercise native installation with explicit verification doubles, never release certification."""
import argparse
import hashlib
import os
from pathlib import Path
import platform
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]

def run(directory, fixture):
    with tempfile.TemporaryDirectory(prefix='runlens-installer-test-') as owned:
        owned = Path(owned)
        install = owned / 'installed'
        archives = owned / 'archives'
        archives.mkdir()
        names = list(directory.glob('runlens-*'))
        archives_list = [file for file in names if file.name.endswith(('.zip', '.tar.gz'))]
        assert len(archives_list) == 1, 'installer fixture requires one native archive'
        archive = archives_list[0]
        (archives / archive.name).write_bytes(archive.read_bytes())
        (archives / (archive.name + '.sigstore.json')).write_text('{}')
        (archives / 'SHA256SUMS.sigstore.json').write_text('{}')
        checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
        (archives / 'SHA256SUMS').write_text(f'{checksum}  {archive.name}\n')
        env = dict(os.environ)
        if platform.system() == 'Windows':
            harness = owned / 'verify.ps1'
            harness.write_text('''param([string]$Installer,[string]$Destination,[string]$Archives)
function global:cosign { $global:LASTEXITCODE = [int]$env:RUNLENS_VERIFY_RESULT }
& $Installer -Version '0.1.0' -InstallDir $Destination -ArchiveDir $Archives
''')
            command = ['pwsh', '-NoProfile', '-File', str(harness), str(ROOT / 'scripts/install/runlens.ps1'), str(install), str(archives)]
            binary = install / 'runlens.exe'
        else:
            tools = owned / 'tools'
            tools.mkdir()
            verifier = tools / 'cosign'
            verifier.write_text('#!/bin/sh\nexit "${RUNLENS_VERIFY_RESULT:-1}"\n')
            verifier.chmod(0o700)
            env['PATH'] = str(tools) + os.pathsep + env['PATH']
            command = ['sh', str(ROOT / 'scripts/install/runlens.sh'), '--version', '0.1.0', '--install-dir', str(install), '--archive-dir', str(archives)]
            binary = install / 'runlens'
        env['RUNLENS_VERIFY_RESULT'] = '1'
        assert subprocess.run(command, env=env).returncode != 0
        assert not binary.exists(), 'failed authentication must not install'
        env['RUNLENS_VERIFY_RESULT'] = '0'
        subprocess.run(command, env=env, check=True)
        before = binary.read_bytes()
        subprocess.run([binary, '--version'], check=True)
        working = owned / 'workspace'
        working.mkdir()
        (working / 'input.txt').write_text('installer native tracing fixture')
        subprocess.run([binary, 'run', '--', str(fixture), 'read-write'], cwd=working, check=True)
        # Tampering must preserve the installed version and leave no staging executable.
        with (archives / archive.name).open('ab') as file:
            file.write(b'tampered')
        assert subprocess.run(command, env=env).returncode != 0
        assert binary.read_bytes() == before
        assert not list(install.glob('.runlens*'))
        assert not list(working.glob('*.json')), 'installation tracing must not create history'
        print('Native installer fixture passed; cryptographic authenticity was mocked.')

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', required=True, type=Path)
    parser.add_argument('--fixture', required=True, type=Path)
    args = parser.parse_args()
    run(args.directory.resolve(), args.fixture.resolve())
