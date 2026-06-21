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

Direct installers require `cosign` and support bundle-enabled releases only. If verification material is missing or verification fails, installation stops before the binary is installed.

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
