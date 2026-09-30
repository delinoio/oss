# DeliDev protected account browser

## Scope

Issue #1087 implements the session browser boundary required by issue #964.
Go owns profile registration, current authorization, exact receipts and durable
per-device account-removal obligations. `cmds/delidev-cli/internal/domain/browser.go`,
`internal/store/browser.go`, `internal/server/browser.go`, `internal/cli/dispatch_browser.go` and `internal/cli/browser.go`
are its source owners. `apps/delidev/src/session-browser.tsx` presents controls;
`src-tauri/src/browser.rs` owns private paths and `browser_host.rs` owns raw CEF
contexts and children. Native presentation is distinct from product RPC.

## Runtime and Language

The server and CLI use Go. React 19.2.8 uses generated Connect Query bindings.
Rust uses the existing immutable Tauri CEF revision without changing its pin.
Trusted app documents remain incognito. External content uses a separate CEF
request context and raw native child, without Tauri initialization scripts, scheme
factories, browser-side IPC handlers, app permissions or client authorization
headers. The pinned runtime installs a shared JavaScript message stub in renderer
pages; the external child's client rejects every process message, so that stub has
no product or native authority. Do not attach a Tauri browser client to an external
child.

## Users and Operators

Only a paired client can register or operate its computer's profile identity.
Owner and Worker credentials cannot substitute a device. Owner and paired clients
can inspect non-secret account cleanup counts. Repaired or newly paired identities
cannot silently adopt another device's obligations. Revoked devices remain in
cleanup inventory; inability to acknowledge never means successful removal.

## Interfaces and Contracts

`BrowserService` has service-owned typed `BrowserProfile`, `BrowserProfileState`
and `BrowserCapability` messages/enums. `GetBrowserCapabilities` advertises
`PROTECTED_DEVICE_PROFILE_V1`; it does not attest to native platform acceptance.
No reserved shared enum number, existing-message field number or future SQLite
migration version is consumed.

- `RegisterBrowserProfile` binds the exact session UUID/revision, its actual
  selected account, acting paired client and UUID-v7 request. A transaction
  verifies current session/account ownership and finds or creates one identity
  for the original server/device/account. It never accepts paths or browsing data.
- `GetBrowserProfile` and `ListBrowserProfiles` recheck paired authority. Lists
  filter the original device before pagination (default 50, maximum 200); signed
  cursors bind server and device. Foreign identities are indistinguishable from
  absent profiles. The profile inventory is capped at 2,000 records per device.
- `GetAccountBrowserCleanup` accepts an account UUID even after its configuration
  was removed. Its active/pending/removed counts describe tracked obligations.
  Zero tracked profiles cannot attest to external browser data or unregistered
  software. Workers cannot inspect this owner/client operation.
- `ConfirmBrowserProfileRemoval` requires the original profile UUID/revision,
  original deletion request and owning paired client. Only removal-pending can
  advance to removed. An exact receipt returns current state and never performs
  another native deletion or resurrects an active profile.

The CLI equivalents are `browser-profile capabilities`, `register --id SESSION
--revision N --account-id ACCOUNT`, `status --id PROFILE`, `list --page-size N
--page-token TOKEN`, `account-status --account-id ACCOUNT`, and `confirm-removal
--id PROFILE --revision N --deletion-request-id REQUEST`, with the common original
`--request-id` for mutations. Confirmation is an explicit ownership attestation
for clients which actually completed native cleanup, not an automatic CLI action.
`browser-storage prepare` only prepares the native client's private cache root.

The session Browser button opens a side panel while retaining the conversation
and unsent composer. The user selects an HTTP(S) address before registration;
there is no automatic provider login or implicit external navigation. Back,
Forward, Reload and bounded local tab controls use trusted native commands. A
native presentation UUID is reserved on the UI loop before asynchronous
authorization. Reservation releases the previous raw child and invalidates its
pending creation, even when the replacement authority read or storage preparation
fails. Stale opens, controls and cleanup cannot replace a newer panel instance.
The persistent closed compact sidebar dialog and open nonmodal wide sidebar
region do not block browser presentation. Visible dialogs, hidden panels and
clipped geometry cannot leave an external child above app UI.
Closing the panel releases its view, retaining its request context/profile.
Profile directory traversal, tab reads, URL policy preparation and durable writes
run on serialized storage workers without holding the native state lock during
I/O. Trusted documents and presentation/control generations are rechecked before
UI-affine CEF publication. Tab controls stage and fsync private replacement files
before checking the exact reservation at publication. A separate worker fence
serializes the final replacement with reservation acceptance on the UI loop;
only the worker waits for that callback, and native state is released during I/O.
Superseded staged writes are discarded without changing durable or live tabs.
Observed-address writes use the same fence and recheck exact profile, child
generation and reservation after staging, so replacement opens retain the last
accepted address rather than consuming a superseded callback.
Address callbacks coalesce at most one latest update
per tab (64 profiles × 16 tabs) into one tracked worker; callbacks perform no disk
work. Hide/Resize remain independent of pending storage. The worker joins after
runtime return and before any directory purge.

## Storage

Browser profile metadata extends the existing paired-client `Device` JSON
record. Its own UUID/revision and typed non-secret ownership/state persist under
the server's exclusive SQLite authority. Atomic device events and request receipts
share the transaction. The serialized device document retains existing limits;
schema remains 25 after the independent native-accounting migration, with no browser-specific table or migration. Existing snapshots,
backups and absent-field historical device records remain valid. One account per
device is enforced by the transaction and validated bounded inventory, rather
than consuming a reserved migration for an index.

Native cache paths are constructed solely from canonical server/device/account
UUIDs beneath the prepared owner-private `browser-data/profiles` root. The
actual initialized CEF request-context path must equal that exact path. A live
process shares one context for each profile, including multiple session windows. At most 64 request contexts are retained per
native process; reopening the desktop releases that runtime capacity without
deleting profiles.
Tabs are bounded to 16 and 256 KiB, atomically saved locally with private files;
cookies, storage, history and browser credentials belong to that context. They
never enter SQLite, RPC bodies, configuration transfers or Worker workspaces.
Session deletion, Archive and panel closure do not create removal obligations.

Account configuration deletion atomically marks every registered device profile
removal-pending with the original deletion request and increments its revision,
including offline/revoked clients. Configuration removal and browser cleanup are
separate outcomes. The account confirmation UI reports outstanding cleanup and
can read current counts after account removal.

A native poll reads the original local and saved client scopes, including unopened
windows. Cleanup inventory reads use an independent read-only sidecar controller
with a two-second joined-child deadline and cannot acquire the interactive
controller gate. Quit immediately denies new presentations and requests child
closure, then keeps the native event loop alive through a final worker discovery
pass with an eight-second scheduling budget. No new read starts after that
budget; an in-flight operation retains its existing joined-child bounds. Final
discovery can observe an account deleted during the preceding poll sleep, and
must finish before either zero-child exit or the last native close callback can
release shutdown. Offline scopes outside the budget remain discoverable on a
later launch. After exact pending ownership is observed, it atomically writes a private
removal intent before closing every profile user or discarding pending creation.
A removal intent permanently denies reopening. Cleanup is deferred through the
whole native application's shutdown: raw-child creation/close accounting keeps
CEF's event loop alive until its close callbacks run; background polling joins;
`app.run` must then return through the pinned runtime's `CefShutdown`.
Only afterward can the complete resolved profile directory be removed. Cookie or
cache clearing, an Exit event, a close request or a renderer acknowledgment is
not proof of full process cleanup.

Forgetting a saved connection finishes fallible window setup before staging its
original server/device/connection/pairing and exact removal-request identity.
Before the native client credential is removed, this durable marker denies new browser presentations,
and closes its raw children. The entire device browser directory is purged only
after the same independent CEF shutdown proof and a fresh local Go read of the
matching retained Removing/Removed receipt. An unchanged Paired record cancels
only the exact unaccepted intent, retaining profile bytes and independent account
removal denial. Unavailable or mismatched acceptance remains pending. A native
staging marker alone never authorizes deletion. Retained offline connection
removal tombstones also discover CLI removals and removals made while the desktop
was stopped. This local purge needs no deleted client credential and never claims
a server account acknowledgment or remote device revocation.

The original local removal intent remains after offline acknowledgment or an
uncertain response. Each independent scope is handled separately. Once native
shutdown is independently complete, local directory deletion can proceed offline;
the server remains pending until a fresh exact ownership/status read and original
confirmation succeeds. A retry does not reopen the profile or infer completion.
Each exit handles at most 64 intents within a 45-second loop budget, with the
existing 40-second sidecar command bound; remaining or uncertain intents stay
pending for a later process cleanup. A private durable account-removal cursor
rotates the ordered intent inventory before acknowledgment, so retained offline
receipts cannot repeatedly exclude later profiles from local purge. Deferred acknowledgments and exhausted
cleanup budgets emit structured pending state and preserve a successful normal
quit; failed local persistence or deletion remains a host failure. Generated cache data and native fixtures
remain untracked.

## Security

External navigation and resources deny product origins (including WebSocket
counterparts), app/IPC origins, the fixed local API/development ports, file/custom
schemes and URL credentials. IPv4-mapped IPv6 loopback addresses receive the
same fixed-port denial and explicit-origin requirement as IPv4 loopback. Only trusted address actions add loopback browsing origins to a bounded 32-origin
allowlist; restored local tabs retain their explicit origins. Redirects and resource
requests cannot extend that list. Popups, downloads, file pickers and permission prompts are
denied; script clipboard/paste access is disabled. Ordinary external HTTP(S) pages
receive only their profile's web credentials, never product or platform credentials.
The external CEF client has no app process-message handler or native capability.
Product controls are accepted only from trusted main/saved app documents and
independently reread current Go ownership before browsing mutations.

The Go-prepared root enforces private Unix permissions or owner-only inherited
Windows ACLs. Native path traversal, symlink/reparse directories and oversized or
invalid local documents fail closed. Atomic file replacement preserves original
pending identities. Account-removal denial precedes closing and flushing; browser
callbacks cannot write tabs after removal, and directory deletion follows CEF
shutdown so late browser flushes cannot recreate its contents.

## Logging

Go logs structured operation/request/replay state. Native logs structured browser
opening/closure/removal state and stable failures. Never log URLs, page titles,
tabs, cookies, storage, history, browser credentials, product tokens, native cache
paths or external content. External console messages and source URLs suppress Chromium's default console log.
The UI uses fixed native failure messages.

## Build and Test

Run the complete DeliDev Go race suite and vet from the root, `pnpm proto:check`,
`pnpm test` in `apps/delidev`, and root `cargo test`. Generate required build output
and hydrate consumed LFS assets before validation; remove generated `dist` output
before finalizing. Compile the desktop host against the unchanged pinned CEF.
Regression fixtures use temporary state and controlled native adapters only.
Record exact results and unresolved platform/native evidence under
`docs/evidence/delidev/issue-1087/`, distinguishing implementation verification
from real-account, real-renderer shutdown/flush and release acceptance.

## Dependencies and Integrations

The existing Go sidecar establishes paired authority. Browser RPCs reuse server
identity, cursor signing, SQLite receipts and authorization. Raw CEF profiles are
independent of trusted app webviews; app capabilities match trusted webview labels
only. Existing account credential-disconnection prerequisites remain in force.

## Change Triggers

Update this contract and the scoped desktop/CLI/protocol/client AGENTS files when
ownership, bounds, native lifetime, cleanup acknowledgments or wire semantics
change. Keep the project and docs catalogs linked. Shared numeric or migration
allocations must still follow the source-structure contract on main.

## References

- [DeliDev project](project-delidev.md)
- [Requirements](cmds-delidev-requirements.md)
- [Desktop client](apps-delidev-desktop-contract.md)
- [Source ownership](cmds-delidev-structure-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Repository defaults](repository-defaults.md)
