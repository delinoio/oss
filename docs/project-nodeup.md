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
- Self-management and completion modules are currently explicit skeleton commands returning a deterministic `NotImplemented` response.

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

Command contracts:
- `nodeup toolchain list`
: Input: optional verbose/quiet formatting flags.
: Output: installed runtime list with optional detail metadata.
- `nodeup toolchain install <runtime>...`
: Input: one or more runtime selectors.
: Behavior: missing runtimes are downloaded, checksum-verified, and activated in local store.
: Output: per-runtime installation/update result and resolved runtime identifier.
- `nodeup toolchain uninstall <runtime>...`
: Input: one or more installed runtime selectors.
: Behavior: uninstallation guards treat canonical equivalent spellings (`22.1.0` and `v22.1.0`) as the same selector when checking default/override references.
: Output: removal result and final installed runtime count; tracked selectors for removed versions are cleaned in canonical form.
- `nodeup toolchain link <name> <path>`
: Input: custom runtime name and local runtime directory path.
: Output: linked custom runtime registration result.
- `nodeup default [runtime]`
: Input: global default runtime selector.
: Behavior: installs runtime if missing.
: Output: final default runtime identifier.
- `nodeup show active-runtime`
: Output: currently active runtime after applying resolution precedence.
- `nodeup show home`
: Output: resolved nodeup home directory path.
- `nodeup update [runtime]...`
: Input: optional runtime selectors.
: Behavior: updates selected runtimes; with no selectors, updates tracked selectors from config and falls back to installed runtimes.
: Behavior: explicit version updates report `already-up-to-date` when the newest candidate runtime is already installed.
: Output: update summary by selector/runtime.
- `nodeup check`
: Output: available update status for installed runtimes.
- `nodeup override list`
: Output: directory-to-runtime override mapping table.
- `nodeup override set <runtime> [--path <path>]`
: Input: runtime selector and optional directory path.
: Behavior: validates selector syntax before persisting and stores canonical selector IDs (for example `22.1.0` is stored as `v22.1.0`).
: Output: applied override scope and runtime.
- `nodeup override unset [--path <path>] [--nonexistent]`
: Input: optional directory path or nonexistent cleanup flag.
: Output: removed override entries summary.
- `nodeup run [--install] <runtime> <command>...`
: Input: runtime selector and delegated command line.
: Behavior: if `--install` is set, missing runtime is installed before execution.
: Output: delegated process exit status and selected runtime.
- `nodeup which [--runtime <runtime>] <command>`
: Input: delegated executable name and optional explicit runtime selector.
: Output: concrete executable path that would be executed.
- `nodeup self update`
: Output: `NotImplemented` error in current phase.
- `nodeup self uninstall`
: Output: `NotImplemented` error in current phase.
- `nodeup self upgrade-data`
: Output: `NotImplemented` error in current phase.
- `nodeup completions <shell> [command]`
: Input: target shell and optional command scope.
: Output: `NotImplemented` error in current phase.

Resolution precedence contract:
- Explicit runtime in command invocation (`run`, `which --runtime`) has highest priority.
- Directory override (`override set`) takes precedence over global default.
- Global default (`default`) is used when no explicit runtime or override is present.
- If no selector resolves and auto-install is disabled, command must fail with a deterministic error.

Dispatch contract:
- If invoked as `node`, `npm`, `npx`, or another managed alias, nodeup resolves target Node.js version and forwards execution.
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
- Command path (`nodeup.<group>.<subcommand>` or `nodeup.<command>`) and argument shape (excluding sensitive values)
- Runtime selector source (`explicit`, `override`, `default`) and resolved runtime
- Override lookup result (`path`, `matched`, `fallback_reason`)
- Download source, checksum algorithm, checksum validation result, and install result
- Dispatch executable alias (`argv[0]`) and resolved executable path
- Self-management actions (`self update`, `self uninstall`, `self upgrade-data`) and outcome status
- Delegated process lifecycle for `run` (spawn, exit code, signal)
- Completion generation target shell and success/failure status

## Build and Test
Planned commands:
- Build: `cargo build -p nodeup`
- Test: `cargo test -p nodeup`
- Workspace validation: `cargo test`

## Roadmap
- Phase 1: Rustup-style command skeleton (`toolchain`, `default`, `show`, `override`, `run`, `which`).
- Phase 2: Runtime installer, checksum verification, and command-level auto-install behavior.
- Phase 3: Self-management and completion generation flows (`self`, `completions`).
- Phase 4: Cross-platform shim parity and CI hardening.

## Open Questions
- Signature verification scope beyond `SHA256` checksum matching (for example GPG signature validation).
- Cross-platform archive support expansion timeline (Windows zip installation path).
- Self-update rollout policy and release channel strategy for `nodeup` binary updates.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
