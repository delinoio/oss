# DeliDev native package verification

## Scope

`apps/delidev/scripts` and `.github/workflows/delidev-native-dry-run.yml` own
credential-free native package preparation for DeliDev's six desktop targets.
The existing manual six-target workflow also builds and uploads the desktop/Worker updater inputs after basic package verification. These paths build reviewable packages; they do not publish a release, install an
update or establish native runtime acceptance. Production signing, notarization,
updater trust and Windows/Linux execution remain separate requirements.

## Runtime and Language

Node.js 24 orchestrates the existing pinned Rust/Tauri CEF packager and Go sidecar.
CI installs the repository's `nightly-2026-09-28` Rust toolchain and explicitly
selects the matrix's native host, including Windows arm64. On macOS it installs both Apple Rust target standard libraries because the pinned upstream bundler builds both embedded CEF helper variants; each final package still verifies only the selected native architecture.
The Tauri revision remains `c8c75b1f7f43e7cb1e7d773ed2f6f96fad2fe975`; `cef`
151.8.1 resolves to native CEF 151.3.24 / Chromium 151.0.7922.174. No packaging
operation starts a DeliDev server, Worker, harness or account login.

## Users and Operators

Maintainers run local dry runs or manually dispatch the read-only GitHub workflow.
The workflow has no automatic push, tag, release or pull-request trigger.

## Interfaces and Contracts

Both native dry-run entry points use the desktop's shared `prepare:assets` preflight before compilation. Existing PNGs require no Git/network access; an unchanged DeliDev source-icon pointer is restored from cache or an exact-path current-ref LFS fetch, then checked against its original size/digest and PNG container. Local edits are preserved, unrelated assets are excluded, and preparation failure or cancellation prevents packaging. The same preflight is used by ordinary native build/development/bundle commands; see the [desktop contract](apps-delidev-desktop-contract.md).

`pnpm --dir apps/delidev bundle:dry-run --target <triple>` requires a clean committed
checkout, matching native Node architecture and matching `rustc` host. Recheck the
unchanged commit and clean working tree before publishing verified output. The command
rejects cross compilation as native evidence. `--plan` prints the same six-entry
matrix consumed by CI; it requires neither dependencies nor credentials.

| Target | Native runner | Package |
| --- | --- | --- |
| `x86_64-apple-darwin` | `macos-15-intel` | Ad-hoc signed `.app` in a tar archive |
| `aarch64-apple-darwin` | `macos-15` | Ad-hoc signed `.app` in a tar archive |
| `x86_64-pc-windows-msvc` | `windows-2022` | Unsigned MSI |
| `aarch64-pc-windows-msvc` | `windows-11-arm` | Unsigned MSI |
| `x86_64-unknown-linux-gnu` | `ubuntu-24.04` | Unsigned DEB |
| `aarch64-unknown-linux-gnu` | `ubuntu-24.04-arm` | Unsigned DEB |

Linux desktop packages require Ubuntu 24.04 or newer on X11. The pinned GTK4-backed runtime requires system GTK 4.14 or newer. The shared Tauri CLI remains compatible with the Ubuntu 22.04 glibc baseline used to build its immutable prebuilt release.

The macOS-specific `bundle:macos-dry-run` remains available for development
checkouts and verifies strict nested ad-hoc signatures, original bundle ID,
macOS 13 metadata, main/sidecar/CEF/helper architecture and required CEF data.
Both macOS widget extensions are explicitly built and embedded under `Contents/PlugIns` with bundle IDs `io.delino.delidev.widget` and `io.delino.delidev.widget.selection`, macOS 13 metadata, native architecture, sandbox entitlements and App Group `group.io.delino.delidev`. The containing app declares the same group. Local extension builds disable Xcode provisioning and then ad-hoc sign the declared entitlements; this does not establish provisioned group access or widget installation. Follow [the widget contract](apps-delidev-widget-contract.md). The general command archives that verified app with file modes retained.
Windows extracts the MSI administratively without installing it, verifies
original app/sidecar/CEF PE64 architecture and resources, and requires the product
executables and installer to report `NotSigned`. Linux extracts the DEB without
installation, checks ELF64 architecture, CEF resources and the exact installed
launcher link. None of these static checks implies successful native startup.

The pinned Debian bundler moves the main binary to
`/usr/share/DeliDev/delidev-desktop` while retaining its sidecar at
`/usr/bin/delidev`. Native sidecar resolution recognizes only that exact installed
layout; development/AppImage paths retain adjacent resolution with no PATH lookup.

Before notice inspection, both dry-run paths run the locked public Tauri CLI with `build` with `--no-bundle`. That CLI owns the versioned `tauri-cef` cache; a bare Cargo build downloads into its own build output and cannot prove the package cache is prepared. Preparation failure stops the sequence. The later installer build retains full original distribution identity and notice-byte verification. Original Chromium notices are copied unchanged to ignored `target/delidev-package-notices/<target>/` and supplied as a checkout-relative resource key; this avoids the pinned Windows resolver removing an absolute cache drive prefix. Packaged notice bytes remain compared against the original verified CEF cache.

Every verified package contains unchanged Apache `LICENSE`/`NOTICE`, the original
CEF license and the exact distribution's Chromium `CREDITS.html`. Distribution
identity and complete packaged notice bytes are checked; the report also records
the Chromium credits SHA-256. Ordinary `bundle:native` is development packaging;
only the verified dry-run path currently assembles the complete native notice set.


### Localization resources

English/Korean presentation follows [the localization contract](apps-delidev-localization-contract.md). The independent device Language controller and protected preference stay above connection and Settings visit ownership. Preserve stable category/enum/RPC values, drafts, focus, exact operation identities and original technical evidence. Native/widget catalogs generate typed resources during preparation, tests and packaging; widget language publication never advances server observation timestamps. App body language follows the saved device choice; OS-owned standard UI and widget gallery/selection guidance follow native localization. Fixture/build/package results remain distinct from actual platform and provisioned WidgetKit acceptance.

## Storage

Verified artifacts live under `target/delidev-dry-run/<target>/<source-commit>/`
with `verification.json` and `SHA256SUMS`. Reports bind exact source revision,
target, artifact name/size/digest, CEF version, notice digest and explicit signature
state. They retain `runtimeAcceptance: unverified` and never claim publication.
A per-checkout build lock excludes concurrent packagers; an interrupted lock needs
explicit inspection before removal. Temporary extraction/staging is cleaned on
ordinary failure. A successful result is atomically published, and an existing
commit's artifacts are never silently replaced. CI uploads only successful results
as seven-day workflow artifacts, with no public release or tag.

## Security

Build children receive the bounded system/tool environment only. Windows adds
explicit MSVC/SDK and OS lookup fields; secret-bearing proxy, signing, notarization,
updater and publication variables and executable-injection flags are excluded.
Windows pnpm runs through its JavaScript entry, avoiding `cmd.exe` command-string
construction. GitHub checkout credentials are not persisted, permissions are
`contents: read`, and no signing environment or repository secret is referenced.
LFS is hydrated before compilation; generated `dist` remains untracked output.

The updater-input command and separate manual updater-input workflow reuse this verified native matrix and assembles the update formats: macOS DMG, Windows NSIS executable, Linux AppImage and one dedicated Worker executable per target. It embeds the same public version/source revision in the Go sidecar/Worker, retains exact artifact names, imported notices and immutable revision-bound input metadata, and does not use a production root or signing credentials. Updater assembly re-prepares current source inputs under the same checkout lock, validates Worker architecture, checks Windows installer/product unsigned state, and rechecks exact source revision and cleanliness before atomic publication. Retained basic reports do not pin mutable native outputs. The pinned CEF AppImage path requires an explicit original `share/DeliDev/delidev-desktop` target because upstream copies its Debian bin symlink alone; inspection checks sharun launchers, real `shared/bin` product executables, `bin` CEF resources and `lib/DeliDev` notices. Upstream currently retrieves quick-sharun/hooks from mutable external branches; keyless build results do not establish reproducible or production supply-chain acceptance. Preserve this upstream constraint until the runtime packager owns pinned tools. The [updates contract](cmds-delidev-updates-contract.md) owns production manifest readiness, independent verification and explicit maintainer publication.

## Logging

Build tools emit their ordinary diagnostics. Verification emits bounded status
and typed failure descriptions; its JSON contains package provenance only, never
account data, environment dumps or signing material. A failed check must not leave
a successful verification report. Revision/cleanliness checks consume Git stdout only; stderr warnings are not source state. A rejected final publication emits bounded revision-change and entry counts plus at most sixteen allowlisted tracked repository source paths. Unknown/untracked names and values remain excluded.

## Build and Test

Run `pnpm --dir apps/delidev test`, `pnpm ci:workflows`, `pnpm ci:contracts` and root
`cargo test` after native source changes. Header/resource/notice tests use temporary
files and cover wrong architecture, truncation, invalid PE offsets, duplicate
extracted executables, missing resources and launcher redirection. The workflow
contract test guards manual dispatch, LFS hydration, read-only permissions and
verification before upload. The default-off `workspace_fixture_only` mode runs only the closed temporary Windows storage regression list for namespace claims, maximum inventory, publication/journal recovery, aggregate observation and private-path admission; it skips package planning/assembly, retains read-only credentials and never replaces complete CI or platform acceptance. Full local native packaging requires a clean commit;
matrix source validation does not count as six successful platform builds.

## Dependencies and Integrations

This workflow uses the repository's locked pnpm/Rust/Go dependency graph and
existing frontend/API generation. Native runner labels follow
[GitHub's runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).
Native resource layouts follow the pinned Tauri bundler, not unversioned examples.
Imported CEF/Chromium licensing remains distinct from repository ownership.

## Change Triggers

Update this contract, scoped `apps/delidev/AGENTS.md`, workflow tests, desktop
contract and validation records in pull requests, issues and CI logs/artifacts
together when targets, runtime pins, bundle layouts,
notice sources, verification or publication boundaries change.

## References

- [Project](project-delidev.md)
- [Desktop contract](apps-delidev-desktop-contract.md)
- [Repository defaults](repository-defaults.md)
- [License contract](repository-license-contract.md)

The app Cargo manifest has exact-path LF checkout normalization. The pinned Tauri CLI parses and rewrites it before compilation, normalizing CRLF input even when its TOML meaning is unchanged. Packaging retains the strict source-revision and clean-worktree guard; canonical checkout bytes avoid that platform-only rewrite without accepting real source changes.

Desktop runtime selection uses `tauri_runtime_cef::CefRuntime` and `Cef` attributes, with the runtime-owned helper entry point and extension APIs. macOS/Linux require Chromium sandboxing; Windows is the owner-approved unsandboxed executable-host exception until upstream provides its broker. Linux dialogs use the `rfd` XDG portal/Tokio backend (with `zenity` for confirmation), and GTK initialization uses GTK4. Static package checks do not establish dialog or sandbox runtime acceptance. The shared CLI distribution and cache contract is [prebuilt dependencies](repository-prebuilt-dependencies-contract.md).

Linux Debian packages require the XDG portal service, an installed portal implementation, and `zenity` so the new dialog backend is available after installation. AppImage hosts must provide these services and `zenity`; the archive does not supply a system D-Bus portal service.

AppImage updater dry runs use the verified helper staging defined in `repository-prebuilt-dependencies-contract.md`. The pinned CEF bundler copies native binaries directly into `bin`; the earlier Debian share-directory copy workaround must not be used because its duplicate ELF lacks adjacent CEF libraries. Packaging inspection does not establish native AppImage runtime acceptance.
