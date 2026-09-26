# React Forge Complete Requirements

Source: issue #968, retrieved 2026-09-24. This preserves the complete original issue scope and is not a claim of completed implementation. The later public npm distribution decision supersedes only this issue's private-package and publication exclusions; see `packages-react-forge-release-contract.md`. See the project and domain contracts for ownership and validation evidence.

## Summary

Create **React Forge**, an internal Node.js library and TSX CLI for generating PPTX, DOCX, XLSX, and PDF documents with React and Rust.

Support persistent React sessions, including state updates and asynchronous rendering. Support importing existing PPTX, DOCX, and XLSX files, inspecting editable regions, and mounting React subtrees into those regions while preserving unrelated content.

The issue originally limited support to **Node.js 24, React 19.2.8, and macOS arm64**. The explicit 2026-09-24 follow-up on PR #970 expands the supported platform scope to **macOS, Windows and glibc Linux on x64 and arm64**, with the same Node and React versions. This follow-up supersedes the original platform exclusion; the other issue requirements remain in force. Deliver all agreed capabilities before closing this issue. There is no fixed deadline, public release, or feature flag.

## Evidence

- Foundation: [PR #967](https://github.com/delinoio/oss/pull/967), inspected at commit `72efa39abc3a8009f83d9eb6e17954f788606b82`.
- That PR provides a Rust presentation model, validation, layout, PPTX generation, preservation-aware editing, and a CLI/MCP interface.
- It does not provide a React renderer or DOCX, XLSX, and PDF engines.
- Primary users are repository developers writing Node.js/TypeScript document-generation and editing tasks.
- Repository contracts require internal documentation in `docs/`, synchronized ownership rules in relevant `AGENTS.md` files, generated-output cleanup, and appropriate Rust and Node validation.
- Searches for `forge` and `"react-forge"` found no existing GitHub issue.
- No repository issue template or `PRD` label currently exists.

## Current Gap

Developers cannot currently compose documents using React components, Context, Hooks, asynchronous data, and reusable document elements.

The existing presentation engine also does not cover word-processing documents, spreadsheets, or independent PDF authoring. Existing DOCX and XLSX files require preservation-aware editing that retains unsupported structures during supported changes.

## Proposed Scope

### Architecture and ownership

- Introduce the stable project identifier `react-forge` and product name **React Forge**.
- Own the private TypeScript library and CLI in `packages/react-forge`, with a dedicated N-API adapter in `crates/react-forge-node`.
- Reuse the Forge implementation from #967 as repository dependencies. Extend the Rust engine family for DOCX, XLSX, PDF, and reusable package-preservation functionality.
- Preserve the existing Forge CLI/MCP interfaces, supported editing boundaries, and default font behavior. New React Forge behavior must not silently change those consumers.
- Execute React components and reconciliation in JavaScript. Pass validated, serializable document data to Rust for document processing and export.
- Keep JavaScript values, component functions, Hooks, and callbacks outside native worker computations.
- Use separate presentation, word-processing, spreadsheet, and PDF models. Share styles, themes, assets, and applicable infrastructure.
- Keep format processing independent of N-API. Browser/WASM delivery remains outside this issue.
- Generation and editing require no Office, LibreOffice, Python, external conversion service, or runtime installation/download.
- Use the repository’s pinned Rust toolchain and pin the React/reconciler compatibility combination. The inspected `react-reconciler@0.33.0` declares compatibility with React 19.2.
- Keep packages private and unpublished. Document the Rust and local-file-storage choices as project-specific departures from repository defaults.

### Required format capabilities

| Format | Required capabilities |
|---|---|
| PPTX | #967’s rich text, lists, PNG/JPEG images, shapes, merged tables, editable bar charts, connectors, and row/column/canvas layouts. |
| DOCX | Rich paragraphs, headings, lists, tables and merged cells, images, hyperlinks, sections, page breaks, headers/footers, and editable 2D bar, line, and pie charts. |
| XLSX | Multiple worksheets; text, numeric, date, and Boolean cells; formulas; formatting; merges; row/column dimensions; freeze panes; autofilters; hyperlinks; and editable 2D bar, line, and pie charts. |
| PDF | Independent document authoring with pages, text, images, shapes, tables, links, flow layout, automatic pagination, and semantic tagging. |

Charts include data, legends, labels, and axes where applicable. Preserve native Office chart editability and associated data.

XLSX conditional formatting must support cell-value rules, formula rules, color scales, data bars, and icon sets. Data validation must support lists, integers, decimals, dates, times, text length, and custom formulas.

Store formulas and optional caller-supplied cached results. Request recalculation by spreadsheet applications when appropriate. React Forge does not calculate formulas or represent cached results as internally calculated values.

PDF output is independent of Office authoring. Office-to-PDF conversion and PDF import/editing are excluded.

### Import and preservation

- Import PPTX, DOCX, and XLSX from bytes or explicit local paths.
- Inspect supported structures and obtain document-scoped target handles.
- Mount and update React subtrees in selected editable regions.
- Preserve unsupported elements as opaque content and preserve unrelated package parts and XML outside edited regions.
- Reject edits when preservation cannot be established. Do not flatten or silently discard unsupported content.
- Reject overlapping mounted regions and edits that would damage unsupported elements or unresolved references.
- Support structural updates in newly authored documents. Imported structural edits remain subject to the preservation boundary.
- Preserve unsupported conditional-formatting and validation extensions without exposing them as editable supported rules.
- Retain #967’s package restrictions, including rejection of encrypted, signed, macro-enabled, legacy, and unsupported Strict OOXML packages.
- Required positive fixtures must demonstrate supported edits in externally authored documents; preservation errors must not substitute for implementing the agreed supported cases.

### React support and document sessions

Support the non-DOM React 19.2.8 features applicable to document rendering:

- Function and class components, composition, props, children, keys, and Fragments.
- Context, memoization, state, reducers, custom Hooks, refs, and imperative handles.
- Effects and their cleanup, Strict Mode, error boundaries, profiling callbacks, and external-store subscriptions.
- `lazy`, Suspense, `use` with promises, transitions, deferred updates, and applicable action-state behavior.
- Activity visibility/state behavior, with hidden content excluded from exported document content.

Exclude React DOM, browser events and measurements, arbitrary HTML/CSS compatibility, Server Components, and experimental APIs.

Use a real reconciler; do not manually invoke function components to emulate React.

A ref exposes a typed document-node handle. Geometry is queried asynchronously for a specific revision. `useLayoutEffect` follows React commit timing and does not imply that native document layout has completed.

Sessions remain in memory. Explicit export is the persistence boundary. There is no automatic save, recovery store, persistent document cache, or revision archive.

Serialize mutations within each session and prevent older native work from overwriting newer committed state. Failed changes must not partially mutate the document. An invalid latest render must produce an error rather than silently exporting a previous valid result.

Export waits for the targeted render’s Suspense work and registered assets, then pins a revision. Later updates cannot change an export already operating on that revision. The caller manages asynchronous work started independently by Effects.

There is **no automatic timeout**. Unresolved work can remain pending until canceled. Provide `AbortSignal` support and dispose-time cleanup. Cancellation must prevent later publication when received before the atomic publication boundary; publication that already completed must be reported truthfully.

### Library and CLI interfaces

Provide the private `@delino/react-forge` package with common session APIs and format-specific component imports. The npm scope reflects the subsequent package-naming decision; the project ID and CLI executable remain `react-forge`:

- `@delino/react-forge/pptx`
- `@delino/react-forge/docx`
- `@delino/react-forge/xlsx`
- `@delino/react-forge/pdf`

The API must cover session creation, Office import, structure inspection, root rendering, target mounting/updating, revision-aware measurement, Buffer export, file export, diagnostic subscription, and disposal.

Use typed format/error enums and document-scoped handles. Follow Forge’s UUID-v7 identity contract where persistent document/node identifiers are used. React keys and `useId` values are not persistent document identities.

Accept document, image, and font bytes or explicit local paths. Do not automatically download URL assets or follow external document relationships.

Expose typed errors containing stable classifications and safe diagnostic context, including applicable stage, format, revision, and node/model location. Distinguish malformed input, unsupported packages/edits, invalid targets, conflicts, resource limits, missing fonts, layout overflow, cancellation, disposed sessions, and I/O failures.

Provide this one-shot CLI:

```text
react-forge run <entry.tsx> --output <file>
  [--data <json>]
  [--overwrite]
  [--json]
```

The TSX module’s default-exported task function receives `{ data, signal }` and returns a document session. It can author a new document or import and edit an existing Office document. The CLI exports the prepared result and disposes the session.

Include the TSX execution dependency in the package. Provide help, version information, human-readable errors, optional structured JSON results, and cancellation through process termination signals.

CLI and library messages, README, contracts, examples, and troubleshooting documentation are written in English. The original issue excluded watch mode, a new MCP interface and a GUI. The subsequent explicit MCP request supersedes only the MCP exclusion under [the MCP contract](packages-react-forge-mcp-contract.md); watch mode and GUI remain excluded.

### Layout, fonts, and accessibility

- Default to automatic system-font discovery and fallback. Permit caller-supplied fonts.
- Allow environment-dependent layout differences and document that limitation.
- Support CJK, RTL and mixed-direction text, and color emoji.
- Fail export with an actionable typed diagnostic if fallback still cannot represent required glyphs or color emoji. Do not silently replace or omit them.
- Preserve font embedding permissions. Apply React Forge’s font policy without changing existing Forge consumers’ defaults.
- Font checks for newly rendered content must not require rewriting or rendering untouched opaque imported content.
- PDF flow layout splits paragraphs and tables across pages and repeats table headers. Indivisible content that cannot fit produces an overflow error.
- Office applications remain responsible for their final layout and pagination.
- Support semantic headings, table headers, alternative text, and language information where applicable.
- Tagged PDF must preserve headings, paragraphs, lists, table semantics, links, alternate text, language, and logical reading order.
- Do not claim PDF/UA-1 or PDF/UA-2 conformance.

### Data safety and limits

Default file export fails if the destination exists. Explicit overwrite permits atomic replacement. Source safety is enforced by rejecting replacement of an imported source or its canonical path aliases, even with explicit overwrite (`unsupported_edit`). Export to a separate path: fingerprint-then-rename cannot safely implement conditional replacement against external saves.

Use same-filesystem temporary output and atomic publication. Failed or canceled operations must preserve existing files and clean up owned temporary output. Dispose releases session-owned resources and does not delete exported files.

Adopt these limits:

| Resource | Limit |
|---|---:|
| Office package input/output | 256 MiB |
| PDF output | 256 MiB |
| Total expanded Office package | 512 MiB |
| ZIP entries | 10,000 |
| Individual ZIP/XML part | 64 MiB |
| XML depth | 128 |
| XML nodes per part | 1,000,000 |
| Serialized newly rendered React document tree | 16 MiB |
| Rendered tree depth | 48 |
| Rendered tree nodes | 20,000 |
| Individual image | 64 MiB and 64,000,000 pixels |

Opaque preserved source content is governed by package limits. Retain applicable stricter existing PPTX limits. Publish limits through documentation and a diagnostic/capability interface.

Validate before expensive processing where possible. Reject unsafe package paths, duplicate/ambiguous identities, DTDs, external entities, invalid references, and malformed document structures. Do not execute embedded content.

Caller-supplied React/TSX is trusted code running with the caller’s permissions. This library is not an execution sandbox. Service authentication, tenant isolation, process-wide resource governance, and credentials remain the integrating caller’s responsibility.

### Operations, diagnostics, and support

- Support and validate Node.js 24 and React 19.2.8 on macOS, Windows and glibc Linux, each with x64 and arm64.
- Distribute through the repository workspace only. Public npm publication, prebuilt distribution, and public documentation are deferred.
- No feature flag applies: use is explicitly selected through package imports and API/CLI calls. There are no rollout cohorts or remote kill switches.
- No hosted service, database, queue, scheduler, billing integration, remote telemetry, or alerting infrastructure is introduced.
- Expose structured diagnostic callbacks and connect native `tracing` events without installing a global logger.
- Report operation/stage, format, revision, duration, and stable error classifications. Do not log document contents, source XML, image/font bytes, host paths, or credentials.
- Record representative processing time, memory usage, and event-loop responsiveness. There is no promised latency or throughput SLO.
- Document setup, supported capabilities, limits, cancellation, preservation failures, font troubleshooting, and recovery by correcting input and retrying.
- Internal rollback uses repository revision rollback and rebuild; it does not rewrite previously exported user files.
- Add the canonical `docs/project-react-forge.md`, relevant domain contracts, ownership rules, and documentation catalog entries. Keep existing Forge documentation synchronized where shared engine contracts change.
- Keep all repository-owned `dist` output generated, untracked, and removed from the final worktree.

## Acceptance Criteria

- A repository developer can use the library and CLI to generate all four formats with the required native document features.
- React sessions support the agreed synchronous and asynchronous behavior, updates, revision-aware measurements, export, cancellation, and cleanup.
- Existing PPTX, DOCX, and XLSX files can be inspected and safely edited through mounted React regions.
- Supported edits preserve unrelated original content; unsupported or unsafe edits fail explicitly without partial changes.
- DOCX/XLSX charts and all specified XLSX conditional-formatting and validation rules pass creation and preservation-aware editing tests.
- Exported formulas request application recalculation without claiming a built-in calculation engine.
- CJK, RTL, mixed-direction text, color emoji, missing-font failure behavior, and tagged PDF semantics pass representative tests.
- Concurrency, stale revisions, external source changes, overwrite conflicts, cancellation, and file publication produce the documented behavior.
- All six supported macOS/Windows/glibc Linux x64/arm64 runtimes complete library and installed/workspace CLI tests.
- Automated structural, preservation, and rendering checks pass. Direct Microsoft Office application validation is a follow-up and must not be claimed as completed evidence.
- Benchmarks, English documentation, examples, diagnostics, and repository integration are complete.
- Existing Forge CLI/MCP behavior remains covered by regression tests.
- All required scope is complete before this issue is closed; there is no partial-format completion criterion.

## Test Scenarios

- Generate representative PPTX, DOCX, XLSX, and PDF documents through both library and CLI entry points.
- Import externally authored Office fixtures, inspect targets, apply supported edits, reopen outputs, and verify expected semantic changes.
- Compare unchanged package-part payloads and unaffected XML regions. Exercise opaque content, extensions, embedded chart data, and relationship/name collisions.
- Test nested components, Context, custom Hooks, state updates, memoization, refs, Effects and cleanup, Strict Mode, error boundaries, Suspense, lazy loading, promise rejection, transitions, Activity, and external-store updates.
- Confirm export waits for relevant pending work, remains bound to its pinned revision, and can be canceled without an automatic timeout.
- Test invalid latest renders, superseded work, concurrent mutations/exports, overlapping regions, stale target handles, and disposed sessions.
- Test malformed ZIP/XML, expansion limits, traversal, entities, invalid references, protected package types, oversized images/trees, and every published resource limit.
- Test file conflicts, explicit overwrite, external source modification, cancellation before publication, publication/cancellation races, cleanup, and retry.
- Test spreadsheet value types, formulas and cached values, charts, merges, conditional formatting, data validation, and unsupported-rule preservation.
- Test PDF pagination, repeated table headers, oversized indivisible content, semantic tags, reading order, links, and text extraction.
- Test CJK/RTL/emoji fixtures with system discovery and explicit test fonts; cover missing glyphs, unavailable color rendering, and embedding restrictions.
- Render outputs with test-only LibreOffice/PDF tooling and retain reproducible structural/rendering evidence. Record tool and font versions; do not describe this as Microsoft Office validation.
- Verify diagnostic redaction, callback behavior, CLI JSON/human output, invalid arguments, task failures, and process-signal cleanup.
- Run root `cargo test` after required repository preparation, appropriate package type/lint/test/build checks, native binding/CLI integration tests, and affected CI contract checks.
- Benchmark representative and near-limit workloads without introducing a numerical performance guarantee.

## Out of Scope

- Public npm/crates publication, public documentation hosting, and release infrastructure.
- musl Linux, CPU architectures other than x64/arm64, other Node/React versions, and browser/WASM support.
- React DOM, arbitrary HTML/CSS rendering, browser interactions, RSC, and experimental React APIs.
- Untrusted-code sandboxing and hosted multi-tenant execution.
- Office-to-PDF conversion and PDF import/editing.
- Complete editing of every OOXML feature, macros, encrypted/signed documents, and unsupported Strict/legacy formats.
- Spreadsheet formula calculation, pivots, 3D/combination/scatter charts, and additional advanced features beyond the specified list.
- DOCX tracked-change authoring and equation-object authoring.
- Automatic persistence/recovery, historical revision storage, automatic timeouts, URL asset fetching, watch mode or a GUI. The original new-MCP exclusion is superseded by the explicit session-based MCP follow-up.
- PDF/UA certification and direct Microsoft Office validation in this initial issue.
- Feature flags, remote telemetry, and cloud operations.


## MCP follow-up
The subsequent explicit request adds a local stdio `react-forge mcp` server for all existing formats, TSX code/file inputs, retained memory sessions and state, inspection/measurement, explicit file export or Figma publication, and cleanup. It preserves the original complete-format scope and existing Forge interfaces. The [MCP contract](packages-react-forge-mcp-contract.md) defines the added acceptance boundary; it adds no HTTP hosting, public distribution or automatic persistence.

## 2026-09-25 static 3D follow-up

This additive approved scope does not remove the original document requirements. Provide generation-only static GLB and binary FBX 7.4, Format.Glb/Fbx, public subpaths and independent SceneSession. Share bounded forge-scene data while keeping forge-glb/forge-fbx independent and JavaScript outside native workers. Support indexed triangles, hierarchy, transforms, normals/tangents/UVs, basic PBR textures/opacity, both camera projections and directional/point/spot lighting. Coordinates are meters/right-handed/Y-up; exporters own interchange conversion. Preserve revision pinning, Suspense, cancellation, diagnostics and atomic saves. Register copied binary assets outside React JSON; retain tree/image limits and enforce 256 MiB aggregate assets/output.

Deliver original reusable TSX audio-product examples (headphones, DAC/amplifier, stand and combined studio), deterministic geometry/texture source and provenance. Validate actual exports using Khronos Validator, independent ufbx, pinned Blender 4.5 LTS empty-scene imports and 2048-pixel front/back/oblique/detail renders without material/mesh repair, plus interactive local GLB viewing. Inspect and correct visual faults, record versions/hashes/observations, and state unexecuted hosts honestly. Extend CLI/MCP, installation consumers, six-platform engine CI and Linux visual CI. Run root Rust tests, relevant Clippy, package build/typecheck/lint/tests/example checks and public-doc tests; update contracts/AGENTS and remove generated dist. Scene import/editing, animation, rigging, refraction, transmission and advanced coatings are excluded. Full details are in `packages-react-forge-scene-contract.md`.

## Sprite Follow-up
The explicit 2026-09-25 request extends React Forge to author game sprites and requires real implementation and tests. The [sprite contract](packages-react-forge-sprite-contract.md) defines React pixel/shape/image composition, palettes, frame animations, native rasterization, PNG atlas and individual PNG exports with JSON metadata in one atomic archive, and library/CLI/MCP integration. This additive extension was included in npm `0.2.0`; it does not replace any Office/PDF/Figma requirements.

## SFX follow-up

The subsequent explicit game SFX request adds offline React-authored sound generation and WAV export, validated with a zombie-game gunshot. The [SFX contract](packages-react-forge-sfx-contract.md) specifies the additional components, independent native model, synthesis bounds, CLI/MCP behavior and evidence. This additive scope does not reduce any original document-format requirement.
