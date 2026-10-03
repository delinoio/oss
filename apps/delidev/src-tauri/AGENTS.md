# DeliDev src-tauri ownership

- `src/oauth.rs` and `src/oauth_host.rs` implement only the closed OpenRouter browser/callback infrastructure under `docs/cmds-delidev-account-oauth-contract.md`. Capture/recheck trusted-window native epochs, selected server and saved-window instance; bind both loopback families to one ephemeral port and retain one generation per opening. Bound parsing and single-use code transfer, return constant no-store/no-referrer callback pages, clear transient bytes and join listeners/openers on disposal. Scoped tombstones block a queued Begin after unknown-response disposal; late predecessor cleanup cannot revoke replacements. Rust never exchanges a code or stores a provider credential, broadcasts callbacks or exposes a generic opener. Settings departure removes local authority without implicit business cancellation.

Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- Local connection and registration permission errors must explain both device authorization and owner-only private state; do not equate a permission denial with an owner being unable to read files or a revoked registration. Give macOS/Linux guidance of 0700 for private directories and 0600 for private files, preserve existing data, and never automatically change permissions or offer replacement without verified revoked-client evidence.

- CEF URL getters block on the native UI loop at the pinned revision. Keep every command reaching URL authorization asynchronous and move native callback authorization off that loop; recheck shutdown and notification generations before publishing navigation. Bundling must enable the local `custom-protocol` feature as well as `tauri/cef`.

- Enable native accessibility for each trusted CEF document after load, including saved-server windows created after the initial accessibility notification. Keep native content out of logs, use the UI-loop callback and retain the matching pinned CEF dependency. Record the Exit event, notification/tray task joins and runtime return separately, without equating an event or join with observed process termination. Record delayed native browser teardown separately from independent server lifetime.

- Desktop uses CEF at the existing immutable Tauri revision and the CEF helper entry point, with macOS 13 retained. Bind capabilities to trusted webview labels, never window-wide labels that grant future external child views app authority. Keep engine migration distinct from account-browser persistence and cleanup evidence.

- Refresh tray overview and the selected UTC-day usage summary every 15 seconds, including hidden windows, so native publication cannot keep resetting freshness for unchanged usage. Account reads refresh every 30 seconds; failed or missing usage stays unavailable.

- macOS application reopening restores the existing main window through the same unminimize/show/focus path as tray activation. Never recreate the renderer, reset its geometry or submit work to restore presentation. Follow `docs/apps-delidev-desktop-contract.md`; keep actual OS reopen evidence separate from tray-menu activation.

- Session files follow `docs/cmds-delidev-files-contract.md`. Use owner/client Connect Query reads in the right session application area, preserving the mounted composer and unsent draft. File contents are inert text; retain exact sizes, explicit binary/truncated/unavailable states, scoped repository selection and stale-page errors. Cancel inactive reads and discard their nonpersistent query data when navigation or closing removes the view. No filesystem Tauri binding or link/citation-derived authority.

- The Inbox is one persistent paginated list/detail workspace for requests and terminal results; tray activation selects the exact item through a fresh `GetInboxEntry` read and never marks it read. Use server-side source/read-state enum filters and pages of 20. Retain typed response drafts only in connection memory, bound them to the original interaction ID, revision and request identity, cap them at 4 MiB/1,000 nonempty requests, preserve stale drafts read-only, and allow an uncertain retry only after a fresh authorized source read. Keep active response controls gated by the current source/session state; retain the workspace across surface navigation and keep notification polling separate.

- Claude Code discovery uses only its pinned stream-json initialization profile in a fresh private bare runtime, with no inherited credentials, OAuth/keychain access, project extensions, persistent sessions or prompts. Accept one exact correlated control response with no token, explicit closed permission/remote-control state and bounded typed descriptors; reject unknown/duplicate/trailing output. Bound and join both streams and the input writer, preserve cleanup uncertainty, and publish only the handshake outcome. Model/agent advertisements cannot grant account readiness or execution; Claude session execution and subscription login require their separate adapters.

- Follow `docs/cmds-delidev-credentials-contract.md` for protected server secrets. Keep wrapping material in the native OS store, payloads authenticated-encrypted, and immutable request references/deletion markers durable across uncertainty. Never silently unlock, enumerate user credentials, replace a missing sealed key, or use a plaintext fallback. Native tests must use temporary keychains/UUID entries or an explicitly disposable Secret Service container; never lock a user's shared collection. A stored secret does not establish account readiness.

- Native desktop registration inspection/recovery must pass the compiled fixed endpoint as a Go-side guard before recovery intent or pairing. An endpoint mismatch cannot create or publish a replacement; post-publication connection validation is additional defense, never the first endpoint check.

- Protected browser contexts follow `docs/cmds-delidev-browser-contract.md`. Reserve exact presentation IDs on the UI loop before asynchronous Go reads, releasing the superseded raw child and invalidating late creation even if replacement preparation fails. Keep raw external CEF children outside Tauri browser-side IPC handlers and initialization scripts, explicitly reject their process messages despite the pinned runtime's shared renderer stub, and count native creation/close callbacks through exit. Never remove a profile before poller join, all native close proofs and independently observed CEF shutdown return; retain original offline removal intents and exact acknowledgment retries.

- Before forgetting a saved connection, persist its original browser scope for local purge. Discover completed CLI removals through retained connection tombstones; deny reopen and purge only after independently completed CEF shutdown, without using deleted client credentials or claiming remote acknowledgment.

- Browser validation, profile reads and durable writes run on serialized workers without holding native state during I/O. Post only CEF/native presentation work to the UI loop, recheck trusted documents and exact generations after preparation, coalesce bounded address updates, and join their worker before post-shutdown purge.

- Stage durable tab-control writes before exact reservation validation. Fence final publication against UI reservation acceptance from a blocking worker; the UI callback must never acquire that fence or wait for storage, and stale preparations must leave the prior tabs intact.

- First-tab initialization must use the same staged publication fence and reservation check; a superseded initial open cannot publish its starting address into a replacement profile.

- Bound the worker's UI-reservation wait and cancel unexecuted late callbacks before releasing its publication fence. Native shutdown must not leave the address-worker join waiting for a reservation callback that the UI loop can no longer deliver.

- Observed-address writes must stage first and recheck the profile, child generation and current reservation under the same worker publication fence before replacing durable tabs.

- Quit closes address-callback acceptance under the bounded queue gate and drains previously accepted updates on the tracked worker. Shutdown alone cannot discard them; exact generation, reservation and removal checks still reject invalid writes. Join that worker before profile purge without making the UI wait for storage.

- Retain asynchronous raw-child creation failure only for its exact profile, generation and reservation. Native state polling and ordinary controls must surface the typed failure until explicit presentation retry; stale creation failures must not poison replacements or hide tracked removal.

- Initial native geometry/host failures in the after-created callback follow the same exact failure retention before requesting child closure. The close callback clears the child handle without clearing that failure; an explicit replacement presentation starts fresh.

- Shared-profile tab replacement must attempt every affected child even when an earlier creation fails. Retain each exact view's failure and return the first failure only after all replacements have been attempted.

- Replace shared browser children only when their selected tab identity changes. Closing a background tab or selecting the current tab preserves page state, navigation history, pending creation and exact retained failure.

- Before replacing a selected/final tab child or superseding a presentation, synchronously unmap every old child before dropping handles or advancing generations. An unmap failure retains the original views and cleanup identity; request asynchronous close only after releasing native state. Removal/quit retain each child handle until its exact callback, so concurrent Hide cannot mistake a delayed close for invisibility.

- Discovered profile removal synchronously unmaps every matching child before requesting asynchronous CEF closure. Attempt every user even if one unmap fails, retain original handles and presentation identities for exact Hide retries, and release native state before close requests. Failed unmapping never grants profile-purge authority.

- Browser Hide is idempotent for an exact original view: synchronously hide only its matching native child before acknowledging success, or retain ownership until its exact close callback proves absence. Pending native creation and failed native hiding keep the original identity pending and deny ordinary controls. Confirm absence without touching a superseding view and return no browsing data from cleanup, including absent or unrelated-window requests. Preserve trusted-document checks and offline closure; native close accounting still gates process shutdown.

- Actual native window close and explicit saved-window destruction must invalidate its browser reservation and pending context creation and request raw-child closure on the UI loop before destroying the parent. The saved-window Destroyed fallback checks the original binding instance before closing its exact window view and releasing the binding. Tray hiding preserves presentation; window closure retains profiles and native callback counts until independent shutdown.

- Browser cleanup discovery uses an independent read-only controller with a two-second joined-child deadline; never hold the interactive connector gate through offline polling. Stage saved-connection purge only after fallible window setup, bind its exact original removal identity, and require a fresh retained Go removal receipt plus complete CEF shutdown before deleting bytes. Unchanged paired evidence cancels only the unaccepted intent; uncertain acceptance and independent account removal remain pending. Follow `docs/cmds-delidev-browser-contract.md`.

- Advance the durable account-removal cursor before acknowledgment attempts, rotating retained intents across process exits so offline receipts cannot starve later local profile purges. Keep original request/revision ownership and the existing per-exit bounds.

- Forgotten-scope and account-removal cleanup have independent 45-second/64-intent budgets after native shutdown. A depleted forgotten-scope budget cannot prevent account cursor advancement or local account-profile purge.

- Quit denies presentations immediately but retains the CEF event loop until the worker's final bounded removal discovery finishes and every raw child closes. Perform that final discovery even when quit arrives during the poll sleep; never move its sidecar reads or durable intent writes onto the UI loop.

- Fresh main hosts own one joined launch before supervision; helpers/saved windows/renderer observations cannot replay it. Recheck current durable Stop and fixed client authority before returning cached success. Explicit Retry preserves Stop; advanced Start shares Go desktop-launch service admission and cannot bypass installed registration ownership. Advanced Start/recovery adopt only verified outcomes. Go automatic/desktop startup uses one 35-second aggregate wait budget below the native 40-second command bound; never reset it per joined phase. Cancel and join short controllers on Quit, retaining detached lifetimes. Keep lifecycle logs bounded and secret/output/path-free.

- Device appearance follows issue #1238 and the desktop contract. Only trusted `main` and registered `server-*` documents may read/update the versioned `appearance.json` in `app_config_dir()`. Keep it outside server data, pairing, backup and configuration transfer. Serialize bounded validated reads and atomic synchronized writes, publish committed changes to trusted windows only, and order snapshots with non-wrapping process revisions. Preserve invalid/newer files with System fallback; failed writes retain the last committed preference and uncertain outcomes require inspection before retry. Never grant generic filesystem authority or expose file paths/raw OS errors in diagnostics. Tests inject temporary directories.

- `choose_repository_folder` is one asynchronous folder-only dialog per process under issue #1142. Accept no renderer-selected path/authority; authorize trusted main or the exact saved-server binding before opening and after completion, bound the selected native path and return cancellation without Git/content access. Keep the repository-pinned optional rfd version and phase/code-only logs.

- Subscription recovery notifications use the closed subscription-recovery kind and fixed masked product text. Renderer input remains limited to original opaque claim/inbox identities; no account identity, alias, quota value or provider content enters native presentation.

- `worker_network_control` grants only Prepare/Import/Status on the trusted main or exact saved-server window's existing same-computer Worker. Native selects the scope, rejects foreign machine/input and rechecks saved-window generation after the joined sidecar operation. No renderer path, endpoint, key or saved-profile selector is accepted. Go owns X25519, import validation and protected storage; ciphertext/digest are bounded transient input and public result parsing rejects extra/private fields. Never implicitly register, start, stop or replace the Worker.
