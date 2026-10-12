# typeopt Rust foundation

## Scope

`crates/typeopt` owns the CLI, private compiler integration, shared whole-program analysis, the eighteen optimization passes, the transformation pipeline, the stage-2 bundle mode, fixtures, Vite example adapter and Excalidraw benchmark harness. The CLI and output interfaces are experimental; `report.json` is schema v1 and `modules.json`/`properties.json` are schema v0. Incompatible changes require explicit documentation.

## Runtime and Language

Rust, with Node.js 24 for fixture execution and integration tooling. Pin `ts_goport` to upstream revision `b4144b4769d13bbfe9e648840fe6297893981427`; manually validate upstream changes. Keep optimizer code separate from upstream compiler code. Initial distribution is Cargo source builds for macOS arm64 and Linux x64.

## Users and Operators

Application maintainers invoke the optimizer, then separately run their bundler and behavior tests. GitHub issues provide support, without a support SLA.

## Interfaces and Contracts

Optimization has two stages with one pass inventory. Stage 1, `typeopt --project <tsconfig> --entry <file>... --out-dir <new-dir>`, analyzes application TypeScript before bundling and emits ESM. Stage 2, `typeopt --bundle <bundler-output-dir> --out-dir <new-dir>`, analyzes bundler output chunks after bundling (see Bundle Mode). `--check` performs the same analysis, in-memory transformation and size decisions without publishing output and does not require an output directory. Repeatable `--disable-pass <id>` excludes passes; unknown IDs are invalid CLI arguments. Support `--help`, `--version` and `--verbose`.

The fixed pass inventory and its stable IDs are: `property-shortening`, `property-disambiguation`, `property-ambiguation`, `finite-key-rewriting`, `unused-method-removal`, `unused-field-removal`, `constant-property-inlining`, `method-devirtualization`, `call-inlining`, `function-specialization`, `signature-reduction`, `type-directed-control-flow`, `primitive-operation-simplification`, `enum-runtime-reduction`, `discriminant-compaction`, `aggregate-elimination`, `intrinsic-specialization` and `residual-dependency-removal`. All are enabled by default. Skip reasons use stable snake_case codes.

Shared analysis covers every reachable application TS/TSX module from one tsconfig and the explicit entries: alias-resolved symbol references with their kind (declaration, alias, call, construction, write, type-only, read), public entry exports, symbols referenced from application JavaScript, computed accesses, mentioned names, names declared by external declarations, expression purity and module initialization purity. Internal exports are eligible for analysis; public entry exports, JavaScript references, module namespace objects, external declarations, unknown calls, unresolved keys, reflection, serialization, prototype mutation, decorators and declaration merging are boundaries. Type information only nominates candidates. Runtime evidence comes from source, control flow and uses; an annotation never proves a closed call graph, immutable value, impossible branch or intact intrinsic. Preserve evaluation order, effects, exceptions, property descriptors, object/function identity and observable names or arity, and preserve code when an obligation cannot be proven.

Pass planners produce transformation bundles: TypeScript source edits (insertions, deletions and replacements, possibly across modules) with a stable identity, a primary pass and the cleanup passes the bundle includes. The pipeline evaluates each bundle by transpiling the affected modules before and after with equal emitter settings and accepts it only when the total emitted ESM byte count strictly decreases; source maps and reports are excluded. A bundle that needs a disabled pass is skipped; disabled transformations never run as cleanup. When a bundle removes the last runtime use of an import, the pipeline keeps module initialization with a bare import unless residual-dependency-removal is enabled and the module initialization is proven pure. Accepted bundles are type-checked; a bundle that introduces type errors is rolled back by bisection and recorded as `recheck_failed`. Syntax errors in transformed code and compiler failures fail the operation. Every round recomputes all facts from the current text; rounds repeat in deterministic order until no reduction is accepted, and property passes run once afterward.

The property passes use a whole-program, context-insensitive points-to analysis over object literals, key literals, bindings, contained objects and internal function arguments and returns across imports, re-exports and module cycles. Functions get trusted summaries only when internal, declared once and referenced only by direct fixed-arity calls or module aliases. Unknown values taint joined regions; an escape in any joined region preserves the entire group. Named and generic annotations, `typeof` queries, namespace objects and JavaScript remain property boundaries.

TypeScript modules with a leading `@ts-nocheck` comment are unchecked boundaries like JavaScript, because they disable the semantic recheck behind every transformation.

Resolve reachable workspace sources using the project's configuration. Declaration files provide external types. JavaScript passes through unchanged; node_modules is read-only external code. Reject syntax/type errors, eval, with, CommonJS module syntax (`import = require()` and `export =`, which `module: Preserve` would emit as CommonJS), unsupported configuration and project-reference orchestration with actionable diagnostics. Read configuration as data; do not execute it. Compatibility adjustments must be explicit and semantics-preserving.

Emit JS and source maps plus versioned `modules.json`, `properties.json` and `report.json`. Source maps compose every round's edits; inserted, deleted, inlined or moved code maps to the original range it replaced. Module mappings link original paths to output JS/maps, record the SHA-256 of each analyzed source and mark any module with an accepted runtime transformation as `optimized`. The `inputs` list records SHA-256 digests of configuration files and every other file the compiler read, including module-resolution metadata; consumers must reject output when any module or input changed. Property mappings include stable group identities and original/final names and describe property transformations only. Report v1 keeps the applicable v0 statistics and adds enabled/disabled pass IDs, rounds, emitted size before and after, per-pass applied/skipped counts with reason codes, accepted bundles with locations and measured byte changes, and skipped locations with reasons. Bundle savings are attributed to the whole bundle, never split among its passes. Identical inputs/configuration/options/tool versions produce identical code, mappings and reports. Exclude time and peak-memory measurements from deterministic comparisons. Zero safe changes is a valid explained result.

The Vite example validates the whole analysis snapshot at build start and substitutes mapped JS for `optimized` modules under original module identities; preserved TS modules retain the original Vite transpilation. Compose source maps through the original module identities. Keep assets in the original project and preserve resolution context. It is not a separately published plugin. Roll back one optimization by excluding its pass and rebuilding into a new directory; roll back adoption by restoring original build inputs/configuration.

## Bundle Mode

Stage 2 loads every `.js`/`.mjs` file of a bundler output directory as one closed ES module program with the standard DOM libraries and no `@types` packages; `--project` and `--entry` are not used. Static imports, literal dynamic imports and Vite preload dependency lists link chunks. A chunk that no other chunk imports is an entry whose exports are public. A chunk is transformed only when it is known to be an ES module (ESM statements, imported by a chunk or loaded by an HTML module script); classic scripts, `importScripts` workers and chunks matched by repeatable `--bundle-exclude <text>` stay unchanged unchecked boundaries.

Bundled JavaScript is not type-checked, so inferred and JSDoc types can be wrong. The bundle pass set is fixed to the passes whose proofs do not depend on static types: `property-shortening`, `property-disambiguation`, `property-ambiguation`, `call-inlining`, `signature-reduction`, `type-directed-control-flow`, `primitive-operation-simplification` and `residual-dependency-removal`. The other ten passes are reported as disabled and cannot be enabled in bundle mode; `--disable-pass` narrows the set. Identifier-like tokens of string and template literals count as mentioned names. Candidates are not parsed individually; the recheck after each round treats syntax errors (including TypeScript-only syntax) and semantic errors in chunks as failures and rolls the responsible bundles back with `recheck_failed`. Input chunks with syntax errors are rejected.

Edits apply to the chunk text, and the size gate compares the edited chunk text bytes without emitting. Output is a staged copy of the input directory with edited chunks replaced, source maps composed with the edits, and edited content-hashed chunks (`<name>-<hash>.js` or `<name>.<hash>.js` with an 8-character base64url hash that contains a digit, an uppercase letter, `-` or `_`) plus every hashed chunk that references a renamed chunk renamed deterministically with all references in text assets rewritten; `--keep-chunk-names` disables renaming. Pages with Subresource Integrity attributes are rejected. The report v1 is printed to standard output; its `size.metric` is `chunk_text_bytes` instead of `emitted_esm_bytes`. Behavior tests against the optimized directory remain the operator's responsibility.

## Storage

One-shot, in-memory analysis; no daemon, watch mode or persistent cache. Never modify input files or an existing output destination. Stage output in an operation-owned temporary directory and publish without replacing an existing destination. Error and cancellation paths return failure, remove owned temporary artifacts and never publish a complete result.

## Security

After installation, analysis performs no network access, user-code execution or telemetry. Do not log source contents, credentials or environment values. External APIs and observable object shapes form preservation boundaries.

## Logging

Use structured Rust tracing and English stderr diagnostics. Verbose mode adds diagnostic context without source text or environment values. Measure elapsed time and peak resident memory; no performance threshold or resource ceiling applies.

## Build and Test

Run root `cargo test` after Rust changes and applicable frontend `pnpm test` after frontend changes. Compare original and optimized fixtures in Node, including side effects. Cover each optimization, combinations, structural types, unions, aliases, computed keys, accessors, inheritance, reflection, serialization, external escape, collisions, determinism, maps, malformed input, filesystem failures and Ctrl+C. Verify no input execution or network access in a restricted environment.

Bundle mode needs Node fixtures that compare original and optimized entry chunks, including chunks whose unchecked types lie, runtime-parsed member paths, generated functions, dynamic namespace access, observable arity, unimported entry chunks, classic scripts, exclusion, map composition, rehashing and determinism.

Every pass needs positive fixtures that demonstrate its transformation and size benefit and negative fixtures for its safety boundaries, run with the pass alone, in combinations and with every pass enabled and disabled; every fixture compares original and optimized Node behavior. Validate Vite with workspace aliases, TSX, unchanged JS, original assets, non-property transformations and stale transitive inputs. Pin the Excalidraw commit, lockfile, runtime, browser, production settings and compression tools before benchmarking; apply documented compatibility adjustments equally to all builds. Compare three builds: original inputs, existing typeopt at the pinned `existing_typeopt_revision` and the extended optimizer; each optimized build records the clean source revision its optimizer binary was built from. Asset sets may differ; record added and removed assets, verify that no entry or chunk references a missing chunk and sum every production JS asset excluding source maps. The extended build's raw, gzip and Brotli totals must each be strictly smaller than both other builds; no minimum percentage applies.

Chromium acceptance compares drawing shapes/freehand, text/move/resize edits, undo/redo, save/reload/file import and PNG/SVG export across the three builds. Actual application acceptance cannot be replaced by fixtures. Require native build/execution on macOS arm64 and Linux x64. Retain source revision, commands, results and unperformed checks in CI artifacts or PR/issue records. Preserve existing affected CI and macOS runner restrictions.

## Dependencies and Integrations

Use immutable ts-rust input and preserve all upstream licenses/notices. Repository-owned code is Apache-2.0. Vite and Excalidraw are validation integrations; the CLI never launches them. Public Rust/Node APIs, library declaration transformation, CommonJS output, JavaScript optimization outside bundle mode, prebuilt distributions and other platforms are out of scope.

## Change Triggers

Synchronize CLI/report or safety changes with the README and tests. Update the project index for ownership changes and relevant AGENTS files for development-rule changes only.

## References

- [Project index](project-typeopt.md)
- [Repository defaults](repository-defaults.md)
- [License contract](repository-license-contract.md)
- [Workflow contract](repository-workflow-contract.md)
