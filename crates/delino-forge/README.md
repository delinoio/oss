# Delino Forge MCP

A private Rust CLI and local stdio MCP server for creating and editing PPTX files from validated JSON. It has no internal LLM or network asset fetcher. Generation and editing require no Office installation. LibreOffice and Poppler are optional preview dependencies.

## Quick start

From the repository root:

```sh
cargo build -p delino-forge
cargo run -p delino-forge -- schema
cargo run -p delino-forge -- capabilities
cargo run -p delino-forge -- create crates/forge-tree-doc/examples/overview.json
```

`create` returns a UUID-v7 `document_id` and revision. Substitute that returned ID below:

```sh
delino-forge inspect DOCUMENT_ID --depth 2
delino-forge export DOCUMENT_ID --output presentation.pptx
delino-forge preview DOCUMENT_ID --output preview
delino-forge close DOCUMENT_ID
```

Use `--state-dir DIRECTORY` to isolate local state; it is a global option. The default is the platform user-data directory. `open presentation.pptx` snapshots an existing document. Export edits to a different path, such as `delino-forge export DOCUMENT_ID --output edited.pptx`. Replacing the tracked source returns `unsupported_edit`, even with `--overwrite`, because Forge cannot guarantee preservation of another application's concurrent save. The original and managed revision remain unchanged by this rejection. Replacing a separate existing output requires `--overwrite`. `capabilities` reports this boundary as `export.tracked_source_overwrite: false`.

Register PNG or JPEG images with `delino-forge asset add image.png`. Put its returned handle into the document's `assets` map, then reference that alias with `asset_ref`. The [all-node example](../forge-tree-doc/examples/all-nodes.json) needs its sample handle replaced with a registered handle before creation. The [schema bundle](../forge-tree-doc/schema.json) contains `presentation` and `patch` JSON Schemas.

```json
{
  "dsl_version": 1,
  "kind": "patch",
  "document_id": "019f5260-0000-7000-8000-000000000001",
  "base_revision": 0,
  "operations": [
    {"op": "set_text", "target": {"key": "overview.title"}, "text": "Updated presentation"}
  ]
}
```

Save a patch using the actual document ID and current revision, then run `delino-forge apply patch.json`. The complete operation batch succeeds as one revision or leaves the previous revision available. A no-op keeps the revision unchanged. Targets use exactly one `key` or `node_id`.

`set_text` can address an existing table cell with `"cell": {"row": 1, "column": 2}`. Coordinates are zero based and merged cells must be addressed at their top-left origin. Text replacement removes that text body's rich-run formatting; node/cell style remains. `set_text_style` and `unset_text_style` change node defaults; explicit run styles retain precedence. `set_image_asset` accepts either an existing document alias or a newly registered content handle.

The other operations are `set_frame`, `insert_node`, `remove_node`, `move_node`, and `set_chart_data`. The same JSON types are used in CLI and MCP. Inspect supports depth 0–8 and at most 1,000 projected nodes; inspect a returned node ID for a deeper subtree.

Slide root containers always occupy the full page. Set presentation dimensions with `page`; `frame`, `width`, `height`, and `set_frame` are rejected on the slide root. Use nested containers for smaller regions.

## MCP

Start `delino-forge --state-dir DIRECTORY mcp` with stdio connected to an MCP client. Tools are `forge.schema`, `forge.capabilities`, `forge.asset.add`, `forge.create`, `forge.open`, `forge.inspect`, `forge.apply`, `forge.export`, `forge.preview`, and `forge.close`. Document operations return IDs, revisions, structured diagnostics or artifact paths. Logs use stderr; stdout carries only the MCP protocol.

A typical MCP client configuration is:

```json
{"mcpServers":{"delino-forge":{"command":"delino-forge","args":["mcp"]}}}
```

## Editing and previews

Imports expose editable supported shapes on coordinate-preserving canvases and retain unsupported content as opaque nodes. `inspect` lists available layout references and placeholder indices. Open a template to edit its existing placeholders or insert supported nodes with `placeholder_ref` against that slide's layout. New presentations can select a built-in `slide_layout_ref`. Master authoring is not supported.

Unchanged ZIP part payloads and unrelated XML remain intact. No-op exports of ordinary imported files retain the exact original bytes. Groups, rotations/flips, stretched or asymmetric-crop images, unsupported chart kinds and drawing effects are preserved; unsafe changes return `unsupported_edit`. Group contents remain opaque in v1. Strict OOXML, encryption, macros and signatures are rejected. Metadata altered outside Forge returns `stale_metadata`; it is never silently trusted.

Changing an imported table's merge/grid definition requires explicit node replacement. In-place cell text changes preserve its native cell properties. Chart data editing requires a single-sheet `Sheet1` workbook with the expected chart range and no formulas or additional data in that range. Other workbook structures are preserved by rejecting the edit. There is no slide-count change operation or cross-slide native shape move in v1.

Tables with unequal native row heights or heights inconsistent with their frame remain opaque in v1. Their original content is preserved during edits to other nodes.

Imported text is editable when its text area has explicit zero margins, square wrapping, top anchoring, horizontal single-column flow and no automatic fitting. Text boxes with other or implicit native geometry remain opaque so edits cannot bypass their actual space limits. They remain intact when you edit other supported objects.

Previews use a private LibreOffice profile, a 120-second deadline per renderer and child-process cleanup on cancellation. They are limited to 100 pages, 40 million pixels per page and 250 million pixels total at 96 dpi. Missing renderers produce `renderer_unavailable`. Optional `FORGE_SOFFICE` and `FORGE_PDFTOPPM` variables select executable paths. The bundled, OFL-licensed Noto Sans KR is embedded for dependable Korean rendering. Preview evidence is LibreOffice/Poppler output, not a Microsoft PowerPoint compatibility certification.

## Validation

```sh
cargo test -p forge-tree-doc -p forge-pptx -p delino-forge
cargo clippy -p forge-tree-doc -p forge-pptx -p delino-forge --all-targets -- -D warnings
cargo fmt --all --check
cargo test -p delino-forge --test render -- --ignored
```

The last command requires the optional renderers. Set `FORGE_RENDER_ARTIFACTS` to retain generated and edited PPTX/PDF/PNG fixtures. CI runs the Rust suites on Linux, macOS and Windows and requires real rendering on Linux. External fixture provenance and reproduction are in [fixtures/generate.py](../forge-pptx/tests/fixtures/generate.py); Python is not a runtime dependency.
