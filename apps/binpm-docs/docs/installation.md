# Installation

binpm installs native binary tools without requiring Node.js, npm, pnpm, yarn, or Bun.

## Supported Release Assets

binpm can install release assets distributed as:

- Bare executable assets.
- `.tar.gz` and `.tgz` archives.
- `.tar.xz` and `.txz` archives.
- `.tar.zst` archives.
- `.zip` archives.

## Global Home

Global installs use `~/.binpm`:

- `~/.binpm/bin`: globally installed executable links or copies.
- `~/.binpm/cache`: user-level asset cache.

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

binpm uses HTTPS source-provider APIs and release asset URLs. Stored URLs are sanitized so query strings, fragments, credentials, and expiring signed URL details are not written into project files.

When strict verification is requested, `--require-verified` and `binpm verify --require-verified` fail unless a provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature is available.
