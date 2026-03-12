# Feature: operations

## Storage
- Install root: managed Node.js runtimes per version (`data/toolchains/<version>`).
- Cache root: downloaded archives and runtime metadata (`cache/downloads/*`, `cache/release-index.json`).
- Config root:
: `config/settings.toml` for schema version, default selector, linked runtimes, and tracked selectors.
: `config/overrides.toml` for per-path runtime selector overrides.
- Default path policy:
: POSIX data: `$XDG_DATA_HOME/nodeup` (fallback `~/.local/share/nodeup`)
: POSIX cache: `$XDG_CACHE_HOME/nodeup` (fallback `~/.cache/nodeup`)
: POSIX config: `$XDG_CONFIG_HOME/nodeup` (fallback `~/.config/nodeup`)
- Test/dev overrides:
: `NODEUP_DATA_HOME`, `NODEUP_CACHE_HOME`, `NODEUP_CONFIG_HOME`
: `NODEUP_INDEX_URL`, `NODEUP_DOWNLOAD_BASE_URL`, `NODEUP_FORCE_PLATFORM`
: `NODEUP_RELEASE_INDEX_TTL_SECONDS` (positive integer seconds; default `600`)
: `NODEUP_LOG_COLOR` (`always|auto|never`; default `always`)
: `NODEUP_SELF_UPDATE_SOURCE`, `NODEUP_SELF_BIN_PATH`


## Security
- Validate download integrity before activation.
- Restrict permissions on local install and cache directories.
- Avoid executing unverified artifacts.
- Log provenance metadata for each installed version.


## Logging
Default management-command human logging uses `tracing` pretty formatting with `level=on`, `target=off`, `time=off`, and `ansi=on` and a default filter of `nodeup=info`.
Default managed-alias dispatch logging uses the same formatting and a default filter of `nodeup=warn`.
In `--output json`, default logging remains disabled (`nodeup=off`) unless `RUST_LOG` is explicitly set.

Required baseline logs:
- Baseline structured events are emitted at `info` level and are guaranteed when `RUST_LOG` includes `nodeup=info` (or a more verbose level); alias default mode intentionally suppresses those `info` events unless explicitly enabled.
- Command path (`nodeup.<group>.<subcommand>` or `nodeup.<command>`) and `arg_shape` JSON payload (single structured field, sanitized)
- Runtime selector source (`explicit`, `override`, `default`) and resolved runtime
- Override lookup result (`path`, `matched`, `fallback_reason`) with stable fallback codes:
: `override-matched`
: `fallback-to-default`
: `no-default-selector`
- Release-index cache lifecycle (`cache_path`, `age_seconds`, `ttl_seconds`, `outcome`) with stable outcomes:
: `hit`
: `miss`
: `expired`
: `refresh`
: `stale-fallback`
: `write-failure`
- Download source, checksum algorithm, checksum validation result, and install result
- Dispatch executable alias (`argv[0]`) and resolved executable path
- Self-management actions (`self update`, `self uninstall`, `self upgrade-data`) and outcome status (`updated|already-up-to-date|removed|already-clean|upgraded|already-current|failed`)
- Delegated process lifecycle for `run` with spawn/execution logs plus termination detail (`exit_code`, `signal`)
- Completion generation target shell and success/failure status (`action=generate`, `outcome=not-implemented` in current phase)


## Build and Test
Local development install and shell-session patch:
- `eval "$(./scripts/setup/nodeup-local.sh)"`
- Script contract:
: Installs from `crates/nodeup` using `cargo install --path .`.
: Verifies installed `nodeup` binary exists at `<install-root>/bin/nodeup` and is executable.
: Creates managed alias shims `node`, `npm`, and `npx` in `<install-root>/bin`, each pointing to
  the `nodeup` binary via symlink.
: Uses install root `${NODEUP_LOCAL_INSTALL_ROOT:-<repo>/.local/nodeup}`.
: Prints shell exports for `PATH` and `NODEUP_SELF_BIN_PATH` so the current shell session can apply them immediately.
: Does not auto-select a default runtime; operators bootstrap runtime explicitly after install:
  `nodeup default lts`, then verify with `node --version` and `npm --version`.

Validation commands:
- Build: `cargo build -p nodeup`
- Lint: `cargo clippy -p nodeup --all-targets -- -D warnings`
- Test: `cargo test -p nodeup`
- Workspace validation: `cargo test`

Test coverage baseline:
- Unit tests validate selector parsing, runtime resolution, release index cache behavior, logging context detection, and installer checksum helpers.
- CLI integration tests validate contracts for `toolchain`, `default`, `show`, `update`, `check`, `override`, `which`, `run`, `self`, and `completions`.
- CLI integration tests validate deterministic JSON failure envelopes (`kind`, `message`, `exit_code`) and selector precedence (`explicit > override > default`).
- CLI integration tests validate managed alias dispatch behavior for `node`, `npm`, and `npx`, including delegated process exit semantics.
- Project-level operator documentation for workflows and validation lives in `crates/nodeup/README.md`.

Release automation integration:
- `.github/workflows/auto-publish.yml` runs workspace publish orchestration through `cargo run -p cargo-mono -- publish`.
- The workflow triggers on `push` to `main` and `workflow_dispatch`, with a `main`-branch runtime guard.
- Nodeup is included automatically when selected by `cargo-mono publish` as a publishable crate version.
- `.github/workflows/release-nodeup.yml` is the nodeup distribution pipeline.
- `release-nodeup` trigger contract:
: push tags: `nodeup@v*`
: `workflow_dispatch` with `version` and `dry_run`
- Release artifact contract:
: `nodeup-linux-amd64.tar.gz`
: `nodeup-darwin-amd64.tar.gz`
: `nodeup-darwin-arm64.tar.gz`
: `nodeup-windows-amd64.zip`
: `SHA256SUMS` + per-artifact cosign signatures (`*.sig`, `*.pem`)
- Package-manager publication integration:
: Homebrew formula update via `scripts/release/update-homebrew.sh` (`nodeup`)
: winget manifest update via `scripts/release/update-winget.sh` (`DelinoIO.Nodeup`)

