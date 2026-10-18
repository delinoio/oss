# FixKey Desktop Contract

## Scope

Owns the `apps/fixkey` Tauri 2 desktop application (component `desktop`): settings UI, permission dialog, candidate popup, tray, local settings, diagnostics, update flow and distribution. `apps/fixkey/src-tauri` is an explicit Rust workspace member.

## Runtime and Language

Tauri 2 shell in Rust with a React UI built by Rsbuild. Raw input and morphology processing stay in `crates/fixkey-core`. Prefer React Query for UI server-state when available. Target platforms: `macos-arm64`, `windows-x64`, `linux-x11-x64`. There is no backend, account service, public API, billing, synchronization or mobile application.

## Users and Operators

Individual desktop users who type Korean and English. Support uses GitHub Issues; there is no separate support service or public documentation website.

## Interfaces and Contracts

Settings window (reference viewport 840×680), left navigation sections in this order:

```ts
enum SettingsSection {
  General = "general",
  ExcludedApps = "excluded-apps",
  Shortcuts = "shortcuts",
  Updates = "updates",
  DiagnosticsAndHelp = "diagnostics-and-help",
}
```

- General: correction status with pause and resume, enabled languages (both on by default), login startup (off by default), UI language and theme.
- Excluded Apps: empty state, app selection, list and removal. Persist each exclusion by a stable application identity, never by display name or a mutable path alone: macOS bundle identifier, Windows AppUserModelID when present otherwise the executable's full path, and Linux X11 `WM_CLASS` class together with the desktop-entry ID when available. Match on the identity key; the display name is presentation only. When the identity changes or cannot be resolved, do not silently drop the entry: keep it listed as unresolved and keep correction off for a target whose identity cannot be established.
- Shortcuts: key capture for the manual-candidate shortcut, unassigned by default, with registration and conflict errors that show the reason.
- Updates: current version, manual check, available update, consent, progress and failures.
- Diagnostics and Help: error codes, open-log-folder, delete-logs, localized help and a GitHub Issues link. Help covers installation, permissions, supported layouts, correction, undo, manual candidates, exclusions, privacy, updates, troubleshooting and log deletion.
- Apply and persist settings immediately. On persistence failure, keep the previous value and show an error. Scroll at smaller sizes or larger text scaling instead of clipping.
- Coordinate shortcut and login-startup registration with atomic settings publication. Native registration failures leave the prior registration intact. If settings publication fails after registration, restore the previous OS registrations. If restoration fails, clear transient input, keep correction inactive and report that preference reconciliation is required; an explicit recheck reapplies the saved preferences before correction can resume.
- There is no input history or correction-history screen.

Permission dialog (reference viewport 540×440): explain why accessibility access is needed, that input stays on the device and that password fields are excluded. Adapt instructions and actions to the permission or accessibility service each OS requires. Provide the system-settings action, recheck and dismiss/later. Correction stays inactive until required permissions and dictionaries are ready, then activates.

Candidate popup (reference viewport 360×240): opened by the global shortcut near the input location and kept within the visible display. Do not open it when the location or replacement target cannot be established. Show the original, enabled-language candidates and validation results. Arrow keys select, Enter applies and Esc closes. Opening the popup preserves the target context: the popup must not make the source editor lose its identity as the captured target. Use a non-activating popup with separate key capture while it is open, or an engine-owned focus transition that records the popup as the expected focus change, retains the original target and revalidates it (target, caret, selection and text) immediately before writing. Focus moving to any other window or application invalidates the popup.

Presentation and accessibility: restrained white and light-gray surfaces, dark text, blue interactive emphasis, visible selection and an equivalent dark theme. Default to the system theme; theme and UI language are user-selectable. Use Korean UI when the OS language is Korean, otherwise English. Provide Korean and English UI and help. Support keyboard navigation, visible focus, accessible names and states, and screen-reader semantics. Dialogs close with Esc; a form with one critical input focuses it when shown. Never communicate errors or status by color alone.

Tray and notifications: tray actions are pause/resume correction, settings and exit. Closing the settings window hides it and keeps the process and correction running (prevent exit when the last window closes); only the tray Exit action, or an update installation, terminates the process. Tests cover close, reopen and exit. Show unsupported states and failures in the tray and settings. Send an OS notification when the state changes, not on every keystroke.

Updates and distribution:

- Publish macOS ARM64 DMG, Windows x64 NSIS EXE and Linux X11 x64 AppImage through GitHub Releases.
- Publish alongside those installers every artifact the updater consumes: the platform updater bundles (for macOS the `.app.tar.gz`), their detached signatures for all three targets, and the release metadata the update endpoint serves (for example `latest.json`). Startup and manual update checks must have a verifiable payload to install.
- Require macOS code signing and notarization, Windows code signing and update signature verification on all three targets. Signing identities and release credentials are external release dependencies and are never committed.
- Check stable releases over HTTPS at startup and every 24 hours while running, and on manual request. After an offline or failed check, wait for the next scheduled or manual check without a rapid retry loop.
- Notify the user when an update is available. Download and install only after consent. Stop correction and clear transient input before installation. Reject failed downloads and invalid signatures without installation. Report installation failures with restart and manual reinstallation instructions. Preserve settings during recovery. Allow manual reinstallation of a previous signed package; never downgrade automatically.
- No remote feature flag or staged rollout system.
- Preserve the repository's affected CI behavior and macOS-runner restrictions; put necessary macOS release work behind the owning release gates.

## Storage

- Store settings locally for the current OS user. Preserve them across updates and manual recovery. Settings use a versioned format with a `schemaVersion` field. A newer release writes only changes that older supported releases can read, or first writes a backup of the previous file (`settings.v<N>.bak`) before migrating, so manual reinstallation of a previous signed package can restore it. An older binary that finds a newer unreadable schema leaves the file untouched, runs with defaults in memory, reports the reason in status, and offers restore from the backup. Never silently replace unreadable settings or user configuration.
- Never persist input text, reconstructed candidates, document content, window titles or correction history.
- Logs: retain at most seven days and at most 10 MiB total, deleting the oldest logs when either limit is exceeded. Settings provide log deletion and log-folder access.
- `src-tauri/src/settings.rs` owns the persisted schema and atomic file replacement. `preferences.rs` coordinates native preference changes and the Rust correction session. `diagnostics.rs` owns bounded JSON diagnostic files, version/platform envelopes and the tracing filter. The filter accepts only FixKey targets; input text and configuration values must never be tracing fields. `NO_COLOR` disables operator-console ANSI output. Log deletion removes only owned log files and leaves unrelated files untouched.

## Security

- No telemetry, remote crash reports or diagnostic log upload. Network use is limited to update checks and downloads.
- OS permissions, update verification, signing and local privacy requirements apply. Server authentication, tenancy, billing, quotas, remote dashboards, alerting, SLOs, data import/export and cloud migration do not apply.
- Preserve upstream licenses and notices in distributions, following [the repository license contract](repository-license-contract.md).

## Logging

Use `tracing` structured events without input content: errors, processing durations, correction outcomes and skip reasons, each with application version, platform and stable error or reason codes.

## Build and Test

- Frontend: `pnpm test` from the frontend directory. Rust: `cargo test` from the repository root, plus applicable formatting and build checks.
- Desktop Rust source and resource changes participate in the central affected Rust test and clippy selection. Keep the default affected manual mode and native-runner eligibility unchanged.
- Required automated tests: settings persistence failures, empty exclusion lists, localization, keyboard focus, accessible states, light and dark presentation; shortcut registration failure, candidate navigation, invalid-candidate labeling, Esc and target invalidation; log redaction, rotation, retention and deletion, and absence of input persistence or telemetry; mocked update checks, scheduling, offline behavior, consent, download failure, invalid signatures, installation failure and settings preservation.
- Mocks, fixtures and builds do not establish actual OS or application behavior (accessibility reads and writes, global shortcuts, signing, package installation). Automated tests and package inspection are not substitutes for native acceptance. Native acceptance on each target platform is a separate activity required before claiming verified compatibility or shipping a release. Record every native check that was not performed in pull requests, issues and CI artifacts together with source revision, commands and results, and never claim compatibility for it.

## Desktop integration

- `src/App.tsx` owns the localized settings, permission dialog and transient candidate view. `src/api.ts` restricts IPC to the local Rust command surface. The frontend uses React Query for settings and updater state; native input processing does not depend on WebView polling.
- `src-tauri/src/desktop.rs` owns Tauri windows, tray, commands, startup and shortcut reconciliation. The candidate window remains non-activating; temporary global arrow, Enter and Esc registrations preserve the editor target. Stale candidate generations are rejected.
- `src-tauri/src/native.rs` owns one bounded, transient input worker. It proves that the observed keys explain the complete accessibility edit before evaluation; missing events, unrelated edits and unknown targets discard the context. `fixkey-core/native` enables the platform adapters without adding native dependencies to ordinary engine tests.
- Development frontend: `127.0.0.1:46321`, strict port binding. Run `pnpm --filter fixkey-desktop dev` for Tauri or `dev:frontend` for UI development. `build:frontend` generates the ignored frontend `dist`; `build` creates native packages with the `desktop` feature. Generate frontend output before native compilation with the custom protocol.
- The updater uses a build-time public `FIXKEY_UPDATER_PUBLIC_KEY`, the official GitHub Releases API and immutable `fixkey-v<semver>` release assets. `fixkey-latest.json` selects signed platform payloads. Missing keys or unverifiable metadata leave updates unavailable. Development builds do not contain signing credentials.

Minimum dependency baselines are macOS 11 on ARM64, Windows 10 x64 with WebView2, and Linux x64 with X11, GTK 3, WebKitGTK 4.1 and an AT-SPI accessibility bus. These are build/runtime prerequisites, not verified application compatibility. Wayland remains unsupported. See [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/) and [supported platforms](https://v2.tauri.app/reference/platform-versions/).

## Dependencies and Integrations

- `crates/fixkey-core` for all correction behavior.
- Tauri 2, React and Rsbuild, pinned with recorded sources, hashes, licenses and notices.
- GitHub Releases for update delivery.

## Change Triggers

Update [project-fixkey.md](project-fixkey.md) and [the core contract](crates-fixkey-core-foundation.md) when UI contracts that depend on engine identifiers, update flow, storage or privacy rules change. Update `apps/AGENTS.md` and root `AGENTS.md` only for ownership or development-rule changes.

## References

- [Project index](project-fixkey.md)
- [Core contract](crates-fixkey-core-foundation.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
