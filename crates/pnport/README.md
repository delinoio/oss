# pnport

pnport is an unreleased Rust CLI for a virtual Yarn 4 Plug'n'Play filesystem.
**Issue #958 is not complete, and version 0.1.0 is not ready to distribute.**

The current development implementation loads inline and split PnP data without
executing JavaScript, resolves dependencies and aliases using pnp 0.12.12, keeps
peer-specific logical paths, and materializes ZIP content in a private cache.
It includes `doctor --json` schema v1 and `cache path|list|prune|clean`.

Native C conformance fixtures exercise file reads, metadata, directory
traversal, directory-relative open, mmap, dependency write rejection, source
writes, inherited protocol stdout, and child execution. Linux also has a fully
static Go fixture and an offline Yarn inline/split suite using Microsoft's
unchanged native TypeScript compiler. These are development conformance tests,
not complete tool compatibility certification.

The development command shape is:

```text
pnport --project <installed-project> --cache-dir <private-cache> run -- <native-executable> [args...]
pnport doctor --json
pnport cache path
pnport cache list
pnport cache prune
pnport cache clean
```

Global options are `--project`, `--cache-dir`, `--log-level` and `--color`.
`--color=never` disables color; `NO_COLOR` disables it in the default `auto`
mode, while explicit `--color=always` overrides `NO_COLOR`. `doctor --json`
remains ANSI-free in every mode. Diagnostics use stderr; child
streams are inherited. Debug output may include paths, but not file content,
environment values, full argv or child output. Owned failures use stable
`PNPORT_*` codes and exit 125, except missing commands (127), execution failures
(126), and argument errors (2). Child exit status is preserved.

Linux x64 and arm64 GNU builds use an owned-child seccomp syscall tracer for
dynamic and static executables. `doctor` checks the host, companion `.so`, and
actual syscall tracing before running a command. Unsupported kernel or container
tracing fails with `PNPORT_UNSUPPORTED_OPERATION` and exit 125. Ubuntu 22.04
arm64 Docker execution passes the native fixtures and offline TypeScript suite;
the arm64 host's amd64 emulation does not expose the tracing capability, so
native x64 execution still needs validation. Windows has no Detours backend.
Current restrictions include incomplete macOS fork/exec/posix_spawnp
propagation, mutation/handle/watch coverage, complete detached-descendant and
abrupt-supervisor recovery, and terminal job-control certification. The macOS
backend is also a development implementation. All six target release gates
remain open.

Before release, all six native targets must pass the complete filesystem,
process, installation, privacy and recovery suite. Native/npm packages,
installers, Homebrew, publication recovery and benchmark results remain open.
The [public guides](https://oss.delino.io/pnport/) now describe editor setup
without claiming editor-specific certification. No partial preview is
permitted. Use [GitHub issues](https://github.com/delinoio/oss/issues) for
support.
