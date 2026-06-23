# Releases and Artifacts

Nodeup releases provide prebuilt CLI downloads, checksums, and Sigstore verification material.

Download Nodeup release artifacts from the [`delinoio/oss` GitHub Releases page](https://github.com/delinoio/oss/releases). Look for releases whose tag starts with `nodeup@v`, such as `nodeup@v<semver>`.

## Tag Contract

Release tags use:

```text
nodeup@v<semver>
```

## Nodeup CLI Artifacts

Release downloads are provided for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

In Nodeup release asset names, `amd64` means the same 64-bit Intel/AMD CPU family often called `x64` in operating-system and user-facing documentation. For example, Linux x64 hosts use `linux/amd64` assets, and Windows x64 hosts use `windows/amd64` assets.

Each download can be checked against `SHA256SUMS` and Sigstore verification material. Direct installers require releases that include this verification material.

## Direct Installer Verification

Direct installers verify:

1. The selected artifact's `SHA256SUMS` entry.
2. The artifact's Sigstore bundle with `cosign`.

Direct installers require `cosign` and support bundle-enabled releases only. Missing `cosign` is reported as a prerequisite failure before artifact download. If verification material is missing or verification fails, installation stops before the binary is installed with a distinct verification or release-material error.

A release is direct-installer compatible only when it includes `SHA256SUMS`, the selected artifact, and the selected artifact's `<artifact>.sigstore.json` bundle sidecar. Legacy `.sig` or `.pem` sidecars are not supported by the direct installer. For older releases that lack bundle sidecars, use a newer bundle-enabled release, Homebrew on macOS/Linux when available, or `cargo-binstall` on supported hosts with complete first-party assets.

## cargo-binstall Asset Contract

`cargo-binstall` uses the same first-party release assets listed above. Nodeup disables `quick-install` and `compile` fallback strategies so installation never silently switches to third-party binary discovery or a source build when a prebuilt asset is unavailable.

Supported `cargo-binstall` assets are:

- `nodeup-linux-amd64.tar.gz`
- `nodeup-linux-arm64.tar.gz`
- `nodeup-darwin-amd64.tar.gz`
- `nodeup-darwin-arm64.tar.gz`
- `nodeup-windows-amd64.zip`
- `nodeup-windows-arm64.zip`

If a host is unsupported or a release is missing the expected asset, use Homebrew on macOS/Linux, the direct installer with `cosign`, or a supported x64/arm64 host with a complete Nodeup release.

## Runtime Download Artifacts

Nodeup installs Node.js runtimes from Node.js release archives:

- macOS/Linux use `.tar.xz`.
- Windows uses `.zip`.
- Windows archives that unpack without a top-level directory are normalized into the stable `bin/` runtime layout.

Runtime archive integrity is verified against the upstream `SHASUMS256.txt` entry before extraction.

## Mirrors and Diagnostics

Use these environment variables for custom mirrors:

```bash
NODEUP_INDEX_URL=https://mirror.example/download/release/index.json
NODEUP_DOWNLOAD_BASE_URL=https://mirror.example/download/release
NODEUP_RELEASE_INDEX_TTL_SECONDS=300
```

Set both mirror variables together unless you intentionally mix sources. Checksum mismatch and runtime download errors include sanitized index and download-base diagnostics when a mirror override is configured, and URL diagnostics omit credentials, query strings, and fragments.
