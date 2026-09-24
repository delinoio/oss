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
Bound before expensive parsing/decoding; reject unsafe ZIP paths, duplicate identities, DTDs/entities, malformed structures and invalid references. Never execute embedded content or follow remote relationships. JavaScript values, functions, hooks and callbacks never enter worker computations. Native cancellation flags are scoped to each synchronous operation and restored on return or unwind; independent workers never share implicit cancellation state. Checkpoints interrupt ZIP inflation/compression in 64 KiB chunks, XML preflight and traversal, font/run/glyph work, layout nodes/pages/rows, and Office generation/import/edit loops. Third-party parser, shaping and final serialization calls remain indivisible bounded units; cancellation is checked around them rather than claiming preemption inside those libraries.

## Logging
Operation-scoped tracing without global logger installation. Emit safe operation/stage/format/revision/duration/error fields, never parser messages containing contents, host paths or bytes.

## Build and Test
Run root `cargo test` after required generated app prerequisites, plus targeted native and Node integration tests. Cover package resource/security limits, unchanged bytes, native chart/workbook edits, spreadsheet rules, fonts and PDF semantics. Test-only LibreOffice/Poppler may render outputs; record versions and font provenance. Such evidence is not Microsoft Office validation.

DOCX paragraph and run backgrounds emit RGB run shading (`w:shd`), with paragraph defaults inherited into runs and explicit run colors taking precedence ([Open XML shading](https://learn.microsoft.com/en-us/dotnet/api/documentformat.openxml.wordprocessing.shading?view=openxml-3.0.1)). The DOCX engine has structural tests for rich text, headings, lists, sections, headers/footers, page breaks, merged cells, images and native bar/line/pie charts with editable embedded workbooks. Its python-docx 1.2.0 fixture verifies supported paragraph/cell edits and exact preservation of unselected parts and XML, plus opaque-equation refusal. The complementary renderer evidence and reproduction commands are recorded in [validation](packages-react-forge-validation.md).

## Dependencies and Integrations
Forge foundation from PR #967 is a repository dependency. Shared extraction must retain its regression coverage. All new crates remain unpublished. Native output is generated, untracked and rebuilt explicitly.

## Change Triggers
Update the Node contract, project index, Forge foundation when shared behavior changes, relevant ownership rules and evidence in the same change.

## References
- [Project](project-react-forge.md).
- [Requirements](packages-react-forge-requirements.md).
- [Forge foundation](crates-forge-foundation.md).
- [Repository defaults](repository-defaults.md).

## Spreadsheet Engine
`forge-xlsx` owns independent workbook models and native worksheet/chart emission. Formula caches are caller-provided and typed; omitted caches remain absent, and workbook calculation properties request application recalculation. Five conditional-format families and seven validation families have independent generation and external-fixture edit tests. Supported cell edits preserve original styles unless an explicit replacement format is supplied; replacement formats append resources without rewriting existing style children. Chart replacement adds isolated worksheet data and retains unrelated source data and chart parts. Spreadsheet charts containing foreign element or attribute namespaces remain opaque, including markup-compatibility alternatives. Imported cells and rules retain their original address/range. Validation edits preserve imported input/error message visibility, including absent false defaults. Unmodeled validation titles, non-stop error styles, hidden dropdowns, unknown attributes and formula attributes remain opaque. Conditional rules also whitelist attributes on every nested element; stop-if-true precedence, threshold inclusivity, bar lengths, icon percentage mode, tinted/theme colors and other unmodeled properties remain opaque. Unknown extensions remain opaque.

The external workbook fixture is generated with openpyxl 3.1.5; its generator and provenance are committed beside the fixture. These structural and preservation checks are not renderer or Microsoft Office validation.

## PDF and Font Engine
`forge-document::fonts` uses pinned Parley 0.9.0/Fontique 0.9.0 for operation-local system discovery, caller-provided fonts, shaping and fallback. It checks shaped glyphs and color-emoji representations. PDF subset embedding rejects restricted, no-subsetting and bitmap-only embedding permissions instead of bypassing them. Office output references font families; it does not embed caller/system font files. Office applications own final fallback, layout and pagination.

Color-capable caller families and the platform emoji generic take precedence over general text families only on complete emoji graphemes. This avoids Fontconfig selecting a monochrome DejaVu glyph before Noto Color Emoji. Classification uses the same pinned Unicode properties as Parley; segmentation preserves joined sequences, ordinary digits/spacing and explicit text presentation selectors. Caller-only emoji generics contain color-capable registered families, while normal text retains caller ordering. Selected glyphs still undergo color and embedding validation.

`forge-pdf` uses pinned Krilla 0.7.0 to generate independent tagged PDFs. Flow paragraphs split at shaped line boundaries. Table rows split across pages, initial header rows repeat as visual pagination artifacts, and the semantic tree retains one logical table header. Tagged headings, paragraphs, lists, cells, links and figures preserve logical reading order, alternate text and document/run language. Each authored node has revision-specific page fragments; the first fragment is the initial geometry. Oversized indivisible content fails before output publication. No PDF/UA conformance is claimed.

The presentation engine exposes operation-owned text measurement and an explicit `FontEmbedding` selection. Existing `generate`/`update`/layout APIs still select the previous pinned default and OFL embedding. React Forge selects system/caller shaping and reference-only font output. This extension must retain the complete existing Forge regression suite.

Current validation includes structural PDF pagination/tagging, system CJK/RTL/color-emoji output, explicit-font and missing-font behavior, and React PDF export/measurement. Poppler page rendering and pypdf extraction have also been inspected locally. Reproducible renderer, benchmark, CLI and CI evidence is recorded in the validation contract.

Office font validation materializes the selected fallback family into newly authored Word runs while preserving logical text, run styles and hyperlinks. Macintosh-only font name records are decoded as well as Unicode records. Standalone Word chart series use explicit RGB colors so imported documents do not require a theme rewrite for visible data.


Preservation hardening allocates Word drawing/list IDs against existing packages, appends numbering definitions without renumbering existing lists, and retains selected cell wrappers/namespaces. Explicitly empty authored section headers/footers suppress Word inheritance. External python-docx/openpyxl chart fixtures exercise all three Word chart replacement families. Foreign spreadsheet rule attributes remain opaque. New chart data and worksheet merges are bounded before XML expansion (200,000 chart data cells; 250,000 expanded worksheet merge cells). Duplicate dimensions fail, hidden chart-data sheets remain plotted, and appended differential number formats receive unused custom IDs. Boolean text styles retain explicit false overrides.

ZIP declared expansion is checked before inflation, with independent actual-byte checks. Tests cover expanded-size, entry, XML node/depth, image pixel and chart expansion boundaries. Font fixtures also cover restricted, no-subsetting and bitmap-only embedding flags. PDF semantic-order tests resolve page/MCID pairs through the structure tree; the first table header remains with the first body line.

Presentation inspection parses structure and captures source identity without discovering fonts or computing layout, so caller-only sessions can register fonts after import. Native geometry and new-content font validation occur during export or measurement. Imported presentation sessions bind the original package digest to a UUID-v7 traversal snapshot, including image asset keys and connector targets. Re-importing bytes for an update restores those identities before comparison, so externally authored packages need no pre-existing Forge metadata and no-op exports retain their original bytes. JSON geometry parsing retains exact floating-point round trips.

XML preflight enforces the published depth before parsing. Valid XML with at least 32 nested elements uses a scoped 16 MiB parser stack because the recursive roxmltree tokenizer has large unoptimized frames; shallow parts remain on the caller stack. Remove this compatibility boundary only after iterative parsing or a proven supported-depth stack bound. Output-byte checks are exercised at and above 256 MiB independently of expensive serialization; package input, part/aggregate expansion, XML depth/nodes, image bytes/pixels and React tree byte/depth/node boundaries have explicit tests.

Word regions with foreign paragraph/run attributes or bookmark/field/revision markers around drawings remain opaque. Drawing and chart replacements reject foreign extension namespaces instead of dropping them; selected cell wrapper attributes remain byte-preserved. Ordinary external paragraphs, cells, images and native chart families retain positive edit coverage.

The 0.9.0 font stack includes upstream CoreText enumeration and macOS CJK fallback repairs ([enumeration](https://github.com/linebender/parley/pull/536), [CJK fallback](https://github.com/linebender/parley/pull/598)). This avoids missing system fonts outside legacy Library/Fonts scans and unsupported PingFangUI outlines on macOS 15. Swash 0.2.10 remains an explicit name-table decoder for legacy Macintosh font names; it no longer relies on a Parley re-export.

React Forge native targets cover macOS/Windows/glibc Linux on x64 and arm64. Parley/Fontique select CoreText, DirectWrite or Fontconfig system discovery. Caller fonts and typed missing-glyph/color/embedding errors apply identically on every target. Dedicated native-host CI enables the non-macOS system-font tests after installing fonts; ordinary Cargo runs may omit those environment-dependent fixtures. Windows console integration requires the built Node workspace and explicitly enabled isolated-console test.

Word paragraphs containing page/column breaks or non-default text-wrapping clearance remain opaque, including inside selected tables/cells. Only ordinary line breaks (implicit or explicit `textWrapping`, with absent/`none` clearance) are admitted to editable paragraphs; unrelated edits preserve the protected break XML.

Every package relationship part requires an OPC-namespace `Relationships` root and leaf `Relationship` children. Matching attribute names on foreign or incorrectly named elements do not grant relationship authority; malformed relationship elements reject the package before reference resolution.

The XML depth ceiling counts nested elements, with the document element at depth one. Exactly 128 levels are accepted for both paired and self-closing elements, including text/comment children at the deepest level; 129 levels are rejected by iterative preflight before recursive parsing.

Shared text alignment retains omission separately from explicit left alignment. DOCX/PDF resolve an omitted alignment to their inherited/default left behavior; XLSX leaves horizontal alignment absent to preserve Excel's General behavior for each value type. Applying number formats, borders or wrapping alone does not force left alignment, including differential styles.
