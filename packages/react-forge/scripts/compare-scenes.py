"""Create review contact sheets and observational pixel differences, not a visual pass oracle."""
from pathlib import Path
import json
import sys
from PIL import Image, ImageDraw, ImageChops, ImageStat

root = Path(sys.argv[1])
views = ['front', 'back', 'hero', 'detail']
stats = []
for product in ['studio', 'headphones', 'dac', 'stand']:
    sheet = Image.new('RGB', (1600, 856), '#182128')
    draw = ImageDraw.Draw(sheet)
    for row, fmt in enumerate(['glb', 'fbx']):
        for col, view in enumerate(views):
            image = Image.open(root / 'renders' / f'aura-{product}-{fmt}-{view}.png').convert('RGB')
            image.thumbnail((400, 400))
            sheet.paste(image, (col * 400, row * 428 + 28))
            draw.text((col * 400 + 10, row * 428 + 8), f'{product.upper()} / {fmt.upper()} / {view}', fill='white')
    sheet.save(root / f'comparison-{product}.jpg', quality=95)
    for view in views:
        a = Image.open(root / 'renders' / f'aura-{product}-glb-{view}.png').convert('RGB')
        b = Image.open(root / 'renders' / f'aura-{product}-fbx-{view}.png').convert('RGB')
        stat = ImageStat.Stat(ImageChops.difference(a, b))
        stats.append({'product': product, 'view': view, 'meanAbsoluteRgbDifference': sum(stat.mean) / 3, 'rmsRgbDifference': sum(stat.rms) / 3, 'scale': 255})
(root / 'pixel-comparison.json').write_text(json.dumps(stats, indent=2) + '\n')
