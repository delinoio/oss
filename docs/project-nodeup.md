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
- Release automation must publish both standalone prebuilt binaries and archive assets for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, plus Sigstore bundle sidecars (`*.sigstore.json`) for each artifact and `SHA256SUMS`.
- Direct installers must verify `SHA256SUMS` entries and Sigstore bundle sidecars, and only support bundle-enabled releases.
- Homebrew installation must use prebuilt `nodeup` release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `nodeup` runtime installation and shim dispatch must support `macOS`, `Linux`, and `Windows` x64/arm64 hosts while leaving x86 hosts out of scope.

## Change Policy
- Update this index and `docs/crates-nodeup-foundation.md` in the same change for behavior or storage contract updates.
- Keep release and install contracts synchronized with root and `crates/AGENTS.md` rules.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
