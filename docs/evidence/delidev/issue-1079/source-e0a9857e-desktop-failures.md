# PR #1231 complete desktop validation failures

The merged frontend source is the same as the first merge verification. The final
run used worktree source `e0a9857e32bf8b585dac72c9734669275ce69092` with no
frontend or generated-source edits after the merge. Go scratch ownership changed
in that revision; real-server/Worker integration fixtures therefore use that source.

`pnpm test` in `apps/delidev` exited 1. Client build and frontend typecheck passed.
Vitest completed in 389.55 seconds: 113 failed, 1,166 passed, 24 failed files and
76 passed files (1,279 tests / 100 files). Seventy-five failure blocks explicitly
report test timeouts; other blocks contain unavailable DOM/role/label observations
or assertion errors. Packaging/widget/build phases after Vitest were skipped.
Their earlier explicit merge-source passes remain in `pr-1231-main-6c749670.md`;
do not describe this complete final command as successful.

An unchanged serial selection (`pnpm exec vitest run src/App.test.tsx
src/backups.test.tsx src/settings-workspace.integration.test.tsx
--fileParallelism=false`) also exited 1 in 239.99 seconds: 10 App failures, 44
passes across 54 tests / 3 files. All backup tests and the real Go workspace
integration fixture passed. This different selection/concurrency cannot turn the
full run green. Its ten App failures report their original 5-second timeout.
No source code or timeout limit was changed between these commands.

The earlier merge-source complete command failed only one existing App focus test;
its unchanged exact isolated rerun passed. Preserve that earlier failure and its
limited rerun separately rather than replacing them with these later results.

At 2026-09-30T23:17:28.404080Z, read-only `uptime` reported load averages
316.72 / 224.45 / 118.37. A redacted process inventory recorded concurrent native
and Node test activity, including this full Go race run and desktop command.
This is host context, not proof of causality or a blanket baseline exception.
Individual timing, initialization and observation failures remain unproven.

Full log: `/tmp/delidev-1079-fourth-desktop-final.log`, SHA-256
`1ab4f61f34fffa8e7fecea259bc03c7bf86a3a038ce20342b1942b480a283450`.
Serial log: `/tmp/delidev-1079-fourth-desktop-isolated.log`, SHA-256
`4bc91a1d43fa341f1867d921523fce3168bf15fd3b054ba1c6f6122d990005a6`.
Host observation: `/tmp/delidev-1079-fourth-validation-host-load.json`.

## Complete full-run failure inventory

- `src/App.test.tsx > creates an automatically named session from the first message and explicit Workers`
- `src/App.test.tsx > shows selector loading while the current page has not returned`
- `src/App.test.tsx > opens a fresh New Project form from the plus button and restores opener focus`
- `src/App.test.tsx > defers a targeted entry within an opening and clears it when that opening closes`
- `src/App.test.tsx > abandons an uncertain New Project save without replay when reopening`
- `src/App.test.tsx > invalidates the loaded sidebar pages after saving without resetting their cursors or archive filter`
- `src/App.test.tsx > keeps the draft and session mounted across settings and navigation, and renders native text inertly`
- `src/App.test.tsx > refreshes reads after recovery without replacing the connection's session draft`
- `src/App.test.tsx > retries the exact accepted message identity after uncertainty instead of sending a new message`
- `src/App.test.tsx > opens execution configuration without changing the unsent session draft or dispatching work`
- `src/App.test.tsx > sends explicit Restore with the original revision and never sends Resume on its behalf`
- `src/App.test.tsx > retains an uncertain message across a switch to another session`
- `src/App.test.tsx > drops connection-scoped drafts and caches when the selected transport changes`
- `src/App.test.tsx > renders original Claude blocks and rejects mixed message families (true)`
- `src/App.test.tsx > renders original Claude tool ownership and rejects mixed records (true)`
- `src/App.test.tsx > renders Claude callbacks through the session RPC without other response controls (true)`
- `src/App.test.tsx > opens and closes workspace Files without replacing or sending the composer draft`
- `src/App.test.tsx > renders closed Grok user history through session RPC (mixed)`
- `src/App.test.tsx > discards an Instructions draft when navigating away and preserves targeted repository entry`
- `src/App.test.tsx > discards a nested integration profile draft on close before targeted repository entry`
- `src/App.test.tsx > discards a notification draft on close without saving`
- `src/App.test.tsx > discards an import draft on close before targeted repository entry without saving`
- `src/activity-sidebar.test.tsx > keeps compact filter edits open and closes only on Apply or Reset without replacing controllers`
- `src/agent-configuration.test.tsx > creates an accountless Agent with only the original defaults and visible core controls`
- `src/agent-configuration.test.tsx > keeps ordered reference operations, relative weights and duplicate prevention through collapse`
- `src/agent-configuration.test.tsx > disposes Agent disclosures, draft and uncertain retry on close`
- `src/agent-configuration.test.tsx > disposes Agent disclosures, draft and uncertain retry on native cancel`
- `src/agent-configuration.test.tsx > disposes Agent disclosures, draft and uncertain retry on navigation`
- `src/agent-configuration.test.tsx > ignores a late accepted Agent response after closing and replacing the opening`
- `src/backups.test.tsx > polls each accepted operation beyond the first history page and refreshes inventory on completion`
- `src/backups.test.tsx > retains multiple accepted creations and marks a failed direct refresh stale`
- `src/desktop.test.tsx > uses only the native-pinned saved authority and direct product RPCs without local bootstrap`
- `src/desktop.test.tsx > registers and inspects this computer through only the saved window's fixed Worker boundary`
- `src/desktop.test.tsx > refreshes a window label without replacing transport or open settings and ignores older notifications`
- `src/device-settings.test.tsx > discards another device's confirmation on close and reads its new revision`
- `src/doctor.test.tsx > keeps query gating and identity-bound native disclosures through category changes, reorder and replacement`
- `src/doctor.test.tsx > resets all details on actual Settings Close and reopens collapsed without mutations`
- `src/doctor.test.tsx > resets all details on actual Settings Escape and reopens collapsed without mutations`
- `src/doctor.test.tsx > resets all details on actual Settings navigation and reopens collapsed without mutations`
- `src/notification-presentation.test.tsx > drops a closed Settings permission wait without applying its late result to a new opening or the shared cache`
- `src/pull-requests.test.tsx > preserves off-page identity and filter drafts through empty later pages and First`
- `src/pull-requests.test.tsx > keeps applied results during edits and drops them on navigation without losing controls`
- `src/pull-requests.test.tsx > distinguishes catalog failure 7 from an empty page and preserves cached rows on failed Refresh`
- `src/schedules-sidebar.test.tsx > protects history and every schedule replacement during uncertain across navigation`
- `src/schedules-sidebar.test.tsx > retains Schedules connection memory on same-identity reconnect and resets on identity replacement`
- `src/session-tools.test.tsx > selects an Agent from later pages and preserves an explicit per-repository starting override`
- `src/settings-claude.integration.test.tsx > persists native Claude permission selection through the desktop and real Go configuration service`
- `src/settings-configuration.integration.test.tsx > configures a real Go server through the settings forms and explicitly validates a private keyless provider`
- `src/settings-devices.integration.test.tsx > revokes a real paired client through settings and reads bounded server diagnostics`
- `src/settings-github.integration.test.tsx > saves and renames GitHub profiles through the real Go server and CLI`
- `src/settings-lifetime.test.tsx > starts at the first category after closing via button an unsaved Instructions editor`
- `src/settings-lifetime.test.tsx > starts at the first category after closing via cancel an unsaved Instructions editor`
- `src/settings-lifetime.test.tsx > starts at the first category after closing via button an unsaved Agent Workers editor`
- `src/settings-lifetime.test.tsx > starts at the first category after closing via cancel an unsaved Agent Workers editor`
- `src/settings-lifetime.test.tsx > aborts a Settings write and ignores its late outcome undefined while retaining committed resources`
- `src/settings-lifetime.test.tsx > aborts a Settings write and ignores its late outcome 14 while retaining committed resources`
- `src/settings-lifetime.test.tsx > aborts a Settings write and ignores its late outcome 1 while retaining committed resources`
- `src/settings-lifetime.test.tsx > disposes an Agent opening without replaying its committed late save: undefined`
- `src/settings-lifetime.test.tsx > disposes an Agent opening without replaying its committed late save: 14`
- `src/settings-lifetime.test.tsx > disposes an Agent opening without replaying its committed late save: 1`
- `src/settings-lifetime.test.tsx > preserves an open editor and its transport identity through a same-identity transport replacement`
- `src/settings-lifetime.test.tsx > ignores a late native Worker completion without refreshing or replacing the new opening's status`
- `src/settings-models.test.tsx > composes one Models heading/action and a neutral successful empty page with optional accounts`
- `src/settings-models.test.tsx > does not infer zero accounts from incomplete inventory`
- `src/settings-models.test.tsx > does not infer zero accounts from unavailable inventory`
- `src/settings-models.test.tsx > retains pagination on an empty first page with continuation and a later empty page`
- `src/settings-models.test.tsx > keeps cached same-page rows through refresh/failure/retry, but never displays them under a new search`
- `src/settings-models.test.tsx > preserves every model identity/status/metadata and both original resource actions`
- `src/settings-models.test.tsx > retains list state across edit/pricing/category/responsive/reconnect transitions in one opening`
- `src/settings-models.test.tsx > discards Models search/page and read Retry on close via button, restoring the opener`
- `src/settings-models.test.tsx > discards Models search/page and read Retry on close via cancel, restoring the opener`
- `src/settings-models.test.tsx > retains the original uncertain model write and list scope through reconnect without granting navigation`
- `src/settings-models.test.tsx > ignores a disposed Models read's late result without changing replacement focus or rows`
- `src/settings-models.test.tsx > labels native-name fallback and keeps model rows when only inventory refresh fails`
- `src/settings-models.test.tsx > retains cached 'unfiltered' emptiness when the 'inventory' refresh fails`
- `src/settings-models.test.tsx > retains cached 'unfiltered' emptiness when the 'models' refresh fails`
- `src/settings-models.test.tsx > retains cached 'later' emptiness when the 'inventory' refresh fails`
- `src/settings-models.test.tsx > retains cached 'later' emptiness when the 'models' refresh fails`
- `src/settings-preferences.integration.test.tsx > creates and edits singleton server preferences with the exact Go defaults`
- `src/settings-projects.test.tsx > shows final first-page emptiness with exact help, one create action and no pagination`
- `src/settings-projects.test.tsx > keeps empty continuation and later pages navigable without claiming final emptiness`
- `src/settings-projects.test.tsx > separates loading/failure 7 from empty success and preserves creation availability`
- `src/settings-projects.test.tsx > preserves four field groups, repository order, primary clearing, restrictions and full documents`
- `src/settings-projects.test.tsx > retains an edit draft and blocks Save after current-resource revision`
- `src/settings-projects.test.tsx > retains an edit draft and blocks Save after current-resource failure`
- `src/settings-projects.test.tsx > explicitly retries the exact project save request within its opening`
- `src/settings-projects.test.tsx > explicitly retries the exact project delete request within its opening`
- `src/settings-projects.test.tsx > discards a project draft and uncertain retry on button close without replay`
- `src/settings-projects.test.tsx > discards a project draft and uncertain retry on cancel close without replay`
- `src/settings-projects.test.tsx > focuses targeted creation and confines the presentation to Projects`
- `src/settings-subscriptions.integration.test.tsx > keeps subscription-only metadata CRUD reachable, sends no native lifecycle RPC and resets filters on close`
- `src/settings-workspace.integration.test.tsx > inspects and saves a real owned Git checkout through a separate Go Worker before creating a project`
- `src/settings.test.tsx > renders the Agent-only empty inventory after the first read succeeds`
- `src/settings.test.tsx > preserves Agent opaque pages when the first page is empty: true`
- `src/settings.test.tsx > preserves Agent opaque pages when the first page is empty: false`
- `src/settings.test.tsx > keeps Agent row content inert and actions scoped to exact supported configurations`
- `src/settings.test.tsx > keeps the original Agent deletion revision and retry request within its opening`
- `src/settings.test.tsx > retains an Agent draft at its captured revision when a peer changes the entry`
- `src/settings.test.tsx > retains exact Agent save bytes and navigation locks through reflow and reconnect`
- `src/settings.test.tsx > shows the complete grouped navigation once and keeps its selected category in sync`
- `src/settings.test.tsx > keeps provider-row entry fields disabled when the server lacks account-type filtering`
- `src/settings.test.tsx > retains the exact first-activation retry after inventory reveals the saved preset`
- `src/settings.test.tsx > uses server-owned preset key guidance and inert documentation in the API account wizard`
- `src/settings.test.tsx > discards account filters, later pages, wizard input and configuration deletion confirmations on close`
- `src/settings.test.tsx > keeps exact retries within an opening and discards its provider draft on close`
- `src/settings.test.tsx > edits global routing and fetch preferences without rewriting unrelated policy or creating another singleton`
- `src/settings.test.tsx > keeps the unfiltered picker cursor independent and retains an exact provider absent from account filters`
- `src/subscription-controller.test.tsx > keeps capability failure 14 retryable without granting lifecycle or classifying it as Coming soon`
- `src/subscription-controller.test.tsx > keeps capability failure 7 retryable without granting lifecycle or classifying it as Coming soon`
- `src/subscription-controller.test.tsx > keeps capability failure 16 retryable without granting lifecycle or classifying it as Coming soon`
- `src/subscription-controller.test.tsx > discards metadata drafts, filters and late acknowledgments on close while accepted server work continues`
- `src/subscription-controller.test.tsx > applies provider search to bounded subscription creation choices without filtering account rows`
- `src/tray-presentation.test.tsx > opens the native-selected view without sending work or losing the session draft`

## Unchanged serial selection failures

- `src/App.test.tsx > creates an automatically named session from the first message and explicit Workers`
- `src/App.test.tsx > shows selector loading while the current page has not returned`
- `src/App.test.tsx > opens a fresh New Project form from the plus button and restores opener focus`
- `src/App.test.tsx > defers a targeted entry within an opening and clears it when that opening closes`
- `src/App.test.tsx > abandons an uncertain New Project save without replay when reopening`
- `src/App.test.tsx > invalidates the loaded sidebar pages after saving without resetting their cursors or archive filter`
- `src/App.test.tsx > keeps the draft and session mounted across settings and navigation, and renders native text inertly`
- `src/App.test.tsx > refreshes reads after recovery without replacing the connection's session draft`
- `src/App.test.tsx > retries the exact accepted message identity after uncertainty instead of sending a new message`
- `src/App.test.tsx > retains an uncertain message across a switch to another session`
