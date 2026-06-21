# Getting Started

Start with a project-local tool declaration when you want reproducible commands in a repository.

```bash
binpm init
binpm add rg github:BurntSushi/ripgrep@14.1.1
binpm x rg --version
```

`binpm init` creates `binpm.toml` with `version = 1` when a manifest does not already exist. It must not install tools by default.

`binpm add <cmd> <source>` declares a local command, installs the selected executable into `$repoRoot/.binpm/bin`, and updates `binpm.lock`.

`binpm x CMD [args...]` resolves `CMD` from `binpm.toml`, installs on demand when allowed by the lockfile policy, prepends `$repoRoot/.binpm/bin` to `PATH`, preserves the caller's working directory, and forwards arguments after `CMD`.

## One-Off Execution

Use an explicit package when a command is not declared in the local manifest:

```bash
binpm x --package github:BurntSushi/ripgrep rg --version
```

binpm must not infer a GitHub repository from the command name. If `CMD` is missing and `--package` is not supplied, the command must fail with a clear hint.

## Frozen Lockfiles

Local `binpm install`, `binpm update`, and `binpm x` honor `--frozen-lockfile`. `CI=true` enables frozen behavior by default, and `--no-frozen-lockfile` is the explicit local-development escape hatch.
