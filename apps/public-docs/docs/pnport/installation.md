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

When a verified release is published, choose an explicit version and confirm the installed CLI with `pnport --version` and `pnport doctor`. The following examples are **for a future published version only**; replace `<published-version>` after checking [GitHub Releases](https://github.com/delinoio/oss/releases) or the [npm package](https://www.npmjs.com/package/@delino/pnport).

For npm or Yarn 4, keep optional dependencies enabled so the launcher can select the matching native package:

```sh
npm install --save-dev --ignore-scripts '@delino/pnport@<published-version>'
# or, in a Yarn 4 project:
yarn add --dev '@delino/pnport@<published-version>'
```

For a direct install on macOS or glibc Linux, download the <a href="/pnport/install.sh">POSIX installer</a> and pin the version:

```sh
curl -fsSLo pnport-install.sh https://oss.delino.io/pnport/install.sh
bash pnport-install.sh --version '<published-version>'
```

On Windows, use the <a href="/pnport/install.ps1">PowerShell installer</a>:

```powershell
Invoke-WebRequest https://oss.delino.io/pnport/install.ps1 -OutFile pnport-install.ps1
./pnport-install.ps1 -Version '<published-version>'
```

After the first release, supported macOS and glibc Linux hosts can also use `brew install delinoio/tap/pnport`. Direct installers verify archive checksums and activate the executable with its matching adjacent interception library. Keep those two files together when using a native archive. No method performs an automatic update. Continue with the [command reference](/pnport/commands) or see [releases and rollback](/pnport/releases) for version policy and verification.
