# Commands

binpm provides commands for global installs, project-local tools, one-off execution, diagnostics, environment setup, and cache management.

## Global and Local Install

```bash
binpm install <source> [--as <cmd>] [--bin <upstream-binary>]
binpm add <cmd> <source> [--bin <upstream-binary>] [--also <cmd=upstream-binary>] [--manifest-only]
binpm install
binpm update [cmd...] [--local|--global] [--dry-run]
binpm remove <cmd> [--local|--global]
```

`binpm install <source>` installs globally, even inside a repository with `binpm.toml`. Use `--as <cmd>` when the global command name should differ from the repository name, and `--bin <upstream-binary>` when an archive needs explicit binary selection. Successful installs print the installed command alias separately from the selected upstream binary, so a repository like `ripgrep` can expose a command such as `rg` when you choose it explicitly. `binpm install <source> --local` is not supported; use `binpm add <cmd> <source>` to declare and install a project-local tool. `binpm install` without a package spec syncs the local `binpm.toml` manifest.

Use `binpm add <cmd> <source> --bin <upstream-binary>` when the release archive contains multiple executables or when the upstream executable name differs from the local command name. The selected binary is persisted in `binpm.toml`.

Use `binpm add <cmd> <source> --manifest-only` only for advanced declaration-review workflows. It writes only `binpm.toml`; it does not resolve releases, write `binpm.lock`, populate cache entries, write package records, or install executables. Success output names the manifest path, skipped lockfile path, skipped executable paths, and the exact follow-up command: `binpm install`.

Use `--also <cmd=upstream-binary>` to declare several commands from one source without repeating the source and version:

```bash
binpm add foo github:owner/tools@v1.2.3 --bin bin/foo --also bar=bin/bar --also baz=bin/baz
```

In each `cmd=upstream-binary` value, `cmd` is the local command alias and `upstream-binary` is the executable name or archive member selected from the upstream release. binpm still writes separate `[tools.<cmd>]` manifest tables for each command.

Commands that support both local and global scope default to local when a local `binpm.toml` is discovered. Otherwise they default to global. `--local` and `--global` are explicit overrides.

`binpm update [cmd...] [--local|--global]` updates selected tools, or every tool in the selected scope when no command names are supplied. Output states the selected scope and whether the request is all-tools or command-scoped before printing the planned update list. Global updates use existing global package records, preserve each command alias and selected upstream binary, resolve the latest stable release for the recorded source, and finalize through the same cache, install, rollback, and verification behavior as global installs. Add `--dry-run` to preview the selected scope, update mode, and planned runtime changes without mutating package records, cache references, or executables.

## Execution

```bash
binpm x CMD [args...]
binpm x --package <source> [--bin <upstream-binary>] [CMD] [args...]
```

`binpm x` runs commands from the local manifest or from an explicitly supplied `--package`.

If a command was added with `--manifest-only`, `binpm x <cmd>` attempts an on-demand install before running it. Frozen mode, including the default `CI=true` behavior, blocks that lockfile work and reports the blocked on-demand install with `binpm install --local` as the safest next command.

With `--package`, use `--bin` to choose the upstream executable for one-off execution. `CMD` remains the command name placed in the temporary execution context. If `CMD` is omitted, the one-off shortcut keeps the source explicit and exposes the repository basename, or the `--bin` basename when `--bin` is supplied. The shortcut form does not forward args; provide an explicit `CMD` when you need to pass args, for example `binpm x --package <source> <cmd> -- <args...>`. `binpm x rg` without a local manifest entry still does not infer a remote package.

## Diagnostics

```bash
binpm doctor
binpm explain <cmd-or-source> [--local|--global]
binpm verify [--local|--global] [--require-verified]
binpm info <cmd-or-source> [--local|--global]
binpm outdated [--local|--global]
```

`binpm doctor`, `binpm explain`, `binpm verify`, `binpm info`, and `binpm outdated` inspect state without changing manifests, lockfiles, cached assets, or installed executables. Scoped read-only commands show the selected local or global scope in human output when that scope affects results, and include `scope` in JSON output. `binpm list --local` and `binpm doctor` identify tools declared in `binpm.toml` without package records or executables as declared but not installed and point back to `binpm install`. `binpm outdated` includes each tool source in stale human rows and JSON tool entries so global tools can be reinstalled from the reported source.

## Environment

```bash
binpm env [--shell <bash|zsh|fish|powershell|pwsh>] [--global|--local]
```

`binpm env` prints shell-specific commands for adding the project-local and global binpm binary directories to `PATH`. It labels the global command as profile-safe and the project-local command as current-project/session-only. Use `--global` to print only the global command for profile setup, or `--local` to print only the project-local current-session command. It does not edit shell profile files.

Supported shell values are `bash`, `zsh`, `fish`, and `powershell`. `PowerShell` is accepted case-insensitively, and `pwsh` is accepted as a PowerShell alias. `--shell` may be omitted when `SHELL` or `ComSpec` identifies a supported shell. `cmd` is a recognized but deferred value and returns an unsupported-shell diagnostic with current-session and persistent user-PATH guidance for cmd.exe.

## Cache

```bash
binpm cache key
binpm cache list
binpm cache prune
binpm cache clean
```

`binpm cache key` is read-only. With `binpm.lock` present, human output is one cache key line for CI cache setup. If `binpm.lock` is missing, human output labels the key as `missing-lockfile`, warns that the empty lockfile digest is used, and recommends `binpm install`; `--json` exposes `status`, `lockfile`, and `recommended_next_command` with the computed key.

`binpm cache prune` removes stale structured local-project cache references before deleting unreferenced cached assets. Active references from other checkouts are preserved, and legacy plain-text references remain preserving until rewritten.

`binpm cache clean` removes global cached asset entries and states exactly what it removes and preserves. It preserves `~/.binpm/cache/refs`, installed package records, and executable links or copies under `~/.binpm/bin`.
