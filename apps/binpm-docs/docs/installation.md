# Installation

binpm is implemented as a Rust CLI under `crates/binpm`. It must not require Node.js, npm, pnpm, yarn, or Bun to install native binary tools.

## Current Scope

The documentation app records the user-facing contract for existing and planned binpm behavior. It does not add new runtime installation paths or release automation.

The current runtime supports documented release asset install flows and archive extraction for:

- Bare executable assets.
- `.tar.gz` and `.tgz` archives.
- `.tar.xz` and `.txz` archives.
- `.tar.zst` archives.
- `.zip` archives.

## Global Home

Global binpm state uses `~/.binpm`:

- `~/.binpm/bin`: globally installed executable links or copies.
- `~/.binpm/packages`: global installed package records.
- `~/.binpm/cache`: user-level asset cache.
- `~/.binpm/tmp`: temporary downloads and extraction roots.

## PATH Setup

Global installs place executables under `~/.binpm/bin`. When that directory is not on `PATH`, global install output and `binpm doctor` print guided setup messaging.

Use `binpm env` to print shell-specific PATH commands:

```bash
binpm env --shell bash
binpm env --shell zsh
binpm env --shell fish
binpm env --shell powershell
```

Supported `--shell` values are `bash`, `zsh`, `fish`, and `powershell`. `PowerShell` is accepted case-insensitively. `cmd` is recognized but explicitly deferred and returns an unsupported-shell diagnostic.

binpm does not edit shell profile files from these commands. Persistent profile changes are opt-in: add only the printed global bin command to your shell profile when you want global installs to persist on `PATH`. The printed project-local command is for the current project or shell session only.

## Security Boundary

binpm uses HTTPS source-provider APIs and release asset URLs. Persisted URLs in lockfiles, cache metadata, diagnostics, errors, and logs must be sanitized by removing query strings and fragments.

When strict verification is requested, `--require-verified` and `binpm verify --require-verified` must fail unless a provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature under a documented trust policy is available.
