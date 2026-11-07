# Installation and availability

> CacheCow 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Release status

Version 0.1.0 is not yet released. Do not assume a native archive, checksum, or installation path is available because it appears in this guide. The procedures below describe the intended release. Native acceptance and performance measurements remain required before support claims can be made.

CacheCow is distributed only as native archives on [GitHub Releases](https://github.com/delinoio/oss/releases). It is not published to npm, and no npm package installs it.

## Native installation

Native archives do not require Node.js. When a release is available:

1. Open [GitHub Releases](https://github.com/delinoio/oss/releases) and select the `ccow@v<version>` release.
2. Download the archive for your OS and architecture, and the release's `SHA256SUMS` file.
3. Verify the archive before extracting it: compute its SHA-256 digest and compare it with the entry for that archive in `SHA256SUMS`. For example, use `shasum -a 256 <archive>` on macOS, `sha256sum <archive>` on Linux, or `Get-FileHash -Algorithm SHA256 <archive>` in PowerShell on Windows. Do not install an archive whose digest differs.
4. Extract the archive and put the `ccow` executable in a directory on your PATH. On Windows, use `ccow.exe`.
5. Run `ccow --version` and `ccow doctor` in your project to inspect the environment before preparing a cache.

Native archives use `ccow-<version>-<target>.tar.gz`, with targets `darwin-arm64`, `linux-x64`, and `windows-x64`. Each archive contains the executable, `LICENSE`, `NOTICE`, and a `release.json` file that records the version, source revision, target, and executable digest. Keep `LICENSE` and `NOTICE` with redistributed copies. These names do not imply that an artifact has been published. No standalone installer, Homebrew installation, or automatic updater is planned for the first release.

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

Updates are manual. Download and install the archive of the desired published version in the same way. Rollback means installing the archive of an earlier published version in place of the current executable; no earlier CacheCow release is asserted here.

An older version must not rewrite settings or records it cannot understand. If it reports an unsupported record version, use a version that understands those records and follow its recovery guidance. Preserve the records until recovery is understood.

Before removing CacheCow, [remove persistent shell registration and uninstall its shims](/cachecow/shims#remove-activation-and-shims). Then remove only the executable you installed. Cache deletion is a separate, deliberate [clean operation](/cachecow/cache#manual-cleaning); shim uninstall preserves caches and settings.
