# Getting Started

Start with a project-local tool declaration when you want reproducible commands in a repository.

```bash
binpm init
binpm add rg github:BurntSushi/ripgrep@14.1.1
binpm x rg --version
```

`binpm init` creates `binpm.toml` with `version = 1` when a manifest does not already exist. It prints the resolved full destination path before creation and prints the created manifest path after success. It does not install tools by default.

When you run `binpm init` from a nested directory, manifest creation uses the current Git worktree root when one is available. Outside Git, it uses the nearest ancestor that already contains `binpm.toml`, or the current directory when no ancestor manifest exists. There is no current-directory override flag in the current contract.

`binpm add <cmd> <source>` declares a local command, installs the selected executable into `<project>/.binpm/bin`, and updates `binpm.lock`.

`binpm x CMD [args...]` resolves `CMD` from `binpm.toml`, installs on demand when allowed by the lockfile policy, prepends `<project>/.binpm/bin` to `PATH`, preserves the caller's working directory, and forwards arguments after `CMD`.

After `binpm add`, use `binpm x <cmd>` as the default local invocation path. `binpm env --shell <bash|zsh|fish|powershell>` prints opt-in PATH commands when you want direct shell access; it does not edit shell profiles.

## One-Off Execution

Use an explicit package when a command is not declared in the local manifest:

```bash
binpm x --package github:BurntSushi/ripgrep rg --version
```

For packages where the repository name is the command, the shorter explicit-source form is available:

```bash
binpm x --package github:owner/tool
```

Use `--bin <upstream-binary>` when the upstream executable or archive member has a different basename. The omitted-`CMD` shortcut does not forward args; provide an explicit `CMD` when you need to pass args, for example `binpm x --package <source> <cmd> -- <args...>`. binpm does not infer a GitHub repository from the command name. If `CMD` is missing and `--package` is not supplied, the command fails with a clear hint to use `binpm add <cmd> <source>` or `binpm x --package <source> <cmd>`.

## Frozen Lockfiles

Local `binpm install`, `binpm update`, and `binpm x` honor `--frozen-lockfile`. `CI=true` enables frozen behavior by default, and `--no-frozen-lockfile` is the explicit local-development escape hatch.

Frozen failures explain the mode, missing or stale `binpm.lock` file or target record, any `binpm x` on-demand install attempt, the exact lockfile path that would change, and the safest next command. In CI this usually means running `binpm install --local` or `binpm update --local <cmd>` locally and committing `binpm.lock`.
