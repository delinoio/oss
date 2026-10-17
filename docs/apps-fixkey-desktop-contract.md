# FixKey Desktop Contract

## Scope

Owns the planned `apps/fixkey` Tauri 2 desktop application (component `desktop`): settings UI, permission dialog, candidate popup, tray, local settings, diagnostics, update flow and distribution. The path is `planned` until its skeleton is created together with the first runtime implementation.

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
- Excluded Apps: empty state, app selection, list and removal.
- Shortcuts: key capture for the manual-candidate shortcut, unassigned by default, with registration and conflict errors that show the reason.
- Updates: current version, manual check, available update, consent, progress and failures.
- Diagnostics and Help: error codes, open-log-folder, delete-logs, localized help and a GitHub Issues link. Help covers installation, permissions, supported layouts, correction, undo, manual candidates, exclusions, privacy, updates, troubleshooting and log deletion.
- Apply and persist settings immediately. On persistence failure, keep the previous value and show an error. Scroll at smaller sizes or larger text scaling instead of clipping.
- There is no input history or correction-history screen.

Permission dialog (reference viewport 540×440): explain why accessibility access is needed, that input stays on the device and that password fields are excluded. Adapt instructions and actions to the permission or accessibility service each OS requires. Provide the system-settings action, recheck and dismiss/later. Correction stays inactive until required permissions and dictionaries are ready, then activates.

Candidate popup (reference viewport 360×240): opened by the global shortcut near the input location and kept within the visible display. Do not open it when the location or replacement target cannot be established. Show the original, enabled-language candidates and validation results. Arrow keys select, Enter applies and Esc closes. Opening the popup preserves the target context.

Presentation and accessibility: restrained white and light-gray surfaces, dark text, blue interactive emphasis, visible selection and an equivalent dark theme. Default to the system theme; theme and UI language are user-selectable. Use Korean UI when the OS language is Korean, otherwise English. Provide Korean and English UI and help. Support keyboard navigation, visible focus, accessible names and states, and screen-reader semantics. Dialogs close with Esc; a form with one critical input focuses it when shown. Never communicate errors or status by color alone.

Tray and notifications: tray actions are pause/resume correction, settings and exit. Show unsupported states and failures in the tray and settings. Send an OS notification when the state changes, not on every keystroke.

Updates and distribution:

- Publish macOS ARM64 DMG, Windows x64 NSIS EXE and Linux X11 x64 AppImage through GitHub Releases.
- Require macOS code signing and notarization, Windows code signing and update signature verification on all three targets. Signing identities and release credentials are external release dependencies and are never committed.
- Check stable releases over HTTPS at startup and every 24 hours while running, and on manual request. After an offline or failed check, wait for the next scheduled or manual check without a rapid retry loop.
- Notify the user when an update is available. Download and install only after consent. Stop correction and clear transient input before installation. Reject failed downloads and invalid signatures without installation. Report installation failures with restart and manual reinstallation instructions. Preserve settings during recovery. Allow manual reinstallation of a previous signed package; never downgrade automatically.
- No remote feature flag or staged rollout system.
- Preserve the repository's affected CI behavior and macOS-runner restrictions; put necessary macOS release work behind the owning release gates.

## Storage

- Store settings locally for the current OS user. Preserve them across updates and manual recovery. Never silently replace unreadable settings or user configuration.
- Never persist input text, reconstructed candidates, document content, window titles or correction history.
- Logs: retain at most seven days and at most 10 MiB total, deleting the oldest logs when either limit is exceeded. Settings provide log deletion and log-folder access.

## Security

- No telemetry, remote crash reports or diagnostic log upload. Network use is limited to update checks and downloads.
- OS permissions, update verification, signing and local privacy requirements apply. Server authentication, tenancy, billing, quotas, remote dashboards, alerting, SLOs, data import/export and cloud migration do not apply.
- Preserve upstream licenses and notices in distributions, following [the repository license contract](repository-license-contract.md).

## Logging

Use `tracing` structured events without input content: errors, processing durations, correction outcomes and skip reasons, each with application version, platform and stable error or reason codes.

## Build and Test

- Frontend: `pnpm test` from the frontend directory. Rust: `cargo test` from the repository root, plus applicable formatting and build checks.
- Required automated tests: settings persistence failures, empty exclusion lists, localization, keyboard focus, accessible states, light and dark presentation; shortcut registration failure, candidate navigation, invalid-candidate labeling, Esc and target invalidation; log redaction, rotation, retention and deletion, and absence of input persistence or telemetry; mocked update checks, scheduling, offline behavior, consent, download failure, invalid signatures, installation failure and settings preservation.
- Perform no actual OS or application interaction tests and no installation-package inspection. Record them as unperformed in pull requests, issues and CI artifacts together with source revision, commands and results.

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
