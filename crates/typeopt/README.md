# typeopt

Experimental TypeScript property optimization before bundling. The CLI analyzes a single application project, shortens safe property names, separates unrelated same-named properties and reuses names between noninterfering objects. It emits ESM for an existing bundler. It never runs your application or bundler.

## Install

Use the repository's Rust toolchain and build from source:

```sh
cargo install --locked --path crates/typeopt
```

The supported native validation targets are macOS arm64 and Linux x64. Source builds download dependencies during installation. Subsequent analysis is local, with no network access, telemetry, persistent cache or user-code execution.

## Use

```sh
typeopt --project ./tsconfig.json --entry ./src/main.tsx --out-dir ./optimized-new
typeopt --project ./tsconfig.json --entry ./src/main.tsx ./src/worker.ts --check
```

Paths are relative to the current directory. The output parent must exist and the destination must not exist, including an empty directory or symlink. Use a new output path for each run. Inputs are never rewritten. Ctrl+C cancels the operation and removes operation-owned staging files. Exit codes are 0 for success, 1 for analysis/output failure, 2 for invalid CLI arguments and 130 for cancellation handled by the CLI.

`--check` prints a JSON analysis report without emitting files. `--verbose` adds compiler diagnostic messages with source-derived arguments replaced by `<redacted>`; `--help` and `--version` describe the CLI. Logs go to stderr and do not include source bodies or environment values. Set `NO_COLOR=1` to disable ANSI colors.

## Output and v0 compatibility

- Numbered `.js` files and adjacent `.js.map` files contain ESM and maps back to original source text.
- `modules.json` contains `schema_version: 0` and a `modules` array. Each entry has `source`, `source_sha256` (SHA-256 of the analyzed source text), `output`, `source_map` and `kind` (`optimized`, `preserved` or `passthrough`). The Vite example fails when a source no longer matches its digest; rerun typeopt into a new output directory after source changes. Source paths are absolute; optimized output paths are relative to the output directory. JavaScript passthrough entries refer to original files and have no new map.
- `properties.json` contains `schema_version: 0` and `properties`. Each record identifies a property group, original source/name, final name, occurrence count and optional preservation reason.
- `report.json` contains compiler/tool versions, file/object/property counts, changed occurrences, preservation counts and elapsed-time/peak-RSS measurements. `renamed_properties: 0` is a valid conservative result, not evidence of application size reduction.

Code and mappings are deterministic for identical source paths, contents, configuration and tool version. Measurements vary and must be excluded from deterministic comparisons. The interfaces are experimental. Incompatible changes require an explicit version note; adapters must reject unknown schema versions.

## Safety and supported input

Supply explicit application entries and one tsconfig. Workspace imports and declaration files use the compiler's resolver. Project-reference orchestration, CommonJS output, compiler plugins/content mappers, `eval` (including aliases), `with`, `noCheck`, `noResolve`, `outFile`, declaration-only output and preserved JSX are rejected. Set an ESM module mode and `jsx: "react-jsx"` or `"react"` for TSX. Syntax and type errors fail before publication. Unsupported upstream settings produce compiler diagnostics; they are not silently reinterpreted.

The CLI owns emission controls: it overrides `noEmit`, declaration/incremental/composite output, output locations and source-map output settings. It does not change the input tsconfig. JavaScript and node_modules are not transformed; imported JSON and other assets stay in the original project.

The proof supports fresh object literals with ordinary data properties and module-local direct calls. A fixed-point graph propagates allocation sets through const/let bindings, assignments, parameters, returns, recursive calls and captured bindings. Allocations that meet at the same binding or call boundary share a property group; preserved names participate in collision checks. Inline flat parameter and return object types use the same property mapping. The analysis is context-insensitive: one unknown argument or observable escape preserves the whole joined region across all calls.

Only functions whose references are all direct calls with fixed arity receive a trusted summary. Exported or escaping functions, imported/indirect calls, async/generator functions, generic signatures, rest/default parameters and shared named type declarations remain boundaries. Cross-module function summaries are not implemented. Objects with annotations, public exports, containment escape, spread, dynamic access, reflection, property deletion, method calls, accessors or unsupported shape operations keep their names. Structural relationships, unions and inheritance outside this proof are preserved. Flat destructuring preserves binding names and defaults; rest, computed keys and nested patterns remain unsupported. Anonymous function/class values are preserved because renaming can change an inferred `.name`. Special protocol keys are preserved. Property shorthand retains the original variable binding.

This conservative boundary is intentional: types alone do not prove rename safety. There is no aggressive mode or trusted-declaration opt-out. Always run application behavior tests after bundling. This is an application optimizer, not a library/declaration compatibility tool.

## Vite example

Copy `examples/vite/adapter.mjs` into your build tooling, then add it to the existing Vite configuration:

```js
import { typeopt } from './adapter.mjs';
export default {
  plugins: [typeopt('./optimized-new')],
};
```

Keep existing React/asset plugins and aliases. The example substitutes only modules with actual property edits. It leaves preserved TS modules to Vite so unrelated modules retain the baseline transpilation. It loads optimized code under original module identities so relative imports, workspace aliases and assets retain their original context. Queries such as `?raw` are handled by the original loaders. Maps compose through Vite. Run Vite and application tests yourself; typeopt never launches them. Remove the adapter to roll back adoption.

This adapter is an example, not a published plugin. Its test environment pins Vite 5.0.12 to match the benchmark. That old benchmark pin is not a recommendation or a security remediation; audit and update your application's dependencies independently.

## Validation and benchmark reproduction

```sh
cargo test -p typeopt
cargo test
pnpm --dir crates/typeopt/examples/vite install --ignore-workspace --frozen-lockfile
pnpm --dir crates/typeopt/examples/vite test
```

`benchmark/pin.json` pins Excalidraw and the build/measurement runtime. Clone that revision into a separate directory, use its frozen Yarn lockfile and retain its original assets. Record lockfile SHA-256, Chromium executable/version and any explicit compatibility patch before testing. Apply each compatibility patch to both baseline and optimized builds. Never suppress type errors to obtain a benchmark.

Apply `benchmark/patches/remove-redundant-base-url.patch` with `git apply` in the pinned checkout. It removes the redundant root `baseUrl` setting, which the pinned compiler rejects; existing relative paths remain unchanged. Install the application with `corepack yarn install --frozen-lockfile --ignore-scripts`.

```sh
node crates/typeopt/benchmark/build.mjs /absolute/excalidraw /absolute/baseline-build
typeopt --project /absolute/excalidraw/tsconfig.json --entry /absolute/excalidraw/excalidraw-app/index.tsx --out-dir /absolute/optimized-new
node crates/typeopt/benchmark/build.mjs /absolute/excalidraw /absolute/optimized-build /absolute/optimized-new
```

The build helper fails unless the checkout `HEAD` and `yarn.lock` SHA-256 match `pin.json` and the worktree, including untracked non-ignored files, equals the pinned revision plus exactly the compatibility patch. Keep build and optimizer outputs outside the checkout. It applies the pinned environment to both builds and records the verified revision, source tree, lockfile hash, elapsed time and peak RSS in `typeopt-build.json`. Build the pinned application once with its original inputs, then run typeopt with the application entry and build again with the example adapter. Use the same environment, Vite settings, minifier and source-map settings. Keep outputs in separate directories. Compare:

```sh
node crates/typeopt/benchmark/measure.mjs /absolute/baseline-build /absolute/optimized-build
```

The command sums every production `.js`, `.mjs` and `.cjs` file, excluding source maps. It uses gzip level 9 and Brotli quality 11. It fails unless both builds emit the same logical asset set (file paths without the Vite content hash) and all three totals decrease; the output lists missing and extra assets. Record the result with source revision, exact commands, tool versions, execution time and peak RSS. Fixture results do not substitute for application acceptance.

Install browser tooling with `pnpm --dir crates/typeopt/benchmark install --ignore-workspace --frozen-lockfile` and `pnpm --dir crates/typeopt/benchmark exec playwright install chromium`. Serve each build on a separate loopback HTTP port, then run:

```sh
node crates/typeopt/benchmark/browser.mjs http://127.0.0.1:48180 /absolute/baseline-browser
node crates/typeopt/benchmark/browser.mjs http://127.0.0.1:48182 /absolute/optimized-browser
```

Compare the two `browser.json` observations and export summaries. The harness normalizes sketch seeds in the imported scene so PNG pixels and SVG geometry can be compared despite time-seeded drawing roughness. It uses the application's download/file-input fallback; it does not validate native OS file pickers. It retains actual scene, PNG and SVG downloads for inspection. Run the same Chromium checks on both builds: draw shapes/freehand, add/edit text, move/resize, undo/redo, save/reload/import a scene, and export PNG/SVG. Compare scene data and exported results. Native platform acceptance requires actual macOS arm64 and Linux x64 execution; an emulated container is not native evidence.

## Licensing and support

Repository-owned code is Apache-2.0; see `LICENSE` and `NOTICE`. The pinned ts-rust compiler retains its MIT/Apache-2.0 and third-party terms in `licenses/`. Its original notice links describe paths in the upstream repository. Report problems through this repository's GitHub issues. Include tool version and redacted reproduction steps; there is no separate support SLA.
