# Install Scripts

Cross-platform install scripts with the shared interface:

- `--version <semver|latest>`
- `--method package-manager|direct`
- `direct` verifies the selected artifact against `SHA256SUMS`
- Unsupported direct-installer hosts fail before release lookup or artifact download and must report detected OS/architecture, supported direct-install targets, and alternatives without printing release or artifact URLs
- PowerShell installers default to `-Method direct` on Windows hosts
- `cargo-mono` accepts `package-manager` only as a compatibility alias and maps it to `direct`
- Public binpm docs must show latest and pinned first-party raw GitHub installer command patterns before maintainer checkout commands.

Scripts:

- `binpm.sh` / `binpm.ps1`
- `cargo-mono.sh` / `cargo-mono.ps1`
- `nodeup.sh` / `nodeup.ps1`
- `with-watch.sh` / `with-watch.ps1`
- `derun.sh` / `derun.ps1`
