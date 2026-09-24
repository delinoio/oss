# React Forge Native Contract

## Scope
Shared `forge-package` and `forge-document`, format-specific `forge-docx`, `forge-xlsx`, `forge-pdf`, and dedicated `react-forge-node` adapter. `forge-document` owns reusable text/style, image and native chart/data primitives without merging format-specific document models. Reuse `forge-tree-doc` and `forge-pptx` for presentations. Complete scope is preserved in [requirements](packages-react-forge-requirements.md).

## Runtime and Language
Rust on the repository-pinned toolchain. Engines do not depend on N-API, Office, LibreOffice, Python, external converters or runtime installation. Rust is selected over repository-default Go to reuse the Forge engines and native document libraries.

## Users and Operators
The Node session library and repository engine developers; existing Forge CLI/MCP consumers retain their interfaces and font defaults.

## Interfaces and Contracts
Four separate document models share bounded package, style, font and asset infrastructure where applicable. Office charts remain editable, including their associated data. Spreadsheet formulas retain optional caller-provided cached values and request application recalculation; no formula calculation engine exists. PDF authoring is independent of Office and includes flow pagination, repeated table headers, actionable indivisible-content overflow and semantic reading-order tags; PDF import/editing and PDF/UA claims are excluded.

Imports retain unsupported content as opaque bytes. Edit only provably owned supported regions; preserve all unrelated package payloads and outside-region XML bytes. Reject encrypted/signed/macro/legacy/Strict packages, ambiguous identities, dangling references and unsafe edits. Positive externally authored fixtures must demonstrate actual supported edits, including charts and spreadsheet rules.

Limits: Office input/output and PDF output 256 MiB; expanded Office 512 MiB; 10,000 ZIP entries; individual ZIP/XML part 64 MiB; XML depth 128 and 1,000,000 nodes per part; newly rendered tree 16 MiB, depth 48 and 20,000 nodes; image 64 MiB and 64,000,000 pixels. Preserve stricter existing PPTX limits. Opaque content uses package limits, not newly rendered tree limits.

React Forge defaults to system font discovery/fallback with explicit caller fonts. Check new content for missing glyphs, CJK, RTL/mixed direction and color emoji; report typed actionable failures instead of missing/replaced glyphs. Honor embedding permissions and never require untouched opaque content to be rendered. Do not change existing Forge's bundled default font policy.

## Storage
Memory-only native operations and explicit local files; no database/cache/recovery archive. Local storage is an explicit R2 exception. File publication belongs to the session's revision/cancellation boundary, with same-filesystem temporary output and truthful publication results.

## Security
Bound before expensive parsing/decoding; reject unsafe ZIP paths, duplicate identities, DTDs/entities, malformed structures and invalid references. Never execute embedded content or follow remote relationships. JavaScript values, functions, hooks and callbacks never enter worker computations.

## Logging
Operation-scoped tracing without global logger installation. Emit safe operation/stage/format/revision/duration/error fields, never parser messages containing contents, host paths or bytes.

## Build and Test
Run root `cargo test` after required generated app prerequisites, plus targeted native and Node integration tests. Cover package resource/security limits, unchanged bytes, native chart/workbook edits, spreadsheet rules, fonts and PDF semantics. Test-only LibreOffice/Poppler may render outputs; record versions and font provenance. Such evidence is not Microsoft Office validation.

The DOCX engine checkpoint has structural tests for rich text, headings, lists, sections, headers/footers, page breaks, merged cells, images and native bar/line/pie charts with editable embedded workbooks. Its python-docx 1.2.0 fixture verifies supported paragraph/cell edits and exact preservation of unselected parts and XML, plus opaque-equation refusal. These checks do not yet constitute the complete format/font/rendering acceptance evidence.

## Dependencies and Integrations
Forge foundation from PR #967 is a repository dependency. Shared extraction must retain its regression coverage. All new crates remain unpublished. Native output is generated, untracked and rebuilt explicitly.

## Change Triggers
Update the Node contract, project index, Forge foundation when shared behavior changes, relevant ownership rules and evidence in the same change.

## References
- [Project](project-react-forge.md).
- [Requirements](packages-react-forge-requirements.md).
- [Forge foundation](crates-forge-foundation.md).
- [Repository defaults](repository-defaults.md).

## Spreadsheet Engine Checkpoint
`forge-xlsx` owns independent workbook models and native worksheet/chart emission. Formula caches are caller-provided and typed; omitted caches remain absent, and workbook calculation properties request application recalculation. Five conditional-format families and seven validation families have independent generation and external-fixture edit tests. Supported cell edits preserve original styles unless an explicit replacement format is supplied; replacement formats append resources without rewriting existing style children. Chart replacement adds isolated worksheet data and retains unrelated source data and chart parts. Imported cells and rules retain their original address/range. Unknown extensions remain opaque.

The external workbook fixture is generated with openpyxl 3.1.5; its generator and provenance are committed beside the fixture. These structural and preservation checks are not renderer or Microsoft Office validation.

## PDF and Font Engine Checkpoint
`forge-document::fonts` uses pinned Parley 0.6.0/Fontique 0.6.0 for operation-local system discovery, caller-provided fonts, shaping and fallback. It checks shaped glyphs and color-emoji representations. PDF subset embedding rejects restricted, no-subsetting and bitmap-only embedding permissions instead of bypassing them. Office output references font families; it does not embed caller/system font files. Office applications own final fallback, layout and pagination.

`forge-pdf` uses pinned Krilla 0.7.0 to generate independent tagged PDFs. Flow paragraphs split at shaped line boundaries. Table rows split across pages, initial header rows repeat as visual pagination artifacts, and the semantic tree retains one logical table header. Tagged headings, paragraphs, lists, cells, links and figures preserve logical reading order, alternate text and document/run language. Each authored node has revision-specific page fragments; the first fragment is the initial geometry. Oversized indivisible content fails before output publication. No PDF/UA conformance is claimed.

The presentation engine exposes operation-owned text measurement and an explicit `FontEmbedding` selection. Existing `generate`/`update`/layout APIs still select the previous pinned default and OFL embedding. React Forge selects system/caller shaping and reference-only font output. This extension must retain the complete existing Forge regression suite.

Current validation includes structural PDF pagination/tagging, system CJK/RTL/color-emoji output, explicit-font and missing-font behavior, and React PDF export/measurement. Poppler page rendering and pypdf extraction have also been inspected locally. The complete reproducible rendering/benchmark/CLI/CI evidence remains outstanding; these checkpoints do not close #968.

Office font validation materializes the selected fallback family into newly authored Word runs while preserving logical text, run styles and hyperlinks. Macintosh-only font name records are decoded as well as Unicode records. Standalone Word chart series use explicit RGB colors so imported documents do not require a theme rewrite for visible data.
