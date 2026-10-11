# typeopt Rust foundation

## Scope

`crates/typeopt` owns the CLI, private compiler integration, property analysis, fixtures, Vite example adapter and Excalidraw benchmark harness. The CLI/report interfaces are experimental v0; incompatible changes require explicit documentation.

## Runtime and Language

Rust, with Node.js 24 for fixture execution and integration tooling. Pin `ts_goport` to upstream revision `b4144b4769d13bbfe9e648840fe6297893981427`; manually validate upstream changes. Keep optimizer code separate from upstream compiler code. Initial distribution is Cargo source builds for macOS arm64 and Linux x64.

## Users and Operators

Application maintainers invoke the optimizer, then separately run their bundler and behavior tests. GitHub issues provide support, without a support SLA.

## Interfaces and Contracts

`typeopt --project <tsconfig> --entry <file>... --out-dir <new-dir>` analyzes and emits ESM. `--check` analyzes without publishing output and does not require an output directory. Support `--help`, `--version` and `--verbose`.

Use type information and actual use/escape analysis to shorten eligible names, distinguish unrelated same-named properties, and reuse names only for noninterfering groups. Preserve evaluation order, accessors, inheritance, lookup and side effects. Propagate object escape and property identity through aliases and containment. Preserve affected names at public entry exports, external calls, JavaScript boundaries, serialization, reflection, dynamic keys and uncertain relationships. Never trust unchecked declarations as a safety assertion. No aggressive mode applies.

For module-local direct calls, build a flow graph for object allocations, variable bindings, parameters, call results and returns. Compute reachable allocation sets to a fixed point, including recursive calls, assignments and captured bindings. Unify regions that meet at a shared binding or call boundary before allocating property names. Unknown values taint the joined region; an escape in any joined region preserves the entire group. Inline flat parameter and return object types participate in the same property edits. Exported or escaping functions, indirect/imported calls, async/generator functions, generic signatures, rest/default parameters and shared named type declarations remain preservation boundaries. Do not infer a closed call graph from types alone.

Resolve reachable workspace sources using the project's configuration. Declaration files provide external types. JavaScript passes through unchanged; node_modules is read-only external code. Reject syntax/type errors, eval, with, CommonJS module syntax (`import = require()` and `export =`, which `module: Preserve` would emit as CommonJS), unsupported configuration and project-reference orchestration with actionable diagnostics. Read configuration as data; do not execute it. Compatibility adjustments must be explicit and semantics-preserving.

Emit JS and source maps plus versioned `modules.json`, `properties.json` and `report.json`. Module mappings link original paths to output JS/maps and record the SHA-256 of each analyzed source; consumers must reject output whose source no longer matches; property mappings include stable group identities and original/final names. Reports explain transformations and preservation reasons and include analysis statistics. Identical inputs/configuration/tool versions produce identical code and mappings. Exclude time and peak-memory measurements from deterministic comparisons. Zero safe changes is a valid explained result.

The Vite example substitutes mapped JS for modules with actual property edits under original module identities; preserved TS modules retain the original Vite transpilation. Compose source maps through the original module identities. Keep assets in the original project and preserve resolution context. It is not a separately published plugin. Roll back by restoring original build inputs/configuration.

## Storage

One-shot, in-memory analysis; no daemon, watch mode or persistent cache. Never modify input files or an existing output destination. Stage output in an operation-owned temporary directory and publish without replacing an existing destination. Error and cancellation paths return failure, remove owned temporary artifacts and never publish a complete result.

## Security

After installation, analysis performs no network access, user-code execution or telemetry. Do not log source contents, credentials or environment values. External APIs and observable object shapes form preservation boundaries.

## Logging

Use structured Rust tracing and English stderr diagnostics. Verbose mode adds diagnostic context without source text or environment values. Measure elapsed time and peak resident memory; no performance threshold or resource ceiling applies.

## Build and Test

Run root `cargo test` after Rust changes and applicable frontend `pnpm test` after frontend changes. Compare original and optimized fixtures in Node, including side effects. Cover each optimization, combinations, structural types, unions, aliases, computed keys, accessors, inheritance, reflection, serialization, external escape, collisions, determinism, maps, malformed input, filesystem failures and Ctrl+C. Verify no input execution or network access in a restricted environment.

Validate Vite with workspace aliases, TSX, unchanged JS and original assets. Pin the Excalidraw commit, lockfile, runtime, browser, production settings and compression tools before benchmarking; apply documented compatibility adjustments equally to both builds. Exclude source maps and sum the same production JS asset set. Raw, gzip and Brotli totals must each strictly decrease; no minimum percentage applies.

Chromium acceptance compares drawing shapes/freehand, text/move/resize edits, undo/redo, save/reload/file import and PNG/SVG export before and after optimization. Actual application acceptance cannot be replaced by fixtures. Require native build/execution on macOS arm64 and Linux x64. Retain source revision, commands, results and unperformed checks in CI artifacts or PR/issue records. Preserve existing affected CI and macOS runner restrictions.

## Dependencies and Integrations

Use immutable ts-rust input and preserve all upstream licenses/notices. Repository-owned code is Apache-2.0. Vite and Excalidraw are validation integrations; the CLI never launches them. Public Rust/Node APIs, library declaration transformation, CommonJS output, JavaScript optimization, prebuilt distributions and other platforms are out of scope.

## Change Triggers

Synchronize CLI/report or safety changes with the README and tests. Update the project index for ownership changes and relevant AGENTS files for development-rule changes only.

## References

- [Project index](project-typeopt.md)
- [Repository defaults](repository-defaults.md)
- [License contract](repository-license-contract.md)
- [Workflow contract](repository-workflow-contract.md)
