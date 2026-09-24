"""Independent structural and raster verification; test dependencies only."""
from pathlib import Path
from zipfile import ZipFile
import hashlib
import importlib.metadata
import json
import platform
import struct
import sys
from lxml import etree
from PIL import Image
from pypdf import PdfReader

root = Path(sys.argv[1])
manifest = json.loads((root / "manifest.json").read_text())
manifest["versions"].update({name: importlib.metadata.version(name) for name in ["pypdf", "Pillow", "lxml"]})
W = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
S = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
C = "http://schemas.openxmlformats.org/drawingml/2006/chart"
parser = etree.XMLParser(resolve_entities=False, no_network=True)

def parse(data):
    return etree.fromstring(data, parser)

def tags(reader):
    result = []
    def visit(item):
        item = item.get_object() if hasattr(item, 'get_object') else item
        if isinstance(item, list):
            for child in item:
                visit(child)
        elif isinstance(item, dict):
            if '/S' in item:
                result.append(str(item['/S']))
            if '/K' in item:
                visit(item['/K'])
    visit(reader.trailer['/Root']['/StructTreeRoot'])
    return result

for case in manifest["cases"]:
    directory = root / case["name"]
    reader = PdfReader(directory / "document.pdf", strict=True)
    assert reader.pages, case
    text = "\n".join(page.extract_text() or "" for page in reader.pages)
    expected = "Edited by React Forge" if case["edited"] else {"pptx": "React Forge", "docx": "Quarterly report", "xlsx": "Revenue", "pdf": "Native PDF report"}[case["format"]]
    assert expected in text, (case["name"], "expected text missing")
    baseline = sorted((directory / "baseline").glob("page-*.png")) if case["edited"] else []
    if baseline:
        assert len(baseline) == len(reader.pages), "Edited fixture pagination changed"
    raster = []
    for path in sorted(directory.glob("page-*.png")):
        with Image.open(path) as image:
            pixels = list(image.convert("RGB").get_flattened_data())
            colored = sum(max(pixel) - min(pixel) > 45 and min(pixel) < 220 for pixel in pixels)
            nonwhite = sum(min(pixel) < 235 for pixel in pixels)
            ratio = nonwhite / len(pixels)
            assert ratio < 0.95, (case["name"], path.name, "black output")
            if ratio <= 0.0005:
                # External spreadsheet print areas may include intentional blank
                # pages. Permit them only when the same source page is blank.
                original = directory / "baseline" / path.name
                assert original.is_file(), (case["name"], path.name, "unexpected blank page")
                with Image.open(original) as before:
                    before_pixels = list(before.convert("RGB").get_flattened_data())
                    assert sum(min(pixel) < 235 for pixel in before_pixels) / len(before_pixels) <= 0.0005, "Edit introduced a blank page"
            raster.append({"file": path.name, "width": image.width, "height": image.height, "nonwhiteRatio": round(ratio, 6), "coloredPixels": colored, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()})
    assert len(raster) == len(reader.pages), case
    if baseline:
        case["sourcePagination"] = "preserved, including source blank pages"
    case["pages"] = len(reader.pages)
    case["raster"] = raster
    case["textExtraction"] = "passed"
    if case["name"] == "created-docx":
        assert "한국어" in text and "日本語" in text and "中文" in text
        assert raster[1]["coloredPixels"] > 3000, "Word chart series must be visible, not labels alone"
    if case["name"] == "created-pdf":
        catalog = reader.trailer['/Root']
        assert catalog['/MarkInfo']['/Marked'] and catalog['/Lang'] == 'en-US'
        assert set(['/H1', '/P', '/L', '/LI', '/Lbl', '/LBody', '/Table', '/TR', '/TH', '/TD', '/Link', '/Figure']).issubset(set(tags(reader)))
        assert "한국어" in text and "日本語" in text and "中文" in text
        assert len(reader.pages) >= 2
        assert any(page.get('/Annots') for page in reader.pages)
        case["semanticTags"] = "passed"
    if case["format"] != "pdf":
        with ZipFile(directory / f"document.{case['format']}") as archive:
            assert archive.testzip() is None
            parts = {name: archive.read(name) for name in archive.namelist()}
        for name, data in parts.items():
            if name.lower().endswith(('.xml', '.rels')):
                parse(data)
        if case["name"] == "created-docx":
            charts = [parse(value) for name, value in parts.items() if name.startswith('word/charts/') and name.endswith('.xml')]
            assert len(charts) == 3
            for chart in charts:
                assert chart.find(f'.//{{{C}}}externalData') is not None
            assert len([name for name in parts if name.startswith('word/embeddings/')]) == 3
        if case["name"] == "created-xlsx":
            sheet = parse(parts['xl/worksheets/sheet1.xml'])
            assert len(sheet.findall(f'.//{{{S}}}cfRule')) == 5
            assert len(sheet.findall(f'.//{{{S}}}dataValidation')) == 7
            assert sheet.find(f'.//{{{S}}}mergeCell') is not None
            assert sheet.find(f'.//{{{S}}}hyperlink') is not None
            workbook = parse(parts['xl/workbook.xml'])
            assert workbook.find(f'{{{S}}}calcPr').get('fullCalcOnLoad') == '1'
            assert sum(page['coloredPixels'] for page in raster) > 3000
        case["packageXml"] = "passed"

# Read font metadata and checksums without copying or redistributing system
# fonts. OFL test font provenance is committed in forge-tree-doc/assets/fonts.
fonts = []
for path in [Path('/System/Library/Fonts/Helvetica.ttc'), Path('/System/Library/Fonts/AppleSDGothicNeo.ttc'), Path('/System/Library/Fonts/Apple Color Emoji.ttc'), Path('/System/Library/Fonts/GeezaPro.ttc')]:
    if not path.is_file():
        continue
    data = path.read_bytes()
    offset = struct.unpack_from('>I', data, 12)[0] if data[:4] == b'ttcf' else 0
    count = struct.unpack_from('>H', data, offset + 4)[0]
    version = None
    for i in range(count):
        tag, checksum, start, length = struct.unpack_from('>4sIII', data, offset + 12 + 16 * i)
        if tag != b'name':
            continue
        _, records, storage = struct.unpack_from('>HHH', data, start)
        for j in range(records):
            platform_id, encoding, language, name_id, size, at = struct.unpack_from('>HHHHHH', data, start + 6 + 12 * j)
            if name_id == 5:
                raw = data[start + storage + at:start + storage + at + size]
                try:
                    version = raw.decode('utf-16-be' if platform_id in (0, 3) else 'mac_roman')
                except UnicodeError:
                    pass
                if version:
                    break
    fonts.append({"file": path.name, "version": version, "sha256": hashlib.sha256(data).hexdigest(), "provenance": "macOS-installed; not redistributed"})
manifest["fonts"] = fonts
manifest["os"] = {"system": platform.system(), "version": platform.mac_ver()[0], "architecture": platform.machine()}
(root / 'evidence.json').write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + '\n')
print(f"Verified {len(manifest['cases'])} generated/edited cases; XML, extraction, semantic tags and raster checks against original pagination passed.")
