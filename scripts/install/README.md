# Install Scripts

Cross-platform install scripts with the shared interface:

- `--version <semver|latest>`
- `--method package-manager|direct`
- `direct` verifies `SHA256SUMS` plus Sigstore bundle sidecars (`*.sigstore.json`)
- Older releases that only published legacy `.sig`/`.pem` files are not supported in direct mode

Scripts:

- `nodeup.sh` / `nodeup.ps1`
- `derun.sh` / `derun.ps1`
- `dexdex-stack.sh` / `dexdex-stack.ps1`
