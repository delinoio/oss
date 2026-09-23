# Installation and availability

**pnport 0.1.0 has not been released.** The current source does not provide a published npm package, native archive, POSIX or PowerShell installer, or Homebrew formula. Do not use an unpublished package version, guessed download URL, or source-only launcher as an installation method.

## Planned distribution

A complete release is planned to provide prebuilt GitHub archives with checksums and signed verification material, direct installers for POSIX and PowerShell, prebuilt Homebrew packages, and the `@delino/pnport` npm launcher. The npm launcher will require Node.js 22 or newer and the matching native optional package. The standalone native CLI will not need Node.js merely to start.

The release targets are:

| Host | Architectures |
| --- | --- |
| macOS 13 or newer | x64, arm64 |
| Windows 10 22H2 or newer, MSVC | x64, arm64 |
| Ubuntu 22.04-equivalent glibc Linux | x64, arm64 |

Linux support includes static child executables as a release requirement. Musl hosts and mixed-architecture execution are excluded. These are **release targets, not a claim of current package availability or verified compatibility**. Every target must pass execution and installation validation before 0.1.0 is published; there is no partial preview release.

## Before a future install

pnport needs an already installed Yarn 4 Plug'n'Play project with `.pnp.cjs`. It does not install dependencies, generate a PnP manifest, update a lockfile, or create a physical project `node_modules` directory. Keep the selected project's Yarn installation intact.

When a verified release is published, choose an explicit version, use its documented distribution method, and confirm the installed CLI with `pnport --version` and `pnport doctor`. Until then, the [getting started guide](/pnport/getting-started) and [command reference](/pnport/commands) describe the intended workflow without providing a non-existent installation command. See [releases and rollback](/pnport/releases) for version policy.
