# Install Scripts

Cross-platform install scripts with the shared interface:

- `--version <semver|latest>`
- `--method package-manager|direct`
- `direct` verifies `SHA256SUMS` plus Sigstore bundle sidecars (`*.sigstore.json`)
- `direct` requires `cosign` for Sigstore bundle verification
- Older releases that only published legacy `.sig`/`.pem` files are not supported in direct mode
- PowerShell installers default to `-Method direct` on Windows hosts
- `cargo-mono` accepts `package-manager` only as a compatibility alias and maps it to `direct`

Scripts:

- `cargo-mono.sh` / `cargo-mono.ps1`
- `nodeup.sh` / `nodeup.ps1`
- `with-watch.sh` / `with-watch.ps1`
- `derun.sh` / `derun.ps1`
- `dexdex-stack.sh` / `dexdex-stack.ps1`
