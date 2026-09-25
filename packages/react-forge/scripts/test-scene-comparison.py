"""Reject partial, stale or reduced-resolution scene-render evidence."""
import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from PIL import Image

spec = importlib.util.spec_from_file_location('comparison', Path(__file__).with_name('compare-scenes.py'))
comparison = importlib.util.module_from_spec(spec)
spec.loader.exec_module(comparison)


class EvidenceTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        (self.root / 'renders').mkdir()
        buffer = io.BytesIO()
        Image.new('RGB', (2048, 2048), '#182128').save(buffer, format='PNG')
        png = buffer.getvalue()
        artifacts = []
        self.report = {'imports': []}
        for fmt in comparison.FORMATS:
            name = f'aura-stand.{fmt}'
            data = f'Fixture {fmt}'.encode()
            (self.root / name).write_bytes(data)
            digest = hashlib.sha256(data).hexdigest()
            artifacts.append({'file': name, 'sha256': digest})
            item = {'file': name, 'sha256': digest, 'renders': []}
            for view in comparison.VIEWS:
                image_name = f'aura-stand-{fmt}-{view}.png'
                (self.root / 'renders' / image_name).write_bytes(png)
                item['renders'].append({'file': image_name, 'sha256': hashlib.sha256(png).hexdigest(), 'width': 2048, 'height': 2048})
            self.report['imports'].append(item)
        (self.root / 'generation.json').write_text(json.dumps({'artifacts': artifacts}))
        self.save_report()

    def save_report(self):
        (self.root / 'renders/render-report.json').write_text(json.dumps(self.report))

    def test_complete_pair_produces_four_comparisons(self):
        comparison.compare(self.root, ['stand'])
        self.assertEqual(len(json.loads((self.root / 'pixel-comparison.json').read_text())), 4)
        self.assertTrue((self.root / 'comparison-stand.jpg').is_file())

    def test_incomplete_or_duplicate_imports(self):
        original = copy.deepcopy(self.report)
        for imports in [original['imports'][:1], [original['imports'][0]] * 2]:
            self.report['imports'] = imports
            self.save_report()
            with self.assertRaisesRegex(ValueError, 'product imports'):
                comparison.verify_evidence(self.root, ['stand'])

    def test_incomplete_or_duplicate_views(self):
        original = copy.deepcopy(self.report)
        for renders in [original['imports'][0]['renders'][:3], [original['imports'][0]['renders'][0]] * 4]:
            self.report['imports'][0]['renders'] = renders
            self.save_report()
            with self.assertRaisesRegex(ValueError, 'rendered views'):
                comparison.verify_evidence(self.root, ['stand'])

    def test_changed_export(self):
        (self.root / 'aura-stand.fbx').write_bytes(b'Changed export')
        with self.assertRaisesRegex(ValueError, 'Export hash mismatch'):
            comparison.verify_evidence(self.root, ['stand'])

    def test_changed_render(self):
        (self.root / 'renders/aura-stand-glb-front.png').write_bytes(b'Changed image')
        with self.assertRaisesRegex(ValueError, 'Render hash mismatch'):
            comparison.verify_evidence(self.root, ['stand'])

    def test_reduced_resolution_even_with_updated_hash(self):
        image = self.report['imports'][0]['renders'][0]
        path = self.root / 'renders' / image['file']
        Image.new('RGB', (512, 512)).save(path)
        image.update(width=512, height=512, sha256=hashlib.sha256(path.read_bytes()).hexdigest())
        self.save_report()
        with self.assertRaisesRegex(ValueError, 'dimensions mismatch'):
            comparison.verify_evidence(self.root, ['stand'])


if __name__ == '__main__':
    unittest.main()
