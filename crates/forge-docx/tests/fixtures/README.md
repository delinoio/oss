# External DOCX fixture

`external.docx` is authored by python-docx 1.2.0 using `generate.py`, independently of the Forge writer. It includes a heading, ordinary paragraph, merged table, header and an opaque equation. The generator adds an unrelated XML part for exact byte-preservation checks. Python and python-docx are test-only dependencies; generation/import/editing in the library require neither.

Regenerate with Python 3 and exactly `python-docx==1.2.0`. ZIP timestamps may differ; tests compare original and edited payloads, not regenerated compressed archive bytes. This fixture is not Microsoft Office application validation evidence.

`charts.docx` combines python-docx 1.2.0 packaging with independently authored openpyxl 3.1.5 native bar/line/pie chart parts and associated embedded workbooks. It also retains original numbered lists and a picture so edits can detect drawing/list ID collisions. No Forge writer generates these fixture inputs.
