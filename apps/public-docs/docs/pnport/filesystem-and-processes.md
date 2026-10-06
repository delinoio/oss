# Filesystem and processes

**pnport 0.1.2 is available.** This page describes its filesystem and process contracts. Complete compatibility remains unverified; check [installation and known limits](/pnport/installation), including unresolved macOS initialization and cancellation failures.

## Dependency view

pnport is designed to translate an installed Yarn 4 PnP graph into a virtual `node_modules` view for supported subprocesses. The release contract includes workspaces, aliases, scoped packages, peer-dependent variants, unplugged packages, and caches outside the project. It covers reading ZIP-backed files, metadata, directory listing, links and real paths, directory-relative operations, file handles, watching, memory-mapped reads, and supported binary and native-library loading.

Dependency views and managed package contents are read-only. Writes through them fail with filesystem errors. Ordinary project source and output paths retain normal write behavior. Dependency reads do not create a physical project `node_modules` directory, and pnport refuses a conflicting physical directory rather than changing user files.

The view follows the installed PnP dependency graph. It does not promise complete enforcement of the Yarn JavaScript loader's import boundaries for arbitrary languages or tools. It is not a security sandbox.

## Tool-cache support

Version 0.1.2 can combine a writable project's hidden tool caches, such as `node_modules/.vite`, with its virtual dependencies. Empty directories and hidden files/directories are accepted, except `.bin` and dependency names. Tools can create, update and remove their caches using their default paths. Reading dependencies does not create a physical dependency tree. Installed packages remain read-only; real package folders and symlinked `node_modules` roots remain conflicts.

This capability is not included in the earlier 0.1.0 or 0.1.0-next.1 packages. Those versions still report a conflict for cache-only physical `node_modules` directories.

## Child processes and watches

On macOS, initially rejected protected or otherwise non-injectable child images return `ENOTSUP` to the invoking program before execution. The caller may handle that error and continue using the virtual dependency view. pnport never executes or replaces the rejected image. A protected root command, privileged image, executable that changes before launch or missing injection still fails explicitly.

The release contract applies the view to supported descendants in the owned process tree, including supported spawn and exec paths. Child arguments, cwd, environment, stdin, stdout, stderr, and exit status remain compatible with ordinary execution. pnport has no normal execution timeout or automatic retry.

One process tree uses one PnP graph snapshot. If PnP data or an actively used ZIP archive changes or disappears, pnport must stop the owned tree and ask for a restart. Ordinary source edits continue through normal watch notifications. A supervisor stop first requests normal termination, then escalates after five seconds for remaining owned processes.

## Current limits

In the published `0.1.0-next.1` preview, macOS commands that detach into a new background session or process group can leave descendants running after pnport stops. Test foreground commands with daemonization disabled. The stable 0.1.2 release includes later process-ownership changes, but complete detached-process compatibility remains unverified. Publication does not repair the immutable preview.

The stable 0.1.2 build passed native execution, installed-package, inline/split TypeScript and numerical benchmark checks on macOS 15 and Ubuntu 22.04 for x64 and arm64, including static Linux children. These checks do not establish complete filesystem, process, watch or native-loading compatibility. Full acceptance remains incomplete. Intermittent macOS initialization failures and cancellation returning 125 instead of the signal-derived status remain unresolved. Windows execution remains unavailable and is planned for 0.2.0 with the same full requirements. Protected or incompatible executables fail explicitly. pnport will not replace a protected executable, elevate privileges, or silently run it without virtualization. No claim of universal executable compatibility or editor-version certification is made.

For an unsupported child, check [diagnostics](/pnport/diagnostics) and use the [GitHub issue tracker](https://github.com/delinoio/oss/issues) to report a reproducible case.
