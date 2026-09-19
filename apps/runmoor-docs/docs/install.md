# Install and Verify Runmoor

Download the matching `runmoor-darwin-arm64.tar.gz`, `runmoor-linux-amd64.tar.gz` or `runmoor-linux-arm64.tar.gz` from a [`runmoor@v…` prerelease](https://github.com/delinoio/oss/releases). Download `SHA256SUMS` and the `.sigstore.json` bundles alongside it. A publication dry-run archive is unsigned and is not a public release.

Verify with a separately installed cosign before extraction. For example, for version `0.1.0` on an Apple Silicon Mac:

```sh
cosign verify-blob \
  --bundle runmoor-darwin-arm64.tar.gz.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/delinoio/oss/\.github/workflows/release-runmoor\.yml@refs/(heads/main|tags/runmoor@v0\.1\.0)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  runmoor-darwin-arm64.tar.gz
cosign verify-blob \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/delinoio/oss/\.github/workflows/release-runmoor\.yml@refs/(heads/main|tags/runmoor@v0\.1\.0)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
shasum -a 256 runmoor-darwin-arm64.tar.gz
tar -xzf runmoor-darwin-arm64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 755 runmoor "$HOME/.local/bin/runmoor"
runmoor version
```

Compare the printed hash with the exact archive entry in the verified `SHA256SUMS`. Linux may use `sha256sum` instead. Add your user binary directory to PATH yourself. No Homebrew package, automatic update, system-level service, or bundled Docker/Tart is installed.
