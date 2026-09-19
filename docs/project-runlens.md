# Runlens

## Goal
Explain filesystem observations, execution differences, side effects, declaration coverage, clean execution, repeated outputs, and potential command conflicts for finite noninteractive developer and CI commands.

## Project ID
`ProjectId.Runlens = "runlens"`.

## Domain Ownership Map
- `crates/runlens`: independent Rust CLI workspace and pinned tracing sources.
- `apps/runlens-docs`: English Rspress public documentation at https://runlens.delino.io.
- `scripts/release` and `scripts/install`: release validation and explicit installers.
- `packaging/homebrew`: prebuilt Homebrew template.

## Domain Contract Documents
- [Runtime and distribution](crates-runlens-foundation.md).
- [Public documentation](apps-runlens-docs-foundation.md).

## Cross-Domain Invariants
Issue #907 is normative. Version 0.1.0 implements all nine capabilities; regular-release support requires actual target evidence. The current change delivers implementation, validation, and a PR; public release, tap publication, and site deployment are separate operations.

Runlens is an explicit exception to the default Go language and root Cargo membership rules: its fspy artifact dependencies require an isolated nightly-2026-08-02 workspace. The root nightly-2026-01-01 and protected DevHud dependency graph must not change. Local metadata reports are an explicit exception to default hosted R2 storage: no service, telemetry, or retained history exists.

Report and configuration schema versions are independently fixed at 1. Execution identifiers are UUID v7. CLI/report compatibility is preserved within a product major version. Unknown, incomplete, or cross-environment evidence never establishes reproducibility or universal cache safety.
Baseline compatibility requires equal source revisions and working-tree inclusion policies, even when source bytes match.
Combined verification preserves definite policy failures even when other evidence is inconclusive.
Offline conflict analysis rejects more than 65,536 cross-report target-execution pairs before generating results.

Root `pnpm dev:runlens-docs` starts the documentation app on its fixed loopback port; see the app contract for build and preview commands.

## Change Policy
Update this index, domain contracts, applicable AGENTS rules, public guides, and affected tests together when ownership or public behavior changes. Vendored patches require provenance, rationale, tests, and removal conditions. Generated dist directories are never tracked and must be removed before delivery.

## References
- [Issue #907](https://github.com/delinoio/oss/issues/907)
- [Repository defaults](repository-defaults.md)
- [Workflow contract](repository-workflow-contract.md)
