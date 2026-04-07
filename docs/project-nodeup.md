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
- `package.json` `packageManager` support for `yarn|pnpm` must remain strict and deterministic.
- Shell completion generation must remain deterministic for supported shells and top-level command scopes.
- Human output styling controls (`--color`, `NODEUP_COLOR`, and `NO_COLOR` precedence) must remain stable across CLI and public documentation.
- Release automation must publish both standalone prebuilt binaries and archive assets for the supported OS/architecture matrix, plus Sigstore bundle sidecars (`*.sigstore.json`) for each artifact and `SHA256SUMS`.
- Direct installers must verify `SHA256SUMS` entries and Sigstore bundle sidecars, and only support bundle-enabled releases.
- Homebrew installation must use prebuilt `nodeup` release archives for `darwin/amd64`, `darwin/arm64`, and `linux/amd64`, while failing clearly for unsupported Linux arm64 hosts.

## Change Policy
- Update this index and `docs/crates-nodeup-foundation.md` in the same change for behavior or storage contract updates.
- Keep release and install contracts synchronized with root and `crates/AGENTS.md` rules.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
