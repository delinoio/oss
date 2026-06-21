# Local Tooling

binpm local tooling is anchored by committed `binpm.toml` and `binpm.lock` files in your project.

## Manifest

`binpm.toml` is the committed project-local tool declaration file.

`binpm init` prints the full manifest destination before it writes. From a nested directory inside a Git worktree, that destination is the worktree root `binpm.toml`; outside Git, binpm uses the nearest existing manifest ancestor or the current directory. The current contract does not include a flag for forcing a different initialization root.

```toml
version = 1

[tools.rg]
source = "github:BurntSushi/ripgrep"
version = "14.1.1"
bin = "rg"
```

Tool command names are executable basenames. Path separators, `.` and `..` are invalid command names.

`bin` stores the selected upstream executable name or archive member path. Prefer setting it through the CLI:

```bash
binpm add rg github:BurntSushi/ripgrep@14.1.1 --bin rg
```

Use `--bin` when a package archive contains more than one plausible executable or when the executable selected from the release differs from the local command name.

Use `--manifest-only` when you only want to review or commit a declaration:

```bash
binpm add rg github:BurntSushi/ripgrep@14.1.1 --bin rg --manifest-only
```

Declaration-only add writes `binpm.toml` and skips release lookup, downloads, cache mutation, `binpm.lock`, package records, and `.binpm/bin`. Run `binpm install` later to resolve, lock, and install.

For multi-binary releases, use `--also <cmd=upstream-binary>` instead of hand-copying repeated source tables:

```bash
binpm add foo github:owner/tools@v1.2.3 --bin bin/foo --also bar=bin/bar
```

The manifest still keeps one `[tools.<cmd>]` table per command so each selected binary remains explicit.

Target-specific asset overrides use `[tools.<cmd>.targets.<target-key>]`.

## Lockfile

`binpm.lock` is the committed deterministic project-local resolution file. It records the selected release, target-specific asset, selected binary, checksum, and installed path needed to reproduce the tool install.

Lockfiles do not include install timestamps, last-used timestamps, absolute cache paths, or other machine-local operational metadata.

Committed lockfiles store sanitized asset URLs only. They do not store query strings, fragments, credential-bearing URLs, or expiring signed download URLs.

## Local Paths

Project-local executable files are installed under:

```text
<project>/.binpm/bin
```

After a normal `binpm add`, run local tools with `binpm x <cmd>`. Use `binpm env --shell <bash|zsh|fish|powershell>` only when you want opt-in direct shell access for the current project or session. binpm does not edit shell profile files.
