# Releases and Artifacts

Nodeup release automation publishes prebuilt binaries, compressed archives, checksums, and Sigstore bundle sidecars.

## Tag Contract

Release tags use:

```text
nodeup@v<semver>
```

## Nodeup CLI Artifacts

Each release must include standalone prebuilt binaries and compressed archives for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Each artifact has a Sigstore bundle sidecar named `<artifact>.sigstore.json`. Releases also include `SHA256SUMS` and `SHA256SUMS.sigstore.json`.

Legacy `.sig` and `.pem` sidecars are out of scope for direct installation.

## Direct Installer Verification

Direct installers verify:

1. The selected artifact's `SHA256SUMS` entry.
2. The artifact Sigstore bundle sidecar with `cosign verify-blob --bundle`.

Direct installers require `cosign` and support bundle-enabled releases only.

## Runtime Download Artifacts

Nodeup installs Node.js runtimes from Node.js release archives:

- macOS/Linux use `.tar.xz`.
- Windows uses `.zip`.
- Windows archives that unpack without a top-level directory are normalized into the stable `bin/` runtime layout.

Runtime archive integrity is verified against the upstream `SHASUMS256.txt` entry before extraction.

## Mirrors and Diagnostics

Use these environment variables for mirrors or testing:

```bash
NODEUP_INDEX_URL=https://mirror.example/index.json
NODEUP_DOWNLOAD_BASE_URL=https://mirror.example/release
NODEUP_RELEASE_INDEX_TTL_SECONDS=300
```

URL diagnostics in errors omit query strings and fragments.
