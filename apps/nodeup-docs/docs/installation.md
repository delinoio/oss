# Installation

Nodeup is distributed as first-party release artifacts. Install flows are designed for macOS x64, macOS arm64, Linux x64, Linux arm64, Windows x64, and Windows arm64 hosts.

Release asset names use `amd64` for the same 64-bit Intel/AMD CPU family that many user-facing docs and tools call `x64`. For example, a Linux x64 machine uses the `linux/amd64` Nodeup release asset.

## Choose an Install Method

For most macOS and Linux users, start with Homebrew. For Windows users, start with the direct installer.

| Method | Use this when |
| --- | --- |
| Homebrew | You are on macOS or Linux and already use Homebrew, or you want the simplest managed install path. |
| Direct installers | You want a first-party release artifact without Homebrew or `cargo-binstall`. Use pinned commands in CI or audited environments. |
| `cargo-binstall` | You already have Rust tooling and want to install from first-party GitHub Release assets without source-build fallback. |
| binpm | You want a global CLI install managed by binpm, or you need a project-local manifest for reproducible per-repo tooling. |

## binpm

Install binpm by following the [binpm installation docs](https://binpm.delino.io/installation).

For Nodeup itself, the normal path is a global CLI install:

```bash
binpm install github:delinoio/oss@nodeup@v<semver> --as nodeup
```

If `~/.binpm/bin` is not already on `PATH`, add the global binpm environment first.
macOS and Linux:

```bash
eval "$(binpm env --global --shell bash)"
```

PowerShell:

```powershell
binpm env --global --shell powershell | Invoke-Expression
```

Use a dedicated or project-local manifest only when you want Nodeup pinned as part of a repository's committed tooling, or when you are deliberately managing a separate binpm home for automation.

Replace `<semver>` with the Nodeup release version to install. Nodeup release tags use `nodeup@v<semver>`.

If you need Nodeup managed as part of a repository manifest, create or update that repository's `binpm.toml` with `binpm add nodeup github:delinoio/oss@nodeup@v<semver>`, then install it locally with `binpm install`. That path is useful for pinned team environments, but it is not the default install path for a standalone CLI.

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

Direct installers are for users who want a release artifact without Homebrew or `cargo-binstall`. They verify the selected artifact against its `SHA256SUMS` entry before installation. If you do not want to manage a direct release artifact yourself, use Homebrew or `cargo-binstall` instead.

macOS and Linux short URL:

```bash
curl -fsSL https://nodeup.delino.io/install.sh | bash -s -- --version latest --method direct
```

Windows PowerShell short URL:

```powershell
$InstallerUrl = "https://nodeup.delino.io/install.ps1"
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

The short URLs are public Nodeup docs-site entrypoints backed by the canonical installer scripts in `delinoio/oss`.

Raw GitHub current installer commands are also supported.

macOS and Linux raw GitHub:

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

Windows PowerShell raw GitHub:

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

These raw GitHub commands fetch the current first-party installer scripts from `delinoio/oss`.

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

Direct installers require the selected release to include `SHA256SUMS` and the selected artifact. If a release is missing either one, use a newer release, Homebrew on macOS/Linux when available, or `cargo-binstall` on supported hosts with complete first-party assets.

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

Third-party quick-install and compile fallback strategies are disabled by contract. If the current host is unsupported or the matching first-party asset is missing from a release, `cargo-binstall` should fail instead of compiling from source or downloading a community-provided binary. Use Homebrew on macOS/Linux, the direct installer, or a supported x64/arm64 host with a complete Nodeup release.

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
