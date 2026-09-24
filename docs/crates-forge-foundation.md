# Forge Rust Foundation

## Scope
The private `forge-tree-doc`, `forge-pptx`, and `delino-forge` packages implement the PPTX v1 vertical slice described by [project-forge.md](project-forge.md). The executable is `delino-forge`; the internal library is `forge_tree_doc`.

## Runtime and Language
Rust on the root pinned toolchain, with Linux, macOS, and Windows validation. No Python, Node, Office installation, or model provider is required at runtime for generation or editing. Optional previews invoke LibreOffice and Poppler directly without a shell.

## Users and Operators
Local users and AI agents generating and editing presentations through CLI or MCP. Workspace files and document text are untrusted input.

## Interfaces and Contracts
DSL version 1 has `presentation` and `patch` kinds, pt geometry, typed nodes, document-unique keys, and UUID-v7 document/node identifiers. Unknown fields, versions, and enum variants fail validation. Supported nodes are row, column, canvas, text, list, image, shape, table, bar chart, and connector. Shape presets are rectangle, rounded rectangle, and ellipse; images are PNG/JPEG with contain/cover placement. Tables support rich text, formatting, explicit/equal column widths and rectangular nonoverlapping merges. Charts support horizontal/vertical bars, series, legends and value labels with synchronized embedded workbook data. Connectors reference same-slide nodes and cardinal anchors, using straight or elbow paths.

Row/column layout supports fixed sizes, hug, fill, padding and gaps. Canvas children require parent-relative frames. Fill shares remaining space equally. Unknown/cyclic dimensions, overflow, and unresolvable references fail. Theme, named style, node style, then run style determine text properties. Plain text and paragraphs/runs are mutually exclusive. Overflow defaults to error; explicit shrink respects a minimum font size. Text measurement uses cosmic-text and bundled, source-pinned Noto Sans KR; unavailable requested fonts are diagnosed. System fonts with the same default-family name cannot override the pinned font. Generated PPTX embeds its unchanged sfnt payload in an uncompressed EOT wrapper with embedding permissions preserved. Preview prepares the same font in a disposable copy when necessary; managed source parts remain unchanged.

Patch operations include set_text, set_text_style, unset_text_style, set_frame, insert_node, remove_node, move_node, set_chart_data, and set_image_asset. Targets contain exactly one node ID or key. set_text replaces the complete text body, retaining node style and removing run styling. Patches execute against a temporary state, validate as a whole, and increment one revision only on success. No-ops retain their revision. `set_text` optionally accepts a zero-based `cell` row/column on a table; merged-cell references must select the top-left grid slot. Node style operations preserve the precedence of explicit run styles. Image replacement accepts a document alias or registered content handle. Asset handles use SHA-256 content addressing instead of entity identifiers.

CLI commands are schema, capabilities, asset add, create, open, inspect, apply, export, preview, close, and mcp. MCP exposes corresponding `forge.*` tools with structured input/output and recoverable tool errors. Inspect supports bounded tree projections. Schema and examples are generated/tested against Rust types, not independently maintained wire models.

Ordinary PPTX imports preserve source coordinates and carry opaque unsupported nodes. Template layouts and placeholders are inspectable and referenceable; inherited placeholder geometry resolves through layout/master definitions. Open a template to insert a referenced placeholder into its existing slide; new decks select from built-in layouts. Master authoring is excluded. Unchanged ZIP parts and XML outside edited regions retain original contents. Forge metadata binds the logical tree and identity to package hashes; stale metadata cannot overwrite external edits. Signed/encrypted packages and edits whose preservation cannot be proved fail explicitly. No external relationships are fetched. V1 accepts Transitional PresentationML and explicitly rejects Strict OOXML and macros as unsupported packages. Group contents, rotated/flipped shapes, stretched or asymmetric-crop images outside contain/cover semantics, and unsupported graphic kinds remain opaque. Opaque nodes cannot be authored, moved or deleted. Drawing-order changes are refused when a direct native tree element has no binding; nested group shape IDs remain reserved. Slide-count changes and cross-slide native moves are not v1 patch operations.

Cell text edits retain native table properties; changing merge/grid/base-style definitions requires explicit table replacement. Bar-chart data updates retain series extensions while updating cached values and the embedded workbook. In-place workbook edits require a canonical `Sheet1` chart range without formulas, unrelated cells or row/cell extensions that cannot be preserved; other layouts return `unsupported_edit`. Added/removed series are explicit chart-data changes. Forge repairs the pinned pptx 0.1.0 writer's repeated B-column formula references, absent bar-chart legend and signed axis IDs; tests assert second/third-series column references as well as cache/workbook values. Unknown relationships and unused original media parts are retained, not garbage-collected.

## Storage
The user-data directory owns registered assets, document originals, immutable revisions, and atomically replaced state pointers. `--state-dir` overrides the default. Persistent state is private to the user. Open/apply do not overwrite source files. Export requires an explicit overwrite option to replace an existing output and verifies source fingerprints, serializes writers, validates candidate bytes and atomically publishes from the same filesystem. Close removes only the selected managed document; shared assets and lock files are retained. An interrupted generation is unreachable until the atomic pointer commit, and old generations remain available. Source fingerprints are checked before mutation and again before committing an edit. Local storage is an explicit exception to the repository's R2 default.

## Security
Bound JSON, ZIP expansion, entry count, XML depth, image decoding and tree traversal before expensive processing. Reject duplicate/traversing package paths, DTD/entity input, unsafe references and ambiguous identity. Do not execute embedded content or follow external assets. Preview uses a private profile, discarded subprocess output, cancellation and 120-second per-renderer timeouts, and reaps owned descendants through Unix process groups or Windows Job Objects. Its 96-dpi raster budget is 100 slides, 40 million pixels per slide and 250 million pixels per document; the resulting page count must match. Logs must not contain document text, image bytes, source XML, or host paths.

## Logging
Use tracing on stderr with operation, stage, stable error code, and elapsed duration. Errors use structured codes and model/node paths; underlying parser/process errors are not exposed verbatim. stdout remains machine-readable even with logging enabled.

## Build and Test
Run package unit/integration tests, schema/example checks, cargo fmt and Clippy. Run root `cargo test` after preparing required generated DevHud frontend assets. Add affected three-OS Forge validation and Linux LibreOffice/Poppler rendering to central CI selection and aggregation. Test external PPTX round trips, opaque XML/part preservation, native chart workbooks, revision conflicts, concurrent updates, cancellation, atomic output, MCP interoperability, and renderer absence. Tests use isolated temporary state. Remove repository-owned generated dist directories before finishing.

## Dependencies and Integrations
Use serde/schemars for the DSL, cosmic-text for shaping, official rmcp for stdio MCP, pptx for new Office elements, and a Forge-owned ZIP/XML preservation layer for existing documents. Noto Sans KR must carry its source hash and OFL license. LibreOffice and Poppler are optional external renderers. No automatic installation or remote fetching occurs in the product.

New packages match every content-type override's path spelling to its actual ZIP member. The pinned pptx 0.1.0 writer lowercases overrides while retaining mixed-case members. Although OPC defines ASCII-case-insensitive equivalence, exact spelling also supports case-sensitive inspectors and older readers. This compatibility normalization runs only on newly generated packages, never on imported XML. The generated round-trip regression checks every override against its exact ZIP member. See [Microsoft's package URI compatibility note](https://learn.microsoft.com/en-us/dotnet/core/compatibility/core-libraries/8.0/system-io-packaging-case-insensitive-uri).

## Change Triggers
Keep project index, AGENTS rules, schema/examples, CLI/MCP descriptions and CI contracts synchronized. All packages remain unpublished until a separate release contract is approved.

## References
- [Project](project-forge.md).
- [Repository defaults](repository-defaults.md).
- [MCP tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools).
- [PresentationML](https://learn.microsoft.com/en-us/office/open-xml/presentation/working-with-presentation-slides).


## Implementation and Validation Evidence
- `crates/forge-tree-doc/schema.json` is generated by `delino-forge schema`; a regression test rejects drift. `overview.json` is directly runnable, while `all-nodes.json` requires registering the supplied fixture image and replacing its example asset handle.
- `crates/delino-forge/README.md` documents the local CLI/MCP contract and supported editing boundaries.
- External PPTX fixtures are authored with python-pptx 1.0.2 and Pillow by the checked-in fixture script. These tools are not production dependencies. Namespace changes and unbound extensions are applied in Rust tests.
- Local validation uses macOS, LibreOfficeDev 26.8.0.0.alpha0 and Poppler. Generated and edited decks were rendered and PNGs inspected for Korean/English text, image containment/cropping, merged tables, legends and connections. This is not validation in the Microsoft PowerPoint application.
- The root Cargo suite requires the documented DevHud frontend build and pnport/preload build. On this macOS host, `TMPDIR=/private/tmp` avoids the already documented binpm `/var` versus `/private/var` fixture mismatch; the complete root suite passed with those prerequisites.
- CI selection and aggregation include `forge-test` on all three desktop OS families and `forge-render` on Linux. Hosted Windows/Linux results remain pending until CI runs; local tests must not be presented as hosted matrix evidence.

### Format references
- [EOT header and unchanged sfnt payload](https://www.w3.org/submissions/EOT/).
- [LibreOffice renderer parameters](https://help.libreoffice.org/latest/en-GB/text/shared/guide/start_parameters.html).
- [DrawingML preset connection sites](https://github.com/LibreOffice/core/blob/master/oox/source/drawingml/customshapes/presetShapeDefinitions.xml).
