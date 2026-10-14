# Interpreting diagnostics

:::warning Availability
bundlewhy is not yet published to npm. Version 0.1.0 is in development. This page describes the prepared 0.1.0 rules.
:::

## Evidence statuses

Every finding states how bundlewhy knows it.

| Status      | Meaning                                                                                                                        |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Confirmed   | Observed in the analysis engine's output or configuration, for the engine used in this analysis.                               |
| Inferred    | Derived by bundlewhy's own analysis. It is not an observed engine decision, and the finding lists the evidence it is based on. |
| Unavailable | The evidence could not be collected.                                                                                           |

An inferred cause is never presented as an engine decision. Treat inferred findings as a well-supported hypothesis to verify.

## Coverage

Each optimization category has a coverage state for the run.

| State          | Meaning                                                                    |
| -------------- | -------------------------------------------------------------------------- |
| Covered        | Verified detectors ran for this category.                                  |
| Not applicable | The category does not apply to the selected engine.                        |
| Unavailable    | The category applies, but no evidence could be collected.                  |
| Partial        | Detectors ran on incomplete input, for example after a partial collection. |

"No findings" in a covered category means the detectors ran and found nothing. That is different from "no coverage": an unavailable or not-applicable category says nothing about your build. bundlewhy never presents missing coverage as a clean result.

## Rules

| Rule                                   | Engine | Category              | Status                                                                                       | Evidence                                                                                                                                                                                                                                                                                                                                       |
| -------------------------------------- | ------ | --------------------- | -------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `rspack/side-effect-retained`          | Rspack | Tree shaking          | Confirmed                                                                                    | No used exports and an engine-reported `Statement with side_effects` bailout with location                                                                                                                                                                                                                                                     |
| `vite/side-effect-retained`            | Vite   | Tree shaking          | Inferred (Confirmed when the effective declaration keeps the module)                         | Module rendered with no rendered exports while its exports exist; Rolldown reports no retention reason, so the cause is inferred unless the effective declaration keeps the module; the effective declaration is a hook's `moduleSideEffects` value (a plugin's, or Vite's resolver reporting a declared package `sideEffects` field), else the configured `treeshake.moduleSideEffects`, else the package `sideEffects` value |
| `vite/tree-shaking-disabled`           | Vite   | Tree shaking          | Confirmed                                                                                    | Configuration sets `build.rolldownOptions.treeshake: false`; tree-shaking coverage is then not applicable                                                                                                                                                                                                                                      |
| `rspack/module-concatenation-bailout`  | Rspack | Module concatenation  | Confirmed                                                                                    | `ModuleConcatenation bailout` reason for a module that is in a chunk and not concatenated                                                                                                                                                                                                                                                      |
| `vite/commonjs-wrapped`                | Vite   | Module concatenation  | Confirmed                                                                                    | Rolldown input format `cjs` and the `__commonJSMin` runtime helper is rendered                                                                                                                                                                                                                                                                 |
| `rspack/module-in-multiple-chunks`     | Rspack | Chunk splitting       | Confirmed                                                                                    | Module listed in two or more chunks                                                                                                                                                                                                                                                                                                            |
| `rspack/used-exports-disabled`         | Rspack | Tree shaking          | Confirmed                                                                                    | Configuration sets `optimization.usedExports: false`; tree-shaking coverage is then not applicable                                                                                                                                                                                                                                             |
| `rspack/side-effects-disabled`         | Rspack | Tree shaking          | Confirmed                                                                                    | Configuration sets `optimization.sideEffects: false`; tree-shaking coverage is then not applicable                                                                                                                                                                                                                                             |
| `rspack/provided-exports-disabled`     | Rspack | Tree shaking          | Confirmed                                                                                    | Configuration sets `optimization.providedExports: false`; tree-shaking coverage is then not applicable                                                                                                                                                                                                                                         |
| `rspack/concatenation-disabled`        | Rspack | Module concatenation  | Confirmed                                                                                    | Configuration sets `optimization.concatenateModules: false`; concatenation coverage is then not applicable                                                                                                                                                                                                                                     |
| `vite/ineffective-dynamic-import`      | Vite   | Chunk splitting       | Confirmed                                                                                    | Rolldown `INEFFECTIVE_DYNAMIC_IMPORT` log, or static and dynamic importers                                                                                                                                                                                                                                                                     |
| `bundle/duplicate-package`             | Both   | Duplicate modules     | Confirmed                                                                                    | Same package name included from two or more package directories                                                                                                                                                                                                                                                                                |
| `minifier/unresolved-env-condition`    | Both   | Dead-code elimination | Inferred                                                                                     | Generated code still reads `process.env.<NAME>` (Rspack) or Vite's `({}).<NAME>` replacement                                                                                                                                                                                                                                                   |
| `minifier/minification-disabled`       | Both   | Dead-code elimination | Confirmed                                                                                    | Configuration disables minification                                                                                                                                                                                                                                                                                                            |
| `minifier/direct-eval-blocks-mangling` | Both   | Name mangling         | Confirmed with an engine-reported `EVAL` log or eval bailout, Inferred from source otherwise | Direct `eval` in a module whose long local names survive in the generated chunk                                                                                                                                                                                                                                                                |

For `vite/side-effect-retained`, the effective side-effect declaration follows Rolldown's precedence, highest first: a `moduleSideEffects` value set by a resolve, load or transform hook, the configured `build.rolldownOptions.treeshake.moduleSideEffects`, the package `sideEffects` field, and then Rolldown's own analysis of the code. Vite's resolver is a native Rolldown plugin that reports the nearest package `sideEffects` field as such a hook value whenever the package declares it (`true` for a `true` field or a matching list entry, `false` otherwise), so a declared package field outranks the configured option. `getModuleInfo()` lists only the values that JavaScript hooks set, so the collector also resolves each retained module's ID again and records the resolved `moduleSideEffects` value, which includes native hooks. When that read fails and the package declares `sideEffects`, the condition stays unknown because the resolver may have set a value that outranks the configured option. The collector evaluates the configured option for each module: `false`, a module ID list (an empty list included, which marks no module as having side effects), a rule list and `"no-external"` decide, while `true` and a rule list without a matching rule give no verdict, so the package value applies; a function is not evaluated and leaves the declaration unknown. A hook value of `true` is not a declaration: it asks Rolldown to analyze the code and skips both the configured option and the package field, so the finding is inferred and the "declared side-effect free" condition is unmet. Because Vite's resolver produces that value from a package `sideEffects` field that is `true` or lists the file, the direction also names narrowing that declaration, which makes the resolver return `false`; a plugin can return the value as well, and the direction names that source too. An effective `false` reports the condition as met. Rolldown may discard such a module even when its code has side effects, so the finding is inferred from another use of the module (a side-effect import, namespace or re-export use, or an entry) and does not claim top-level side effects. An effective `"no-treeshake"` from a hook, `true` from the configured option, or `true` from the package field (when the resolver set no value) reports the condition as unmet with Confirmed status. A package `sideEffects` list that bundlewhy cannot match to the file leaves the condition unknown, and the direction asks to narrow or remove the matching entry rather than to add a declaration. Any other unrecognized value also leaves the condition unknown, and the direction names the unresolved source (the plugin hook, the configured option or the package declaration). The finding title does not depend on the declaration, so a retained module keeps one diagnostic ID when its declaration changes.

Inlining and constant folding currently have no coverage for either engine. Neither engine exposes those decisions, and bundlewhy has no verified detector for them, so those categories are always shown as unavailable.

## Conditions

Each rule lists the conditions it checks. Every condition has an outcome:

- **Met**: the condition holds in this build.
- **Unmet**: the condition does not hold.
- **Unknown**: bundlewhy could not decide, usually because evidence was unavailable.

## Locations

Locations use your original source positions when a source map maps them. When a chunk has no usable source map, bundlewhy falls back to generated chunk positions, marks source attribution for that chunk unavailable and reports `BW_SOURCE_MAP_MISSING`. Snippets are short excerpts; the UI can also show the full source file when source content was stored.

## Dependency paths

Dependency paths show up to three of the shortest static importer chains from an entry point to the module. Use them to find which import brings a module into the build.

## Corrective direction

Each diagnostic suggests a direction for a fix, together with the safety conditions that must hold before you apply it, for example that a module really has no side effects. bundlewhy does not apply fixes and does not verify that a change keeps your application correct.

## Chunk sizes

For each emitted JavaScript chunk, bundlewhy measures:

- Raw bytes.
- Gzip bytes (level 9).
- Brotli bytes (quality 11).
- Mapped source bytes, where a source map supports attribution. Each generated byte is attributed to at most one source, and unmapped bytes are shown separately.

These are measurements, not savings. bundlewhy never estimates how many bytes a fix would save.

## Retained code is not automatically a defect

Code that a bundler keeps is often kept for a correct reason, such as a real side effect. A diagnostic explains why an optimization did not apply; it does not claim that the result is wrong. Review the conditions and the safety conditions before you change anything.

## Related guides

- [Supported engines](/bundlewhy/supported-engines)
- [Limitations](/bundlewhy/limitations)
