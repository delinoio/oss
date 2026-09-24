# Filesystem and processes

**pnport 0.1.0 remains unreleased.** This page describes the intended release behavior; current development evidence covers only a subset of the filesystem and process cases below.

## Dependency view

pnport is designed to translate an installed Yarn 4 PnP graph into a virtual `node_modules` view for supported subprocesses. The release contract includes workspaces, aliases, scoped packages, peer-dependent variants, unplugged packages, and caches outside the project. It covers reading ZIP-backed files, metadata, directory listing, links and real paths, directory-relative operations, file handles, watching, memory-mapped reads, and supported binary and native-library loading.

Dependency views and managed package contents are read-only. Writes through them fail with filesystem errors. Ordinary project source and output paths retain normal write behavior. pnport does not create a physical project `node_modules` directory, and it refuses a conflicting physical directory rather than changing user files.

The view follows the installed PnP dependency graph. It does not promise complete enforcement of the Yarn JavaScript loader's import boundaries for arbitrary languages or tools. It is not a security sandbox.

## Child processes and watches

The release contract applies the view to supported descendants in the owned process tree, including supported spawn and exec paths. Child arguments, cwd, environment, stdin, stdout, stderr, and exit status remain compatible with ordinary execution. pnport has no normal execution timeout or automatic retry.

One process tree uses one PnP graph snapshot. If PnP data or an actively used ZIP archive changes or disappears, pnport must stop the owned tree and ask for a restart. Ordinary source edits continue through normal watch notifications. A supervisor stop first requests normal termination, then escalates after five seconds for remaining owned processes.

## Current limits

Release acceptance still requires complete six-target filesystem, process, watch, native loading, and installation checks, including static child execution on Linux. Linux development fixtures pass on Ubuntu 22.04 arm64, while native x64 and complete release validation remain open; Windows execution remains unavailable. Protected or incompatible executables fail explicitly. pnport will not replace a protected executable, elevate privileges, or silently run it without virtualization. No claim of universal executable compatibility or editor-version certification is made.

For an unsupported child, check [diagnostics](/pnport/diagnostics) and use the [GitHub issue tracker](https://github.com/delinoio/oss/issues) to report a reproducible case.
