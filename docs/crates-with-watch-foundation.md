# crates-with-watch-foundation

## Scope
- Project/component: `with-watch` crate foundation contract
- Canonical path: `crates/with-watch`

## Runtime and Language
- Runtime: Rust CLI watcher runtime
- Primary language: Rust

## Users and Operators
- Developers who want POSIX/coreutils-style commands to rerun automatically when their inputs change
- Maintainers validating generic watch planning and rerun behavior across platforms
- Release engineers operating crate publication and binary distribution workflows

## Interfaces and Contracts
- Root passthrough mode must remain `with-watch [--no-hash] [--clear] <utility> [args...]`.
- Shell mode must remain `with-watch [--no-hash] [--clear] --shell '<expr>'`.
- `exec` mode must remain `with-watch exec [--no-hash] [--clear] --input <glob>... -- <command> [args...]`.
- The CLI must continue to reject mixed modes and empty delegated-command requests with operator-facing guidance.
- Passthrough and shell modes must infer watch inputs before the first run; if inference does not produce safe filesystem inputs, the command must fail with `exec --input` guidance instead of guessing.
- After watch input inference, watcher setup, and baseline snapshot capture succeed, all command modes must execute the delegated command immediately once before waiting for the first filesystem change event.
- `exec --input` must accept repeatable explicit glob/path values, keep the delegated command unchanged, and remain the canonical fallback for otherwise ambiguous or pathless commands.
- `--no-hash` must remain a global flag that switches rerun filtering from content hashes to metadata-only comparison.
- `--clear` must remain a global flag that clears stdout before the initial run and each rerun only when stdout is a terminal.
- `WW_LOG` must remain the only supported environment variable for configuring `with-watch` diagnostic `tracing` filters.
- The default diagnostic log filter must remain `with_watch=off`, and `RUST_LOG` must not affect `with-watch` logging behavior.
- Public crate installation must remain available via `cargo install with-watch`.
- Direct installers must remain available at `scripts/install/with-watch.sh` and `scripts/install/with-watch.ps1`, and direct installs must verify `SHA256SUMS` plus Sigstore bundle sidecars via `cosign verify-blob --bundle`.
- `cargo-binstall` metadata must resolve only first-party GitHub Release assets and disable `quick-install` and `compile` strategies.
- Publish tag naming must remain `with-watch@v<version>`.
- Stable internal enums must remain aligned with the current v1 contract:
  - `ChangeDetectionMode::{ContentHash, MtimeOnly}`
  - `CommandSource::{Argv, Shell, Exec}`
  - `OutputRefreshMode::{Preserve, ClearTerminal}`
  - `CommandAnalysisStatus::{Resolved, NoInputs, AmbiguousFallback}`
  - `CommandAdapterId` adapter categories used for built-in inference
  - `SideEffectProfile::{ReadOnly, WritesExcludedOutputs, WritesWatchedInputs}`
  - `PathSnapshotMode::{ContentPath, ContentTree, MetadataPath, MetadataChildren, MetadataTree}`
  - `WatchInput::{Path, Glob}`
  - `SnapshotEntryKind::{File, Directory, Missing}`
- Shell mode must parse command-line expressions with `&&`, `||`, and `|`, while shell control-flow constructs remain out of scope for v1.
- Shell redirects must treat `<` and `<>` targets as watched inputs and `>`, `>>`, `&>`, `&>>`, and `>|` targets as filtered outputs.
- Generic watch planning must use adapter-driven inference for built-in command families and a conservative fallback for unknown tools.
- Safe pathless `.` defaults are limited to `ls`, `dir`, `vdir`, `du`, and `find`.
- `ls`, `dir`, and `vdir` must use metadata listing snapshots so the first run does not recurse through large trees before spawning the delegated command.
- Plain `ls`/`dir`/`vdir` directory operands must watch only the named directory plus immediate children, `-R` must switch them to recursive metadata tree snapshots, and `-d`/`--directory` must watch only the named path entry.
- Built-in inference must exclude known outputs, scripts, inline patterns, and opaque fallback operands from the watch set.
- Wrapper commands (`env`, `nice`, `nohup`, `stdbuf`, and `timeout`) must unwrap to the delegated command before adapter selection.
- `exec --input` remains the canonical explicit input contract when inference is insufficient, but command-side side-effect metadata may still be inferred for rerun suppression and logging.
- Commands marked as `WritesWatchedInputs` must refresh the baseline snapshot after each run and suppress reruns caused only by their own writes while they were executing.
- Path watch inputs must attach their OS watcher to the nearest existing directory so replace-style writers such as GNU `sed -i` do not orphan follow-up change detection on Linux.
- Operator-facing documentation must explain the three command modes, the `exec --input` escape hatch, shell support boundaries, and why self-mutating commands do not loop on their own writes.
- `with-watch --help` long help must enumerate the recognized delegated-command inventory, including wrapper commands, dedicated built-in adapters and aliases, generic read-path commands, safe current-directory defaults, and recognized-but-not-auto-watchable commands.
- Recognized-but-not-auto-watchable commands must remain clearly labeled as requiring `exec --input` when operators want explicit rerun inputs.
- Homebrew installation must consume prebuilt GitHub release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.

## Storage
- `with-watch` does not persist project state.
- Snapshot state is in-memory only for the current process.

## Security
- Delegated commands run with inherited stdio and current-process privileges.
- `with-watch` must not rewrite delegated argv or inject changed-path placeholders into child processes in v1.
- Logging must avoid printing secret environment values passed through delegated commands.
- Release automation must publish checksum manifests and Sigstore bundle sidecars without exposing registry or tap-write credentials.

## Logging
- Use structured `tracing` logs for command planning, watcher setup, snapshot capture, debounce decisions, and rerun causes.
- Diagnostic `tracing` logs are operator opt-in: they are disabled by default and enabled via `WW_LOG`.
- Logs must include `command_source`, `detection_mode`, `output_refresh_mode`, input counts, `adapter_id`, `fallback_used`, `default_watch_root_used`, `filtered_output_count`, `side_effect_profile`, snapshot modes, snapshot entry counts, snapshot capture elapsed time, and rerun suppression outcomes.

## Build and Test
- Local validation: `cargo test -p with-watch`
- Workspace validation baseline: `cargo test --workspace --all-targets`
- Tests must cover CLI modes, immediate startup execution, shell parsing, adapter classification, fallback ambiguity handling, snapshot diffing, self-write suppression, TTY-only output clearing, and representative rerun flows.
- Documentation changes should be checked against `cargo run -p with-watch -- --help` and the integration scenarios in `crates/with-watch/tests/cli.rs`.
- Publishability validation: `cargo publish -p with-watch --dry-run`
- Release contract checks should align with `.github/workflows/release-with-watch.yml`.
- Release assets must include standalone binaries (`with-watch-<os>-<arch>[.exe]`) and compressed archives (`with-watch-<os>-<arch>.tar.gz|zip`) for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.
- Release signing outputs must include `SHA256SUMS.sigstore.json` and `<artifact>.sigstore.json` sidecars.
- Install docs must keep Bash, PowerShell, `cargo-binstall`, and GitHub Actions usage aligned with the installer scripts and manifest metadata.

## Dependencies and Integrations
- Uses `clap` for CLI parsing.
- Uses `starbase_args` for shell command-line parsing.
- Uses `notify` for filesystem event delivery.
- Uses `blake3` for content-hash-based rerun filtering.
- Integrates with root `auto-publish` tag publication and `.github/workflows/release-with-watch.yml`.
- Integrates with Homebrew tap automation through `scripts/release/update-homebrew.sh` and `packaging/homebrew/templates/with-watch.rb.tmpl`.
- Integrates with direct installer scripts and `cargo-binstall` metadata for prebuilt binary distribution.

## Change Triggers
- Update `docs/project-with-watch.md` with this file when command shape, detection behavior, release distribution, or ownership changes.
- Update `docs/README.md`, root `AGENTS.md`, and `crates/AGENTS.md` when project registration or policy changes.

## References
- `docs/project-with-watch.md`
- `docs/domain-template.md`
