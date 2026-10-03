# pnport

pnport is being developed to run subprocesses in an installed Yarn 4 Plug'n'Play
project without generating a physical `node_modules` tree.

**Stable 0.1.0 has not met its release acceptance gates.** Do not treat the presence of launcher
source as evidence that all four initial native targets work.

Experimental `0.1.0-next.N` npm next previews are intended for external
testing. Check registry availability before installing:

```sh
npm view @delino/pnport@next version && npm install --save-dev --ignore-scripts @delino/pnport@next
```

Keep optional dependencies enabled. Full feature, minimum-OS and benchmark
acceptance is incomplete, and intermittent native initialization failures remain
under investigation. Preview availability does not establish stable readiness;
Windows and preview Homebrew remain unavailable. Pin the exact returned version
when reporting a reproducible failure in issue #958, and share only sanitized
diagnostics without credentials, private paths or project content.

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

Version 0.1.0 targets macOS 15+ and Ubuntu 22.04-equivalent glibc, each on
x64 and arm64. Windows 10 22H2+ MSVC on x64 and arm64 is planned for 0.2.0;
this release rejects Windows execution and installation. Musl and mixed architectures are
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
artifacts and installation evidence have been published. The
[public guides](https://oss.delino.io/pnport/) describe editor configuration
and a reproducible benchmark method. Performance results still require release
acceptance; no editor-specific compatibility or benchmark result is certified yet.

Support: [GitHub issues](https://github.com/delinoio/oss/issues).
