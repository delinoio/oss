# Project: nodeup

## Goal
`nodeup` provides a rustup-like Node.js version management experience in Rust.
The primary goal is deterministic multi-version Node.js execution with automatic runtime installation, directory-aware override selection, and executable-name-based dispatch.

## Path
- `crates/nodeup`

## Runtime and Language
- Rust CLI

## Users
- Developers who need multiple Node.js versions on one machine
- CI operators who need deterministic Node.js runtime selection

## In Scope
- Rustup-style hierarchical command surface for Node.js runtime management.
- Toolchain lifecycle management: list, install, uninstall, and local-link runtime directories.
- Runtime selection controls: global default runtime, per-directory overrides, and explicit one-shot execution.
- Runtime-aware introspection commands: active runtime and runtime home discovery.
- Update flows for installed runtimes.
- Shim-aware command delegation for `node`, `npm`, and `npx`.
- Dispatch behavior based on executable name (`argv[0]`) for runtime shims.
- Automatic Node.js binary download and activation when a requested runtime is missing.
- Human and JSON output modes (`--output human|json`) for machine-parseable command output.

## Out of Scope
- JavaScript package manager features (`npm`, `pnpm`, `yarn`) beyond runtime delegation
- Node package dependency resolution
- Remote execution services
- Rust-only command families and concepts: `target`, `component`, `doc`, `man`, `set`
- Rust compiler-specific target triples, standard library components, and documentation topics

## Architecture
- Top-level command router dispatches to rustup-style subcommand groups (`toolchain`, `show`, `override`, `self`) and leaf commands (`default`, `update`, `check`, `run`, `which`, `completions`).
- Version resolver normalizes user input into a canonical runtime selector (exact version and stable aliases such as `lts`, `current`, `latest`).
- Runtime installer/downloader fetches official Node.js archives, validates `SHA256` checksums from `SHASUMS256.txt`, and stages verified artifacts before activation.
- Runtime store manager maintains installed runtimes, linked runtime metadata, tracked selectors, and activation pointers.
- Override manager resolves runtime precedence by directory scope and fallback defaults.
- Shim dispatcher handles executable-name-based mode branching for `node`, `npm`, `npx`, and other managed aliases.
- Self-management commands are explicit skeleton commands returning a deterministic `NotImplemented` response.
- Completion generation is implemented via clap command metadata and supports scoped script generation for a selected top-level command.

## Interfaces
Canonical nodeup command identifiers:

```ts
enum NodeupCommand {
  Toolchain = "toolchain",
  Default = "default",
  Show = "show",
  Update = "update",
  Check = "check",
  Override = "override",
  Which = "which",
  Run = "run",
  Self = "self",
  Completions = "completions",
}
```

Canonical toolchain subcommand identifiers:

```ts
enum NodeupToolchainCommand {
  List = "list",
  Install = "install",
  Uninstall = "uninstall",
  Link = "link",
}
```

Canonical show subcommand identifiers:

```ts
enum NodeupShowCommand {
  ActiveRuntime = "active-runtime",
  Home = "home",
}
```

Canonical override subcommand identifiers:

```ts
enum NodeupOverrideCommand {
  List = "list",
  Set = "set",
  Unset = "unset",
}
```

Canonical self subcommand identifiers:

```ts
enum NodeupSelfCommand {
  Update = "update",
  Uninstall = "uninstall",
  UpgradeData = "upgrade-data",
}
```

Canonical channel identifiers:

```ts
enum NodeupChannel {
  Lts = "lts",
  Current = "current",
  Latest = "latest",
}
```

CLI entrypoints:
- `nodeup [--output <human|json>] <command> ...`
- Shim dispatch path: when the same binary is invoked as `node`, `npm`, or `npx`, nodeup resolves the active runtime and delegates execution directly (without going through the management CLI parser).

Global option contract:
- `--output <human|json>` is available for all management commands and defaults to `human`.

Runtime selector grammar:

```ts
enum NodeupRuntimeSelectorKind {
  Version = "semver-with-optional-v-prefix",
  Channel = "lts|current|latest",
  LinkedName = "ascii-alnum-first + [ascii-alnum|_|-]*",
}
```

- `22.1.0` and `v22.1.0` are equivalent version selectors and normalize to `v22.1.0`.
- Linked runtime names must start with an ASCII alphanumeric character and may contain `_` or `-` after the first character.

Subcommand contracts:
- `nodeup toolchain list`
: Input: none.
: Output: installed runtime versions and linked runtime map.
- `nodeup toolchain install <runtime>...`
: Input: one or more selectors; empty input is invalid.
: Allowed selector kinds: `Version`, `Channel` (linked names are rejected for install).
: Behavior: resolves each selector, downloads/validates runtime when missing, and tracks the original selector.
: Status field (`--output json`): `installed` or `already-installed`.
- `nodeup toolchain uninstall <runtime>...`
: Input: one or more selectors; empty input is invalid.
: Allowed selector kinds: exact `Version` only (channels/linked names are rejected).
: Behavior: blocks removal if target runtime is referenced by default selector or any override; selector spelling is canonicalized so `22.1.0` and `v22.1.0` are treated as the same runtime.
: Output: removed runtime list; tracked selectors that canonicalize to removed versions are deleted.
- `nodeup toolchain link <name> <path>`
: Input: linked runtime name and existing local runtime path.
: Behavior: validates name format, canonicalizes path, stores it in linked runtimes, and tracks the selector.
: Status field (`--output json`): `linked`.
- `nodeup default [runtime]`
: With `runtime`: resolves selector, installs if it resolves to a version and is missing, saves selector as global default, and tracks selector.
: Without `runtime`: returns current default selector and resolved runtime (if configured).
- `nodeup show active-runtime`
: Output: resolved runtime (`runtime`), selection source (`explicit|override|default`), and canonical selector.
: Failure: returns deterministic not-found error when neither override nor default selector exists.
- `nodeup show home`
: Output: `data_root`, `cache_root`, and `config_root`.
- `nodeup update [runtime]...`
: With selectors: processes exactly the provided selectors.
: Without selectors: uses tracked selectors first; falls back to installed versions when tracked selector list is empty.
: Status field (`--output json`): `updated`, `already-up-to-date`, or `skipped-linked-runtime`.
- `nodeup check`
: Output: one row per installed runtime with `latest_available` and `has_update`.
- `nodeup override list`
: Output: configured override entries (`path`, `selector`).
- `nodeup override set <runtime> [--path <path>]`
: Input: runtime selector and optional path (defaults to current directory).
: Behavior: selector is validated and stored in canonical stable form (example: `22.1.0` -> `v22.1.0`), then tracked.
: Status field (`--output json`): `set`.
- `nodeup override unset [--path <path>] [--nonexistent]`
: Input: optional target path and optional cleanup flag for stale paths.
: Output: removed override entries.
- `nodeup which [--runtime <runtime>] <command>`
: Input: delegated command name and optional explicit selector.
: Behavior: resolves runtime precedence, verifies runtime availability, and prints concrete executable path.
: Note: unlike `run`, this command does not auto-install missing runtimes.
- `nodeup run [--install] <runtime> <command>...`
: Input: explicit runtime selector and delegated argv (at least one command token is required).
: Behavior: if resolved runtime version is missing, command fails unless `--install` is provided.
: Output: delegated command result with runtime, delegated command name, and exit code.
: Exit code: returns delegated process exit code on success path.
- `nodeup self update`
: Output: deterministic `NotImplemented` error in current phase.
- `nodeup self uninstall`
: Output: deterministic `NotImplemented` error in current phase.
- `nodeup self upgrade-data`
: Output: deterministic `NotImplemented` error in current phase.
- `nodeup completions <shell> [command]`
: Input: target shell (`bash|zsh|fish`) and optional top-level command scope.
: Behavior: renders shell completion script text from the clap command tree; when `command` is provided, generated output contains only that top-level command branch.
: Output (`--output human`): raw completion script text.
: Output (`--output json`): deterministic metadata payload containing `shell`, `scope`, `status`, `script`, and `script_bytes`.

Help output contract:
- `nodeup --help` must show one-line descriptions for all top-level commands (`toolchain`, `default`, `show`, `update`, `check`, `override`, `which`, `run`, `self`, `completions`).
- `nodeup <group> --help` must show one-line descriptions for nested subcommands in grouped command families (`toolchain`, `show`, `override`, `self`).
- `nodeup <command> --help` should include concise argument descriptions for required and optional inputs.

Resolution precedence contract:
- Explicit runtime in command invocation (`run`, `which --runtime`) has highest priority.
- Directory override (`override set`) takes precedence over global default.
- Global default (`default`) is used when no explicit runtime or override is present.
- If no selector resolves and auto-install is disabled, command must fail with a deterministic error.

Dispatch contract:
- If invoked as `node`, `npm`, or `npx`, nodeup resolves target Node.js version and forwards execution.
- If invoked as `nodeup`, nodeup performs management commands.

Symlink contract:
- Shims point to one nodeup binary.
- Runtime behavior branches by `argv[0]`.

## Storage
- Install root: managed Node.js runtimes per version (`data/toolchains/<version>`).
- Cache root: downloaded archives and metadata (`cache/downloads/*`).
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

## Security
- Validate download integrity before activation.
- Restrict permissions on local install and cache directories.
- Avoid executing unverified artifacts.
- Log provenance metadata for each installed version.

## Logging
Required baseline logs:
- Command path (`nodeup.<group>.<subcommand>` or `nodeup.<command>`) and `arg_shape` JSON payload (single structured field, sanitized)
- Runtime selector source (`explicit`, `override`, `default`) and resolved runtime
- Override lookup result (`path`, `matched`, `fallback_reason`) with stable fallback codes:
: `override-matched`
: `fallback-to-default`
: `no-default-selector`
- Download source, checksum algorithm, checksum validation result, and install result
- Dispatch executable alias (`argv[0]`) and resolved executable path
- Self-management actions (`self update`, `self uninstall`, `self upgrade-data`) and outcome status (`outcome=not-implemented` in current phase)
- Delegated process lifecycle for `run` with spawn/execution logs plus termination detail (`exit_code`, `signal`)
- Completion generation target shell, optional scope, and status (`action=generate`, `outcome=generated`)

## Build and Test
Planned commands:
- Build: `cargo build -p nodeup`
- Test: `cargo test -p nodeup`
- Workspace validation: `cargo test`
- Integration test stability: stdout-sensitive assertions must execute nodeup with `RUST_LOG=warn` unless a test explicitly validates info logs.

## Roadmap
- Phase 1: Rustup-style command skeleton (`toolchain`, `default`, `show`, `override`, `run`, `which`).
- Phase 2: Runtime installer, checksum verification, and command-level auto-install behavior.
- Phase 3: Self-management flows (`self`) and completion generation flow hardening (`completions`).
- Phase 4: Cross-platform shim parity and CI hardening.

## Open Questions
- Signature verification scope beyond `SHA256` checksum matching (for example GPG signature validation).
- Cross-platform archive support expansion timeline (Windows zip installation path).
- Self-update rollout policy and release channel strategy for `nodeup` binary updates.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
