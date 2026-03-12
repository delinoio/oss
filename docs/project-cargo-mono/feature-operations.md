# Feature: operations

## Storage
- No project-owned persistent storage.
- Reads and updates workspace `Cargo.toml` manifests during bump operations.
- Uses git repository state for change detection and release commits/tags.
- Uses Cargo local caches managed by Cargo itself.


## Security
- Never logs registry credentials, authentication tokens, or secret environment values.
- Requires explicit `--allow-dirty` to bypass clean-tree checks for mutating operations.
- Treats non-publishable crate metadata as an enforcement boundary and skips publication.
- Uses explicit command argument construction (no shell interpolation) for git and cargo subprocesses.


## Logging
Required structured log fields:
- `command_path`
- `arg_shape`
- `workspace_root`
- `package`
- `action`
- `outcome`
- `retry_attempt`
- `git_ref`
- `base_ref`

Operational expectations:
- Log command invocation shape before execution.
- Log clean-tree preflight start and outcome for `bump` and `publish`.
- Log package selection decisions and skip reasons.
- Log bump mutation summary (updated manifests, commit id, tags).
- Log publish attempt lifecycle including retries and terminal outcome.
- Log publish prefetch lifecycle (start/completion/lookup error) with package and error context.
- Log rate-limit retry lifecycle with selected wait duration and `Retry-After` presence.
- Use Rust `tracing` for all operational logs.
- Keep ANSI-colored human logs enabled by default with documented opt-out controls.


## Build and Test
Planned commands:
- Build: `cargo build -p cargo-mono`
- Integration tests: `cargo test -p cargo-mono --test cli`
- Test: `cargo test -p cargo-mono`
- Workspace validation: `cargo test`

