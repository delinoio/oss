# Releases and Artifacts

binpm releases provide prebuilt CLI downloads, checksums, and Sigstore verification material.

## Tag Contract

Release tags use:

```text
binpm@v<semver>
```

## binpm CLI Artifacts

Release downloads are provided for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Each release includes standalone prebuilt binaries, archive assets, `SHA256SUMS`, and Sigstore bundle sidecars (`*.sigstore.json`) for each artifact. Direct installers require releases that include this verification material.

## Direct Installer Verification

Direct installers verify:

1. The selected artifact's `SHA256SUMS` entry.
2. The artifact's Sigstore bundle with `cosign`.

Direct installers require `cosign` and support bundle-enabled releases only. If verification material is missing or verification fails, installation stops before the binary is installed.

## Homebrew and cargo-binstall

Homebrew installation consumes prebuilt release archives for:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`

`cargo-binstall` metadata resolves only first-party GitHub Release assets. Quick-install and compile fallback strategies are disabled.

## Package Verification Boundary

binpm release artifact verification applies to the `binpm` binary itself. It does not imply that binpm package installs have signature verification beyond the package verification contract documented in [Cache and Verification](/cache-and-verification).
