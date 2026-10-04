# Installation and availability

**pnport 0.1.0 is available.** npm latest, signed GitHub archives, the POSIX installer and Homebrew provide macOS 15+ and Ubuntu 22.04-equivalent glibc Linux builds for x64 and arm64. Windows is planned for 0.2.0 and remains unsupported. Use an exact published version; a source-only build does not establish package availability.

Known limits: intermittent macOS initialization failures and cancellation returning 125 instead of the signal-derived status remain unresolved. Complete filesystem/process/tool compatibility remains unverified. Report reproducible failures in [issue #958](https://github.com/delinoio/oss/issues/958), with the exact version, OS/architecture and sanitized diagnostics.

## Stable distribution

The 0.1.0 release provides [prebuilt GitHub archives](https://github.com/delinoio/oss/releases/tag/pnport%40v0.1.0) with checksums and signed verification material, a direct POSIX installer, prebuilt Homebrew packages, and the `@delino/pnport` npm launcher for macOS and glibc Linux. Windows and its PowerShell installer remain planned for 0.2.0. The npm launcher requires Node.js 22 or newer and one of the four matching native optional packages. The standalone native CLI does not need Node.js merely to start.

The published and planned platforms are:

| Host | Architectures | First release |
| --- | --- | --- |
| macOS 15 or newer | x64, arm64 | 0.1.0, available |
| Ubuntu 22.04-equivalent glibc Linux | x64, arm64 | 0.1.0, available |
| Windows 10 22H2 or newer, MSVC | x64, arm64 | 0.2.0, planned |

The stable 0.1.0 build passed native execution, installed npm/Yarn and direct-install checks, inline/split TypeScript and numerical benchmarks on macOS 15 and Ubuntu 22.04 for both architectures. Linux checks include static children. These checks do not establish compatibility with every executable or complete filesystem/process acceptance. Musl hosts and mixed-architecture execution are excluded. Both Windows targets retain complete validation requirements before 0.2.0. The 0.1.x npm launcher reports Windows as unsupported without downloading or starting another executable.

## Install 0.1.0

pnport needs an already installed Yarn 4 Plug'n'Play project with `.pnp.cjs`. It does not install dependencies, generate a PnP manifest, update a lockfile, or create a physical project `node_modules` directory. Keep the selected project's Yarn installation intact.

Choose an explicit version and confirm it in the registry before installing. Confirm the installed CLI with `pnport --version` and `pnport doctor`. The following examples install the published 0.1.0 release:

For npm or Yarn 4, keep optional dependencies enabled so the launcher can select the matching native package:

```sh
npm view @delino/pnport@0.1.0 version && npm install --global --ignore-scripts @delino/pnport@0.1.0
# or, in a Yarn 4 project:
npm view @delino/pnport@0.1.0 version && yarn add --dev @delino/pnport@0.1.0
```

Yarn's [minimum package age setting](https://yarnpkg.com/configuration/yarnrc/#npmMinimalAgeGate) can temporarily quarantine a newly published version, including stable 0.1.0. If Yarn reports that the version is quarantined, wait for your configured age requirement. The global npm CLI above can run against your already installed PnP project while that Yarn installation is unavailable.

For a direct install on macOS or glibc Linux, download the <a href="/pnport/install.sh">POSIX installer</a> and pin the version:

```sh
curl -fsSLo pnport-install.sh https://oss.delino.io/pnport/install.sh
bash pnport-install.sh --version 0.1.0
```

Windows installation is planned for a verified 0.2.0 release. The current <a href="/pnport/install.ps1">PowerShell installer</a> rejects requests before downloading or installing files. Keep the following example for a future published Windows-supported version only:

```powershell
Invoke-WebRequest https://oss.delino.io/pnport/install.ps1 -OutFile pnport-install.ps1
./pnport-install.ps1 -Version '<published-version>'
```

Supported macOS and glibc Linux hosts can also use `brew install delinoio/tap/pnport`. Direct installers verify archive checksums and activate the executable with its matching adjacent interception library. Keep those two files together when using a native archive. No method performs an automatic update. Continue with the [command reference](/pnport/commands) or see [releases and rollback](/pnport/releases) for version policy and verification.

## Experimental npm next channel

The first experimental version, `0.1.0-next.1`, is published for external testing on macOS and glibc Linux, each on x64 and arm64. Matching [signed GitHub prerelease archives](https://github.com/delinoio/oss/releases/tag/pnport%40v0.1.0-next.1) are also available. Check the registry before installing; if the check fails, the preview is unavailable:

```sh
npm view @delino/pnport@next version && npm install --global --ignore-scripts @delino/pnport@next
# Or, after the same successful registry check, in a Yarn 4 project:
npm view @delino/pnport@next version && yarn add --dev @delino/pnport@next
```

Keep optional dependencies enabled and use Node.js 22 or newer. Global npm installation keeps a physical `node_modules` directory out of the selected PnP project; do not run an npm local install inside that project. Confirm the global CLI with `pnport --version`, or use `yarn pnport --version` after the Yarn project installation. Invoke project-local commands through `yarn pnport` in the examples below. To reproduce a report, pin the exact returned version instead of `next`, which can advance.

Continue with [preview testing](/pnport/preview-testing) for exact-version installation, project preparation, and Turbopack and TypeScript 7 commands.

The preview is experimental: full filesystem/process/tool compatibility, minimum supported OS validation and complete benchmark acceptance remain unfinished. Intermittent native initialization failures, reported with exit status 125, remain under investigation. A passing installation or TypeScript build does not establish compatibility with every tool. Windows, musl hosts and mixed architectures are unsupported; no preview Homebrew formula is provided. Report reproducible failures in [issue #958](https://github.com/delinoio/oss/issues/958), including the exact version, OS and architecture, command shape and sanitized diagnostics. Remove credentials, private paths and project content before sharing.
