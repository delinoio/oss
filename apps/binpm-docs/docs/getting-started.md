# Getting Started

Start with a project-local tool declaration when you want reproducible commands in a repository.

```bash
binpm init
binpm add rg github:BurntSushi/ripgrep@14.1.1
binpm x rg --version
```

`binpm init` creates `binpm.toml` with `version = 1` when a manifest does not already exist. It prints the resolved full destination path before creation and prints the created manifest path after success. It does not install tools by default.

When you run `binpm init` from a nested directory, manifest creation uses the current Git worktree root when one is available. Outside Git, it uses the nearest ancestor that already contains `binpm.toml`, or the current directory when no ancestor manifest exists. Use `binpm init --manifest-path ./binpm.toml` when you explicitly want to create a new manifest at a different destination, including the current nested directory. The destination must be named `binpm.toml`, existing files are never overwritten, and `--force` is not supported.

`binpm add <cmd> <source>` declares a local command, installs the selected executable into `<project>/.binpm/bin`, and updates `binpm.lock`.

`binpm x CMD [args...]` resolves `CMD` from `binpm.toml`, installs on demand when allowed by the lockfile policy, prepends `<project>/.binpm/bin` to `PATH`, preserves the caller's working directory, and forwards arguments after `CMD`.

After `binpm add`, use `binpm x <cmd>` as the default local invocation path. `binpm env --local --shell <bash|zsh|fish|powershell>` prints an opt-in project PATH command when you want direct shell access; it does not edit shell profiles. Omit `--local` to print both global and project-local commands.

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

Local `binpm install`, `binpm update`, and `binpm x` honor `--frozen-lockfile`. `CI=true` enables frozen behavior by default, and `--no-frozen-lockfile` is the explicit local-development escape hatch. Frozen mode prevents lockfile creation or modification; it is reproducible, but it is not an offline or cache-only mode.

An empty `binpm.toml` has no tools to resolve. `binpm update --local --frozen-lockfile` succeeds for that no-op case without creating `binpm.lock` and reports that no tools are declared and no lockfile was created. JSON dry-run output exposes `no_op.reason = "empty_manifest_no_tools_no_lockfile_changes"`.

Frozen failures explain the mode, missing or stale `binpm.lock` file or target record, any `binpm x` on-demand install attempt, the exact lockfile path that would change, and the safest next command. In CI this usually means running `binpm install --local` or `binpm update --local <cmd>` locally and committing `binpm.lock`.

When a frozen local install or `binpm x` only needs to restore a missing project-local executable or package record, binpm first uses SHA-256-verified global cache bytes. If the cache entry is absent or corrupt, it may download the sanitized asset URL already stored in `binpm.lock`, validate the locked SHA-256, and restore from those bytes without provider release-list pagination. For private GitHub or GitLab releases, configure the host-scoped provider token in CI or pre-populate the global cache; binpm may attach that runtime-only token to same-origin locked provider URLs, but it never writes credentials or expiring signed URLs to `binpm.lock`.
