# Delino Forge MCP

## Goal
Create and edit Office documents through a typed, validated document tree without an internal LLM. The first implementation targets PPTX through a local Rust CLI and stdio MCP server.

## Project ID
`forge` (`ProjectId::Forge`). Product name: **Delino Forge MCP**. Executable: `delino-forge`.

## Domain Ownership Map
- `crates/forge-tree-doc`: `forge_tree_doc`, the format-independent presentation DSL, validation, layout, and transactional patches.
- `crates/forge-pptx`: PPTX generation, import, package preservation, and embedded Forge metadata.
- `crates/delino-forge`: CLI, stdio MCP, local revision store, and optional preview subprocesses.

## Domain Contract Documents
- [Rust foundation](crates-forge-foundation.md).

## Cross-Domain Invariants
- Rust is explicitly selected instead of the repository's default Go. All three crates remain `publish = false`.
- Files, assets, and revisions remain in user-owned local storage instead of R2. There is no cloud service, telemetry, internal LLM, or implicit network asset retrieval.
- CLI and MCP use the same implementation. `stdout` is reserved for JSON results or MCP protocol; redacted structured logs use `stderr`.
- Supported edits preserve unrelated original package parts and unsupported XML. Unsupported edits fail explicitly rather than flattening or dropping content.
- The DSL is versioned independently of the executable. Persistent identifiers are UUID v7.
- Preview is optional; generation and editing do not require LibreOffice or Poppler. Preview results identify their renderer and are not Microsoft PowerPoint validation evidence.

## Change Policy
Update this index, the Rust foundation contract, examples/schema, and root/crates AGENTS rules with ownership or behavior changes. Release automation, public hosting, other formats, and remote MCP are outside the initial implementation.

## References
- [Repository defaults](repository-defaults.md).
- [Project template](project-template.md).
- [Domain template](domain-template.md).
