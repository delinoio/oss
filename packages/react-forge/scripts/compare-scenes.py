"""Verify complete actual-file evidence and produce observational comparisons."""
import argparse
import hashlib
from pathlib import Path
import json
from PIL import Image, ImageDraw, ImageChops, ImageStat

PRODUCTS = ['studio', 'headphones', 'dac', 'stand']
VIEWS = ['front', 'back', 'hero', 'detail']
FORMATS = ['glb', 'fbx']


def verify_evidence(root, products):
    """A successful shard must contain both formats and every required view."""
    generated = {a['file']: a for a in json.loads((root / 'generation.json').read_text())['artifacts']}
    report = json.loads((root / 'renders/render-report.json').read_text())
    expected = {f'aura-{product}.{fmt}' for product in products for fmt in FORMATS}
    imports = report['imports']
    if len(imports) != len(expected) or {item['file'] for item in imports} != expected:
        raise ValueError('Incomplete or duplicate product imports')
    for item in imports:
        name = item['file']
        digest = hashlib.sha256((root / name).read_bytes()).hexdigest()
        if digest != item['sha256'] or digest != generated[name]['sha256']:
            raise ValueError(f'Export hash mismatch: {name}')
        product, fmt = name.removeprefix('aura-').split('.')
        expected_views = {f'aura-{product}-{fmt}-{view}.png' for view in VIEWS}
        images = item['renders']
        if len(images) != len(expected_views) or {i['file'] for i in images} != expected_views:
            raise ValueError(f'Incomplete or duplicate rendered views: {name}')
        for rendered in images:
            path = root / 'renders' / rendered['file']
            if hashlib.sha256(path.read_bytes()).hexdigest() != rendered['sha256']:
                raise ValueError(f'Render hash mismatch: {rendered["file"]}')
            with Image.open(path) as image:
                if image.size != (rendered['width'], rendered['height']) or min(image.size) < 2048:
                    raise ValueError(f'Render dimensions mismatch: {rendered["file"]}')


def compare(root, products):
    verify_evidence(root, products)
    stats = []
    for product in products:
        sheet = Image.new('RGB', (1600, 856), '#182128')
        draw = ImageDraw.Draw(sheet)
        for row, fmt in enumerate(FORMATS):
            for col, view in enumerate(VIEWS):
                with Image.open(root / 'renders' / f'aura-{product}-{fmt}-{view}.png') as source:
                    image = source.convert('RGB')
                image.thumbnail((400, 400))
                sheet.paste(image, (col * 400, row * 428 + 28))
                draw.text((col * 400 + 10, row * 428 + 8), f'{product.upper()} / {fmt.upper()} / {view}', fill='white')
        sheet.save(root / f'comparison-{product}.jpg', quality=95)
        for view in VIEWS:
            with Image.open(root / 'renders' / f'aura-{product}-glb-{view}.png') as source:
                a = source.convert('RGB')
            with Image.open(root / 'renders' / f'aura-{product}-fbx-{view}.png') as source:
                b = source.convert('RGB')
            if a.size != b.size:
                raise ValueError(f'Paired render dimensions differ: {product}/{view}')
            stat = ImageStat.Stat(ImageChops.difference(a, b))
            stats.append({'product': product, 'view': view, 'meanAbsoluteRgbDifference': sum(stat.mean) / 3, 'rmsRgbDifference': sum(stat.rms) / 3, 'scale': 255})
    (root / 'pixel-comparison.json').write_text(json.dumps(stats, indent=2) + '\n')
    print(json.dumps({'event': 'react_forge_scene_comparison', 'products': products, 'imports': len(products) * 2, 'renders': len(products) * 8}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('directory', type=Path)
    parser.add_argument('--product', choices=PRODUCTS)
    args = parser.parse_args()
    compare(args.directory, [args.product] if args.product else PRODUCTS)
