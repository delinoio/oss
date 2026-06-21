# Installation

Nodeup is distributed as first-party release artifacts. Install flows are designed for macOS x64, macOS arm64, Linux x64, Linux arm64, Windows x64, and Windows arm64 hosts.

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

Direct installers detect unsupported x86 hosts before resolving release tags or downloading assets. Use an x64/arm64 host or a supported CI image when an installer reports an unsupported host.

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

x86 hosts are unsupported. Runtime installation and shim dispatch fail with `unsupported-platform` before archive download or delegated command planning. JSON errors include deterministic diagnostics: `os`, `architecture`, `platform_source`, optional `forced_platform`, and `supported_platforms`.

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
