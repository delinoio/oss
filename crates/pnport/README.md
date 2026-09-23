# pnport

pnport is an unreleased Rust CLI for a virtual Yarn 4 Plug'n'Play filesystem.
**Issue #958 is not complete, and version 0.1.0 is not ready to distribute.**

The current development implementation loads inline and split PnP data without
executing JavaScript, resolves dependencies and aliases using pnp 0.12.12, keeps
peer-specific logical paths, and materializes ZIP content in a private cache.
It includes `doctor --json` schema v1 and `cache path|list|prune|clean`.

A macOS native C conformance fixture exercises file reads, metadata, directory
traversal, directory-relative open, mmap, logical realpath, dependency write
rejection, source writes, literal/empty arguments, inherited protocol stdout and
posix_spawn with a replacement environment. This evidence is limited to the
fixture. It is not tool compatibility certification or full filesystem support.

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

Current restrictions include incomplete fork/exec/posix_spawnp propagation,
mutation/handle/watch coverage, detached-descendant recovery and terminal
job-control certification, and no Linux syscall or Windows Detours backend.
Linux and Windows execution currently fails explicitly. The macOS backend is a
development implementation, not the release support contract.

Before release, all six native targets must pass the complete filesystem,
process, installation, privacy and recovery suite. Native/npm packages,
installers, Homebrew, publication recovery and benchmark results remain open.
The [public guides](https://oss.delino.io/pnport/) now describe editor setup
without claiming editor-specific certification. No partial preview is
permitted. Use [GitHub issues](https://github.com/delinoio/oss/issues) for
support.
