# Installation and availability

> CowCache 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Release status

Version 0.1.0 is not yet released. Do not assume an npm version, native archive, checksum, or installation path is available because it appears in this guide. The procedures below describe the intended release. Native acceptance and performance measurements remain required before support claims can be made.

## Native installation

Native Cargo use does not require Node.js. When a release is available:

1. Open [GitHub Releases](https://github.com/delinoio/oss/releases) and select a CowCache version.
2. Select the native archive for your OS and architecture. Check its release notes for native acceptance and known limitations.
3. Verify the archive against the checksums supplied with that release before extracting it.
4. Put the extracted `cowc` executable in a directory on your PATH. On Windows, use `cowc.exe`.
5. Run `cowc doctor` in your project to inspect the environment before preparing a cache.

Exact archive names, checksum filenames, and download commands will be supplied with the release. No standalone installer, Homebrew installation, or automatic updater is planned for the first release.

## npm installation

The intended npm package is `@delino/cowc`, which exposes `cowc`. The launcher requires Node.js 24 or later. After the exact version is published, the intended pinned installation is:

```sh
npm install --global @delino/cowc@0.1.0
cowc doctor
```

This is a future installation example, not evidence that npm currently serves this version. Installing the CLI does not activate tool shims. [Install and activate shims](/cowcache/shims) separately if you want ordinary tool commands to enter CowCache.

## Target environments

These are first-release acceptance targets, not a list of platforms that have passed native tests.

| OS and architecture                            | CoW filesystem               |
| ---------------------------------------------- | ---------------------------- |
| macOS 15 or later, arm64                       | APFS                         |
| Linux x64, Ubuntu 22.04-equivalent environment | Btrfs or reflink-enabled XFS |
| Windows 11 24H2 or later, x64                  | ReFS                         |

Cargo requires 1.99.1 or later within 1.x. Rspack and Rsbuild require 2.3.0 or later within 2.x. Their frontend integration also requires Node.js 24 or later. Older tools, nightly guarantees, other architectures, and other native environments are outside the first-release support guarantee.

CoW may be unavailable across volumes or on another filesystem. Preparation must fail by default in that case. `--allow-copy` explicitly authorizes an ordinary copy; CowCache must report that mode. Ordinary copies consume separate storage and do not establish CoW support.

## Update, rollback, and removal

Updates are manual. Install the desired published version through the same distribution method. Rollback means reinstalling an earlier published version; no earlier CowCache release is asserted here.

An older version must not rewrite settings or records it cannot understand. If it reports an unsupported record version, use a version that understands those records and follow its recovery guidance. Preserve the records until recovery is understood.

Before removing CowCache, [remove persistent shell registration and uninstall its shims](/cowcache/shims#remove-activation-and-shims). For an npm installation, then run `npm uninstall --global @delino/cowc`. For a native installation, remove only the executable you installed. Cache deletion is a separate, deliberate [clean operation](/cowcache/cache#manual-cleaning); shim uninstall preserves caches and settings.
