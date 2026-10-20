# TypeNative Native Host

## Scope

Own the planned `crates/typenative-host/` Rust crate, Blitz integration, native FFI, application services and desktop/mobile platform integration. Do not register the crate in the Cargo workspace until its skeleton exists.

The SDK-owned `packages/typenative-cli/test/experiments/custom-tsx/` comparison may include a standalone Rust fixture copied outside the workspace for compilation. It checks ScriptC calls into one retained Blitz document and offscreen rendering only; it does not create or register the production host. Preserve the reviewed Blitz revision and exclude scripting engines from the fixture's dependency graph.

The SDK-owned `packages/typenative-cli/examples/native-todo/` example may include a
standalone Rust static library for a macOS arm64 Blitz window, DOM event forwarding,
input reads and targeted DOM updates. Copy it outside the workspace before compilation.
It is an executable example adapter, not the production host. Keep application state
and event handlers in ScriptC-compiled TypeScript. Preserve the pinned Blitz revision,
handle drawing/lifecycle synchronously and distinguish OS-input acceptance from
synthetic event tests. The SDK contract defines the example's supported behavior.

## Runtime and Language

Use Rust and required platform integration code. The host executes ScriptC-compiled code with no JavaScript engine. Use Blitz for HTML/CSS layout and rendering, without its Boa scripting example. Link runtime objects from the exact compiler installation that produced application code.

## Users and Operators

TypeNative applications, native-plugin authors and framework maintainers.

## Interfaces and Contracts

The [SDK contract](packages-typenative-foundation.md#interfaces-and-contracts) owns the complete required behavior. The native ABI must connect custom TSX rendering mutations, events, scheduling and application lifecycle to Blitz. Support one desktop window or one mobile screen, resize/close, foreground/background, rotation, safe areas and virtual keyboards. Validate keyboard navigation, focus, accessibility semantics, screen readers and Korean IME.

Compatible native code updates must preserve process identity and compatible application state. Component identity or state-structure changes or host/plugin changes may restart with a stated reason. Compilation failure retains the last successful running application and state; with no successful build, launch nothing. Snapshot-and-restart is not a compatible update implementation.

Provide fetch/AbortSignal with TLS verification, asynchronous JSON key/value and file APIs, clipboard, HTTP(S) external URL opening, and typed trusted static plugins with async calls, cancellation and removable subscriptions. Release resources on cancellation, unsubscribe and shutdown. Return coded errors without automatic retries. Custom URL schemes and secret storage belong to separate trusted plugins.

## Storage

Validate the manifest, package and launch `applicationId` according to the [SDK application identity contract](packages-typenative-foundation.md#interfaces-and-contracts) before opening storage. Key the storage namespace by that immutable UUID v7, not a display name, source path, version or build ID. Preserve the namespace across updates and rollbacks; reject identity mismatches without touching application data.

Use application-specific local storage, an explicit exception to R2 defaults. Provide get/set/remove/clear and explicit file reads/writes/deletion. Reject path escapes, serialize same-key/path writes within the application process and use atomic key writes. Preserve the last successful write and persistence across restarts. No automatic expiry, encryption, cloud synchronization or multi-process mutation guarantees.

## Security

Honor OS permissions and sandbox boundaries; return explicit denial. Plugins execute with application privileges, with no native crash isolation. Preserve dependency licenses and notices. Inspect development and distribution artifacts and dependency graphs for embedded JavaScript engines.

## Logging

Use structured `tracing` events with versions, build identifier, target, phase, duration, code and source location. Do not log application data, tokens, request/response bodies or user input by default. Do not persist or transmit logs automatically. Record fatal errors before termination and return recoverable errors to applications.

## Build and Test

Run root `cargo test` after Rust changes. Test the full SDK behavior and failure scenarios described in the [SDK acceptance contract](packages-typenative-foundation.md#build-and-test). Require actual execution on macOS arm64, Windows x64, Linux x64, iOS arm64 simulator and Android arm64 emulator. Record unavailable platforms as unverified. Derive OS minima from pinned SDKs and dependencies, and publish only validated combinations. Physical-device acceptance and store/signing automation are excluded.

Measure startup, memory, distribution size, UI update and development update durations without numerical thresholds. Record exact source revisions, commands, results and unperformed checks outside source contracts. Preserve affected CI selection and exclude macOS runners from PRs, main pushes and affected manual runs; use permitted local or owning-release acceptance.

## Dependencies and Integrations

Use Blitz revision `1e5479ae62737d60f7c2f8e8d31524f5cfe2ec01` as the initial reviewed renderer input and ScriptC source revision `d8c2b0f96d8de8fadf4937d93a701dfd3a10f4df`. Upstream availability does not establish TypeNative compatibility. Revision-pinned patches/forks must document cause, scope and removal conditions while preserving upstream licenses.

## Change Triggers

Update the [project index](project-typenative.md) and [SDK contract](packages-typenative-foundation.md) for ABI, ownership, service or platform changes. Update AGENTS files only for development procedures, ownership or development-rule changes.

## References

- [TypeNative project index](project-typenative.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Repository license](repository-license-contract.md)
