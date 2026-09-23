# pnport

pnport is being developed to run subprocesses in an installed Yarn 4 Plug'n'Play
project without generating a physical `node_modules` tree.

**0.1.0 has not met its release acceptance gates. No npm or native distribution
is available from this implementation.** Do not treat the presence of launcher
source as evidence that all six native targets work.

The command interface is:

```text
pnport run -- <command> [args...]
pnport doctor [--json]
pnport cache path
pnport cache list
pnport cache prune
pnport cache clean
```

Select a project with `--project` without changing the child's working directory.
Use `--cache-dir` for a private cache location, `--log-level=debug` for diagnostics,
and `--color=never` or `NO_COLOR` to disable color. Diagnostics go to stderr;
child standard streams are inherited. `doctor --json` emits one ANSI-free JSON
object with schemaVersion 1, ready, and typed checks.

The intended platforms are macOS 13+, Windows 10 22H2+ MSVC, and Ubuntu
22.04-equivalent glibc, each on x64 and arm64. Musl and mixed architectures are
excluded. Universal executable compatibility is not claimed.

The launcher requires Node.js 22+ and forwards literal arguments to the matching
installed native package. It never downloads, compiles, executes install scripts,
or falls back to an unrelated executable. A missing native package or injection
companion is an error. Platform packages declare `preferUnplugged` for Yarn 4.
The standalone native executable does not require Node.js merely to start.

The release contract requires dependencies to be read-only; ordinary source/output paths remain writable. Cache
entries are retained until explicit cleanup, without automatic expiry, eviction
or a size quota. Active entries are preserved. A graph or active archive change
requires restarting the complete process tree. Debug diagnostics may contain
paths, but never file contents, environment values, full argv or child output.

Future releases use explicit exact-version installation for updates and rollback,
with no automatic update checks. Only use a version whose complete platform
artifacts and installation evidence have been published. Editor configuration
and reproducible performance results will accompany release acceptance; no
editor-specific compatibility or benchmark result is certified yet.

Support: [GitHub issues](https://github.com/delinoio/oss/issues).
