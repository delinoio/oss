# Project: with-watch

## Goal
Provide a Rust-based CLI wrapper that reruns delegated shell utilities and arbitrary commands when inferred or explicit filesystem inputs change.

## Project ID
`with-watch`

## Domain Ownership Map
- `crates/with-watch`

## Domain Contract Documents
- `docs/crates-with-watch-foundation.md`

## Cross-Domain Invariants
- Root passthrough mode must remain `with-watch [--no-hash] <utility> [args...]`.
- Shell mode must remain `with-watch [--no-hash] --shell '<expr>'` and is the supported entrypoint for `&&`, `||`, and `|`.
- Arbitrary command mode must remain `with-watch exec [--no-hash] --input <glob>... -- <command> [args...]`.
- Default change detection must prefer content hashing, while `--no-hash` must switch the rerun filter to metadata-only comparison.
- `exec --input` reruns the delegated command unchanged and must not inject changed paths into argv or environment variables.
- Public crate distribution must remain `cargo install with-watch`.
- Publish tag eligibility must remain enabled through root `[workspace.metadata.cargo-mono.publish.tag].packages`, and release tag naming must remain `with-watch@v<version>`.
- Release automation must publish signed GitHub Release assets for `linux/amd64`, `darwin/amd64`, `darwin/arm64`, and `windows/amd64`, including standalone binaries (`with-watch-<os>-<arch>[.exe]`) and archives (`with-watch-<os>-<arch>.tar.gz|zip`).
- Homebrew installation must consume prebuilt `with-watch` release archives for `darwin/amd64`, `darwin/arm64`, and `linux/amd64`.

## Change Policy
- Update this index and `docs/crates-with-watch-foundation.md` together when CLI shape, watch inference behavior, release automation, or storage/logging contracts change.
- Update root `Cargo.toml`, `.github/workflows/release-with-watch.yml`, `scripts/release/update-homebrew.sh`, and `packaging/homebrew/templates/with-watch.rb.tmpl` in the same change when with-watch release tags, artifact names, or package-manager distribution contracts change.
- Keep root `AGENTS.md` and `crates/AGENTS.md` aligned with ownership and project-ID changes.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
