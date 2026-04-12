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
- Root passthrough mode must remain `with-watch [--no-hash] [--clear] <utility> [args...]`.
- Shell mode must remain `with-watch [--no-hash] [--clear] --shell '<expr>'` and is the supported entrypoint for `&&`, `||`, and `|`.
- Arbitrary command mode must remain `with-watch exec [--no-hash] [--clear] --input <glob>... -- <command> [args...]`.
- `with-watch --help` must include a long-help appendix that documents command modes, the recognized delegated-command inventory, safe current-directory defaults, and recognized-but-not-auto-watchable commands.
- The public CLI surface must keep exactly one delegated-command entrypoint per invocation: passthrough argv, `--shell`, or `exec --input`.
- After watch input inference, watcher setup, and baseline snapshot capture succeed, `with-watch` must execute the delegated command immediately once before waiting for the first filesystem change event.
- Default change detection must prefer content hashing, while `--no-hash` must switch the rerun filter to metadata-only comparison.
- `--clear` must remain a global flag that clears stdout before the initial run and each rerun only when stdout is a terminal.
- `WW_LOG` must remain the only supported environment variable for configuring `with-watch` diagnostic `tracing` logs, and the default diagnostic filter must remain `with_watch=off`.
- `RUST_LOG` must not affect `with-watch` diagnostic logging.
- `exec --input` reruns the delegated command unchanged and must not inject changed paths into argv or environment variables.
- Commands without safe inferred filesystem inputs must fail clearly and direct operators to `with-watch exec --input ...`.
- Passthrough and shell modes must use adapter-driven input inference that excludes known outputs, scripts, and pattern operands from the watch set.
- First-class search adapters must include `grep`, `rg`, `ag`, and `fd`; search patterns, globs, and type filters must stay out of the watch set while explicit search roots and file-valued pattern/ignore inputs remain watchable.
- First-class schema/codegen adapters must include `protoc`, `flatc`, `thrift`, and `capnp compile`; explicit source files, include/import roots, descriptor inputs, and conform-reference inputs must remain watchable while generated output paths stay filtered out of the watch set. `protoc` response files (`@argfile`) are too opaque for v1 inference and must fall back to `exec --input` guidance instead of being partially watched. When `protoc` omits `--proto_path`, the current working directory must be treated as an implicit import root and remain watchable.
- `ls`, `dir`, and `vdir` must use metadata listing snapshots instead of recursive file-content hashing: the default watch scope is immediate children, `-R` stays recursive, and `-d` watches only the named path.
- Shell redirects must treat `<` and `<>` targets as watched inputs and `>`, `>>`, `&>`, `&>>`, and `>|` targets as filtered outputs.
- Shell parsing support is limited to command-line expressions plus `&&`, `||`, and `|`; broader shell control-flow stays out of scope until documented otherwise.
- Safe pathless default watch roots are limited to the built-in allowlist (`ls`, `dir`, `vdir`, `du`, and `find`).
- Commands that mutate watched inputs directly must refresh the baseline snapshot after each run and suppress self-triggered reruns caused by their own writes.
- Path-based watch inputs must anchor watcher subscriptions at the nearest existing directory so replace-style writers keep emitting later external changes.
- Public crate installation must continue to support `cargo install with-watch`.
- Direct installers must remain available at `scripts/install/with-watch.sh` and `scripts/install/with-watch.ps1`; direct installs must verify `SHA256SUMS` entries and Sigstore bundle sidecars and require `cosign`.
- `cargo-binstall` metadata must resolve only first-party GitHub Release assets and disable third-party quick-install and compile fallback strategies.
- Publish tag eligibility must remain enabled through root `[workspace.metadata.cargo-mono.publish.tag].packages`, and release tag naming must remain `with-watch@v<version>`.
- Release automation must publish signed GitHub Release assets for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone binaries (`with-watch-<os>-<arch>[.exe]`) and archives (`with-watch-<os>-<arch>.tar.gz|zip`).
- Homebrew installation must consume prebuilt `with-watch` release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.

## Change Policy
- Update this index and `docs/crates-with-watch-foundation.md` together when CLI shape, watch inference behavior, operator guidance, release automation, side-effect suppression, or storage/logging contracts change.
- Update root `Cargo.toml`, `.github/workflows/release-with-watch.yml`, `scripts/release/update-homebrew.sh`, and `packaging/homebrew/templates/with-watch.rb.tmpl` in the same change when with-watch release tags, artifact names, or package-manager distribution contracts change.
- Keep `scripts/install/with-watch.sh`, `scripts/install/with-watch.ps1`, and `crates/with-watch/Cargo.toml` synchronized with release asset names and signing contracts.
- Keep root `AGENTS.md` and `crates/AGENTS.md` aligned with ownership and project-ID changes.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
