# bundlewhy v1 protocol contract

## Scope

`protos/bundlewhy/v1/bundlewhy.proto` owns package `bundlewhy.v1` and service `AnalysisService`. Generated TypeScript (protobuf-es and Connect Query) lives in `apps/bundlewhy-web/src/gen`; generated Go bindings in `protos/gen/go/bundlewhy/v1` are tool output and have no runtime consumer.

## Runtime and Language

Protobuf 3 over Connect unary JSON (`POST /bundlewhy.v1.AnalysisService/<Method>`, `Content-Type: application/json`) on the loopback server, and over an in-memory Connect router transport inside exported HTML.

## Users and Operators

The bundlewhy web UI in a local browser, exported HTML reports and the Rust server.

## Interfaces and Contracts

- Methods: `GetRunProgress`, `GetAnalysis`, `GetSourceFile`, `CancelRun`, `ListRuns`, `SelectRun`, `ExportReport`. No method accepts a filesystem path, shell command or build option. `GetSourceFile` uses a bundle-local source ID; `SelectRun` uses a run UUID from `ListRuns`.
- Enums are stable and additive: `OptimizationCategory`, `EvidenceStatus`, `ConditionOutcome`, `RunStatus`, `RunStage`, `Bundler`, `EngineSupport`, `CoverageState`, `LocationKind`, `SourceOrigin`, `SourceRevisionEvidence`, `AttributionState`, `ProblemSeverity`, `ReportMode`. JSON uses the proto3 enum names.
- `SourceFile.revision_evidence` and `SourceFileSummary.revision_evidence` distinguish `BUILD_SNAPSHOT` text from `UNAVAILABLE` analyzed-revision evidence. `UNSPECIFIED` is the conservative value for older bundles that did not record provenance. An unavailable source must not expose post-build live text or its hash as analyzed content.
- Sizes use `uint32` so JSON carries numbers. `Analysis.schema_version` is `1`.
- Saved and exported reports reject `CancelRun`; exported reports also reject `SelectRun` and `ExportReport` with `failed_precondition`.
- Connect errors use standard codes. Messages start with the stable `BW_*` code followed by `: `, and the UI shows the recovery instruction for that code.
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
