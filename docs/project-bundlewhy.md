# bundlewhy

## Goal

Explain why a bundler or its default JavaScript minifier could not apply an optimization to a client production build. Application developers run a local CLI that performs one diagnostic build of a trusted project, then explore the results in a local English desktop web UI. Each diagnostic connects an optimization rule, its condition outcomes, the relevant code, supporting evidence, dependency paths and a corrective direction. bundlewhy explains; it never changes project source or configuration. Correct, useful explanations take priority over coverage breadth and release dates.

## Project ID

`bundlewhy`

## Domain Ownership Map

- `crates/bundlewhy`: Rust analysis core, CLI (`analyze`, `open`, `export`, `engines`), result bundle storage, size measurement, local Connect server and HTML export.
- `apps/bundlewhy-web`: React/TypeScript web UI built into one self-contained HTML document that the Rust binary embeds and serves.
- `protos/bundlewhy/v1`: Connect RPC schema shared by the Rust server, the web UI and exported reports.
- `packages/bundlewhy`: minimal JavaScript engine collector adapters, the npm/npx entry point, acceptance fixtures and distribution tooling.
- `apps/public-docs/docs/bundlewhy`: public installation, usage, interpretation, privacy, limitation and troubleshooting guides.

## Domain Contract Documents

- [Rust core, CLI and local server](crates-bundlewhy-foundation.md)
- [Web UI](apps-bundlewhy-web-contract.md)
- [Connect protocol v1](protos-bundlewhy-v1-contract.md)
- [Engine adapters and distribution](packages-bundlewhy-distribution-contract.md)

## Cross-Domain Invariants

- Stable enums (`OptimizationCategory`, `EvidenceStatus`, `ConditionOutcome`, `RunStatus`, `RunStage`, `Bundler`, `EngineSupport`, `CoverageState`, `LocationKind`, `SourceOrigin`, `AttributionState`, `ProblemSeverity`, `ReportMode`) are owned by the protocol contract. Rust, the UI and the adapters use the same values; additions are additive and never renumbered.
- Error codes are stable `BW_*` identifiers owned by the Rust contract. Adapters, the server and the UI display the same codes with recovery instructions.
- Evidence statuses mean the same everywhere. `Confirmed` is confirmed for the engine used in this analysis. `Inferred` is bundlewhy's own analysis and must state its supporting evidence. `Unavailable` marks evidence that could not be collected. No surface presents an inferred cause as an observed engine decision, and no surface presents missing coverage as a clean result.
- The frozen engine compatibility matrix is owned by the Rust contract and published in public documentation for each release. The diagnostic build always uses the project's own installed engine with documented collection overrides; bundlewhy never substitutes another engine. Analysis with an engine outside the matrix requires explicit user selection and stays visible in the UI and exported data.
- One run analyzes one client production JavaScript build. Each run has a new UUID v7 identifier and its own directory. There is no retry, resume, watch mode, cache, migration or cross-run comparison.
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
