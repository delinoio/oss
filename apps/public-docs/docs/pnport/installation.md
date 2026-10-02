# Installation and availability

**pnport 0.1.0 has not been released.** The current source does not provide a published npm package, native archive, POSIX or PowerShell installer, or Homebrew formula. Do not use an unpublished package version, guessed download URL, or source-only launcher as an installation method.

## Experimental npm next channel

The first experimental version, `0.1.0-next.1`, is being prepared for external testing on macOS and glibc Linux, each on x64 and arm64. It has not been published yet. Check the registry before installing; if the check fails, the preview is unavailable:

```sh
npm view @delino/pnport@next version && npm install --save-dev --ignore-scripts @delino/pnport@next
# Or, after the same successful registry check, in a Yarn 4 project:
npm view @delino/pnport@next version && yarn add --dev @delino/pnport@next
```

Keep optional dependencies enabled and use Node.js 22 or newer. Confirm the selected version with `pnport --version` through your package manager. To reproduce a report, pin the exact returned version instead of `next`, which can advance.

The preview is experimental: full filesystem/process/tool compatibility, minimum supported OS validation and complete benchmark acceptance remain unfinished. Intermittent native initialization failures, reported with exit status 125, remain under investigation. A passing installation or TypeScript build does not establish compatibility with every tool. Windows, musl hosts and mixed architectures are unsupported; no preview Homebrew formula is provided. Report reproducible failures in [issue #958](https://github.com/delinoio/oss/issues/958), including the exact version, OS and architecture, command shape and sanitized diagnostics. Remove credentials, private paths and project content before sharing.

## Planned distribution

A complete 0.1.0 release is planned to provide prebuilt GitHub archives with checksums and signed verification material, a direct POSIX installer, prebuilt Homebrew packages, and the `@delino/pnport` npm launcher for macOS and glibc Linux. Windows and its PowerShell installer are planned for 0.2.0. The npm launcher will require Node.js 22 or newer and one of the four matching native optional packages. The standalone native CLI will not need Node.js merely to start.

The release targets are:

| Host | Architectures | Planned first release |
| --- | --- | --- |
| macOS 13 or newer | x64, arm64 | 0.1.0 |
| Ubuntu 22.04-equivalent glibc Linux | x64, arm64 | 0.1.0 |
| Windows 10 22H2 or newer, MSVC | x64, arm64 | 0.2.0 |

Linux support includes static child executables as a release requirement. Musl hosts and mixed-architecture execution are excluded. These are **release targets, not a claim of current package availability or verified compatibility**. Every macOS and glibc Linux target must pass execution and installation validation before stable 0.1.0 is published; both Windows targets must meet the same requirements before 0.2.0. Experimental npm next availability is separate from stable acceptance. The 0.1.x npm launcher reports Windows as unsupported without downloading or starting another executable.

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

Windows installation is planned for a verified 0.2.0 release. The current <a href="/pnport/install.ps1">PowerShell installer</a> rejects requests before downloading or installing files. Keep the following example for a future published Windows-supported version only:

```powershell
Invoke-WebRequest https://oss.delino.io/pnport/install.ps1 -OutFile pnport-install.ps1
./pnport-install.ps1 -Version '<published-version>'
```

After the first release, supported macOS and glibc Linux hosts can also use `brew install delinoio/tap/pnport`. Direct installers verify archive checksums and activate the executable with its matching adjacent interception library. Keep those two files together when using a native archive. No method performs an automatic update. Continue with the [command reference](/pnport/commands) or see [releases and rollback](/pnport/releases) for version policy and verification.
