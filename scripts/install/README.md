# Install Scripts

Cross-platform install scripts with the shared interface:

- `--version <semver|latest>`
- `--method package-manager|direct`
- `direct` verifies `SHA256SUMS` plus Sigstore bundle sidecars (`*.sigstore.json`)
- `direct` requires [`cosign`](https://docs.sigstore.dev/cosign/system_config/installation/) for Sigstore bundle verification and fails before release artifact download when `cosign` is not on `PATH`
- Missing `cosign` is a prerequisite failure; checksum mismatch or `cosign verify-blob` failure is a verification failure
- Older releases that only published legacy `.sig`/`.pem` files are not supported in direct mode
- PowerShell installers default to `-Method direct` on Windows hosts
- `cargo-mono` accepts `package-manager` only as a compatibility alias and maps it to `direct`
- Public binpm docs must show latest and pinned first-party raw GitHub installer command patterns before maintainer checkout commands.

Scripts:

- `binpm.sh` / `binpm.ps1`
- `cargo-mono.sh` / `cargo-mono.ps1`
- `nodeup.sh` / `nodeup.ps1`
- `with-watch.sh` / `with-watch.ps1`
- `derun.sh` / `derun.ps1`
