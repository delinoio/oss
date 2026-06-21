# Releases and Artifacts

Nodeup releases provide prebuilt CLI downloads, checksums, and Sigstore verification material.

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

Each download can be checked against `SHA256SUMS` and Sigstore verification material. Direct installers require releases that include this verification material.

## Direct Installer Verification

Direct installers verify:

1. The selected artifact's `SHA256SUMS` entry.
2. The artifact's Sigstore bundle with `cosign`.

Direct installers require `cosign` and support bundle-enabled releases only. Missing `cosign` is reported as a prerequisite failure before artifact download. If verification material is missing or verification fails, installation stops before the binary is installed with a distinct verification or release-material error.

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
NODEUP_INDEX_URL=https://mirror.example/index.json
NODEUP_DOWNLOAD_BASE_URL=https://mirror.example/release
NODEUP_RELEASE_INDEX_TTL_SECONDS=300
```

URL diagnostics in errors omit query strings and fragments.
