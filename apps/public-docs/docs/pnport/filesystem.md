# Filesystem behavior and limits

**pnport is unreleased; this page describes the planned acceptance boundary.**

pnport is designed for Yarn 4 inline and split PnP manifests, workspaces, scoped packages, aliases, peer-dependent variants, unplugged packages, and ZIP caches outside the project directory. Supported child processes see dependency paths through a virtual `node_modules` view while ordinary project source and output files remain ordinary writable files. Managed dependencies are read-only.

The release acceptance tests cover reads, metadata, directory listing, links, canonical paths, relative and directory-relative operations, file handles, memory-mapped reads, watching, package binaries, and native libraries. Peer-specific logical package identity must remain distinct even when package bytes are shared. A physical `node_modules` collision is an error; pnport does not delete or rewrite it.

One run uses one PnP graph snapshot. A change to PnP data or an active package archive stops the owned process tree and asks you to restart. Workspace source changes continue through normal watch behavior. A supported long-running command has no pnport-imposed timeout or automatic retry.

Some operating-system executables and interception paths cannot be virtualized. pnport must report unsupported or protected execution explicitly instead of running a command with incomplete virtualization. pnport is not a security sandbox and does not promise the Yarn JavaScript loader's complete import-boundary enforcement for arbitrary tools. Yarn Classic, Yarn 2/3, musl hosts, mixed architectures, Docker or remote-process injection, and universal executable compatibility are not first-release guarantees.
