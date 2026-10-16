# DeliDev protected account browser

## Scope

The feature implements the session browser boundary required by the feature.
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
  Selection follows the latest retained continuation account change before Resume,
  otherwise the current/initial execution account. The panel uses that same account
  as its key and registration input, closing the prior presentation on a pending
  switch without rewriting historical execution attribution or the unsent composer.
- `GetBrowserProfile` and `ListBrowserProfiles` recheck paired authority. Lists
  filter the original device before pagination (default 50, maximum 200); signed
  cursors bind server and device. Foreign identities are indistinguishable from
  absent profiles. The profile inventory is capped at 2,000 records per device.
  Generic Device Get/List/Snapshot projections and pairing/revocation responses
  omit the entire browser inventory for every caller. Other device metadata and
  the original stored document remain intact; browser reads use this dedicated
  device-scoped service rather than generic resource access.
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

## Direct workspace pages and original native operations

Every retained browser page of the session account's admitted profile is a peer
Page tab in the shared session-tab workspace beside Conversation, keyed by its
original profile/page IDs. There is no Browser singleton tab, no nested page
picker row and no browser pane-header close. The Page tab content has the
persistent Info rail, a header with only the profile information control, the
Back/Forward/Reload, labelled Address and Go toolbar, native-view Retry in its
accessible overflow, and the viewport. The selected page occupies the complete
active content region; Conversation/drafts remain mounted and inert while
inactive. The former Browser split, compact overlay, Expand/Restore presentation
and Browser picker are superseded.

The workspace tab strip is followed by the single Open tool `+` button (the
SessionToolMenu trigger, localized accessible name and title Open tool / 도구 열기).
It is not a tab or numeric-shortcut position, stays reachable beside the
scrolling tabs and never creates a page directly; the tab bar renders no direct
New browser page button. The Open tool dialog's Browser row is the only entry to the
compact `Modal` (480px maximum, viewport-bounded, 40px controls, 8px corners, English and
Korean "New browser page / 새 브라우저 페이지"). It shows the existing
account-sharing explanation, one labelled Address field (focused on opening, with
focus trapped and the opener restored on dismissal), Cancel and Open page. Opening
the dialog, and Cancel, X or Escape, create no profile and no page. Open page is
enabled only for a bounded HTTP(S) address without credentials (at most 8,192
characters), a supported capability read, no removal-pending profile and fewer
than 16 pages. Capability loading, unsupported and removal-pending guidance, the
original exact registration retry, and safe failure text never create a fallback
page.

Admission follows the read-only enumeration state. Open page stays disabled, and
submission returns without sending anything, while the `ListBrowserProfiles`
walk is loading, paginating (a next page token is outstanding), failed or
invalid (a read error, more than one matching profile across the walk, a repeated token or a
next page token returned by page 64). A matching profile is only a candidate while a
next page token is outstanding: the walk continues, and the profile is admitted
only after the terminal page confirms it is the sole match. A confirmed absence
(a settled terminal page with no matching profile on any page) can also admit a
new page; absence permits exactly one registration at explicit submission. A found
profile additionally needs one successfully observed current-profile inventory,
so an unread or unreadable inventory is never treated as empty. A later inventory read failure revokes that proof
until a retry read succeeds. The dialog shows
localized read-only guidance for pending states and, for a failed profile or
inventory read, a retry that re-reads only that state (profiles from the first
page with cached pages bypassed, or the inventory) and never repeats any accepted or uncertain mutation.
A session that becomes active again while the walk has no profile restarts it the same way, from the first page with cached pages bypassed, so a partial refetch of the current page never combines with pages read before the session was inactive.

Only explicit submission may change the inventory. It reuses the original
capability/account/session/revision checks and exact registration recovery: an
existing profile (found by the read-only `ListBrowserProfiles` page walk) is
reused; a missing profile is registered once at submission. It then issues exactly
one Create under a fresh UUID-v7 operation ID and selects the confirmed Page
descriptor. An accepted operation stays owned by the mounted browser manager after
the dialog is dismissed; reopening resumes its original result or recovery and
never sends a fresh creation while the original outcome is unsettled. Restoration,
inventory reads, presentation and observation never create a page: an empty
inventory stays empty, and a presentation request for an unknown page identity is
rejected.

## Native-local page commands

`browser_pages` is one closed trusted-local Tauri command (declared in the build
manifest and granted only through `browser-presentation` to the `main` and saved
product webviews). It is denied to raw external browser children, remote origins,
the tray and auxiliary views, and it recaptures and rechecks the original
product-window instance, Local/Saved connection and Go-authorized profile
(server/device/account/profile and Active state) before and after every
operation. It accepts no renderer path or presentation handle. Its typed request
has one of four actions:

- `inventory` reads the profile's local pages without initializing storage or a
  runtime profile; a loaded profile returns its in-memory pages, including
  observed addresses.
- `create` (operation ID plus bounded HTTP(S) URL) and `close` (operation ID plus
  page ID) are the only mutations. Admission records the original operation
  (window owner, profile and ID, bounded to 64 retained operations) before any
  storage work and allows one mutation per profile at a time. An identical replay
  returns the original receipt without a second mutation; a different request for
  the same identity is rejected. Create adds one page and selects it; Close removes
  exactly the requested page (an already absent page is an idempotent completed
  closure), selecting its left neighbor when it was selected, and closing the final
  page leaves the profile with zero pages. The 16-page gate is retained. Storage
  work stages off the UI thread and only the replacement is published under the
  existing worker fence and a window-authority fence that rechecks registry,
  barrier and binding state. Immediately before replacement the publication fence
  re-reads the Go profile and requires the exact admitted Active revision; a
  removal-pending, removed or changed record rejects the publication and leaves
  the prior metadata untouched.
- `observe` (operation ID) returns the original receipt and the current inventory.
  It never repeats metadata mutation; it retries only the directory fsync that
  followed a successful replacement and the idempotent native child
  reconciliation.
  The original receipt is validated against the original operation (identity,
  committed metadata, complete native cleanup, no failure) independently of the
  current profile availability: a completed receipt observed while the profile is
  removal-pending is still accepted. A Close proof requires the page to be
  absent; a Create proof requires the page to be listed only while the profile is
  available, because a removal-pending inventory is deliberately empty. Such a
  Create settles without restoring a descriptor or sending a second Create. The
  client treats a removal-pending reply as an empty inventory even when the host
  returns the loaded tabs next to `removal_pending: true` (a committed Create can
  race with removal): the raw listing only proves the original receipt, and
  reconciliation and completion handling never see or open those pages.
  A removal-pending reply whose Close or Create receipt fails any proof check
  stays rejected: the operation remains unsettled and uncertain and the closing
  page keeps its descriptor. The independently validated removal-pending state is
  still reconciled as an empty inventory, so other Pages' descriptors are not left
  stale while the bad receipt is retained.

Every reply carries `{state, receipt}`. A receipt separates committed metadata
from incomplete native cleanup (`phase` accepted, cleanup-pending, completed or
failed; `metadata_committed`; `native_cleanup_complete`; a closed failure code).
After a mutation commits, live shared children are reconciled with the shared
selection on the UI loop: a child already on the selected page is preserved, a
superseded child is unmapped before any handle or generation changes, and a page
nobody presents is never displayed. CloseBrowser is asynchronous, so a successful
close request is not completed cleanup: the receipt records the exact (window,
generation) of every original native child it superseded and stays
`cleanup-pending` with `native_cleanup_complete` false until each of those
children reports `on_before_close`. Replacement children carry a newer generation
and unrelated children are never awaited, so neither delays nor satisfies the
original cleanup; two windows presenting one page require both original
callbacks. If profile removal begins after the metadata commit and before this
reconciliation, the reconciliation does not replace children; instead the receipt
awaits every original native child of that profile (within the receipt's
generation boundary) until removal's own close requests report
`on_before_close`, so it never reports completed cleanup early. A receipt that
no longer tracks outstanding children, such as an idempotent Close of an already
absent page whose awaited set is final and empty, registers nothing on this path
and completes without waiting for removal's callbacks. Closing a
page that no native child presents completes immediately.
A page selection control from another window that supersedes children after the
metadata commit but before this reconciliation registers those children on the
profile's in-flight receipt as well, so the receipt never reports
completed cleanup before their `on_before_close`. Registration starts while the
mutation is in flight, before the receipt records `metadata_committed`, because
a close can land after the staged replacement publishes and before that flag is
set; a mutation that fails, or an idempotent Close of an already absent page that
publishes nothing, discards the registrations and never attributes later child
closes to its receipt, while a published one keeps them. The same registration applies when a lifecycle close (a superseding
reservation, a hide, or a window close) removes a view that owns or is creating a native child after the metadata commit but before
reconciliation, so reconciliation never finds nothing to await while that child's `CloseBrowser` is still pending. A hide or window close whose native unmap fails
retains the view for retried cleanup but still requests `CloseBrowser`, so it
registers the view's exact generation on the receipt as well. A lifecycle close
registers only a child that the mutation would supersede, which is a child not
showing the page the mutation leaves selected: the receipt records that page when
the metadata commits and drops earlier in-flight registrations that show it, so
closing an inactive page never waits on the callback of an unrelated child that
displays another page. A child that a selection already superseded is awaited
whichever page it showed. A hide that races a view whose native child is still being created marks the view closing and returns busy, and it registers that generation before returning, because the closing view is excluded from reconciliation and the creation callback closes the child only afterwards. Publishing an
`open_browser` view that replaces one already published for the window (for
example overlapping opens sharing a view ID) removes the old view through the
same registration. The `on_before_close` callback that empties a reconciled receipt's set settles it
immediately, so an owner that unmounts before its next Observe cannot leave a
`cleanup-pending` receipt that exhausts the page operation limit. A receipt whose
reconciliation failed stays `cleanup-pending` with a partial set after the
profile's mutation slot is released, so every later registration above also
applies to a committed receipt whose reconciliation has not succeeded, and a
child removed before the retried Observe is still awaited. Only children that
existed when the receipt's metadata committed are attributed to it: the receipt
records the highest view generation at that commit, so a replacement child created
after it (for example by a partially failed reconciliation) is never added to the
awaited set when it is later hidden or closed. When a later
operation's reconciliation supersedes a child that such an older unresolved receipt
still owns as an original, the child is registered on that receipt as well, so
observing the older receipt before the close callback cannot complete it early.
Observing the
receipt re-evaluates this set without a second metadata mutation or a duplicate
close request. Failures before admission are returned as a
typed `not-admitted` error and prove nothing changed; any later failure is
retained on the receipt or surfaces as an uncertain reply that only observation of
the original operation settles. Admission includes an identical replay of a
retained operation: once the replay is accepted, a failed observation of the
original operation (including a host stop racing in before the read) is an
`uncertain` reply, never `not-admitted`, and never repeats the mutation; observing
the same operation ID settles it. `open_browser` takes a page ID instead of an
address and moves the shared profile selection to that retained page;
`control_browser` can no longer create or close a page.

## Page close and recovery

Page × requests closure of that original native page; other tab kinds keep their
existing dismissal. The Page descriptor is removed (selection falls to the left
neighbor, and to Conversation after the last page) only after the original
operation is observed with committed metadata removal and complete native cleanup.
While pending, failed or uncertain, the descriptor remains with accessible inline
recovery below the tab strip: truthful progress, a Check status action that
observes the original operation (never a blind second Close), and a distinction
between committed removal with incomplete native cleanup and a failed operation.
A rejection before admission or a failed receipt clears the operation so a later
close is a fresh explicit operation. Closing an inactive page never presents it or
another native page. This resource Close is the one browser-specific exception to
the presentation-only dismissal other tools use, and it remains separate from the
16-page gate and from profile/account removal.

An acknowledged close is profile-scoped, so its cleanup is not limited to the
initiating Session: when the original Close is observed complete, or an
observation without an original receipt proves the page is gone, the exact
profile/page descriptor is removed from every Session snapshot in the shared tab
store, including inactive and retained Sessions that are not polling. Each
affected Session applies the same selection rule only when its own selected
descriptor was removed (left neighbor, then Conversation after the last page);
unrelated selections, ordering, the same page id under another profile and other
pages are untouched. Repeated acknowledgment is idempotent, and pending, failed
or uncertain closes retain the descriptor in every Session. The cleanup is
store-level only and issues no extra native close.

When the initiating Session's Page tab control owns keyboard focus, a proven
Close moves focus after the tab strip commits to that Session's selected
surviving tab control with `preventScroll`. The target is resolved from the
retained descriptor key after the shared close selection rule runs. The same
handoff applies to an immediate receipt and a later observation of the original
operation. Pending or rejected Close keeps its descriptor and focus. A response
after Session deactivation or another navigation cannot move focus into the
old Session; focus already moved to a surviving control remains with the user.

Other windows and sessions of the same account observe the inventory read-only
and align their descriptors (append newly listed pages in inventory order,
refresh labels from observed URLs, and drop descriptors the inventory no longer
lists) without selecting a page or taking ownership of another window's
operation. A removal-pending (empty) inventory is authoritative: it drops this
profile's Page descriptors the same way, without any native mutation. The
invalidation is profile-wide: the observing Session also removes the profile's
Page descriptors from every other retained Session snapshot of the
connection-owned tab store, including inactive Sessions that do not poll, through
`SessionTabsStore.reconcileUnavailableProfile`. Only descriptors of exactly that
profile are removed (the same page id under another profile, non-Page tabs, tab
order and unrelated selections stay unchanged), a removed selected Page follows
the existing `closePage` selection fallback in its own Session, the pass never
selects or polls a Session, and a repeated pass publishes nothing. A revisited
Session therefore cannot present a removed stale descriptor. A page
owned by an unsettled original Close keeps its descriptor in every Session
and every same-origin product window (the close is profile-scoped, so the shared
tab store records the owner and a reconciling Session never drops that page)
until the owner observes the proof or abandons the operation. Product windows
have independent renderer memory, so the store announces each hold, a
heartbeat refresh and its release over a same-origin `BroadcastChannel`
(`delidev.browser.pending-close`). A newly opened window queries the current
holds, and windows learn each other through that exchange. Before the native
Close is sent, the initiating window announces the hold with a unique announcement
id and waits until every sibling window has acknowledged that exact announcement
(an Ack echoes the id, so a delayed Ack for an earlier Close of the same page is
never credited to a replacement Close). Siblings are the windows already seen on
the channel plus, when Web Locks is available, every window currently holding a
liveness lock, so a window whose handshake has not arrived yet is still awaited;
without Web Locks the Close also waits a 100 ms discovery period for unintroduced
windows. A silent sibling that still holds its liveness lock (for example a
suspended renderer) keeps being awaited, because it may resume and reconcile at
any time; it stops being awaited only after it acknowledges, announces `bye`, or
its lock is gone (rechecked every second). Without a liveness source a window
that stays silent for one second is presumed gone. A released or settled hold
ends its wait. A window that is discovered (or joins) after the first broadcast is sent an acknowledgement-requesting Hold again, and a Close whose hold was withdrawn while it waited (pagehide or disposal) is cancelled before the native call and reported as a failed action. This ensures a sibling's inventory read cannot find the page unowned after
it disappears natively. A hold whose window stopped refreshing (for example after a crash)
expires after 15 seconds, except that when the Web Locks API is available each
window also holds a liveness lock named after its window identity and an expired
hold is kept while that lock is still held, because renderer timers of a hidden
or minimized window are throttled and are not proof of death. Without Web Locks the
15-second expiry applies. A closing window releases its holds on `pagehide`, becomes inactive (it announces no further Hold, so a Close started after `pagehide` cannot resurrect it on siblings), and cancels its liveness-lock request through an `AbortSignal` so a request that was never granted cannot hold the lock after disposal. Pending lock requests of siblings count as live windows. `pagehide` is a suspension, not a disposal: the registry stashes the owners of Close operations whose native Close was already dispatched (marked sent once the pre-send barrier passed) before it releases them. A `pageshow` restored from the back/forward cache (`persisted`) reactivates the window and, before any resumed reconciliation, re-registers those same owners and announces their holds again with the acknowledgement barrier, so siblings report the page as owned again. Restoration reuses the original operation identity and never sends a new native Close; the sent operation is only observed. A Close whose pre-send barrier was withdrawn by `pagehide` is never stashed, so it stays cancelled and is not revived. A settled, rejected or abandoned Close removes its stashed owner, and permanent disposal (`dispose()`) clears every stash, so none of them is restored. The restoration changes no channel message, native receipt or persisted state.
A window whose `Query` arrives while a barrier is open is added to that barrier's required set before its Ack is awaited. An inconclusive liveness result is never proof of death: a failed `locks.query()` keeps an expired remote hold (it is re-probed at the next expiry), and a barrier's liveness query that fails or does not settle within one second (the sync timeout) counts as inconclusive. A barrier whose liveness query keeps being inconclusive keeps awaiting silent windows for at most five rechecks (a documented best-effort bound, so a permanently failing or hanging query cannot hang a Close) before presuming them gone. The deadline timer is owned by the barrier and cleared when it finishes, and a timed-out query that settles later is ignored (a late rejection is swallowed), so it never restarts a barrier, sends a message or leaves a timer pending. A query that succeeds and proves a silent window alive keeps the barrier awaiting beyond that budget. The registry constructor connects by default, but `deferConnect` skips that; the React provider and the `useSessionTabsStore` fallback construct the registry with `deferConnect` and call `connect()` only from a committed effect, so render never opens the channel, page listeners or liveness lock.
When the owning window observes the original Close proof, it broadcasts a
`settled` message in place of a plain release: every sibling window drops the
hold and removes the exact profile/page descriptor from all of its Sessions,
including retained or inactive ones that run no inventory poll. A rejected or
abandoned Close only releases the hold and never removes a descriptor. A removal-pending reply is normalized to an empty inventory for reconciliation, but the raw listing is kept to prove a Close: an observation with no receipt settles the Close only when the raw reply no longer lists the page, otherwise only this operation is released.
A discarded tab store (for example when the connection identity remounts the
provider) disposes its registry: it releases its holds, detaches the `pagehide`
listener and closes the channel, and a replayed mount reconnects it.
The channel carries only an opaque window identity and the profile/page key,
never URLs or credentials; without `BroadcastChannel` the hold stays
window-local. Existing local `tabs.json` storage remains compatible; no business RPC,
protocol allocation, SQLite migration, dependency or browser-engine change is
added.

The original account/profile manager remains mounted independent of the active
page pane. Inactivity uses exact original Hide cleanup and retains failed or
uncertain cleanup before any replacement. A page label derives only from its
retained URL; there is no title/favicon discovery, page script, URL logging or
generalized key bridge. A Page descriptor follows native observation without
changing identity: each observed URL refreshes only the label of the existing
exact profile/page descriptor, never its order, selection or any foreign profile
page. When a busy action settles, the native page is reconciled once to the
current shared selection; a failed reconciliation is not retried until the
selection or native state changes. Its failed-attempt suppression ends when the
shared selection leaves that Page, including a move to a non-page tab; an
explicit return may retry once after an eligible presentation opens. An
unchanged selection and native state never start an automatic retry loop.
Reconciliation also waits until a pending native open has published its view,
then selects the current shared page once.
Profile explanation uses the original
accessible Modal and native visibility boundary.
Numeric selection has only typed, original-owner fenced
local presentation authority under the desktop shortcut contract. The owner
approved deferring only Windows/Linux numeric forwarding while external browser
content has focus; use the tab bar there. macOS forwarding requires original
native composition-clear proof. All ordinary app/input/terminal shortcuts and
other tab acceptance remain required, and localized Help states this exception.

Back/Forward/Reload, labelled address and Go share
a compact toolbar; explicit native-view Retry is in its accessible overflow.
Keep original registration uncertainty/retry beside its owning problem. The
viewport fills the remaining bounded height without a fixed 400px minimum;
localized chrome wraps/scrolls with semantic themes, 8px controls and 40px targets.
These changes introduce no protocol allocation, migration or dependency; the native-local `browser_pages` command above is the only added native interface.

Switching away releases only the original native presentation while retaining
its profile and the composer; other panels retain independent operation lifetimes.
The user selects an HTTP(S) address before registration;
there is no automatic provider login or implicit external navigation. Back,
Forward, Reload and bounded local tab controls use trusted native commands. A
native presentation UUID is reserved on the UI loop before asynchronous
authorization. Reservation releases the previous raw child and invalidates its
pending creation, even when the replacement authority read or storage preparation
fails. Stale opens, controls and cleanup cannot replace a newer panel instance.
Asynchronous request-context child creation failures are retained for the exact
presentation and surfaced by native state polling. Ordinary controls return that
typed failure until explicit Retry creates a new presentation; stale failures
cannot poison its replacement, and tracked removal remains visible separately.
Initial host/geometry failures after child acceptance use that same exact failure
retention before requesting asynchronous closure. The close callback releases the
child handle without erasing its typed failure, so state polling still offers Retry.
When a shared-profile tab operation replaces multiple children, every affected
window receives a creation attempt even if another attempt fails. Each failure
is retained against its exact view; the initiating command returns the first
failure after attempting all replacements.
Children whose selected tab identity is unchanged remain intact when closing a
background tab or selecting the current tab, preserving in-page state, navigation
history, pending creation and exact retained failure. Changing selection or closing
the final tab still replaces or releases every affected shared child.
The persistent closed compact sidebar dialog and open nonmodal wide sidebar
region do not block browser presentation. Visible dialogs, hidden panels and
clipped geometry cannot leave an external child above app UI.
Selected/final-tab replacement synchronously unmaps every superseded shared child
before dropping any handle or advancing any generation. Failure retains the
original views for exact Hide cleanup; no replacement child is created. Presentation
reservation/open obey the same unmap-before-release rule. Removal and quit retain
closing handles until their exact callbacks, so Hide can still reach a child whose
asynchronous close has not completed. Discovered profile removal and forgetting
a saved server/device scope mark every matching view closing, then synchronously
unmap every child before requesting closure. Both paths attempt later users even
after an unmap failure and retain each original handle and presentation for exact
Hide retry or its close callback. Scope forgetting preserves unrelated server/device
profiles and pending-creation guards. Failed unmapping never permits directory purge.
Removal unmaps and close requests run outside native state. Close requests after
tab replacement also run outside native state; pending old creation callbacks
remain generation-guarded.
Raw child closure is child-only. The raw child's life-span handler owns
`do_close` for non-popup children: on macOS and Windows it returns 1 and completes
destruction of exactly that child's own native host on the UI loop (removing its
`NSView` from its superview, or `DestroyWindow` on its `HWND`), so CEF never
sends its default parent-window close (`performClose:` / `WM_CLOSE`) to the
containing product window and `on_before_close` still fires once per child. On
Linux `do_close` keeps CEF's default (0), and popups keep the default. The
handler binds the exact Browser identifier at creation; destruction is claimed
at most once and only for that identifier, so duplicate or late close callbacks
cannot destroy a replacement child or product views, and `child_closed` keeps
its generation filter. New Tab, selected-tab replacement, tab close and panel
disposal therefore never hide, close or minimize the product window; explicit
product-window close still hides to the tray through `CloseRequested`, and Quit
still joins. Close diagnostics are structured `browser_view` tracing events
(`close_requested`, `destroyed`, `destroy_failed`) carrying only the platform,
popup flag, numeric browser identifier and outcome, never URLs, credentials or
page content. Fixture and build results prove only the disposition, identity and
idempotency rules; macOS, Windows and Linux native acceptance is recorded
separately.
Only successful native geometry updates become the panel's last applied bounds;
a failed resize clears that cache so later callbacks retry identical geometry
for the current presentation.
Closing the panel releases its view, retaining its request context/profile.
Failed Hide retains its exact presentation identity and retries that closure,
including after unmount or late open completion. Hides are serialized; resize
and reopening wait for successful closure. Native Hide synchronously makes the
matching child invisible before acknowledging success; asynchronous CEF close
requests alone cannot acknowledge Hide. Pending native creation and failed native
hiding retain the original view until a successful hide or its exact close callback,
and deny ordinary controls while closure is pending. Native Hide confirms an already
absent original view without touching a replacement and returns no browsing
data, including for unrelated-window requests. Old cleanup cannot clear the
replacement's frontend identity. Closure acceptance does not replace
the independent native close/shutdown proofs required for profile deletion.
Native window closure does not depend on React unmount: actual close and explicit
saved-window destruction invalidate that window's reservation and pending creation
and request raw-child closure on the UI loop before destroying the parent. The
saved-window Destroyed fallback checks the original binding instance before closing
the view and dropping its binding. Tray hiding retains presentation; window closure
retains profiles and the native callback counts needed for independent shutdown.
Window closure also unmaps a current child before dropping its handle; on failure
it retains that closing view while still requesting teardown.
Profile directory traversal, tab reads, URL policy preparation and durable writes
run on serialized storage workers without holding the native state lock during
I/O. Trusted documents and presentation/control generations are rechecked before
UI-affine CEF publication. Tab controls stage and fsync private replacement files
before checking the exact reservation at publication. A separate worker fence
serializes the final replacement with reservation acceptance on the UI loop;
only the worker waits for that callback, and native state is released during I/O.
Reservation waits are bounded to five seconds and cancel an unexecuted late UI
callback, releasing the worker fence if native exit stops callback delivery.
Superseded staged writes are discarded without changing durable or live tabs.
Presentation selection writes use that same staged publication boundary, so a
superseded open cannot move the replacement's shared selection. Presentation
never seeds a first page.
Observed-address writes use the same fence and recheck exact profile, child
generation and reservation after staging, so replacement opens retain the last
accepted address rather than consuming a superseded callback.
Address callbacks coalesce at most one latest update
per tab (64 profiles × 16 tabs) into one tracked worker; callbacks perform no disk
work. Quit closes callback acceptance under the queue gate and drains previously
accepted addresses, retaining the exact generation/reservation/removal checks.
Hide/Resize remain independent of pending storage. The worker joins after
runtime return and before any directory purge.

### cmds/delidev-cli constraints

- Browser ownership and cleanup follow `cmds-delidev-browser-contract.md`. Keep bounded non-secret profile metadata on the original paired-client Device document with independent UUID-v7 identity/revision, exact authorization and receipts. Account deletion atomically marks every device obligation; session deletion and Archive retain profiles. Service-owned browser messages avoid reserved shared enum numbers and SQLite migrations; never persist browsing content or paths.

### cmds/delidev-cli/internal/domain constraints

- Protected browser records contain only canonical server/device/account/profile IDs, monotonic revisions and closed cleanup state. Keep browsing data and paths outside Device metadata and product documents; follow `cmds-delidev-browser-contract.md`.

### cmds/delidev-cli/internal/server constraints

- BrowserService profile operations require the original paired client, current transaction-time authority and exact UUID/revision receipts. Owner/Worker credentials cannot substitute a browser device; owner/client cleanup counts remain independent of account configuration removal. Confirmation attests to separately completed native cleanup and cannot replay native work. Follow `cmds-delidev-browser-contract.md`.

### cmds/delidev-cli/internal/store constraints

- Protected browser registration and account deletion compose through bounded paired-client Device metadata without a browser-specific migration. Register one account profile per server/device, publish Device events and actor-bound receipts atomically, and mark all offline/revoked device obligations before account deletion. Session erasure and Archive preserve shared profiles. Follow `cmds-delidev-browser-contract.md`.

### Protocol integration

- DeliDev BrowserService follows `cmds-delidev-browser-contract.md`: use its own typed profile/state/capability declarations, exact original mutation identities and metadata-only payloads. Preserve all existing numeric allocations and migration reservations; generated compatibility exports must reproduce.

## Storage

Browser profile metadata extends the existing paired-client `Device` JSON
record. Its own UUID/revision and typed non-secret ownership/state persist under
the server's exclusive SQLite authority. Atomic device events and request receipts
share the transaction. The serialized device document retains existing limits;
schema remains 25 after the independent native-accounting migration, with no browser-specific table or migration. Existing snapshots,
backups and absent-field historical device records remain valid. One account per
device is enforced by the transaction and validated bounded inventory, rather
than consuming a reserved migration for an index.

Native System cache paths are constructed solely from canonical server/device/account
UUIDs beneath the prepared owner-private `browser-data/profiles` root. Only
macOS `debug_assertions` builds use explicit CEF Mock storage and the corresponding
`browser-data/development/profiles` root, with `browser-data/development` as their
CEF root. Every other build retains explicit System storage and the original CEF
root. Shared `tabs.json` metadata remains under the original System profile path;
the development cookie path is separate. Existing cookies are never copied,
re-encrypted or removed merely by switching builds. The actual initialized CEF
request-context path must equal the selected mode's exact path. A live
process shares one context for each profile, including multiple session windows. At most 64 request contexts are retained per
native process; reopening the desktop releases that runtime capacity without
deleting profiles.
Tabs are bounded to 16 and 256 KiB, atomically saved locally with private files;
cookies, storage, history and browser credentials belong to that context. They
never enter SQLite, RPC bodies, configuration transfers or Worker workspaces.
Session deletion, Archive and panel closure do not create removal obligations.

`src-tauri/src/browser_storage.rs` owns this compiled policy and the common
owner-private `browser-data/browser-host.lock` lease. Open the original lock file
without replacing it, or create a private regular file when absent, before CEF
initialization; reject symlinks, hard-linked/shared Unix files and competing
hosts. Never unlink or replace the lock. Keep its exclusive OS file lock until
CEF returns, tracked workers join and durable local cleanup finishes. Both modes
share the lease, so they cannot concurrently use or remove the same profiles.
Older binaries that do not hold this lease must not run concurrently against the
same data directory. Missing, malformed or inaccessible storage fails explicitly;
neither mode silently unlocks a keychain or falls back after a System error.

The Mock cookie key is a publicly known test constant with no meaningful at-rest
protection. It does not change the native Go API/PAT/OAuth credential stores,
Chromium sandbox requirements, external-child IPC denial or account authority.
Development behavior cannot establish production Keychain or shutdown acceptance.

Account configuration deletion atomically marks every registered device profile
removal-pending with the original deletion request and increments its revision,
including offline/revoked clients. Configuration removal and browser cleanup are
separate outcomes. Account deletion confirmation explains pending browser
cleanup; confirmed configuration removal closes its desktop dialog without
reading cleanup counts or waiting for native cleanup. The authenticated
owner/client cleanup-count API remains available after account removal, and
offline obligations still require original native shutdown, complete profile
removal and the owning server acknowledgment.

Managed database restore discards historical Device records and copies complete
current Device documents from the synchronized safety image outside the replaced
database. Browser profile IDs, states, profile revisions and deletion request IDs
therefore retain current authority, including offline/revoked pending profiles and
completed removal. The restore journal pins that safety image and candidate before
publication; startup reconciles the independent outcome before serving. Restoring
an older backup cannot make those profiles active or reauthorize revoked clients.

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

Local profile purge removes its exact development directory and original System
directory before any server acknowledgment. Forgotten-scope purge similarly
removes both exact device directories before retiring its local marker. Remove
development first to preserve shared System metadata if that step fails. Validate
existing private ancestors without creating missing profile directories; retain
the original journal and request identity on any failed purge. Synchronize each
surviving parent after deletion, including an exact retry that observes absence
after an earlier synchronization failure. Partial deletion never authorizes an
acknowledgment, and unrelated server/device/account directories remain intact.

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
Each exit gives forgotten scopes and account-removal intents independent budgets
of at most 64 intents and 45 seconds per queue. Offline forgotten-scope inspection
cannot consume the account queue's time or prevent its cursor advancement.
A local failure in either queue retains its original evidence while the other
queue still runs after the shared native shutdown, discovery join and storage
prerequisites. Return the first local failure only after both queue passes.
Existing two-second observer and 40-second acknowledgment child bounds remain;
an in-flight child retains its joined bound. Remaining or uncertain intents stay
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
Product controls are accepted only from registered trusted local/saved app documents and
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
Record the source revision, commands, exact results and unresolved platform/native
limits in pull requests, issues and CI logs/artifacts. Do not add repository evidence
documents. Distinguish implementation verification from real-account, real-renderer
shutdown/flush and release acceptance; exclude secrets, user state and raw native
content from validation records.

## Dependencies and Integrations

The existing Go sidecar establishes paired authority. Browser RPCs reuse server
identity, cursor signing, SQLite receipts and authorization. Raw CEF profiles are
independent of trusted app webviews; app capabilities match trusted webview labels
only. Existing account credential-disconnection prerequisites remain in force.

## Change Triggers

Update this contract and the scoped desktop/CLI/protocol/client AGENTS files when
ownership, bounds, native lifetime, cleanup acknowledgments or wire semantics
change. Keep the project and docs catalogs linked. Shared numeric or migration
allocations must still follow the source-structure contract in the owning feature PR.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [DeliDev project](project-delidev.md)
- [Requirements](cmds-delidev-requirements.md)
- [Desktop client](apps-delidev-desktop-contract.md)
- [Source ownership](cmds-delidev-structure-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Repository defaults](repository-defaults.md)

## Resident desktop runtime protection

All protected Local and Saved browser profiles reject the desktop main process's current private loopback port and its loopback aliases, including WebSocket aliases. Adding this port preserves each profile's separate selected product origin and loopback-port exclusion. Legacy ordinary CLI and frontend development ports remain protected. Browser cleanup reads share the resident version-2 CLI; their bounded final discovery precedes host shutdown, and post-shutdown acknowledgments retain durable intents without spawning another CLI. See the [desktop lifetime boundary](apps-delidev-desktop-contract.md#app-owned-sidecar-shutdown).

## External link browser destinations

The protected account/session browser is unrelated to the user's installed browsers. Opening an ordinary external HTTP(S) link in an installed browser (message and terminal links, Provider guidance, App information and GitHub forms) is a separate device-local native preference with independent General and Local destinations. It never uses, reads or modifies protected-profile paths, CEF request contexts or per-account cleanup obligations, and the Browser settings category does not select the embedded protected browser. External webview navigation and popups remain denied; the renderer only asks the native host to open a validated URL. OAuth and subscription authorization links keep the system default browser. The owning contract is [Browser destinations for external links](apps-delidev-desktop-contract.md#browser-destinations-for-external-links); installed-browser discovery and launch acceptance on macOS, Windows and Linux is recorded separately from fixtures.
