#!/usr/bin/env python3
"""Build and stage artifacts only. This command never publishes a release."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
from licenses import bundle

ROOT = Path(__file__).resolve().parents[2]
TARGETS = [('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'amd64'), ('darwin', 'arm64'), ('windows', 'amd64')]


def build(output):
    output = Path(output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    subprocess.run(['go', 'mod', 'download', 'all'], cwd=ROOT, check=True)
    checksums = {}
    for system, arch in TARGETS:
        name = f'aiprouter-{system}-{arch}' + ('.exe' if system == 'windows' else '')
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED='0')
        subprocess.run(['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(output / name), './cmds/aiprouter'], cwd=ROOT, env=env, check=True)
        checksums[name] = hashlib.sha256((output / name).read_bytes()).hexdigest()
        if system == 'linux':
            server = f'aiprouter-server-{system}-{arch}'
            subprocess.run(['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(output / server), './servers/aiprouter'], cwd=ROOT, env=env, check=True)
            checksums[server] = hashlib.sha256((output / server).read_bytes()).hexdigest()
    package = output / 'npm'
    shutil.copytree(ROOT / 'packaging/aiprouter/npm', package)
    (package / 'checksums.json').write_text(json.dumps(checksums, indent=2) + '\n')
    for name in ['LICENSE', 'NOTICE']:
        shutil.copy2(ROOT / name, output / name)
        shutil.copy2(ROOT / name, package / name)
    bundle(ROOT, output / 'THIRD_PARTY_NOTICES')
    shutil.copy2(output / 'THIRD_PARTY_NOTICES', package / 'THIRD_PARTY_NOTICES')
    (output / 'SHA256SUMS').write_text(''.join(f'{digest}  {name}\n' for name, digest in sorted(checksums.items())))
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    (output / 'build.json').write_text(json.dumps({'revision': revision, 'targets': TARGETS, 'native_acceptance': 'not performed', 'publication': 'not performed'}, indent=2) + '\n')
    packed = subprocess.check_output(['npm', 'pack', '--ignore-scripts', '--json', '--pack-destination', str(output)], cwd=package, text=True)
    tarball = output / json.loads(packed)[0]['filename']
    with tarfile.open(tarball) as archive:
        names = set(archive.getnames())
        required = {'package/LICENSE', 'package/NOTICE', 'package/THIRD_PARTY_NOTICES', 'package/checksums.json', 'package/bin.cjs', 'package/install.cjs', 'package/package.json'}
        if not required.issubset(names):
            raise RuntimeError('npm package lacks required license or installer files')
        if json.load(archive.extractfile('package/checksums.json')) != checksums:
            raise RuntimeError('npm checksum manifest differs from release artifacts')
    print('Cross-platform artifacts and npm package contents verified; publication not performed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', help='New output directory outside the source tree')
    build(parser.parse_args().output)
