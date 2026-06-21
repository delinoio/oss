# Local Tooling

binpm local tooling is anchored by committed `binpm.toml` and `binpm.lock` files in your project.

## Manifest

`binpm.toml` is the committed project-local tool declaration file.

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
