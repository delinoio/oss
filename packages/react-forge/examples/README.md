# React Forge task examples

Build the workspace package, then run any task with the one-shot CLI:

```sh
pnpm --filter @delino/react-forge build
pnpm --filter @delino/react-forge cli run examples/presentation.tsx --output /tmp/report.pptx
pnpm --filter @delino/react-forge cli run examples/document.tsx --output /tmp/report.docx
pnpm --filter @delino/react-forge cli run examples/workbook.tsx --output /tmp/report.xlsx
pnpm --filter @delino/react-forge cli run examples/pdf.tsx --output /tmp/report.pdf
pnpm --filter @delino/react-forge cli run examples/zombie-gunshot.tsx --output /tmp/zombie-gunshot.wav --json
```

Existing destinations fail by default. Use `--overwrite` to explicitly replace an output, and `--json` for structured CLI results. System fonts affect measurements and appearance. Office applications own their final font substitution and pagination. PDF is independently authored and tagged; no PDF/UA conformance is claimed. Spreadsheet cached results are supplied by this example, not computed by React Forge.

`sample.png` is the generated non-photographic fixture from `crates/forge-pptx/tests/fixtures/generate.py` (repository Apache-2.0 license). It is included locally so examples work without downloads.

For a complete designed deck, run the [ROAM travel-app investor example](travel-ir-assets/README.md):

```sh
pnpm --filter @delino/react-forge cli run examples/travel-ir.tsx --output /tmp/roam-investor-deck.pptx --json
```

Its 12 English slides combine reusable React components, original local imagery, native text, an editable acquisition table and a quarterly-target chart with embedded workbook data. The fictional company, financial assumptions, researched market statistic, image provenance and font requirements are documented with the example.


For a preservation-aware edit, pass an explicit source path and format:

```sh
pnpm --filter @delino/react-forge cli run examples/edit-office.tsx --output /tmp/edited.docx --data '{"format":"docx","source":"/tmp/original.docx","text":"Updated paragraph"}'
```

The task selects the first supported text/paragraph/cell region. Use the library's `inspect()` results and `mount()` API for explicit target selection.

## MCP session task
`mcp-session.tsx` is a session-based MCP task, not a one-shot CLI entry. Run it through `react_forge_execute` with optional `{ "title": "..." }` data, retain its session ID for subsequent calls, inspect/measure the revision, export explicitly and close the session. Its render function remains in the MCP-owned state Map. See the package README for connection configuration and the complete tool sequence.

## Zombie-game gunshot

`zombie-gunshot.tsx` is original Apache-2.0 procedural audio, with no imported recordings or samples. Its reusable `Gunshot` component layers muzzle crack, low body, falling tone, mechanical click and a filtered room tail. It exports 0.75 seconds of 48000 Hz mono PCM16 WAV without fonts or networking. Pass `--data '{"seed":816}'` for a variation (seed range 1–4294967292). Reuse `Gunshot` with different starts/seeds inside a longer Sound for a sequence. Audio stays untracked. This source feature is not included in npm `0.1.1`; listen in the target game to assess the mix.
