# Installation

The only executable is `ach`. Supported build targets are macOS 13+, Windows 10 22H2+, and Ubuntu 22.04 LTS+, each x64 and arm64. Install Git first. Direct installers additionally require [Sigstore cosign](https://docs.sigstore.dev/cosign/system_config/installation/) for release authenticity verification.

```
curl -fsSLo install-ach.sh https://ach.delino.io/install.sh
sh install-ach.sh --version 0.1.0

# macOS and Linux, using the Homebrew tap:
brew install delinoio/tap/async-commit-hook
```

```
# PowerShell:
Invoke-WebRequest https://ach.delino.io/install.ps1 -OutFile install-ach.ps1
./install-ach.ps1 -Version 0.1.0
```

The shell installer's `--version` option selects the exact release and overrides `ACH_VERSION`. Without either value, it uses the installer's bundled version. Use a three-part version such as `0.1.0`; invalid or unknown arguments stop installation before downloading.

Download the platform archive, SHA256SUMS and Sigstore bundles from the versioned GitHub release if installing manually. Verify the signed checksum manifest and archive before extraction. The expected OIDC issuer is `https://token.actions.githubusercontent.com`; the signing identity is `https://github.com/delinoio/oss/.github/workflows/release-async-commit-hook.yml@refs/heads/main`. Archive names are `ach-<darwin|linux|windows>-<amd64|arm64>.tar.gz` (Windows: `.zip`).

The initial release and public deployment have not been performed as part of implementation. These commands target the prepared release destinations; use them once a supported release is published.
