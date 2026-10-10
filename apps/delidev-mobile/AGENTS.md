# DeliDev mobile ownership

- Follow `../../docs/apps-delidev-mobile-contract.md` and the DeliDev project,
  shared API-client, sessions, Inbox, protocol and structure contracts.
- This app is a separate HTTPS remote client. Never add a Go/Worker/desktop
  sidecar, CEF, local execution or new business RPC to the platform bridge.
- Keep credentials and original pairing/mutation intent in device-only native
  protected state. Foreground synchronization is read-only. Unknown mutations
  require current original-target inspection and exact explicit retry.
- iOS minimum is 18; Android minimum API 31. Use the pinned Wry runtime and
  verified repository Tauri CLI. Native target build checks are mandatory.
- Keep generated native projects and all `dist` output untracked. Run `pnpm test`
  here for frontend changes and root `cargo test` for Rust changes. Automated
  browser/native builds do not establish real mobile device/account acceptance.
- The internal beta pipeline defaults to dry run. Preserve original candidate
  bytes, versions, source SHA, signer and checksum evidence. Unknown upload
  outcomes require exact authoritative reconciliation. Never publish a public
  track, provision owner accounts or invent credentials as part of fixtures.

- New session inherits current authenticated server/Project Plan defaults until an explicit mode choice. Missing, stale or invalid defaults retain drafts and require reinspection; frozen pending requests retain their original mode. Notification preference writes retain the original client revision and closed situation selection; legacy servers retain the combined-category compatibility shape.

- Persist all selected candidate-bound platform receipts before provider access. Missing recovery receipts remain Unknown. Apple proof requires nested COMPLETE state and complete bounded group pagination; Google staged edit membership is not distribution proof. Mark observation edits writable before any track mutation, commit the original edit, and reconcile exact published bytes without replacement uploads.

- Beta target defaults to both platforms. Explicit iOS-only candidates use schema 2 and require no Android code or credentials; preserve schema-1 both-platform candidates and exact target-bound provenance. Serialize all beta workflow runs without canceling original submissions. Bind candidate and receipt downloads to their independently verified original run IDs.

- Direct iOS Cargo builds must pass the configured minimum system version to the Swift linker; do not rely on its iOS 13 fallback or a warm local build.

- Android beta build hosts install the exact SDK package `platforms;android-37.0` with command-line tools 16111833; the integer-only `android-37` package is absent from the official inventory. This build SDK does not alter the API 31 runtime minimum.
