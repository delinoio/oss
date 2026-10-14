# bundlewhy adapters and distribution contract

## Scope

`packages/bundlewhy` tests the engine collector adapters, which live in `crates/bundlewhy/adapters` (`common.mjs`, `vite.mjs`, `rspack.mjs`) and are embedded by the binary. It owns the npm/npx launcher (`bin/bundlewhy.cjs`), acceptance fixtures (`fixtures/vite-app`, `fixtures/rspack-app`) and acceptance scripts.

## Runtime and Language

Adapters are dependency-free ESM for Node.js 22.18 or newer. They run in the analyzed project's directory and load the project's own `vite` or `@rspack/core`. The launcher is CommonJS using Node.js built-ins.

## Users and Operators

Developers running `npx @delino/bundlewhy analyze ...` and maintainers running acceptance.

## Interfaces and Contracts

- Adapter protocol: `node <adapter>.mjs <request.json>`. The request carries the project root, selected configuration, configuration name, build directory, collection path and the frozen supported version. Adapters write protocol frames to stdout, one per line: `BUNDLEWHY-FRAME <protocolToken> <json>`, where the per-run token comes from the request and the JSON is a `progress`, `engine`, `error` (with a `BW_*` code and recovery) or `done` event. Lines without the exact prefix and token are project output and are ignored, so JSON printed by configuration or plugins cannot impersonate adapter events. Adapters also write and one collection file (`collectVersion: 1`) with engine identity, overrides, a safe configuration summary, normalized modules (identifiers, paths, importers, exports, used exports, input format, rendered size, chunk files, bailouts, reasons, nearest package identity, hook-level and configured `moduleSideEffects` values for Vite), chunks, assets, warnings and structured engine logs.
- Adapter protocol: `node <adapter>.mjs <request.json>`. The request carries the project root, selected configuration, configuration name, build directory, collection path and the frozen supported version. Adapters write protocol frames to stdout, one per line: `BUNDLEWHY-FRAME <protocolToken> <json>`, where the per-run token comes from the request and the JSON is a `progress`, `engine`, `error` (with a `BW_*` code and recovery) or `done` event. Lines without the exact prefix and token are project output and are ignored, so JSON printed by configuration or plugins cannot impersonate adapter events. Adapters also write one collection file (`collectVersion: 1`) with engine identity, overrides, a safe configuration summary, normalized modules (identifiers, paths, importers, exports, used exports, input format, rendered size, chunk files, bailouts, reasons, nearest package identity, hook-level and configured `moduleSideEffects` values for Vite), chunks, assets, warnings and structured engine logs.
- Adapters never write project source, configuration or lockfiles; build output goes to the bundlewhy run directory. Vite keeps a configured relative or absolute root for the diagnostic build and collection context; when no root is configured, it uses the selected project root. Configuration summaries contain option names and summarized values, never environment values.
- The Rust binary embeds the adapter files and writes them into each run directory, so the installed adapter version always matches the binary.
- Distribution: the public `@delino/bundlewhy` launcher resolves a platform package for macOS arm64 (`darwin-arm64`), Linux x64 with glibc (`linux-x64-gnu`) and Windows x64 (`win32-x64-msvc`) and executes its binary without a shell, forwarding arguments, stdio, exit codes and signals. Unsupported platforms and missing platform packages report a structured error with reinstall guidance; there is no download or compile fallback. Release artifacts publish exact Node.js and platform requirements and the frozen engine matrix. Rollback installs a previous version.
- The workspace package is private until a release workflow is approved.

## Storage

None beyond the run directory chosen by the user.

## Security

Adapters execute project configuration and plugins with the user's permissions. They do not read environment values into collections or logs.

## Logging

Adapters report progress and errors as JSON lines; Rust records them in the run log without source content.

## Build and Test

`pnpm --filter @delino/bundlewhy test` runs launcher and adapter tests. `pnpm --filter @delino/bundlewhy test:acceptance` builds the Rust binary, runs both fixtures end to end and verifies that fixture files are unchanged. Acceptance removes its generated web `dist` output after success or failure while preserving the original failure diagnostic. Fixtures pin `vite` `8.3.4` and `@rspack/core` `2.2.9` through this package's development dependencies.

## Dependencies and Integrations

- [Rust foundation](crates-bundlewhy-foundation.md)

## Change Triggers

Adapter protocol or collection changes update the Rust collection types and this contract together. Engine version changes update the matrix in the Rust contract and public docs.

## References

- [Project index](project-bundlewhy.md)
- [Repository defaults](repository-defaults.md)
