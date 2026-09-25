# React Forge

## Goal
Provide publicly distributed React document sessions, a TSX task CLI and a local session-based stdio MCP server for authoring and preservation-aware editing of PPTX, DOCX, XLSX, and independent tagged PDF. Issue [#968](https://github.com/delinoio/oss/issues/968) defines the original feature boundary; the explicit 2026-09-24 follow-up on PR #970 adds macOS x64 and Windows/Linux x64/arm64 support. The subsequent public npm distribution decision supersedes the original private-package boundary. Partial format support does not satisfy that issue. The Figma extension adds explicit remote creation and preservation-aware editing under its separate contract; authenticated acceptance targets Node.js 24 on macOS arm64.

The explicit SFX follow-up adds offline procedural game effects exported as WAV, with zombie-game gunshot acceptance, under the [SFX contract](packages-react-forge-sfx-contract.md). It is implemented in source and not yet published.

## Project ID
`react-forge` (`ProjectId::ReactForge`). Product name: **React Forge**. The public npm package is `@delino/react-forge`; its CLI executable is `react-forge`.

## Domain Ownership Map
- `crates/forge-sprite`: independent bounded pixel sprite model, native rasterization and deterministic archive generation.
- `packages/react-forge`: TypeScript session library, React reconciler, components, CLI, stdio MCP server, examples and integration tests.
- `packages/react-forge/examples/travel-ir.tsx` and `travel-ir-assets`: designed English ROAM investor-deck example, local concept imagery, source provenance and explicitly fictional financial model.
- `crates/react-forge-node`: dedicated N-API adapter; JavaScript execution never moves into native workers.
- `crates/forge-package`: shared bounded OOXML package and preservation primitives.
- `crates/forge-document`: shared text/style validation, image validation and editable chart/data primitives.
- `crates/forge-docx`: word-processing model and DOCX engine.
- `crates/forge-xlsx`: spreadsheet model and XLSX engine.
- `crates/forge-sfx`: independent bounded synthesis and PCM WAV engine.
- `packages/react-forge/examples/zombie-gunshot.tsx`: original procedural SFX acceptance example.
- `crates/forge-pdf`: independent PDF model, pagination and semantic tagging.
- `crates/forge-figma`: pure Figma model validation, diff and bounded publication plans; the Node package owns official MCP networking and host credentials.
- `packages/react-forge/examples/travel-figma*.tsx`: editable ROAM mobile design and explicit reopened-file edits.
- Existing `crates/forge-tree-doc` and `crates/forge-pptx`: presentation model, layout and PPTX engine reused from Forge.

## Domain Contract Documents

- [Sprite authoring and export](packages-react-forge-sprite-contract.md) (unreleased).

- [Game SFX and WAV export](packages-react-forge-sfx-contract.md).
- [Figma creation and editing](packages-react-forge-figma-contract.md).
- [Public documentation](apps-react-forge-docs-foundation.md).
- [Complete requirements](packages-react-forge-requirements.md).
- [Session-based MCP contract](packages-react-forge-mcp-contract.md).
- [Node session and CLI contract](packages-react-forge-contract.md).
- [Native engines and preservation contract](crates-react-forge-contract.md).
- [Validation and benchmark evidence](packages-react-forge-validation.md).
- [Public npm release contract](packages-react-forge-release-contract.md).
- [Existing Forge foundation](crates-forge-foundation.md).

## Cross-Domain Invariants
- Support Node.js 24, React 19.2.8 with react-reconciler 0.33.0, and macOS/Windows/glibc Linux on x64 and arm64. Use the repository-pinned Rust toolchain. The npm library and six native packages are public; Rust crates remain private and unpublished.
- The external `0.0.1` npm packages only reserve seven names. The `0.1.0` source tag passed all packaging gates but failed before npm publication. `0.1.1` is the first functional npm release; all seven packages were published through the exact-tag Trusted Publisher workflow and use `0.1.1` as `latest`.
- TypeScript executes React; Rust processes validated serializable format-specific models. Rust is a project-specific exception to the default Go language; local files and explicit exports are an exception to default R2 storage.
- Sessions live only in memory. Explicit export is the local persistence boundary. No automatic recovery, revision archive, hosted service, telemetry, arbitrary URL fetching, runtime downloads or external conversion dependencies. Figma alone permits official MCP publication and scoped image uploads, with partial/unknown outcomes and external receipts instead of atomic remote replacement.
- Persistent document/node identities are UUID v7. React keys and useId values do not become persistent identities. Figma native IDs are separately mapped to those logical identities.
- Preserve unrelated Office parts/XML; reject unsafe or unprovable edits transactionally. Never flatten opaque imported content. Existing Forge CLI/MCP behavior and default fonts remain stable.
- Registered assets and render-relevant asynchronous work must settle before export pins a revision. Later commits cannot change an export already pinned. Cancellation has no automatic timeout and cannot misreport an already completed atomic publication.
- Source-backed requirements must remain intact. Track missing evidence honestly; do not close #968 until all acceptance criteria pass. Direct Microsoft Office validation and PDF/UA certification are not claimed.
- Public user guides are owned by the consolidated documentation app at `https://oss.delino.io/react-forge/`; they cover the released library, CLI, MCP, four local document formats, Figma, and clearly marked unreleased SFX without exposing internal implementation or overstating evidence.

## Change Policy
Update the project index, affected domain contracts, relevant AGENTS rules, examples and validation together when interfaces or ownership change. Internal rollback uses source revision rollback and rebuild without rewriting exported files.

## References
- [Repository defaults](repository-defaults.md).
- [Forge project](project-forge.md).
- [Issue #968](https://github.com/delinoio/oss/issues/968).

## Sprite Extension (Unreleased)
The 2026-09-25 sprite request adds `Format.Sprite` and `/sprite` through the existing library, CLI and MCP. React declares layers, pixel grids, shapes, local images and timed animations. One `.sprite.zip` export contains a PNG atlas, individual frames and JSON metadata. This source implementation is not part of npm 0.1.1; it adds no hosted artwork generation or sprite import. Follow the sprite contract for bounds, geometry and evidence.
