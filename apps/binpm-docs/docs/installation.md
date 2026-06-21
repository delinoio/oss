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

On POSIX hosts, archive installs write the selected binary with executable permissions. Zip archives that omit Unix executable metadata can still install when the expected binary name and target-aware path tokens identify exactly one member; otherwise binpm fails and asks for an explicit `bin` value instead of guessing.

## Global Home

Global binpm state uses `~/.binpm`:

- `~/.binpm/bin`: globally installed executable links or copies.
- `~/.binpm/packages`: global installed package records.
- `~/.binpm/cache`: user-level asset cache.
- `~/.binpm/tmp`: temporary downloads and extraction roots.

## Security Boundary

binpm uses HTTPS source-provider APIs and release asset URLs. Persisted URLs in lockfiles, cache metadata, diagnostics, errors, and logs must be sanitized by removing query strings and fragments.

When strict verification is requested, `--require-verified` and `binpm verify --require-verified` must fail unless a provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature under a documented trust policy is available.
