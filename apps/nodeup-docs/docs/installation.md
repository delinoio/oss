# Installation

Nodeup is distributed as first-party release artifacts. Install flows are designed for macOS x64, macOS arm64, Linux x64, Linux arm64, Windows x64, and Windows arm64 hosts.

Release asset names use `amd64` for the same 64-bit Intel/AMD CPU family that many user-facing docs and tools call `x64`. For example, a Linux x64 machine uses the `linux/amd64` Nodeup release asset.

## Choose an Install Method

For most macOS and Linux users, start with Homebrew. For Windows users, start with the direct installer after installing `cosign`.

| Method | Use this when |
| --- | --- |
| Homebrew | You are on macOS or Linux and already use Homebrew, or you want the simplest managed install path. |
| Direct installers | You want a first-party release artifact without Homebrew or `cargo-binstall`, and you can install `cosign` first. Use pinned commands in CI or audited environments. |
| `cargo-binstall` | You already have Rust tooling and want to install from first-party GitHub Release assets without source-build fallback. |
| binpm | You want Nodeup managed by a project-local or dedicated binpm manifest, usually because the rest of your tooling already uses binpm. |

## binpm

Install binpm by following the [binpm installation docs](https://binpm.delino.io/installation). Use a dedicated Nodeup binpm home so `binpm add` does not modify an unrelated project manifest.

macOS and Linux (bash/zsh):

```bash
NODEUP_BINPM_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/nodeup-binpm"
mkdir -p "$NODEUP_BINPM_HOME"
cd "$NODEUP_BINPM_HOME"
[ -f binpm.toml ] || binpm init
binpm add nodeup github:delinoio/oss@nodeup@v<semver>
eval "$(binpm env --shell bash)"
```

Windows PowerShell:

```powershell
$env:NODEUP_BINPM_HOME = Join-Path ${env:LOCALAPPDATA} "nodeup-binpm"
New-Item -ItemType Directory -Force -Path $env:NODEUP_BINPM_HOME | Out-Null
Set-Location $env:NODEUP_BINPM_HOME
if (-not (Test-Path -LiteralPath "binpm.toml")) { binpm init }
binpm add nodeup github:delinoio/oss@nodeup@v<semver>
binpm env --shell powershell | Invoke-Expression
```

Replace `<semver>` with the Nodeup release version to install. Nodeup release tags use `nodeup@v<semver>`.

## Homebrew

Use Homebrew when you are on macOS or Linux and want the shortest managed installation path.

On macOS and Linux:

```bash
brew install delinoio/tap/nodeup
```

The Homebrew formula uses prebuilt Nodeup release archives for:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`

Here, `amd64` means the x64 CPU family.

## Direct Installers

Direct installers are for users who want a release artifact without Homebrew or `cargo-binstall`. Install `cosign` first and leave it on `PATH`; the installers require it to verify `SHA256SUMS` entries and Sigstore bundle sidecars (`*.sigstore.json`) with `cosign verify-blob --bundle`. Missing `cosign` is a prerequisite failure, not a reason to disable verification. If you do not want to manage that prerequisite directly, use Homebrew or `cargo-binstall` instead.

Install `cosign`:

```bash
brew install cosign
```

On Linux without Homebrew, follow the [Sigstore cosign installation guide](https://docs.sigstore.dev/cosign/system_config/installation/). On Windows, use:

```powershell
winget install sigstore.cosign
# or
scoop install cosign
```

macOS and Linux:

```bash
(
  installer_url="https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/nodeup.sh"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT
  if ! curl -fsSL "$installer_url" -o "$tmp_dir/nodeup.sh"; then
    exit 1
  fi
  bash "$tmp_dir/nodeup.sh" --version latest --method direct
)
```

Windows PowerShell:

```powershell
$InstallerUrl = "https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/nodeup.ps1"
$Installer = Join-Path ([System.IO.Path]::GetTempPath()) ("nodeup-install-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")
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

These commands fetch the current first-party installer scripts from `delinoio/oss`.

Use pinned commands when reproducibility matters, especially in CI, bootstrap scripts, and audited environments. Pin the same first-party raw GitHub paths to a reviewed repository tag or commit, and replace `latest` with an explicit Nodeup semver.

macOS and Linux pinned pattern:

```bash
(
  installer_ref="refs/tags/nodeup@v<semver>"
  installer_url="https://raw.githubusercontent.com/delinoio/oss/${installer_ref}/scripts/install/nodeup.sh"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT
  if ! curl -fsSL "$installer_url" -o "$tmp_dir/nodeup.sh"; then
    exit 1
  fi
  bash "$tmp_dir/nodeup.sh" --version "<semver>" --method direct
)
```

Windows PowerShell pinned pattern:

```powershell
$InstallerRef = "refs/tags/nodeup@v<semver>"
$InstallerUrl = "https://raw.githubusercontent.com/delinoio/oss/$InstallerRef/scripts/install/nodeup.ps1"
$Installer = Join-Path ([System.IO.Path]::GetTempPath()) ("nodeup-install-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")
try {
  Invoke-WebRequest -Uri $InstallerUrl -OutFile $Installer -UseBasicParsing
  Unblock-File -LiteralPath $Installer -ErrorAction SilentlyContinue
  $PowerShell = (Get-Process -Id $PID).Path
  & $PowerShell -NoProfile -ExecutionPolicy Bypass -File $Installer -Version "<semver>" -Method direct
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
}
finally {
  Remove-Item -LiteralPath $Installer -Force -ErrorAction SilentlyContinue
}
```

The canonical in-repo installer paths remain:

- `scripts/install/nodeup.sh`
- `scripts/install/nodeup.ps1`

From a repository checkout, maintainers can run the scripts directly:

```bash
bash ./scripts/install/nodeup.sh --version latest --method direct
```

```powershell
./scripts/install/nodeup.ps1 -Version latest -Method direct
```

Direct installers detect unsupported x86 hosts before resolving release tags or downloading assets. Use an x64/arm64 host or a supported CI image when an installer reports an unsupported host.

Direct installers support bundle-enabled releases only. The selected release must include `SHA256SUMS`, the selected artifact, and the selected artifact's `<artifact>.sigstore.json` bundle sidecar. Legacy `.sig` or `.pem` sidecars are not enough for the direct installer. If an older release lacks bundle sidecars, use a newer bundle-enabled release, Homebrew on macOS/Linux when available, or `cargo-binstall` on supported hosts with complete first-party assets.

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

Use `cargo-binstall` when you already use Rust tooling and want a first-party GitHub Release asset without source-build fallback.

```bash
cargo binstall nodeup --no-confirm
```

Nodeup's `cargo-binstall` metadata resolves first-party GitHub Release assets only:

- `nodeup-linux-amd64.tar.gz`
- `nodeup-linux-arm64.tar.gz`
- `nodeup-darwin-amd64.tar.gz`
- `nodeup-darwin-arm64.tar.gz`
- `nodeup-windows-amd64.zip`
- `nodeup-windows-arm64.zip`

Third-party quick-install and compile fallback strategies are disabled by contract. If the current host is unsupported or the matching first-party asset is missing from a release, `cargo-binstall` should fail instead of compiling from source or downloading a community-provided binary. Use Homebrew on macOS/Linux, the direct installer with `cosign`, or a supported x64/arm64 host with a complete Nodeup release.

## GitHub Actions

```yaml
- uses: taiki-e/install-action@v2
  with:
    tool: cargo-binstall
- run: cargo binstall nodeup --no-confirm
```

## Verify the Install

Run these commands in a shell where `nodeup` resolves on `PATH`:

```bash
nodeup --version
nodeup show home
nodeup completions bash >/tmp/nodeup.bash
```

`nodeup show home` verifies that the binary can initialize Nodeup's local directory layout. `nodeup completions` verifies CLI parsing without requiring a Node.js runtime. Completion scripts are written to stdout, while logs are written to stderr when enabled.

## Supported Runtime Hosts

Nodeup runtime installation and shim dispatch support:

| Host | CPU | Runtime archive |
| --- | --- | --- |
| macOS | x64 | `node-v<version>-darwin-x64.tar.xz` |
| macOS | arm64 | `node-v<version>-darwin-arm64.tar.xz` |
| Linux | x64 | `node-v<version>-linux-x64.tar.xz` |
| Linux | arm64 | `node-v<version>-linux-arm64.tar.xz` |
| Windows | x64 | `node-v<version>-win-x64.zip` |
| Windows | arm64 | `node-v<version>-win-arm64.zip` |

x86 hosts are unsupported. Runtime installation and shim dispatch fail with `unsupported-platform` before archive download or delegated command planning. JSON errors include deterministic diagnostics: `os`, `architecture`, `platform_source`, and `supported_platforms`.

## Local Directories

Nodeup uses separate data, cache, and config roots. Override them with:

- `NODEUP_DATA_HOME`
- `NODEUP_CACHE_HOME`
- `NODEUP_CONFIG_HOME`

Defaults are XDG-style directories on macOS/Linux and AppData-style directories on Windows. `nodeup show home` prints the effective paths.

## Release Index and Mirrors

By default, Nodeup reads the Node.js release index from `https://nodejs.org/download/release/index.json` and downloads runtime archives from `https://nodejs.org/download/release`.

Mirror overrides:

- `NODEUP_INDEX_URL`
- `NODEUP_DOWNLOAD_BASE_URL`

When using a mirror, set both variables to matching release data:

```bash
NODEUP_INDEX_URL=https://mirror.example/download/release/index.json
NODEUP_DOWNLOAD_BASE_URL=https://mirror.example/download/release
```

Checksum mismatch and runtime download diagnostics include sanitized mirror source details when either override is set.

The release index cache TTL defaults to 600 seconds and can be changed with `NODEUP_RELEASE_INDEX_TTL_SECONDS`.
The value must be a non-negative integer number of seconds. Invalid values such as an empty string, `-1`, or `abc` keep the 600-second fallback and emit a safe diagnostic category without exposing the raw value.
