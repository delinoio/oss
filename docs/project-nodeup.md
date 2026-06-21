# Project: nodeup

## Goal
Provide a Rust-based Node.js version manager with predictable channel resolution, deterministic shell completions, and shim-based execution.

## Project ID
`nodeup`

## Domain Ownership Map
- `crates/nodeup`
- `apps/nodeup-docs`

## Domain Contract Documents
- `docs/crates-nodeup-foundation.md`
- `docs/apps-nodeup-docs-foundation.md`

## Cross-Domain Invariants
- Stable channel naming and runtime dispatch semantics must be preserved.
- Shim behavior must remain deterministic across supported operating systems.
- `package.json` `packageManager` support for `yarn|pnpm` must remain strict and deterministic.
- Shell completion generation must remain deterministic for supported shells and top-level command scopes.
- Human output styling controls (`--color`, `NODEUP_COLOR`, and `NO_COLOR` precedence) must remain stable across CLI and public documentation.
- `nodeup show color` must remain available as the color diagnostic command for human stdout, human stderr, and log color decisions.
- Release automation must publish both standalone prebuilt binaries and archive assets for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, plus Sigstore bundle sidecars (`*.sigstore.json`) for each artifact and `SHA256SUMS`.
- Direct installers must verify `SHA256SUMS` entries and Sigstore bundle sidecars, require `cosign`, and only support bundle-enabled releases.
- Direct installers must remain available at `scripts/install/nodeup.sh` and `scripts/install/nodeup.ps1`.
- `cargo-binstall` metadata must resolve only first-party GitHub Release assets and disable third-party quick-install and compile fallback strategies.
- Homebrew installation must use prebuilt `nodeup` release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `nodeup` runtime installation and shim dispatch must support `macOS`, `Linux`, and `Windows` x64/arm64 hosts while leaving x86 hosts out of scope.
- `apps/nodeup-docs` must use the repository-default Rspress/Rsbuild-family static documentation toolchain and Cloudflare Pages deployment contract unless this project index and `docs/apps-nodeup-docs-foundation.md` document a replacement.
- Nodeup documentation routes exposed by `apps/nodeup-docs` are `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, and `/reference`.
- Nodeup documentation routes exposed by `apps/nodeup-docs` must stay aligned with runtime, release, installer, shim, completion, package-manager, human/JSON output, and color-control contracts.

## Change Policy
- Update this index, `docs/crates-nodeup-foundation.md`, and `docs/apps-nodeup-docs-foundation.md` in the same change for behavior or storage contract updates that affect Nodeup documentation.
- Update this index and `docs/apps-nodeup-docs-foundation.md` in the same change for `apps/nodeup-docs` path, route, toolchain, validation, or deployment contract updates.
- Keep `scripts/install/nodeup.sh`, `scripts/install/nodeup.ps1`, and `crates/nodeup/Cargo.toml` synchronized with release asset names and signing contracts.
- Keep release, install, and documentation-app contracts synchronized with root, `crates/AGENTS.md`, and `apps/AGENTS.md` rules.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
