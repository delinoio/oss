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

## Interfaces and Contracts
- Root passthrough mode must remain `with-watch [--no-hash] <utility> [args...]`.
- Shell mode must remain `with-watch [--no-hash] --shell '<expr>'`.
- `exec` mode must remain `with-watch exec [--no-hash] --input <glob>... -- <command> [args...]`.
- Stable internal enums must remain aligned with the current v1 contract:
  - `ChangeDetectionMode::{ContentHash, MtimeOnly}`
  - `CommandSource::{Argv, Shell, Exec}`
  - `CommandAnalysisStatus::{Resolved, NoInputs, AmbiguousFallback}`
  - `CommandAdapterId` adapter categories used for built-in inference
  - `SideEffectProfile::{ReadOnly, WritesExcludedOutputs, WritesWatchedInputs}`
  - `WatchInput::{Path, Glob}`
  - `SnapshotEntryKind::{File, Directory, Missing}`
- Shell mode must parse command-line expressions with `&&`, `||`, and `|`, while shell control-flow constructs remain out of scope for v1.
- Shell redirects must treat `<` and `<>` targets as watched inputs and `>`, `>>`, `&>`, `&>>`, and `>|` targets as filtered outputs.
- Generic watch planning must use adapter-driven inference for built-in command families and a conservative fallback for unknown tools.
- Safe pathless `.` defaults are limited to `ls`, `dir`, `vdir`, `du`, and `find`.
- Built-in inference must exclude known outputs, scripts, inline patterns, and opaque fallback operands from the watch set.
- Wrapper commands (`env`, `nice`, `nohup`, `stdbuf`, and `timeout`) must unwrap to the delegated command before adapter selection.
- `exec --input` remains the canonical explicit input contract when inference is insufficient, but command-side side-effect metadata may still be inferred for rerun suppression and logging.
- Commands marked as `WritesWatchedInputs` must refresh the baseline snapshot after each run and suppress reruns caused only by their own writes while they were executing.

## Storage
- `with-watch` does not persist project state.
- Snapshot state is in-memory only for the current process.

## Security
- Delegated commands run with inherited stdio and current-process privileges.
- `with-watch` must not rewrite delegated argv or inject changed-path placeholders into child processes in v1.
- Logging must avoid printing secret environment values passed through delegated commands.

## Logging
- Use structured `tracing` logs for command planning, watcher setup, snapshot capture, debounce decisions, and rerun causes.
- Logs must include `command_source`, `detection_mode`, input counts, `adapter_id`, `fallback_used`, `default_watch_root_used`, `filtered_output_count`, `side_effect_profile`, and rerun suppression outcomes.

## Build and Test
- Local validation: `cargo test -p with-watch`
- Workspace validation baseline: `cargo test --workspace --all-targets`
- Tests must cover CLI modes, shell parsing, adapter classification, fallback ambiguity handling, snapshot diffing, self-write suppression, and representative rerun flows.

## Dependencies and Integrations
- Uses `clap` for CLI parsing.
- Uses `starbase_args` for shell command-line parsing.
- Uses `notify` for filesystem event delivery.
- Uses `blake3` for content-hash-based rerun filtering.

## Change Triggers
- Update `docs/project-with-watch.md` with this file when command shape, detection behavior, or ownership changes.
- Update `docs/README.md`, root `AGENTS.md`, and `crates/AGENTS.md` when project registration or policy changes.

## References
- `docs/project-with-watch.md`
- `docs/domain-template.md`
