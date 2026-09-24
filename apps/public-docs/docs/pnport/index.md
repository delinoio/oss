# pnport

pnport is a command-line tool in development for running subprocesses against an installed Yarn 4 Plug'n'Play project. Its virtual `node_modules` view is intended for programs that read dependencies as ordinary files, without creating a project `node_modules` directory.

**pnport 0.1.0 is not released. No npm package, native archive, installer, or Homebrew formula is currently available for installation.** These guides describe the CLI interface and the requirements for a future complete release; they are not a compatibility certification for arbitrary tools.

## Release targets

The 0.1.0 release targets macOS 13+, Windows 10 22H2+ with MSVC, and Ubuntu 22.04-equivalent glibc Linux, each on x64 and arm64. All six targets must pass native execution and installation checks before release. Linux static child executables are part of that gate. Musl hosts and mixed-architecture execution are outside the target set.

Development evidence on macOS arm64 and Ubuntu 22.04 arm64 Docker does not establish support for every target or executable. Linux dynamic and static development fixtures pass on arm64; native Linux x64 execution and full release validation remain open. Windows execution remains unavailable. See [installation and availability](/pnport/installation) for the current status.

## What the CLI is designed to do

- Select one installed Yarn 4 PnP project without changing the subprocess's working directory.
- Give supported subprocesses a read-only dependency view, including ZIP-backed packages, while preserving ordinary source and output writes.
- Keep child standard streams and exit status intact, so protocol-based tools can use stdout.
- Report unsupported children explicitly and require a restart when the dependency graph changes, without silently running outside the virtual view.

Start with [getting started](/pnport/getting-started) and the [command reference](/pnport/commands). The [filesystem and process guide](/pnport/filesystem-and-processes) explains the boundaries.

## Guides

- [Installation and availability](/pnport/installation)
- [Getting started](/pnport/getting-started)
- [Commands](/pnport/commands)
- [Filesystem and processes](/pnport/filesystem-and-processes)
- [Editors and language servers](/pnport/editors)
- [Cache management](/pnport/cache)
- [Diagnostics and troubleshooting](/pnport/diagnostics)
- [Benchmarks](/pnport/benchmarks)
- [Releases and rollback](/pnport/releases)

Report problems through [Delino OSS GitHub Issues](https://github.com/delinoio/oss/issues). There is no support response-time commitment.
