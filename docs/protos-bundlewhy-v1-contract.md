# bundlewhy v1 protocol contract

## Scope

`protos/bundlewhy/v1/bundlewhy.proto` owns package `bundlewhy.v1` and service `AnalysisService`. Generated TypeScript (protobuf-es and Connect Query) lives in `apps/bundlewhy-web/src/gen`; generated Go bindings in `protos/gen/go/bundlewhy/v1` are tool output and have no runtime consumer.

## Runtime and Language

Protobuf 3 over Connect unary JSON (`POST /bundlewhy.v1.AnalysisService/<Method>`, `Content-Type: application/json`) on the loopback server, and over an in-memory Connect router transport inside exported HTML.

## Users and Operators

The bundlewhy web UI in a local browser, exported HTML reports and the Rust server.

## Interfaces and Contracts

- Methods: `GetRunProgress`, `GetAnalysis`, `GetSourceFile`, `CancelRun`, `ListRuns`, `SelectRun`, `ExportReport`. No method accepts a filesystem path, shell command or build option. `GetSourceFile` uses a bundle-local source ID; `SelectRun` uses a run UUID from `ListRuns`.
- Enums are stable and additive: `OptimizationCategory`, `EvidenceStatus`, `ConditionOutcome`, `RunStatus`, `RunStage`, `Bundler`, `EngineSupport`, `CoverageState`, `LocationKind`, `SourceOrigin`, `SourceRevisionEvidence`, `AttributionState`, `ProblemSeverity`, `ReportMode`, `ExportUsageState`, `ExportScope`, `ExportUsagePathEnd`, `ExportUsageTruncation`. JSON uses the proto3 enum names.
- `Diagnostic.export_usage` (field `17`, optional message `ExportUsage`) carries Rspack export-level usage evidence: `state`, `note`, `paths` (ordered `ExportUsagePath` chains of `ExportUsageEdge` from the outermost consumer to the diagnostic module, each with an `ExportUsagePathEnd`: `ROOT` for a confirmed start, `CYCLE`, `TRUNCATED` for a display or work limit, `INCOMPLETE` when a dropped edge may be an earlier edge, so the start is not confirmed) and `truncation`. An `ExportReference` is a module display path plus `export_path` segments and an `ExportScope`: `NAMED` for a named export, `MODULE` (empty `export_path`) for module or namespace scope, which is not evidence of a function call. Edges are engine observations; the chains and their order are derived by bundlewhy. Absence of the field means no evidence was recorded (Vite diagnostics, rules without a module scope and results saved before the field existed). New fields and enums are additive; saved results without the field remain valid.
- `SourceFile.revision_evidence` and `SourceFileSummary.revision_evidence` distinguish `BUILD_SNAPSHOT` text from `UNAVAILABLE` analyzed-revision evidence. `UNSPECIFIED` is the conservative value for older bundles that did not record provenance. An unavailable source must not expose post-build live text or its hash as analyzed content.
- Sizes use `uint32` so JSON carries numbers. `Analysis.schema_version` is `1`.
- Saved and exported reports reject `CancelRun`; exported reports also reject `SelectRun` and `ExportReport` with `failed_precondition`.
- Connect errors use standard codes. Messages start with the stable `BW_*` code followed by `: `, and the UI shows the recovery instruction for that code.
- Only Rust validates saved `export_usage` (see the Rust contract for the exact rules); the generated Go bindings have no runtime consumer and the UI renders defensively without validating.
- The Rust server implements the JSON mapping with serde types (`camelCase` field names, enum names as strings, omitted defaults accepted). Changes to the schema must update those types in the same change.

## Storage

None. The result bundle stores `Analysis` and `SourceFile` messages in proto3 JSON form inside `analysis.json`.

## Security

Every server request requires `Authorization: Bearer <token>`, a `Host` equal to the bound loopback address and an `Origin` equal to the server origin. Responses are never cached. Content is untrusted display text.

## Logging

The server logs method, status and duration; never tokens, request bodies or source content.

## Build and Test

`pnpm proto:generate` regenerates bindings; `pnpm proto:lint` and the proto freshness checks run in CI. Rust tests verify that JSON produced by the server parses with the generated TypeScript schema through the UI tests' fixtures.

## Dependencies and Integrations

- [Rust foundation](crates-bundlewhy-foundation.md)
- [Web UI](apps-bundlewhy-web-contract.md)

## Change Triggers

Schema changes update this contract, the Rust serde types, the UI and the project index invariants.

## References

- [Project index](project-bundlewhy.md)
- [Repository defaults](repository-defaults.md)
