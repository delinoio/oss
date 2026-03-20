# Project: nodeup

## Goal
Provide a Rust-based Node.js version manager with predictable channel resolution, deterministic shell completions, and shim-based execution.

## Project ID
`nodeup`

## Domain Ownership Map
- `crates/nodeup`

## Domain Contract Documents
- `docs/crates-nodeup-foundation.md`

## Cross-Domain Invariants
- Stable channel naming and runtime dispatch semantics must be preserved.
- Shim behavior must remain deterministic across supported operating systems.
- Shell completion generation must remain deterministic for supported shells and top-level command scopes.

## Change Policy
- Update this index and `docs/crates-nodeup-foundation.md` in the same change for behavior or storage contract updates.
- Keep release and install contracts synchronized with root and `crates/AGENTS.md` rules.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
