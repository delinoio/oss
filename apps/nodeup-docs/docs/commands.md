# Command Reference

Global options:

```bash
nodeup --output human|json --color auto|always|never <command>
```

`--output` defaults to `human`. `--color` controls human stdout and stderr styling only.

## toolchain list

```bash
nodeup toolchain list [--quiet|--verbose]
```

Lists installed and linked runtimes.

- Standard human output prints installed and linked counts.
- `--quiet` prints compact runtime identifiers only. With logging disabled, it prints nothing when no runtimes exist.
- `--verbose` prints installed runtime paths and linked runtime paths.
- JSON output has `installed` and `linked` fields.

Use `RUST_LOG=off nodeup toolchain list --quiet` or `nodeup --output json toolchain list` when scripts need log-free stdout.

## toolchain install

```bash
nodeup toolchain install <runtime>...
```

Installs or verifies one or more semantic-version or channel selectors. At least one runtime selector is required. Supported examples:

```bash
nodeup toolchain install 22.1.0
nodeup toolchain install v22.1.0 lts current latest
```

The command rejects linked runtime names before linked-runtime lookup, so a linked-name selector fails the same way whether or not that linked runtime exists. JSON output is an array of entries with `selector`, `runtime`, and `status`, where `status` is `installed` or `already-installed`.

## toolchain uninstall

```bash
nodeup toolchain uninstall <version>...
```

Removes exact installed versions only. At least one version selector is required. Channels and linked runtime names are rejected. Use `nodeup toolchain unlink <name>` for linked runtime records. A runtime cannot be removed while referenced by an exact-version global default or exact-version directory override.

JSON output is the removed version list.

## toolchain link

```bash
nodeup toolchain link <name> <path>
```

Registers an existing runtime directory. The directory must contain `bin/node` or `bin/node.exe`.

Linked names must match `[A-Za-z0-9][A-Za-z0-9_-]*`. Reserved channel names `lts`, `current`, and `latest` cannot be used as linked runtime names.

The linked `node` command must be runnable. Unix hosts require an executable permission bit on `bin/node`; Windows platform behavior uses `bin/node.exe`.

Linking validates the minimum runtime requirement only. It does not require every managed alias command to exist. Successful human output includes a managed shim command availability matrix for `node`, `npm`, `npx`, `yarn`, and `pnpm`, including the checked runtime paths.

JSON output includes `name`, `path`, `status: "linked"`, `managed_shim_commands`, `install_on_demand_eligible`, and `path_precedence_guidance`. Each `managed_shim_commands` entry includes:

- `command`
- `runtime`
- `linked_runtime_name`
- `linked_runtime_path`
- `checked_paths`
- `selected_path`
- `direct_executable_exists`
- `direct_executable_runnable`
- `install_on_demand_eligible`
- `install_on_demand_scope`
- `path_precedence_guidance`

## toolchain unlink

```bash
nodeup toolchain unlink <name>...
```

Removes linked runtime records from nodeup settings and tracked selectors without deleting the external runtime directories.

Unlinking fails with `conflict` when a linked name is the current default or is referenced by a directory override. Change the default or remove/update the override before unlinking.

Missing linked names fail with `not-found`. JSON output is the removed linked-name list.

## default

```bash
nodeup default [runtime]
```

Without an argument, prints the current default selector and resolution status. With an argument, resolves the selector, installs version targets when needed, saves it as the global default, and tracks it for updates.

JSON output includes:

- `default_selector`
- `resolved_runtime`
- `resolution_error`

`resolution_error` is populated when an existing default cannot currently be resolved.

## show active-runtime

```bash
nodeup show active-runtime
```

Prints the active runtime after override/default resolution. It verifies that version runtimes are installed and that the resolved runtime has a runnable `node` executable.

JSON output includes `runtime`, `source`, and `selector`.

## show home

```bash
nodeup show home
```

Prints the effective `data_root`, `cache_root`, and `config_root`.

## show color

```bash
nodeup show color
```

Prints effective color decisions for human stdout, human stderr, and logs. JSON output includes `human_stdout`, `human_stderr`, and `logs` objects with the effective mode, source, enabled state, `NO_COLOR` state, and ignored invalid color environment values when present.

## update

```bash
nodeup update [runtime]...
```

With explicit selectors, processes those selectors. Without arguments, updates tracked selectors first; if no selectors are tracked, it falls back to installed runtimes.

Behavior by selector:

- Linked runtime names are skipped with `skipped-linked-runtime`.
- Channels resolve to the current channel version and install it if needed.
- Exact versions are immutable pins. They are skipped with `skipped-exact-version`, and `previous_runtime` and `updated_runtime` both report the pinned runtime.

Tracked exact versions are canonicalized and deduplicated by semantic version. For example, tracking both `22.1.0` and `v22.1.0` results in one tracked selector, `v22.1.0`.

JSON output is an array with `selector`, `previous_runtime`, `updated_runtime`, and `status`.

## check

```bash
nodeup check
```

Checks installed runtimes for newer available releases without installing anything.

JSON output is an array with `runtime`, `latest_available`, and `has_update`.

## override list

```bash
nodeup override list
```

Lists configured directory overrides. JSON output is an array of `path` and `selector` entries.

## override set

```bash
nodeup override set <runtime> [--path <path>]
```

Sets a directory-scoped selector. `--path` defaults to the current directory. The selector is canonicalized before storage and tracked for `nodeup update`.

JSON output includes `path`, `selector`, and `status: "set"`.

## override unset

```bash
nodeup override unset [--path <path>] [--nonexistent]
```

Removes an override for the provided path or current directory. `--nonexistent` removes stale entries whose directories no longer exist.

JSON output is the removed override list.

## which

```bash
nodeup which [--runtime <runtime>] <command>
```

Prints the executable path Nodeup would run. `--runtime` is an explicit selector and overrides directory/default resolution.

For `yarn` and `pnpm`, `which` uses package-manager planning. In npm-exec mode, the resolved executable path is the selected runtime's `npm` executable.

JSON output includes `runtime`, `command`, and `executable_path`.

Missing-command JSON errors include `diagnostics.checked_paths`, `diagnostics.selected_path`, linked runtime fields when applicable, `diagnostics.install_on_demand_eligible`, and PATH/PATHEXT precedence guidance.

## run

```bash
nodeup run [--install] <runtime> <command> [args...]
```

Runs a delegated command with an explicit runtime selector. Missing version runtimes fail unless `--install` is provided.

In human mode, delegated stdio is inherited. In JSON mode, delegated stdout is routed to stderr so stdout can contain the final JSON response with `runtime`, `command`, and `exit_code`.

When a version runtime is missing and `--install` is omitted, the error includes the exact retry shape `nodeup run --install <runtime> ...` and explains that `nodeup run` requires explicit installation while managed shim dispatch can install a missing version runtime selected by the active default or override. JSON errors include `diagnostics.install_on_demand_eligible: false`, `diagnostics.retry_with_install`, and checked runtime command paths.

## shim setup

```bash
nodeup shim setup [--dir <path>]
```

Creates or repairs managed executable-name dispatch shims for `node`, `npm`, `npx`, `yarn`, and `pnpm`.

- Without `--dir`, Nodeup uses `NODEUP_SHIM_DIR` when set, otherwise `$HOME/.local/bin`.
- macOS and Linux use symlinks named `node`, `npm`, `npx`, `yarn`, and `pnpm`.
- Windows uses copied executables named `node.exe`, `npm.exe`, `npx.exe`, `yarn.exe`, and `pnpm.exe`.
- Re-running the command reports existing valid shims as `existing`.
- Existing unrelated commands are reported as conflicts and are not replaced.
- Stale Nodeup symlinks are repaired.
- Non-Nodeup files and different existing Windows executables are refused instead of being overwritten.

JSON output includes `action`, `status`, `shim_dir`, `nodeup_binary`, `path_active`, `path_instruction`, and `shims`. Each shim entry includes `alias`, `path`, `status`, and `method`.

## self update

```bash
nodeup self update
```

Replaces the current Nodeup binary from `NODEUP_SELF_UPDATE_SOURCE`. `NODEUP_SELF_BIN_PATH` can override the target path; otherwise Nodeup uses the current executable path.

JSON output includes `action`, `status`, `target_binary`, and `source_binary`.

## self uninstall

```bash
nodeup self uninstall
```

Removes Nodeup-owned data, cache, and config roots when they contain artifacts. It refuses unsafe paths that are not clearly Nodeup-owned.

Cleanup boundaries:

- Data: removed when the data root is Nodeup-owned and populated.
- Cache: removed when the cache root is Nodeup-owned and populated.
- Config: removed when the config root is Nodeup-owned and populated.
- Binary: manual; Nodeup does not delete the running binary.
- Shims: manual; Nodeup does not delete aliases created by `nodeup shim setup`.
- Shell profile/PATH: manual; Nodeup does not edit shell profile files or the user PATH.

Human output includes removed paths and remaining manual steps. JSON output includes `action`, `status`, `removed_paths`, `cleanup_boundaries`, `remaining_manual_steps`, and `likely_leftover_paths`.

## self upgrade-data

```bash
nodeup self upgrade-data
```

Creates or migrates local settings and overrides files to the current schema.

JSON output includes the action, top-level status, and per-file migration results.

## completions

```bash
nodeup completions <shell> [command]
```

Generates raw completion scripts. See [Completions](/completions).
