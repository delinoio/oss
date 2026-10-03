# pnport

pnport is a command-line tool in development for running subprocesses against an installed Yarn 4 Plug'n'Play project. Its virtual `node_modules` view is intended for programs that read dependencies as ordinary files, without creating a project `node_modules` directory.

**pnport 0.1.0 is not released.** The experimental `0.1.0-next.1` preview is available for external testing through npm next and [signed GitHub prerelease archives](https://github.com/delinoio/oss/releases/tag/pnport%40v0.1.0-next.1). Stable installation and Homebrew remain unavailable. These guides describe the CLI interface and the requirements for a future complete release; they are not a compatibility certification for arbitrary tools.

See [installation and availability](/pnport/installation) for the registry check, installation commands and known preview limits. Pin the exact version when reporting a failure.

To try the published preview with Turbopack or TypeScript 7, follow [preview testing](/pnport/preview-testing).

## Release targets

The 0.1.0 release targets macOS 15+ and Ubuntu 22.04-equivalent glibc Linux, each on x64 and arm64. All four targets must pass native execution and installation checks before release. Windows 10 22H2+ with MSVC on x64 and arm64 is planned for 0.2.0, with the same complete validation requirements. Linux static child executables are part of the first release gate. Musl hosts and mixed-architecture execution are outside the target set.

The preview passed native candidate execution and installation fixtures on all four macOS and glibc Linux targets, including Linux dynamic and static children. These fixtures do not establish support for every executable. Full feature, minimum-OS and benchmark acceptance remain unfinished; intermittent native initialization failures remain under investigation. Windows execution remains unavailable. See [installation and availability](/pnport/installation) for the current status.

## What the CLI is designed to do

- Select one installed Yarn 4 PnP project without changing the subprocess's working directory.
- Give supported subprocesses a read-only dependency view, including ZIP-backed packages, while preserving ordinary source and output writes.
- Keep child standard streams and exit status intact, so protocol-based tools can use stdout.
- Report unsupported children explicitly and require a restart when the dependency graph changes, without silently running outside the virtual view.

Start with [getting started](/pnport/getting-started) and the [command reference](/pnport/commands). The [filesystem and process guide](/pnport/filesystem-and-processes) explains the boundaries.

## Guides

- [Installation and availability](/pnport/installation)
- [Getting started](/pnport/getting-started)
- [Preview testing](/pnport/preview-testing)
- [Commands](/pnport/commands)
- [Filesystem and processes](/pnport/filesystem-and-processes)
- [Editors and language servers](/pnport/editors)
- [Cache management](/pnport/cache)
- [Diagnostics and troubleshooting](/pnport/diagnostics)
- [Benchmarks](/pnport/benchmarks)
- [Releases and rollback](/pnport/releases)

Report problems through [Delino OSS GitHub Issues](https://github.com/delinoio/oss/issues). There is no support response-time commitment.
