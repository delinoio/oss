#!/usr/bin/env python3
"""Offline artifact and release gate regressions; performs no publication."""
import sys
sys.dont_write_bytecode = True
from contextlib import redirect_stdout
from unittest.mock import patch
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('release', Path(__file__).with_name('runlens.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

class ReleaseTests(unittest.TestCase):
    def test_versions_are_closed(self):
        self.assertEqual(release.version(release.source_version()), release.source_version())
        for version in ['999999.0.0', 'v0.1.0', '00.1.0', '0.1.0;echo secret', '0.1.0-beta.1']:
            with self.assertRaises(ValueError):
                release.version(version)

    def test_source_version_follows_manifest_bumps(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary)
            # A dependency version before [package] must not supply the version.
            (source / 'Cargo.toml').write_text('[dependencies.fixture]\nversion = "9.0.0"\n[package]\nversion = "0.2.1"\n')
            with patch.object(release, 'SOURCE', source), patch.object(sys, 'argv', ['runlens.py', 'source-version']):
                output = io.StringIO()
                with redirect_stdout(output):
                    release.main()
                self.assertEqual(output.getvalue(), '0.2.1\n')
                self.assertEqual(release.version('0.2.1'), '0.2.1')
                with self.assertRaises(ValueError):
                    release.version('0.1.0')

    def test_inventory_and_minimum_os_gate(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=release.ROOT, text=True).strip()
            for key in release.PLATFORMS:
                archive = root / release.asset_name(key)
                if key.startswith('windows'):
                    with zipfile.ZipFile(archive, 'w') as output:
                        output.writestr('runlens.exe', b'fixture')
                        output.writestr('LICENSES.txt', b'fixture')
                else:
                    with tarfile.open(archive, 'w:gz') as output:
                        for name in ['runlens', 'LICENSES.txt']:
                            info = tarfile.TarInfo(name)
                            info.size = 7
                            output.addfile(info, io.BytesIO(b'fixture'))
                proof = {'schema_version': 1, 'version': release.source_version(), 'commit': commit, 'platform': key,
                         'archive': archive.name, 'sha256': release.digest(archive), 'minimum_os': True,
                         'native_execution': True, 'installer_fixture': True}
                (root / f'evidence-{key}.json').write_text(json.dumps(proof))
            args = SimpleNamespace(directory=root, version=release.source_version(), dry_run=False)
            release.collect(args)
            self.assertEqual(len((root / 'SHA256SUMS').read_text().splitlines()), 6)
            release.readiness(args)
            proof_file = root / 'evidence-windows-arm64.json'
            proof = json.loads(proof_file.read_text())
            proof['minimum_os'] = False
            proof_file.write_text(json.dumps(proof))
            with self.assertRaises(ValueError):
                release.readiness(args)
            args.dry_run = True
            release.readiness(args)
            (root / release.asset_name('darwin-amd64')).unlink()
            with self.assertRaises(ValueError):
                release.inventory(root, release.source_version())

if __name__ == '__main__':
    unittest.main()
