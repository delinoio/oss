# Installation and verification

Install only after a fully validated [Runlens release](https://github.com/delinoio/oss/releases) is available. The initial version is 0.1.0; public validation is currently incomplete. Verify your [platform prerequisites](/platforms) first.

## POSIX installer

Install a separately trusted `cosign`, then download the installer from a reviewed release/source revision or the published documentation site. The installer verifies the exact release workflow identity, OIDC issuer, Sigstore bundles, and SHA256 material before replacement.

```sh
curl --fail --location https://runlens.delino.io/install.sh -o install-runlens.sh
sh install-runlens.sh --version 0.1.0
```

The default destination is `$HOME/.local/bin/runlens`; choose a different directory with `--install-dir`. An explicit version is required so installation and rollback select exactly the reviewed release. x64 artifacts use `amd64` in archive names.

## PowerShell installer

```powershell
Invoke-WebRequest https://runlens.delino.io/install.ps1 -OutFile install-runlens.ps1
./install-runlens.ps1 -Version 0.1.0
```

The default destination is `$HOME/.local/bin/runlens.exe`; `-InstallDir` selects another directory. A separately trusted `cosign` is required. Signature and checksum mismatches fail without replacing the installed executable.

## Homebrew

```sh
brew install delinoio/tap/runlens
```

The formula uses verified prebuilt artifacts for supported macOS/Linux x64 and arm64 hosts. It has no source-build fallback. Exact-version installation and rollback use the direct installers or manually verified artifacts.

## Manual verification

Download the matching archive, `SHA256SUMS`, and their `.sigstore.json` bundles from the exact `runlens@v0.1.0` release. Verify the checksum manifest and archive with `cosign verify-blob`, requiring issuer `https://token.actions.githubusercontent.com` and the exact Runlens release workflow certificate identity recorded for that release. Check the archive's SHA256 against the verified manifest before extracting the single executable and license notices.

Run `runlens --version` and `runlens doctor` after installation. Sigstore verification does not imply Apple notarization or Windows Authenticode. There is no crates.io/npm package or automatic update service.


For offline installation, copy the platform archive, its `.sigstore.json` bundle,
`SHA256SUMS`, and `SHA256SUMS.sigstore.json` from the same release into one directory.
Pass `--archive-dir DIRECTORY` to the POSIX installer or `-ArchiveDir DIRECTORY` to
the PowerShell installer. Offline archives pass the same version-bound Sigstore
and checksum checks; there is no unsigned or skip-verification mode. Verification
may need Sigstore service access according to your installed cosign version.
