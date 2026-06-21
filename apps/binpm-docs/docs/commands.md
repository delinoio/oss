# Commands

binpm exposes a clap-based command surface. Diagnostic commands must stay read-only unless the crate contract documents a mutation.

## Global and Local Install

```bash
binpm install <source>
binpm add <cmd> <source> [--bin <upstream-binary>]
binpm install
binpm update [cmd...] [--local]
binpm remove <cmd> [--local|--global]
```

`binpm install <source>` installs globally by default, even inside a repository with `binpm.toml`. Pass `--local` to add the source to the project-local manifest instead. `binpm install` without a package spec syncs the local `binpm.toml` manifest.

Use `binpm add <cmd> <source> --bin <upstream-binary>` when the release archive contains multiple executables or when the upstream executable name differs from the local command name. The selected binary is persisted in `binpm.toml`.

Commands that support both local and global scope default to local when a local `binpm.toml` is discovered. Otherwise they default to global. `--local` and `--global` are explicit overrides. Global update is not implemented yet; use local `binpm update` for project tools.

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

`binpm doctor`, `binpm explain`, `binpm verify`, `binpm info`, and `binpm outdated` must not mutate manifests, lockfiles, package records, cache entries, or executables.

## Cache

```bash
binpm cache key
binpm cache list
binpm cache prune
binpm cache clean
```

`binpm cache key` is read-only. `binpm cache prune` and `binpm cache clean` must preserve installed package records and executable entries.
