# Releases and Artifacts

binpm releases provide prebuilt CLI downloads and checksums.

## Tag Contract

Release tags use:

```text
binpm@v<semver>
```

## binpm CLI Artifacts

First-party binpm release downloads are provided for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Each release includes standalone prebuilt binaries, archive assets, and `SHA256SUMS`. Direct installers require the selected artifact and `SHA256SUMS`.

This distribution matrix describes where the binpm binary itself is published. It is separate from binpm's target parsing support for third-party package resolution, which can recognize additional target values such as `freebsd`, `i686`, and `armv7` when scoring upstream release assets or rendering override snippets.

## Direct Installer Verification

Direct installers verify:

1. The selected artifact's `SHA256SUMS` entry.

If checksum material is missing or verification fails, installation stops before the binary is installed.

## Homebrew and cargo-binstall

Homebrew installation consumes prebuilt release archives for:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`

Homebrew is prebuilt-only for binpm; the formula does not compile from source when an archive is missing or the host platform is unsupported.

`cargo-binstall` metadata resolves only first-party GitHub Release assets. Quick-install and compile fallback strategies are disabled, so unsupported cargo-binstall platforms fail instead of using third-party binary indexes or source compilation.

## Package Verification Boundary

binpm release artifact verification applies to the `binpm` binary itself. It does not imply that binpm package installs have signature verification beyond the package verification contract documented in [Cache and Verification](/cache-and-verification).
