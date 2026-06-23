# binpm

`binpm` is a Rust CLI for managing native command-line tools from release
assets without requiring Node.js or language-specific package managers.

This crate contains the `binpm` runtime: stable command parsing, typed contract
foundations, structured tracing setup, centralized errors, provider release
lookup, deterministic asset selection, download and cache handling, archive
extraction, local tooling records, install/diagnostic flows, and command
execution.

Provider release lookup may authenticate with documented environment variables.
Host-specific token variables take precedence, and enterprise or self-managed
hosts only use their host-specific token variable. Tokens and authorization
headers are never logged or persisted.

Sources are release-asset providers, not package-manager backends. GitHub.com
shorthands may normalize to canonical `github:` specs, but GitLab specs always
include the host, such as `gitlab:gitlab.com/group/project`. Prefixes such as
`npm:`, `cargo:`, and `brew:` are rejected with explicit unsupported-backend
diagnostics.

`binpm explain <source>` is read-only and may contact the source provider to
show release selection, skipped release reasons, and asset scoring. `binpm
explain <cmd>` is both read-only and network-free because it inspects existing
package records only.

Source archives such as `source.tar.gz`, `source.zip`, GitHub generated source
downloads, and GitLab `assets.sources` entries are ignored for installation.
Source-only releases report a source-archive-only diagnostic instead of a
generic no-asset or target-mismatch failure. On Linux musl targets, assets with
missing libc signals are rejected unless they explicitly say `musl`, `static`,
`portable`, `universal`, or `any`; diagnostics list the rejected assets and
guide users to verify compatibility before adding target overrides.

Cache commands keep asset cleanup separate from uninstall behavior:
`binpm cache clean` removes global cache asset entries while preserving cache
references, package records, and executable links or copies, and `binpm cache
prune` repairs stale structured project references before pruning unreferenced
assets. Both commands make removed and preserved boundaries explicit in human
output and `--json` summaries. Legacy cache references are preserved until a
local install, update, or remove rewrites them as structured references.
`binpm cache key` remains read-only and reports missing lockfiles explicitly.

Global binpm state lives under the fixed `~/.binpm` home by default. binpm does
not split global cache, package records, binaries, or temporary extraction state
across XDG directories. Set an absolute `BINPM_HOME` only for tests, isolated
automation, or portable environments.

If a download verifies successfully but install finalization later fails, such
as an archive binary-selection error, binpm may keep the SHA-256-recorded cache
entry for a retry. Reuse still revalidates the cached bytes before extraction or
install finalization.

`binpm init` creates new manifests without overwriting existing files.
`--manifest-path <PATH>` is the explicit destination escape hatch when the
default Git-root or manifest-ancestor destination is not desired. `binpm env`
prints non-mutating PATH commands, supports optional shell inference, accepts
`pwsh` as PowerShell syntax, and exposes `--global` or `--local` to print only
one PATH command.

`binpm update [cmd...] [--local|--global]` supports local and global tools.
Omitting command names updates every tool in the selected scope, and output
states that all-tools mode before printing the planned update list. Local
updates advance exact-version manifest entries to the latest stable release and
write `binpm.toml`, `binpm.lock`, and project-local executables consistently.

Use `-v`/`--verbose` for info-level tracing diagnostics and `--debug` for
debug-level tracing diagnostics. `BINPM_LOG` remains supported when no CLI
verbosity flag is provided; CLI verbosity flags take precedence.

Canonical contracts live in:

- `../../docs/project-binpm.md`
- `../../docs/crates-binpm-foundation.md`

## Validation

```sh
cargo test -p binpm
cargo test --workspace --all-targets
```
