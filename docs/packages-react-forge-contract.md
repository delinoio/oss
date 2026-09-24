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

## Dependencies and Integrations
The N-API adapter owns native work; React and JavaScript callbacks remain on the JavaScript thread. Pin the React/reconciler pair. The Rust engines are format-processing libraries independent of N-API.

## Change Triggers
Synchronize project/native contracts, package ownership rules, examples, capabilities and acceptance evidence with API changes. Generated dist is ignored and removed from final worktrees.

## References
- [Project](project-react-forge.md).
- [Requirements](packages-react-forge-requirements.md).
- [Repository defaults](repository-defaults.md).
