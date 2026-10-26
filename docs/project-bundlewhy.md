# bundlewhy

## Goal

Explain why a bundler or its default JavaScript minifier could not apply an optimization to a client production build. Application developers run a local CLI that performs one diagnostic build of a trusted project, then explore the results in a local English desktop web UI. Each diagnostic connects an optimization rule, its condition outcomes, the relevant code, supporting evidence, dependency paths and a corrective direction. bundlewhy explains; it never changes project source or configuration. Correct, useful explanations take priority over coverage breadth and release dates.

## Project ID

`bundlewhy`

## Domain Ownership Map

- `crates/bundlewhy`: Rust analysis core, CLI (`analyze`, `open`, `export`, `engines`), result bundle storage, size measurement, local Connect server and HTML export.
- `apps/bundlewhy-web`: React/TypeScript web UI built into one self-contained HTML document that the Rust binary embeds and serves.
- `protos/bundlewhy/v1`: Connect RPC schema shared by the Rust server, the web UI and exported reports.
- `packages/bundlewhy`: minimal JavaScript engine collector adapters, the npm/npx entry point, acceptance fixtures and distribution tooling (three native targets and the tag-triggered npm/GitHub release workflow).
- `apps/public-docs/docs/bundlewhy`: public installation, usage, interpretation, privacy, limitation and troubleshooting guides.

## Domain Contract Documents

- [Rust core, CLI and local server](crates-bundlewhy-foundation.md)
- [Web UI](apps-bundlewhy-web-contract.md)
- [Connect protocol v1](protos-bundlewhy-v1-contract.md)
- [Engine adapters and distribution](packages-bundlewhy-distribution-contract.md)

## Cross-Domain Invariants

- Stable enums (`OptimizationCategory`, `EvidenceStatus`, `ConditionOutcome`, `RunStatus`, `RunStage`, `Bundler`, `EngineSupport`, `CoverageState`, `LocationKind`, `SourceOrigin`, `SourceRevisionEvidence`, `AttributionState`, `ProblemSeverity`, `ReportMode`, `ExportUsageState`, `ExportScope`, `ExportUsagePathEnd`, `ExportUsageTruncation`) are owned by the protocol contract. Rust, the UI and the adapters use the same values; additions are additive and never renumbered.
- Rspack export usage evidence distinguishes what Rspack reported (edges) from what bundlewhy derived (chains and their order). It never states that code ran, can be removed or saves bytes, and an unavailable graph is shown as unavailable for the diagnostic instead of an invented chain.
- Error codes are stable `BW_*` identifiers owned by the Rust contract. Adapters, the server and the UI display the same codes with recovery instructions.
- `BW_SOURCE_FRESHNESS_UNVERIFIED` is a warning, not a change claim: a live source file could not be read or inspected, so freshness (or, for a source without build text such as a binary asset or generated asset module, existence) is unverified. Sources with build snapshot text keep using it and are not marked stale; sources without build text keep only their path. Only a file that no longer exists is reported with `BW_SOURCE_STALE`, and the run is not partial. Every surface shows the same wording distinction.
- Evidence statuses mean the same everywhere. `Confirmed` is confirmed for the engine used in this analysis. `Inferred` is bundlewhy's own analysis and must state its supporting evidence. `Unavailable` marks evidence that could not be collected. No surface presents an inferred cause as an observed engine decision, and no surface presents missing coverage as a clean result.
- Original source content, snippets and hashes are attributed to the analyzed revision only when a build source-map snapshot supplies that text. A later live read without a snapshot does not prove the revision; saved, served and exported results retain explicit unavailable source-revision evidence.
- A retained environment read is a dead-code elimination diagnostic only when the generated code shows that it guards a branch. Value-only reads do not establish an optimization blocker. A read that is only a value inside an array literal, object literal, function object, call argument list, constructor call or `typeof`, `void` or `delete` operand does not inherit a `&&`, `||`, `??` or `?:` operator that follows it; a branch operator found inside the container still guards its own read. An array or object literal passes its read on only when the member access that follows selects the element or property holding it (`[read][0]`, `({a: read}).a`), a function invoked in place passes on the value it returns, and `typeof read` guards a branch only through an equality test (`typeof read === "undefined"`). The branch-evidence scan is lexical: it decides from the generated code whether `await(` is the unary operator or a call (a class field initializer does not inherit the async context of the function around the class, and a concise async arrow body ends at a line break, and `yield` ends the statement at a line break like `return` and `throw`), and an undecidable case omits the finding or keeps it Inferred.
- The frozen engine compatibility matrix is owned by the Rust contract and published in public documentation for each release. The diagnostic build always uses the project's own installed engine with documented collection overrides; bundlewhy never substitutes another engine. Analysis with an engine outside the matrix requires explicit user selection and stays visible in the UI and exported data.
- One run analyzes one client production JavaScript build. Each run has a new UUID v7 identifier and its own directory. There is no retry, resume, watch mode, cache, migration or cross-run comparison.
- Live, saved and exported completed-run durations use the instant after the main result bundle write and optional summary write attempt. A small completion record persists that duration; its own write and later finalization are outside the measured interval. Older bundles without the record retain their saved duration.
- Result bundles and exported HTML are untrusted input when reopened. Rust validates schema version and structure; the UI renders all content as escaped text and loads no external resources.
- Deviations from `docs/repository-defaults.md`: the primary language is Rust (with a React/TypeScript UI and minimal JavaScript adapters) instead of Go; results are stored in a user-selected local directory instead of Cloudflare R2; the UI is hosted by a local loopback server and embedded in exported HTML instead of Cloudflare Pages. These exceptions follow from the product being an explicitly invoked local developer tool without hosted infrastructure, accounts or telemetry.
- Feature flags, telemetry, remote observability and hosted services do not apply. Release and rollback use version installation and replacement.

## Change Policy

Update this index and the owning contract when paths, interfaces, enums, error codes or the engine matrix change. Protocol changes update the protocol contract, the Rust contract and the UI contract together. Engine matrix or adapter changes update the Rust contract, the distribution contract and the public supported-engine page together. Update `AGENTS.md` only for ownership or development-rule changes and follow the [instruction-update policy](README.md#instruction-update-policy). Record implementation status, validation results, measured analysis time and peak memory, and unperformed checks in pull requests, issues and CI artifacts.

## References

- [Repository defaults](repository-defaults.md)
- [Repository license](repository-license-contract.md)
- [Repository workflow](repository-workflow-contract.md)
- [Project index template](project-template.md)
- [typeopt](project-typeopt.md) is a separate optimizer; bundlewhy has no typeopt integration.
