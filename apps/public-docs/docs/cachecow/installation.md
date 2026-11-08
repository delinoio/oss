# Installation and availability

> CacheCow 0.1.0 is published. Native CoW and process-lifecycle acceptance remain unverified; package installation does not establish native support or performance benefits.

## Release status

Version 0.1.0 is available as signed native archives on [GitHub Releases](https://github.com/delinoio/oss/releases/tag/ccow%40v0.1.0). Native CoW and process-lifecycle acceptance remain unverified. Native acceptance and performance measurements remain required before support or performance claims can be made.

Install CacheCow 0.1.0 with Homebrew on macOS ARM64, or stable APT/DNF packages on Linux amd64 and arm64. Native archives are also available on [GitHub Releases](https://github.com/delinoio/oss/releases/tag/ccow%40v0.1.0). It is not published to npm, and no npm package installs it.

## Homebrew: macOS ARM64

Install on macOS 15 or later with Apple Silicon:

```sh
brew install delinoio/tap/ccow
ccow --version
```

The formula installs the prebuilt executable without Node.js. Intel macOS is not supported by this formula.

## APT: Debian and Ubuntu

CacheCow 0.1.0 is available in the stable APT repository. Follow [Linux package setup](/linux-packages#verify-the-repository-key) to verify the full key fingerprint and register the stable APT repository. Then install the package named `ccow`:

```sh
sudo apt-get update
sudo apt-get install ccow
ccow --version
```

The repository supports package installation on amd64 and arm64. APT installs the shared `delino-archive-keyring` dependency for authenticated certificate updates. Package installation checks do not establish CoW filesystem or process-lifecycle support; see the runtime targets below.

## DNF: Fedora and RHEL-compatible systems

CacheCow 0.1.0 is available in the stable DNF repository. Verify the key and register the stable DNF repository from [Linux package setup](/linux-packages#dnf-stable). Keep both package and repository signature checks enabled. Then run:

```sh
sudo dnf install ccow
ccow --version
```

Neither package manager activates shims, edits shell configuration, or initializes a cache. After installation, run `ccow doctor` inside your project. Native CLI installation does not require Node.js.

## Native installation

Native archives do not require Node.js. To install an archive:

1. Open [GitHub Releases](https://github.com/delinoio/oss/releases) and select the `ccow@v<version>` release.
2. Download the archive for your OS and architecture, and the release's `SHA256SUMS` file.
3. Verify the archive before extracting it: compute its SHA-256 digest and compare it with the entry for that archive in `SHA256SUMS`. For example, use `shasum -a 256 <archive>` on macOS, `sha256sum <archive>` on Linux, or `Get-FileHash -Algorithm SHA256 <archive>` in PowerShell on Windows. Do not install an archive whose digest differs.
4. Extract the archive and put the `ccow` executable in a directory on your PATH. On Windows, use `ccow.exe`.
5. Run `ccow --version` and `ccow doctor` in your project to inspect the environment before preparing a cache.

Native archives use `ccow-<version>-<target>.tar.gz`, with targets `darwin-arm64`, `linux-x64`, `linux-arm64`, and `windows-x64`. Each archive contains the executable, `LICENSE`, `NOTICE`, and a `release.json` file that records the version, source revision, target, and executable digest. Keep `LICENSE` and `NOTICE` with redistributed copies. There is no standalone installer or automatic updater. Linux ARM64 archives provide package installation coverage; native CoW acceptance on ARM64 remains unverified.

Installing the CLI does not activate tool shims. [Install and activate shims](/cachecow/shims) separately if you want ordinary tool commands to enter CacheCow.

## Target environments

These are first-release support targets, not a list of platforms that have passed native tests.

| OS and architecture                            | CoW filesystem               |
| ---------------------------------------------- | ---------------------------- |
| macOS 15 or later, arm64                       | APFS                         |
| Linux x64, Ubuntu 22.04-equivalent environment | Btrfs or reflink-enabled XFS |
| Windows 11 24H2 or later, x64                  | ReFS                         |

Cargo requires 1.99.1 or later within 1.x. Rspack and Rsbuild require 2.3.0 or later within 2.x. Their frontend integration also requires Node.js 24 or later. Older tools, nightly guarantees, other architectures, and other native environments are outside the first-release support guarantee.

CoW may be unavailable across volumes or on another filesystem. Preparation must fail by default in that case. `--allow-copy` explicitly authorizes an ordinary copy; CacheCow must report that mode. Ordinary copies consume separate storage and do not establish CoW support.

## Update, rollback, and removal

Update through the channel you used to install:

```sh
# Homebrew
brew update
brew upgrade ccow

# APT
sudo apt-get update
sudo apt-get install --only-upgrade delino-archive-keyring ccow

# DNF
sudo dnf upgrade ccow
```

For a direct archive installation, download and install the desired published version in the same way. Rollback means installing the archive of an earlier published version in place of the current executable; no earlier CacheCow release is asserted here.

An older version must not rewrite settings or records it cannot understand. If it reports an unsupported record version, use a version that understands those records and follow its recovery guidance. Preserve the records until recovery is understood.

Before removing CacheCow, [remove persistent shell registration and uninstall its shims](/cachecow/shims#remove-activation-and-shims). Then remove the package through the same package manager:

```sh
brew uninstall ccow
sudo apt-get remove ccow
sudo dnf remove ccow
```

Run only the command for your installation channel. For a direct archive installation, remove only the executable you installed. Cache deletion is a separate, deliberate [clean operation](/cachecow/cache#manual-cleaning); shim uninstall preserves caches and settings.
