# bundlewhy Rust foundation

## Scope

`crates/bundlewhy` owns the `bundlewhy` binary: CLI, project and engine detection, adapter process lifecycle, analysis rules, size measurement, result bundle storage, the loopback Connect server and self-contained HTML export. It embeds the collector adapters from `packages/bundlewhy/adapters` and the built UI document from `apps/bundlewhy-web`.

## Runtime and Language

Rust (workspace edition) with tokio, hyper 1 and serde. The analyzed project's build runs in Node.js 22.18 or newer through the adapters (the first 22.x release that strips TypeScript types by default, which TypeScript Rspack configurations require). Rust is a documented deviation from the repository Go default; see [the project index](project-bundlewhy.md).

## Users and Operators

Application developers analyzing a trusted local Vite or Rspack project, and maintainers validating fixtures and real applications. Support is through GitHub issues without an SLA.

## Interfaces and Contracts

### Commands

- `bundlewhy analyze --out-dir <dir> [--project <dir>] [--config <file>] [--config-name <name>] [--bundler vite|rspack] [--allow-unsupported-engine] [--no-open] [--no-serve] [--exclude-sources]` collects one diagnostic build into a new run directory, serves the UI and opens the browser unless `--no-open`. `--no-serve` exits after saving. `--out-dir` is required: results are stored only where the user chooses.
- `bundlewhy open <run-dir> [--no-open]` validates and serves a saved result bundle.
- `bundlewhy export <run-dir> --output <file.html> [--exclude-sources]` writes a self-contained interactive HTML report. It states on stderr whether source content is included.
- `bundlewhy engines` prints the frozen engine compatibility matrix.
- Global flags: `--no-color` (also `NO_COLOR`), `--verbose`, `--help`, `--version`. Exit codes: `0` success, `1` failure, `2` usage error, `130` cancelled.

### Selection

The project defaults to the current directory. The bundler is detected from `package.json` dependencies (`vite`, `@rspack/core`) and configuration files (`vite.config.{js,mjs,cjs,ts,mts,cts}`, `rspack.config.{js,mjs,cjs,ts,mts,cts}`). Auto-selection happens only when exactly one bundler and one configuration file are candidates. Otherwise analysis fails with `BW_BUNDLER_AMBIGUOUS` or `BW_CONFIG_AMBIGUOUS`, lists the candidates and explains the selecting flag. Rspack configurations exporting several builds require `--config-name`. Library, SSR and Node-target builds fail with `BW_UNSUPPORTED_BUILD`.

### Engine compatibility matrix (frozen for 0.1.0)

| Bundler | Package        | Version                     | Default JavaScript minifier          |
| ------- | -------------- | --------------------------- | ------------------------------------ |
| Vite    | `vite`         | `8.3.4` (Rolldown `1.2.13`) | Oxc minifier (`build.minify: 'oxc'`) |
| Rspack  | `@rspack/core` | `2.2.9`                     | `SwcJsMinimizerRspackPlugin`         |

An installed version outside the matrix fails with `BW_ENGINE_VERSION_UNSUPPORTED` before the build. `--allow-unsupported-engine` is the explicit selection that analyzes with that installed engine; the run records `ENGINE_SUPPORT_UNSUPPORTED_SELECTED`. A non-default minifier keeps module-graph categories but marks minifier categories `UNAVAILABLE` with `BW_MINIFIER_NON_DEFAULT`; disabled minification produces a confirmed configuration diagnostic and `BW_MINIFICATION_DISABLED`.

### Diagnostic engine

The diagnostic build always runs the project's own installed engine with collection overrides recorded in `EngineIdentity.overrides`: Vite forces a bundlewhy output directory, `emptyOutDir`, source maps and a post collector plugin; Rspack forces production mode, a bundlewhy output path, `output.clean` and `devtool: source-map`. Findings describe that diagnostic build, `diagnostic_engine` is always true and the notice states that output equivalence with the original build is not guaranteed. Adapter identity is `bundlewhy-collector-vite` / `bundlewhy-collector-rspack` with an adapter version.

### Rules and coverage

Each rule has a stable identifier, a category, conditions with outcomes, evidence provenance and a corrective direction with safety conditions. Every rule has a fixture blocker and a counterexample.

| Rule                                   | Engine | Category              | Status                                                                                       | Evidence                                                                                     |
| -------------------------------------- | ------ | --------------------- | -------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `rspack/side-effect-retained`          | Rspack | Tree shaking          | Confirmed                                                                                    | No used exports and an engine-reported `Statement with side_effects` bailout with location   |
| `vite/side-effect-retained`            | Vite   | Tree shaking          | Confirmed                                                                                    | Module rendered with no rendered exports while its exports exist                             |
| `rspack/module-concatenation-bailout`  | Rspack | Module concatenation  | Confirmed                                                                                    | `ModuleConcatenation bailout:` reason for a module that is in a chunk and not concatenated   |
| `vite/commonjs-wrapped`                | Vite   | Module concatenation  | Confirmed                                                                                    | Rolldown input format `cjs` and the `__commonJSMin` runtime helper is rendered               |
| `rspack/module-in-multiple-chunks`     | Rspack | Chunk splitting       | Confirmed                                                                                    | Module listed in two or more chunks                                                          |
| `vite/ineffective-dynamic-import`      | Vite   | Chunk splitting       | Confirmed                                                                                    | Rolldown `INEFFECTIVE_DYNAMIC_IMPORT` log, or static and dynamic importers                   |
| `bundle/duplicate-package`             | Both   | Duplicate modules     | Confirmed                                                                                    | Same package name included from two or more package directories                              |
| `minifier/unresolved-env-condition`    | Both   | Dead-code elimination | Inferred                                                                                     | Generated code still reads `process.env.<NAME>` (Rspack) or Vite's `({}).<NAME>` replacement |
| `minifier/minification-disabled`       | Both   | Dead-code elimination | Confirmed                                                                                    | Configuration disables minification                                                          |
| `minifier/direct-eval-blocks-mangling` | Both   | Name mangling         | Confirmed with an engine-reported `EVAL` log or eval bailout, Inferred from source otherwise | Direct `eval` in a module whose long local names survive in the generated chunk              |

Inlining and constant folding are `COVERAGE_STATE_UNAVAILABLE` for both engines: neither engine exposes those decisions and no verified detector exists. Module concatenation is covered for both engines. Missing source maps mark the affected chunk's attribution `UNAVAILABLE`, fall back to generated locations and record `BW_SOURCE_MAP_MISSING`. A partial collection keeps usable results, sets `RUN_STATUS_PARTIAL` and marks affected categories `COVERAGE_STATE_PARTIAL`; an emitted chunk that cannot be read marks dead-code elimination and name mangling partial. The env-condition scan ignores string, template, comment and regular expression literal text. Retained code alone is never reported as a defect.

### Dependency paths and locations

Dependency paths are the shortest static importer chains from an entry to the module (up to three). Dynamic `import()` edges are not part of a dependency path. Locations use original source positions when a source map maps them; otherwise generated chunk positions. Snippets carry at most nine lines of 240 characters each.

### Sizes

Each emitted JavaScript chunk records raw, gzip (level 9) and Brotli (quality 11) bytes. When the chunk has a source map, generated bytes are attributed to sources by splitting the generated text at mapping boundaries; each byte belongs to at most one segment, so attribution never double counts and never exceeds raw bytes. Unmapped bytes are reported separately. Sizes are measurements, never savings.

### Lifecycle

Every run gets a new UUID v7 directory. Progress stages are `DETECTING`, `BUILDING`, `COLLECTING`, `ANALYZING`, `MEASURING`, `SAVING` and `FINISHED`. Ctrl+C or `CancelRun` terminates the owned adapter process (its process group on Unix), writes a `RUN_STATUS_CANCELLED` bundle with `BW_RUN_CANCELLED` and keeps the `INCOMPLETE` marker. A bundle left `RUNNING` by an interrupted process reopens as `RUN_STATUS_FAILED` with `BW_RUN_INTERRUPTED`. There is no automatic retry or resume; a retry is a new run. Project `package.json`, the selected configuration and lockfiles are hashed before and after the build; a change is reported as `BW_PROJECT_MODIFIED`, downgrades a complete run to `RUN_STATUS_PARTIAL`, keeps the `INCOMPLETE` marker and makes `analyze` exit with a failure code.

### Error codes

`BW_USAGE`, `BW_PROJECT_NOT_FOUND`, `BW_BUNDLER_NOT_DETECTED`, `BW_BUNDLER_AMBIGUOUS`, `BW_CONFIG_NOT_FOUND`, `BW_CONFIG_AMBIGUOUS`, `BW_CONFIG_LOAD_FAILED`, `BW_BUNDLER_NOT_INSTALLED`, `BW_ENGINE_VERSION_UNSUPPORTED`, `BW_NODE_NOT_FOUND`, `BW_BUILD_FAILED`, `BW_COLLECTION_FAILED`, `BW_UNSUPPORTED_BUILD`, `BW_SOURCE_MAP_MISSING`, `BW_SOURCE_UNREADABLE`, `BW_MINIFIER_NON_DEFAULT`, `BW_MINIFICATION_DISABLED`, `BW_PROJECT_MODIFIED`, `BW_RUN_CANCELLED`, `BW_RUN_INTERRUPTED`, `BW_SCHEMA_UNSUPPORTED`, `BW_RESULT_INVALID`, `BW_OUTPUT_DIR_INVALID`, `BW_SERVER_BIND_FAILED`, `BW_BROWSER_OPEN_FAILED`, `BW_EXPORT_FAILED`, `BW_ACCESS_DENIED`, `BW_SOURCE_STALE`, `BW_SOURCE_NOT_FOUND`, `BW_ACCESS_TOKEN_MISSING` (UI only). Each user-facing error prints the code, message and a recovery instruction.

## Storage

`<out-dir>/<run-id>/` contains `analysis.json` (versioned result bundle, `schemaVersion: 1`), `collect.json` (adapter collection), `build/` (diagnostic build output), `adapter/` (adapter scripts written for this run), `bundlewhy.log` (JSON lines) and `INCOMPLETE` while a run is unfinished. The result bundle contains the analysis (diagnostics, coverage, sizes, engine identity, safe configuration summary, problems) and source files with project-relative or dependency display paths, SHA-256 and content unless `--exclude-sources`. Results are kept until the user deletes them; bundlewhy never uploads or deletes them. Unknown schema versions fail with `BW_SCHEMA_UNSUPPORTED`; there are no migrations. This local storage is a documented deviation from the repository R2 default.

## Security

Project configuration, plugins and adapters execute with the user's permissions; bundlewhy supports trusted local projects and does not sandbox plugin filesystem or network effects. The server binds `127.0.0.1` on an OS-assigned port, requires a per-run 256-bit bearer token (delivered in the URL fragment), rejects requests whose `Host` is not the bound address and whose `Origin` is not exactly the server origin, and sends `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and a CSP without network sources. RPC methods take no filesystem paths: sources are looked up by bundle-local identifiers and runs by UUID within the output directory. Saved bundles are untrusted: structure, enum values, identifiers and display paths are validated, and stale checks read only project-relative paths without `..` that resolve (after symlinks) to regular files of at most 16 MiB inside the recorded project root. Source files are identified by their resolved path, so distinct installed copies that share a display path stay distinct. `--exclude-sources` also removes diagnostic snippets from result bundles and exports. Exported HTML embeds JSON with `<`, `>`, `&`, U+2028 and U+2029 escaped and a CSP that forbids network access. Reports that include source content are not claimed to be secret-free.

## Logging

`tracing` writes human-readable, ANSI-colored stderr logs by default (`--no-color` or `NO_COLOR` disables color, `--verbose` adds debug detail). Each run writes `bundlewhy.log` JSON lines with timestamp, run ID, stage, duration, engine versions and error codes. Logs never include source content, environment variable values, credentials or access tokens.

## Build and Test

Run root `cargo test` after Rust changes. Unit tests cover every rule with committed fixture collections for its blocker and counterexample, size attribution without double counting, schema rejection, result validation, path validation, token and Origin enforcement, HTML escaping and log redaction. `packages/bundlewhy` acceptance (`pnpm --filter @delino/bundlewhy test:acceptance`) runs real Vite and Rspack fixture builds through the binary and verifies project files stay unchanged. The UI document is embedded from `apps/bundlewhy-web/dist/index.html` when present at build time; otherwise a minimal page explains how to build it. Release builds must build the UI first.

## Dependencies and Integrations

Consumes the [Connect protocol](protos-bundlewhy-v1-contract.md) through hand-written serde types that follow the proto3 JSON mapping, the [adapters](packages-bundlewhy-distribution-contract.md) and the [web UI](apps-bundlewhy-web-contract.md). Uses `sourcemap`, `flate2`, `brotli`, `sha2`, `uuid` (v7), `hyper`, `tokio` and `clap`.

## Change Triggers

Update this contract, the project index and the public docs when commands, flags, error codes, rules, the engine matrix or storage change. Update the protocol contract with Rust JSON types.

## References

- [Project index](project-bundlewhy.md)
- [Repository defaults](repository-defaults.md)
