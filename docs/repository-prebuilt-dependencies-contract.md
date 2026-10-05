# Prebuilt dependency distribution

## Ownership and interface

`delinoio/prebuilt` builds public dependency executables from reviewed recipes.
`prebuilt-dependencies.lock.json` selects immutable releases for this repository.
The first dependency is Tauri CLI at upstream revision
`c8c75b1f7f43e7cb1e7d773ed2f6f96fad2fe975`, built with
`nightly-2026-09-28`. The release tag is
`tauri-cli-c8c75b1f7f43e7cb1e7d773ed2f6f96fad2fe975-r1`.
`delino-apps` consumes the identical release and lock.

A lock records the distribution repository, source and producer revisions,
recipe version, toolchain, exact CLI version, and six host assets with archive
size and SHA-256 plus executable SHA-256. Host selection uses the executing
Node process's OS and architecture, including Windows `.exe`; an iOS or Android
application target never selects the host executable.

`scripts/prebuilt-dependencies.mjs` owns generic installation.
`scripts/tauri-cli.mjs -- <arguments...>` prepares the CLI and forwards structured
arguments, environment, cwd, exit status and termination signals through the
repository process supervisor. DevHud desktop/mobile generation/build paths and
DeliDev macOS development, bundling and dry runs use this adapter. DeliDev's
Windows/Linux executable-only development path remains a direct application
Cargo build. Consumer manifests and `Cargo.lock` contain no Tauri CLI wrapper.

## Installation and storage

The helper checks `.cache/prebuilt/<dependency>/<release>/<host>/` before fetching
the exact public GitHub Release asset. A cache receipt, regular executable,
SHA-256 and successful exact-version execution are required on every reuse.
Restored GitHub caches receive the same checks. A damaged cache is replaced only
after the public archive's size/digest, bounded regular-file extraction and
executable digest/execution pass. Staging and atomic directory replacement keep
an earlier installation intact when verification fails. A per-installation lock
serializes preparation; an interrupted lock requires explicit inspection.

Unsupported hosts, invalid lock data, download failures, archive or executable
hash mismatches and execution failures stop the caller. There is no automatic
source fallback. `node scripts/tauri-cli.mjs --source [--print-path]` explicitly
builds the pinned source locally into a separate host cache; CI rejects this
option. Application Rust/library compilation retains its existing Cargo caches.

`.github/actions/setup-prebuilt` restores only the exact release/host/archive
and executable digest key, then prepares/verifies it. Ordinary CI and native dry
runs save that binary only after successful main-branch validation. Private
candidate workflows restore/verify without saving or publishing dependencies.

## Updates and validation

Create a new reviewed recipe version in the public producer, build all six
native hosts, publish its complete immutable release, and verify public cold
and warm installation on all six hosts before updating both consumer locks.
Never use `latest`, replace published bytes or silently revise a tag.
The producer owns original licenses, third-party notices and build provenance.
The consumers retain CEF package notices and their independent app validation.

Installer fixtures cover first download, reuse, corruption, digest mismatch,
network failure, Windows executable names, unsupported hosts, explicit local
source builds and CI source-build refusal. CI contract tests guard cache keys,
main-only saves and native/mobile routing. `pnpm ci:contracts` and package-owned
DevHud/DeliDev tests include these boundaries.
