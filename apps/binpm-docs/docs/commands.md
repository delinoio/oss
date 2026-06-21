# Commands

binpm provides commands for global installs, project-local tools, one-off execution, diagnostics, environment setup, and cache management.

## Global and Local Install

```bash
binpm install <source>
binpm add <cmd> <source> [--bin <upstream-binary>]
binpm install
binpm update [cmd...] [--local]
binpm update --global
binpm remove <cmd> [--local|--global]
```

`binpm install <source>` installs globally by default, even inside a repository with `binpm.toml`. Pass `--local` to add the source to the project-local manifest instead. `binpm install` without a package spec syncs the local `binpm.toml` manifest.

Use `binpm add <cmd> <source> --bin <upstream-binary>` when the release archive contains multiple executables or when the upstream executable name differs from the local command name. The selected binary is persisted in `binpm.toml`.

Commands that support both local and global scope default to local when a local `binpm.toml` is discovered. Otherwise they default to global. `--local` and `--global` are explicit overrides.

Global update is pending implementation. `binpm update --global`, including `--dry-run`, fails with a workaround: run `binpm outdated --global` to find stale global tools, then reinstall each one with `binpm install <source>`. Use `binpm update --local` for project tools.

## Execution

```bash
binpm x CMD [args...]
binpm x --package <source> [--bin <upstream-binary>] CMD [args...]
```

`binpm x` runs commands from the local manifest or from an explicitly supplied `--package`.

With `--package`, use `--bin` to choose the upstream executable for one-off execution. `CMD` remains the command name placed in the temporary execution context.

## Diagnostics

```bash
binpm doctor
binpm explain <cmd-or-source> [--local|--global]
binpm verify [--local|--global] [--require-verified]
binpm info <cmd-or-source> [--local|--global]
binpm outdated [--local|--global]
```

`binpm doctor`, `binpm explain`, `binpm verify`, `binpm info`, and `binpm outdated` inspect state without changing manifests, lockfiles, cached assets, or installed executables.

## Environment

```bash
binpm env --shell <bash|zsh|fish|powershell>
```

`binpm env` prints shell-specific commands for adding the project-local and global binpm binary directories to `PATH`. It labels the global command as profile-safe and the project-local command as current-project/session-only. It does not edit shell profile files.

Supported shell values are `bash`, `zsh`, `fish`, and `powershell`. `PowerShell` is accepted case-insensitively. `cmd` is a recognized but deferred value and returns an unsupported-shell diagnostic.

## Cache

```bash
binpm cache key
binpm cache list
binpm cache prune
binpm cache clean
```

`binpm cache key` is read-only. If `binpm.lock` is missing, human output warns that the empty lockfile digest is used; `--json` exposes `lockfile` status with the computed key.

`binpm cache prune` removes stale structured local-project cache references before deleting unreferenced cached assets. Active references from other checkouts are preserved, and legacy plain-text references remain preserving until rewritten.

`binpm cache clean` removes global cached asset entries and states exactly what it removes and preserves. It preserves `~/.binpm/cache/refs`, installed package records, and executable links or copies under `~/.binpm/bin`.
