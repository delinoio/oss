# TypeNative Project Index

## Goal

Build an experimental native application framework for TypeScript developers using a custom TSX runtime with HTML elements and CSS, ScriptC native compilation, and Blitz rendering. Development and distribution applications must not embed a JavaScript engine. Completion requires functional and platform acceptance, with no fixed deadline or numerical performance thresholds.

## Project ID

`typenative`

## Domain Ownership Map

- `packages/typenative-cli/`: TypeScript developer CLI, distributed as `@delino/typenative-cli` with the `tn` executable.
- `packages/typenative-cli/examples/native-todo/`: standalone ScriptC/custom-TSX todo example and macOS Blitz window adapter, built outside the workspace; not the production SDK or host.
- `packages/typenative/` (planned): TypeScript SDK, custom TSX renderer and typed plugin bindings, distributed separately as `@delino/typenative`.
- `crates/typenative-host/` (planned): Rust Blitz host, native FFI, application services and platform integration.

## Domain Contract Documents

- [SDK and developer workflow](packages-typenative-foundation.md)
- [Native host](crates-typenative-foundation.md)

## Cross-Domain Invariants

The TypeScript SDK/renderer/CLI and Rust host are explicit exceptions to the default Go language. Application storage is local to the application rather than Cloudflare R2 because offline native persistence is part of the API contract. Node.js is permitted only for tooling. ScriptC compiles application code in both modes. The UI runtime is repository-owned and uses a custom TSX factory, signals, effects and text bindings. External UI framework compatibility is excluded.

The target identifiers are `macos-arm64`, `windows-x64`, `linux-x64`, `ios-arm64-simulator` and `android-arm64-emulator`. Supported combinations require recorded execution acceptance. Dependency metadata, compilation and package creation alone do not establish support. Preserve process identity and compatible application state during native component updates. Upstream gaps require implementation, never a JavaScript-engine fallback or an undocumented reduction in the custom TSX contract.

Installation-ready packages and host launches share the immutable UUID v7 `applicationId` defined by the SDK contract. Package identity and application storage must preserve it across updates and rollbacks.

Use compatible, exact framework/compiler/renderer/plugin versions. Rollback requires explicit selection and preserves application data. Repository-owned code is Apache-2.0; upstream patches retain licenses and document cause, scope and removal conditions. No public documentation website is required.

## Change Policy

Update this index and both owning contracts when path ownership or cross-domain interfaces change. Follow the [instruction-update policy](README.md#instruction-update-policy). Record implementation status, commands, source revisions, validation results and unresolved limits in PRs, issues or CI artifacts. Keep native execution, fixture, packaging and virtual-device evidence distinct.

## References

- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [License contract](repository-license-contract.md)
- [ScriptC](https://scriptc.dev/)
- [Blitz](https://github.com/dioxuslabs/blitz)
