# React Forge

## Goal
Provide private React document sessions and a TSX task CLI for authoring and preservation-aware editing of PPTX, DOCX, XLSX, and independent tagged PDF. Issue [#968](https://github.com/delinoio/oss/issues/968) defines the complete delivery boundary. Partial format support does not satisfy that issue.

## Project ID
`react-forge` (`ProjectId::ReactForge`). Product name: **React Forge**.

## Domain Ownership Map
- `packages/react-forge`: private TypeScript session library, React reconciler, components, CLI, examples and integration tests.
- `crates/react-forge-node`: dedicated N-API adapter; JavaScript execution never moves into native workers.
- `crates/forge-package`: shared bounded OOXML package and preservation primitives.
- `crates/forge-docx`: word-processing model and DOCX engine.
- `crates/forge-xlsx`: spreadsheet model and XLSX engine.
- `crates/forge-pdf`: independent PDF model, pagination and semantic tagging.
- Existing `crates/forge-tree-doc` and `crates/forge-pptx`: presentation model, layout and PPTX engine reused from Forge.

## Domain Contract Documents
- [Complete requirements](packages-react-forge-requirements.md).
- [Node session and CLI contract](packages-react-forge-contract.md).
- [Native engines and preservation contract](crates-react-forge-contract.md).
- [Existing Forge foundation](crates-forge-foundation.md).

## Cross-Domain Invariants
- Support Node.js 24, React 19.2.8 with react-reconciler 0.33.0, and macOS arm64. Use the repository-pinned Rust toolchain. All packages remain private and unpublished.
- TypeScript executes React; Rust processes validated serializable format-specific models. Rust is a project-specific exception to the default Go language; local files and explicit exports are an exception to default R2 storage.
- Sessions live only in memory. Explicit export is the persistence boundary. No automatic recovery, revision archive, service, telemetry, URL fetching, runtime downloads or external conversion dependencies.
- Persistent document/node identities are UUID v7. React keys and useId values do not become persistent identities.
- Preserve unrelated Office parts/XML; reject unsafe or unprovable edits transactionally. Never flatten opaque imported content. Existing Forge CLI/MCP behavior and default fonts remain stable.
- Registered assets and render-relevant asynchronous work must settle before export pins a revision. Later commits cannot change an export already pinned. Cancellation has no automatic timeout and cannot misreport an already completed atomic publication.
- Source-backed requirements must remain intact. Track missing evidence honestly; do not close #968 until all acceptance criteria pass. Direct Microsoft Office validation and PDF/UA certification are not claimed.

## Change Policy
Update the project index, affected domain contracts, relevant AGENTS rules, examples and validation together when interfaces or ownership change. Internal rollback uses source revision rollback and rebuild without rewriting exported files.

## References
- [Repository defaults](repository-defaults.md).
- [Forge project](project-forge.md).
- [Issue #968](https://github.com/delinoio/oss/issues/968).
