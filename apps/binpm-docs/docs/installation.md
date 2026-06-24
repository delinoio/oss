# Installation

binpm is distributed as first-party release artifacts. Install flows are designed for macOS x64, macOS arm64, Linux x64, Linux arm64, Windows x64, and Windows arm64 hosts.

These first-party binpm release platforms are narrower than the target tokens binpm understands when it scores third-party release assets. Values such as `freebsd`, `i686`, and `armv7` can appear in target parsing and local override contracts, but the prebuilt binpm installer channels publish only the platforms listed on this page. Build from source when you need to run binpm itself outside the first-party release matrix.

## Homebrew

On macOS and Linux:

```bash
brew install delinoio/tap/binpm
```

The Homebrew formula uses prebuilt binpm release archives for:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`

Homebrew is a prebuilt-only channel for binpm. The formula does not build binpm from source when a platform-specific archive is unavailable. If Homebrew reports an unsupported platform or a missing archive, use a supported macOS/Linux host, the direct installer on a supported host, `cargo binstall`, or a source build.

## Direct Installers

Direct installers are for users who want a release artifact without Homebrew or `cargo-binstall`. They verify the selected artifact against its `SHA256SUMS` entry before installation. If you prefer a managed channel instead, use [Homebrew](https://brew.sh/) or [`cargo-binstall`](https://github.com/cargo-bins/cargo-binstall).

Use the short docs-site URLs for interactive installs where you want the current public first-party installer script. These entrypoints are served from `https://binpm.delino.io` and are backed by the same maintained installer scripts as the repository paths.

macOS and Linux:

```bash
(
  installer_url="https://binpm.delino.io/install.sh"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT
  if ! curl -fsSL "$installer_url" -o "$tmp_dir/binpm.sh"; then
    exit 1
  fi
  bash "$tmp_dir/binpm.sh" --version latest --method direct
)
```

Windows PowerShell:

```powershell
$InstallerUrl = "https://binpm.delino.io/install.ps1"
$Installer = Join-Path ([System.IO.Path]::GetTempPath()) ("binpm-install-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")
try {
  Invoke-WebRequest -Uri $InstallerUrl -OutFile $Installer -UseBasicParsing
  Unblock-File -LiteralPath $Installer -ErrorAction SilentlyContinue
  $PowerShell = (Get-Process -Id $PID).Path
  & $PowerShell -NoProfile -ExecutionPolicy Bypass -File $Installer -Version latest -Method direct
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
}
finally {
  Remove-Item -LiteralPath $Installer -Force -ErrorAction SilentlyContinue
}
```

The raw GitHub current installer URLs remain available when you want to fetch directly from the `delinoio/oss` repository.

macOS and Linux raw GitHub current installer:

```bash
(
  installer_url="https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/binpm.sh"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT
  if ! curl -fsSL "$installer_url" -o "$tmp_dir/binpm.sh"; then
    exit 1
  fi
  bash "$tmp_dir/binpm.sh" --version latest --method direct
)
```

Windows PowerShell raw GitHub current installer:

```powershell
$InstallerUrl = "https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/binpm.ps1"
$Installer = Join-Path ([System.IO.Path]::GetTempPath()) ("binpm-install-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")
try {
  Invoke-WebRequest -Uri $InstallerUrl -OutFile $Installer -UseBasicParsing
  Unblock-File -LiteralPath $Installer -ErrorAction SilentlyContinue
  $PowerShell = (Get-Process -Id $PID).Path
  & $PowerShell -NoProfile -ExecutionPolicy Bypass -File $Installer -Version latest -Method direct
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
}
finally {
  Remove-Item -LiteralPath $Installer -Force -ErrorAction SilentlyContinue
}
```

Use the pinned commands for automation after reviewing a repository tag or commit. Pinned examples use raw GitHub paths because the short docs-site URLs always represent the currently deployed public installer entrypoints.

Pinned macOS and Linux pattern:

```bash
(
  installer_ref="binpm@v0.1.0"
  binpm_version="0.1.0"
  installer_url="https://raw.githubusercontent.com/delinoio/oss/refs/tags/${installer_ref}/scripts/install/binpm.sh"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT
  if ! curl -fsSL "$installer_url" -o "$tmp_dir/binpm.sh"; then
    exit 1
  fi
  bash "$tmp_dir/binpm.sh" --version "$binpm_version" --method direct
)
```

Pinned Windows PowerShell pattern:

```powershell
$InstallerRef = "binpm@v0.1.0"
$BinpmVersion = "0.1.0"
$InstallerUrl = "https://raw.githubusercontent.com/delinoio/oss/refs/tags/$InstallerRef/scripts/install/binpm.ps1"
$Installer = Join-Path ([System.IO.Path]::GetTempPath()) ("binpm-install-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")
try {
  Invoke-WebRequest -Uri $InstallerUrl -OutFile $Installer -UseBasicParsing
  Unblock-File -LiteralPath $Installer -ErrorAction SilentlyContinue
  $PowerShell = (Get-Process -Id $PID).Path
  & $PowerShell -NoProfile -ExecutionPolicy Bypass -File $Installer -Version $BinpmVersion -Method direct
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
}
finally {
  Remove-Item -LiteralPath $Installer -Force -ErrorAction SilentlyContinue
}
```

These commands fetch first-party installer scripts from `delinoio/oss`. For reproducible automation, pin raw URL paths to a reviewed commit or repository tag and replace `latest` with an explicit binpm semver.

The canonical in-repo installer paths remain:

- `scripts/install/binpm.sh`
- `scripts/install/binpm.ps1`

From a repository checkout, maintainers can run the scripts directly:

```bash
bash ./scripts/install/binpm.sh --version latest --method direct
```

```powershell
./scripts/install/binpm.ps1 -Version latest -Method direct
```

Direct installers detect unsupported hosts before resolving release tags or downloading assets. Unsupported-host output reports the detected OS and architecture, lists the supported direct-install targets, and says that no first-party binpm direct installer artifact is published for that detected host. This is a direct-installer artifact boundary, not a statement that binpm cannot ever run there. Use an x64/arm64 host or supported CI image for direct install, use Homebrew or `cargo-binstall` where they support your host, or build binpm from source.

Supported direct-install targets are:

- `darwin/amd64` (macOS x64)
- `darwin/arm64` (macOS arm64)
- `linux/amd64` (Linux x64)
- `linux/arm64` (Linux arm64)
- `windows/amd64` (Windows x64)
- `windows/arm64` (Windows arm64)

Direct installers place the binary in `~/.local/bin` by default and do not modify your shell `PATH`. Add that directory before verifying the install, or pass `--install-dir` / `-InstallDir` with a directory already on `PATH`.

macOS and Linux:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Windows PowerShell:

```powershell
$env:Path = "$HOME\.local\bin;$env:Path"
```

## cargo-binstall

```bash
cargo binstall binpm --no-confirm
```

binpm's `cargo-binstall` metadata resolves first-party GitHub Release assets only. Third-party quick-install and compile fallback strategies are disabled by contract, so unsupported platforms fail instead of compiling from source or using third-party binary indexes. This keeps the install path on binpm-owned release assets; use Homebrew, a direct installer on a supported host, or a source build when a matching cargo-binstall asset is not available.

## GitHub Actions

```yaml
- uses: taiki-e/install-action@v2
  with:
    tool: cargo-binstall
- run: cargo binstall binpm --no-confirm
```

## Verify the Install

Run these commands in a shell where `binpm` resolves on `PATH`:

```bash
binpm --version
binpm doctor
binpm env --shell bash
```

`binpm doctor` verifies that the binary can inspect local and global binpm state. `binpm env --shell bash` verifies shell-specific PATH command generation without mutating shell profile files.

## Supported Release Assets

binpm can install release assets distributed as:

- Bare executable assets.
- `.tar.gz` and `.tgz` archives.
- `.tar.xz` and `.txz` archives.
- `.tar.zst` archives.
- `.zip` archives.

On POSIX hosts, archive installs write the selected binary with executable permissions. Zip archives that omit Unix executable metadata can still install when the expected binary name and target-aware path tokens identify exactly one member; otherwise binpm fails and asks for an explicit `bin` value instead of guessing.

## Global Home

Global installs use `~/.binpm`:

- `~/.binpm/bin`: globally installed executable links or copies.
- `~/.binpm/packages`: global installed package records.
- `~/.binpm/cache`: user-level asset cache.

## PATH Setup

Global installs place executables under `~/.binpm/bin`. When that directory is not on `PATH`, global install output and `binpm doctor` print guided setup messaging.

Use `binpm env` to print shell-specific PATH commands:

```bash
binpm env --shell bash
binpm env --shell zsh
binpm env --shell fish
binpm env --shell powershell
binpm env --shell pwsh
```

Supported `--shell` values are `bash`, `zsh`, `fish`, `powershell`, and `pwsh`. `PowerShell` is accepted case-insensitively. Use `--shell powershell` for Windows PowerShell and `--shell pwsh` for PowerShell 7 so setup targets the profile that shell loads. You may omit `--shell` when `SHELL` or `ComSpec` identifies a supported shell. `cmd` is recognized but explicitly deferred and returns an unsupported-shell diagnostic with cmd.exe PATH guidance.

binpm does not edit shell profile files from install, add, doctor, or plain env output. Persistent profile changes are opt-in: use `binpm env --global --shell <shell>` for a non-mutating global PATH command, or preview the exact profile update with `binpm env setup --shell <shell> --dry-run` before applying it with `binpm env setup --shell <shell>`. PowerShell 7 users should pass `--shell pwsh` for setup. Setup appends only the global bin PATH line and prints rollback guidance. The printed project-local command is for the current project or shell session only; `binpm env --local --shell <shell>` prints only that session command.

For cmd.exe, run `binpm env --global --shell cmd` to print the resolved global bin path, including any absolute `BINPM_HOME` override. Use the printed `set "PATH=<global-bin>;%PATH%"` command for the current session, or add that global bin path to the user PATH in Windows Environment Variables for persistent setup.

## Security Boundary

Release installers verify binpm's own published release artifacts. That release verification is separate from binpm's package-asset verification for tools installed by binpm.

binpm package installs use HTTPS source-provider APIs and release asset URLs. Stored URLs are sanitized so query strings, fragments, credentials, and expiring signed URL details are not written into project files.

When strict verification is requested for installed tools, `--require-verified` and `binpm verify --require-verified` fail unless a trusted provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified package signature is available. Package signature verification is separate from release-installer verification for binpm itself: binpm currently supports GitHub.com Sigstore bundle sidecars named `<selected-asset>.sigstore.json` when `cosign verify-blob --bundle` validates the selected asset for the same repository and release tag. Raw signature sidecars do not satisfy strict verification by presence alone.
