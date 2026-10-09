# DeliDev AI Subscription Settings

## Scope

Issue #1143 owns the compact AI Subscription presentation; #1235 composes its independent service identity in `subscription-accounts.tsx`, `subscription-settings.tsx` and `subscription-catalog.ts`. The shared Settings application screen, 17 categories, stable `subscription-accounts` category ID and short AI Subscription label remain authoritative under [the desktop contract](apps-delidev-desktop-contract.md). The category description is “Manage your subscriptions and connect more accounts.” Managed Codex login/cancel/refresh/logout uses authenticated SubscriptionService and the independent server credential owner. Native quota and reset-credit operations belong to #1096/#1104; Claude lifecycle uses its independent capability 38 and original Runner; unsupported Grok lifecycle remains explicit. Account preferences, native authentication, quota observation and real-account/platform acceptance remain independent.

## Runtime and Language

React/TypeScript presentation follows the shared issue #1256 Settings body contract: system font, semantic light/dark tokens, left-aligned 1040px maximum column, 32px/24px/compact padding, 40px controls and 8px corners. Flat divided rows preserve the 40px provider mark, alias, separate connection state, up to two ordered quota windows and Refresh/Disconnect/ellipsis controls. Your subscriptions precedes Connect a subscription and the initially collapsed Advanced settings disclosure. Successful empty inventory uses the shared 160px-minimum horizontal icon/help region. Container queries retain wrapping identity/actions/quotas; service choices are flat ChatGPT/For Codex, Claude/For Claude Code and Grok/For Grok Build rows. Negotiated service-account support enables explicit Add account; unsupported servers retain disabled Coming soon controls. Shared context-pane/drawer navigation and all original lifecycle/metadata guards remain intact.

## Users and Operators

DeliDev users manage saved subscription account metadata on their selected server. Server operators own account authorization and supported lifecycle capabilities. The selected service, negotiated capability and original native profile establish each operation independently; account preference creation alone establishes no login or quota authority.

## Interfaces and Contracts

The frontend catalog uses typed ChatGPT / For Codex, Claude / For Claude Code and Grok / For Grok Build descriptors in that order. Local SVG marks come from the pinned MIT LobeHub source recorded beside the assets. Explicit server-owned `subscription_service` chooses the brand; editable alias/provider names cannot. No masked identity is inferred from the credential commitment or editable metadata. Presentation fixtures may supply independently attributed masked example identities.

The production controller negotiates System `SUBSCRIPTION_SERVICE_ACCOUNTS_V1 = 17` and uses generated Connect Query `ListResources` with the exact subscription account type before pagination. It never filters fetched pages locally. AI Subscription has no search, Provider filter, Provider inventory/preset/model discovery request or native Provider creation. Account creation writes schema v2 with one closed service and no `provider_id`, starts disconnected and defaults recovery notifications off. Advanced explains preferences, historical retirement and configured-empty deny-all restrictions. Preferences and confirmed deletion retain accessible ellipsis ownership and exact metadata retries. Account details retain additional quota windows, health, enablement, independent service attribution and exhaustion. Unknown, mixed or malformed account pages are unavailable as a whole.

Existing-account preference edits and the quota recovery notification toggle patch only their allowlisted top-level JSON tokens through the shared Account preference scanner. They preserve original protected bytes, including uint64 lease revisions above JavaScript's safe integer range. Post-login naming uses the same scanner for alias alone. The original expected protobuf revision, request UUID and resource bytes remain immutable for uncertain retries. Malformed JSON cannot produce a preference request; Go retains protected observation, numeric schema, authorization and stale-revision checks. These saves grant no authentication, quota or execution authority.

ChatGPT Add account requires inventory capability 17, independent server-login capability 30 and the trusted desktop native browser control. One deliberate ChatGPT action creates an account named ChatGPT and immediately requests ordinary browser login with omitted machine selection. Duplicate clicks, Strict Mode, status polling and reconnect cannot start another account or login. Claude uses the independent capability 38 flow below. Grok retains explicit Coming soon/unsupported guidance and cannot create an apparently supported login. Existing disconnected ChatGPT accounts can start the same flow deliberately. Authentication refresh and logout use the server lane without a Runner Device; logout confirms the exact current revision. Quota and credit controls negotiate their independent server/Worker capabilities and never infer support from server login.

ChatGPT deletion is owned by `account-deletion.tsx` inside the active category. `Delete {alias}?` offers `Disconnect and delete account`, explaining execution cancellation, protected credential removal and retained history/reference restrictions. In a top-level Settings task, header X/Escape owns dismissal; omit the duplicate `Keep account` button. Standalone forms and distinct nested task steps retain `Keep account` with its original departure callback. One explicit confirmation reads the exact current account and then requests server-lane logout under capability 30. A current original server logout is observed without another request; other pending operations, recovery and removal remain blocking. Already disconnected, fully cleared accounts skip logout and require no native capability.

The controller reads only the original logout progress, without query caching, every two seconds with no overlapping reads. Only typed success plus fresh matching account/service/preferences, disconnected health and absent connection, generation, pending operation, lease, removal and recovery permit one `DeleteConfiguration` using the fresh bigint revision. The final account must retain the original succeeded logout identity with no native owner. Configuration changes or reconnecting the account require fresh explicit confirmation. Go's protected-vault and reference checks remain authoritative. Logout/deletion response uncertainty retains only the exact original request; transient observation failure offers a read-only original-status retry. Failed, canceled, expired, unsupported, unknown or recovery-required completion keeps the configuration and never automatically starts another operation.

Waiting uses `Deleting {alias}`, `Logging out and cleaning up credentials...` and `The account will be deleted after cleanup is confirmed.` Failure uses ordinary status/alert text with sanitized diagnostics. Top-level header X/Escape abandons automatic client deletion without canceling accepted logout. Standalone forms and distinct nested task steps retain `Back to subscriptions` with the same departure behavior; top-level Settings tasks omit this duplicate return button. Category/Settings departure disposes timers and follow-up authority, including late successful reads or acknowledgments; same-identity reconnect never starts a request. A matching successful configuration-deletion acknowledgment received by the active task invokes the existing completion callback once, closing the dialog immediately and refreshing the current account inventory. There is no completion screen, cleanup-count read, Return action or new toast. Inventory refresh and independently owned browser cleanup do not delay closure; offline-device obligations remain pending. The flat 720px form uses shared theme tokens, 40px buttons, an outlined destructive confirmation and wrapping/stacked narrow actions; no modal, percentage or inferred cleanup is added.

Only the original operation's typed succeeded status and matching freshly read connection/generation permit Account name. Prefill once from valid email, provided display name or the service name; subsequent polling never overwrites edits. Name entry focuses once. Save account name validates the existing 256-byte UTF-8 alias bound, reads the current revision and patches only the alias JSON token so protected uint64 lease revisions remain exact. A revision conflict retains the draft for another explicit save; uncertain mutations retain their exact request identity/bytes. Save returns to inventory. Later, Back and application navigation retain the default-name account and accepted login without implicit cancellation.

Typed `Internal` responses can follow durable mutation admission. Account creation, login and name saving retain the original action and exact request UUID, account/revision, device selection and request bytes for explicit `Retry original request`, as they do for unavailable or canceled responses. Mount, polling and reconnect never replay these mutations. Before a valid login receipt returns, no operation ID is inferred and no progress or browser authority is granted. A valid original replay receipt restores ordinary bounded polling and browser binding. Typed Conflict remains a fresh-read/reconfiguration outcome; name edits remain available for another explicit Save.

Browser sign-in accepts only the registered `http://localhost:1457/auth/callback` and `http://127.0.0.1:1457/auth/callback` addresses. Preserve the original URL and callback spelling within an operation; duplicate query fields, malformed state and other callback authorities are rejected before opening. Existing recovery-required accounts retain their ownership fence; a fresh Add account action uses a separate account and operation.

The native desktop opens the original official browser URL once and owns deliberate Open browser again. Local servers retain the original Codex callback; remote servers receive the exact original state on the trusted native loopback receiver and relay one callback over authenticated Connect. Sensitive progress, name suggestions and callback bytes stay outside React Query caches and browser persistence. Callback bytes are cleared after dispatch; uncertain delivery never resends. Dispose the receiver on terminal status or departure and reject late screen transitions. Waiting contains no device/code selector, code presentation or initial Sign in action. Preparing, browser-open failure, waiting, cancellation, expiry, unsupported, failed and uncertain recovery remain readable states. Follow the server subscription contract for exclusive ownership and cleanup.

A capability-read error has an explicit retry and is never classified as planned support. Initial loading, successful empty, retained-data failure, permission denial, expired authentication and unsupported service-account capability remain distinct. API validation, provider enablement and model discovery never grant subscription lifecycle authority.

`subscription-onboarding.tsx` is the login-first presentation seam. It owns no
RPC or browser side effects. Its controller supplies preparing, waiting, confirmed
name entry, canceled, expired, unsupported, failed and recovery states. Name entry
receives focus once after confirmed success; progress rerenders cannot replace
an edited value. Save validates the existing 256-byte UTF-8 name bound. Back and
Later invoke only the supplied departure callback. The flat 720px form retains
semantic light/dark tokens, 40px controls, 8px corners and stacked narrow-screen
actions. `subscription-login.tsx` owns browser binding, explicit acceptance and original-operation
checks; this presentation alone grants no login capability. It contains no Runner Device or login-code control.

`SubscriptionSettingsView` is a pure frontend presentation seam. Supported fixtures can supply exact-account callbacks and independently owned operation state. Refresh all calls one supplied server-wide callback, with no frontend page traversal, provider aggregation or discovery request. Fixtures render independent account authentication/quota outcomes. Disconnect first confirms the exact account and describes preserved metadata/history, then calls its owning callback. Busy blocks another operation; uncertain and cleanup-pending states expose only an owning original-operation retry callback and cannot issue a new request identity. Production controllers must prove generated capabilities, current authorization and the exact native profile before supplying these callbacks.

Each window preserves its ID, order, remaining fraction, observation time and reset time. Finite fractions within [0, 1] become percentages and native progress elements with text equivalents; zero is observed zero rather than unknown. Missing/invalid values and future observation times have no valid bar. Observations older than five minutes, future observation times or elapsed resets are stale. Unknown, stale, failed and unsupported remain distinct; retained last-success values can remain visible with stale/failed text. An elapsed reset never implies recovery. No provider/window values are pooled. Refresh failure presentation retains the original successful windows/time supplied by its owner. An active surface schedules one presentation-only expiry at the next known freshness/reset boundary, without network requests; hidden/disposed surfaces clear it.

## Task dialogs

Keep the subscription inventory, filters and quota observations mounted in their category page. Account details uses one category-owned 768px Form SettingsTaskDialog outside virtualized rows, with Heading focus and alias/service context. Account creation, browser sign-in and management use the shared 960px Settings task shell; account preferences use 768px, and disconnect/logout confirmation uses a 480px step in the same native dialog. Preserve all fields, full identities, service capability, revision and original native/account checks. The [desktop Settings task contract](apps-delidev-desktop-contract.md#settings-task-dialogs) owns sizing, scrollable body, fixed header/footer, focus and responsive behavior.

Closing sign-in disposes its task controller, local callback/listener authority, sensitive progress, inputs and retry presentation even when a request is pending or uncertain. Keep the account inventory mounted and interactive; show no hidden-operation status or original-operation opener. Existing account creation and accepted login remain server-owned. X/Escape sends no business cancellation or logout. Remove duplicate dismissal buttons under the desktop dialog contract; preserve distinct business cancellation and internal return. A fresh task reads current state without replaying the closed task. Open-task uncertain retries remain explicit and preserve exact UUIDs, revisions and bytes. Late results cannot reopen a task, navigate, focus name entry, bind/open a browser or initiate a subsequent login/name save.

ChatGPT deletion uses the same task disposal even though its generated clients bypass the mutation registry. Closing disposes pending checks, local logout/deletion retry presentation and automatic follow-up authority; it never cancels an accepted logout or deletion. A late logout acknowledgment or cleanup observation cannot start configuration deletion after close. A reopened deletion requires fresh account observation and explicit current confirmation. A matching successful configuration-deletion acknowledgment delivered while the task is active invokes the existing completion callback once and closes immediately. Retire a confirmed-deleted account-row opener before inventory refresh and use the shared visible category title fallback; other connected visible openers remain valid. Independent browser cleanup and offline acknowledgments do not delay closure. Top-level deletion tasks use header X/Escape as the sole dismissal action and focus the named header close control for destructive confirmation. Standalone forms and distinct nested task steps retain Keep account and Back to subscriptions with their original departure callbacks. Category/Settings departure also disposes every nested task. The shared task shell owns modal geometry while this controller is hosted in Settings.

## Storage

All presentation state belongs to the active Settings category. Category filters and Advanced state survive task opening/closing, reflow, same-category reselection and same-identity reconnect. Account details retains its exact selected ID and latest valid metadata projection through row payload eviction, reflow, locale/theme changes and same-identity reconnect. An open task retains its drafts, confirmations and exact retry presentation across reflow and reconnect. Any task dismissal disposes that task state and its nested lifetime. Category departure or leaving Settings disposes all category and task state under the desktop contract. Accepted server/Worker effects continue; category disposal guards late continuations and leaves sibling QueryClient workflows and session drafts intact.

## Security

No credentials, login URLs/codes, identity fixtures or quota values enter query keys, logs, browser storage or analytics. Only active original login/status reads poll; category departure or leaving Settings drops scoped presentation and never cancels or repeats accepted native work. Settings itself has no modal focus or Escape-to-leave behavior. Actual child dialogs and the shared compact navigation drawer retain their own focus containment, Escape and opener restoration; ellipsis/confirmation Escape is handled locally; icon actions have accessible labels.

## Logging

Read and mutation failures use the existing typed, redacted transport diagnostics and Problem presentation. This controller adds no provider telemetry or raw native log stream. Operational troubleshooting must retain stable action/error classifications without recording credentials, masked identities, quota values or original request payloads.

## Build and Test

Run `pnpm test` from `apps/delidev`, after generating required client inputs and hydrating consumed LFS assets. Presentation quota/callback/state fixtures, generated router/account regression fixtures, Settings disposal checks and the feature-specific temporary Go-server integration cover the UI boundary. Bundle original mark notices with frontend output; remove generated `dist` directories from the final worktree. Record browser and native visual/keyboard evidence separately in pull requests, issues and CI logs/artifacts. Component/browser fixture success establishes no real-provider login, native quota collection or native platform acceptance.

## Dependencies and Integrations

Use the existing React/TypeScript, Rsbuild, React Query and generated Connect Query workspace dependencies without adding packages. Integrate with the existing Account resource, System capability read, authenticated SubscriptionService and Settings-local mutation registry. API AccountSettings retains its separately owned Provider inventory and wizard. Native provider lifecycle/quota follow-ups remain separate from presentation fixtures and require generated capabilities/operations.

## Change Triggers

Update this contract, the desktop contract, project index and owning frontend AGENTS rules when hierarchy, lifetime, identity, quota or action-authority boundaries change. Keep the docs catalog current for ownership changes. Imported mark changes require pinned source/modification notices and the repository license contract to stay synchronized. A future wire change must update its protocol and API-client contracts alongside generated sources.

## References

- [Project index](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Credentials](cmds-delidev-credentials-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [API client](packages-delidev-api-client-contract.md)

Native quota and reset-credit controls follow the [managed subscription contract](cmds-delidev-subscription-contract.md#native-quota-and-reset-credits--issues-1096-and-1104). Refresh-all invokes one server-owned complete operation; current account settings expose separate quota refresh, default-off observed-recovery notifications and revision/generation-bound credit confirmation. Retained uncertain requests and original-key reconciliation belong to the current Settings visit and never implicitly run after disposal.

Quota row refresh selects the active execution lease machine, falling back to the retained native owner only when no lease exists. Other active lease kinds disable that action. Recovery Inbox details show the account and quota observation time without terminal content; read-state changes grant no quota, session or execution authority.

Row quota refresh remains unavailable while a lifecycle operation, removal/recovery owner or queued/sending/uncertain observation retains account authority, including after the client acceptance mutation finishes. Current connection and generation are required.

### ChatGPT sign-in failure presentation

Retain the existing login-first hierarchy, Back action, theme and account lifetime. A ChatGPT native failed/unsupported/expired/recovery result shows `ChatGPT sign-in failed` as `role="status"`, followed by an ordinary `role="alert"` explanation and compact version, optional legacy minimum version, failed step, error code and optional opaque reference. Reconstruct explanations from closed metadata; never render server/native raw text. An empty detected version shows `Not detected`; an absent or invalid diagnostic shows `Not reported`. Keep independent recovery/cleanup uncertainty visible. Guidance explains the original failed step locally, including concrete installation, credential-access or permission prerequisites when supported evidence identifies them. Original inspection, cleanup and explicit retry controls remain in the owning sign-in task; Connections remains optional.

The approved 1280×800 preview illustrates initialization failure; it is not observed failure evidence for Codex 0.159.2. Preserve natural wrapping on narrow screens, the departure footer and existing controls. Failure may present existing owner-controlled inspection, cleanup and explicit retry actions locally under issue #1699; it adds no new repair capability, copy action, automatic focus movement, browser/device-code fallback or automatic retry. Grok retains its existing unsupported message; Claude account onboarding and management follow below. Confirmed naming keeps its original once-only focus behavior. Original Settings lifetime, late-callback suppression and once-only mutations remain unchanged.

## Claude account onboarding and management

Claude Add account requires inventory 17, System `CLAUDE_SUBSCRIPTIONS_V1 = 38`
and trusted native browser control. The existing wide 960px Settings task dialog
shows Runner Device → Sign in → Account name. Opening the task does not save an
account or start authentication. Select one local/remote machine advertising
Worker `NATIVE_CLAUDE_SUBSCRIPTIONS_V1 = 20` with the single verified original
Claude Code `2.1.236` installation. Bounded Runner pages and explicit refresh
retain current selection semantics; discovery metadata alone cannot enable it.
One explicit Start sign-in saves default disconnected Claude metadata and
submits one sticky original login request. Duplicate clicks, Strict Mode,
reconnect and progress polling cannot create another account or operation.

Login shows the selected Runner and opens the official CLI's original URL
through the lifetime/window/server-bound native browser action. Open browser
again is explicit and reuses that same binding. Only the native browser-code
method shows the masked Approval code input. The input remains outside React
state, shared query/mutation caches, persistence and logs; hide clears unsent
input. Submit clears the DOM value immediately and zeroizes the outgoing buffer
when the request finishes. An uncertain result keeps the original one-time
claim and permits no second code or automatic replay. No DeliDev callback/code
exchange is involved.

Success requires fresh original operation, account/profile owner, generation
and connection proof before the Account name step. Focus moves once to the
default `Claude` name; user edits use the exact alias-only patch. Save account
name and Later both retain the connected account. Management displays the
original owner Runner, Sign in again and logout. Reauthentication targets that
same profile/machine and may not move authentication. Claude shows no quota or
reset-credit controls.

X, Escape and Back hide an active task through the existing shared task host;
its original controller keeps observing accepted work. The subscriptions view
offers View original operation to reopen a hidden task without starting another
login. Explicit Cancel sign-in
requests cancellation and waits for original cleanup. Category/Settings
departure disposes presentation/native browser authority without canceling
accepted Worker work. Failed, expired, unsupported, permission and recovery
states use ordinary status/alert text with closed safe diagnostic
version/phase/code/correlation fields. They never expose native output, URLs,
identities or automatic retries. Unknown acceptance retains only the original
request and offers explicit original-request reconciliation.

Claude deletion composes the existing confirmed account-deletion controller.
Connected accounts require capability 38 and logout on their original owner
Runner. Only typed original success plus a fresh unchanged account with no
connection, generation, profile/owner, lease, pending operation, removal or
recovery permits configuration deletion. Offline/uncertain cleanup keeps the
account. Already fully cleared accounts require no native capability. Historical
references and protected server deletion rules remain authoritative.

Keep the existing focus trap, trigger restoration, scrolling body/fixed footer,
40px controls and responsive button stacking. Test 1280×800 and 480×800, both
themes and 200% zoom. Browser/component fixtures and generated preview boards
do not establish packaged CEF or actual account/platform acceptance. The
approved preview is design input only and must not enter repository assets.
## Automatic failed-login cleanup

`subscription-cleanup.tsx` owns the explicit capability-41 action and metadata-only generated Connect Query status reads. Your subscriptions places Auto cleanup / 자동 정리 immediately before Refresh all / 모두 새로고침, with shared 40px controls, 8px corners and an 8px wrapping gap. The idle help is Deletes failed login and disconnected subscription accounts. / 실패한 로그인 계정과 연결 해제된 구독 계정을 삭제합니다. The server selects fully disconnected subscription configurations for every supported service, in addition to failed initial ChatGPT logins; referenced or unconfirmed-cleanup accounts remain retained with reasons. There is no confirmation dialog. Missing capability, unreadable permission/connection, stopping server or another account workflow disables admission with a readable reason.

Pending admission/job status shows Cleaning up… / 정리 중… and processed/total counts above the rows. Account mutations and duplicate batches are blocked, while Settings navigation stays available. A terminal observation refreshes the current inventory once and reports deleted/retained counts or No accounts to clean up. / 정리할 계정이 없습니다. A native disclosure exposes retained accounts/reasons with original 50-result pages. Status uses a polite atomic live region without automatic focus movement; both languages and semantic themes retain narrow-screen wrapping.

Uncertain acceptance retains only its original UUID/wire request for an explicit retry. An admitted job never offers mutation replay for failed status reads: its explicit retry reads that original ID. Polling stops on read/shape failures and terminal states; focus, reconnect, mount and language changes cannot submit work. Category disposal drops local queries, retries and late callbacks without canceling accepted server work. Reopening does not recreate a batch. The server alone freezes the complete target inventory and owns cleanup/deletion; this UI has no vault, native login or callback authority.


## Explicit failed-login account deletion

The ChatGPT deletion task sends one existing `DeleteConfiguration` request for a failed initial LOGIN without independent connection/generation/Worker ownership, including credential-cleanup-pending recovery. Go owns original native cleanup, protected credential cleanup and deletion through a durable single-account job. The client does not submit a replacement LOGIN or LOGOUT. It preserves the originally confirmed request/revision for uncertain retries and closes only on a matching successful deletion acknowledgment. Accepted server deletion survives task departure; late responses cannot invoke a disposed completion callback. A definite terminal retained result requires fresh account observation and explicit confirmation. Fully disconnected accounts retain ordinary configuration deletion, and connected ChatGPT accounts retain their original logout sequence.

## Account storage presentation

AI Subscription exposes no account-storage inspection reader, toolbar, row slot, notice, warning or technical disclosure. Opening, refreshing, paginating, revising accounts and changing locale never initiate storage-owned Doctor reads. Follow [Account-storage presentation](apps-delidev-diagnostics-contract.md#account-storage-presentation). Preserve account identity, connection/health and cleanup badges, quota/usage, Details/View usage, login and connection controls, Auto cleanup and independently owned operation failures. Protected storage, server/CLI diagnostics, native ownership and durable cleanup remain unchanged.

### Read-only quota rail

The desktop contract's issue #1694 rail independently reads saved subscription resources and uses the same service-native connection projection. It does not mount this category's login, cleanup or quota operation controllers. Fresh blocking-window aggregation determines its conservative remaining badge; the detail popover may show saved stale values only with explicit state labels. Manage subscriptions enters this existing category without targeted account mutation or automatic quota observation.

## Inline failure ownership (issue #1699)

Subscription onboarding, management and deletion preserve typed safe causes and original recovery actions in their current task. API/OAuth failures retain selected provider/account/profile, original request and once-only exchange authority; native authorization and immutable referenced profiles remain independent. Cleanup uncertainty never permits replacement login, logout or deletion. Terminal attempts retain fresh explicit confirmation where required.

Claude selection retains bounded original machine observations and the typed inventory error. Loading, failed reads, final successful emptiness, current-page exclusions, continuation and stale or malformed evidence remain distinct. Excluded machines expose only validated closed enabled/capability/installation/version/protocol facts; raw native output and paths do not become diagnosis. Selection still requires capability 20 and exactly one verified Claude Code 2.1.236 installation, with unchanged protocol, account owner and server checks. Claude selection exposes no Inspect this Runner shortcut or shortcut-owned dialog for selected or excluded machines. Protocol guidance directs explicit diagnostic work through Runner Devices. Keep the shared original Runner controller, explicit installation/path/protocol diagnostics and original session failure recovery. Pending and uncertain inspections from a retained diagnostic surface still block conflicting Start/save effects for the exact machine; removing presentation grants no eligibility or automatic inspection.


## Server quota controls

Capability 46 enables row and detailed Refresh for eligible server-owned ChatGPT accounts without a Runner Device. Send the existing QUOTA request with omitted machine and exact account revision, connection and credential generation. Active executions use their original Worker machine. Keep queued, sending and uncertain server observations disabled, preserve retained values and last-success times after failures, and show explicit server-update guidance when the capability is absent. Worker reset-credit consumption retains its original Runner Device. Capability 49 separately allows eligible server-owned accounts to review and confirm available exact or count-only next credits without a Runner. Preserve revision, connection, generation, inventory and original owner selector. Zero/unknown inventory and pending ownership cannot enable consumption. Original-operation reconciliation requires confirmed native/file cleanup and preserves its key and selection. No mount, reconnect or presentation callback sends an unsupported quota request. Follow the [subscription server quota contract](cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota--issue-1728).

Subscription and API quota bars share the displayed integer remaining percentage: 0% uses danger-text, 50% warning-text and 100% success-text. Intermediate values interpolate the adjacent semantic tokens in sRGB. Each fill is uniform; retained stale and failed observations use the same percentage color while their original labels and timestamps remain independent. Numeric values, empty zero fills and invalid-value suppression remain unchanged. Theme changes add no observation or account operation.

### Quota reset countdown presentation (issue #1826)

All subscription quota windows, including Details-only additional windows, use
the desktop quota-countdown mode for future validated reset instants. Preserve
window order, fractions, observation times and existing independent quota clocks.
Expiry returns to the original reset timestamp and elapsed/recovery-unconfirmed
wording without restoring quota or requesting refresh. Invalid or missing reset
evidence keeps its original presentation. Shared presentation scheduling grants
no account, login, cleanup or reset-credit authority.

## Account details dialog (issue #1824)

Both the ellipsis Account details action and the additional quota-window entry open the same category-owned Form dialog. Keep Health, Account enablement, Service status and Exhaustion, and show only quota windows after the first two in their original order. The background row keeps its first two windows. Retain one metadata-only safe projection, including its original ID/revision and service; never retain Resource documents or row operation callbacks for presentation. Existing accepted inventory updates may replace it only at a nondecreasing revision. Reached identities distinguish bounded payload eviction from confirmed account removal. Removal, authorization loss and category/Settings departure close the dialog without reads or effects.

Use the shared approximately 12% black backdrop (`--backdrop: #0000001f`), inert background, scrolling body, fixed header/footer, semantic themes and 40px controls. Header X and native Escape dismiss; there is no duplicate footer Close action. Preserve mounted inventory, Advanced state and scroll. Restore the opening control, or the visible subscriptions heading when virtualization removes that control. A metadata handoff preserves the original opener through the replacement task and never restores into a disposed category.

Manage metadata alone occupies the footer. Close details before handing the exact selected identity once to the existing management controller under current category, cleanup, connection and mutation guards. This works after payload eviction: the management task's existing Resource reader validates the exact account ID/service and minimum revision before lifecycle controls appear. The minimum revision is the greater of the retained valid presentation revision and current reached identity revision; a stale inventory update cannot lower that proof. Presentation snapshots grant no account authority. Failed, missing or stale reads remain unavailable under that original guard. Opening and closing details add no Resource read, inventory refresh, mutation or polling; closing the separate management task retains its existing explicit inventory refresh behavior. Locale changes, reconnect and rerenders never replay the handoff.

English/Korean strings and shared keyboard focus containment remain unchanged. Required frontend/localization checks and synthetic zero-to-three-window, eviction/update/removal, opener fallback, metadata guards and once-only handoff fixtures cover this boundary. Browser/component fixtures and builds do not establish real-account, installed-native or packaged CEF/platform acceptance.

The saved quota rail popover reports each retained window’s actual observation state and timestamp. It does not display a generic failed-operation warning merely because saved details are open. Saved-read failures and incomplete aggregate evidence retain their owning explanations; Recheck reads saved resources without starting quota collection or proving recovery.

### Compact reset-credit presentation — issue #1855

The account-management reset-credit section shows the authoritative available
count and one Use action. Details start collapsed. Use expands exact-credit
selection and focuses its heading; it never automatically selects or consumes a
credit. Only explicitly null details with a valid positive fresh count permit the
original native-next confirmation. Missing or malformed details grant no selector.
The returned detail count remains separate from the authoritative available count.
Rows show localized one-based Reset credit / 리셋권 names in the current returned
detail order, including unavailable rows. Full inert original IDs appear below
with a localized ID label, followed by availability and valid supplied expiry
timestamps. Selection accessible names use the friendly number. Native titles
and descriptions remain private. Numbers never become resource or request IDs.
Selection uses the same strict RFC3339 calendar validation as expiry presentation.
Omitted/null expiry retains ordinary eligibility; malformed or expired supplied
expiry grants no selection or confirmation authority.

Confirmation captures the selected ordinal alongside the original credit ID and
account/revision/connection/generation/inventory/owner bindings. It displays that
captured ordinal in the current locale and the complete original ID, even after
returned rows change order or are replaced; stale bindings still disable sending.
Confirmation names the original account and selection, explains that consumption
spends a credit, and keeps quota recovery separate. Keep dismisses it without a
request and restores its original selection opener or the section heading.
Credit identity participates in the row key, so a different credit at the same
position cannot inherit the original opener DOM node or focus. Collapse,
locale changes, resize and background updates retain the mounted controller,
original selection, request IDs and account/revision/connection/generation,
inventory and owner bindings. Only explicit confirmation sends consumption.
Outcomes, failures, uncertainty, original retries and cleanup-gated reconciliation
remain visible outside details. Existing Worker/server capability and eligibility
gates remain authoritative; unknown, zero, stale, pending, unsupported and
ineligible states expose localized reasons without manufacturing zero counts.

Use “Reset credits” in English and “리셋권” in Korean. Scope the semantic themed
styles to this section: 14px body, 12px secondary, 16px heading/padding, 8px corners
and controls at least 40px tall. Below 480px section width, wrap the summary action
and stack detail rows. Preserve parent dialog dismissal and keyboard ownership.
Browser fixtures cover 640px/320px sections, both languages/themes and 200% zoom;
they grant no installed-native, real-account or platform acceptance. Add no RPC,
allocation, migration, persistent preference or native behavior.

## Server quota V2 negotiation — issue #1854

Individual row/detail Refresh and Refresh all require System 50 and send an omitted machine selector even when the account retains Worker ownership or an Execute lease. The selected server owns five-minute quota maintenance. An absent or disconnected Runner Device does not disable this quota lane; lifecycle/recovery/removal and independent quota/reset-credit obligations still fence competing work. Older servers receive update guidance and no expanded request. Preserve exact retained mutation bytes, last-success evidence, failed/stale status and observed-recovery preferences. Reset-credit confirmation keeps its original idle server versus explicit Worker ownership and is not widened by quota availability. No broad settings redesign is introduced.

## OpenCode Go subscriptions — issue #2097

The [OpenCode Go contract](cmds-delidev-opencode-go-subscription-contract.md) owns the exact key-backed `opencode_go` exception, fixed server relay profile, original native session header and independently confirmed cleanup. Identity 4, System 54 and Worker 28 retain separate ownership; System 52 remains Project behavior. Native login and quota authority remain unavailable. No migration is added.
