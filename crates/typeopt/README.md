# typeopt

Experimental type-aware TypeScript size optimization before bundling. The CLI analyzes a single application project with TypeScript type and symbol information plus runtime evidence from source, control flow and uses. It applies eighteen conservative optimization passes, accepts a transformation only when it makes the emitted ESM strictly smaller, and emits ESM for an existing bundler. It never runs your application or bundler.

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
typeopt --project ./tsconfig.json --entry ./src/main.tsx --out-dir ./optimized-new --disable-pass call-inlining --disable-pass property-ambiguation
```

Paths are relative to the current directory. The output parent must exist and the destination must not exist, including an empty directory or symlink. Use a new output path for each run. Inputs are never rewritten. Ctrl+C cancels the operation and removes operation-owned staging files. Exit codes are 0 for success, 1 for analysis/output failure, 2 for invalid CLI arguments and 130 for cancellation handled by the CLI.

All passes are enabled by default. `--disable-pass <id>` excludes one pass and can repeat; an unknown ID is an invalid CLI argument (exit code 2). A disabled pass never runs, including as cleanup inside another pass's transformation; such a transformation is skipped with reason `requires_disabled_pass`. `--check` performs the same analysis, in-memory transformation and size decisions as output mode and prints the JSON report without emitting files. `--verbose` adds compiler diagnostic messages with source-derived arguments replaced by `<redacted>`; `--help` and `--version` describe the CLI. Logs go to stderr and do not include source bodies or environment values. Set `NO_COLOR=1` to disable ANSI colors.

## Passes

Pass IDs are stable CLI and report identifiers.

| ID | Transformation |
| --- | --- |
| `property-shortening` | Renames properties of fresh object literals to short names when a whole-program flow analysis proves every read and write is known. It follows bindings, internal function arguments and returns across imports, re-exports and module cycles, plus contained objects and destructuring. |
| `property-disambiguation` | Treats same-named properties of objects that never meet in one value flow as independent groups, so one escaping object does not preserve the name everywhere. |
| `property-ambiguation` | Reuses short names across groups that cannot interfere. |
| `finite-key-rewriting` | Rewrites `obj["name"]` and `obj[KEY]` (an unannotated `const` string initialized before every access) to `obj.name`, and renames finite literal key sets with the properties they select. Type-level key sets are not runtime evidence. |
| `unused-method-removal` | Removes methods and accessors of internal classes that no code can reach, using name mentions, external declarations, protocol names and a whole-program type-flow closure for dynamic keys and reflection. |
| `unused-field-removal` | Removes unread class fields, unread properties of closed `const` object literals and dead writes, keeping each removed right-hand side's evaluation. Classes must be confined. |
| `constant-property-inlining` | Replaces reads of literal-valued properties of internal `const` object literals and class `static` fields with the literal, and removes definitions nothing can observe. `readonly` and `as const` are never evidence. |
| `method-devirtualization` | Rewrites `receiver.method(args)` into `fn(receiver, args)` when the method name is declared once, every mention is a direct call on a proven non-nullish instance and no dynamic key, reflection or external code can reach the prototype method. |
| `call-inlining` | Replaces calls of small resolved functions whose body is one returned expression, when names resolve identically at the call site, argument order, count and exceptions are preserved and the result is smaller. |
| `function-specialization` | Emits one implementation per literal value of a closed function's literal-union parameter, folds branches on it, shares identical clones and redirects calls. Clones count toward the size gate. |
| `signature-reduction` | Removes unused parameters and their arguments, propagates a common literal argument and drops results no caller uses, for closed functions. Removed arguments with effects still run once in order. |
| `type-directed-control-flow` | Removes branches, switch cases, logical operands and optional-chain checks whose outcome is proven at runtime by literals, operators, never-reassigned bindings, closed-function arguments and dominating guards. |
| `primitive-operation-simplification` | Removes conversions that cannot change a proven primitive, removes double negation in conditions and folds literal operations exactly, preserving coercion, `NaN`, negative zero and BigInt behavior. |
| `enum-runtime-reduction` | Replaces reads of source-backed enum members with their constant values and turns an enum whose object is no longer observable into `declare enum`, removing the object and reverse mappings. |
| `discriminant-compaction` | Replaces the string tags of a closed discriminated union with small numbers when a whole-program flow check proves every producer and consumer is known and the tag is never serialized or observed as a string. |
| `aggregate-elimination` | Replaces nonescaping local objects and tuples with separate bindings and collapses unexported namespace objects of immutable bindings into direct references. |
| `intrinsic-specialization` | Replaces built-in array, string and object operations on fresh literals with literal syntax (spreads, `Array.from`, `slice`, `concat`, `Object.assign`, `includes`, array-literal destructuring) when the built-ins are unmodified. |
| `residual-dependency-removal` | Removes functions, classes, variables and enums that have no remaining runtime or type reference, with the import and export specifiers that only forward them, so the bundler can drop unused dependencies and chunks. |

Shared rules apply to every pass. TypeScript types only nominate candidates; an annotation never proves a closed call graph, an immutable value, an impossible branch or an intact built-in. Public entry exports, symbols referenced from application JavaScript, external declarations, module namespace objects, unknown calls, unresolved keys, reflection, serialization, prototype mutation, decorators and declaration merging are boundaries. Evaluation order, effects, exceptions (including temporal dead zones), property descriptors, object and function identity and observable names and arity are preserved. When an obligation cannot be proven, the code is preserved and the report records a reason.

## Pipeline

Each round rebuilds the program from the current in-memory text, recomputes every fact, plans candidates for every enabled pass in a fixed order and evaluates each candidate bundle, including its required cleanup, by transpiling the affected modules before and after with equal emitter settings. A bundle is accepted only when the total emitted ESM byte count strictly decreases; cloned code, new bindings and retained effects count, and source maps and reports do not. Rejected candidates never change accepted state. Accepted edits are type-checked; a bundle that introduces type errors is rolled back (reason `recheck_failed`) and the rest are kept. Rounds repeat until no reduction is accepted; property passes run once afterward. Syntax errors in transformed code and compiler failures fail the operation.

When a transformation removes the last runtime use of an import, TypeScript would elide the import and skip or reorder that module's initialization. The pipeline keeps the dependency with a bare `import "specifier";`, unless `residual-dependency-removal` is enabled and the module and its imports are proven to initialize without effects.

## Output and compatibility

- Numbered `.js` files and adjacent `.js.map` files contain ESM and maps back to original source text. Code that is inserted, deleted, inlined or moved maps to the original range it replaced, across every round.
- `modules.json` contains `schema_version: 0`, a `modules` array and an `inputs` array. Each module has `source`, `source_sha256` (SHA-256 of the analyzed source text), `output`, `source_map` and `kind` (`optimized`, `preserved` or `passthrough`). `optimized` means any accepted runtime transformation, with or without renamed properties. `inputs` lists the tsconfig and extended configuration files (`configuration`) and every other program file the analysis read, such as declarations and dependency modules (`dependency`), with their SHA-256. Consumers must reject the output when any module or input changed, because optimized modules can depend on facts from any of them. Source paths are absolute; optimized output paths are relative to the output directory. JavaScript passthrough entries refer to original files and have no new map.
- `properties.json` contains `schema_version: 0` and `properties`. Each record identifies a property group, original source/name, final name, occurrence count and optional preservation reason. It describes property transformations only.
- `report.json` uses `schema_version: 1`. It keeps the v0 statistics (tool and compiler versions, files, object regions, properties, renamed properties, changed occurrences and preservation reasons) and adds `passes.enabled`/`passes.disabled`, `rounds`, `size` (emitted ESM bytes before and after), `pass_summary` (applied bundles, cleanup participation, skipped counts and reason codes per pass), `transformations` (each accepted bundle with its primary pass, cleanup passes, source location and measured byte change) and `skipped` (pass, location and reason). Savings belong to the whole bundle and are never split among its passes. Zero accepted transformations is a valid conservative result, not evidence of application size reduction.

Code, mappings and reports are deterministic for identical source paths, contents, configuration, options and tool version, except the `measurements` (elapsed time and peak RSS). The interfaces are experimental. Report v1 replaces v0; adapters must reject unknown schema versions.

## Safety and supported input

Supply explicit application entries and one tsconfig. Workspace imports and declaration files use the compiler's resolver. Project-reference orchestration, CommonJS output, compiler plugins/content mappers, `eval` (including aliases), `with`, `import x = require()`, `export =`, `noCheck`, `noResolve`, `outFile`, declaration-only output and preserved JSX are rejected. Set an ESM module mode and `jsx: "react-jsx"` or `"react"` for TSX. Syntax and type errors fail before publication. Unsupported upstream settings produce compiler diagnostics; they are not silently reinterpreted.

The CLI owns emission controls: it overrides `noEmit`, declaration/incremental/composite output, output locations and source-map output settings. It does not change the input tsconfig. JavaScript and node_modules are not transformed; imported JSON and other assets stay in the original project.

This conservative boundary is intentional: types alone do not prove transformation safety. There is no aggressive mode or trusted-declaration opt-out. Always run application behavior tests after bundling. This is an application optimizer, not a library/declaration compatibility tool. To roll back one optimization, rerun into a new directory with `--disable-pass <id>`; to roll back adoption, restore the original build inputs and configuration.

## Vite example

Copy `examples/vite/adapter.mjs` into your build tooling, then add it to the existing Vite configuration:

```js
import { typeopt } from './adapter.mjs';
export default {
  plugins: [typeopt('./optimized-new')],
};
```

Keep existing React/asset plugins and aliases. At build start the example validates every module digest and every recorded input (configuration and dependency files) and fails when any changed, because an optimized module can depend on all of them. It substitutes only `optimized` modules, including modules changed without property renaming. It leaves preserved TS modules to Vite so unrelated modules retain the baseline transpilation. It loads optimized code under original module identities so relative imports, workspace aliases and assets retain their original context. Queries such as `?raw` are handled by the original loaders. Maps compose through Vite. Run Vite and application tests yourself; typeopt never launches them. Remove the adapter to roll back adoption.

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
node crates/typeopt/benchmark/build.mjs /absolute/excalidraw /absolute/original-build
typeopt-existing --project /absolute/excalidraw/tsconfig.json --entry /absolute/excalidraw/excalidraw-app/index.tsx --out-dir /absolute/existing-new
node crates/typeopt/benchmark/build.mjs /absolute/excalidraw /absolute/existing-build /absolute/existing-new
typeopt --project /absolute/excalidraw/tsconfig.json --entry /absolute/excalidraw/excalidraw-app/index.tsx --out-dir /absolute/extended-new
node crates/typeopt/benchmark/build.mjs /absolute/excalidraw /absolute/extended-build /absolute/extended-new
```

`typeopt-existing` is the CLI built from `existing_typeopt_revision` in `pin.json`, the property-only optimizer that the extended passes are compared against.

The build helper fails unless the checkout `HEAD` and `yarn.lock` SHA-256 match `pin.json` and the worktree, including untracked non-ignored files, equals the pinned revision plus exactly the compatibility patch. Keep build and optimizer outputs outside the checkout. It also fails when the installed Vite version differs from the pin, when `NODE_ENV` or a variable with the Vite env prefix is set but not pinned, or when an ignored `.env*` file exists. It applies the pinned environment to every build and records the verified revision, source tree, lockfile hash, Vite version, effective build environment, the optimizer's report schema, tool version and disabled passes, elapsed time and peak RSS in `typeopt-build.json` (schema 1). Build the pinned application once with its original inputs, then run typeopt with the application entry and build again with the example adapter. Use the same environment, Vite settings, minifier and source-map settings. Keep outputs in separate directories. Compare:

```sh
node crates/typeopt/benchmark/measure.mjs /absolute/original-build /absolute/existing-build /absolute/extended-build
```

The command sums every production `.js`, `.mjs` and `.cjs` file of each build, excluding source maps. It requires the pinned Node, which also fixes the bundled zlib and Brotli, and uses gzip level 9 and Brotli quality 11. It fails unless all `typeopt-build.json` files have identical provenance (revision, source tree, lockfile, Node, Vite, mode, source maps and environment), the original build is not optimized, the existing build used report v0 and the extended build report v1, no HTML entry or JS chunk references a chunk file that is missing from its build, and the extended build's raw, gzip and Brotli totals are each strictly smaller than both other builds. Asset sets may differ, because residual-dependency-removal can remove chunks; the output lists added and removed logical assets (file paths without the Vite content hash) against each baseline. A two-argument form compares an original and one optimized build. Record the result with source revision, exact commands, tool versions, execution time and peak RSS. Fixture results do not substitute for application acceptance.

Install browser tooling with `pnpm --dir crates/typeopt/benchmark install --ignore-workspace --frozen-lockfile` and `pnpm --dir crates/typeopt/benchmark exec playwright install chromium`. Serve each build on a separate loopback HTTP port, then run:

```sh
node crates/typeopt/benchmark/browser.mjs http://127.0.0.1:48180 /absolute/original-browser
node crates/typeopt/benchmark/browser.mjs http://127.0.0.1:48181 /absolute/existing-browser
node crates/typeopt/benchmark/browser.mjs http://127.0.0.1:48182 /absolute/extended-browser
node crates/typeopt/benchmark/compare-browser.mjs /absolute/original-browser /absolute/existing-browser
node crates/typeopt/benchmark/compare-browser.mjs /absolute/original-browser /absolute/extended-browser
```

`browser.mjs` fails unless Node, Playwright and Chromium match `pin.json`, and it requires a new evidence directory. Its `checks_passed` covers only that run. Browser acceptance passes only when `compare-browser.mjs` finds identical normalized observations and PNG/SVG export summaries in both runs; it lists any mismatched checks. The harness normalizes sketch seeds in the imported scene so PNG pixels and SVG geometry can be compared despite time-seeded drawing roughness. It uses the application's download/file-input fallback; it does not validate native OS file pickers. It retains actual scene, PNG and SVG downloads for inspection. Run the same Chromium checks on all three builds: draw shapes/freehand, add/edit text, move/resize, undo/redo, save/reload/import a scene, and export PNG/SVG. Compare scene data and exported results. Native platform acceptance requires actual macOS arm64 and Linux x64 execution; an emulated container is not native evidence.

## Licensing and support

Repository-owned code is Apache-2.0; see `LICENSE` and `NOTICE`. The pinned ts-rust compiler retains its MIT/Apache-2.0 and third-party terms in `licenses/`. Its original notice links describe paths in the upstream repository. Report problems through this repository's GitHub issues. Include tool version and redacted reproduction steps; there is no separate support SLA.
