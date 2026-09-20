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

## Linux APT and DNF

Runmoor is available through the separately enabled Delino preview repository. Installation does not register or start a service. Packages support x86-64 and ARM64 on Ubuntu 22.04/24.04/26.04 LTS, Debian 12/13, Fedora 43/44, and RHEL-compatible 9/10 systems, including UBI, Rocky Linux and AlmaLinux.

The repository address is `https://pkgs.oss.delino.io`. Verify its public RSA 4096 key before registering it:

```sh
curl -fsSLo delino-packages.asc https://pkgs.oss.delino.io/keys/delino-packages.asc
gpg --show-keys --with-fingerprint delino-packages.asc
```

The full primary fingerprint must match `B08D E37A 14DD 10DD FD04 E66E 87CB 82A1 F70F BD30`. Stop on a mismatch.

Follow the [Linux package setup guide](https://oss.delino.io/linux-packages) to register preview with your package manager. After registration:

```sh
# APT
sudo apt-get install runmoor
sudo apt-get install --only-upgrade runmoor
sudo apt-get remove runmoor

# DNF
sudo dnf install runmoor
sudo dnf upgrade runmoor
sudo dnf remove runmoor
```

Use `runmoor version` to check the installed CLI. Package removal preserves user configuration and data.
