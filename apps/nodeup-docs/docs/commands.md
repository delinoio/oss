# Command Reference

Global options:

```bash
nodeup --output human|json --color auto|always|never <command>
```

`--output` defaults to `human`. `--color` controls human stdout and stderr styling only.

For script-safe stdout, use `--output json` for structured data, `nodeup toolchain list --quiet` for newline-delimited runtime identifiers, and `nodeup completions <shell> >file` for completion script redirection. Logs are written to stderr when enabled.

## toolchain list

```bash
nodeup toolchain list [--quiet|--verbose]
```

Lists installed and linked runtimes.

- Standard human output prints installed and linked counts.
- `--quiet` prints compact runtime identifiers only. With logging disabled, it prints nothing when no runtimes exist.
- `--verbose` prints installed runtime paths and linked runtime paths.
- JSON output has `installed` and `linked` fields.

Use `nodeup toolchain list --quiet` or `nodeup --output json toolchain list` when scripts need parseable stdout.

## toolchain install

```bash
nodeup toolchain install <runtime>...
```

Installs or verifies one or more semantic-version or channel selectors. At least one runtime selector is required. Supported examples:

```bash
nodeup toolchain install 22.1.0
nodeup toolchain install v22.1.0 lts current latest
```

The command validates every requested selector before resolving channels, downloading archives, extracting runtimes, or tracking selectors. It rejects linked runtime names before linked-runtime lookup, so a linked-name selector fails the same way whether or not that linked runtime exists. JSON output is an array of entries with `selector`, `runtime`, and `status`, where `status` is `installed` or `already-installed`.

## toolchain uninstall

```bash
nodeup toolchain uninstall <version>...
```

Removes exact installed versions only. At least one version selector is required. Channels and linked runtime names are rejected before uninstall preflight. For channels, list installed exact versions with `nodeup toolchain list --verbose` and uninstall the exact version. Use `nodeup toolchain unlink <name>` for linked runtime records. A runtime cannot be removed while referenced by an exact-version global default or exact-version directory override.

When removal is blocked, human output reports each blocking reference type and path:

- `global-default` points to the settings file that stores the global default.
- `directory-override` points to the override directory path.

Clear or change the reference, then retry:

```bash
nodeup default <runtime>
nodeup override unset --path <path>
nodeup override set <runtime> --path <path>
nodeup toolchain uninstall <version>
```

Successful JSON output is the removed version list. Blocked JSON errors include `diagnostics.blocked_versions` and `diagnostics.blockers`; each blocker includes `reference_type`, `runtime`, `selector`, `path`, `clear_command`, and `change_command`.

## toolchain link

```bash
nodeup toolchain link <name> <path>
```

Registers an existing runtime directory. The directory must contain `bin/node` or `bin/node.exe`.

Linked names must match `[A-Za-z0-9][A-Za-z0-9_-]*`. Reserved channel names `lts`, `current`, and `latest` cannot be used as linked runtime names. Case variants such as `LTS`, `Current`, and `LATEST` are also rejected because they are ambiguous with channel selectors. Use a distinct name such as `local-lts` or `work-node`.

The linked `node` command must be runnable. Unix hosts require an executable permission bit on `bin/node`; Windows platform behavior uses `bin/node.exe`.

Linking validates the minimum runtime requirement only: the linked `node` command is runnable. It does not require every managed alias command to exist. Successful human output separates the required `node` check from optional managed shim availability, warns when optional package-manager shims are missing, and includes checked runtime paths for `node`, `npm`, `npx`, `yarn`, and `pnpm`.

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

Unlinking fails with `conflict` when a linked name is the current default or is referenced by a directory override. The conflict output names every blocker, includes remediation commands such as `nodeup default <runtime>` or `nodeup override unset --path <path>`, and includes the retry command. External runtime directories are not deleted.

Missing linked names fail with `not-found`. JSON output is the removed linked-name list. Blocked JSON errors include `diagnostics.blocked_linked_runtimes`, `diagnostics.blockers`, and `diagnostics.retry_commands`; each blocker includes `reference_type`, `runtime`, `selector`, `path`, `clear_command`, `change_command`, and `action`.

## default

```bash
nodeup default [runtime]
```

Without an argument, prints the current default selector and resolution status. With an argument, resolves the selector, installs version/channel targets when needed, saves it as the global default, and tracks it for updates. Human output reports whether the resolved version was installed or already installed.

JSON output includes:

- `default_selector`
- `resolved_runtime`
- `install_side_effect` when setting a version/channel default
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

Prints effective color decisions for human stdout, human stderr, and logs. JSON output includes `human_stdout`, `human_stderr`, and `logs` objects with the effective mode, source, enabled state, `NO_COLOR` state, ignored invalid color environment values when present, and whether `NO_COLOR` was overridden by a Nodeup-specific color setting.

Valid color environment values are `NODEUP_COLOR=auto|always|never` and `NODEUP_LOG_COLOR=auto|always|never`.

## update

```bash
nodeup update [runtime]...
```

With explicit selectors, validates every requested selector before resolving channels or installing runtimes, then processes those selectors. Without arguments, updates tracked selectors first; if no selectors are tracked, it falls back to installed runtimes. JSON entries for no-argument updates include `selector_source` (`tracked-selectors` or `installed-runtimes`) and `implicit_target: true`.

Behavior by selector:

- Linked runtime names are skipped with `skipped-linked-runtime`.
- Channels resolve to the current channel version and install it if needed.
- Exact versions are immutable pins. They are skipped with `skipped-exact-version`, and `previous_runtime` and `updated_runtime` both report the pinned runtime. Human output calls these pinned selectors out and suggests installing or selecting a newer exact runtime with `nodeup toolchain install <version>`, `nodeup default <version>`, or `nodeup override set <version> --path <path>`.

Tracked exact versions are canonicalized and deduplicated by semantic version. For example, tracking both `22.1.0` and `v22.1.0` results in one tracked selector, `v22.1.0`.

`current` and `latest` resolve to the same newest release-index entry; `latest` is reported as an alias of canonical selector `current`.

JSON output is an array with `selector`, optional `selector_source`, optional `implicit_target`, `selector_kind`, `canonical_selector`, optional `selector_alias_of`, `previous_runtime`, `updated_runtime`, `status`, optional `diagnostic`, and optional `next_action`. Exact-version pins keep `status: "skipped-exact-version"` and include diagnostics that explain the immutable pin plus a next action for moving to a newer exact runtime. Empty no-argument updates include structured error diagnostics with selector source, selector counts, and a selector preview.

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
nodeup override unset [--path <path> | --nonexistent]
```

Removes an override for the provided path or current directory. `--nonexistent` removes stale entries whose directories no longer exist.

`--path` and `--nonexistent` are mutually exclusive. Use `--path` for one override target, or `--nonexistent` for global stale-entry cleanup.

JSON output is the removed override list.

## which

```bash
nodeup which [--runtime <runtime>] <command>
```

Prints the executable path Nodeup would run. `--runtime` is an explicit selector and overrides directory/default resolution.

For `yarn` and `pnpm`, `which` uses package-manager planning. In direct mode it prints the selected runtime's package-manager executable and labels it as a direct runtime binary. In npm-exec mode it prints the selected runtime's `npm` executable and labels that `npm exec` will invoke the requested package manager with the selected package spec.

JSON output includes `runtime`, `command`, `requested_command`, `executable_path`, `mode`, `reason`, optional `package_manager_strategy`, optional `corepack_supported`, optional `package_spec`, optional `package_spec_pinned`, optional `package_json_path`, and a nested `planning` object with the same stable planning diagnostics.

Direct-mode human output includes the path plus the package-manager plan:

```text
/home/me/.nodeup/data/toolchains/v22.1.0/bin/yarn
nodeup: yarn will run as direct runtime binary /home/me/.nodeup/data/toolchains/v22.1.0/bin/yarn (strategy=direct-runtime-binary; package_json=/repo/package.json; reason=package-json-missing-field-direct; corepack=unsupported)
```

npm-exec-mode human output includes the `npm` path plus the package-manager plan:

```text
/home/me/.nodeup/data/toolchains/v22.1.0/bin/npm
nodeup: yarn will run via npm exec using package @yarnpkg/cli-dist@4.13.0 (pinned; strategy=pinned-npm-exec; package_json=/repo/package.json; npm=/home/me/.nodeup/data/toolchains/v22.1.0/bin/npm; reason=package-manager-pinned; corepack=unsupported)
```

Missing-command JSON errors include `diagnostics.checked_paths`, `diagnostics.selected_path`, linked runtime fields when applicable, `diagnostics.install_on_demand_eligible`, and PATH/PATHEXT precedence guidance.

## run

```bash
nodeup run [--install] <runtime> <command> [args...]
```

Runs a delegated command with an explicit runtime selector. Missing version runtimes fail unless `--install` is provided.

In human mode, delegated stdio is inherited. If `yarn` or `pnpm` uses package-manager planning, Nodeup prints a planning notice to stderr before delegation so stdout remains owned by the delegated command. In JSON mode, delegated stdout is routed to stderr so stdout can contain the final JSON response with `runtime`, `command`, `exit_code`, and `planning`.

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
- Existing unrelated commands are reported as conflicts and are not replaced. Conflict output includes the path, ownership classification, and remediation; JSON errors expose the same data under `diagnostics.conflicts`.
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

Removes Nodeup-owned data, cache, and config roots when they contain artifacts. It refuses unsafe paths and reports configured roots that are not clearly Nodeup-owned without deleting them.

Cleanup boundaries:

- Data: removed when the data root is Nodeup-owned and populated.
- Cache: removed when the cache root is Nodeup-owned and populated.
- Config: removed when the config root is Nodeup-owned and populated.
- Binary: manual; Nodeup does not delete the running binary.
- Shims: manual; Nodeup does not delete aliases created by `nodeup shim setup`.
- Shell profile/PATH: manual; Nodeup does not edit shell profile files or the user PATH.

Human output separates removed paths, manual leftovers, ownership-refused paths, and remaining manual steps. JSON output includes `action`, `status`, `removed_paths`, `manual_leftover_paths`, `ownership_refused_paths`, `cleanup_boundaries`, `remaining_manual_steps`, and `likely_leftover_paths`.

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

Generates raw completion scripts. The optional command scope is top-level only and produces a script scoped to that command. Successful scripts stay raw on stdout even with `--output json`; invalid shells and unsupported scopes use JSON error envelopes on stderr when JSON mode is requested. See [Completions](/completions).
