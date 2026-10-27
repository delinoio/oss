import importlib.util
import contextlib
import io
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('maintenance', Path(__file__).with_name('maintenance.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class MaintenanceTest(unittest.TestCase):
    def test_restore_orders_invalidation_before_any_restart(self):
        with tempfile.NamedTemporaryFile() as archive, patch.object(m, 'run') as run:
            m.offline('restore', archive.name)
        calls = [call.args[0] for call in run.call_args_list]
        self.assertEqual(calls[0], ['stop', 'aiprouter'])
        self.assertIn('--single-transaction', calls[2])
        self.assertEqual(calls[-1][-1], 'restore-invalidate')
        self.assertFalse(any('start' in call or 'up' in call for call in calls))

    def test_failed_restore_never_starts_or_invalidates_partial_restore(self):
        with tempfile.NamedTemporaryFile() as archive, patch.object(m, 'run', side_effect=[None, None, subprocess.CalledProcessError(1, 'pg_restore')]) as run:
            with self.assertRaises(subprocess.CalledProcessError):
                m.offline('restore', archive.name)
        self.assertEqual(run.call_count, 3)

    def test_backup_excludes_temp_and_does_not_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'backup.dump'
            with patch.object(m, 'run') as run:
                m.backup(path)
            self.assertIn('--exclude-schema=aiprouter_temp', run.call_args.args[0])
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                m.backup(path)

    def test_failed_backup_is_not_published(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(m, 'run', side_effect=subprocess.CalledProcessError(1, 'pg_dump')):
            path = Path(directory) / 'backup.dump'
            with self.assertRaises(subprocess.CalledProcessError):
                m.backup(path)
            self.assertEqual(list(Path(directory).iterdir()), [])

    def test_rotation_never_starts_server(self):
        with patch.object(m, 'run') as run:
            m.offline('rotate')
        self.assertEqual(run.call_args_list[0].args[0], ['stop', 'aiprouter'])
        self.assertEqual(run.call_args_list[-1].args[0][-1], 'reencrypt')


class AdmissionMaintenanceTest(unittest.TestCase):
    proof = b'synthetic-proof-01234567890123456789\n'

    def test_drain_captures_proof_in_private_file_without_logging(self):
        def child(args, **kwargs):
            self.assertEqual(args[-2:], ['admission-drain', 'boot-1'])
            self.assertEqual(kwargs['stderr'], subprocess.PIPE)
            kwargs['stdout'].write(self.proof)
        with tempfile.TemporaryDirectory() as directory, patch.object(m, 'run', side_effect=child):
            path = Path(directory) / 'proof'
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                m.admission('admission-drain', 'boot-1', path)
            self.assertEqual(path.read_bytes(), self.proof)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertNotIn(self.proof.decode().strip(), output.getvalue())

    def test_drain_refuses_existing_path_without_rotating_proof(self):
        with tempfile.NamedTemporaryFile() as source, patch.object(m, 'run') as run:
            with self.assertRaises(FileExistsError):
                m.admission('admission-drain', 'boot-1', source.name)
            run.assert_not_called()

    def test_reconcile_requires_both_confirmations_before_effects(self):
        for stopped, upstreams in [(False, False), (True, False), (False, True)]:
            with patch.object(m, 'run') as run:
                with self.assertRaises(ValueError):
                    m.admission('admission-reconcile', 'boot-1', '/absent', stopped, upstreams)
                run.assert_not_called()

    def test_reconcile_passes_only_stdin_and_removes_consumed_proof(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(m, 'run') as run:
            path = Path(directory) / 'proof'
            path.write_bytes(self.proof)
            path.chmod(0o600)
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                m.admission('admission-reconcile', 'boot-1', path, True, True)
            self.assertEqual(run.call_args_list[0].args[0], ['stop', 'aiprouter'])
            call = run.call_args_list[1]
            self.assertEqual(call.args[0][-3:], ['admission-reconcile', 'boot-1', '--upstreams-stopped'])
            self.assertEqual(call.kwargs['input'], self.proof)
            self.assertEqual(call.kwargs['stdout'], subprocess.PIPE)
            self.assertEqual(call.kwargs['stderr'], subprocess.PIPE)
            self.assertNotIn(self.proof.decode().strip(), str(call.args))
            self.assertNotIn(self.proof.decode().strip(), output.getvalue())
            self.assertFalse(path.exists())
            self.assertNotIn('restore-invalidate', str(run.call_args_list))

    def test_failed_reconcile_keeps_private_proof_for_retry(self):
        with tempfile.NamedTemporaryFile() as source, patch.object(m, 'run', side_effect=[None, subprocess.CalledProcessError(1, 'server')]):
            source.write(self.proof)
            source.flush()
            with self.assertRaises(subprocess.CalledProcessError):
                m.admission('admission-reconcile', 'boot-1', source.name, True, True)
            self.assertEqual(Path(source.name).read_bytes(), self.proof)

    def test_reconcile_refuses_world_readable_and_symlink_proofs(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(m, 'run') as run:
            path = Path(directory) / 'proof'
            path.write_bytes(self.proof)
            path.chmod(0o644)
            with self.assertRaises(ValueError):
                m.admission('admission-reconcile', 'boot-1', path, True, True)
            path.chmod(0o600)
            link = Path(directory) / 'link'
            link.symlink_to(path)
            with self.assertRaises(OSError):
                m.admission('admission-reconcile', 'boot-1', link, True, True)
            run.assert_not_called()

    def test_invalid_drain_output_removes_file(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(m, 'run'):
            path = Path(directory) / 'proof'
            with self.assertRaises(ValueError):
                m.admission('admission-drain', 'boot-1', path)
            self.assertFalse(path.exists())


if __name__ == '__main__':
    unittest.main()
