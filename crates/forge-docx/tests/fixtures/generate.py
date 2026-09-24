"""Test-only fixture producer: python-docx 1.2.0; never a runtime dependency."""
from pathlib import Path
from zipfile import ZipFile, ZIP_DEFLATED
from docx import Document
from docx.oxml import OxmlElement

root = Path(__file__).parent
document = Document()
document.add_heading("External title", 1)
document.add_paragraph("External paragraph")
table = document.add_table(rows=2, cols=2)
table.style = "Table Grid"
table.cell(0, 0).merge(table.cell(0, 1)).text = "Merged external header"
table.cell(1, 0).text = "Left"
table.cell(1, 1).text = "Right"
document.sections[0].header.paragraphs[0].text = "External header"
opaque = document.add_paragraph("Protected equation")
equation = OxmlElement("m:oMath")
run = OxmlElement("m:r")
text = OxmlElement("m:t")
text.text = "x = 1"
run.append(text)
equation.append(run)
opaque._p.append(equation)
document.save(root / "external.docx")
with ZipFile(root / "external.docx") as archive:
    parts = {name: archive.read(name) for name in archive.namelist()}
parts["word/unrelated.xml"] = b'<opaque xmlns="urn:react-forge:fixture">Preserve these exact bytes.</opaque>'
with ZipFile(root / "external.docx", "w", ZIP_DEFLATED) as archive:
    for name, data in parts.items():
        archive.writestr(name, data)
