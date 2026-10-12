# typeopt

## Goal

Reduce application JavaScript size through eighteen conservative type-aware optimization passes before bundling. Preserve observable behavior; report unmet application acceptance rather than weaken safety.

## Project ID

`typeopt`

## Domain Ownership Map

- `crates/typeopt`: experimental Rust CLI, private analysis and compiler adapter, optimization passes and pipeline, fixtures, Vite example and benchmark tooling.

## Domain Contract Documents

- [Rust CLI foundation](crates-typeopt-foundation.md)

## Cross-Domain Invariants

The CLI consumes one tsconfig and explicit application entries. Source files and external dependencies remain unchanged. Bundlers and application tests run separately. Rust and local filesystem output are explicit exceptions to repository language and object-storage defaults. No public website, hosted service, telemetry or account integration applies.

## Change Policy

Update this index and the owning contract when paths or interfaces change. Update instructions only for ownership or development-rule changes. Keep implementation status, source revisions, validation results and unperformed checks in PRs, issues or CI artifacts.

## References

- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Repository license](repository-license-contract.md)
