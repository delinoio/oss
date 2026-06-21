# Installation

Nodeup is distributed as first-party release artifacts. Install flows are designed for supported macOS, Linux, and Windows x64/arm64 hosts.

This page is published at https://nodeup.delino.io/installation.

## binpm

Use [binpm](https://binpm.delino.io) to install Nodeup from the first-party `delinoio/oss` release asset for a pinned Nodeup release tag:

macOS and Linux:

```bash
NODEUP_BINPM_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/nodeup-binpm"
mkdir -p "$NODEUP_BINPM_HOME"
cd "$NODEUP_BINPM_HOME"
[ -f binpm.toml ] || binpm init
binpm add nodeup github:delinoio/oss@nodeup@v<semver>
binpm env --shell <shell>
```

Windows PowerShell:

```powershell
$env:NODEUP_BINPM_HOME = Join-Path ${env:LOCALAPPDATA} "nodeup-binpm"
New-Item -ItemType Directory -Force -Path $env:NODEUP_BINPM_HOME | Out-Null
Set-Location $env:NODEUP_BINPM_HOME
if (-not (Test-Path -LiteralPath "binpm.toml")) { binpm init }
binpm add nodeup github:delinoio/oss@nodeup@v<semver>
binpm env --shell powershell
```

Replace `<semver>` with the Nodeup release version to install. Nodeup release tags use `nodeup@v<semver>`. The `NODEUP_BINPM_HOME` directory keeps binpm's local manifest and `nodeup` binary out of unrelated project worktrees. In POSIX shells, replace `<shell>` with `bash`, `zsh`, or `fish`. Apply the printed environment command before verifying the install.

## Homebrew

On macOS and Linux:

```bash
brew install delinoio/tap/nodeup
```

The Homebrew formula uses prebuilt Nodeup release archives for:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`

## Direct Installers

The repository maintains direct installers at:

- `scripts/install/nodeup.sh`
- `scripts/install/nodeup.ps1`

Direct installers verify `SHA256SUMS` entries and Sigstore bundle sidecars (`*.sigstore.json`) with `cosign`. They support bundle-enabled releases only.

macOS and Linux:

```bash
./scripts/install/nodeup.sh --version latest --method direct
```

Windows PowerShell:

```powershell
./scripts/install/nodeup.ps1 -Version latest -Method direct
```

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
cargo binstall nodeup --no-confirm
```

Nodeup's `cargo-binstall` metadata resolves first-party GitHub Release assets only. Third-party quick-install and compile fallback strategies are disabled by contract.

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
RUST_LOG=off nodeup completions bash >/tmp/nodeup.bash
```

`nodeup show home` verifies that the binary can initialize Nodeup's local directory layout. `nodeup completions` verifies CLI parsing without requiring a Node.js runtime. `RUST_LOG=off` keeps redirected completion scripts free of human-mode log lines.

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

x86 hosts are unsupported.

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

The release index cache TTL defaults to 600 seconds and can be changed with `NODEUP_RELEASE_INDEX_TTL_SECONDS`.
