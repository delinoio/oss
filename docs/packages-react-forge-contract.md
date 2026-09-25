# React Forge Node Contract

## Scope
`packages/react-forge` owns the public `@delino/react-forge` library, one-shot TSX CLI and [session-based stdio MCP](packages-react-forge-mcp-contract.md). The [complete requirements](packages-react-forge-requirements.md) are normative; this contract records implementation boundaries, not a reduced delivery scope.

The local Office/PDF behavior below is extended by the [Figma contract](packages-react-forge-figma-contract.md), which separately specifies remote creation/editing, host-owned authentication, explicit publication, partial outcomes and receipts. Its network and remote-state exceptions do not apply to the local formats.

## Runtime and Language
Node.js 24 on macOS, Windows and glibc Linux, each with x64 and arm64, TypeScript, React 19.2.8 and react-reconciler 0.33.0. TSX executes trusted caller code with ordinary caller permissions. The reconciler must implement real React commits, refs, effects, Suspense, transitions and Activity; invoking components manually is forbidden.

## Users and Operators
Node.js developers authoring reusable document tasks. The local stdio MCP interface serves trusted document tasks. There is no hosted service or GUI.

## Interfaces and Contracts
Common session APIs own settled `snapshot({ signal })` inspection without file generation, creation, Office import, inspection, root rendering, target mounting/updating, revision-aware asynchronous measurement, Buffer/file export, diagnostic subscriptions and disposal. Format imports are `@delino/react-forge/pptx`, `/docx`, `/xlsx`, and `/pdf`. Separate models retain each format's semantics. Refs expose typed document-node handles; layout effects follow React commit timing, not native layout completion.

Mutations serialize per session. Failed changes never publish partial state; invalid latest renders fail export instead of falling back to older content. Target handles are document-scoped and mounted regions cannot overlap. Export waits for relevant Suspense work and registered assets, then pins an immutable revision. Effects' independent asynchronous work remains the caller's responsibility. AbortSignal and disposal release pending work with no automatic timeout.

PPTX list, image, shape, chart and connector components are leaves. Nonempty React children on these components fail with `malformed_input`; they are never silently discarded.

`react-forge run <entry.tsx> --output <file> [--data <json>] [--overwrite] [--json]` loads the default task function with `{ data, signal }`, exports its returned session and disposes it. Help, version, typed human/JSON failures and process-signal cleanup are required. Include the TSX loader dependency.

## Storage
In-memory sessions and explicit local input/output only, a documented departure from R2 defaults. Accept bytes or explicit paths for documents/images/fonts. File exports sharing a canonical directory identity are serialized in invocation order across sessions, including case and symlink aliases; a cancelled waiter cannot release an earlier publisher. Different directories and buffer exports remain independent. Output conflicts fail by default; explicit overwrite uses same-filesystem atomic publication. Imported sources and canonical path aliases reject replacement with `unsupported_edit`, including explicit overwrite; callers must select a separate output path. Portable unconditional rename cannot protect an external save after fingerprinting. Failed/canceled operations preserve existing files and clean owned temporary output. Disposal never deletes exported files.

## Security
No URL downloads or external relationship fetching. The package is not an untrusted-code sandbox. Publish and enforce all resource ceilings from the requirements through capabilities and diagnostics. Service authentication, credentials, isolation and global resource governance belong to integrators.

## Logging
Structured callbacks include operation/stage, format, revision, duration and stable error classification. No source contents/XML, asset bytes, credentials or host paths. Subscriber failures cannot corrupt session state. Native tracing is connected per operation without installing a global subscriber.

## Build and Test
Package-local typecheck, lint, test, build, native integration and workspace/installed CLI tests on the supported runtime. Test React behavior, concurrent updates/exports, stale/overlapping handles, cancellation/publication races, latest-render failures, external source modification and cleanup. Record benchmark time, memory and event-loop responsiveness without an SLO. Root Cargo and existing Forge regression tests remain required.


## Dependencies and Integrations
The N-API adapter owns native work; React and JavaScript callbacks remain on the JavaScript thread. Pin the React/reconciler pair. The Rust engines are format-processing libraries independent of N-API.

## Change Triggers
Synchronize project/native contracts, package ownership rules, examples, capabilities and acceptance evidence with API changes. Generated dist is ignored and removed from final worktrees.

## References
- [Project](project-react-forge.md).
- [Requirements](packages-react-forge-requirements.md).
- [Repository defaults](repository-defaults.md).

## Implemented Sessions and Formats
All four format engines connect through native session operations. Spreadsheet inspection includes sheet names and zero-based address/range data; mounted cells and rules may omit these to retain their imported selection. DOCX/XLSX support native bar, line and pie charts. Spreadsheet generation and mounted edits cover all specified value, formatting, conditional-formatting and validation families. PDF has independent text/list/table flow, images, shapes, links and explicit/automatic page breaks.

`registerFont(bytesOrPath)` registers bounded fonts. System discovery is default; `createSession(format, { systemFonts: false })` requires explicit caller fonts. Assets settle before the revision is pinned. Workspace tasks for all four formats and `examples/edit-office.tsx` demonstrate the library and CLI interfaces. The installed-consumer test packs the workspace source package, installs it into a temporary project and exercises the real binary for all four formats. The public distribution has a separate seven-package assembled-candidate test.

`examples/travel-ir.tsx` is the designed 12-slide English ROAM investor-pitch example. It uses reusable React components, module-relative local image registration with cancellation, native text/table/chart output, and an embedded chart workbook. Its committed assets and exact image-generation prompts live in `examples/travel-ir-assets`; generation itself requires no network or converter. That directory documents font references, external market-statistic provenance and all fictional financial assumptions. Keep assumptions and formulas consistent, retain the distinction between concept imagery and a shipping app, and keep generated presentations/previews untracked.

Operation diagnostics include `source` (`javascript`/`native`); native records additionally identify the generate/inspect/update operation and started/completed/failed status. Native events are buffered per worker operation and delivered on the JavaScript thread after worker completion. A bounded whitelist excludes dependency events, arbitrary fields, paths and content, and callback exceptions cannot change outcomes. The reconciler suite also covers deferred Suspense updates, awaited action-state transitions and rejected-promise boundary recovery.

Geometry distinguishes native PDF/PPTX page boxes from Office authoring measurements. DOCX reports `word_flow` boxes in points before Word pagination; mounted DOCX blocks report `mounted_region` coordinates using source section/cell constraints, and omit geometry when the source width is unresolved without preventing safe export. XLSX reports `worksheet` coordinates in points using the conventional 7-pixel maximum digit-width conversion, declared dimensions and merges; these are not print-page or font-substitution measurements. Office applications remain authoritative for final layout. Non-layout-bearing handles have no geometry and return `invalid_target`. Newly authored trees are inspectable; their structural targets use `editable: false` because edits go through root React updates, while `mount` is the imported-region interface. `capabilities` exposes the supported runtime, format import boundaries, coordinate spaces, font policy and resource limits without I/O.

Pinned exports snapshot font registrations along with model/assets, and font changes participate in revision identity. Preparing an export rechecks mounts/assets that arrive while Suspense is pending. Disposal owns in-flight mounts and returns one shared cleanup promise. Imported-source publication is rejected before creating output, including source symlink aliases; fingerprints never authorize source replacement. Explicit file reads reject non-regular files without blocking on FIFOs. CLI loading supports the Node 24 CommonJS default-export namespace as well as ESM; process-signal tests verify cleanup and redacted task failures. Native failures expose bounded whitelisted model locations, never parser/source paths.


## Validation and CI
The `react-forge` CI job is selected on affected PRs and main pushes. Its six native runners cover macOS x64/arm64, Windows x64/arm64 and glibc Linux x64/arm64 with Node 24; each builds the private binding, runs native and existing Forge regressions plus Clippy, and invokes package-owned build/typecheck/lint/test tasks through Turbo. Native/system-font-dependent build, test, rendering and benchmark tasks are never Turbo-cacheable. The job has no publication credentials or action. A dependent CI job verifies the complete seven-package candidate; the separate tag workflow owns release publication.

Test-only LibreOffice/Poppler and pinned Python inspection dependencies produce created/edited Office renders plus independent native PDF renders. Original external Office fixtures are rendered alongside edits so original pagination, including deliberate blank pages, is distinguished from lost output. Structural XML, expected text, visible chart series, CJK text and PDF semantics are checked. Tool/font versions and checksums are recorded without redistributing system fonts. CI retains render and benchmark artifacts for seven days and removes generated package dist even on failure.

`benchmark` runs representative authoring for all formats, preserving edits for three Office formats, and near-limit trees/slides in fresh Node processes. Reports include preparation/export duration, output size, peak RSS, event-loop p99/max delay and utilization, OS/CPU and dependency versions. They are observations without numerical guarantees. See [validation evidence](packages-react-forge-validation.md) for reproduction and limitations.

The private source workspace manifest omits `os`/`cpu` installation filters so unrelated cross-platform pnpm tools retain byte-clean stdout (including protoc plugins and clibox). Native build and loading independently enforce Node.js 24 and the six declared platform/architecture targets.


## Platform Extension
The 2026-09-24 PR #970 follow-up supersedes issue #968's original macOS-arm64-only boundary. `src/native-platforms.json` is the shared build/runtime inventory of six Node-platform/architecture/Rust-target tuples. Native builds explicitly select the matching Rust target and emit `react-forge.<id>.node`; loading selects only that host artifact, never another architecture or an external binary. Packed test consumers contain the locally built artifact and must be rebuilt on another host. Host-neutral workspace metadata prevents pnpm warnings from corrupting unrelated protocol stdout. Unsupported architectures, Node majors and musl return `unsupported_package` before binding load.

`capabilities.runtime.hosts` lists all supported platform/architecture IDs. Linux uses Fontconfig and installed fonts; CI installs Noto CJK/core/color emoji. Windows uses DirectWrite/system fonts and registers the repository's OFL CJK fixture on disposable runners; callers can also use `registerFont`. Missing fonts remain typed recoverable failures, never downloaded implicitly.

File paths use native URL conversion, including Windows drive letters and escaped characters. Canonical directory identities serialize publications. Source comparisons conservatively reject case aliases on Windows/macOS even after source removal and on case-sensitive volumes; macOS also rejects Unicode normalization aliases. Files are flushed before atomic publication on every host. Unix additionally synchronizes the parent directory; Windows does not claim directory crash durability because Node cannot fsync directories there. Windows Ctrl+C and Ctrl+Break cancel and dispose with status 130, validated in an isolated real console. Unix SIGINT/SIGTERM retain status 130/143. Forceful termination cannot guarantee cleanup on any host.

All six runners execute native/React tests, installed archive/CLI consumers, system-font PDF/CJK/RTL/emoji tests, the complete ROAM investor example and benchmarks. The example uses host-appropriate text families, bounded text shrink and two-line table rows to accommodate different metrics. macOS and Linux additionally run LibreOffice/Poppler structural, extraction and raster checks; Windows runs the same native exports/imports and a real console test without depending on external Office converters. No Microsoft Office validation is claimed.

PPTX `PresentationTextStyle` exposes font family/size, bold, italic, underline and text color only. Paragraph alignment belongs to `Paragraph.align`; table-cell fills use `Cell.fill`. Unknown style fields, including shared-style background, alignment, language and direction, are rejected at runtime instead of losing caller intent. Other formats retain their own style capabilities.

Each export/measurement operation retains its pinned revision for JavaScript completion diagnostics and error context, including publication failures after native processing. Concurrent later snapshots cannot relabel earlier results; failures before snapshot preparation report the operation's initial revision.

## MCP Extension
The explicit MCP follow-up supersedes the original new-MCP exclusion. `react-forge mcp [--cwd <directory>]` exposes the four document formats, Figma and the source WAV extension through memory-only sessions, inline/file TSX execution and explicit local export or remote publication. Follow the [MCP contract](packages-react-forge-mcp-contract.md) for tool inputs, retained state, cancellation, protocol isolation and failure outcomes. `McpTaskContext` and `McpSessionTask` are type-only exports; the existing library and one-shot CLI remain compatible.

## Public distribution

The private source workspace generates a public `@delino/react-forge` package plus six `@delino/react-forge-<host-id>` native packages. Main optional dependencies pin the exact source version. Generated platform manifests use `os`, `cpu`, and Linux `libc` filters; the source workspace manifest stays host-neutral. Runtime loading selects the exact matching package and rejects a missing or mismatched binding without downloads or compilation. See [release contract](packages-react-forge-release-contract.md).

## SFX extension

The [SFX contract](packages-react-forge-sfx-contract.md) adds `Format.Wav`, the `/sfx` component subpath, generation-only PCM WAV exports and seconds-based `timeline` geometry. It shares the session, revision, cancellation, atomic-output, diagnostics and six-host native boundaries. SFX does not register image/font assets or import audio. The source feature is unreleased; preserve existing document/Figma behavior.
