# React Forge task examples

Build the workspace package, then run any task with the one-shot CLI:

```sh
pnpm --filter react-forge build
pnpm --filter react-forge cli run examples/presentation.tsx --output /tmp/report.pptx
pnpm --filter react-forge cli run examples/document.tsx --output /tmp/report.docx
pnpm --filter react-forge cli run examples/workbook.tsx --output /tmp/report.xlsx
pnpm --filter react-forge cli run examples/pdf.tsx --output /tmp/report.pdf
```

Existing destinations fail by default. Use `--overwrite` to explicitly replace an output, and `--json` for structured CLI results. System fonts affect measurements and appearance. Office applications own their final font substitution and pagination. PDF is independently authored and tagged; no PDF/UA conformance is claimed. Spreadsheet cached results are supplied by this example, not computed by React Forge.

`sample.png` is the generated non-photographic fixture from `crates/forge-pptx/tests/fixtures/generate.py` (repository MIT license). It is included locally so examples work without downloads.


For a preservation-aware edit, pass an explicit source path and format:

```sh
pnpm --filter react-forge cli run examples/edit-office.tsx --output /tmp/edited.docx --data '{"format":"docx","source":"/tmp/original.docx","text":"Updated paragraph"}'
```

The task selects the first supported text/paragraph/cell region. Use the library's `inspect()` results and `mount()` API for explicit target selection.
