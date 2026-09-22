# TaskFlow

## Goal
Implement issue [#898](https://github.com/delinoio/oss/issues/898): a graph-driven, cross-language task and development engine. TaskFlow schedules existing commands, not compiler actions. This implementation follows the subsequently approved v1 plan; the issue's original YAML proposals are not independently normative.

## Project ID
`taskflow`; product `TaskFlow`; executable `tflow`.

## Domain Ownership Map
- `crates/taskflow`: Rust library, CLI, configuration schema, adapters, fixtures, and conformance tests.
- Public `/taskflow` documentation is curated in `apps/public-docs`.

## Domain Contract Documents
- [TaskFlow engine](crates-taskflow-foundation.md)
- [Conformance and validation](crates-taskflow-conformance.md)

## Cross-Domain Invariants
- Project dependencies, task prerequisites, and declared artifact relationships are distinct. Native dependency edges never implicitly generate compiler commands.
- pnpm, Cargo, and Go own membership and resolved dependencies; TaskFlow owns explicit task definitions in `taskflow.yml`.
- Local execution, development sessions, and exported CI preserve independent execution causes.
- Remote caching uses R2/S3; it is not remote execution. No service infrastructure is deployed by TaskFlow.
- Rust is explicitly selected by the product requirement. User project/task names and content hashes are stable semantic identifiers; execution IDs use UUID v7.
- Existing repository workflows are not migrated. No public release is authorized by this implementation; the crate is unpublished.
- Cross-platform support must be backed by conformance evidence; unavailable external validation is reported rather than implied.

## Change Policy
Keep the crate rules, engine contract, configuration schema, public documentation, and conformance fixtures synchronized. Add repository ownership and CI rules to the appropriate `AGENTS.md` in the same change.

## References
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)
- [Issue #898](https://github.com/delinoio/oss/issues/898)
