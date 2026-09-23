# Forge Rust Foundation

## Scope
The private `forge-tree-doc`, `forge-pptx`, and `delino-forge` packages implement the PPTX v1 vertical slice described by [project-forge.md](project-forge.md). The executable is `delino-forge`; the internal library is `forge_tree_doc`.

## Runtime and Language
Rust on the root pinned toolchain, with Linux, macOS, and Windows validation. No Python, Node, Office installation, or model provider is required at runtime for generation or editing. Optional previews invoke LibreOffice and Poppler directly without a shell.

## Users and Operators
Local users and AI agents generating and editing presentations through CLI or MCP. Workspace files and document text are untrusted input.

## Interfaces and Contracts
DSL version 1 has `presentation` and `patch` kinds, pt geometry, typed nodes, document-unique keys, and UUID-v7 document/node identifiers. Unknown fields, versions, and enum variants fail validation. Supported nodes are row, column, canvas, text, list, image, shape, table, bar chart, and connector. Shape presets are rectangle, rounded rectangle, and ellipse; images are PNG/JPEG with contain/cover placement. Tables support rich text, formatting, explicit/equal column widths and rectangular nonoverlapping merges. Charts support horizontal/vertical bars, series, legends and value labels with synchronized embedded workbook data. Connectors reference same-slide nodes and cardinal anchors, using straight or elbow paths.

Row/column layout supports fixed sizes, hug, fill, padding and gaps. Canvas children require parent-relative frames. Fill shares remaining space equally. Unknown/cyclic dimensions, overflow, and unresolvable references fail. Theme, named style, node style, then run style determine text properties. Plain text and paragraphs/runs are mutually exclusive. Overflow defaults to error; explicit shrink respects a minimum font size. Text measurement uses cosmic-text and bundled, source-pinned Noto Sans KR; unavailable requested fonts are diagnosed.

Patch operations include set_text, set_text_style, unset_text_style, set_frame, insert_node, remove_node, move_node, set_chart_data, and set_image_asset. Targets contain exactly one node ID or key. set_text replaces the complete text body, retaining node style and removing run styling. Patches execute against a temporary state, validate as a whole, and increment one revision only on success.

CLI commands are schema, capabilities, asset add, create, open, inspect, apply, export, preview, close, and mcp. MCP exposes corresponding `forge.*` tools with structured input/output and recoverable tool errors. Inspect supports bounded tree projections. Schema and examples are generated/tested against Rust types, not independently maintained wire models.

Ordinary PPTX imports preserve source coordinates and carry opaque unsupported nodes. Template layouts and placeholders are inspectable and referenceable; master authoring is excluded. Unchanged ZIP parts and XML outside edited regions retain original contents. Forge metadata binds the logical tree and identity to package hashes; stale metadata cannot overwrite external edits. Signed/encrypted packages and edits whose preservation cannot be proved fail explicitly. No external relationships are fetched.

## Storage
The user-data directory owns registered assets, document originals, immutable revisions, and atomically replaced state pointers. `--state-dir` overrides the default. Persistent state is private to the user. Open/apply do not overwrite source files. Export requires an explicit overwrite option to replace an existing output and verifies source fingerprints, serializes writers, validates candidate bytes and atomically publishes from the same filesystem. Close removes only the selected managed document. Local storage is an explicit exception to the repository's R2 default.

## Security
Bound JSON, ZIP expansion, entry count, XML depth, image decoding and tree traversal before expensive processing. Reject duplicate/traversing package paths, DTD/entity input, unsafe references and ambiguous identity. Do not execute embedded content or follow external assets. Preview uses a private profile, bounded subprocess output, cancellation and timeouts, and reaps owned descendants. Logs must not contain document text, image bytes, source XML, or host paths.

## Logging
Use tracing on stderr with operation, stage, stable error code, and elapsed duration. Errors use structured codes and model/node paths; underlying parser/process errors are not exposed verbatim. stdout remains machine-readable even with logging enabled.

## Build and Test
Run package unit/integration tests, schema/example checks, cargo fmt and Clippy. Run root `cargo test` after preparing required generated DevHud frontend assets. Add affected three-OS Forge validation and Linux LibreOffice/Poppler rendering to central CI selection and aggregation. Test external PPTX round trips, opaque XML/part preservation, native chart workbooks, revision conflicts, concurrent updates, cancellation, atomic output, MCP interoperability, and renderer absence. Tests use isolated temporary state. Remove repository-owned generated dist directories before finishing.

## Dependencies and Integrations
Use serde/schemars for the DSL, cosmic-text for shaping, official rmcp for stdio MCP, pptx for new Office elements, and a Forge-owned ZIP/XML preservation layer for existing documents. Noto Sans KR must carry its source hash and OFL license. LibreOffice and Poppler are optional external renderers. No automatic installation or remote fetching occurs in the product.

## Change Triggers
Keep project index, AGENTS rules, schema/examples, CLI/MCP descriptions and CI contracts synchronized. All packages remain unpublished until a separate release contract is approved.

## References
- [Project](project-forge.md).
- [Repository defaults](repository-defaults.md).
- [MCP tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools).
- [PresentationML](https://learn.microsoft.com/en-us/office/open-xml/presentation/working-with-presentation-slides).
