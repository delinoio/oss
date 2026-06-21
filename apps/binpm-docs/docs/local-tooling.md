# Local Tooling

binpm local tooling is anchored by committed manifest and lockfile documents at the repository root.

## Manifest

`binpm.toml` is the committed project-local tool declaration file.

```toml
version = 1

[tools.rg]
source = "github:BurntSushi/ripgrep"
version = "14.1.1"
bin = "rg"
```

Tool command names must be executable basenames. Path separators, `.` and `..` are invalid command names.

Target-specific asset overrides use `[tools.<cmd>.targets.<target-key>]`.

## Lockfile

`binpm.lock` is the committed deterministic project-local resolution file. It records release tags, target-specific assets, selected binaries, checksums, and installed paths.

Lockfile records must not include install timestamps, last-used timestamps, absolute cache paths, or other machine-local operational metadata.

Committed lockfiles must store sanitized canonical asset URLs only. They must never store query strings, fragments, credential-bearing URLs, or expiring signed download URLs.

## Local Paths

Project-local executable files are installed under:

```text
$repoRoot/.binpm/bin
```

Other project-local runtime state stays under `$repoRoot/.binpm`.
