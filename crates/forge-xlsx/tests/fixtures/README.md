# External XLSX fixture

`external.xlsx` is independently authored with openpyxl 3.1.5 using `generate.py`. It includes text/numeric/date/Boolean/formula cells, formatting, merges, dimensions, freeze panes, filtering, a hyperlink, editable bar/line/pie charts, all five required conditional-formatting families and all seven required validation kinds. An unrelated worksheet and opaque worksheet extension provide preservation assertions.

Regenerate with Python 3 and exactly `openpyxl==3.1.5`. ZIP timestamps are not reproducible; tests compare original and edited part/XML payloads. These tools are fixture producers only and are not runtime dependencies or Microsoft Office validation evidence.
