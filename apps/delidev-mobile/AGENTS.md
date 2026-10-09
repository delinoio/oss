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
