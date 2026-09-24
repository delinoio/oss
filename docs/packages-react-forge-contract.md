# React Forge Node Contract

## Scope
`packages/react-forge` owns the private `react-forge` library and one-shot TSX CLI. The [complete requirements](packages-react-forge-requirements.md) are normative; this contract records implementation boundaries, not a reduced delivery scope.

## Runtime and Language
Node.js 24 on macOS arm64, TypeScript, React 19.2.8 and react-reconciler 0.33.0. TSX executes trusted caller code with ordinary caller permissions. The reconciler must implement real React commits, refs, effects, Suspense, transitions and Activity; invoking components manually is forbidden.

## Users and Operators
Repository developers authoring reusable document tasks. There is no public package, hosted service, GUI or MCP interface.

## Interfaces and Contracts
Common session APIs own creation, Office import, inspection, root rendering, target mounting/updating, revision-aware asynchronous measurement, Buffer/file export, diagnostic subscriptions and disposal. Format imports are `react-forge/pptx`, `/docx`, `/xlsx`, and `/pdf`. Separate models retain each format's semantics. Refs expose typed document-node handles; layout effects follow React commit timing, not native layout completion.

Mutations serialize per session. Failed changes never publish partial state; invalid latest renders fail export instead of falling back to older content. Target handles are document-scoped and mounted regions cannot overlap. Export waits for relevant Suspense work and registered assets, then pins an immutable revision. Effects' independent asynchronous work remains the caller's responsibility. AbortSignal and disposal release pending work with no automatic timeout.

`react-forge run <entry.tsx> --output <file> [--data <json>] [--overwrite] [--json]` loads the default task function with `{ data, signal }`, exports its returned session and disposes it. Help, version, typed human/JSON failures and process-signal cleanup are required. Include the TSX loader dependency.

## Storage
In-memory sessions and explicit local input/output only, a documented departure from R2 defaults. Accept bytes or explicit paths for documents/images/fonts. Output conflicts fail by default; explicit overwrite uses same-filesystem atomic publication. Verify an imported source's fingerprint before replacing it. Failed/canceled operations preserve existing files and clean owned temporary output. Disposal never deletes exported files.

## Security
No URL downloads or external relationship fetching. The package is not an untrusted-code sandbox. Publish and enforce all resource ceilings from the requirements through capabilities and diagnostics. Service authentication, credentials, isolation and global resource governance belong to integrators.

## Logging
Structured callbacks include operation/stage, format, revision, duration and stable error classification. No source contents/XML, asset bytes, credentials or host paths. Subscriber failures cannot corrupt session state. Native tracing is connected per operation without installing a global subscriber.

## Build and Test
Package-local typecheck, lint, test, build, native integration and workspace/installed CLI tests on the supported runtime. Test React behavior, concurrent updates/exports, stale/overlapping handles, cancellation/publication races, latest-render failures, external source modification and cleanup. Record benchmark time, memory and event-loop responsiveness without an SLO. Root Cargo and existing Forge regression tests remain required.

Current implementation checkpoint: the real reconciler and PPTX N-API path cover library generation/import/mounts, state/effects/refs/Context, Suspense/use/lazy, Activity, error boundaries, external stores, revision-pinned exports, file conflicts and the workspace TSX CLI. This is incomplete implementation evidence, not acceptance of #968. The remaining format engines, font policy, extended React cases, installed CLI, rendering evidence, benchmarks and CI integration remain required.

## Dependencies and Integrations
The N-API adapter owns native work; React and JavaScript callbacks remain on the JavaScript thread. Pin the React/reconciler pair. The Rust engines are format-processing libraries independent of N-API.

## Change Triggers
Synchronize project/native contracts, package ownership rules, examples, capabilities and acceptance evidence with API changes. Generated dist is ignored and removed from final worktrees.

## References
- [Project](project-react-forge.md).
- [Requirements](packages-react-forge-requirements.md).
- [Repository defaults](repository-defaults.md).

DOCX and XLSX now have real native session/React connections. Spreadsheet inspection includes worksheet names and zero-based address/range data; mounted cells and rules may omit their address/range to retain the imported selection. Workspace library tests exercise DOCX rich content and charts, spreadsheet scalar/date/formula values, dimensions, merges, rules, native charts and external-document region replacement. Font/layout, PDF, full CLI and acceptance evidence remain ongoing work.

PDF is connected through `react-forge/pdf`, with independent flow pages, text, lists, tables, links, images, shapes and explicit breaks. `registerFont(bytesOrPath)` registers bounded font assets; system discovery remains the default, while `createSession(format, { systemFonts: false })` selects caller fonts only. All registered assets settle before the export revision is pinned. The current package suite has 21 passing library/reconciler/workspace-CLI checks; installed CLI, extended concurrency/resource coverage and final acceptance evidence remain outstanding.

Operation diagnostics now include `source` (`javascript`/`native`); native records additionally identify the generate/inspect/update operation and started/completed/failed status. Native events are buffered per worker operation and delivered on the JavaScript thread after worker completion. A bounded whitelist excludes dependency events, arbitrary fields, paths and content, and callback exceptions cannot change outcomes. The reconciler suite also covers deferred Suspense updates, awaited action-state transitions and rejected-promise boundary recovery.

Geometry distinguishes native PDF/PPTX page boxes from Office authoring measurements. DOCX reports `word_flow` boxes in points before Word pagination; mounted DOCX blocks report `mounted_region` coordinates. XLSX reports `worksheet` coordinates in points using the conventional 7-pixel maximum digit-width conversion, declared dimensions and merges; these are not print-page or font-substitution measurements. Office applications remain authoritative for final layout. Non-layout-bearing handles have no geometry and return `invalid_target`. Newly authored trees are inspectable; their structural targets use `editable: false` because edits go through root React updates, while `mount` is the imported-region interface. `capabilities` exposes the supported runtime, format import boundaries, coordinate spaces, font policy and resource limits without I/O.
