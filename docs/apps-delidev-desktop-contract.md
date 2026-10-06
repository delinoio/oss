# DeliDev desktop client

## In-app toast notifications

`src/toast-notifications.tsx`, `src/toast-store.ts` and the static scoped stylesheet own transient in-app notifications. Each authenticated connection mounts one `NotificationProvider` inside its existing connection identity boundary. `useNotifications()` exposes `notify({ kind, message, id?, durationMs? }): string` and `dismiss(id)`. `ToastKind` is the closed success/info/warning/error enum. Publishing updates only the viewport subscription, not conversation/query consumers. Isolated consumers without a provider receive an inert controller; missing optional presentation cannot turn an acknowledged business operation into a failed mutation.

Display the viewport at 80 CSS pixels below the webview top, centered across the complete webview rather than its main-content column. Use a maximum 420px width, minimum 16px side margins, 16px internal padding, 10px corners, 14px wrapping text and a named 32px close button. Reuse the semantic light/dark theme colors and decorative repository-owned SVG icons. Static CSS preserves the production CSP, reduced motion, underlying layout and pointer access outside the toast cards. Long content scrolls within the available viewport height. Notification content is inert text, bounded to 4,096 code units, with no HTML, implicit links, raw-error projection, persistence, telemetry or content logging.

Default visible duration is 5,000ms; zero requires explicit dismissal. Durations must be nonnegative integers within the browser timer range. Hover, internal keyboard focus, document invisibility and a visible native modal suspend the remaining countdown independently. The wide nonmodal sidebar region does not suspend presentation; its compact modal drawer does. Modals hide the toast viewport until closure without moving focus or expiring its items. Same-ID publication replaces content and duration in place while retaining arrival order and current hover/focus pauses.

Show the first three arrivals and retain later arrivals in FIFO order. Countdown begins only when an item becomes visible. Bound the complete list to 20 entries; overflow drops the oldest queued entry while retaining the three visible entries. Ordinary navigation and same-identity reconnect preserve the provider. Connection replacement/unmount disposes its entries and timers, and its old controller ignores late publication. Strict Mode cleanup/setup restores an active empty store without duplicate viewports, retained callbacks or timer leaks.

Success, information and warning content use polite status announcements; errors use assertive alerts. Include a non-color kind label for assistive technology and keep icons decorative. Creation never takes focus. Close supports pointer, Tab/Enter/Space and Escape within the toast, preserving global dialog Escape behavior. Explicit focused dismissal returns to the last connected, available external focus target or the main content fallback.

Notification preferences and general configuration editors publish fixed safe success copy only from their existing acknowledged-save callbacks. A configuration response with a job takes precedence over any resource and remains an accepted operation, not an immediate save completion. Unknown acknowledgments, jobs, failed/uncertain requests, cancellation and late results from disposed Settings visits do not publish success. Original request IDs identify the toasts. Preserve existing mutation receipts/retry rules, query invalidation, focus handoffs, detailed errors and job inspection.

These toasts neither request OS permission nor claim/report native delivery, modify Inbox read state or enable session actions. OS notifications, durable Inbox reservations, synchronized preferences and native activation retain their existing owners and contracts. No Connect API, migration, storage, native permission or package dependency is added. Component and browser layout checks remain separate from packaged CEF/platform acceptance; record validation in pull requests, issues and CI artifacts rather than repository evidence files.

## Model request Diagnostics panel

Issue #1103 adds an explicit Diagnostics panel beside the retained session conversation. It uses generated owner/client Connect Query after `REQUEST_DIAGNOSTICS_V1` capability validation, with an optional exact execution UUID filter, explicit refresh and 50-record pages. Validate the entire page's original session/execution, closed source/operation/state/error/settings, identity spelling, timestamps and precise counters before rendering. Keep native input selection/effective settings separate from proxy request/provider observations, and unavailable fields separate from zero/false. A send claim cannot prove provider acceptance; native terminal metadata cannot confirm native cleanup. Historical absence cannot be reconstructed. Escape/Close returns focus to Diagnostics and preserves the composer; inactive diagnostic queries are canceled/disposed. No mutation intent, automatic retry, payload display, usage reconstruction or public opener is introduced. See the [diagnostics contract](cmds-delidev-diagnostics-contract.md) and validation records in the integration PR.


## Scope
`apps/delidev` owns the React desktop presentation and native Tauri host for DeliDev. The full desktop requirements in issue #964 remain normative; record implemented surfaces and remaining native/product work in pull requests, issues and CI logs/artifacts.

## Runtime and Language
React 19.2.8 and TypeScript render the trusted app through Rsbuild. The native Tauri component implements the window, bounded Go sidecar startup, private client bootstrap, saved server windows, local-server supervision, tray presentation and native notifications. Go owns every product operation. The frontend development origin is fixed at `http://127.0.0.1:46311`; conflicts fail. This is a desktop application, not a browser product or deployed website. The separate [parallel browser QA](apps-delidev-qa-contract.md) entry uses disposable real Go servers and Workers; it grants no native-window authority and is excluded from shipped entry points.

The native crate is a root workspace member. Its optional `desktop-host` feature uses CEF from the immutable official Tauri revision `c8c75b1f7f43e7cb1e7d773ed2f6f96fad2fe975`, sharing the existing workspace CEF pins. The executable routes native helper invocations through `tauri_runtime_cef::cef_entry_point` before parsing product arguments or starting Go controllers. macOS retains the 13.0 minimum. Capabilities match only registered trusted local (`main`/`local-*`) and saved (`server-*`) webview labels, never every webview in their containing window; external child views receive no app permission by window association. Trusted windows remain incognito and deny external navigation and new-window requests. Account browser persistence, raw CEF isolation and exact cleanup are specified in [the protected browser contract](cmds-delidev-browser-contract.md); native/platform acceptance is recorded separately. The default library tests require no display. The executable is `delidev-desktop`, bundle identifier `io.delino.delidev`.

## Users and Operators
One server owner can connect multiple paired desktop clients. The initial prerequisite checklist links to saved settings without installing a harness or overriding existing setup. Automated readiness checks remain pending.

## Interfaces and Contracts
The session Subagents disclosure follows the [child observation contract](cmds-delidev-subagents-contract.md). Require the typed server capability; use generated Connect Query resource reads with bounded pages and revision-driven refresh. Display exact child/parent/root and source identities, independent status, requested versus observed model, partial recent output and unavailable telemetry. Preserve exact nullable decimal counters and original native usage strings without additive billing. Only refresh/page actions are offered; parent completion does not finish descendants or grant native controls.

Validate the complete 50-record child page before rendering content, including each original session-owned resource envelope, exact supported harness/version, execution/native identities, bounded output/status/source graph and source-specific exact usage parity. A malformed or foreign member makes the whole page unavailable or inconsistent, exposes no child content and disables Next. Check known-parent consistency without requiring parents on the same page. Retain earlier supported output/model/usage when later task/activity observations omit them; validate the retained coverage separately from the incoming-source matrix.

- Use `@delinoio/delidev-api-client` and generated service-specific `@connectrpc/connect-query` descriptors for direct authenticated Connect RPC. No Rust agent traffic proxy or duplicate eligibility/routing engine is permitted.
- Preserve independent outcome, Archive, dispatch and recovery states. Actions use original resource revisions and stable request IDs; uncertain retries reuse the original immutable request, never another mutation identity. Restore cannot imply Resume.
- Keep connection-owned drafts mounted across supporting-surface navigation. Settings-local drafts live only within their active category, as specified below. Settings is a regular main-content destination with shared category navigation; Escape does not leave it. Workspace icons have explicit accessible names in addition to appearance.
- Conversation content is inert text. Never render native content as HTML, execute a returned command or open arbitrary links automatically. Transcript tools/artifacts remain distinguishable from user/assistant text.
- Search, activity and inbox use bounded pages. Inbox reading and answering remain separate. Unknown/unsupported native capabilities are explicit and cannot enable an emulated action.
- Native initialization, local server lifetime, protected pairing, tray, notification/widget, signed update and session-side-app requirements must each have implementation and actual platform evidence before desktop completion is claimed.

### Shared form-control focus treatment

Every `input`, `textarea` and `select`, including checkboxes, radios and file pickers, retains its ordinary appearance when focused by a pointer, Tab or programmatic autofocus. Do not add a focus ring, glow, shadow or focus-induced border-color change. Suppress browser focus outlines in shared CSS and exclude these elements from category-specific focus rules. Preserve ordinary borders and static checkbox outlines, selected states, insertion carets, labels, validation states, keyboard operation, focus order and existing focus handoffs. Buttons, navigation, disclosures and other focusable content retain their existing visible focus indicators. Apply this treatment in Light, Dark and System appearance throughout the desktop frontend; category-specific focus requirements apply only to other focusable elements.

### Native Codex model observations (issue #1206)

The Agent Worker wizard Model step exposes an explicit native observation disclosure using generated
`NativeModelQuery` bindings and the server capability. Select the Runner Device,
connected account and hidden-model policy with their original revisions. Retain
uncertain discovery/cancellation requests exactly, lock scope changes while they
are unresolved, and inspect accepted jobs through read-only queries. Pages select
one immutable observation; failed jobs can expose the separately labeled last
success without claiming freshness. Render picker and executable IDs separately
and metadata as inert advisory text. Use model prepares the executable ID for final Worker saving under its
selected API-account source. It never saves automatically, and only selected
accounts can supply the observation scope. Keep managed-subscription unsupported and
follow the [native observation contract](cmds-delidev-native-models-contract.md).

### Execution-device terminology (issue #1136)

The New session machine selector has exactly **Runs on** as its visible label and accessible name. Its `ResourceChoice` passes `resourceLabel="Runner Device"` for the **Select runner device** placeholder and loading, empty-page, cached-read and unavailable-selection status nouns. `resourceLabel` is optional and defaults to `label`; `emptyLabel` still takes precedence. The unavailable selected identity uses **Selected Runner Device** and retains its original ID without choosing another resource.

Other former Execution Worker presentation labels and messages use **Runner Device** or **Runner Devices**, including checkout and schedule selectors, **Remediation Runner Device**, Settings navigation/help, diagnostics and the **Runner Device and harness** prerequisite step. PR planning reports **The selected PR Runner Device is not connected.** Schedule activation guidance is **Edit its selected project, Agent Worker and Runner Device to clear the retained disabling problem.** These messages retain their original conditions and error classifications.

This naming boundary preserves Agent Worker, generic technical Worker references, user-assigned resource names, CLI commands, logs, error codes, authorization, RPC/storage fields and the `execution-workers` Settings category value. Selection, Local pinning, mounted drafts, bounded pagination and exact uncertain-request retries retain their existing behavior; no protocol, dependency or migration change is introduced.

### Chat-first session creation
The issue #1057 flow is the mounted `Surface.NewSession` page, not a dialog. It retains the selected session and connection-memory creation draft while navigating to sessions, Settings and other surfaces. Back returns to the existing selected conversation or welcome view. Entry focuses First message; changing surfaces during an accepted create cannot steal navigation or focus when the response arrives.

The page centers an 820px maximum content column around **What would you like to work on?** Project selection precedes the composer. An empty project means **General Chat**, an isolated projectless directory on the explicit Worker. The composer has a 210px base height, 18px radius, accessible First message textarea, the specified task placeholder, Agent Worker and Runs on selectors, Execute/Plan control, inline Options, and a 40px Create session button. Keep 24px minimum side padding and ensure 960×640 remains scrollable without horizontal clipping. The sidebar retains its rail/pane geometry and the shared Home accumulation/scroll scope described below; loaded global and expanded project ranges refresh every 15 seconds while active and visible.

Initial workspace/mode is General Chat/Execute; neither Worker is preselected. The page reuses paginated selectors, project restrictions, optional budget validation/disclaimer, Worktree/Local selection, repository starting references, and fresh same-computer Local proof. Selectors distinguish loading, an empty current page, permission denial, connection failure, and stale cached choices; an off-page selection keeps its exact identity without silently choosing a replacement. General Chat removes Git options. Enter submits except during IME composition; Shift+Enter inserts a newline. UTF-8 input is capped at 256 KiB while retaining the previous valid draft on overflow. Drafts and Options survive same-identity navigation, Settings and reconnect in memory only; identity changes clear them.

Require `AUTOMATIC_TITLES_V1` from `GetStatus` before enabling creation. Missing support preserves the draft and offers update guidance; never synthesize a manual title. Reuse the exact retained create mutation and retry identity. On accepted readable success, clear the submitted message once and retain selections; open the session only while this page still owns activation, otherwise show a nonmodal Open session notice. An unreadable accepted identity preserves the draft and blocks duplicate creation until session inspection. No creation modal/backdrop/focus trap or Escape-close behavior is used.

Session detail and loaded sidebar rows show server-owned automatic title state and safe reason. They never infer state from a local request or overwrite the status with cached selection. Manual session names remain ordinary names.

### Contextual desktop navigation
The desktop shell uses a shared 52px icon rail beside a 288px context pane; at viewport widths up to and including 1,100px, only the pane contracts to 256px. Preserve the native 960×640 minimum. The rail order is Sessions, Pull requests, Usage, Schedules, Activity, with Settings at the bottom. DeliDev remains static, left-aligned text. The header Inbox bell and Search magnifier appear only in the home area (`Surface.Sessions`, including welcome and selected detail, and `Surface.NewSession`). Other destinations render no action group, buttons, disabled controls or placeholders. Keep the 34px minimum header height, bell-then-magnifier order, 34×34px targets, 18px decorative SVGs, 2px action gap, `#5b6577` foreground and existing hover/focus treatment; hiding actions must not move the heading, pane content or rail. Accessible button names remain exactly **Inbox** and **Search**. Their existing callbacks select the corresponding context surface without selecting a rail item; Sessions returns to the retained conversation or welcome. Sidebar-only styling must not alter the conversation/composer, title bar, or right-side panels. The rail and groups remain keyboard-operable with visible focus, descriptive names/tooltips, and reduced-motion-safe status graphics.

Below 760 CSS pixels, retain the rail and show a purpose-named **Open …** control in the main area. It opens one native modal drawer beside the rail with a visible Close action, Escape handling, contained focus, background inertness and focus restoration to the opener. Keep the same surface controllers and query observers while switching between pane and drawer placement; filter edits alone do not close the drawer. Selection, opening a destination, explicit Apply/Reset/Search/Load and schedule selection close it. If the viewport becomes wide while open, close modal mode without dropping state and focus the corresponding visible navigation control. Reflow without clipping at 200% zoom.

Header-origin navigation and Settings entry/departure transfer focus after the destination DOM commit and drawer close. Consume a pending destination in the parent synchronous layout effect: wide viewports focus `#main` with `preventScroll`, allowing the existing subsequent first-entry Search query autofocus to win; compact viewports focus the persistent destination opener (**Open inbox filters**, **Open search filters**, or the other surface’s purpose-named control). First compact Search query autofocus still waits for explicitly opening its drawer. Evaluate the current `(max-width: 759px)` media query at handoff, discard superseded navigation/Settings intent, and never schedule a competing later autofocus frame or focus an inert/closed destination. Other ordinary rail, tray and notification navigation retain their existing focus behavior. Settings entry/departure uses the visible main or compact destination opener; explicit New Project entry retains once-only Name focus and departure never restores a former modal opener. Visibility depends only on surface, including loading, empty, denied, offline and cached-refresh states; controllers, query instances, filters, cursors, per-surface scroll, selected session, composer and New session drafts remain connection-scoped memory. Hiding actions introduces no Web Storage, extra reads/writes, feature flag, API/schema change or telemetry. Issue #1149 supersedes only the previous globally visible header-action rule. Record verification and native coverage limits in issue #1149, its pull requests and CI runs.

### Project-grouped Home navigation
Sessions and New Session share one connection-owned Home navigation/scroll scope. The 52px rail, 288px pane (256px at <=1,100px), native 960×640 minimum and <760px native modal drawer remain unchanged. The static DeliDev header with existing Inbox/Search and the 36px New session action stay above the independently scrolling inventory. Projects and named/fallback groups precede General Chat. Home uses system fonts, an 18px brand, 14px conversation titles, 12px secondary text, 34px flat rows, 8px ordinary radii, and 4/8/12/16/24px spacing. Its pane is `#F7F8FA`, text `#202632`, secondary text `#5B6577` and separators `#E3E6EB`; blue is reserved for primary action, selection and focus. Keep the New project plus at 34×34px with its existing 6px radius. Other menu contexts, their manual paging, conversation/composer, native title bar and right panels retain their behavior.

Generated authenticated Connect Query reads keep independent project-catalog, unfiltered global-session and expanded named-project chains, with 50 records per request. A visible continuation within 96px of the scrolling root's bottom requests another batch; underfilled lists continue serially until filled or exhausted. Hidden, collapsed, inactive and document-hidden anchors cannot traverse. Each scope has one reader and its own abort/publication generation. Leaving Home, closing a project, changing filters or replacing a transport cancels the affected reads and discards ignored-abort outcomes. Sessions/New Session and same-server/device reconnect retain accepted ranges, selection and scroll; a different connection identity drops them. No Web Storage or durable navigation state is used.

Append accepted rows in server order and deduplicate by original Resource ID, preserving exact bigint revisions. Empty batches with a new continuation remain traversable; absent continuation stops, and repeated/non-advancing tokens produce a stable failure with explicit Reload list. The empty global project selector remains unfiltered. Only an original empty parent is General Chat; off-catalog parents retain `Project · <UUID>` fallback groups. When a later catalog batch names a fallback, retain its rows, expansion and selection until an authoritative named read accepts success. Equal display names never merge identities. A collapsed/unqueried group is not an empty scope.

Home deliberately has **no application row-count cap or Search-only cutoff**. Retained navigation projections contain original IDs/project IDs, exact revisions, names, workspace/execution/archive values and safe title-state descriptions only, alongside accepted page boundaries/continuations. Metadata grows with inventory reached; total Home memory is not constant-bounded. Full Resource documents do not accumulate, and each projected batch query is disposed after its read. One small active invalidation observer per scope refreshes the accepted chain. Ordinary queries retain the existing eight-extra-inactive-payload policy; in-flight requests are protected until settlement. Keep the 100 expanded-project and 50 collapsed-fallback identity bounds; obsolete single-page and 50-project-cursor storage is replaced by accepted Home chains. Collapsing a group retains accepted metadata.

Active, visible global and expanded named scopes refresh every 15 seconds, reading only as many ranges as already accepted. Refresh uses accepted request tokens verbatim and retains those opaque continuations while validating continuation presence (including final exhaustion) and the end-row identity of nonterminal ranges; cursor-expiry renewal alone is allowed, and boundary drift retains the accepted chain and offers scope-local Reload list. It never discovers an unseen trailing range. Commit successful refresh ranges atomically, and preserve previous rows with a previous-data label after failure. Read failures stop automatic retries. Global-session failure/previous-data notices and recovery stay outside the General Chat disclosure, because global-derived fallback rows can remain visible while General Chat is collapsed. Retry repeats the exact failed scope/token, including a failed refresh range; changed refresh boundaries require explicit Reload list. Typed expiry is `clientFailure(error).code === FailureCode.CursorExpired`, never a numeric status guess. Reload reads that scope's first token and replaces the stale chain only after accepted success; other scopes, selected conversation and drafts remain intact. Distinguish first/additional loading, successful exhaustion, permission denial, connection failure and previous data. Additional loading appears at its continuation; exhausted empty copy is **No projects loaded.** or **No conversations loaded.** No normal First/Next/Load more controls or invented totals are present.

The always-visible New project plus and the successful-empty **Create a project** action select the existing explicit Settings New Project destination without saving. Preserve protected-work deferral, fresh Name focus and exact current-visit drafts/uncertain requests under the Settings lifetime contract below; navigation away disposes that visit; page-level Escape preserves it. The anchored keyboard-accessible Project and conversation options popup owns Include archived and an **Archived included** indicator. Escape closes it and restores its opener. Archive changes reset only global/named session chains, preserving catalog ranges, selection and the mounted conversation/composer; they do not close the navigation drawer. Saving through Settings invalidates loaded navigation without changing archive selection or silently creating another Settings visit.

Issue #1137 supersedes the earlier Home server-management disclosure. Outside the list, Home displays generic connection status under **This computer** or the actual saved profile name, without versions or lifecycle controls. A native non-ready observation or authenticated stopping status overrides cached success and labels previous data as potentially stale. LocalDesktop/SavedDesktop pass an internal TypeScript enum descriptor through App; do not infer identity from endpoints, ReactNode labels or contents. The footer remains separate from the independently scrolling catalog. Original management controllers, confirmations and uncertain requests now stay mounted in the persistent Connection & diagnostics panel; saved verification/local-window controls retain their original authority and lifetime. Presentation cannot cancel, replay, start/stop, register/pair/recover or grant authority.

Long names truncate on one line with complete pointer/focus and accessible descriptions. Preserve independent passive execution/archive glyphs and unknown values, title states/reasons, visible focus, keyboard activation and reduced motion. Automatic insertion never moves focus. Keep one responsive drawer/controller lifetime, native focus containment/background inertness, Escape/Close/opener restoration, and 200% reflow. There is no new feature flag, dependency, preference, persisted schema, migration or write authority. Diagnostics contain stable stage/classification only, never names, prompts, cursors, credentials or endpoint/path values. Record component/browser checks and unperformed native CEF/platform acceptance separately in issue #1161, its pull requests and CI runs.

For issue #1054, record component and native validation separately in pull requests, issues and CI runs, including untested viewports and platforms.

### Menu-specific context panes
Each selected menu owns its context controls while the main content and shared shell stay mounted. Keep filter drafts and applied values, selections, bounded page tokens, scroll position and composer text in the current connection memory. Same-identity reconnect retains this state; replacing the connection clears it under the existing bounds. Suspend inactive surface reads and never write it to Web Storage.

Usage keeps local-time From/Until plus Project, Session, Account, Provider, Model and General Chat filters in the sidebar. Apply validates before changing the request; Reset applies the server-relative last-30-days defaults. General Chat clears and disables Project. Preserve the exact usage values and historical cost behavior below.

Activity keeps the Activity heading, All activity row, divided FILTERS group, Project and Session selectors, then Apply filters and Reset on one row. This private Activity presentation uses system fonts, an effective 16px content inset (including the existing pane/outlet padding), 8/16/24px spacing, 6px radii, `#F8F9FB` content, white inputs, `#D8DEE8` separators, `#202632` primary text and `#5B6577` secondary text. The heading is 18px/650, FILTERS 11px/600, field labels 12px/550 and controls 13px. Controls have 40px minimum targets and grow for wrapping text. All activity has a decorative 16px list icon and an applied-empty-only decorative check, `#E7EFFF` selected fill and `#17499F` selected text. Apply fills the action row in charcoal; Reset has a minimum 52px target and muted text, separated by 8px. Hover is neutral and existing visible focus remains.

Filter edits are drafts; changing Project retains Session. Apply sends both exact IDs, resets only the activity cursor and closes the compact drawer. All activity/Reset clear both drafts and immediately apply the unfiltered first page. Native ResourceChoice selectors retain independent bounded pages and their First choices/More choices actions. Enable their existing read-status messages for Activity: loading, empty current page, denied access, failed connection, cached choices after failed refresh and off-page/unavailable identity stay distinct. Inert wrapping helper text retains only the selected Project/Session labels from existing callbacks in connection memory, always identifies them as last-selected labels and exposes exact IDs; it performs no extra read or persistence. Do not add time/status filters, automatic filtering, Project/Session cascading or selector-cursor resets on Apply. Scope styling to Activity content, preserving the shared rail/header/footer, other contexts, results, Settings disposal, server controllers and the existing responsive drawer. Issue #1137 remains authoritative for server relocation and #1149 for header actions.

The private `activity-sidebar.css` owner applies only to the existing Activity outlet. Its visible outlet fills the remaining scroll height without shrinking long content; hidden controllers keep their normal hidden behavior. Record component/browser fixtures separately from actual packaged CEF acceptance in issue #1156, its pull requests and CI runs.

Inbox keeps All items/Unread/Read, Source, Project and Session in the sidebar. Edits do not read until Apply; Reset applies all defaults. Preserve the landed list/detail workspace, typed answer/approval drafts, explicit read actions, exact notification activation and current-source revalidation.

Search keeps the literal Search conversations query, Archive, Project, Session, Agent Worker, Account and Outcome in the sidebar. Search submits the complete draft; include-archived remains the existing unspecified value, including archiving. Focus the query field once when Search is first entered (or when its compact drawer first opens); returning to a retained draft or polling cannot repeatedly steal focus.

Schedules keeps New schedule, All schedules/Enabled/Paused, Project, bounded schedule rows, refresh, independent First/Next paging and Retained history in the sidebar. Show textual enabled state and the complete server-returned next UTC instant. Status/project selection applies immediately and resets only the schedule-list cursor. No initial schedule selection shows guidance in the main area. While an editor, confirmation, in-flight operation or uncertain request would be replaced, disable schedule selection, filters, New and Retained history entry; global navigation remains available and preserves the exact workflow. Existing schedule defaults and server-owned scheduling behavior do not change.

The issue #1153 presentation is scoped to the existing Schedules sidebar outlet. Its order is Schedules title, New schedule with a decorative plus, horizontal All schedules/Enabled/Paused segments, Filter by project, Saved schedules with decorative icon and visible Refresh text, schedule rows, First/Next, then Retained history. Only this project filter supplies ResourceChoice's optional `emptyLabel` as All projects; the empty string remains unfiltered and other callers retain the exact Select lowercase-label default. Filters keep enum values and aria-pressed, apply immediately and reset only the schedule-list cursor. The selector's bounded page is independent. Refresh retains the existing first-page/refetch behavior and every request retains page size 50, without walking later tokens.

Use the existing system sans-serif stack and pane/ink/muted/accent/selection/border palette, 8/12/16/20px spacing, thin separators and 6–8px radii. Title is 16px semibold, row title 13px, metadata 11–12px and labels 12px. Main actions/inputs/disclosure have 40px minimum height, while segments have a 36px minimum and may grow for wrapping. A pale-grey segmented group has a white selected surface and darker-blue text. The selected schedule's entire list-row button uses pale blue and retains aria-current. Wrap long titles and the complete server-returned UTC instant, showing explicit Enabled/Paused, Next run (UTC): and None scheduled. Successful empty first/later pages retain their distinct text and a decorative clock; errors cannot become successful-empty claims. Keep typed failures, the previous-page refresh warning and cached same-scope rows, without relabeling old-scope rows as a changed filter result. Do not invent totals or execution outcomes.

Retained history starts collapsed. The mounted Schedules controller owns its disclosure boolean, stable region ID and focus reference. A native button connects aria-expanded/aria-controls to a labeled form region; the form remains mounted and hidden while collapsed. Keep the disclosure accessible name Retained history and the submit accessible name Open retained history distinct; the submit retains its approved visible Retained history text. Only user-caused expansion focuses the owned input after rendering. Navigation, refetch, same-identity reconnect and pane/drawer placement preserve state without stealing focus. Toggling makes no RPC, does not close the drawer and cannot replace the selected workflow. The required Retained schedule ID input retains its 36-character bound and trimmed submission; the existing Retained history submit action keeps the exact history ID, bounded query, main history view and drawer-close behavior. Disable both disclosure and form alongside all replacement controls during an editor, confirmation, in-flight or uncertain workflow. Global navigation stays available and exact request/revision/serialized bytes remain owned by the retained workflow.

Disclosure, history draft, filters, selection and independent pages survive menu navigation and same endpoint/server/device-identity reconnect in memory; identity replacement starts fresh and collapsed. Reuse the existing SVG shapes, native keyboard/focus semantics and hidden decorative icons. Preserve the shared 52px rail, 288px/256px pane, native 960×640 minimum, below-760px native modal drawer, independent content scroll and bounded footer. Labels, complete UTC, expanded history and errors must reflow at 1440×900, 1100×768, 960×640, compact effective widths and 200% zoom. Keep Close/Escape, modal focus containment/background inertness/opener restoration, existing selection/submit close behavior, reduced motion and one controller/query scope. Add no font, asset, dependency, inline-style/CSP exception, persistence, RPC/schema/migration, scheduling/defaults, mutation/authorization/retention changes, telemetry or sensitive logs. Ordinary frontend/native packaging remains the rollout path without a new flag. Preserve separately landed header/footer/terminology/new-schedule work and the current Settings visit lifetime. Record issue-specific validation in issue #1153, its pull requests and CI runs; keep browser/component evidence separate from native/platform acceptance.


### Standalone Pull requests
The Pull requests rail item selects `Surface.PullRequests`; it does not open Settings. The sidebar shows one bounded 50-resource repository catalog page with independent First/Next pagination and exact local UUIDs. Selecting a row alone is a local authenticated catalog/resource read and must not query GitHub. The initial state has no selected repository. Load remains disabled for missing integration/profile mappings, GitHub owner/name or supported schema and shows configuration guidance.

State (Open by default), bounded plain title/body terms and PR page size 20 are drafts until explicit **Load pull requests**. Retain capacity-recovery choices 1/5/10/20, First/Next, detail/back and deliberate refresh. Changed repository selection immediately clears previous content; changed filters leave the existing result labeled with its last applied conditions. Reuse validated PR detail, diff, Checks, statuses, rules, required CI, feedback, reviewer verification and retained problem/remediation history. Do not add cross-repository aggregation, GitHub query syntax, new writes or execution capabilities. Repository settings targets Repositories in the current Settings visit through its explicit category-entry workflow, creating a fresh visit when Settings was inactive. Never restore abandoned Settings-local drafts.

Issue #1155 scopes the approved sidebar presentation to `Surface.PullRequests` on the existing sidebar root. Keep the same rail, pane, portal, controller/query instances, drawer and independent middle/footer scrolling regions; the footer retains its 35% maximum. Order the content as Pull requests, Repositories with Refresh, glyph/name/full wrapping UUID rows or the decorative folder empty block, First/Next, a divided Query options section for the selected repository, and the full-width secondary Repository settings action. A successful empty catalog page says exactly **No repositories on this page.**, never that the entire catalog is empty. Preserve page-scoped loading/failure/cached-refresh states. Selected rows retain `aria-pressed` and pale blue `#e7efff`; decorative outline SVGs are accessibility-hidden. Explicit-load guidance remains visible with both empty and selected states.

Use the existing system font, 17px semibold titles, 13px body/control text, 12px sentence-case headings and metadata, 16px horizontal pane insets, 8/12/16/24px spacing and 8px control corners. Primary text is `#202632`, muted text `#5b6577`, borders `#d8dee8`, primary accent `#2563d8` and selected fill `#e7efff` on the existing off-white pane. Inputs and Load remain at least 40px high; Load pull requests is the only solid blue primary sidebar action. Existing inline server/registration/saved-server controls use a 36px minimum action-row height and expand for wrapping labels, status, errors, confirmations and recovery guidance. New footer overrides exclude every nested dialog and its contents, including when the outer pane itself is the responsive dialog; standard modal typography, controls and geometry remain unchanged. Preserve each control's owning visibility, authorization and retry contract. This presentation does not implement adjacent header/control-relocation work or restore controls removed by those owning changes. Other contexts, main content/composer and native window behavior retain their existing styling.

When Pull requests becomes inactive, cancel and remove its disposable GitHub observation/history reads. Returning restores control selections and shows Pending PR actions before any Load. Retain only target IDs and confirmation identity/revision in connection state; keep immutable mutation requests in the bounded existing registry. Exact receipt retries remain available without reconstructing payloads from a new result or forcing a GitHub read. Never keep full GitHub response documents solely to preserve a read. Unsent confirmations cannot authorize a new mutation without fresh matching evidence. Only unsent drafts/confirmations may be canceled; pending, in-flight or uncertain actions cannot be cleared by navigation or repository/PR replacement.

Across all surfaces, distinguish first loading, successful empty pages, empty later pages, permission/authentication errors, connection failures and stale same-scope data. Never show old-scope rows as the new selection or claim a complete inventory from one bounded page. Navigation, filtering, collapse and paging remain read-only; existing explicit mutations keep their original revisions, authority and exact retry payloads. Native visual, focus and OS acceptance remains separate from component/browser evidence.

### Usage
The Usage surface follows the [exact native usage contract](cmds-delidev-usage-contract.md) through direct generated Connect Query. It defaults to 30 server-relative days and supports local-time range, session/project/account/provider/model and General Chat filters with explicit Apply/reset. Keep drafts mounted across navigation. The summary shows arbitrary-precision known subtotals, per-counter unavailable responses, original identity groups with optional current labels, accepted executions missing telemetry, unavailable actual cost and separate currency-denominated historical estimate subtotals. Resume/legacy/child/unsupported telemetry cannot appear as zero or complete. Usage → Model details → Token pricing reads current model/price revisions and creates an explicit source/date/currency/input-mode/rate/exclusion version. Nullable decimal fields preserve blank versus zero; stale drafts and exact uncertain requests remain retained. Source descriptions render as inert text. Historical Usage details expose retained price versions and priced/missing/unsupported category counts, with exact amount strings and no client-side recomputation. No complete-category count implies complete telemetry, verified spend or budget compliance. Refresh failures label cached values, and opening a session never starts execution.

Issue #1052 adds a visible **Token Usage** page while preserving the Usage navigation entry. It presents an overall known-total/input/output hierarchy, the remaining four token measures, explicit measured/unavailable response counts, incomplete-coverage evidence and unavailable actual cost above historical estimates. Filter drafts live in the Usage context pane, persist across navigation, and separate Apply/Reset from the displayed query. Datetime-local values use a frozen detected IANA zone for each applied request and calendar bucket; UTC is the explicit fallback. Applied timestamps are first server-retention times with an exclusive upper bound. The existing session table retains its grouping/order, full source identities, disclosures, and open-session navigation.

Daily analytics show clipped chronological zone-local buckets with exact response evidence; model analytics show the top five measured original provider/model groups and the server's Other aggregate, while full data tables retain every model identity including unmeasured groups. Both data tables show all six exact measures and their measured/unavailable counts; wide table regions remain keyboard-scrollable. Charts distinguish no records, unavailable totals, partial known totals and measured zero without relying on color alone. SVG geometry uses bounded integer ratios; visible numeric output retains exact decimal strings. Hover/focus details, one keyboard entry point per chart (arrows, Home/End, Escape), and semantic data tables expose the same values. Production styling uses static CSS and SVG attributes, preserving the existing CSP and adding no chart dependency. If an older server has no `analytics`, existing summary/table/cost surfaces stay available and the page states that charts need a server update; present empty analytics means a supported empty query.

Issue #1100 adds a separate Verified Grok closed inputs section to Usage. The existing generated summary query explicitly requests NATIVE_UNITS_V1 and checks its echo; old servers keep the response surface with update guidance. Exact BigInt display, native unit counts, original session/project/account/model/provider attribution and semantic daily/model/session tables come from one server snapshot. Show every original project ID alongside its optional current label, preserving identity across duplicate labels and renames. Exclude native-only groups/models from the response detail table and response-chart model inventory, including View data. Retain the no-response detail state when no response groups remain, preserve the complete analytics snapshot for Grok accounting, and keep response groups/models with unavailable counters in the response views. Do not add these inputs to Codex response cards/charts, renderer aggregates, historical price estimates or budgets. Applied conditions explicitly label response first-retention time. The Grok section separately explains that its filters and daily buckets use the server's first retention of verified completion after confirmed cleanup. Daily Grok tables display both exact localized interval endpoints, including seconds and offsets, in the returned IANA timezone; Until is explicitly exclusive, and clipped partial days must not appear as complete days. Retain mounted filters, stale-data disclosure and session navigation.

### Local native connection
Only registered local product documents receive `local-bootstrap`; the initial `main` and additional `local-*` webviews share the existing process-owned launch observation. Issue #1137 treats each fresh trusted main desktop process as intentional local Start. The host locates the bundled sibling `delidev` binary, runs Go-owned `server desktop-launch` once with the exact app/development origins, then `device pair-local` and `device inspect` against its fixed private `desktop-client` subdirectory. This off-UI-thread operation belongs to the host lifetime and finishes before supervision begins. CEF helpers and saved-server windows cannot launch it. Go owns singleton startup, compatibility, detachment, private filesystem validation and all pairing mutations. A newly admitted server runs in that original Go child; compatible live reuse exits the controller without transferring server ownership. The native host independently checks the returned metadata before reading that one credential file; only the separate revocable client credential enters the trusted renderer. It never returns the owner token or permits renderer-selected executable/argv/filesystem paths.

Local product windows additionally own `inspect_local_registration` and `recover_local_registration`. They call the closed Go local-registration commands, never accept paths/argv/origin overrides, preserve canonical UUIDs and decimal uint64 revisions, and do not start/stop a server. Recovery passes only the original device/revision/request identity. After publication, native code independently reinspects the fixed client metadata and reads only its new client credential. The owner token never crosses native IPC; saved-server windows have neither capability.

The persistent native Connection panel exposes Check desktop registration, reached through Settings > System > Connection & diagnostics or startup Troubleshooting. A verified revoked registration offers Re-register this desktop, with explicit confirmation that the old registration remains revoked, server sessions/settings/Workers are retained, and adopting the new identity clears window-local drafts and pending actions without resuming sessions. Unknown/file/permission/owner failures cannot offer speculative replacement. A lost receipt retains the exact request through dialog hiding; after app restart, inspection recovers the same pending request from Go. A fresh non-recovering inspection of a different device/revision on the same server retires an obsolete pending request and its confirmation; a revoked replacement requires a new explicit confirmation/request, while an authorized replacement offers no further registration. Preserve the exact uncertain request when inspection still identifies its original registration. A conflicting in-progress request or foreign server remains an evidence error and cannot discard pending ownership. Double clicks are serialized. Both native registration commands pin the fixed desktop endpoint through a Go-side precondition checked before recovery intent or pairing, so another loopback listener cannot be replaced before an incompatible-endpoint error. Successful adoption verifies the new direct Connect transport and disposes the old connection's memory cache. There is no automatic registration on mount, polling, reconnect or failure.

Local connection and registration permission errors share guidance covering device authorization, file ownership and owner-only access. A permission denial does not establish that the owner cannot read a file: a readable directory with group/other access is also rejected. On macOS/Linux, the guidance specifies 0700 private directories and 0600 private files, with existing data preserved before retrying. Keep the existing native error classification and strict Go privacy checks; the app never automatically changes permissions, resets data or offers replacement without verified revoked-client evidence.

Paired devices labels the current client and omits its Revoke action. The confirmation component independently blocks self-revocation submission/retry, including a stale mounted confirmation. Other devices keep their existing revision checks and exact retry behavior. This is an app UX guard; authenticated owner/other-client administration and the existing Device RPC remain available.

The initial local UI accepts the fixed `http://127.0.0.1:46310` listener; a different live listener is preserved and requires explicit compatible-client guidance. Go rejects it before any legacy lifecycle adoption. Compatible legacy reuse leaves absent lifecycle configuration absent because status cannot prove original TLS paths or origins; supervision cannot invent restart intent after that server exits. The root defaults to the platform user configuration directory plus `delidev`; an explicit absolute native process `--data-dir` selects another scope. Short CLI controller output is limited to 128 KiB per stream and 40 seconds; automatic/desktop Go startup shares a 35-second aggregate deadline across its joined phases, reserving five seconds for native output/exit handling. Short-controller streams are joined, hosted-server drains remain tracked until process exit, and process failures expose stable classifications only. Ambient credentials are excluded from the child environment. Close-to-tray and abnormal client exit leave the Go server alive; normal Quit applies the owned-sidecar boundary below. Saved server selection uses the separate profile/window boundary below.

One native supervision loop per desktop process invokes Go's `server desktop-host --mode ensure`; Go serializes cross-process controllers and checks durable local intent plus the original listener/TLS/origin configuration before launching. Healthy and stopped scopes are checked every five seconds; transient failures use exponential equal-jitter delays capped at 30 seconds, and incompatible/invalid states are inspected at most once per minute without replacing them. Supervision still never starts an unconfigured or explicitly stopped scope. Only a fresh-process launch or explicit advanced Start can initialize an ordinary scope or reopen stopped ordinary intent after original store ownership is released. Both use Go desktop admission, preserving installed native-service ownership through publication and spawn; ordinary CLI Start remains separate. Read-only launch observation may authenticate a compatible live legacy server with absent lifecycle evidence, without adopting restart configuration; explicit stopped intent and an unavailable legacy listener remain authoritative. Same-process explicit Stop remains authoritative until advanced Start or a later fresh process; show/focus, macOS reopen, renderer remount, observation and Retry do not clear it. The read-only `local_server_status` capability exposes only closed state, attempts, bounded delay and failure classification. Native logs report state transitions, never child output. Normal Quit fences new starts, wakes and joins the loop, then stops only original server children admitted by this host. Borrowed CLI/service/remote servers remain independent.

The desktop offers explicit server stop through direct authenticated Connect with retained mutation identity and a description of its effect on clients/Workers. Accepted stop, automatic-restart suppression and session cleanup remain distinct. Advanced local Start uses the existing closed native bootstrap command. Startup Retry is an explicit serialized native action using `server desktop-host --mode retry`; it never clears stopped intent. Renderer connection verification retries at most three transient status reads and never repeats a native startup/pairing operation or product mutation automatically. Reconnection to the same server/device/endpoint/credential revalidates active read queries without replacing drafts or pending mutation identities; an identity change still clears connection-owned state.

The host retains the joined launch outcome in memory. `launch_local` observes it without replaying startup or pairing; pending observations return no credential. Before returning an earlier successful result, Go's read-only `server desktop-status` checks current running intent and authenticated compatibility, and native code reinspects the original fixed client identity. Direct renderer status verification rejects stopping servers. Explicit Start and registration recovery update the host's connection observation only after their own native checks. Quit cancels and joins short controller/supervision work and applies the owned-sidecar shutdown boundary below; independent Workers and retained session data remain preserved.

Desktop launch and supervision hold the native server-service control lock before the startup and lifecycle locks, retain it through admitted startup and release it at readiness, and recheck original registration admission before intent publication and spawn. Compatible live service-owned servers may be reused without modifying service intent or registration. Stopped/unresolved registered scopes block detached launch with the stable `service-managed` classification; malformed or foreign private evidence remains an evidence/privacy failure. Ordinary explicit `server start` keeps its separate semantics. No app launch installs, starts, removes or repairs a native service.

Ordinary startup shows **Starting DeliDev…**, followed by the authenticated product after version/server verification. Failure uses an accessible product explanation, **Retry** and **Troubleshooting**. Stable missing-sidecar, timeout, permission, incompatible, credential, service-ownership and evidence distinctions appear only in troubleshooting. Sidebar connectivity uses generic current/disconnected wording without versions, lifecycle buttons or retry internals. Tray groups each local window as **This computer** with its window number, preserving timestamps, staleness, counts and saved names. Its explicit exit action reads **Quit DeliDev**; quitting stops app-owned sidecars while preserving borrowed runtimes, independent Workers and retained session data.

### App-owned sidecar shutdown

The trusted main host uses `server desktop-host --mode launch|retry|ensure` with its compiled listener/origins. The modes share existing Go admission, compatibility, lifecycle intent and cleanup barriers; Retry/ensure cannot reopen Stop. Compatible CLI/service reuse returns one version-1 JSON envelope and exits. An admitted new server runs in that original child and publishes one bounded ready envelope with its startup generation; its final envelope follows joined server/store cleanup. Business traffic remains authenticated Connect, without a new RPC or SQLite migration.

Native code retains at most eight original child handles and private stdin pipes before readiness, including failed/unknown startup and later explicit Start or automatic recovery. Controller output is bounded and separately drained; business logs use the existing private `server.log`. Renderer, helpers and saved windows receive no control pipe, PID, owner credential or termination authority. Native child process-group isolation preserves the abnormal-exit boundary without a kill-on-parent-exit registration.

Normal Quit immediately fences fresh starts and supervision. A tracked blocking worker joins short commands and the browser's final bounded removal discovery before sending exactly `{"version":1,"action":"stop"}` plus a newline to each retained child. Go validates the bounded closed frame, suppresses only the original startup generation and enters the existing server shutdown path. EOF, malformed input and loss of the desktop process do not stop a running server. A synchronous inherited stdin reader need not wake on Close; helper process exit releases that process-local reader, and native waiting independently joins the entire process.

The sidecar shutdown deadline is 35 seconds from the native request, separate from startup and existing server/native cleanup deadlines. At expiry, native code kills only the retained original child and waits for its actual exit. Reuse never grants authority over a server endpoint or a discovered PID; an old child cannot stop a replacement. Repeated Quit shares one off-UI-thread operation, and native exit remains gated by both sidecar completion and existing raw-browser close proofs. Title-bar close-to-tray preserves the app and sidecar. Normal setup/return failures also join owned children; panic/crash/forced desktop termination preserves their independent running lifetime.

Structured logs distinguish request, restart suppression, joined server cleanup, force request and confirmed/unconfirmed process exit. Forced process exit does not prove native/session cleanup; original data, protected ownership and recovery state remain authoritative. Failure to force or observe exit retains original handles and reports uncertainty without a PID fallback. Actual macOS/Windows/Linux Quit and packaged CEF shutdown remain separate acceptance from controlled process fixtures and compilation. Record validation in PRs/issues and CI logs/artifacts, never repository evidence documents.

The 16 Settings categories are retained; Diagnostics is displayed as **Connection & diagnostics**. Its labelled Connection subsection opens the same persistent native connection panel used before transport. Local lifecycle controls, registration inspection/recovery and Saved servers live there. The panel and connection-scoped Stop mutation registry stay mounted outside the disposable Settings visit, so original confirmations and uncertain request bytes survive hiding and category/navigation changes. Registration confirmation visibility follows the panel without discarding its identity. Native dialogs retain Escape, contained focus and opener restoration. Doctor has its own read-only diagnostics subsection; selecting it never repairs or mutates. Saved windows place their verification and Show local window controls in their own advanced panel and remain connect-only.

No feature flag, persisted startup preference, migration, product RPC, new dependency, automatic revoked-client replacement, permission repair, reset, account login, Worker startup, Resume or harness activity is introduced. Logs contain bounded lifecycle phases and stable classifications, never credentials, child output or private paths. Record actual native/platform acceptance and unresolved gaps in issue #1137, its pull requests and CI logs/artifacts under the root DeliDev validation policy.

Navigation is restricted to the app entry document, including Tauri’s empty custom-scheme path. External navigations and popups are denied. The local window CSP permits only the fixed local RPC and native IPC; only the development policy permits the fixed development HMR connection and inline styles. The app webview is ephemeral. The DeliDev app icon uses a background-extracted version of the supplied 1254×1254 RGBA PNG as its canonical source: the outer dark rounded-square tile and the central dark play triangle are both transparent, while the colored ribbon retains its original shape, gradient, and proportions. The canonical artwork is uniformly enlarged to 1.2× its supplied size around canvas center `(627, 627)`, with zero rotation and the unchanged 1254×1254 canvas; only the outer transparent margins shrink. This approved enlargement is already baked into the canonical source and must not be repeated during export. Preserve all visible artwork without clipping. Derive the 256×256 RGBA PNG for the native window and tray and the Windows ICO entries at 16, 24, 32, 48, 64, and 256 pixels from this source, preserving alpha and clean antialiased edges. Tauri's explicit bundle icon list includes both PNGs and the ICO so the macOS bundle can derive an ICNS from the full-resolution source. Preserve the 1254×1254 canvas and the `icon-source@2x.png` density marker: the pinned bundler downsizes it to 1024px, which ICNS supports as 512 points at retina density only; a density-one entry fails packaging.

Icon export validation reads the final 256px PNG and all six ICO frames after their last conversion. Require near-opaque samples inside the colored ribbon, graded antialiased edge alpha, transparent background and center samples, and mean alpha coverage within one percentage point of the canonical source. The 256px ICO entry must match the native PNG exactly. Inspect the final PNG composited over light, dark, and checkerboard backgrounds. These checks must reject a mostly transparent ribbon even when its background and central cutout remain transparent; validating an earlier intermediate image is insufficient.

### Multiple product windows

File > New Window uses one native app-level `CmdOrCtrl+N` accelerator: Cmd+N on macOS and Ctrl+N on Windows/Linux. It creates a separate Home view for the focused, or most recently used, product window's connection. No session, draft, selected surface, Settings visit or pending mutation is copied. Window geometry remains maximized initially, 1280×820 when restored and at least 960×640, with native decorations and existing app-body styling. Creation failure preserves existing views and reports only a stable native failure plus a fixed error dialog.

The native process-local registry owns Local/Saved roles, unique window UUID instances, monotonic window numbers, preparing/ready/closing phases and focus order. Labels are never reused in the process. Capability patterns apply only to app webviews; a matching label alone grants no authority without the exact registry entry and trusted entry document. Saved windows additionally retain the original profile, server, endpoint, device and CSP. They receive no local bootstrap or saved-profile management authority.

Closing the initial main window does not end local admission or supervision. Additional local windows observe the existing joined launch and independently verify their direct Connect transport. Opening windows never starts another server, pairs a client or creates another supervisor. Local registration/credential adoption advances a process generation and emits an identity-free signal to sibling local documents; each re-reads the adopted observation without startup and authenticates it. Same-identity revalidation retains that window's memory; changed identity disposes only its old connection scope. Native responses recheck original instance, role and connection generation after asynchronous work. OAuth/update callbacks keep their independent native lifetime fences.

Each renderer owns independent connection-memory queries, drafts, Settings visits and exact mutation receipts. Server-owned state still flows through authenticated Connect. All saved-window names update monotonically after a profile rename. A profile-level removal barrier rejects creation and late credentials before every current or preparing view of that profile is closed; durable browser removal staging and uncertain exact retries retain their original authority. Native notification claims remain once-only server grants, independent of the number of windows.

Show DeliDev and macOS Dock reopening restore the most recently used remaining product window without changing geometry. Show local window restores a remaining local view, or creates one from the existing process outcome if all local views were closed. Window destruction listeners retain only registry state, avoiding AppHandle/manager cycles that retain CEF request contexts during shutdown. Quit closes creation admission and joins tracked menu work, while preserving detached servers, Workers and sessions. The registry is never persisted: a fresh process starts with one local window. No product RPC, protocol number, migration or browser storage is added.

### Saved server windows
A local product window's Saved servers dialog uses the [Go saved-connection boundary](cmds-delidev-connections-contract.md). Listing never contacts every server. A masked private pairing document shows its non-secret endpoint/server identity before explicit submission; its original profile ID/name/grant remain immutable through uncertainty and dialog visibility, then clear after confirmed pairing. Existing pending profiles retry only their retained Go intent. An old inventory read cannot erase newer mutation feedback. The native controller sends at most 32 KiB of pairing input through a joined stdin writer, never argv, and preserves the existing 40-second timeout and 128-KiB output bound, covering 32 maximally escaped profile labels. The dialog has no token reveal or arbitrary file/command operation.

Each paired profile may have multiple independent `server-<profile UUID>-<window UUID>` windows, initially maximized with native window decorations. Local windows also start maximized with native decorations. Users can restore and resize each window; showing an existing hidden or minimized window preserves its current geometry and maximization state. The native map pins profile/endpoint/server/device plus a fresh window instance; opening its saved connection again focuses its most recently used live window, while New Window always creates an additional client view. With the tray installed, title-bar close hides only the last product window and retains its drafts, connection and caches; closing any other window destroys that view. Without an installed tray, ordinary close destroys the window and reopening creates a new client view without changing the pairing or server lifecycle. A stale completion after window destruction cannot deliver credentials to a replacement instance. Both native and renderer checks require the original identity before creating the direct Connect transport. Explicit destruction/removal or full app exit disposes the affected window's memory state; other server drafts/caches are never transferred.

Display-name editing captures the original profile revision and request identity, keeps stale drafts visible and retains exact uncertain retries through dialog visibility and refreshed inventory. An accepted rename changes only that profile's label. Existing native window bindings compare every authority field separately from the display name/revision, preserve their original instance, and update title and metadata monotonically. An identity-free native event asks only the receiving saved window to re-read its pinned context. The React subtree, transport, connection cache and open forms remain mounted; a stale notification cannot roll the name backward. Reopening an existing window refreshes CLI-edited labels. Saved windows receive only event listen/unlisten permission, never event emission or profile-management authority.

Only bundled app documents can receive saved credentials. Tauri's app-resource response callback replaces only `connect-src` with native IPC and the one canonical saved server origin while retaining script/style/other restrictions. It never grants wildcard HTTPS or a local window's unrelated local RPC origin. Saved windows deny external navigation/popups, use ephemeral storage and expose only `connection_context`, their argument-free `connect_saved`, profile-owned `saved_worker_proof`/`saved_worker_control` and native presentation of a local product window. They cannot invoke local-server bootstrap, local Worker controls, profile management or arbitrary credential-path reads. Only registered local product windows manage profiles. External development-server documents cannot open a saved credential window; use the bundled native development build.

`connect_saved` delegates fresh TLS/authenticated identity/version checking to Go, then reads only that profile's Go-validated paired client file. Ordinary product RPCs, including event streams, are direct frontend Connect requests under this window's exact CSP. Remote selection never invokes remote supervision/start/update or falls back to another profile/owner token. Status refresh failure displays disconnected state even when an earlier version response remains cached. A saved window uses only its own profile's Worker on this computer, never a local Worker proof. SSH setup and the complete real remote/platform matrix remain required separate integrations.

Connection removal explicitly confirms closing every window for the selected saved connection and losing their unsent drafts, while retaining server sessions, independent Workers and work files. Capture the original profile revision; retain the exact request through uncertain delivery and dialog hiding. A stale fresh confirmation cannot submit. Cleanup-pending inventory exposes only its original removal retry, never open/re-pair/rename. The native local product window checks current metadata before closing, blocks late credential/proof delivery and profile-wide reopening, destroys every affected window, and then calls the bounded Go removal command. A completed removal keeps a process-local admission barrier so a previously started open cannot recreate a window from stale metadata. The server/client device ID is never reused for a new pairing.

Completed removals have a separately paginated, read-only history in Saved servers. An explicitly selected retained Worker has its own status/start/generation-bound stop controls, with no new registration or token/Local-proof delivery. Keep pending stop identity through dialog visibility and block switching selected Worker while an operation or stop confirmation remains pending. No action on this history reconnects or restores the deleted client. Both the dialog and CLI distinguish local credential cleanup from explicit remote device revocation; copied tokens and other client processes are not claimed revoked.

Profile-owned Worker controls accept the same closed lifecycle action and original stop generation as local controls, with no renderer-selected profile ID. Go's `connection worker-*` commands validate the pinned client and retain grant/pairing requests before network effects. Native reads validate the same profile before and after private Worker proof retrieval; the command handler also rechecks the original window instance before returning. A saved window can therefore select Local sessions/schedules with fresh proof for this computer while its server runs elsewhere. Independent profile Workers have independent device/machine identities and process generations. Registration, current server authorization, controller status and session cleanup remain separate facts; no mutation is automatically repeated by status polling or window reopening.

### Native tray and menu bar
One native tray groups every open local and saved-server window, with a process-local window number that distinguishes views of the same server. TypeScript reads `SystemService.GetOverview`, today's existing usage summary and bounded account pages directly through Connect Query. Overview and today's usage summary poll every 15 seconds, and account reads every 30 seconds while the app process runs, including hidden windows. A failed or missing usage read remains unavailable on publication instead of carrying an old total forward. Rust receives only a validated presentation projection, never RPC authority, account credentials, prompts, native tool identities or paths.

Show server observation time, exact active-session and unanswered-request counts, connected/registered Workers, known UTC-day tokens with incomplete telemetry, and separate account quota windows. Preserve zero and decimal precision; failed or missing evidence is unavailable. Today's half-open UTC range comes from the server. A quota cannot become pooled remaining capacity: preserve its own state, observation/reset times and stale result. At most 20 account aliases and eight windows per account are shown, with explicit Settings links for additional records. Email-shaped aliases are masked before native IPC and again before native menu rendering.

Each publication uses the calling window's fresh scope and a strictly increasing revision. Stale scopes, another window's scope and reordered revisions cannot overwrite the current presentation. A native monotonic timer marks unrefreshed data stale after 45 seconds even if renderer timers are suspended. Cached read failures are also visibly stale. Native menus use only closed Sessions, Inbox, Usage and Settings destinations; opening them does not enqueue input, resolve/read a request or grant execution authority. Activation rechecks the original saved-window instance, retains a ticket in memory until the receiving view acknowledges that exact ticket and rejects an older acknowledgment that would erase newer navigation. This preserves activation across delayed mounts and React Strict Mode cleanup.

With a successfully installed tray, title-bar close hides the last product window without discarding unsent drafts or disposing its active native OAuth opening. Other product windows close and dispose their own memory state; a serialized closing reservation prevents simultaneous close requests from destroying the last view. Show and navigation restore that same window while preserving its current geometry and maximization state. On macOS, the OS application-reopen event also unminimizes, shows and focuses the most recently used live product window without recreating its renderer, resetting user-selected geometry or dispatching session work. Explicit Quit joins native presentation/supervision tasks and app-owned sidecars before terminating the desktop client; independently started servers and Workers remain running. A tray installation failure retains ordinary window-close behavior. Native initialization/build evidence is separate from actual menu activation and platform acceptance; Native notification delivery is defined below; the [macOS status widget](apps-delidev-widget-contract.md) reuses this projection in protected metadata snapshots, while signed updates remain a separate integration.

### Native inbox notifications
The React controller polls metadata-only `InboxService.ListNotificationCandidates` every 10 seconds while the process runs, including hidden windows. Server-side client preferences default to questions/approvals enabled and terminal notices disabled. The settings draft captures its original revision, remains mounted through visibility changes and exposes only the same retained request after uncertain saving. Preferences remain specific to the authenticated client and server; OS permission belongs to the app on the computer.

Before any native display, claim that exact candidate through direct Connect. Validate the fresh `may_present` grant, original request/claim identity, closed kind and exact inbox/session source. Never present from receipt replay, an uncertain claim acknowledgment or an unverified response. One serial batch and one latest replacement bound pending work; pages are capped at 50 (20 requested), and claim/report calls have ten-second deadlines and connection-owned cancellation. Report submitted/denied/failed/uncertain once. A report failure never retries display; the durable original claim preserves uncertainty and prevents reconnect/restart duplication. No presentation result changes inbox reading or source responses.

Rust receives only canonical claim/inbox UUIDs and a closed notification kind under its calling window's fresh presentation scope. Titles and body are fixed native strings; prompts, answers, paths, names, tokens and URLs never enter native notification payloads. The host never queries product RPCs. It validates the trusted original window and saved-window instance, reserves a claim before OS work, and keeps the claim seen for the whole process even after closing its notification. Scope replacement, exact scope disposal, window destruction and app exit cancel owned callbacks. A stale disposal cannot close a replacement scope. Native navigation tickets also retain their notification scope and cannot activate after that scope ends. Activation opens only the exact inbox ID through the original connection, freshly joins its current server state and cannot mark read, answer, approve, resume or switch the active session/composer. Missing/mixed sources are unavailable; paused, archived, recovering, replaced and failed-refresh requests cannot expose active response controls.

Permission reads have a five-second deadline and never prompt. Only the explicit settings button requests OS permission; one process-wide prompt admission remains occupied if its 120-second response becomes uncertain. Native presentation has a ten-second deadline, retains at most 256 active handles and expires each after 24 hours. Full exit cancels and joins owned notification and prompt tasks; cleanup has bounded waits and typed uncertainty logging. A process-lifetime 10,000-claim ceiling also bounds uncertain callback retention in the pinned macOS wrapper; capacity is an explicit unavailable state and never evicts a claim into a repeat display. Inbox remains usable. Remove this conservative callback bound only after all platform adapters prove owned cancellation, including the wrapper's response-delegate retention.

- macOS uses `mac-usernotifications` 0.3.1 and the native UserNotifications framework. It requires a real DeliDev app bundle, reads authorization plus alert/Notification Center settings and provides a fixed Open action. Unbundled execution reports bundle-required. Permission does not guarantee a visible banner under Focus or system suppression.
- Windows uses pinned `windows` 0.62.2 WinRT notifications under `io.delino.delidev`; it never borrows PowerShell or another application's identity. Retain activation/dismissal registrations, distinguish application/user/policy/manifest disablement, keep Action Center activation after banner timeout and unregister callbacks during cleanup. Installed AppUserModelID registration and actual supported-system delivery require packaging acceptance.
- Linux uses pinned `zbus` 5.19.0 with the Freedesktop notification service. Require actions, subscribe before Notify, bind signals and CloseNotification to the original unique bus owner and numeric ID, and use the pinned bundler's `DeliDev.desktop` basename. A replacement daemon cannot activate or close another notification with a reused ID. The interface has no standard user-permission query: display service-available without claiming permission or actual banner visibility.

Native status uses a connection-scoped React Query cache and 30-second reads. Disabled/unavailable permission does not claim candidates. Settings explicitly distinguishes unrequested permission, denial, bundle/action/service/capacity failures and Linux service availability. API submission, platform permission and actual displayed/seen notifications are separate evidence. No browser notification API, network broker, arbitrary native payload or OS-permission request from a timer is allowed. Component tests, cross compilation and native host initialization do not establish real notification acceptance on any platform.

#### Notifications settings presentation (issue #1245)

`notification-settings.tsx` and `notification-settings.css` own the Notifications
body inside issue #1236's ordinary Settings page. Issue #1256 supersedes its
category-specific geometry and copy with the shared left-aligned 1040px column,
32px/24px/compact host padding, 26px/32px title, 16px/24px section headings,
14px/20px body, 12px/18px scope, 16px rows and 24px section gaps. Preserve one
host inset, complete wrapping content, 40px controls/8px corners, contrasting
focus and static semantic-theme CSS. Section actions still stack at an available
body width of 600px or less. Decorative outline SVGs are inaccessible; the
approved bitmap remains reference material. Rail/context-pane, compact drawer,
sibling categories and visit ownership stay with the shared host.

| Position | Exact copy |
| --- | --- |
| Title | Notifications |
| Summary | Choose which updates this client receives. |
| Scope | For this client on the selected server |
| Native section / read action | On this computer / Refresh status |
| Preference section / read action | Notify this client about / Edit notification preferences |
| Interaction label | Questions and approval requests |
| Interaction description | When a session needs your answer or approval. |
| Terminal label | Execution completion, failure and interruption |
| Terminal description | When an execution succeeds, fails or stops. |
| Edit actions | Save notification preferences / Cancel notification edit |

Read mode renders server-confirmed noninteractive Enabled/Disabled text, never disabled
checkboxes or guessed defaults. Explicit Edit captures the complete original
preference object and revision, then exposes two native labeled checkboxes with
separate `aria-describedby` descriptions. Preserve the existing active 5-second
poll, category-scoped mutation registry, fetch/error gates, stale alert and retained
draft, successful invalidation and exact request-ID/wire-byte uncertainty retry.
Busy/uncertain operations lock edit controls and Cancel. A peer revision change
retains the draft and blocks a new save; Cancel returns to current data. Initial
loading/failure contains no fabricated values and retains “Notification preferences
are unavailable until this server can be read.” Cached refresh failures retain
values, visibly disclose that they may be out of date and block new edit/save.
Saving, typed failures, revision conflicts and the existing “Retry the same
notification preferences” action remain distinct.

Edit focuses the first checkbox after commit. Cancel and acknowledged Save may
return focus to enabled Edit only while the workflow owned focus before its
controls were removed. Keep a one-shot intent through refetch; document/body
fallback from removal or disablement is not a deliberate transfer. Discard it on
deliberate focus elsewhere, category inactivity, disposal, hidden/inert ancestry,
category drawer or visible child dialog. A dialog appearing while refetch waits
consumes the intent permanently. Late callbacks read current visibility rather
than their original submission closure. Polling, disclosure changes, stale alerts
and reconnect never create an intent. Mounted state survives reflow and same-identity reconnect within the active
Settings category; category departure or leaving Settings
discards it under #1236 without changing accepted server/native effects or sibling
workflows. Page Escape and active Settings rail reselection preserve that visit.

After the rows, a quiet neutral Inbox strip always says “Inbox requests stay
available even when notifications are off.” and “Opening a notification never
marks an item read, answers a request, approves work or resumes a session.” A
native, initially collapsed “About notification delivery” disclosure retains its
state only for that visit and contains “A submitted notification does not prove
that its banner was displayed.” and “Reading an inbox item never answers it.”
Toggling it performs no read, write or permission request.

Native permission stays independent from readable preferences. Preserve explicit
request/Refresh admission, deadlines, 30-second reads and visit-scoped late-result
checks. Only NotDetermined offers Allow desktop notifications; pending request or
read disables its controls. Granted shows “Notifications allowed” and
“Focus or Do Not Disturb may still suppress banners.” Permission scope remains
“Permission is shared by DeliDev windows on this computer.” A read/request error
replaces cached permission success with “Native notification permission could not
be confirmed.” Retain exact checking, unrequested, denied, Linux service-available,
bundle-required, actions-unavailable, capacity, other unavailable and non-desktop
recovery copy from the native settings controller outside the collapsed disclosure.
Linux availability is not user-permission evidence. OS denial never gates otherwise
readable preference editing. No protocol, schema, native engine, migration,
dependency, telemetry, Inbox state or execution authority changes are permitted.

| Native state | Exact status / recovery copy |
| --- | --- |
| Checking | Checking native notification availability… |
| NotDetermined | Notification permission has not been requested. |
| Denied | Notifications are disabled. Enable DeliDev in your operating system's notification settings. |
| ServiceAvailable | The desktop notification service supports actions. This service does not report user permission or whether a banner was shown. |
| BundleRequired | Native notifications require the installed DeliDev app bundle. |
| ActionsUnavailable | This desktop notification service cannot open notification actions. |
| Capacity | The native notification limit is reached for this app process. Requests remain in the inbox; restart DeliDev to clear its native presentation state. |
| Other unavailable | Native notification service is unavailable. |
| Non-desktop | Open DeliDev on your desktop to manage native notifications. |


Record component/browser validation, packaging and actual native-window/platform
acceptance separately in PRs/issues/CI. Browser fixtures cannot prove native
geometry/keyboard behavior or real macOS/Windows/Linux notification delivery.


### Inbox workspace
Inbox is one persistent list/detail workspace for retained requests and terminal results. The list starts with no selection and fetches server-filtered pages of 20 using the generated source and read-state enums. Changing either filter resets pagination; expired cursors return to the first page. Rows show the retained Inbox record time and current read state. Selecting a row performs a fresh exact `GetInboxEntry`; opening from a native notification uses the same path and preserves the item's read state. Reading and notification activation never mark an entry read. Read-state changes are explicit revision-bound mutations.

The selected detail refreshes while Inbox is active and the window is visible, and revalidates on focus/visibility return. This selected-item read is separate from the existing metadata-only notification candidate poll, which remains on its ten-second process-wide schedule. A detail item outside the current list page remains viewable after its exact source is joined again. Missing, mixed, stale or unauthorized source state is unavailable or read-only; it cannot enable a response. Session pause/archive/recovery and active-execution ownership checks remain required in addition to the Inbox read.

Question/approval forms share their typed controls with the session view, but Inbox drafts remain React-memory-only and scoped to the effective connection identity. Each draft retains the original interaction ID, interaction revision and request identity. Preserve a draft after source/request changes for inspection and block applying it to a new request. Bound the serialized collection to 4 MiB and 1,000 nonempty requests per connection; a limit error keeps the previous draft intact. An uncertain submission retains its exact mutation identity and may be retried only after a fresh current-source read. The persistent Inbox controller preserves drafts while the user visits other app surfaces. This UI work does not establish native notification delivery on a supported operating system.

### Session terminals

The session's Terminals pane uses authenticated generated public operations
under the [terminal contract](cmds-delidev-terminals-contract.md). Mounting it
lists retained terminal metadata only after the server advertises session
terminal support. Unknown/unsupported status gates history polling, manual
refresh and selection and hides cached terminal errors; only explicit creation
launches a shell.
Creation supports the Worker's default shell or an absolute override. Each
terminal offers line input, Ctrl+C/Ctrl+D bytes, resize, output reattachment and
close. Creation/control use the connection-owned retained mutation registry;
an uncertain retry preserves the original request and revision. Input focus returns when controls become available after a
pending input or resize. Metadata polling does not refocus an already available
input.
Creation retains its single accepted resource for direct selection and attachment
even when the current 50-record history page omits it.

Output uses one incremental UTF-8 decoder per terminal and exact bigint cursors
across reconnects. Gaps visibly reset decoding, normal confirmed exit flushes
its tail, and stale generations cannot publish after view disposal. The text
view is bounded to 262,144 UTF-16 code units without splitting a retained
surrogate pair. It preserves the native byte contract but does not emulate a
full VT/full-screen application display. Styling remains static under the
production CSP. Hiding the view aborts observation only; Agent Stop preserves
terminals and Archive waits for native cleanup. Worker/shell/cwd and current
terminal state are displayed separately from connection state.

## Storage
The server remains the only database owner. Frontend query caches and unsent drafts are memory-only and scoped to the selected connection. No credential, prompt, transcript, cursor or account browser state enters Web Storage. Client exit cannot stop server-owned sessions. Native profiles and server startup are separate infrastructure boundaries.

Generated Connect Query read keys contain their read request parameters, including search text and pagination cursors, only in the connection-scoped memory cache. Credentials and mutation payloads never enter those keys. Retain at most eight inactive query pages in addition to the bounded currently observed pages; cancel and clear the entire cache when its connection is disposed, including React Strict Mode effect replay. Conversation navigation retains at most 100 previous cursors and always offers a return to the first page.

The last loaded conversation and interaction pages append newly created resources in authenticated stream delivery order, including resources whose Worker-generated UUID sorts before the page's last ID. Earlier pages retain their bounds; duplicate, removed and foreign-session resources are excluded. A new stream snapshot retains only still-present arrivals and refreshes the retained pages without changing the unsent composer.

### Interactive requests and input queue
Session and inbox views expose the same generated interaction RPCs. Questions retain every native ID, exact offered labels, optional free text and an explicitly chosen unanswered array. Bound the complete answer before retaining it. Secret-marked questions disable ordinary response submission until protected delivery is implemented. Reading an inbox item remains independent of answering or approving it; queued delivery is shown separately from native acceptance and closure.

The pinned Codex 0.151.0 approval forms preserve command decision objects and their exact offered amendments. File approvals use the native closed decision enum. Permission forms start with no grant, preserve path/glob/special descriptors and native deny rules, allow a requested write to be reduced to read, and distinguish turn/session duration. A present native entries array overrides legacy path mirrors even when empty. Unknown native versions cannot submit invented choices. The Go server remains the authority for request validity and first-response-wins concurrency.

Queue pages expose edit, remove and explicit Steer for unclaimed items. Editing captures the original revision and preserves the draft after a peer change while blocking stale submission. Steer binds the selected queue item and observed active execution/turn; it cannot fall back to ordinary send. Every uncertain action retains its immutable request for explicit retry, including after another client closes the original request. Refreshing a failed event stream resnapshots without clearing the composer or retained mutation identities.

### Editable settings and account connection
Provider, model, AI account, Agent Worker and instruction-template forms use generated configuration RPCs. Provider presets seed concrete editable values; custom API endpoints and explicit keyless authentication remain server-validated. Model provider/native identity is immutable when editing. Agents retain ordered weighted account links, ordered templates, routing inheritance and native permission/options. Configured compatibility does not establish native execution capability. Project/repository forms and local execution-Worker registration/controller lifecycle are implemented; remote setup, updates and OS services retain their separate acceptance requirements.

Each edit captures its original resource revision and full document. Server-owned account observations and model discovery provenance are preserved. A peer revision change blocks a new save while retaining the draft; an uncertain save retries only its original request. Edits and exact uncertain requests remain available within the active Settings category across responsive layout changes and same-identity reconnect. Changing categories or leaving Settings discards them, including when an accepted save may still complete on the server. Forms cap complete documents at 1 MiB and instruction content at 128 KiB UTF-8 before retention, and selectors retain one bounded page with an explicit selected identity outside that page.

### Agent Worker core and optional presentation

Issue #1158 established the flat Worker form presentation. The four-step wizard
below supersedes its single-form arrangement while retaining the existing shell,
category copy, system font, semantic theme tokens and native geometry. The wizard
body is left-aligned and at most 720 CSS px wide, with subordinate 16px/24px
workflow/section headings, 14px/20px labels and explanations, 16px field gaps,
40px controls and 8px control radii. Harness, Accounts and Model each have their
own step. Configure retains the full-row Name and divided permission group.
Default permission guidance remains “Uses the harness default. Review permissions
before execution.” Native permission/incompatibility explanations and explicit
clearing remain authoritative.

Three native `details` disclosures in Configure start closed for both create and
edit. They may open independently and retain mounted controls and queries:

| Order | Section | Summary and retained fields |
| --- | --- | --- |
| 1 | Reasoning | Current `effort`, or Native default when absent/empty; no normalization |
| 2 | Instructions | Template count; all ordered reference operations |
| 3 | Native harness options | Defaults only for absent/empty known fields or concurrency 0 with no unknown keys; otherwise Customized, explicitly identifying unknown options as retained |

Reasoning effort and Subagent effort use the shared editable combobox in
`apps/delidev/src/reasoning-effort-field.tsx` and its static stylesheet. These
harness-level hints do not establish selected-model or execution support:

| Harness | Reasoning effort hints | Subagent effort hints |
| --- | --- | --- |
| Codex | none, minimal, low, medium, high, xhigh, max, ultra, persistent | The same nine values |
| Claude Code | low, medium, high, xhigh, max | None |
| OpenCode / Grok Build | None | None |

Focus or the list button opens the in-flow list, capped at 280px with scrolling.
Filter hints by a case-insensitive prefix; trim only the search comparison, never
the stored input. Use native default remains the first option and explicitly
passes an empty string. Direct input remains editable, including unknown values;
existing server validation and errors remain authoritative. Opening, closing or
changing harness never writes an effort value or adds an omitted field. A harness
change replaces hints and clears keyboard selection while retaining both drafts.

Arrow keys move the active option and keep it visible. Enter selects that option;
with no active option it closes the open list and retains direct input without
submitting. Escape, Tab and focus departure close without selecting. Composition
keys are left to the IME. Each input has a unique combobox/listbox identity,
active-descendant and help association. Use the existing semantic themes, 40px
controls, 8px corners and responsive form width, without extra panels or input
focus rings. Preserve Codex child capability-disabled values and ancestor form
locks for both the input and custom list actions. The component adds no query,
RPC, discovery, persistence, public schema or native execution authority.

Accounts & routing moves to Accounts, retaining ordered weighted links and all
add/move/remove operations. The legacy configuration RPC and shared field seam
still permit accountless Workers; existing records remain valid. The wizard
requires at least one current same-source account before saving. A harness or
source change clears incompatible model/account choices for explicit reselection;
unrelated fields and unknown document/link/option fields survive save.

Collapsed sections expose Needs attention for read or validation problems, while
expanded selectors retain sanitized detailed diagnostics. Loading, successful
empty current pages (including continuation), permission/authentication failure,
connection failure and cached prior choices after failed refresh remain distinct.
A failed read never becomes an empty inventory or a new verification.

Disclosures use native keyboard semantics and remove closed contents from the tab
order without unmounting them. Invalid hidden inputs open their section before
focus, preserving unrelated fields. Text, identifiers and action buttons wrap.
Inputs/selects/textareas retain ordinary neutral boundaries without focus rings;
buttons and other controls retain their applicable focus indicators. Footer actions
follow normal scrolling, and exact uncertain retry remains explicit. Existing
locks, revision conflicts, accepted jobs, bounded selectors/provider gating,
complete-document limits and byte-identical request retries remain authoritative.

Navigation away disposes the entire Settings visit under #1138/#1150/#1236;
page-level Escape preserves it, with no abandoned request restoration/replay or
late updates to replacement visits. Same-identity reconnect retains the active
editor. Record component/browser checks separately from supported-platform native
acceptance in pull requests, issues and CI runs.

### Shared Settings body presentation (issue #1256)

All 17 category bodies and their existing child workflows use the internal presentation-only helpers in `settings-presentation.tsx` and static `settings-presentation.css`. Existing category controllers retain RPC/query/mutation/authorization ownership, fields/help, exact revisions and retry bytes, polling/cursors, schemas, permission gates and the visit lifetime below. These shared rules supersede the earlier category-specific presentation exceptions, without changing native window or ordinary-page shell geometry.

Every body shares one left anchor, `width: 100%` and `max-width: 1040px`, white/semantic-theme surface, 32px padding at viewport widths >=1100px, 24px at 760–1099px and 24px vertical/16px horizontal below 760px. The pane and main scroll independently. Category headings use one live-announced H1 at 26px/32px semibold, one purpose description when applicable and one scope line at 12px/18px. Section headings are 16px/24px; body text is 14px/20px. Toolbars move below titles below 1100px; row actions wrap below metadata. Settings-only controls retain at least 40px height, 8px corners, distinct AA control borders/focus tokens, 16px row padding and 24px section gaps. The application rail retains its 44px targets.

Successful empty regions have at least 160px height, a 32px decorative vector at the left and left-aligned title/help at the right, growing with text. Backup-table empties and singleton notices stay compact semantic rows. Loading uses exactly two static decorative skeleton rows where a list is expected. Successful-empty predicates remain with each controller; initial errors/loading, unsupported/denied states, retained refresh failures and scoped later/continuation empty pages stay distinct. No duplicate empty-state create action is added; pagination hides only on a successful final empty first page.

Forms share the category anchor and a 720px maximum, using two columns only at available form widths >=640px. Remove enclosing/nested form cards in favor of flat semantic groups and thin rules. Keep complete documents, every field/help/default/unknown value, mounted independent disclosures, invalid-field reveal/focus and Save/Cancel/original retry in ordinary flow. Subscription services use flat ChatGPT/Claude/Grok rows with negotiated service-only account creation and explicit unsupported lifecycle guidance. Managed Codex authentication follows the independent subscription-settings contract; authentication refresh never implies quota refresh. Appearance alone retains autosave, with native System/Light/Dark radios, decorative CSS miniatures and choices stacked below 640px available width. Git Profiles keeps its single New GitHub profile action in the category header even when empty; profile storage/identity/access distinctions remain visible.

Notifications renders saved Enabled/Disabled values as noninteractive label/value rows. Explicit Edit focuses the first checkbox; Save/Cancel return once to the enabled Edit action within the same active visit. A delayed refetch may postpone return, but deliberate focus transfer, another dialog/drawer, inactivity, window loss or departure discards that intent. Native status and server/client preferences remain independent, with visible Inbox/no-implicit-approval guidance and the full supplementary About notification delivery disclosure. Appearance/device controller and persistent Connection controls remain outside visit disposal. Backups retains semantic inventory, independently observed accepted jobs and manual history tabs, with short Refresh/Dismiss tracking text and full identity-specific accessible names. Diagnostics keeps its original 1100px/1200px viewport breakpoints, exact canonical BigInt values and independent caveats.

Use existing semantic light/dark/System tokens and system font; no external assets/fonts/dependencies, inline styles, gradients, transparency, blur or CSP exceptions. Preserve full wrapping identities/names/bytes/timestamps and keyboard/focus semantics. Presentation validation must cover all 17 synthetic empty/populated categories at 1920×1080, 1440×1000, 1440×900, 1280×820, 960×640, 640×480 and effective 200% CSS layouts. Fixture/browser/build/package checks remain distinct from actual browser chrome zoom, packaged CEF, macOS/Windows/X11, screen-reader, real-account and OS banner acceptance. Record revision/commands/results/limits in PRs/issues/CI artifacts, never repository evidence documents.

### Settings task dialogs

Settings keeps all 17 category lists and their owning controllers mounted when an operation opens. `settings-task.tsx`, `settings-task-context.ts` and `settings-task.css` add a Settings shell over the shared native `DialogSurface` in `ui.tsx`. Other dialogs retain their presentation. Appearance immediate choices, Import / Export, Connection & diagnostics, backup operation history and short Details disclosures stay in the category page. Search, page tokens, disclosures and scroll position survive opening and closing a task.

Use the closed size enum: 480px confirmations for configuration deletion, account disconnect/logout, device revocation and network-profile/backup deletion; 768px forms for Project, Provider, Model, account preferences and GitHub-profile create/edit, pricing, routing preview and notification edits; 960px workflows for Agent Workers, Instructions, repository editing/registration, account creation/connection/management, SSH setup, Runner Device details, network settings, pairing documents and backup inspection. Width never exceeds viewport minus 32px; height never exceeds viewport minus 48px. Use 16px outer corners, 20px titles, 16px section titles, 14px body and 12px hints/scope, existing theme tokens and 40px controls with 8px corners. Header and action footer remain fixed; only the body scrolls. Narrow forms stack and wrap full identifiers/actions. These task rules supersede the ordinary-flow action and page-form geometry above only while a task is open.

X, Escape and local Cancel dismiss presentation; backdrop clicks do not dismiss it. Before submission, dispose drafts and secret inputs. Pending or unconfirmed submissions instead hide the same mounted task controller, retain its original immutable request, receipt/job/operation identity and necessary transient authority within the current category, and expose a status plus View original operation in the list. Block replacement submissions; an uncertain write has only its existing exact original retry. Clear editable secret inputs on dismissal while preserving any credential bytes already owned by an authorized original request. Same-computer encrypted imports retain their exact ciphertext/digest for explicit retry; dismissal clears editable copies and cannot prepare a replacement protected recipient during uncertainty. SSH start keeps its original setup ID, blocks replacement host inspection and is never resubmitted after an ambiguous start. Dismissal never calls server/Worker cancellation or OAuth Cancel. Explicit business cancellation remains a separate operation. Confirmed saves use the existing completion and list refresh; accepted jobs retain their existing observation and Done flow. Late hidden results cannot reopen a dialog, navigate or take focus.

Internal workflow and confirmation steps share one native modal surface, keeping their parent controllers mounted rather than stacking dialogs. The dialog has no separate Settings lifetime. Category departure and Settings exit retain the disposal rules below, including original native/account ownership and detached work. The native modal makes the background inert and contains Tab/Shift+Tab. Creation focuses its first input, long details focus their title and destructive confirmations focus the least destructive Cancel/Keep action. Restore focus only to a connected visible opener; otherwise use that category's primary action/title. Do not overwrite a deliberate focus transfer or restore a departed category. Closing a compact category drawer precedes opening its task. Follow the [W3C modal Dialog pattern](https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/).

Validation covers open/close/save/failure/denial, unchanged list position, discarded drafts/secrets, pending close, exact uncertain retry, duplicate blocking, hidden late results and departure, X/Escape/Tab/Shift+Tab/return focus, drawer handoff, Strict Mode and same-server reconnect. The frontend jsdom suite uses at most four workers to preserve timer responsiveness during concurrent native builds; test and product deadlines remain independent and unchanged. Raise concurrency only after verifying those suites under peak shared-host load. Browser fixtures cover light/dark at 1440×900, 1280×820, 960×640, 640×480 and effective 200% layouts. Keep those checks separate from actual packaged CEF keyboard/zoom and account/platform acceptance. Prepare required generated clients and hydrated LFS assets, run `pnpm test` in `apps/delidev`, and remove generated `dist` directories after validation. No RPC, schema, migration or dependency changes are required.

### Settings screen and visit lifetime (issue #1236)

Settings selects the internal `Surface.Settings` destination and renders its category content inside `#main`. The bottom Settings rail item has `aria-current="page"`; all ordinary application navigation remains usable. Categories use the shared sidebar outlet and its 52px rail/288px context pane, contracting to 256px at widths up to and including 1100px. Below 760 CSS pixels, **Open settings categories** opens the existing shared navigation drawer. Settings has no outer dialog, modal header, Close action, Escape hint, backdrop, background inertness, focus trap or opener restoration. Escape does not leave this page; actual child dialogs and the compact drawer retain their own dismissal and focus behavior. No native window geometry changes.

The 17 independent category screens appear in this order: AI (AI Subscription, AI API Keys, API Providers, Agent Workers, Instructions), Coding (Projects, Repositories, Git Profiles, Git), Device management (Runner Devices, Paired devices), and System (Appearance, Server preferences, Connection & diagnostics, Notifications, Import / Export, Backups). All existing category IDs remain stable. Git Profiles renames the former Integrations presentation while retaining `integrations`; Git adds `git-workflow`. Each menu opens its own screen without a Git tab container. Git owns global Worktree fetch and PR remediation presentation; Server preferences retains account routing and the existing separately owned Network settings. Navigation appears once in the shared pane/drawer; there is no separate Settings sidebar or compact category select. Content uses the shared issue #1256 padding: 32px at >=1100px, 24px at 760–1099px and 24px vertical/16px horizontal below 760px. The shared pane and main scroll independently and reflow at 200% zoom without clipping controls or focus outlines. AI Subscription remains `subscription-accounts`, with the same label in navigation and heading.

Retained edit/account/delete/routing/Worker/device/pricing and child-workflow state does not lock category navigation after its task is hidden. An open native task temporarily makes the background inert. Selecting another category disposes the previous category and opens the target with fresh presentation state. Same-category reselection preserves its current state. Operation-local validation, revision, busy and exact-retry guards remain authoritative. Category selection exposes current state and visible focus; the active category is announced. A visit is uninterrupted time on Settings. Reselecting its active rail item preserves the current visit, selected category and current workflow. Category changes, responsive changes and same-identity reconnect retain that visit. A fresh ordinary visit starts at AI Subscription. Home **New project** and **Create a project** enter Projects creation with once-only Name focus; Pull requests **Repository settings** enters Repositories. Prerequisite, New session guidance and tray entries continue to enter Settings. Entry focus transfers after destination commit/drawer close to visible main or the compact opener, with explicit Name focus remaining authoritative.

Issue #1236 changes navigation and supersedes the modality from #1045 while retaining the #1138 disposal safeguards. Changing categories unmounts the category-owned tree and discards filters/cursors, details, editors, wizard steps, drafts, confirmations, secrets/disclosures, pending category entry, pending client waits and uncertain retry presentation. Leaving Settings also discards category selection. It never restores a former modal opener. There is no discard confirmation, recovery banner, implicit save, abandoned replay, server/native cancellation or rollback. The device-owned Appearance controller remains above connection state; its committed selection and pending native saves survive Settings presentation disposal and connection changes. Persisted settings, accepted server/native effects and receipts, authorization, selected conversation/composer, New session drafts and sibling workflows remain authoritative and intact.

Each selected category owns a fresh mounted component tree, mutation registry and authenticated transport wrapper. Supported RPC requests receive a linked category abort signal; disposal rejects late outcomes and blocks follow-up RPC/native work even when the underlying operation ignores cancellation. Native Worker and notification permission completions cannot update a replacement category. Keep the connection QueryClient and successful-save invalidation; cancel/remove only queries keyed by the disposed transport or category and remove its tagged mutation cache entries. Same-identity reconnects retain the current editor and route subsequent requests through the replacement authenticated transport. Strict Mode setup/cleanup replay creates a fresh un-aborted generation before mounting its readers. Fresh reads may reveal committed changes or pending server jobs; this is authoritative state, never restoration of an abandoned workflow. Issue #1137's native Connection panel is a separate persistent sibling, reached by this visit's Connection controls action; hiding that panel or leaving Settings retains its original lifecycle and registration requests and confirmations.

Menu selection lives outside the category lifetime. Only the selected category body mounts. Explicit New Project/Repositories entry and API Providers account management/creation use one transition path; non-secret typed entry information initializes the target once. OAuth begins only after the target category lifetime mounts. Category departure disposes local native callback/listener authority without business cancellation, automatic completion, save or replay. Returning reads current server state and never restores abandoned input, confirmations, filters or uncertain retries.

Every category has one visible category title and its existing scope/help description above its content. Configuration list toolbars keep Refresh settings and any eligible existing New action together at the upper right; detailed editors retain their existing explicit save/cancel/back actions. Devices and Runner Devices do not gain a generic create action, and Server preferences remains a revision-bound singleton. API Providers keeps the capability-gated Custom provider action and its successful final-first-page custom-provider empty region. Initial loading and initial read errors do not render successful emptiness or grant new create eligibility. A cached result remains visible during refresh; a refresh error shows its sanitized correlated failure and a stale-results notice. Pagination is hidden only for a successful empty first page with no continuation token; an empty later page still offers First page. Query staleness alone is not a read failure.

The former Models Settings category and independent model editor are removed.
The Agent Worker wizard owns model selection; Usage owns model details and Token
pricing. CLI/RPC model configuration remains compatible. Internal canonical
models retain their original IDs, discovery provenance, display settings and
historical attribution. Source-scoped server catalog queries supply autocomplete.

### Agent Worker wizard

Creation and editing use the same four steps: Harness, Accounts, Model, Configure.
Fill existing values on edit. Stage navigation never saves. Choose a supported
harness, then one subscription service or API provider and at least one account.
Multiple accounts must share that source. Keep their explicit order and relative
weights (1–1,000), all six routing policies and the inherited server default.
Fixed routing permits exactly one account. Source or harness changes clear
incompatible account/model choices and require explicit selection; ordinary Back
and disclosure changes retain values. Legacy accountless Workers remain readable
through existing APIs, but need an account before wizard resaving. Retired models
remain inert; affected Workers require explicit current account and model
reconfiguration without rewriting historical executions.

The Accounts choice list displays only accounts with connection metadata, saved
health `ready` or `unverified`, and no pending credential removal. Hide disconnected,
failed, expired, revoked and unknown-health choices from both the visual list and
keyboard/accessibility navigation. Account enablement and execution eligibility
remain independent. Validate the complete source page before applying this
wizard-only presentation rule; preserve the server page, cursor and all account
metadata without automatic continuation reads. Account management lists retain
their server-side filters before pagination.

Already selected accounts retain their IDs, order and weights when hidden,
including during edit and refresh. Routing options still exposes their names and
explicit removal; stage and save validation do not acquire a health gate. A
successfully loaded valid nonempty page with no visible choices displays **No
accounts to select on this page.** and **Connect an account in AI Subscription or
AI API Keys, then refresh.** Preserve First/Next/Refresh controls and the selected
count. Loading, failed reads and invalid source pages never display this notice;
genuinely empty pages retain their existing inventory guidance. This changes no
RPC, capability, migration, authentication or execution authority.

The Model step uses source-scoped server catalog autocomplete and permits exact
native ID input after loading, empty, failed or unsupported discovery results.
Typing never contacts a provider endpoint. Endpoint refresh uses the deliberate
existing discovery RPC and original account revision. Codex native observation
retains explicit selected-account/Runner Device/installation scope, accepted jobs,
immutable pages and original uncertain retries. Subscription native model lookup
remains unsupported. Connection and saved health observations remain separate
from execution eligibility; mount, navigation and save never start login,
connection, validation or inference. Saving declares configured harness/model
compatibility; execution still rechecks current authority and native support.

Configure retains name, permission, reasoning, instructions and native options,
including mounted collapsed values, and shows the selected source/model/accounts.
Submit only SaveAgentWorker under negotiated System capability 33; older servers
show update guidance. Go validates source membership and atomically reuses/creates
the internal model plus Worker under the original revision and UUID-v7 receipt.
Keep exact uncertain request bytes, current-revision conflicts, visit disposal,
late-response fencing and same-identity reconnect/Strict Mode behavior.

Use the shared 1040px left column, 720px wizard body, flat rows/dividers, semantic
themes, 40px controls and 8px corners. The Harness step replaces its select with
four equal selectable cards in Codex, Claude Code, OpenCode, Grok Build order.
Show their decorative 48px local brand marks, 16px names and 12px origin labels
OpenAI, Anthropic, Open source and xAI. Use 16px grid gaps, 24px card padding,
176px minimum card height and 8px corners. Two columns fit at available form
widths of at least 640px; below that width use one column. The selected card uses
the semantic selected background, accent border and a checked circular indicator;
unselected cards use the ordinary surface, border and empty circular indicator.
Keep the existing Harness heading/helper. The footer guidance is
"Choose a harness to continue to Accounts." and joins the group's accessible
description. Cards use a
button-based single-selection radiogroup with one selected Tab entry, wrapping
Arrow Up/Down/Left/Right selection, Home/End and native Space/Enter activation.
With an unsupported saved harness, select no card and use the first card as the
Tab entry. Preserve the new Codex default and saved edit selection. Click or
native Space/Enter confirmation immediately opens Accounts and focuses its
heading, including confirmation of the already selected card. Arrows and Home/End
change selection and focus without advancing. Initial display and edit loading
never advance. Harness has no Next button, and submitting its form does not
advance. Stage navigation never saves or discovers models. Reselecting the
current harness preserves account/model choices, while an actual change retains
the existing explicit reset only after the bounded draft is accepted. Buttons
retain visible keyboard focus and active/server-support/mutation guards. Local SVG masks use semantic ink in
both themes without network access, inline styles or additional dependencies.
Input/select focus preserves ordinary
boundaries; buttons/disclosures retain visible focus. The combobox supports
Arrow Up/Down, Enter and Escape with active-descendant semantics. Stage changes
focus their heading; invalid configuration returns to its stage and input. Narrow
windows use one column and compact stage labels. Footer Cancel remains in all
steps; Back/Next from Accounts onward and the final Save remain in normal
document flow. Verify 1280×800, 960×640, 640×480,
effective zoom and light/dark browser fixtures separately from actual browser
zoom, packaged native platforms and real-account acceptance. Usage model details
provide current revision-bound Token pricing, including models without usage;
retired historical model attribution remains read-only.

Agent Workers uses the shared left-aligned `width: 100%`, `max-width: 1040px` column inside the shared 32px/24px/16px content padding. Its single 26px title is followed by the 14px summary **Reusable configurations for your agents.** and 12px scope **Saved on the selected server.** Keep Refresh settings and the single eligible **+ New Agent Worker** action together at the upper right in list mode, hiding that toolbar during existing parent workflows. Below 1100 CSS pixels, the Agent toolbar moves below the title; every category uses this shared toolbar breakpoint. A subtle divider ends the heading, with content 24px below it. Controls retain 40px minimum height, 8px corners and visible focus.

After a successful empty first page without a continuation token, Agent Workers shows one semantic-themed region with thin neutral rules and 160px minimum height. Place a decorative, accessibility-hidden 32px outline icon at the left, with the 16px heading **No agent workers yet**, and 14px copy **Define a harness, model, accounts, and instructions, then reuse them in new sessions.** No second creation action appears in this panel. Loading, initial permission/read errors and failed cached-empty refreshes never satisfy that success-only predicate. Empty continuation or later pages say **No agent workers on this page.** and retain the original First/Next controls, kind, page size and opaque server tokens.

Loaded Agent configurations occupy one flat list with divided rows. Preserve exact inert names and full IDs, with optional existing Harness/Status text only; no health or readiness is inferred. Unsupported schemas expose only inert name/alias text within the existing 256-byte UTF-8 Agent name limit and full identity; oversized projected names retain the Unnamed fallback, and all existing unsupported-schema action gates remain enforced. Check code-unit length before allocating a UTF-8 validation buffer. Visible actions appear in Edit, Preview routing, Delete order; each accessible action name includes the configuration name. Rows, long text and controls wrap without clipping. Use the creation/edit wizard above, existing read-only routing and explicit revision-bound deletion confirmation, ordered account/template/options validation and exact uncertain requests. Reflow and same-identity reconnect retain these only within the same visit. The common #1138 disposal/default-category policy above remains authoritative for navigation away, including late outcomes and already accepted server effects.

Git Profiles uses the approved issue #1147 content hierarchy within this shell. Its description is “Manage GitHub profiles for repository access. AI accounts are configured separately.” One left-aligned GitHub panel, at most 1040 CSS pixels wide, contains the provider heading, Refresh, bounded profile rows and exactly one New GitHub profile action. Keep that action in the category header; a successful empty first page without a continuation or read failure shows first-profile guidance with three static informational steps and no second action. Loading, initial failure, cached refresh/failure and empty continuation/later pages remain distinct; create keeps its original eligibility before read success and paging keeps its original opaque cursor and 50-resource size.

Rows and Manage display Token storage and Identity validation separately, including pending denial, unsupported versions, unknown observations and original check time/identity when provided. Neither identity verification nor token storage claims repository capability. Create/rename forms are bounded to720 CSS pixels, focus the name only on first visible entry, and keep token type/owner immutable after creation. Manage separates explicit token connection, identity validation and confirmed deletion. Its empty password field disables submission and never reveals stored tokens. Full official-form guidance remains mounted inside a native disclosure; toggling alone cannot query or open a form. New unconnected profiles explain token creation by default; connected profiles initially collapse it. Existing exact uncertain identities, decimal revisions, PAT clearing and operation guards and the common Settings visit lifecycle remain authoritative. Use scoped CSP-compatible CSS, decorative vectors, wrapping metadata/actions and stacked steps; preserve shell spacing, category selection and actual child-dialog/drawer focus and dismissal behavior. See the [integration contract](cmds-delidev-integrations-contract.md) for authority and operation ordering.

Settings-specific colors and system fonts are scoped to this Settings screen and remain CSP-compatible: white content, pale-gray navigation, 8px control radii, flat empty regions, 40px minimum controls, decorative outline icons and non-color selected-state semantics. Do not add a route, native window, dependency, external asset, inline-style exception, public API, persisted schema, storage, authorization, credential, polling or migration change. Component tests do not establish native geometry or keyboard containment; record native desktop smoke evidence separately, including platform and viewport, and report unsupported/unavailable platforms without claiming acceptance.


#### Device appearance (issue #1238)

Appearance is the first category in the System group, with the scope description
“Saved on this computer.” Existing category IDs/order, the ordinary AI Subscription
entry and targeted New Project/Repositories entries remain unchanged. The desktop
shared context pane and compact navigation drawer expose the same category. Theme is a native
radio group with System, Light and Dark; System is the fresh-install default.
Selection saves automatically, with pending and visible failure states and no Save
button. System observes `prefers-color-scheme` changes while running; explicit Light
and Dark ignore later OS changes.

`appearance.tsx` mounts above local/saved connection application state. Appearance
changes update static root selectors without remounting sessions, composer drafts,
Settings openings or other workflows. Leaving Settings disposes its visit as
before while the device controller and already submitted appearance save remain
alive. This preference is shared by every live local/saved-server window and loaded
by newly opened windows, independent of selected server or connectivity.

Native owns bounded, strictly validated `appearance.json` in `app_config_dir()`,
using `{ "version": 1, "theme": "system" | "light" | "dark" }`. It contains no
server identifier, secret or user content and stays outside server configuration,
pairing, backups and configuration transfer. The narrow `read_appearance` and
`update_appearance` commands authorize registered trusted local (`main`/`local-*`) or saved (`server-*`)
document using the existing URL/binding checks; no generic path/filesystem or
Connect/protobuf capability is introduced. URL checks and disk I/O stay off CEF's
UI loop. One native process store serializes reads/atomic writes; update checks
the expected process revision and reinspects the stored document before replacement.
Writes synchronize a same-directory temporary file before atomic replacement, then
synchronize the Unix parent directory or use Windows write-through replacement.
Committed changes emit `appearance-changed` only to authorized live webviews.
Snapshots have non-wrapping unsigned process revisions; delayed reads, replies and
events cannot restore older state. Equal unchanged inspections retain the revision.
Brief saved-window binding contention receives bounded event-admission retries.
A healthy retained window reinspects when focused or made visible to reconcile a
missed event; an error or uncertain save still requires explicit Reload appearance.

Read/write failures are visible with stable typed messages, without raw OS errors
or paths. Failed writes retain the prior committed selection. An uncertain IPC or
post-publication synchronization outcome disables another write until a deliberate
fresh inspection; it never blindly repeats the mutation. Invalid, oversized, linked,
unknown-field or unsupported-version files remain untouched, with System fallback
and visible recovery guidance rather than silent replacement. Logging records only
the operation and stable outcome/problem, never file bytes or configuration paths.

`themes.css` owns shared semantic light/dark colors for initial connection/loading/
error presentation, the shell, conversations, forms, dialogs and every Settings
category, including the scoped Projects, Agent, Activity, schedule, Diagnostics and
Import / Export treatments. A static OS media-query fallback covers initial paint
before IPC. The fixed light color descriptions in presentation sections describe their light
baseline; issue #1238 extends them through semantic dark equivalents. Preserve all
layout/content/state semantics, selected/disabled/hover/focus states, strict CSP,
native geometry and decorations. No transparency, blur, custom theme, font/accent
customization, feature flag, database migration or server-synchronized preference
is introduced. Record component/build and temporary storage evidence separately
from actual supported-platform CEF OS/theme/restart/multiwindow, keyboard containment
and 200% zoom acceptance in PRs/issues/CI, retaining unavailable targets explicitly.
#### Runner Devices flat list presentation
Issue #1244 scopes the approved proposal B to Runner Devices list mode (`execution-workers`). Within shared content padding, left-align one fluid column capped at 1040 CSS pixels. Order the category title/scope/Refresh settings, local Worker, Saved runner devices inventory and existing pages. Use flat white sections with thin `#D8DEE8` dividers, 16px adjacent gaps and 24px section gaps. Keep the system font, 26px semibold category title, 16px section headings, 14px body, 12px full UUID, `#202632` text, `#5B6577` secondary text, `#2563D8` primary/focus and 40px minimum controls with 8px corners and AA control boundaries. At >=1100px retain 32px padding and title-aligned Refresh; below 1100px retain 24px padding and stack header/row actions below metadata. Below 760px use the shared navigation drawer and retain existing vertical padding with 16px horizontal padding. Long names, help, UUIDs, buttons and focus indicators must reflow at 960×640 and 200% zoom. Machine detail uses the shared child-workflow body rules; native geometry remains unchanged; issue #1236 owns the shared application shell and category navigation.

Only the existing mounted Settings Worker-controls instance selects internal `LocalWorkerPresentation.RunnerDevices`; `Default` preserves the saved-connections consumer. Styling, reflow and same-category reselection cannot remount the controller; category departure disposes its local presentation without undoing accepted native effects. Show **This computer's Worker** and the complete explanation: **Registration is separate from startup. The Worker continues after you quit DeliDev. Harnesses must already be installed.** Map the existing lifecycle states to **Not started**, **Starting**, **Controller running**, **Stopping**, **Exited** and **Exit unconfirmed** respectively. Keep every detailed description and the full **Execution machine:** UUID. Unreadable inspection grants no inferred lifecycle state. Uncertain's complete description occurs once in a plain white inline notice with decorative warning icon and `#805400` foreground; the non-live text badge cannot duplicate status announcements. No enclosing warning panel or inferred termination, cleanup, server connectivity or harness readiness is added.

Start local Worker is blue primary, Stop local Worker is neutral outlined with dark red text, and Refresh is neutral outlined. Preserve all existing registration/refresh/start/stop visibility and disabled predicates, confirmation/cancellation copy, superseded-generation warning, retained-stop retry/acknowledgment and original retry generation. Preserve the three-second active polling, gating, late-result protection and registration/start separation. Without native local control, keep **Configure these entries through the DeliDev CLI.**

Under **Saved runner devices**, semantic divided articles render directly on white. Preserve server order, original name/fallback, optional health/harness, full IDs, **Inspect installed harnesses**, unsupported-schema disabled actions and the existing MACHINE document parser fallback. Reuse only the authenticated 50-record MACHINE query and opaque token. Initial loading without data shows **Loading runner devices...** and exactly two static decorative skeleton rows excluded from the accessibility tree. Initial read/permission failure uses the existing sanitized Problem message, guidance and correlation reference without success-empty copy. Successful final first-page emptiness says **No saved entries.**; continuation/later empty pages identify page scope and retain paging. Failed refresh retains the previous rows or empty observation with **Refresh failed. Showing the last successfully loaded results.** Hide pages only for successful empty first-page results without continuation; all existing token/fetching/disabled rules remain.

Actual child dialogs and the shared compact drawer preserve their own contained focus, background inertness, Escape/cancel and opener restoration; page-level Escape preserves Settings. Polling cannot steal focus or redundantly announce unchanged state. All controls retain descriptive text and visible focus, all identities remain accessible, and icons are decorative. Reflow and same-identity reconnect retain only the current Settings visit; navigation away disposes it and rejects late native/RPC results without replaying accepted operations or canceling authoritative effects. This ordinary frontend rollout adds no API, schema, storage, migration, dependency, flag, telemetry, logging, query, polling, authorization or native-window change. Preview images remain untracked. Record revision, commands, results and unresolved browser/native limits in the PR, issue or CI artifacts under the current repository validation policy.

#### Projects grouped presentation
Issue #1256 replaces the Projects-only issue #1157 presentation exception with the shared white/semantic-theme surface, left-aligned 1040px category column, 26px title, 14px body, 40px controls and 8px radii. Refresh settings and the single + New Project remain in the shared header, stacking below 1100px. The exact scope, read states, server order, IDs, grouped form fields and behavior below remain authoritative. Decorative vectors are accessibility-hidden, styles remain static CSP-compatible and focus/control boundaries retain AA contrast.

Only a successful empty first page with no continuation token and no read failure shows the shared 160px-minimum empty region, 24px after the heading. Place a 32px decorative folder at the left of the 16px semibold “No projects yet” title. Its left-aligned secondary copy is exactly “Group repositories and choose which Agent Workers and AI accounts a project can use.” followed by “Choose New Project to get started.” There is no second create action. Initial loading/failure never claims emptiness. Successful cached results remain during refresh; failed refreshes retain rows with their sanitized error, guidance, correlation and “Refresh failed. Showing the last successfully loaded results.” Failed cached emptiness does not show the definitive empty panel. Empty later/continuation pages show “No projects on this page.” and retain First page/Next page. Hide pagination only for successful final first-page emptiness.

Loaded Projects use one white divided panel, preserve server order and tokens, show complete wrapping names/IDs and retain project-specific accessible Edit/Delete actions. No sorting, row reads or automatic page traversal is added. Unsupported schemas disable Edit/Delete. New Project remains available during list loading/read failure; server authorization remains authoritative. Existing resource-read failures and revision mismatch block a new edit Save without discarding the draft.

Create/edit forms retain every existing field and explanation in four flat semantic groups: Name, Repositories, Agent Workers and AI accounts. Repositories retain ordered selectors, Up/Remove controls and explicit required Primary repository. Removing the primary clears it without replacement. Each restriction retains configured-empty “permits none” semantics; turning it off clears selected IDs and permits otherwise eligible entries. Keep full serialized documents, byte limits, captured revisions, busy/uncertain locks and Save/Cancel/original request retries beneath the form. Deletion uses the same scoped flat section while retaining confirmation, session/history consequences, future schedule effects and the original revision/retry. No autosave or replay is added.

Issues #1138/#1236 own the visit lifetime: navigation away discards Projects drafts, editors, confirmations, cursors, waits and uncertain presentation. Ordinary reopening starts at AI Subscription; targeted New Project/Repositories entry, protected/deferred navigation, same-identity reconnect and successful-save invalidation remain unchanged within a visit. Preserve sibling session/composer/query state and late-result/cache-disposal guards. Native decorations, maximization, 1280×820 restored and 960×640 minimum sizes remain unchanged. Validate empty/list/create/edit/delete at 1440×900, 1280×820, 960×640 and 200% zoom, preserving the below-760px shared navigation drawer, scrolling to all actions and full identities. Child-dialog and shared-drawer background inertness, keyboard containment, Escape and opener restoration require separate actual desktop evidence; browser and component checks alone cannot establish them.

Account metadata editing is separate from credential connection, validation, catalog refresh and disconnection. API-key input is password-masked, limited to the server's printable ASCII contract, and cleared on submission or hiding the form. Its wire bytes can remain only in the bounded visit-scoped exact pending request after uncertainty; completed React Query mutation variables are released. Keys never enter read keys, logs or browser persistence. Keyless connection requires the selected provider's explicit keyless configuration. Service-native subscription configuration uses its independent managed login capability and never inherits system credentials.

The 35 canonical Provider presets retain one server/CLI/desktop order with 32
hosted defaults and three explicit local defaults. Account key guidance exposes
explicit official-key and documentation actions on the native desktop. Rust
accepts only a canonical preset/action and uses the Go-generated compiled public
HTTPS allowlist; server-supplied URLs cannot expand OS opener authority. Regional
key separation, ordinary API versus subscription/coding-plan guidance, Hugging
Face inference permissions and Baseten personal keys remain explicit. Opening
help never validates a key, creates an account or sends inference; unconfirmed
OS opening retains a sanitized status without automatic replay. See the
[provider contract](cmds-delidev-providers-contract.md).

The build app manifest declares `open_provider_guidance`. Its dedicated
`provider-guidance` permission allows only that command and is granted through
the trusted `main` and `server-*` webview capabilities, never through their
containing windows or remote origins. Regression tests resolve the generated
build manifests and capabilities with Tauri's authority resolver across the
desktop targets, checking trusted access and external-child/remote denial.
These permission checks do not establish OS browser dispatch or page access;
actual native browser opening requires separate platform verification.
After preparing the native assets and sidecar, run these permission tests with
`cargo test -p delidev-desktop --features desktop-host,custom-protocol --lib permission_tests`
from the repository root; the default library feature set excludes native ACL
generation.

Account settings separate **AI Subscription** and **AI API Keys** presentation sections while continuing to query the existing Account resource. Each section sends the server-side account-type selector; provider-row links add the exact provider ID. Filtering must happen before pagination, and cursors must remain scoped to the exact account type and provider. The desktop requires the provider activation, active-model filter, account-provider filter and explicit account-type-filter capability markers before enabling split lists or the guided API account flow. It must never fall back to a mixed list, client-page filtering or an inferred all-enabled provider inventory. The API provider inventory’s Add AI API key action opens the entry form directly with that exact enabled saved provider entry, consuming each deliberate event key once. The wizard rechecks the fresh provider identity, authentication, protocol, endpoint and enabled state before saving account metadata and before an explicit credential connection; it never validates or discovers models automatically. API connection, validation, health, quota, exhaustion and credential cleanup remain distinct states. Category navigation stays locked during account creation, settings and connection workflows; application navigation remains available, with exact uncertain requests retained for deliberate retry. Independent server capability 30 enables ChatGPT browser login followed by editable account naming under the subscription Settings contract; Claude Code and Grok remain unsupported. The collapsed Advanced settings disclosure owns search, provider filters, disconnected metadata creation and the subscription-provider section, which permits only native-subscription protocol, subscription authentication and an empty endpoint. Issue #1143 defines the compact subscription list and planned provider cards in [the subscription Settings contract](apps-delidev-subscription-settings-contract.md); native lifecycle controls require their independent capability.

Issue #1145 replaces the creation picker’s search, radios, selected markers, numbered steps and Continue with native provider action buttons. Choose an API provider / Select a provider to connect your entry. precedes one server-ordered page of enabled saved API entries. The picker requests `query: ""`, `enabledOnly: true`, `pageSize: 50` and its own cursor, independent of AI API Keys’ unsearched account-provider inventory and API Providers’ search. Both account and picker inventories must retain all four capability gates; no generic resource page or credential inference can supply eligibility. Direct-entry details remain disabled while either inventory lacks the gates, and become unavailable again if those capabilities are lost. The clicked UUID and complete summary/Resource are retained as one bounded hint even when the account-filter page does not contain that provider. The clicked snapshot remains authoritative across unrelated inventory refreshes; the existing fresh provider checks validate or reject its contract before writes.

Issue #1237 supersedes the prior API creation-card geometry. API list and child workflows use one left-aligned fluid column at most1040 CSS px wide, provider choices at most 720px and entry/preferences forms at most 720px. Remove the outer creation card. Preserve the shared 32px content padding, 24px below 1100px and existing narrow-screen padding. The picker uses two equal columns at an available form width of at least 640px and one below, with 10px gaps. Neutral native `button type="button"` actions have 64px minimum height, 12px padding and 8px corners, exact provider names, muted API key or Local endpoint methods and decorative chevrons. No selected/pressed semantics, invented logos or inferred OAuth availability are used. System fonts, white content, primary `#202632`, secondary `#5b6577`, accent/focus `#2563d8` and panel border `#d8dee8` remain scoped to API Account documents/workflows. Use a 26px semibold main title, 16px step headings, 14px body and 12px metadata, 40px minimum controls, 8px control corners and flat groups with AA contrast. Other Settings categories share these body defaults; Settings-external callers retain their appearance.

A deliberate click, Enter or Space opens the existing entry form immediately and focuses its step heading. Back/Change retains the picker page and restores the original provider-button focus. Next page exists only with a continuation token; First page exists on later pages, including a successful empty later page. Reads remain bounded without whole-inventory accumulation. Retry preserves the exact query/cursor. Loading, typed permission denial, read failure/Retry and stale cached results remain explicit. Open API Providers is offered only for a successful empty first page without continuation; empty pages with continuation keep Next and cannot imply globally empty inventory. Restoration, rerender, Strict Mode replay and provider navigation cannot create/connect/validate/discover or start authentication. Leaving follows the visit-disposal policy above.

Entry-name/key validation, password masking and transient key clearing, collapsed preferences (`enabled=true`, `exclude_automatic=false`, `recovery_notifications=true`), explicit keyless connection and server-relative localhost guidance remain unchanged. Fresh provider checks precede metadata save and connection; one save is followed by one deliberate connection, with saved-account retention on connection failure, unverified health, separate validation/discovery, original request/revision retries, navigation locks and late-continuation guards. Record browser/component fixtures separately from native/provider acceptance in issue #1145, its pull requests and CI runs.


Issue #1237 simplifies AI API Keys for a few saved entries. List mode owns one **AI API Keys** title, one top-right **Add AI API key** action, the description **Manage AI API keys and keyless local connections. Connection and health are separate states.**, the scope **Saved on the selected server.** and a subtle divider. Creation retains the category H1 **AI API Keys** and its scope, with **Back to AI API Keys** and subordinate **Add AI API key** workflow title. Saved results, Manage connection, Edit preferences and Delete entry use one clear heading, full inert entry identity, the scope line and existing Back/Cancel actions.

Remove API Search providers, Filter entries by provider and More provider filters entirely, with no hidden filter equivalent, totals, sorting or bulk operations. Its account-provider inventory always sends `query: ""`, `enabledOnly: false`, `pageSize: 50` and an API-owned bounded cursor. Subscription retains its own visit-local search/provider/cursor, and API Providers retains its own search. Provider-origin Manage stores only the exact provider-ID scope and bounded hint, never a display-name search. Display that provider’s exact label or complete inert ID with **Clear provider filter**, which clears the scope/hint and returns account pagination to its first page. Preserve server-side API/provider filtering before pagination and cursor binding; never accumulate the whole inventory or filter a loaded page.

Saved entries retain server order in flat divided semantic rows with the shared 16px padding. The approved usage body adds **Your API keys**, **DeliDev usage · Last 30 days** and **Refresh usage**. Each row presents the full wrapping alias/provider identity, separate connection and health states, historical **Estimated cost**, **Observed tokens** with observed-response counts, and saved **Quota** observations. Confirmed exhaustion remains authoritative; missing observations are **Not reported**, never measured zero. Costs remain separate by currency and native accounting unit; unavailable native totals are never computed from categories. A successful empty usage interval shows **$0** estimated cost and **0** observed tokens, both with **No usage in this period**. Empty presentation requires returned native accounting support, present response totals, zero response and all returned accounting-unit counts, present native summary totals, and no accepted executions or native context actions without response usage. Loading, failed reads, absent totals, unreported usage or prices, and older accounting profiles cannot become zero. Refresh failures retain the last successful values with the existing stale notice. This empty display is independent of connection/health and quota and does not establish actual charges or alter ledger/budget evidence. Initially collapsed **Details** retains Entry Enabled/Disabled, Provider Enabled/Off/Unavailable, full Account identity, original observation/range and incomplete evidence. **Manage connection** stays visible; the accessible More actions disclosure retains **Edit preferences** and **Delete entry**, their existing confirmation/retry authority, and Escape/opener focus restoration. **View usage** deliberately opens the original account and exact returned half-open range in the existing Usage page. Unknown schemas retain inert full identity and disabled business/navigation actions; read-only Details grants no authority. No stored-key masking/readback/reveal/copy is added.

The list uses generated account-scoped GetUsageSummary reads only for its current bounded page, with the server-default last 30 days and independent native accounting profile. It never traverses inventory, sums groups in React, queries provider billing or polls usage. Refresh usage refetches the current Account page and active scoped summary reads without connection, validation, discovery or native quota operations. Usage read failure is independent of entry management; retained values are marked stale, and deliberate retry reads only that exact account. The shared Settings opening owns summary caches and cancellation. View usage carries an in-memory unique event key and original account/range; it applies once and subsequent navigation/reconnect cannot overwrite Usage filter drafts. These reads grant no credential or execution capability. Below the list retain **Known usage may be incomplete. Estimates are not billed amounts.** and the existing secure-storage note. Example values and preview notices are fixture-only.

At 900px of available body width, usage metrics move beneath identity/actions; below 640px the metrics and actions stack. The existing 1040px cap, typography, semantic light/dark tokens, 40px controls, 8px corners and below-760px shared category drawer remain authoritative. Reflow, refresh and disclosures retain focus and the existing Settings disposal policy.

Only a successful unscoped final empty first page shows the approximately 260px-high `#fafbfc` neutral panel with a decorative key, **No AI API key entries**, **Add an entry for an enabled API provider.** and **Keyless local providers do not require a key.** It has no second Add action. A provider-scoped final empty first page says **No entries for this provider.** Empty continuation or later pages say **No entries on this page.** Show First page only on later pages and Next page only with an opaque continuation; omit navigation when neither exists and retain fetching guards. Below results keep **Credentials are stored securely on the selected server.**

Initial capability loading, permission denial, missing capability markers and sanitized read failures cannot claim successful emptiness. Failed refresh retains cached rows and the stale notice; cached empty failure suppresses definitive current-empty copy. Each failed inventory/account read has a deliberate Retry disabled while fetching that refetches only its current request/query/cursor, never writes, traverses pages or grants eligibility. All four account and picker capability markers remain required, including capability loss after direct provider entry.

The Provider → Details flow remains immediate. Keep **Only enabled API providers appear here.** and arbitrary supported saved providers in server order; preview names are examples. Deliberate provider activation focuses the step heading; Change restores the exact provider-button focus on its retained page. Details uses **Connect your entry**, a slim provider/method summary and **Change**, Entry name, a transient password-masked API key only when required, collapsed **Where to get an API key** with every existing guidance/documentation string, and collapsed **Advanced preferences** with all existing fields/defaults. Preserve **Use a separate entry for each API key.**, **Stored securely on the selected server.** and **New connections remain unverified until you explicitly validate the connection.** Keep one **Add and connect** submit and the top list-return action; remove the duplicate footer Back to provider. Explicitly keyless providers omit key input/key-creation guidance, retain server-relative localhost/local-endpoint explanation and never ask for key reentry after local failure.

Name/key byte limits, fresh provider checks before save/connection, one metadata save followed by one intended connection, retained saved entries after connection failure, distinct connection/health/validation outcomes, exact original request/revision retries, busy/uncertain locks, secret clearing and late-continuation guards remain authoritative. Management retains active-only five-second status reads and explicit Connect, Validate connection, Refresh models and Disconnect eligibility, separate validation/catalog observations with original times and sanitized problems, exact original cleanup retry and all disconnection consequences. Preferences retain immutable provider/type, complete fields/observations, captured revisions and guarded Save/Cancel/retry; they cannot mark an entry ready. Deletion retains explicit confirmation, disconnection/cleanup and relationship checks, sessions/history and original uncertain requests. No automatic validation, discovery, secret persistence or replay is introduced.

Use static CSP-compatible CSS, semantic headings/articles/navigation, explicit labels, visible focus, text alongside status colors and decorative aria-hidden vectors. Keep the existing below-760px shared category drawer and independent shell scrolling. Child dialogs and the shared drawer retain their own inertness, Tab containment, Escape and opener restoration; Settings has no outer modal or Close action. Long UTF-8 aliases, provider names and full IDs wrap at 200% zoom; actions and forms stack without clipping. Fetching/reflow cannot steal focus. The common Settings-visit disposal policy continues to own drafts, cursors/scope, confirmations, secrets, waits/retry presentation, ordinary Subscription reopening, targeted entry and same-identity reconnect; preserve sibling caches and Strict Mode guards. Record revision, commands, actual results and native geometry/keyboard/zoom limits in issue #1237, its PR and CI artifacts under the current repository validation policy. Component/browser/build/package checks cannot establish native/provider acceptance. This ships in the ordinary frontend build without backend/native/API/schema/migration, dependency/asset, logging, credential-storage, flag or deployment changes.


API entry creation is a content-level Provider → Details flow inside Settings. Only the provider inventory's enabled API entries may be selected. The Details step keeps alias, a masked transient key only when required, and collapsed existing account preferences; keyless entries require an explicit connect action and retain server-relative localhost meaning. Creation is followed by a fresh provider read immediately before one explicit connection attempt, with the created account retained after connection failure. Manage/Done navigation stays disabled while that provider check is in flight. Exact uncertain create/connect requests use their original identities and revisions, visible keys clear at submission, and hiding or changing provider revokes late automatic connection continuations. Validation and discovery remain separate explicit actions and connection does not prove readiness.

The independent issue #1146 OAuth capability and managed OpenRouter connection method select the deliberate browser flow defined in [the OAuth contract](cmds-delidev-account-oauth-contract.md). Its current Settings visit owns `account-oauth.tsx`; native callback/opener authority retains the original trusted window, selected server, opening and native epoch. Complete uses a direct authenticated write-only generated client, keeping codes outside query/mutation caches. Cancel/back/manual fallback requires the original confirmed cancellation and credential cleanup, while leaving Settings only disposes local listener/forwarding authority. Exchange/save/cancellation/recovery progress remains distinct; exact code-free original recovery never repeats HTTP. Success offers the existing Edit account/Manage/Done paths with unverified health. Older servers or unavailable native callback infrastructure retain manual creation without an OAuth claim. The approved 760px card uses current semantic theme tokens and static CSP-compatible styling; actual browser/account and packaged platform acceptance remains independently required.

The `api-accounts` category presents **AI API Keys** in the category navigation and page title, with the description “Manage AI API keys and keyless local connections. Connection and health are separate states.” API-authored list, wizard, preference, loading, stale/error and accessible copy uses **entry**: Add AI API key, Back to AI API Keys, Manage AI API Keys, Entry name, Choose an API provider and Connect your entry. Issue #1145 supersedes the prior Continue to details control and numbered step marker with direct provider actions. Its empty state is “No AI API key entries. Add an entry for an enabled API provider. Keyless local providers do not require a key.” The masked secret remains API key; keyed and keyless actions are Connect API key and Connect local endpoint, followed by separate Validate connection, Disconnect and Manage connection actions. Keyless failures never ask for a key. Shared editor, field, connection and deletion components apply entry terminology only to Account documents with `type: "api"`; preserve subscription/mixed account terminology, aliases, server diagnostics and all stored/RPC/CLI identities. This presentation ships in the ordinary desktop frontend build with no migration, flag, new logging or backend/native change. Record verification in issue #1135, its pull requests and CI runs.

Connection starts unverified. Validation and model discovery show their independent server observations; only explicit actions invoke them. Disconnection describes cancellation and credential removal, and presents accepted state separately from pending credential/Worker cleanup. A retained removal marker retries its original request ID and expected revision, never the newer account revision. Unsupported or unsafe revision encoding disables that retry instead of guessing.

### Server preferences and Git
Server preferences edits singleton server default account routing; Git edits the global automatic-fetch gate and complete PR remediation policy. The presentation-only ServerPreferenceSection enum provides All, AccountRouting and GitWorkflow. Both scoped screens use the same SETTINGS resource and shared configuration RPC, seed the full defaults on creation and preserve every hidden field when saving. Network settings remains in Server preferences with its independent Network RPC authority. New settings seed the documented Go defaults, verified against `settings defaults` in real integration tests. Once the singleton exists, the UI exposes only revision-bound editing, preserves all unrelated notification values and retained remediation selections and does not offer duplicate creation or generic deletion. Existing execution snapshots retain their original routing; Agent configurations with an explicit policy still override inheritance. Worktree fetch requires both global and repository preferences, and Local checkouts stay unchanged. Native notification delivery and remediation behavior remain separate required features.

The server-policy bodies retain issue #1242's presentation rules within the shared Settings shell. Use the shared left-aligned 1040px column and semantic surface/padding, shell/category order and visit lifetime. Server preferences uses title “Server preferences” and description “Default account routing.” Git uses breadcrumb “Coding / Git”, title “Git” and description “Worktree fetch and pull request remediation.” Both use scope “Saved on the selected server.” Use system fonts with 26px semibold title, 16px subordinate workflow/section headings, 14px body and 12px scope/identity; #202632 text, #5b6577 secondary text, #2563d8 primary/focus, #d8dee8 decorative borders and #7b8698 control boundaries. Retain the 3px focus outline, 40px minimum control targets and 8px button radii. Server preferences keeps only header Refresh settings and opens its admitted singleton form directly; the form uses Discard changes and Save changes. Git uses visible Refresh with accessible name Refresh Git workflow and the single New/Edit Git workflow action; below 1100px stack the toolbar and fields. Full IDs and controls wrap at compact widths and 200% effective zoom. All Settings bodies share these defaults; native geometry remains unchanged.

A successful final empty first page admits the mounted Server preferences form with Go defaults; it never authorizes a creation button or write. Git alone uses the successful-empty gate for New, disabled during fetching. Loading, initial errors and empty later/continuation pages cannot admit either editor. The 24px-padded, 1px-bordered, 10px-radius Git empty panel has no duplicate CTA. Its exact shared text is “Review the defaults, then save one preference set for this server.”; “Account routing” / “Choose the default policy for Agent Workers that inherit server routing.”; “Worktree fetch” / “Allow fetching before Worktree preparation. Repository preferences also apply.”; “Pull request remediation” / “Configure bounded automatic fixes for linked pull requests. All automatic policies default off.”; and the Git footer “Choose New Git workflow to review and save.” Account routing copy appears only in Server preferences; Worktree fetch and PR remediation copy appears only in Git. The shared All presentation retains all three sections. Retain the last successful rows or final-empty panel during refresh and after failure, alongside the sanitized correlated error and existing stale-results notice; failed refresh disables New. Refresh repeats only the current read/page. Keep bounded First/Next controls for Git when a continuation or later page requires them; Server preferences admits only a complete final first-page singleton or defaults. A supported saved document displays its exact routing enum in Server preferences, fetch Allowed/Disabled and each remediation switch On/Off in Git, with “Repository preferences also apply.” under fetch; both retain the complete singleton ID, and Git retains the bounded-automation prerequisite notice. Unsupported schemas, unreadable documents and unknown/missing policy values retain full identity with disabled editing and explicit unavailable information, never fabricated defaults. There is no New/Delete for a saved singleton.

Keep one mounted ConfigurationEditor and its existing query/mutation owners. Hide the list toolbar while editing. Show Account routing only in the Server preferences editor and Worktree preparation plus Pull request remediation only in the Git editor, without removing any field/help. The generic All editor retains all three sections. Each editor starts from the complete original document and uses its exact singleton ID/revision; never reconstruct hidden fields from defaults on an existing document. The six exact routing values remain fixed, priority, round-robin, remaining-quota, reset-window and sequential-exhaustion; label/help sit left of a 280px native select at wide widths, stacked full-width below 1100px. New documents retain exactly default_routing=sequential-exhaustion, notifications=true, automatic_fetch=true and remediation={ci_failure:false, review_feedback:false, merge_conflict:false, conflict_strategy:merge, session_strategy:reuse, attempt_limit:3}. In the Git and All editors, always show “Enabled policies run bounded fixes for linked pull requests when the Agent, Runner Device, and current evidence are eligible.” and native checkboxes “Automatically fix required CI failures”, “Automatically handle matching published feedback”, “Automatically resolve verified merge conflicts”. Shared remediation detail presentation defaults to enum Expanded; the Git editor and generic All settings editor select Collapsible. Its native “Remediation details” disclosure starts closed per editor, with static helper “Session strategy, execution targets, conflicts, attempt limit, and reviewers.” Children stay mounted: session strategy/help, exact Agent Worker/Runner Device choices and unavailable states, conflict strategy/conditional rebase help, integer attempt limit 1–100 and complete reviewer selectors/permissions/addition/removal/help retain their order, exact string numeric/node IDs and limits. Collapsing cannot clear values, reset query/cursor ownership or mutate. Settings-policy constraint validation opens the enclosing details before focusing the first invalid control and blocks submission. Repository overrides retain expanded controls and complete-policy inheritance/replacement.

Cancel edit precedes the scope-specific Save Server preferences or Save Git workflow after a divider in the ordinary right-aligned form flow; retain the exact uncertain retry action, complete-document byte bound, original revision and request bytes, retained unrelated notifications/remediation values and existing validation/revision-drift safeguards. Reflow and same-identity reconnect preserve draft/disclosure within the visit; navigation-away disposal, Strict Mode generations and late-result isolation remain unchanged. Page-level Escape preserves the Settings visit. No autosave, singleton deletion, new API/schema/storage/migration/dependency/native permission/telemetry or execution feature is added. Issue #1236 independently owns the shared Settings screen conversion: this body follows its established visit/navigation semantics without restoring the old shell. Record frontend/component, browser layout/keyboard and packaged CEF platform results separately in PRs/issues/CI artifacts; browser fixtures do not prove native child-dialog/drawer background inertness, Escape, focus containment or application destination focus.

### Session budgets
Session creation and retained-session controls expose the optional estimated-cost budget in the [usage contract](cmds-delidev-usage-contract.md). Keep currency and threshold as exact strings, distinguish absent subtotal from zero, and show incomplete/unpriced/other-currency evidence with no actual-spend or compliance claim. Read the dedicated generated SessionService view and display the inclusive reached warning; the server gates every new turn/Resume and retains pending input without canceling accepted execution.

Budget editing captures the session revision. Preserve stale drafts, require an explicit latest-revision action before a new write, and retain original uncertain mutations for exact retry. Removing the budget is an explicit oneof operation. A raise/removal can permit already eligible queued execution but never itself resumes paused/archived sessions. Read failures mark cached evidence as stale and block new edits, while exact uncertain retries remain available. No display calculation or disabled button replaces server authorization.

### Devices and diagnostic observations
Settings lists paired devices with independent authorization, type, pairing/revocation time and optional Worker identity. An authorized device is not automatically online. Revocation opens a confirmation bound to the original revision, identifies this desktop when applicable and independently prevents current-desktop revocation in both the list and direct confirmation path. Stale new confirmations are blocked; uncertain retries preserve the exact original request even after a peer revision. Accepted revocation remains separate from session/native cleanup and private-file erasure. Generic device reads contain no bearer or pairing code.

Issue #1239 defines the **Paired devices-only** compact list in
`device-settings.tsx` / `device-settings.css` and the existing Settings workspace.
Keep the shared native window and Settings application screen owned by #1236,
its shared category context pane/drawer, all 17 category labels/order/IDs and
the shared issue #1256 body rules. Left-align the category in a fluid 100%-width
column with a 1040px maximum inside shared 32px/24px/16px responsive padding. The one
26px semibold title precedes **Pair devices using a short-lived document.** (14px)
and **Saved on the selected server.** (12px). Refresh settings and exactly one
eligible blue Create pairing document action share the list toolbar, with a
decorative plus hidden from assistive technology. Below 1100px the toolbar sits
below the title and row actions below metadata; below 760px retain the labeled
shared category drawer and unrestricted category selection. All content wraps at
200% zoom.

Use one flat semantic-themed list with divided articles in
server order. Each row displays a decorative outline desktop/Worker icon, complete
inert 16px semibold name, known type as Desktop client/Worker (otherwise Unknown),
textual Authorized/Revoked/Unknown badge, exact current-desktop marker and 13px
UTC dates such as **29 Sep 2026, 00:58 UTC**. Missing/unparseable timestamps are
Unknown; reject impossible calendar dates instead of normalizing them. Do not
interpret future-schema documents to invent names, authorization or actions.
Keep **Authorization does not mean this device is currently connected.** once
above the list and **Local Worker registration is available in Runner Devices.**
below the list/pagination, without a new navigation action or connectivity inference.

Every row has a collapsed-by-default, keyboard-operable Details control with a
device-scoped accessible name, announced expansion and associated content.
Details retains full Device ID, optional `machine_id` as Runner Device ID, original
paired/revoked timestamps verbatim (including invalid strings), and existing
additional displayed metadata. Technical values use small monospace text; names
and values wrap without truncation. Toggling is local and performs no RPC,
clipboard operation or persistence. The Settings workspace owns expansion
independently of the generic page token. Retain matching IDs across refresh,
same-identity reconnect, revocation/list and responsive changes within the active category.
Explicit Paired-device First/Next clears the set; category departure or leaving
Settings clears it too. Keep the existing category-selection first-page reset. Bound
retention to the last successful paired page (at most 50 IDs), prune absent IDs
after successful replacement and retain the last set during failed refresh.

Only supported authorized non-current devices retain Revoke, with a restrained
red outlined visible label and device-scoped accessible name. Revoked/unknown or
unsupported documents gain no mutation authority. The current desktop has no
Revoke and keeps **This desktop client cannot revoke its own registration.**
Revision-bound confirmation/result screens and the pairing editor use this same
column with a 720px maximum inner form. Keep every existing field, help/warning,
status and explicit action, original request/revision/self guards, current reads,
polling, stale/permission/read errors, exact uncertain retry, unknown
acknowledgments and session/native-cleanup/private-file caveats.

Keep one mounted PairingGrant controller for the active Paired devices category. Only its
create button is rendered into the heading; the active inline editor consumes
that button without remounting or duplicating secret/request/query/mutation
state. Existing pairing authority remains independent of list loading/failure,
and existing refresh/operation guards remain authoritative without locking category navigation. Focus Device name once
after explicit creation entry. Cancel/discard restores focus to the stable
creation control after visible DOM commit. Preserve the 256-byte/NUL validation,
pinned server/endpoint, 256-bit private code, digest-only RPC, matched acknowledgment,
fresh matching read before reveal, explicit read-only selection/copy, activity
hiding, five-minute expiry, irreversible consumed/expired code clearing, exact
uncertain retry and discard explanation. No automatic clipboard, regeneration or
replacement grant is introduced. Revocation cancel focuses the initiating Revoke
if still eligible; Return to devices after a result focuses that row's Details.
An absent/ineligible control falls back to Refresh settings. Consume these focus
handoffs once after commit; refresh/reconnect/reflow never steal focus. Leaving
Settings follows the application destination focus handoff without restoring an
old modal opener.

Initial loading, initial read/permission failure, successful emptiness, cached
refresh and failed retained refresh remain distinct. Keep sanitized Problem and
correlation metadata and explicit current-query refresh. Only a successful empty
first page without error/continuation shows **No paired devices yet** and, when
pairing is available, **Choose Create pairing document to pair a desktop client
or manually installed Worker.** Otherwise show **Pairing document creation is
unavailable for this connection.** No duplicate create action is added. An empty
later/continuation page says **No paired devices on this page** and keeps existing
First/Next controls. Hide paging only for successful final first-page emptiness;
retain the 50-resource size, opaque tokens and exact enabled states. Cache rows
only for their current query/page. Failed refresh keeps **Refresh failed. Showing
the last successfully loaded results.** and the sanitized error, including after
cached-empty success, without a definitive empty claim or global count.

Use system fonts, white content, `#f3f5f8` Details background, `#202632` primary
text, `#5b6577` secondary text, `#2563d8` primary/focus accent and `#d8dee8`
panel/divider borders. Authorized badges use pale blue/dark-blue text;
Revoked/Unknown are neutral gray. Keep 1px dividers, 16px row padding, 8px control
corners and flat groups, at least 40px targets and AA text/control/focus
contrast. Control boundaries remain independently visible; state has textual
labels. Use static scoped CSP-compatible CSS without inline exceptions,
dependencies, external assets, artwork, shadows or gradients. Preserve contained
child-dialog and shared-drawer focus, Escape, category announcements and
background inertness. Page-level Escape preserves the Settings visit.

This presentation changes no public API, generated/schema/storage/native/backend
interface, credentials, migration, polling, telemetry or feature flag. Keep
Strict Mode generations, visit disposal, same-identity transport replacement,
accepted server effects and sibling workflows/caches authoritative; ordinary
reopen never restores abandoned drafts/disclosures/secrets/exact retries. Record
actual revisions, commands/results and unresolved limits in issue #1239, its PRs
and CI artifacts. Fixture/build/package checks remain separate from supported
native geometry/keyboard acceptance at 1600×1000, 960×640, 720×800, 420×800 and
200% zoom, with unavailable platforms explicitly named.

Paired-device settings explicitly issue single-use client or manually installed Worker grants through direct CreatePairing RPC. Generate 256 random bits with Web Crypto in the trusted app, retain the original name/type/request and native-pinned server origin/ID in one category-owned component, and send only the SHA-256 verifier. Raw code never enters mutation variables, query keys/caches, Web Storage or logs. A lost response retains the same code/request within the active category; no automatic retry, regeneration or discard is allowed while acceptance is uncertain. Explicit category departure or leaving Settings disposes that category under #1138/#1236 without replay or rollback. Match the response request/resource/name/type before exposing any document.

Read the original pairing resource before allowing disclosure, poll only while this area is active and expose unavailable/mismatched observations. The document uses the original pinned endpoint/server/pairing IDs and is hidden until explicit reveal. A read-only textarea supports explicit select/copy without automatic clipboard access. Hide it whenever the area closes and clear the retained raw code after observed consumption or expiry. A consumed/expired grant cannot be revived by a later stale read. Explicit discard clears the window's copy, with an explanation that an issued grant remains usable until consumed/expired; closing the server window also loses this memory-only copy. This is independent of existing paired-device revocation. Cross-computer reachability and remote TLS remain deployment prerequisites.

Diagnostics uses the direct read-only doctor RPC, only while selected or after explicit refresh. Decode at most 1 MiB of strict UTF-8 and display known fields as inert text. The version-2 [diagnostics contract](cmds-delidev-diagnostics-contract.md) distinguishes running server/platform/protocol/schema, separately sampled database/WAL/logical/filesystem sizes, retained resource counts, Worker connection and installation observations, and exact account credential read classifications. Preserve decimal-string integer precision with BigInt. Show missing measurements as unavailable, explicit 50-record partial inventory notices, and superseded connections independently of successful reads. A failed refresh labels retained observations; unsupported/malformed reports establish no health. Legacy reports retain only their original bounded fields and an explicit missing-capacity/protected-store notice. Owner credential availability, store decryptability, provider readiness, protocol handshake and cleanup are separate facts. No repair, login, harness probe or inference starts from this screen.

The approved issue #1144 Diagnostics hierarchy, record-local disclosures and responsive presentation are owned by the [Diagnostics presentation contract](apps-delidev-diagnostics-contract.md). Its state is local to the active Settings category; page-level Escape and reflow retain disclosures, while category departure or leaving Settings disposes them.

### Local execution proof
The local-role `local_worker_proof` command is read-only infrastructure. It invokes Go inspection for the fixed `desktop-client` and CLI-owned `worker` subdirectories, checks exact server/endpoint parity, validates the private credential's canonical Worker/machine/token shape and returns only the proof needed by a Local product mutation. It accepts no renderer path, command or chosen machine. Missing, foreign or malformed registration fails without pairing, starting or replacing a Worker; Worker bootstrap/lifecycle uses its separate explicit native control boundary.

Selecting Local identifies this computer's paired machine, clears starting overrides and disables arbitrary Worker selection. Submission reads proof again and requires the same selected machine and still-selected connection. The server revalidates actual authorization/revocation at the product commit boundary. Existing Local schedule edits can retain their original authenticated machine without new proof. Proof tokens do not enter form state, read caches, documents or logs; only the bounded immutable pending RPC can retain them after uncertainty, and exact retries cannot silently change to a replacement credential. Connection disposal prevents a late native read from submitting a mutation.

### Local Worker controller
Execution-Worker settings expose explicit registration, startup, status and confirmed stop for this computer. The local-role `local_worker_control` accepts a closed action enum and, only for stop, a canonical original generation. Native code verifies the fixed desktop client/server/endpoint before invoking the same Go `worker pair-local`, `start --detach`, `status` and generation-bound `stop` commands. It accepts no renderer-selected paths, machine identity, executable, endpoint or credentials. Registration is independent from startup and never replaces a revoked, foreign or damaged registration.

Status polling is read-only and separately reports not started, starting, controller running, stopping, exited and uncertain. Startup timeout may retain a process waiting for the server's previous instance lease; it cannot automatically start another generation. The UI captures the generation when confirming stop, blocks a newly stale confirmation and retains the original target after uncertain acknowledgment or settings navigation. Refreshing or retrying that stop cannot cancel a replacement. An explicitly stopped reserved child cannot launch later. Offline stop remains available through local Go infrastructure without depending on server connectivity. Controller exit does not establish per-session/native cleanup; existing server recovery and Worker journals remain authoritative. Full native Quit preserves independently started servers and detached Workers; app-owned server children follow the app-owned sidecar shutdown boundary. No OS service, automatic Worker-process restart, SSH setup or binary-update acceptance is inferred.

### Projects, repositories and configuration actions
Project forms retain ordered repositories and an explicitly selected primary repository. Removing the primary clears that choice without selecting a replacement. Agent/account restrictions preserve the difference between unrestricted selection and an explicitly configured empty list, which permits none.

Repository forms request read-only inspection from the explicitly selected Worker, show its canonical root and recorded remote defaults, and add only that inspected checkout. Base and starting references remain separate explicit local-branch/remote-branch/commit choices; omitted choices use the Worker's inspected default or return an ambiguity error. Fetch remains an explicit Worktree preparation preference. The renderer never normalizes another machine's paths or runs Git. Active/uncertain inspection prevents replacing or submitting its containing edit within the visit; global navigation can still dispose Settings under the common visit policy without canceling accepted effects.

Issue #1142 scopes new registration to a folder-first Add repository flow. Initially show a folder-selection card, Choose folder, Enter a path… and passive This computer context without metadata fields or a Next action. The optional desktop-host `rfd = 0.16.0` dependency implements one asynchronous folder-only dialog per native process; its command accepts no renderer-selected path/content/Git authority, bounds the returned path to 4096 bytes, and authorizes registered trusted local (`main`/`local-*`) or independently bound `server-*` webviews before opening and after completion. Cancellation returns no selection and leaves the draft unchanged. Picker busy, invalid-path/evidence, authorization and other selection failures have wait/reselect/access guidance before Worker verification; no failure before accepting a selection may claim that a newly selected folder was retained. A failed replacement picker preserves the existing confirmation and options.

The main/saved fresh same-computer Worker proof adapter passes through App to the Settings visit. Recheck the original connection/server binding and consume only its machine ID; no credential is retained in registration drafts, query keys or logs. Known stopped/exited status, stale registered server heartbeat, access denial and unavailable verification have independent guidance. No hostname/catalog-first selection, registration or startup occurs. Manual entry can explicitly select another computer from the bounded existing selector, preserving its native path bytes for that Worker's inspection.

A selection automatically submits one existing authenticated inspection and successful canonical output directly populates the repository name/first checkout. The compact confirmation shows name, canonical path, computer, selected remote, locally recorded default and detected GitHub identity; unavailable metadata/defaults remain explicit. There is no Add inspected checkout action for the primary creation path. The confirmation and save readiness remain bound to that exact machine/canonical-root checkout; Optional settings cannot remove it during creation and directs replacement through Change folder or explicit manual reinspection. Additional checkouts remain individually removable, while existing repository editing retains its removal behavior. Collapsed Optional settings reuse name, GitHub owner/name/profile, preferred remote, independent base/starting references, fetch, additional checkouts and complete remediation inheritance/replacement semantics. Initial reference overrides stay empty, fetch stays enabled and remediation inherits the server. GitHub detection is metadata only, with no profile selection or GitHub read. Preferred-remote changes use that remote's validated metadata entry. Existing repository editing/additional checkout inspection preserve explicit saved values and remain separate from primary replacement.

Successful primary selection clears repository-bound overrides and reinspects with no inherited preferred remote before new inference; canceled selection retains the draft. Pending/uncertain inspection or save blocks source replacement/new requests. Original jobs are observed without replay, and uncertain requests retry only their retained exact bytes. Only confirmed successful save publication returns to the repository list; failed/canceled saves may return to the current draft. Leaving Settings/disposal drops the entire registration presentation and guarded picker/proof/inspection/save continuations, initiates no follow-up request and cannot steal focus. Already accepted native/server operations remain authoritative and can appear in fresh inventory reads without wizard restoration. This composes #1138's disposal guards with #1236's application-screen visit. Settings has no page-level Close/Escape dismissal; page-level Escape preserves the visit. Actual child dialogs retain their dismissal semantics. A fresh ordinary visit starts at AI Subscription; explicit Repositories entry remains supported.

The creation column is limited to 760px, retaining Settings white/gray/blue tokens, current category naming, 40px targets, disclosure state and progress/error announcements. Summary rows/actions stack on narrow layouts; existing Settings scrolling, shared compact category drawer and destination focus apply at 960×640 and 200% zoom. Native picker/webview authorization and keyboard/geometry evidence must be recorded independently for macOS, Windows and Linux; component/compile evidence alone does not establish those runtime outcomes. No new feature flag is required; optional result enrichment uses protocol capability negotiation.

Saving repository configuration acknowledges a durable asynchronous job. The UI retains and polls that exact job, shows queued/claimed/uncertain/failed/canceled/succeeded independently, and reports configuration publication only after success. Failed/canceled saves permit an explicit return to the retained draft; uncertainty cannot submit another save. Read refresh never resubmits a Worker operation. Confirmed configuration deletion uses the captured revision, preserves retained sessions and explains affected future schedules/account cleanup; uncertain retries retain their original identity. ChatGPT account deletion additionally composes the confirmed original server logout and cleanup before configuration removal, using a fresh bigint revision after cleanup while preserving confirmed preferences. Its category-owned controller stops follow-up deletion on departure and keeps independent device/browser cleanup visible; see the [subscription Settings contract](apps-delidev-subscription-settings-contract.md). Agent routing previews show the server's candidates, eligibility, selected account, nullable quota evidence and fallback without consuming routing state or granting execution readiness.

### Worker discovery and schedules
Execution-Worker settings show platform, last observation, exact selected/resolved harness paths, version and native-protocol outcomes. Refreshing without path edits retains current selections; explicit editing replaces all four paths, with empty paths meaning Worker PATH. Original revisions and uncertain requests are retained. A checkbox separately authorizes native protocol validation without login/inference. Track the accepted discovery job before reporting completion; never install a harness or turn detection into execution readiness.

Schedules have a dedicated desktop surface that remains mounted across navigation. Creation/editing sends only the strict editable definition: prompt, project, Agent, Worker, input mode, cron, explicit IANA timezone, overlap policy and optional per-repository starting overrides. Defaults are paused Worktree schedules. The server alone computes next UTC due times, skips offline instants, applies overlap/skip/FIFO wait and resolves execution authority. Existing Local definitions can be edited with their unchanged authenticated Worker; new/relocated Local schedules require fresh private proof from this computer's paired Worker through the native boundary. The UI never accepts a supplied UUID as origin proof.

Pause/resume changes future scheduling only. Run now explicitly confirms one independent occurrence, works while paused under ordinary server eligibility, and retains the original request after uncertain acknowledgment without enabling the future timer. Accepted occurrences are shown independently from native execution and current session state. Delete confirms future configuration removal, while the independent paginated history remains visible, refreshable and accessible by retained schedule ID. Every configuration/control mutation uses its captured revision, and stale edits preserve their draft. History and status reads do not replay side effects.

### New schedule creation (issue #1152)

The creation presentation owns `apps/delidev/src/schedule-creation.tsx` and
`apps/delidev/src/schedule-creation.css`; shared schedule state and mutations
remain in the existing schedule editor.

Only creation without an initial resource uses the one-page Task, Execution and
Repeat schedule presentation. Editing, list/detail/history, the icon rail,
context pane and scheduling operations retain their existing contracts. At CSS
viewport widths of at least 1280px, center a grid capped at 1080px, with Task then
Execution in a flexible left column, a 320px Repeat column, and a 24px gap. Below
1280px use Task -> Execution -> Repeat. The main-content creation form owns a
scrolling body and a separate white, top-bordered persistent action row; its
actual wrapped height reserves space without covering errors, pagination, final
controls or focus outlines. Keep scrolling at 960×640, narrow effective widths
and 200% zoom. Use existing system fonts and tokens, white 12px-radius cards,
24px card padding, 8px controls of at least 40px, 28px page title, 18px section
headings and 14px labels. Static creation-scoped CSS preserves the strict
production `style-src 'self'`, dependencies and native geometry.

The heading is **New schedule**, with **Set up a recurring task for your
project.** Required Task fields are Schedule name, Project and Scheduled prompt;
name/prompt placeholders are **e.g. Weekday code review** and **Describe what the
agent should do on each run...**. The multiline prompt resizes vertically.
Execution requires explicit Agent Worker and execution-machine choices with
visible catalog loading, empty-page, permission/authentication, connection and
cached-refresh failure states. Preserve bounded paging and exact off-page or
unavailable selections without fallback. Selectors share a row only when their
available width permits. Native Workspace and Execution mode radio groups offer
Worktree/Local computer and Execute/Plan, with ordinary checked/keyboard
semantics. Local disables only arbitrary execution-machine choice. Selection
and new Local submission each acquire fresh proof for the exact selected
connection/machine; proof never enters form state, queries or documents.

Worktree exposes an initially collapsed **Starting reference overrides**
disclosure with **Using saved project references**, or the override count. Keep
its complete existing explanation, repositories, reference types/fields,
addition/edit/removal, uniqueness and 1000-entry ceiling. Collapse keeps its
contents and drafts mounted; invalid hidden required references reopen for
focus. Changing Project or selecting Local clears overrides. Local omits this
read/form path and explains shared checkouts without fetch or starting overrides.

Frequency is an internal enum, never a definition field: Daily, Weekdays,
Weekly, Custom cron. Defaults remain paused, Worktree, Execute, Overlap,
Weekdays 09:00, `0 9 * * 1-5` and explicit `UTC`. Preset Time is required
minute-resolution `HH:mm`; Weekly additionally selects exactly one weekday,
first Monday, retained when leaving/returning. For valid hour `h` and minute `m`,
Daily emits `m h * * *`, Weekdays `m h * * 1-5`, Weekly `m h * * d` (Sunday 0,
Monday 1), with decimal numbers without leading zeros. Preset switches retain
time. Custom exposes the original required 512-character Cron expression and
five-field guidance; switching to Custom preserves the existing expression
exactly. Custom -> preset imports only the time from exact canonical generated
numeric forms above, including valid numeric weekday 0–6; its weekday never
replaces the separately retained Weekly weekday. Arbitrary, padded or otherwise
noncanonical expressions use the last valid preset time, or 09:00 when absent.
No general parser, browser timezone substitution or client calendar validation
is introduced. Empty/invalid Time remains a visible draft, blocks submission and
cannot silently submit the last valid Cron. Preset summaries show frequency,
time, entered timezone and actual Cron only when Time is valid; Custom shows raw
Cron/timezone without natural-language interpretation. These are selection
summaries, not execution-eligibility proof. Display **Next run is calculated by
the server after saving.** The server retains all calendar/timezone, DST and
absolute next-UTC authority.

Retain Overlap independent sessions, Skip new occurrences while prior work is
active, and FIFO Wait until confirmed cleanup. **Enable future scheduled runs**
is unchecked by default with paused-creation guidance. Explain continued server
scheduling after desktop closure and skipped offline due instants without a
catch-up burst. The action row shows Paused/Enabled on creation plus workspace
and mode, secondary Cancel and primary Create schedule. Cancel ends creation and
returns to the list guidance without a write; acknowledgment enters schedule
details. Local verification, saving and uncertainty lock mutable fields and
Cancel; uncertainty also disables Create and exposes **Retry the same schedule**
using its original retained definition bytes, token, request ID and revision.
Definite errors permit draft correction. Preserve 1 MiB definition, 256 KiB UTF-8
prompt and existing field bounds, retaining the previous valid draft on overflow.
Every save remains strict schema-v1 editable definition only, empty resource ID
and revision `0n` for creation, with one mutation identity and existing owner/client
authorization. Frequency, time, weekday, disclosure and resolved/server-owned
metadata never serialize; no RPC, storage schema or migration changes.

Preserve all creation authoring state, including raw Custom input, time/weekday
and disclosure, across global navigation, Settings and same-identity reconnect;
connection replacement clears it. Inactive reads suspend. Existing schedule
replacement locks remain active while editing/in-flight/uncertain. Focus Schedule
name once on the first active fresh entry; retained entry, refetch and responsive
changes cannot steal focus. Required labels, native radios, disclosure expanded
state, visible focus, status/error announcements and Task -> Execution -> Repeat
-> actions focus order stay accessible. The existing compact native drawer keeps
focus containment, Escape/Close and opener restoration without remounting drafts.
No new motion, telemetry, prompt/token/account logging, feature flag or server
capability gate is added. Ordinary frontend/native packaging and compatible
ScheduleService servers retain the same definition boundary. Record actual
platform/viewport/zoom native acceptance and unavailable platforms separately
from component checks in validation records in pull requests, issues and CI logs/artifacts.

### Session selection and recovery
Session creation uses paginated project/Agent/Worker selectors with no first-option fallback, retains configured-empty Agent restrictions, and bounds the first prompt to 256 KiB UTF-8 before state retention. The first-message textarea preserves autofocus and its insertion caret without a focus ring under the shared form-control focus treatment. Disable Project selection while Local Worker proof or session creation is pending or uncertain so the displayed selection remains aligned with the retained request. Project sessions allow independent starting overrides by repository without changing saved base references. General Chat clears project/Git selection and uses the selected Worker's isolated directory. Explicit Local creation reads this computer’s original paired Worker proof through the native boundary and keeps existing checkouts as-is.

Session details expose revision-bound rename, retry of confirmed failed/canceled preparation, original-workspace inspection, separately confirmed incomplete-preparation cleanup, and original-execution reconciliation. A confirmation captures the original revision and execution identity; peer changes block new submission while preserving drafts and exact uncertain retries. Original preparation/recovery jobs remain inspectable independently. Recovery acknowledgment is not successful cleanup or renewed execution authority. Recovery never sends input or invokes Resume; successful reconciliation leaves the server's paused state intact.

### Retained execution configuration and instructions
The session offers a read-only execution configuration view from its original `initial_execution.configuration`, with no current Agent/model/template lookup. Show retained harness/model/revisions, native option selections, routing/account order and weights, original account, and separately selected current execution/account. Later configuration edits or deletions cannot rewrite the displayed snapshot. Missing snapshots, unsupported document versions, additional unknown options and integer values outside exact JavaScript precision remain explicit rather than becoming defaults or reconstructed values.

Display the exact combined DeliDev instructions and each original template's contents/order/revision as inert text. Preserve whitespace and Unicode without Markdown/HTML execution or truncation, and exclude internal harness prompts and arbitrary native configuration fields. The surrounding resource reader retains its 1 MiB document bound. The scroll-bounded disclosure preserves the mounted session and unsent composer; it has no mutation capability. Memoization avoids re-decoding unchanged snapshot resources during unrelated transcript updates.

Native settings remain separate observations attributed to their recorded execution/input. Empty values and unavailable/null values are distinct; never fill a missing observation from the requested setting. Retained preceding-execution observations cannot be labeled as the newly selected execution's settings. Keep Codex sandbox/approval, Claude tool permission, OpenCode Build/Plan primary-agent and Grok initial `default`/`plan` mode observations separate. The Grok field describes its original pre-input observation; it cannot stand in for later current mode or filesystem permissions. Displaying supported observation fields does not enable an unfinished harness dispatcher or establish current account readiness.

### Retained native reasoning
The transcript renders original `reasoning-text` artifacts in a collapsed, keyboard-accessible Reasoning disclosure, separately from assistant answers and indexed summaries. Combine the initial text and strictly ordered, exactly representable index-free delta sequence once. Enforce the 256 KiB UTF-8 and 10,000-delta bounds, valid Unicode and closed state/kind shapes; complete content must equal that exact stream. Inconsistent or missing evidence displays Unavailable without partial output. Preserve empty text, whitespace and Unicode as inert text. Memoized transcript rows avoid reparsing unchanged records on unrelated stream updates. This read-only display cannot enable unfinished harness dispatch or infer a reasoning capability.

### Retained native Read operations
The transcript gives `opencode-read` its own collapsed disclosure with Pending, Running, Completed and Failed states. Validate original call identity, ordered exact sequences, immutable applied inputs/start time, content bounds and the recorded message state before presenting the retained operation. A pending proposal cannot imply a successful read; contradictory or missing evidence remains Unavailable. Preserve explicitly empty output, zero requested offsets/limits and original Unicode/whitespace. Display paths as inert text, never local file links or access authority. Original proposal/state history, native preview, loaded instruction paths and native truncation/interruption evidence stay separately inspectable. A tool error is neither an assistant answer nor an execution outcome. This read-only surface does not enable unfinished native dispatch or attachment presentation.

### Retained native Shell operations
The transcript gives `opencode-shell` a collapsed Shell disclosure with original Pending/Running/Completed/Failed state. Validate original call, immutable applied command/workdir/timeout/start, exact ordered sequences, safe integer/Unicode/content bounds and independent message closure. Inconsistent or unknown evidence stays Unavailable without partial display. Preserve omitted options without inventing defaults and explicit null exit as Unavailable, separately from no exit observation during execution.

Show the final combined output, original native exit, clipping and saved-output reference independently from rolling preview/state history. Do not concatenate previews, infer separate stdout/stderr or equate Completed with exit zero or session success. Preserve native error text and empty result. Render all commands and paths as inert text inside native keyboard-accessible disclosures, with no launch, copy-to-execute, hyperlink or filesystem access. The surface has no execution authority and cannot enable unfinished harness dispatch.

### Retained OpenCode usage observation
The session reads only its exact `execution.latest_usage_id` through generated Connect Query and validates returned resource/session/execution/harness/version before display. Keep step and finalized-assistant source labels, uncached/cache-read/cache-write/nonreasoning/reasoning categories, nullable reported total and exact currency-unspecified native estimate separate. Never add categories or overlapping sources, interpret native defaulted zero as measured absence, or display this data as billed usage, token-price estimates or budget spend. Preserve original execution attribution after a new selection and label stale refresh data. Missing/malformed or foreign records remain unavailable. This read-only disclosure neither dispatches work nor relabels original observations from current configuration.

### Retained native Todo operations and progress
The transcript renders `opencode-todo` tools in a collapsed disclosure, separately from original session-level Todo progress. Preserve exact ordered contents, native status/priority strings, explicit empty lists, pending proposals without applied lists, result/error and clipping/interruption metadata. Validate immutable applied input/start/call, exact state sequences, complete-list equality and original content bounds before displaying tool evidence; inconsistency stays Unavailable without partial display. Known native status values receive readable labels; unknown and empty strings remain original values. Independent progress validates its native event identity without inventing a message/tool owner. Progress may remain open for visibility; disclosures are keyboard-accessible, and content/paths remain inert. Never offer editable checkboxes, infer task or execution completion, merge repeated native events, or enable unfinished dispatch through this read-only surface.

### Retained native revisions and file diffs
Render immutable OpenCode snapshot/patch/step references separately from native session diffs and input-message summaries. Preserve original source labels, exact ordered file references, optional file/patch/status versus empty content, original title/body and independent safe-integer additions/deletions. A summary must match the transcript record's original input identity; progress cannot acquire a guessed tool owner. Before showing content, validate source/ownership, bounds, completion and exact revision start/completion equality. Missing or contradictory evidence remains Unavailable. Keep disclosures collapsed and keyboard-accessible, patches and paths inert, and original empty lists explicit. Never infer checkout state, execute links, read files, apply patches or expose restore actions from reported references.

### Retained native file and search operations
The closed OpenCode Write/Edit/Apply Patch/Glob/Grep family has separate collapsed disclosures for original input, result/error, metadata and proposal/state history. Preserve original JSON strings exactly, including all native fields, whitespace and number spellings outside JavaScript precision. Parse only for object validation; never render a re-encoded object. Validate tool/call identity, immutable applied input/start, ordered exact sequences, original bounds and independent closure before showing content. Malformed or contradictory records remain Unavailable without partial display. Native diagnostics and reported edits do not become product diagnostic, file-read or patch-application authority.

Original file-edited, file-added, file-changed and file-unlinked notifications appear independently with their reported path. Validate their original native event identity and closed kind; they have instance scope without a native session/message/tool owner. Explain that they neither identify a tool nor prove current file state. Notifications cannot grant navigation, filesystem reads or editing. Render all original content as inert text with no automatic links or execution.

### Retained OpenCode questions and permissions
The shared inbox/session interaction surface renders the separate original OpenCode payload after validating its pinned version, original request namespace, assistant/part/call/event and disjoint content. Preserve question/option order, empty matrices and optional multiple/custom flags; display absent flags as native defaults without fabricating per-question identities or user selections. Permission names, requested patterns, native always patterns and original metadata JSON remain separately inspectable as inert text, with exact numeric spelling. Requested scope cannot imply a grant.

The original `question` tool has its own collapsed native lifecycle disclosure, separate from the interaction request. Inbox reading changes only read state. The separate OpenCode response forms preserve matrix order, native single/multiple/custom behavior, exact empty-string choices and explicitly unanswered rows; questions expose separate explicit rejection, and permissions expose native “Allow once”, session allowance and rejection with optional correction feedback. Explain session-only scope and that plain sibling rejections may stop the native run even when a direct correction is supplied. Automatic policy closures show their own allowed/rejected status without implying another response; malformed or contradictory closure evidence remains unavailable. Keep Codex controls absent. Block closed/submitted/inconsistent requests, ambiguous duplicate labels, malformed text and oversized complete responses. Retain exact uncertain response request identities through revision/closure refreshes without native retransmission. Protected response profiles remain unavailable; never suggest ordinary input as an answer. Missing/mixed/contradictory ownership or payloads remain Unavailable without partial display; observed closed requests retain their historical content.

Original Stop closures explain that the unanswered request was canceled after verified process cleanup, without an answer or rejection. Validate the exact proposal, distinct request/input UUIDs, native part/final assistant, lowercase history digest, terminal/idle/pending/cleanup facts and original interruption or HTTP-acknowledged cancellation. Validate bounded unique retry notifications and their separate canceled-backoff flag without inferring a native error. Reject mixed policy/Stop proofs and claimed, transmitted or accepted responses disguised as cancellation before rendering original content or controls. These retained views do not enable unfinished public OpenCode execution dispatch.

## Security
Only trusted app content receives native capabilities. Renderer/server calls require exact allowed origins and the explicitly selected connection. Account credentials and GitHub PATs must never enter read responses. Never expose a shell, arbitrary executable/file reader, network proxy, or secret-bearing diagnostic object to the renderer.

### Development browser storage

Only macOS native builds with `debug_assertions` select CEF `SecretStorage::Mock`.
Every other native build selects `SecretStorage::System`. The mode is compiled,
not selected by a renderer, environment variable or application argument.
The embedded development launch enables `custom-protocol`; at the pinned Tauri
revision that makes `Auto` select System even in a debug build, so both modes
must be selected explicitly. Mock avoids Chromium Safe Storage Keychain prompts
after ad-hoc rebuilds without an Apple developer account or signing certificate.
It encrypts cookies with a public test key and provides no meaningful protection
at rest. Emit one bounded development notice and the closed mode classification,
never cookie bytes, URLs, native errors or private filesystem paths.

The original `browser-data` remains the System CEF root. Development uses its
owner-private `development` child for CEF and external request-context data.
Shared tabs, profile-removal journals and forgotten-connection markers remain in
their original locations. Neither mode copies or re-encrypts existing cookies;
the first development launch uses a fresh cookie jar. Existing configuration,
account identity, Go protected credentials and runtime pins remain unchanged.
Follow the [browser contract](cmds-delidev-browser-contract.md) for exact
mode-specific paths, the shared process lease and deletion of both copies.
Development Mock is a contributor workflow exception; it does not satisfy
production Keychain, responsive native shutdown or release acceptance.

## Logging
Expose typed safe problems and correlation IDs, plus independent connection/retry state. Never log input, resource documents, tokens, native output or account locators. Native logs use stable operation/failure classifications.

## Build and Test
The native service-ownership shell fixture writes its executable in a joined child before connector admission. A concurrent fork can retain a parent-owned writable script description after the parent closes it, causing Linux `ETXTBSY` before the modeled Go boundary runs. Keep the writing descriptor outside the parallel test process, preserve all service-ownership/no-pairing assertions, and expose only the closed native failure classification when an assertion fails. This fixture isolation does not change production startup or establish the cause of an earlier unexplained CI failure.

Native compilation and packaging begin with the shared `pnpm prepare:assets` preflight. `build:native` (and therefore `dev:desktop`), `bundle:native`, and both native packaging dry runs use this step before compiling inputs. It accepts an existing PNG without Git or network access, including a valid locally edited PNG. For an unchanged committed LFS pointer at `apps/delidev/src-tauri/icons/icon-source@2x.png`, it first checks out the cached object. Only if that object is unavailable does it fetch the exact current-ref icon path, overriding ambient LFS include/exclude and recent-ref settings, then retry checkout. No other asset is hydrated or downloaded. Restored bytes must match the original pointer's size and SHA-256 and pass PNG container/chunk integrity checks; Tauri retains final pixel decoding. Missing, malformed, linked, or locally edited pointer inputs fail without replacement. A missing Git/LFS tool or failed download stops preparation with stable structured stage/status diagnostics and an explicit recovery command, without raw Git remote or credential-helper output. Active Git children use the existing termination-forwarding lifecycle. Installation scripts, runtime pins, and signing environments remain unchanged.

Frontend changes require package-local `pnpm test`, typechecking and production Rsbuild verification. Component tests use real generated Connect router transports and explicit test credentials, exercising draft preservation, revision/request identity, authentication failure and inert content. Native Rust work additionally requires root Cargo tests and host-specific lifecycle checks. Generated `dist` is removed from the final worktree.

`pnpm build:native` generates the typed client, frontend and target-specific Go sidecar before building the native host. `pnpm dev:desktop --data-dir /absolute/private/scope` prepares those inputs and runs the embedded frontend through the package-owned launch wrapper. On macOS the wrapper invokes the pinned `delidev-tauri-cli dev` path so the upstream bundler prepares `DeliDev.app`, CEF Frameworks and helper applications before executing the host. A bare `cargo run` lacks this macOS layout and is not the desktop development entry point. The app-owned `desktop-host` feature explicitly includes the `tauri-runtime-cef` dependency for CLI bundle selection. Windows/Linux retain direct Cargo execution with their adjacent runtime files and sidecar.

The macOS development-only config removes `devUrl`, enables local `custom-protocol`, disables the frontend dev server and Rust watcher, and selects ad-hoc signing. Both preparation and execution receive the native dry-run system/tool environment plus optional `CARGO_TARGET_DIR`, using the pinned distribution in the standard `Library/Caches/tauri-cef` cache; caller CEF overrides and signing/notarization credentials are not inherited. The original distribution's Chromium notices are included alongside existing repository/CEF notices. No runtime pin, default data directory, production configuration or release-signing policy changes. Application arguments, including paths with spaces or Unicode, cross the Cargo/Tauri separators unchanged as argv, never shell text or CLI overrides. Every active preparation/launch child receives termination forwarding, failures stop the sequence, and wrapper diagnostics contain only stable stages and outcomes, not application arguments or private data paths. Node launcher tests run separately from jsdom through `pnpm test:desktop-launch` and are included in `pnpm test`.

`pnpm prepare:sidecar [TARGET_TRIPLE]` accepts only the six explicit macOS/Windows/Linux x64/arm64 target mappings and never silently substitutes the host. Native packaging/signing/publication remain separate acceptance work.

Workspace Rust Clippy and test CI prepare those same generated inputs using the pinned Go toolchain before compiling the desktop host. A bare all-features Cargo command in a clean checkout lacks the Tauri external-binary resource until that preparation runs; generated frontend and sidecar files remain untracked.

## Dependencies and Integrations
Use the repository's React, Connect Query, React Query and Rsbuild pins. The Go server/Worker and canonical versioned protobuf remain authoritative. Toss frontend guidelines inform explicit status, focused forms, clear action hierarchy and accessible components.

## Change Triggers
Update app/root AGENTS, project ownership, the client/protocol contracts, build/CI configuration and validation records in pull requests, issues and CI logs/artifacts with path, interface, lifecycle, security or supported-platform changes. Never conflate a component fixture or build with native packaged acceptance.

## References
- [Project](project-delidev.md)
- [Requirements](cmds-delidev-requirements.md)
- [TypeScript client](packages-delidev-api-client-contract.md)
- [Repository defaults](repository-defaults.md)

### Claude native permission configuration
Agent Worker forms expose Claude's separate native tool permission field with exact `default`, `plan`, `acceptEdits`, `dontAsk` and `bypassPermissions` choices. Explain native Plan behavior independently of filesystem sandbox isolation, and display the relevant edit, denial or bypass scope. Saving configuration does not imply that the unfinished public Claude execution path is available.

Switching harnesses preserves all original options, including unsupported/unknown values. A retained Codex sandbox or approval policy stays visible as an incompatibility and is removed only through the explicit clear action; switching back first restores the original displayed choices. A Claude selection retained on another harness likewise needs explicit clearing. Unknown select values retain their exact value with an unsupported-selection label instead of appearing to select a different available option. Other document fields and revision-bound retry behavior remain intact. Go independently rejects mixed native policies and preserves prior storage on failure.

The existing OpenCode question rejection control retains its accepted response and stopped session outcome. For independently eligible original Question dismissal checkpoints, the existing explicit Resume flow now continues the same native session through the server/Worker; opening the session, reading the interaction or recovering a lost completion report cannot resume it. Answered, dismissed and unanswered-canceled questions remain distinct, with no synthesized answer or repeated question.

Existing OpenCode Read rejection/correction controls can now resume independently eligible original stopped histories through the server/Worker while preserving direct response versus automatic sibling closure. Plain or empty feedback keeps explicit Resume, and nonempty feedback may continue only when the native input actually succeeds. Restored history never reissues the rejected Read, duplicates a correction or turns an automatic closure into a user response.

The existing retained OpenCode Read disclosure continues to display original loaded-instruction paths and output as inert text. Eligible completed loaded Read history now survives server/Worker process replacement and existing Resume/FIFO flows without duplicating or replacing that content with later file changes. No renderer file read, new instruction override or native permission is introduced; unsupported profiles remain separately unavailable.

Existing OpenCode permission controls now retain eligible original Shell/search/file and external-directory rejections through the same server/Worker Resume flow. The transcript keeps the original failed tool and direct response or automatic closure; resuming does not grant the denied permission or rerun the rejected operation. Native Plan restrictions and unsupported profiles remain explicit.

The retained OpenCode Read disclosure keeps an ordinary native error as Failed even when the session later completes successfully or the missing file is subsequently created. Eligible server/Worker continuation now preserves that error through the existing FIFO/Resume flow; opening the disclosure does not access the file or rerun the tool. Tool outcome, session outcome and original permission response remain separate.

Existing OpenCode original permission disclosures remain valid across eligible external-directory allowance restoration. The desktop keeps the original direct approval or automatic closure visible, does not ask again for a restored original response, and distinguishes new native tool requests from retained history. External-directory permission does not imply approval of a tool-specific write or command.

### Claude native message disclosure
Session transcripts render the optional Claude provider-message document as ordered text, collapsible thinking, explicitly redacted blocks and inert original tool references. They preserve empty content, per-block lifecycle, whole-message state and observed native stop reason/sequence. The renderer validates the complete bounded document before any display, renders strings inertly, and treats missing/unknown/mixed/inconsistent records as unavailable. Message closure does not imply successful session completion, tool execution or current account capability.

### Claude native usage disclosure
The session’s exact latest native usage reference also supports pinned Claude `2.1.236`. Validate the original resource/session/execution and closed source graph before displaying any part. Show unavailable and measured zero separately; keep large counter strings and native decimal estimate spelling exact. Main-loop counters, ordered native metadata and cumulative model/cost ledgers stay separate, inert and read-only. Exclude mixed usage families and do not add overlapping reports to billing, estimates or budgets.


### Claude original tool disclosure
Render original Claude tools separately from assistant text. Validate the complete bounded tool document, original product/native tool ID, provider parent, proposal equality, exact result lifecycle and absence of mixed message families before any display. Keep the disclosure collapsed and read-only; paths, tool names, original JSON and outputs grant no navigation, file read or execution authority. Preserve initial input, streamed fragments, original proposal and native applied input separately without JSON reserialization. Retain ordered plain-text result blocks, empty outputs, omitted versus direct caller and unavailable versus explicit native error. A result is an observation, not approval, command success or session outcome. Invalid records remain unavailable without displaying partial content.

### Claude original callback disclosure
Render native Claude permission, question and Plan requests through their separate validated interaction document. Preserve exact input JSON, original question text/ordered options/multiple-selection flags, native-enriched plan and inert plan path, nullable metadata and original permission suggestions. Reject mixed request/response families and mismatched cancellation arrivals before any partial display. No suggestion changes saved settings or grants access; native cancellation does not establish response acceptance. Until the original Claude reply coordinator is integrated, these disclosures expose no Codex/OpenCode answer or approval form and explicitly explain that an ordinary message cannot answer the request.

Claude callbacks now expose dedicated direct Connect response forms after full original-request validation. Keep single/multiple selections, exact custom text, empty-string answers and explicitly unanswered questions distinct. Multiple selections preserve user selection order in the native comma-separated answer string; no question UUID or answer matrix is invented. Tool/Plan allows preserve unchanged callback input, while denial requires an explicit bounded reason. Do not offer remembered permission updates or edited tool input until their complete native adapters exist. Original denial may explicitly select interruption through the dedicated composition below. Forms remain disabled after response acceptance/cancellation; lost server acknowledgments retain the exact revision/request/body in the existing connection-scoped mutation registry. Retrying that receipt cannot issue another native reply. Render original response content and exact echo status separately from semantic acceptance and tool/root outcome. Mixed/foreign response families and inconsistent echo/delivery metadata invalidate the complete disclosure.

Original Claude tool results may additionally retain matching `permission-rule` non-execution metadata with explicit native error evidence. Display that classification separately; generic tool errors cannot invent policy denial, response acceptance or session failure.

Claude callback disclosures now distinguish original native cancellation from original result settlement. Accepted responses require matching arrival/tool/result identity, typed evidence, exact acceptance sequence and an earlier retained echo. Tool processing, exact question answers and native denial remain separate evidence kinds; none claims execution success. Malformed/mixed or contradictory closure records remain unavailable rather than displaying partial acceptance. Forms stay closed after settlement; Inbox read state remains independent.

Original Plan approval and denial now share the separately verified callback settlement display. Accepted Plan records require `native-claude-plan-approval` evidence; denial keeps its independent native-denial evidence. Original plan text and path remain inert, and neither approval nor a native permission transition grants a future input or implies plan execution.

### Claude interruption-coupled denial disclosure
Dedicated original callback forms offer an unchecked-by-default interruption option only when denial is selected. Preserve the exact denial text and explicit true selection in the retained mutation receipt; ordinary allow/denial semantics remain unchanged. Acknowledgment loss and a later closed interaction can retry only the same original server request/body, never a new native response. The native adapter enforces its independently verified single-root-tool interruption scope.

Tool disclosure preserves `user-rejected` separately from `permission-rule`. Accepted interruption replies require `native-claude-interrupted-denial` evidence, not ordinary denial evidence. The transcript displays the independently retained native interruption context and session-level result as separate read-only progress records. Validate the complete ownership graph, original native identity, exact context, closed result kind/reason/error and explicitly absent input identity before display; mixed/inconsistent records remain unavailable. Preserve exact result-usage counters/decimal strings and overlapping main-loop/cumulative scopes, with no inferred cost or input outcome.

The result explains that Claude stopped without an input result identity, input completion and process cleanup remain unconfirmed, and further input is paused for reconciliation. It cannot manufacture success, a terminal Inbox event or Resume authority. These component and scripted-native results do not establish a packaged visible acceptance scenario or full public Claude dispatch.

### Claude original session progress disclosure
Render original session status and thinking estimates through a dedicated read-only transcript component. Validate the complete bounded payload, original native envelope/turn ownership, explicit input-acceptance chronology and exclusion of other message families. Display pre-acceptance status without implying input delivery; null status means the native status was cleared, not idle or execution completion. Keep nullable permission mode, compaction result and inert compaction diagnostic distinct. Exact unsigned thinking totals/deltas remain strings, including uint64 values beyond JavaScript integer precision, and never appear as provider usage or billed cost.

The execution-configuration panel preserves initial native settings and displays the latest explicitly reported Claude permission in a separate region. A sticky permission change explains the required future-input configuration reconciliation, even if the latest reported mode returns to the initial mode. Preserve existing earlier-execution attribution and reject contradictory native-turn/state records; reading configuration cannot change native permissions, dispatch another input or revoke the currently accepted response.

### Claude original input terminal disclosure
The execution-configuration panel displays validated original Claude input outcome, exact native reason, native command closure and separately observed idle. Owned process/workspace cleanup remains unconfirmed until the independent server-accepted completion report. Preserve earlier-execution attribution and original session Stop/recovery differences. The renderer requires the complete original input and three distinct native envelopes, consistent native kind/reason/error/command and matching retained input outcome; malformed/mixed/contradictory records remain unavailable. A native success subtype with an API error cannot display successful input. No terminal disclosure offers an implicit next input or Resume action.

### Original Claude Stop presentation
The retained execution configuration now renders the dedicated original Claude Stop proof separately from correlated input terminal evidence. Show acknowledged native interrupt, native idle and native process cleanup independently of the later workspace cleanup report. Preserve that the native session result omitted an input identity. Original partial content remains inert and is labeled interrupted; a closed stream followed by retry cancellation cannot masquerade as an aborted assistant snapshot or a completed provider response.

Bound and validate every original identity, evidence variant and nullable field before rendering. Exact retry counts/delays and native usage remain decimal strings; native retry observations are read-only and do not offer another attempt. Original interruption context and overlapping native usage are expandable, with no billing or implicit continuation claim. Existing prior-execution attribution remains visible when inspecting a newer selected execution.


### Claude API retry disclosure
The original progress transcript renders native API retry attempt, configured native retry maximum, reported delay, closed error classification and explicit nullable HTTP status. Keep uint64 counters as exact strings and show before/after input acceptance from the original observation. Validate complete nonmixed ownership before rendering; retry-only progress cannot imply a permission observation, clear a prior mode transition or create a usage entry. No retry action is derived from these read-only observations. Stop uses the same bounded retry-value validation while preserving its separate interruption evidence.


### Claude interrupted-denial cleanup disclosure
The execution configuration renders dedicated original denial cleanup only when its exact interaction/context/result references match the retained interruption and all initiating/native identities are distinct. Preserve absent native input-result identity and separate the verified native process cleanup from the Worker workspace report. The original context and usage messages stay immutable and inert. Missing, mixed, foreign, reused or unconfirmed proofs show an unavailable state; no implicit Resume is offered and recovery remains independent.

### Claude root tool progress
The transcript renders validated original tool elapsed time, nullable heartbeat and collapsed original tool summaries through its existing read-only Claude progress component. Preserve exact decimal/exponent strings and ordered tool names, escape summary text, and expose no action from a tool reference or advisory observation. Validate complete same-family records, required nullable fields, bounded references and finite native-number spellings before rendering. Latest tool/summary references do not invent permission state or clear a previous permission transition. Root local Bash task publication is specified below; child publication and task-history continuation retain separate required evidence.

Native main-tool heartbeat disclosures also retain their distinct original progress identity and owning-tool parent in collapsed inert details. Require the pinned canonical heartbeat relation, true heartbeat, no task mixture and a valid original tool reference; malformed or incomplete relations are unavailable. Legacy direct-root observations still require null parent and no separate progress identity.

### Claude original local Bash task disclosure
The existing progress transcript renders validated `task-lifecycle` starts, progress, patches, notifications and background snapshots. Preserve original native task/tool references, omitted versus false flags, exact uint64 strings, closed statuses and worker-restart reasons. Original descriptions, summaries, errors and output paths render as inert collapsed text with no file/navigation/command action; task-local counters explain their possible overlap with provider usage. Empty background lists are explicitly observations rather than completion of known tasks. Task IDs on elapsed tool progress and latest task references remain independent from permission mode. Reject mixed, unknown, malformed and incomplete observation families before rendering. Native window/tray acceptance remains separate from component validation.

### Claude original compaction disclosure
The progress transcript validates and renders original compaction boundaries and anchored summaries before or after input acceptance. Context counts remain exact uint64 decimal strings with absent values labeled separately from zero. Original logical-parent/preserved-message references and summary text/blocks are inert expandable disclosures, with no navigation or execution capability. Explain their separation from billed usage, new user input and the retained transcript; never replace prior conversation messages with a summary.

Reject unknown/mixed payloads, malformed native/product identities, explicit null optional fields, mismatched anchors, duplicate or excessive references, invalid text unions and oversized summaries. Latest compaction references alone cannot imply a permission observation or erase an existing permission transition. Native UI acceptance remains separate from component/typecheck/build evidence.

### Claude original citation disclosure
Original provider text keeps its existing block order and content lifecycle while an expandable citation disclosure shows initial, streamed and completed native collections separately. Preserve absent, explicit null and empty lists, original nullable titles, inert source/file/URL references and exact decimal source indices/ranges. Label the native source kind rather than mapping locations to local files or transcript offsets. Explain the pinned native completion omission while retaining its original streamed references; no citation action opens or fetches content.

Validate all typed unions, ordered completion matching/omission, message-wide serialized citation bounds and source positions before rendering any part of the provider message. Reject unknown/opaque fields, malformed or mixed references, invalid ranges and citation-bearing plain-text interruption records. Keep encrypted native indices out of presentation. Component/typecheck/build and installed-native stream evidence remain separate from native desktop window/tray acceptance.

### Session file explorer

The Files control opens the right session application panel without remounting the conversation or composer. It lists the original prepared repository roots (including a nonfirst primary), supports directory navigation, bounded pagination, explicit refresh and a UTF-8 text preview. It uses owner/client Connect Query against the actual execution Worker, even during native execution. Keyboard users can close with Escape and regain the Files control; native controls provide ordinary keyboard navigation. Narrow windows place the panel below the conversation. Binary, truncated, unsupported and failed/stale reads remain explicit. Inactive queries are canceled and their file-content cache is discarded; no Web Storage, Tauri filesystem access or executable preview is added. See the [file explorer contract](cmds-delidev-files-contract.md). Other session-side applications remain separate unfinished requirements.

### Portable configuration

Issue #1243 changes only the Import / Export body. Its single live-announced 26px semibold title and “Move configuration between DeliDev servers.” subtitle, panels, errors, later workflow states and guidance share the shared left-aligned fluid column capped at 1040 CSS pixels. Preserve the authoritative Settings host, its shared outer padding, unrestricted category selection and category/visit disposal; this category must not change the shell owned by issue #1236 or remount controllers during same-category selection/reflow.

Two flat semantic sections appear first: Export configuration, then Import configuration. Use 24px gaps and thin decorative `#D8DEE8` rules without enclosing cards, shadows, gradients or external assets. Section headings are 16px semibold and body text is 14px in system fonts, with primary `#202632`, secondary `#5B6577` and accent `#2563D8`. Enabled control boundaries use `#8792A2`; controls remain at least 40px high with 8px corners, visible accent focus and distinct disabled appearance. Static styles belong to `configuration-transfer.css`. Below a 600px content width, the export action moves below its description; ordered stages wrap. Full identities, paths, labels and errors wrap, while authoritative textareas remain internally scrollable.

Export retains the outlined action, “Copy a portable JSON document from the selected server.” and the complete eight-kind scope beneath a divider. Its original read-only exported bytes and Select export for copying action stay in that panel. Import retains “Choose a JSON file or paste an exported configuration.”, the native labeled Configuration file picker with “JSON · UTF-8 · Up to 384 KiB”, and the always-mounted Configuration JSON textarea, initially 144px high and vertically resizable, with “Paste a DeliDev configuration export…” as a placeholder. Load configuration document retains its guards and the helper “You will map resources and review changes before applying.”

The noninteractive ordered stages are “1 Load document”, “2 Map configuration” and “3 Review & apply”. Derive the current stage from existing loaded/preview/report/uncertain state, exposing it through text and `aria-current="step"`; stage changes never issue RPCs, hide inputs or create wizard state. Mapping, complete review, exact uncertain retry and results retain the same panel treatment and all original controls, values, status/alert semantics and enable/disable conditions. Sending a request is distinct from server acceptance or completion. Below the panels, Before you transfer states both “Imported accounts are disconnected and need a new connection.” and “Authentication, device registrations, observed quotas, discovered model evidence and session history are excluded.” This presentation grants no additional portable kind, authentication, clipboard-write, download, polling, persistence or mutation authority. Record browser/component geometry, contrast and keyboard checks separately from actual supported-platform native acceptance in PRs/issues/CI; do not add repository evidence records.

Settings provides explicit export, file/pasted import, machine and checkout mapping, unchanged existing-entry reuse, server-preference replacement, complete before/after review and retained apply/status/retry through generated Connect operations. Preserve exact original JSON bytes and pending drafts; importing never authenticates an account or registers a device. See [portable configuration](cmds-delidev-configuration-transfer-contract.md) for supported kinds, atomic Worker validation, bounds and remaining surfaces.

### Original Grok text and response disclosure

The session transcript renders server-retained `grok_text` only after validating the complete original metadata array, exact native session/prompt namespaces, product execution identity, event/chunk ordering, role/state and absence of foreign content families. Preserve original text as inert text. Details show original first/latest events, latest native chunk and exact context estimate; response text completion is distinct from execution completion. Invalid or mixed records show unavailable rather than falling through to generic text.

The existing Connect Query native-usage disclosure additionally reads Grok `1.0.41` resources under their original session/execution/thread/prompt. Validate the closed five-counter response shape and preserve canonical uint64 strings without JavaScript numeric conversion. Label product response order, measured zero, absent total/cost and separate context/input/auxiliary scopes. Retained preceding-execution and stale-read handling stays intact. Neither display starts work, changes configuration, synthesizes billing or enables unimplemented dispatch. Component/RPC rendering evidence is separate from native platform/tray acceptance.

Original Grok completion details additionally require the exact `closed-first-text` terminal shape, original execution/session/prompt, default initial mode, observed model, closed one-response content and no foreign terminal family. Display native end-turn outcome and reported input total/call/duration/elapsed integers with full precision. Keep these overlapping input totals separate from individual-response usage, actual cost and auxiliary work; successful native closure cannot offer continuation or imply the final workspace cleanup report. Missing/mixed/inconsistent records show unavailable and expose no control.

Execution configuration shows the saved Grok context window and its known/user-declared metadata source separately from the original observed native context window. Missing, unknown-source, non-integer and out-of-bound values remain unavailable. These are immutable configuration observations, not streamed context usage, measured provider capability or a fresh model lookup.

### Original Grok Stop disclosure

Execution configuration displays the original interrupted or raced-success Stop separately from successful native-close history. It validates exact input/native ownership, exclusive variant/model/mode, original partial/closed response state, typed retry order and decimal counters. Interruption before first text is displayed with explicitly unobserved assistant text and absent usage, and requires the empty-output digest, zero chunks and no message/content/usage reference. Interrupted text is labeled partial and stopped, retaining inert original output and explicitly absent usage; context is never converted into usage. Raced completion preserves actual native success alongside the product Stop request. Native process cleanup and the independent workspace report are shown separately. Missing, foreign, rounded or mixed records display unavailable and create no action, retry, Resume or billing authority.

### Session Git diff

The Diff control selects the same session application area without replacing the composer. It uses the shared [files and Git comparisons contract](cmds-delidev-files-contract.md), selects the primary prepared repository, defaults Worktree to its creation commit and Local to current HEAD, and exposes explicit staged/path/refresh choices. Validate complete scope, object identities, bounded patch and untracked paths; render text inertly and show empty-tree, Gitlink-only submodule and stale/error distinctions. Closing returns focus to Diff and discards its inactive cache. Raw diff observation alone grants no mutation authority; local review actions use the dedicated operations below.


## Local agent review

The Diff panel exposes original file/visible-line comment authoring, read-only selected context, exact revision-checked edit/delete and grouped Request changes. Keep the main conversation/composer mounted. Select old/new lines only within one visible hunk; binary/non-line files offer whole-file comments only. If the comparison changed before authoring, display explicit refresh/reselection guidance and disable saving. Repository, original query, diff revision and context remain inspectable without interpreting text as markup or links.

Paginated review records include current comments and immutable submission history. Selection retains exact original record revisions and bodies across pages/refresh; concurrent edits require explicit reselection or explicit reuse of a retained edit against the latest revision. Submission selects Execute/Plan and optional stale consent; unavailable reads remain errors. Preserve uncertain create/edit/delete/submit requests with the existing connection-scoped registry, including retry after panel navigation. Disable conflicting controls during unresolved submission. Show accepted queue input identity, original submitted body/context/freshness and later edited-since-submission state separately. No automatic resolution, Steer, GitHub publication or claim of unsupported native response completion. See [local reviews](cmds-delidev-files-contract.md).

### Grok original user history disclosure

The transcript renders a complete `grok_user` record through the existing authenticated Resource stream and message reads. Its reserved product ID preserves its original place before the assistant even though verification finishes after native closure. Validate the closed user metadata, original input/execution/session/prompt identities, event anchor, late publication sequence and absence of mixed content before rendering exact inert text. Details show its selected model, original event and exact timestamp, explicitly labeling verification from closed native history. Null/malformed/mixed records show unavailable. The original terminal additionally requires the matching reserved-message/proof pair when present, while old records may omit both. Display never recreates missing/Stop history or sends another input.


## GitHub profile settings

Settings > Coding > Git Profiles > GitHub uses generated IntegrationQuery for named fine-grained/classic profiles, fixed resource-owner/type definitions, renaming, server-native PAT replacement, identity validation and confirmed deletion. Repository settings explicitly select their profile; deleted associations require reconfiguration. Preserve exact decimal pending revisions, transient password input, cleared byte buffers/mutation caches and original request identity without automatic token retention on uncertainty. Retries require explicit reentry; identity success never claims repository/PR/issue/check/ruleset access. The [integration contract](cmds-delidev-integrations-contract.md) keeps query, form and remediation boundaries distinct.

Repository settings now accept explicit GitHub owner/name and provide an opt-in read-access table for the selected profile. Generated Connect Query cancels/discards on close/inactivation; explicit refresh preserves previous labels during pending/failure. Validate scope, exact revision and the complete observation before inert rendering. Current-user permission access is not another reviewer's verification; endpoint availability is not passing CI or satisfied rules. Actual PAT/form/native acceptance remains separate.

Repository settings include an explicit GitHub item browser with PR/issue selection, state filters, bounded page size, plain title/body search, paging and detail/back/refresh actions. It uses generated Connect Query, validates exact scope/query/provenance before display, distinguishes incomplete/capped search and unknown mergeability, and discards inactive data. PR detail offers separate immutable diff, latest head Checks and combined commit-status reads, with bounded eight-query Back history. Validate exclusive response families, exact heads/IDs/counts and original known/unknown classifications; empty statuses cannot become success. Render patch/digest/base/head and independent state tables inertly. Frontend digest validation is structural; Go checks the original bytes. No head observation implies required CI, evaluated-commit, reviewer or automation eligibility. Bodies remain inert text; validated GitHub addresses offer explicit closed native opening. Remediation surfaces remain required separately.

The session's explicit PR associations panel uses generated SessionQuery link/unlink and session-scoped ResourceQuery pages. Keep the conversation mounted, include archived sessions, restrict new selections to the current project's named repositories and show original titles/identities as historical inert data. Decode the complete page before rendering actions, preserve exact decimal IDs and revision values, and bind link acknowledgments to their original request/repository/number. Uncertain mutations retain exact bytes across panel/session navigation. Closing discards association queries while ordinary configuration selectors keep their existing connection-scoped cache policy. Link/unlink never resumes a session, executes a fix, resolves a problem or writes to GitHub. See the integration contract for persisted metadata and receipt boundaries.

GitHub profile management includes revision-bound official token forms through generated Connect Query, explicit fine-grained repository-selection guidance and classic broad-scope disclosure. Form requests are click-driven and late results cannot open a browser after the view closes. Validated PR/issue details offer explicit Open on GitHub. The trusted main/pinned-server native capability delegates only closed GitHub destinations through sidecar stdin to Go OS presentation; it grants no external renderer navigation, credentials or arbitrary opener. See the integration contract.

PR detail also offers Read active PR rules through the same generated query. Show the exact base ref/commit, original source/type/ruleset identities and separate required-context/App tables, retaining unknown source types and explicit zero-App uncertainty. Reject foreign/mixed/duplicate/incomplete bounded projections before rendering. Read all pages on the server; the desktop has no partial-rule paging or inferred CI result. Follow `docs/cmds-delidev-integrations-contract.md`.

Evaluate required CI in PR detail reads the complete scoped server observation and shows evaluated head/test-merge or verified ALLGREEN entry commit, per-ruleset requirement/App/result and collapsed original contexts. Validate complete totals, unique identities, source/required references and exclusive observation family; show queue/entry identity, strategy, position and entry base separately from the failed native check; leave HEADGREEN, missing queue configuration, unsupported rule, unverified App/workflow and unknown commit cases explicit. No Fix or automatic execution is implied by this read. Follow the integration contract; later remediation must revalidate independently.

Read published feedback in PR detail displays submitted review bodies, code-thread comments and ordinary conversation comments, including approved/dismissed reviews. Validate exact current scope, complete parent/thread references and exclusive family before rendering. Keep unknown/deleted authors, original Bot identities, nullable code positions, provider resolution/outdated flags, edit timestamps and content versions explicit; render bodies, URLs and code inertly. Exclude draft content, dispose inactive queries and preserve the same Back/refresh behavior. This read grants neither local handling nor verified reviewer/automation authority; follow the integration contract.

Verify feedback authors in PR detail reads complete scoped reviewer evidence. Show current stable User/Bot identity separately from repository permission and each feedback entry's explicit GitHub App attribution. Unknown/deleted authors, unavailable permission, no collaborator grant and the uncertain upper end of a custom role remain explicit. Validate complete references and original content versions, keep all names/URLs/body/code inert and dispose inactive reads. The view changes no reviewer policy and never starts remediation; the integration contract defines the separate facts and future execution recheck.


Git and repository editors expose persisted remediation policies through existing generated configuration operations. The server default starts with all automatic kinds off, reuse, merge and three attempts; rebase requires an explicit effective server/repository policy. Repository overrides are complete replacements that start disabled, with explicit removal restoring inheritance. Preserve empty selectors/execution choices, exact numeric/node reviewer IDs as strings, unrelated configuration, revision checks and original uncertain requests. Reviewer kind changes explicitly clear incompatible identity/permission fields. Current policy saving has no execution side effect; describe bounded automatic fixes for linked PRs with current Agent, Runner Device and evidence eligibility; all kinds still default off. Follow the integration contract for bounds, exact identity proof and portable local-reference remapping.


Required CI detail additionally displays original CheckRun/suite IDs, native lifecycle timestamps, nullable output and separately labeled workflow metadata. Validate complete check/status evidence, time ordering, bounded native counts and output sizes; render title/summary/text/description inertly. A workflow's observed aggregate attempt does not attribute every retained job to a rerun. Go owns per-result versions; this view grants no durable handling or execution authority.

Pinned required-workflow CI detail also validates and displays the original numeric source repository, exact path/SHA, original run and independently attributed current-attempt jobs through the existing generated authenticated query. Requirements distinguish workflow references from ordinary context/App names; invalid or cross-family references reject the whole observation. Recompute the requirement state, reason and exact result IDs from both the aggregate native lifecycle and the attributed jobs before accepting current or retained proof. A running aggregate with known current jobs remains pending, and terminal aggregate/job disagreement remains Unknown. Recompute the complete headline state and reason from the validated requirement rows and rule authority in original order; unknown rule sources/parameters and unsupported rule types override otherwise known outcomes. Missing SHA and unsupported source/event/attempt evidence remain explicit Unknown. Facts are inert and grant no new navigation, execution or handling authority; follow the [integration contract](cmds-delidev-integrations-contract.md).


Retained PR feedback is available from PR detail and retained session associations through generated IntegrationQuery. Complete pages validate stable remote scope and original content versions before rendering inert body/code/source/audit metadata. Explicit collection is separate from retained reads; local Dismiss targets the exact resource revision/content version and survives closed/reopened views through the connection-scoped mutation registry. Current provider approval/dismissal/thread flags and latest-inventory membership remain distinct from original evidence and local handling. Historical reads/dismissal remain usable without current PAT access. Page changes reset after collection/dismissal; stale cursors require restarting. Explicit Fix now is implemented below; collection and reads still do not start agents. Follow the integration contract.


Retained PR problem history now covers published feedback, required CI and merge conflicts with an explicit independent collection selector. Latest kind-specific observation time/state/reason remains separate from retained original versions. CI rows show original lifecycle/output and lazily fetch the complete immutable rules/result proof through ResourceQuery, validating its exact context, original scope/time and shared PR before historical rendering. Closing releases that proof query. Conflict rows retain the original transition/ref/commit identity across unknown readings without implying current eligibility. Exact local Dismiss works across all kinds; collection does not imply Fix now or automatic execution.


PR detail and detail-bound observations now show their original source repository separately from base/head refs. The source identity disclosure preserves exact numeric/node IDs and private/public metadata. Explicit unavailable source and historical unobserved data have distinct messages; neither substitutes the base repository. Validate exact identity consistency, closed state and detail-only placement before rendering. Names remain inert text and the display cannot initiate Git work.


PR sessions with accepted original startup rejection display “Agent did not start” and the closed safe reason, original observation time and paused state. Validate the complete envelope, canonical decimal-string revision, original session/input/account/connection/configuration identities, absence of native progress and nanosecond timestamp ordering before disclosure. The exact queue input retains a no-resend history message. Malformed or foreign evidence is unavailable; any present rejection disables Resume while the server independently owns authorization. The display creates no Fix now action, native completion or replay authority. Component/build evidence remains separate from actual native app and live GitHub acceptance.


Session recovery also exposes the original initial Worktree identity when interruption occurred before native progress. The existing confirmation sends only the exact original execution ID and session revision through generated RecoverSessionExecution; a lost response retains that identical request across peer changes. Explain that this inspects original startup or native cleanup evidence and never resends input or resumes. The server independently limits pre-native inspection to positive PR rejection proof. Successful recovery uses the existing original rejection disclosure and remains paused; no native app acceptance is inferred from component tests.


PR detail and retained session associations expose a separately opened remediation-attempt history using generated IntegrationQuery. Closed panels release reads. Validate stable PR/set/chain ownership, bounded policy/problem references, original actor/times and exact state/input/execution/outcome combinations before rendering the whole page. Show original reservation/start/finish, manual/automatic kind, selected policy summary, retained session, startup rejection and lifetime versus since-resumption counters without inferring push/handled status. Preserve uint64 revisions as bigint and normalize UTC nanoseconds without calendar rollover before time comparisons.

An inactive chain with a recorded limit exposes an explicit allowance-resumption confirmation. Bind the selected original set ID/revision, block changed/invalid/stale pages and active/uncertain ownership, and retain the exact request across lost receipts/navigation. Server authority remains independent of rendering checks. Resumption preserves history, starts no job itself and never calls session Resume/preparation/creation. Existing policy editing still states that automatic execution is not available until controller composition is implemented.

### Managed database backup observation

SystemService exposes owner/client backup creation, metadata pagination and explicit integrity inspection through generated Connect queries and the CLI. Settings > Backups retains exact creation retries within the current visit and displays precise byte counts. Listing is not integrity or restoration evidence; failed reinspection clears prior success. Follow the [storage contract](cmds-delidev-storage-contract.md) for bounds, identity checks, pagination and remaining session deletion/restoration work. `DeleteBackup` and `ListBackupDeletions` expose durable irreversible image deletion, original inspected revision/metadata/hash, explicit confirmation, retained exact retries and restart-visible pending/completed jobs. Logical image bytes and unknown interrupted unlink counts never imply physical free-space recovery.

## CEF packaging

`pnpm bundle:native` builds frontend assets and the target Go sidecar before
invoking a package-local CLI compiled from the same immutable Tauri revision.
It explicitly enables the local `custom-protocol` feature so saved-window
authorization recognizes bundled assets, and the `tauri-runtime-cef` dependency so the upstream bundler selects CEF resources,
helper applications and platform entitlements. Pass `--debug --bundles app` on
macOS for a local development bundle. No signing credentials or publication are
configured by this command. The executable-only `build:native` command remains
available for compilation; it does not prove that a complete distributable has
all native resources. The separate [native package verification contract](apps-delidev-packaging-contract.md)
defines six native dry-run paths, retained original notices and package inspection;
actual production signing, release publication and six-platform runtime evidence remain pending.

## First-session checklist

The welcome screen now connects its checklist to the authenticated server status,
`SystemService.GetDoctor`, and bounded account/Agent configuration reads. The
user explicitly chooses **Check prerequisites** or **Refresh prerequisites**;
opening the welcome screen does not inspect protected account references.
The check never discovers a harness, logs in, refreshes a provider, installs
software or invokes inference. Its setup button opens existing settings.

Server connection, database/storage observations, enabled Workers with active
streams and retained verified harness handshakes, saved enabled connected account
health, and Agent configurations are separate facts. No aggregate ready indicator
or automatic execution is derived from them. The selected session still validates
its exact model/account/harness/machine choices at acceptance. Repository work
requires a project; General Chat remains an explicit alternative.

Doctor reports must have the supported schema, original authenticated server
identity, bounded unique Worker/harness inventories and no inference-probe claim.
Account/Agent reads validate their expected resource scope and preserve the
first-page bound. Missing results from a partial page remain unknown; malformed,
foreign and failed observations cannot leave a previous successful badge visible.
Inactive welcome/settings presentation starts no checklist read. All results remain
in the connection's existing nonpersistent query scope.

CEF URL authorization uses blocking runtime getters at the pinned revision.
Commands reaching those getters must run asynchronously outside the native UI
loop. Tray and notification navigation must move authorization to a blocking
worker and recheck shutdown and notification generations before publication.
Otherwise the UI loop can wait for its own queued URL request and deadlock.
Keep these safeguards until the runtime provides nonblocking getters.

Each trusted CEF document explicitly enables the native accessibility tree after
page load through the UI-loop webview callback. The pinned runtime otherwise
applies accessibility notifications only to already existing browsers; newly
opened saved-server windows must also expose their semantic UI. The direct CEF
dependency matches the runtime's 151.8.1 pin. Remove this workaround only after
upstream propagates accessibility state to newly created browsers. Structured exit logs contain no renderer content and distinguish the runtime
Exit event, notification task join, tray task join and return from `app.run`.
The Exit event precedes CEF shutdown and must never be labeled process exit.
Even a runtime return is separate from observed process termination. Native
shutdown latency remains an independently recorded acceptance concern.


### Backups presentation

Settings > Backups uses one left-aligned, full-width column bounded at 1040 CSS px,
inside the authoritative Settings host and its shared 32px/24px/compact padding.
The Backups controller owns one live-announced 26px semibold heading, the description Manage database backups and follow backup operations. and the
Saved on the selected server. scope, neutral Refresh backups and a single blue Create
database backup action. The complete private-data/credentials/browser/Worker scope
note is a quiet strip. Use white surfaces, #F8F9FB table headers, #202632 ink,
#5B6577 metadata, #D8DEE8 decorative dividers, #2563D8 accent and #E7EFFF selection;
controls have 8px radii and 40px minimum height, flat groups with thin rules. Functional
outlines and focus indicators meet AA contrast independently of decorative borders.
These light-palette colors map to the shared semantic theme tokens; Backups
follows the same device-selected Light, Dark or System appearance as its host.
The shared Settings application navigation, geometry, child-dialog/drawer focus
and visit lifetime remain authoritative; page-level Escape preserves the visit.
The separate issue #1236 host change must not be implemented or reverted by this
category treatment.

Inventory is one semantic table with Modified (UTC) / Backup ID, Size, Integrity
and action headers. Preserve server order, complete wrapped UUIDs and roughly 92px
rows. English UTC modification labels include seconds; original fractional
precision remains in `time.dateTime`, supplementary accessible text and inspection.
Invalid dates display their original value. Format exact BigInt bytes with English
grouping without Number conversion or approximate units. Page counts describe only
the current page. Not checked is neutral; listing never establishes integrity.
Only the original fresh checked-inspection predicate may show verified inspection.
Keep the complete listing limitation note and concise Inspect buttons with full-ID
accessible names. Below 800px of available Backups content, stack the same rows and
controls with readable labels and explicit table/header associations, independently
of the host navigation breakpoint. Never duplicate interactive controls or clip
UUIDs, hashes, actions or focus at narrow widths or zoom.

Explicit Inspect reveals detail immediately below inventory and focuses its heading
once per activation. Reads, reconnect, category reactivation and reflow cannot move
focus. Close returns to the originating Inspect button if still present, otherwise
the database-list heading. Detail retains the full ID, original timestamp, exact
bytes, schema, complete SHA-256, integrity/server-identity result, observation
limitation, Recheck and Close. Permanent deletion stays separate with the complete
warning and full-ID checkbox bound to the exact inspection object. Deactivation,
failed/in-flight reinspection and replacement clear fresh confirmation; already
submitted uncertain requests retain their original encoded metadata independently.
Preserve original active/busy/uncertain/capacity gates and irreversible acceptance.

Accepted operations appear after inventory/detail and before history, independently
of the selected tab. Retain full backup/job IDs, exact revisions, pending/completed/
failed/unavailable distinctions, problem codes, individual refresh and terminal-only
dismissal, with 20 local entries per kind and complete capacity guidance. Operation
history defaults to Creation jobs and uses visit-local enum selection with stable
tab/panel IDs. Left/Right/Home/End moves roving focus; Enter/Space selects. Selected
tabs have text/underline indication; hidden panel controls leave the tab order. Both
original query owners stay mounted with independent opaque page-size-20 cursors and
active-only two-second polling even while their tab panel is hidden. Tab activation
cannot refetch, reset a page, replace a controller or mutate server state.

Retain Refresh creation jobs, every history-row value and both complete guidance
paragraphs: creation continues after disconnect/restart and past publication is not
current availability; deletion survives restart, retries failed cleanup and measures
logical file size rather than free disk space. Distinguish initial loading/error,
successful emptiness, cached updating and cached refresh failure for each read.
Initial errors use sanitized Problem alerts/recovery/correlation without empty
success. Cached rows remain with updating/stale guidance. Inventory empty copy is
No managed backups. on the first page and No backups on this page. later; history
uses No creation jobs on this page. / No deletion jobs on this page. and the compact
Accepted jobs will appear here. semantic row. Hide a pager only after a successful
empty first page without a continuation token; retain disabled First/Next for
nonempty first pages and First for empty later pages. Keep direct accepted-job
observation and completion-driven inventory refresh independent of history pages.

The category uses static scoped styles and system fonts, adds no API, persisted
state, protocol, migration, dependency, polling or native-window behavior, and follows
ordinary frontend rollout. Navigation-away disposal and Strict Mode/late-continuation guards
remain unchanged. Component/router fixtures and browser geometry checks do not
establish packaged CEF, screen-reader or macOS/Windows/X11 acceptance; record actual
validation revisions, commands, results and unresolved native limits in PRs/issues
and CI logs/artifacts, never repository evidence documents.

### Durable backup creation

`RequestBackup`, `GetBackupCreation` and `ListBackupCreations` expose original durable jobs through Connect and generated queries. Current CLI and Settings use that path; the synchronous `CreateBackup` remains compatible. Keep pending acceptance separate from image publication, exact retries across navigation, typed failure/stale observations and integer precision. Jobs resume after server restart without client resubmission, and completed history does not assert current image availability. See the [storage contract](cmds-delidev-storage-contract.md).

### Keyless macOS packaging dry run

`pnpm --dir apps/delidev bundle:macos-dry-run` builds the API client, frontend,
Go sidecar and pinned native CEF bundle on a native macOS x64/arm64 host. A separate
`tauri.dry-run.conf.json` selects only the ad-hoc signing identity. The wrapper
passes an exact allowlist of non-secret system/tool environment values; it excludes
certificate imports, notarization credentials, updater signing keys, publication
tokens and executable-injection settings. It neither publishes nor notarizes.
Normal `bundle:native` remains a separate command and does not promise a verified
release signature by itself.

After building, require the original bundle ID, minimum macOS 13 metadata and
matching native architecture for the main executable, Go sidecar, CEF framework
and five helpers. Require ICU, resource and scale data, then verify nested code
and sealed resources with `codesign --verify --deep --strict` and independently
confirm the ad-hoc identity. A mismatch or missing file fails the command. These
checks prove packaging structure for the selected native host, not Developer ID,
notarization, update-signature trust, all native runtime behavior or other OS builds.
Node verifier tests exercise authority filtering and rejection of incompatible
artifacts. The existing frontend test command runs those separately from jsdom
component tests under `src`.

The final basic CEF bundle exposed a further shutdown distinction: the runtime
Exit event can precede actual process termination. A 0.289-second event observation
followed by a still-live native process after more than two minutes is not a
successful Quit. Native sampling placed the main-thread wait inside CEF. The
independent Go server remained available. Do not add forced process termination
as a product success path or call this native unsupported; closure remains an
unresolved acceptance issue pending an observed complete native shutdown.

A later unlocked macOS arm64 menu test at baseline `bfe823f7` reached the actual
Quit menu item. In an ad-hoc diagnostic copy with only private data/cache/log
paths changed, Exit occurred 251.849 seconds after the request and the runtime
returned at 271.666 seconds; the original native process and its helpers then
were absent without an agent-issued signal. The sampled CEF worker waited in
`SecItemCopyMatching`; the native permission UI was inaccessible to the automation
and its handling was not observed. This confirms eventual shutdown for that one
run, not responsive Quit or a resolved review. The test's new private server
could not bind the occupied default port, so it provides no new connected-server
retention acceptance. Preserve the original pin and encryption; do not use a
mock Keychain, force-exit success or unseen prompt handling as a fix.

The development storage exception above does not resolve this production/native
acceptance gap. Any new development observation must remain separate from that
original System-storage evidence and its unresolved shutdown requirements.


### Combined desktop navigation and backup surfaces

Managed Backups is a System category in the shared Settings application screen,
including its category pane and compact navigation drawer. Category departure unmounts the backup
component and discards its local
tracking and retries; accepted server jobs continue and are visible in fresh history reads. The project-grouped sidebar and Inbox workspace coexist with the
live first-session checklist in the session welcome surface.

The first-session checklist validates the complete schema-v2 report before
publishing report-derived observations: required server/database/storage and
Worker fields, closed platform/result values, exact uint64 measurements, bounded
unique resource/credential inventories, all four installation entries and valid
calendar timestamps. Partial or foreign reports and contradictory
installation/protocol/problem observations remain Unknown. Validate retained version,
observation time, capability lists and known optional fields before counting any
handshake; a valid unchecked, missing or failed observation still means Needs setup.

Backups tracks accepted creation/deletion IDs independently of ascending history
pages, using the single-job Connect reads. Keep at most 20 local entries per kind,
allow explicit dismissal only for terminal observations, and preserve pending
entries through navigation. Each successful directly observed revision refreshes
the image inventory; reads stop while hidden and uncertain acceptance retries
still use only their original request.

The **API Providers** settings content follows [provider activation](cmds-delidev-provider-activation-contract.md). Require its existing inventory capabilities, preserve server-derived exact account counts and bounded pages, and keep zero-account providers valid. Explicit Off references retain their identities. Switches use exact revision-bound requests and original-request reconciliation after uncertainty. Agent Worker model selection follows the wizard contract; standalone Models settings is removed. This does not change API account creation, connection or validation authority.

## Preserved project-index implementation notes

The following source-backed notes were relocated from the project index at `12b33a2accaf`. Their historical qualifications and unresolved acceptance boundaries are retained verbatim.

The 2026-09-28 partial implementation checkpoint was merged in PR #1041. On 2026-09-29 the owner resumed completion of all missing desktop/CLI/server/Worker features and automated tests, while keeping actual account/private-GitHub and platform distribution acceptance deferred. The owner selected the existing pinned Tauri CEF runtime for the desktop, preserving macOS 13 support; the shell now uses that runtime, while protected account browser profiles follow their separate contract and native distribution acceptance remains separate work. Record current verification and deferred acceptance in pull requests, issues and CI logs/artifacts.

The macOS `dev:desktop` entry point validates/restores the source-icon LFS asset, prepares the embedded frontend, Go sidecar and widget extensions, then uses the pinned Tauri CLI to assemble and run a CEF development bundle with ad-hoc signing. The same asset preflight protects ordinary native builds and packaging, without replacing local edits or fetching unrelated assets. Executable-only Cargo compilation does not prepare the macOS Frameworks/helper layout. Argument forwarding, cancellation and local verification limits follow the [desktop contract](apps-delidev-desktop-contract.md).

The desktop app icon keeps its original colored ribbon and transparent cutouts, with an approved 20% uniform enlargement inside the same canvas. PNG, Windows ICO and macOS ICNS exports share the canonical source and validation rules in the [desktop contract](apps-delidev-desktop-contract.md).

The current pull-request work additionally addresses issues #1056 and #1057: opt-in automatic titles use a separate, capability-negotiated title Worker lane, and the desktop starts sessions from a chat-first page while retaining the existing conversation surface. This increment does not claim completion of issue #964; native desktop visual and real-provider/account acceptance limits must remain explicit in pull requests, issues and CI logs/artifacts.

- `packages/delidev-api-client`: generated TypeScript/Connect Query client, explicit transport and bounded read-only synchronization for the desktop client.

- `apps/delidev`: implemented React presentation for retained sessions, automatic title status and the standalone chat-first new-session page, contextual menu sidebars, standalone explicit-load Pull requests, search, activity, bounded exact-response usage summaries with historical cost estimates and model pricing forms and optional session budgets, a server-filtered paginated Inbox list/detail workspace with exact-source notification navigation and bounded connection-memory question/approval drafts, question/approval responses, queue actions, editable provider/model/account/agent/instruction/project/repository settings, explicit account connection/validation/catalog refresh, Worker checkout inspection, configuration deletion, read-only routing previews, Worker harness discovery, paired-device authorization/revocation and private single-use grant issuance, bounded read-only storage/Worker/protected-reference diagnostics, singleton routing/fetch preferences, schedule lifecycle/history, native same-server Local Worker proof, explicit local Worker registration/controller lifecycle and explicit preparation/execution recovery; the native Tauri/CEF host opens separate pinned saved-server windows and starts/reuses and supervises a bundled Go server with durable explicit-stop coordination and a separate paired client. Native tray and client-specific reserved inbox notifications are implemented with current-source navigation, explicit permission settings and separate platform evidence requirements. Remaining desktop surfaces and multi-platform acceptance are in progress. On 2026-09-25 the owner explicitly expanded the active request to all of issue #964, including the desktop app; CLI-only acceptance is no longer the completion boundary.

Issue #1052 adds same-snapshot server-side daily and per-model analytics to the existing exact-response Usage summary, with optional backward-compatible Connect fields and CLI `--granularity day --timezone <IANA>` support. The desktop retains the existing navigation/table/cost evidence while adding the Token Usage hierarchy, draft filters in the Usage context pane, exact daily/model charts and full data tables. Contracts and implementation evidence live in the usage, protocol, client and desktop docs listed below.

The desktop shell combines the issue #1044 project-grouped session sidebar and bounded independent project/global/project-session pages with the issue #1059 shared rail and seven menu-specific context panes. Usage keeps its draft filters in the Usage context pane while retaining the Token Usage hierarchy, exact daily/model charts and complete data tables. The responsive native-dialog drawer uses explicit Apply behavior. The Pull requests destination requires an explicit repository selection and Load; Settings repository browsing remains separate. PR detail navigation, collection selection, allowance confirmations and exact mutation retries remain available, while disposable observations are dropped on exit. The deferred New project action opens the existing Project editor, query-local read retries preserve current pages, and successful project saves use the existing Settings invalidation path without resetting sidebar cursors. The desktop contract owns implementation boundaries; record acceptance limits in pull requests, issues and CI logs/artifacts. This adds no RPC or backend GitHub capability.

Issue #1134 shortens the shared Settings category label to **AI Subscription** in the sidebar, category heading and shared category navigation while retaining `subscription-accounts` and the existing 16-category order, responsive layout and account behavior. Record validation separately in pull requests, issues and CI logs/artifacts.

Issue #1138 replaces Settings leave/reenter retention: ordinary entry starts at the first AI Subscription category; targeted New Project/Repositories entry remains explicit. Each active category retains workflows through reflow, same-category reselection and same-identity reconnect, then disposes drafts, confirmations, local tracking, client waits and retry UI on category departure or leaving Settings under #1236; page-level Escape preserves the visit. Server/native effects already accepted remain authoritative, and sibling connection/session workflows are preserved. The desktop contract owns category-scoped transport/cache isolation, same-identity reconnect and Strict Mode rules; component evidence remains distinct from native drawer/child-dialog Escape and page focus acceptance.

Local is the default; listeners default to loopback. Remote transport requires authentication and encryption. Native harness/provider protocols remain internal adapter boundaries. Codex native thread/turn controls preserve exact observed settings, input/turn identities and uncertain acceptance; typed core events cannot clear recovery or replace owned cleanup. These primitives do not independently authorize public session execution or freeze server configuration. No WebSocket, standalone client SSE, browser client, Docker distribution, account failover, harness installation, or automatic server updates are introduced.

Portable configuration now uses the [version-1 transfer contract](cmds-delidev-configuration-transfer-contract.md) across Connect, CLI and desktop settings: explicit machine/checkout remapping, read-only signed previews, fresh disconnected accounts and atomic all-repository validation preserve existing settings on failure. The supported eight editable configuration kinds do not imply portability of other product surfaces or runtime authority.

API provider availability is server-owned and presence-aware under [issue #1046](cmds-delidev-provider-activation-contract.md). Six hosted presets are saved On by default on new servers and added once when missing on existing servers; the three local presets remain virtual Off until activated. Defaults create no accounts, models or keys, and saved explicit Off settings remain intact. Inventory capabilities gate desktop provider/model flows; active-only model filtering is server-side. Off blocks fresh discovery and execution grants while preserving retained references and an already authorized turn.

Account type/provider list selectors are list-only Connect inputs applied by SQLite before page limiting. They do not alter coherent snapshots or event streams. Desktop account menus and the guided API account flow require `ACCOUNT_TYPE_FILTER` plus provider activation, active-provider model filtering and account-provider filtering capabilities; see the account, protocol, catalog and desktop contracts.

Grok Build `1.0.41` now participates in native protocol discovery through owned private configuration inspection and ACP initialization, including its original empty startup MCP inventory. The profile rejects inherited settings/extensions and discards native host/path metadata, with no authentication, session creation or inference. Real isolated macOS arm64 discovery evidence is separate from the still-required Grok execution/account/Worker integration and other-platform acceptance; detected or protocol-verified installations retain empty execution capabilities.

Grok original session/input binding now composes its native observation, immutable Worker claims, durable outbox and transactional server publication. Initial Execute/Plan mode remains separately typed and visible in desktop execution configuration. Actual pinned macOS server/relay fixtures cover both modes and lost session/input acknowledgments with receipt-only replay; original text and response counters now have their own typed publication and desktop disclosure, and the separately verified closed first-text profile now publishes its native terminal plus a version-1 completion boundary. The ordinary first-text Worker runner and public initial Execute dispatch now compose workspace ownership, original native closure/history and the version-1 completion report for owned General Chat. The selected model context window and known/user-declared source are immutable and matched against native setup; unsupported selections remain queued without routing changes. User/tool/interaction/Plan/Stop publication and continuation remain unfinished. Registration is limited to the verified first-input Chat Completions/default-settings profile and cannot silently omit explicit instructions or options.

Claude's closed root-session evidence now has a private Worker file boundary binding immutable assignment, account/connection and exact original native completion to independently hashed canonical checkpoint bytes. Restoration verifies those facts and original runtime/history without launching native work or reconstructing missing state. A private installed macOS fixture verifies conversation preservation after replacement; public Claude dispatch, outbox/reporting and lost-Worker recovery still need their separate integration and cannot be inferred from this persistence evidence. Native permission configuration is now separately typed and editable in the desktop; input-specific observations preserve Claude tool approval semantics without a synthetic Codex sandbox.

The original OpenCode Worker runner now composes authenticated assignments, workspace ownership, fresh native startup, registered relay scope, durable binding/transcript/usage/interaction publication, direct replies, targeted Stop/Archive and version-1 cleanup reporting. Actual isolated macOS evidence covers the real outbound Worker stream, including report acknowledgment loss and Worker revocation. Public first dispatch now validates the same immutable native selection and current Worker/account/workspace authority, then atomically claims default OpenCode Build/Plan Chat Completions execution. Actual Execute/Plan evidence includes public configuration, pairing, keyless account validation, session creation and first Resume through real Worker/native completion; the reported protocol discovery remains fixture evidence. Original completed/stopped native cleanup now permits a bounded private checkpoint with exact whole-runtime file digests, original history/settings references and explicit Resume requirements after Stop/failure. The production Worker synchronizes a separate private envelope bound to its original assignment, claims and acknowledged terminal after workspace cleanup. Eligible new General Chat text/reasoning and the closed inline tool/interaction profiles described below now produce digest-bound version-2 completion and use public Build/Plan FIFO/Resume through fresh process/runtime/credential ownership and complete original history checks. Failed/stopped predecessors require explicit intent. Existing version-1 reports remain paused; remaining tool/project/auxiliary continuation, multi-repository settings, Windows General Chat root identity and remaining issue requirements are unfinished.

The native API relay accepts only Worker-registered execution token digests bound to durable claimed jobs and the current server process epoch. Its closed authority profiles now include OpenCode `1.18.32` first-input Chat Completions with exact default Build/Plan settings, separately verified on an actual native process through authenticated registration and account revocation. Original OpenCode session/settings and stored-input bindings now compose the real Worker mutation journal/outbox with the server database, including exact acknowledgment replay. The separate mapper retains original user/assistant text parts and plain-text reasoning artifacts, their native parent identities and independently validated message completion. The desktop renders original reasoning separately, with exact stream/final consistency checks. Original local Read proposals, running inputs and tool results/errors now publish through typed bounded snapshots with independent parent/call ownership and desktop disclosure. Separate OpenCode usage observations retain original step/message sources, counter categories, missing totals and unpriced native estimates; desktop display follows the original execution reference, and overlapping/defaulted native counters cannot enter billing, price estimates or budgets. Original Shell command lifecycle, independent rolling previews/final output, nullable exit and clipping references now pass through the same outbox/server and inert desktop transcript, with cross-kind Read/Shell/Todo call uniqueness. Original Todo tool proposals/applied lists/results and independent session-level list updates now retain exact ordered statuses/priorities and explicit clearing through the outbox/server and desktop, without fabricated tool ownership or session completion. Original snapshot/patch/step references and source-separated session/input-summary diffs now publish and render as immutable read-only evidence; actual Git-root fixtures verify native file edits, original relative-path handling and terminal history coverage. The original event composer now gates terminal publication on settled native state and complete original stored history/projection comparison; exact receipt replay does not establish process cleanup. A separate original-reader-serialized history/owned-cleanup path now produces a version-1 report and passes actual server reporting without enabling continuation. The original Worker now integrates workspace-lease/job-journal orchestration as described above; the remaining native profiles/publication and checkpoints/continuation remain separate unfinished integrations. Every request revalidates session, input, Worker, account connection and restrictions; account disconnection atomically requests cancellation of selected-account native jobs and joins relay cleanup before credential deletion. Worker cleanup/reporting remains a separate confirmation, and old disconnect receipts cannot cancel work on a replacement connection. First-native Stop/Archive persist targeted pause/cancellation; only matching terminal publication plus verified process cleanup can complete Archive, and Restore cannot resume execution. Codex verifies effective private provider configuration and passes an installed-harness/server-relay composition with a scripted local provider. Core input/message/terminal events now publish through a locked durable Worker outbox into atomic server state/transcripts with immutable native identity mapping. Native command/patch lifecycle and revision observations now retain dedicated tool records through the same outbox, including separate nullable stream/aggregate output. An installed macOS Codex Worker fixture verifies a private POSIX command with a scripted local provider; file-patch acceptance remains fixture-only. Plan/reasoning artifacts and immutable plan/diff observations also use the durable outbox, with separate final/streamed content and no invented native item for turn progress. An installed macOS fixture verifies native plan-step updates. Native token observations and redacted notices now share that outbox; counters retain event-time attribution without becoming billable aggregates or inferred costs. Exact Codex response completions additionally persist in a deduplicated immutable ledger with nullable counts and no inferred cost; bounded RPC/CLI/dashboard reads now expose known subtotals and missing telemetry, and immutable pricing versions, exact currency-separated estimate aggregation and owner/client pricing RPC/CLI are implemented; desktop pricing configuration and historical estimate presentation are implemented; optional lifetime session budgets now gate new execution and Resume with explicit incomplete evidence, retained pending input and separate RPC/CLI/desktop controls. The Worker loop now delivers scoped credentials and executes the accepted first Codex assignment through terminal cleanup reporting. Public Codex API first dispatch, FIFO turns, explicit Resume and selected-input Steer are integrated; complete usage coverage, event normalization and native recovery remain pending.

The lightweight owner/client overview provides exact current ownership, unanswered-request and connected/registered Worker counts plus UTC day boundaries through Connect and `server overview`. The desktop tray now renders these observations alongside incomplete known usage and independently stale per-account quotas, with masked aliases, bounded native presentation and original-window navigation. Title-bar close retains the last product window and its drafts when the tray is installed, and destroys other product views; explicit Quit stops app-owned sidecars before terminating the desktop client while preserving independently started servers and Workers. Scope/revision checks and retained activation tickets protect replacement views and delayed acknowledgments. Native initialization, actual menu interactions and supported-platform acceptance remain distinct; notifications and signed updates remain separate integrations. The [initial macOS widget](apps-delidev-widget-contract.md) now reads protected saved-server metadata with explicit per-instance selection; native build/fixture evidence remains separate from provisioned installation and OS interaction acceptance.

The private Claude live-session controller now serializes subsequent inputs after observed idle and settled work, revalidates applied settings, retains original native identities/late callback ownership and blocks implicit continuation after failures or permission changes. Actual same-process two-input, asynchronous-child continuation and Plan fixtures exercise it. Persisted Claude checkpoints/recovery and public execution integration remain required.

Claude automatic compaction now retains the original boundary and anchored summary independently of input acceptance. Main-history verification proves original conversation provenance and separately reconstructs native active context, including exact bounded native batched re-recordings. Actual macOS scripted-provider evidence verifies the three-input compaction and original history. A separate private manual-compaction action now preserves native success/failure independently of the outer result and preceding conversation, with explicit Resume after failure and actual success/provider-rejection/insufficient-history evidence. Manual-command retained history now verifies separate command order/ancestry, original diagnostic identities and explicit unforwarded caveats, including all three actual native outcomes. The private main/child filesystem reader now validates canonical private scopes, original file/ancestor identities, platform permissions and exact bounded file pairs without rewriting native bytes. A private single-use closed root-session handoff now joins cleanup, pins original history/settings and resumes the original native session with a fresh process owner/relay credential; actual default and Plan fixtures cover three processes. Manual-command replacement now verifies immutable original prefixes and separately typed synthetic resume context for success, provider rejection and insufficient history. A separate canonical private checkpoint format now preserves root identities/failures and original prefix evidence across adapter restoration, with independent digest/configuration/reference checks. Settled root inline-text Read, synchronous Bash and Write/Edit tools now preserve their original tool/result ownership through native closure, checkpoint restoration and replacement, with separately retained exact allowed-tool approval echoes. Other tool, child, callback and auxiliary completeness, Worker checkpoint publication/recovery and public Claude integration remain required. Completion evidence now separates original harness identities from product UUID-v7 IDs, preserving native Claude UUID-v4 turns under assignment-selected validation while retaining existing Codex/publication restrictions.

Original OpenCode questions and permissions now reach the retained inbox and desktop under their own ordered matrix/scope representation. Direct question answers and one-request permissions use existing authenticated response/claim RPCs and CLI commands, exact metadata-only Worker journals, original native HTTP/reply evidence and independent request closure. Desktop controls preserve native choices, explicit empty rows/strings and exact uncertain server retries. Registered native fixtures cover successful delivery through owned cleanup/reporting plus lost publication acknowledgements without native resend. Native session allowances, permission/question rejection, optional exact correction feedback and automatic permission cascades now preserve separate direct-response, native closure and root outcome evidence through the same pipeline and desktop. Registered native evidence includes later automatically allowed Read, stopped rejection and correction that remains confined to the direct request. These interaction adapters alone do not grant public dispatch authority; the separately validated General Chat profile below supplies dispatch and continuation. The remaining issue #964 requirements are unfinished.

Original live OpenCode Stop now composes the exact native claim, bounded original event retention, full stopped-history comparison and joined process cleanup with canceled unanswered interactions, terminal publication and version-1 reporting. Server validation retains independent product Stop/Archive, native interruption or canceled retry backoff, exact original ownership and inbox read state. Desktop closures explain cancellation without an answer or rejection. Public OpenCode dispatch, production workspace/job/control orchestration and checkpoint-backed continuation now use the separately validated General Chat profile below. Other workspace/tool scopes and the remaining full issue requirements are unfinished.

OpenCode multi-repository Worktree and authenticated Local execution now keep the designated primary cwd while exposing every ordered additional repository through verified native local references. A private reference-only config bridges the pinned native loaders without adopting repository configuration or remote clones. Exact references join checkpoint/settings and read-only recovery comparisons; the existing complete workspace lease still owns every repository. Native Build/Plan permissions and original file/tool evidence remain independent. Record actual macOS fixtures separately from other-platform and selected-account acceptance in pull requests, issues and CI logs/artifacts.

Claude initial-assignment API registration now uses the existing authenticated Worker and revocable selected-account relay. Its exact Messages-only profile shares settings validation with the private Worker adapter; public Claude dispatch/publication, continuation registration and selected external-account acceptance remain separate unfinished work.

Claude original root text publication now retains one provider-message record with ordered text, thinking and redacted-thinking blocks, separate completed/stopped states and original provider stop metadata. The desktop renders the bounded validated shape as inert content; thinking signatures remain private. Native thinking-token estimates remain separate progress, never usage or billing. Actual Execute/Plan lost-ack evidence covers original user publication and a thinking delta without repeating inference. Rich/child/tool/usage/interaction/terminal publication and public Claude dispatch/continuation remain separate required integrations.

Claude root usage publication now retains original message/block reports and input-result main-loop/cumulative ledgers through the shared durable outbox, with exact string counters and desktop disclosure. They remain overlapping native telemetry outside normalized billing/pricing/budget accounting. Original terminal, tools/interactions/children, dispatch, continuation and remaining account/platform requirements are still being integrated.

Claude original root tool publication now retains ordered provider references and separate native proposal/applied-input/result records transactionally, with exact JSON text and receipt-only retries. The desktop renders these as inert disclosures, preserving nullable error and caller evidence. Actual private Read success/error fixtures cover registered relay, Worker publication and server retention. Failed root Read calls also retain independently verified native error history through private checkpoint restoration and fresh-process continuation; this does not grant public dispatch or generic recovery authority. Interactions, richer/child tools, terminal composition, public Claude dispatch and remaining issue requirements are still being integrated.

Claude root callback requests and original native cancellation now share durable publication, transactional inbox retention and dedicated desktop disclosure. Permission/question/Plan data preserve original tool ownership, callback inputs and nullable metadata without borrowing other harness response controls. Real default permission and Execute/Plan question fixtures cover lost request acknowledgments and original cancellation without executing the unapproved command. Original reply delivery/acceptance, native Plan-approval publication composition, callback restoration, terminal integration and public Claude dispatch remain required work.

Claude original callback response transmission now composes owner acceptance, current Worker claims, a native ownership/digest barrier and one-shot pipe delivery. Dedicated desktop forms preserve original question strings and unchanged-input allow/explicit denial. Original reply echoes remain independent transport evidence, and original denied-tool metadata is retained without promoting it to semantic acceptance. Real registered-relay fixtures cover tool allow/deny and Execute/Plan question answers plus question denial with lost request/delivery/echo acknowledgments. Native semantic acceptance/settlement, interruption-coupled denial, Plan approval composition, restoration, terminal/public dispatch and all remaining issue requirements are still in progress.

Original Claude interruption-coupled denial now composes owner response, once-only native claim/send, exact echo, rejected-tool settlement, separate context and uncorrelated session-result publication through the server and desktop. It preserves absent input identity and independent usage, pauses for reconciliation without inventing terminal/cleanup, and replays only original receipts after lost acknowledgments. Actual private registered-relay fixtures cover tool, Execute/Plan question and Plan approval denial. Native permission/progress publication, terminal/cleanup reconciliation, full public dispatch and the remaining issue scope are still required.

Original Claude session status and unsigned thinking estimates now publish through the shared binding/content outbox and render independently in the desktop transcript. Pre-acceptance status preserves original queue/acceptance chronology. Native Plan permission transitions retain initial settings and a sticky reconciliation flag without revoking the current response relay; thinking estimates never become usage or billing. Actual registered-relay approval/denial fixtures verify original progress acknowledgment recovery. Terminal/cleanup, full public Claude dispatch/continuation, other progress families and all remaining issue requirements remain in progress.

Original Claude input termination now composes the correlated native result, independently closed command and native idle with previously acknowledged content/callback/result usage. The server retains original terminal classification and Inbox outcome without inventing cleanup or clearing prior product state. Exact original-controller clean EOF then permits a separately reported version-1 cleanup completion, including settled Plan permission transitions; dispatch remains paused. The desktop displays original outcome and cleanup separately. Public Claude Worker orchestration, history/continuation and session-level interruption reconciliation remain unfinished, alongside the remaining full issue scope.

Claude original root API retries now publish through the shared outbox and desktop transcript with exact counters/status, original acceptance chronology and independent usage/permission/outcome. Provider retries remain native behavior under the same checked relay scope; acknowledgment recovery never repeats input or inference. Partial-content retries, exhausted-retry cleanup, richer progress and the remaining full issue scope are still required.

Claude interrupted-denial completion now composes original settled callback/context/session-result, cancelled-command/idle, exact native EOF cleanup, a dedicated stopped-execution publication and separate version-1 workspace report. Desktop cleanup disclosure preserves absent native input identity. History recovery remains required and dispatch stays paused; this does not enable resumed interrupted history or remaining issue scope.

Public Claude lost-completion-report recovery now joins original assignment, binding, report/outbox, closed process/workspace and comparison-only native checkpoint evidence for successful root content, Read, answered questions and synchronous Bash/Write/Edit histories. The replacement Worker receives instruction hashes and original settings without prompt/answer bodies or relay authority. Reconciliation preserves the original result and always remains paused until explicit Resume; failed/stopped/denied/Plan-transition and richer histories retain separate required work. This does not complete the full issue or hosted-account/platform acceptance.

Claude root tool progress and advisory summaries now have typed Worker/server publication and read-only desktop disclosure. Original references, native number spelling, nullable heartbeat and receipt-only retries remain distinct from tool completion and approval. Original root local Bash tasks now also compose typed task start/progress/patch/notification and background-list publication, acknowledgment-bound ownership, task-bound tool progress and independent terminal gates. Desktop disclosures preserve exact counters, missing flags and inert original metadata. The actual long-Bash profile completes with original task start/notification and separate native cleanup; the separately proved completed inline Bash task profile now supports v2 continuation and completed-report recovery, while other task histories retain their independent gates.

Original main-tool heartbeat publication additionally preserves the pinned native progress identity and explicit root-tool parent, with exact ownership validation and inert desktop disclosure. This supports long-running tool observations without treating heartbeat as completion; see the harness contract and validation records in pull requests, issues and CI logs/artifacts.

Public Claude automatic compaction now retains original boundary/summary progress through Worker/server publication and inert desktop disclosure. Original context counts and anchored summaries remain independent from input acceptance, provider usage and retained conversation content. Successful root history continues through native compacted checkpoints, including paused comparison-only lost-report recovery; explicit manual compaction commands and richer histories retain their separate required implementation and evidence.

Claude root-text citations now compose native decoding, receipt-ordered Worker publication, atomic server retention and inert desktop disclosure. Original source variants, exact positions and initial/streamed/completed collections remain distinct, including the pinned native omission of completed citations. Original input completion and cleanup remain independent. The positively observed web-citation omission profile now supports v2 continuation across fresh native processes and metadata-only completed-report recovery, preserving native empty arrays and separately retained public deltas. Other citation shapes, interrupted/richer content and remaining issue scope still need their separate implementation and evidence.

Session workspace file browsing now has public owner/client RPC and CLI access plus a right-side desktop panel. A separate outbound Worker observation stream permits reads while the primary stream retains an unresolved native execution, without mutating execution authority. Original manifests select all repository roots and projectless workspaces; portable paths, anchored regular-file reads, bounded UTF-8 previews and directory observation pages are shared by clients. The same channel now provides bounded Git diffs against current HEAD, the staged index or immutable Worktree creation commits, with original unborn-branch evidence and separately listed untracked files. The desktop Diff panel preserves the conversation/composer and renders inert comparisons. Durable local review comments/submissions are implemented below; other session-side apps and the full issue requirements remain unfinished.

Grok first-text Execute/General Chat now composes public Stop with its original once-only native claim, acknowledged content, independently retained interrupted/raced-success terminal and separate workspace cleanup. It preserves an accepted pre-text interruption without creating a message or usage, partial text and missing usage, including the pinned HTTP-retry observation caused by relay revocation before native cancellation. Desktop disclosure separates native outcome from Stop and workspace reporting. This adds no stopped-history continuation, richer tool/Plan Stop, real-account acceptance or remaining issue completion claim.

The workspace read surface also exposes bounded structured local-review coordinates through `ReadSessionReviewContext` and `session review-context`, preserving original Git paths, old/new line numbers, hunk boundaries and final-newline facts. Dedicated owner/client RPC, CLI and desktop operations now implement durable file/line comment CRUD and grouped Request changes into the existing agent input queue. Original revisions/anchors, stale consent, concurrent edits, atomic snapshot/link/input acceptance and exact replay after comment deletion remain explicit. An explicit installed-Codex Worktree fixture now proves a grouped two-repository review continuing the original native history, actual file writes, refreshed Worker diffs and receipt-only replay using a scripted local provider. This does not establish another harness, unfinished native continuation profiles or hosted-account response evidence; see the [workspace contract](cmds-delidev-files-contract.md).

Grok successful first-text sessions now retain their original user input after independently checked native closure/history. The reserved product identity preserves conversation order, while the message records its actual late publication and closed-history source. Input/model/event comparisons and atomic terminal/message receipt replay prevent substitution or duplication; Stop and historical omissions remain unfilled. Existing RPC/CLI reads and the desktop display this original record. Richer user/tool/interaction/Plan histories, continuation and the remaining full issue scope remain required work.

Named GitHub.com PAT profiles now have dedicated authenticated RPC/CLI/desktop save, replacement, identity validation and deletion, with explicit repository selection. Native generations and pending cleanup preserve exact retries and cancel/join original identity inspections; saved tokens never enter product reads or SQLite. Identity verification is separate from repository feature access. Authenticated official fine-grained/classic form prefills were inspected without issuing tokens; fine-grained Checks selection is unavailable and repository restriction requires explicit form selection. Revision-bound product forms and closed local browser opening now share Go validation with explicit repository/scope guidance. Ruleset/reviewer/remediation flows and real PAT access remain required full-issue work under [the integration contract](cmds-delidev-integrations-contract.md).

Repository-specific GitHub read-access inspection now spans RPC, CLI and desktop settings. It preserves independent endpoint states, current profile/generation and exact repository revision, with bounded joined cancellation and no saved observation. This does not complete PR/issue queries, ruleset/reviewer evaluation, remediation, official form or real PAT acceptance; follow the integration contract and validation records in pull requests, issues and CI logs/artifacts.

PR/issue list, repository-scoped plain search and detail now span RPC, CLI and desktop repository settings. Shared profile/generation/revision checks and joined cancellation protect every read; exact numeric namespaces, unknown mergeability and incomplete/capped search remain explicit. Immutable base/head PR diffs, latest head Checks and independent combined commit statuses retain their original provenance and unknown values, with before/after PR binding and scoped pagination. Durable zero-to-many PR associations now span project repositories through RPC/CLI and the session panel; stable provider identities survive Archive/restart and exact retries cannot recreate unlinked records. Actual public response-shape evidence is distinct from DeliDev PAT acceptance. Validated PR/issue addresses now offer explicit local native opening, with OS dispatch distinct from page access. Complete applicable active PR-base rules now span RPC/CLI/desktop with source/App provenance, exact-number original-policy digests and repeated-inventory/PR-change checks. Required CI inspection now matches active rules against complete GitHub rollups, PR-specific required flags, exact App identities and verified head/test-merge commits, with explicit pending/missing/unknown states. Complete published review bodies, code-thread comments and PR conversation comments now span RPC/CLI/desktop, with draft exclusion, repeated inventories and independent content versions that survive provider approval/dismissal. Reviewer inspection now independently resolves durable User/Bot IDs, current collaborator permission intervals and original per-comment App attribution, with a closed pure OR-selector matcher. Server remediation defaults and complete optional repository overrides now persist independent switches, exact OR reviewer selectors, Agent/machine selections, strategies and attempt limits through shared configuration RPC/CLI/desktop and portable remapping. Saving policy starts no work. Full merge-queue/workflow-rule/App-bound-status evaluation, durable feedback handling and remediation still require implementation.

Published PR feedback now has schema-v18 durable shared history and exact content-version local dismissal across RPC/CLI/desktop. Stable remote identity shares one inventory across local aliases and session links; original body/context/provider provenance survives edits, approval/dismissal, disappearance, head changes, restart and configuration deletion. Complete collection rechecks authorization and shared revision atomically; reference-only replay does not repeat network access. CI/conflict problems, handled/resolved transitions, Fix now and the actual bounded remediation controller remain unfinished, alongside the other issue requirements.

Schema-v19 PR history now includes required-CI failure versions with immutable shared complete rule/result proofs, plus independently collected verified conflict transitions. Original feedback and local decisions survive migration unchanged. Unknown/current applicability and original evidence remain separate; retries retain one shared stable PR owner and exact kind/request identity. RPC/CLI/desktop expose independent collection, original proof inspection and local Dismiss. Handled/resolved transitions, Fix now, fresh execution authorization, durable attempt chains and the remediation controller remain unfinished.

PR detail now retains original same-repository/fork source identities with explicit unavailable versus historical-unobserved state. Repeated reads bind this source alongside original refs/commits, and desktop detail displays exact original identity. A private non-secret Git-target snapshot establishes the inputs for future Worker preparation without granting Git authentication or execution. Public remediation/session selection and the direct-harness Git flow remain unfinished.

The Worker workspace engine now supports explicit PR-head detached preparation with exact source/base identities, canonical native Git transport validation, unchanged original refs/index/FETCH_HEAD and retained provenance through recovery/first-execution ownership. First execution rechecks the actual prepared workspace and current remote operands before native ownership. Its durable startup phase preserves positive pre-native rejection, blocks interrupted attempts and prevents the original job/execution from restarting after rejection. The public remediation controller does not yet issue this profile; reused-session preflight and isolated harness Git authentication/commit-push remain required. Explicit owner/client recovery can now reconcile a positive original rejection after Worker replacement using the immutable device/assignment and unchanged local phase; missing or native-eligible history stays uncertain. The live Worker now reports positive pre-native rejection against the immutable assignment; server acceptance retains a paused original input without native completion or resend, and internal attempt release preserves charged counts. Desktop disclosure validates the retained original proof. This does not complete the public controller or live acceptance.

PR remediation attempts now have owner/client RPC, CLI and desktop retained history plus explicit allowance resumption. Pagination binds every original attempt revision, survives profile deletion/restart and cannot infer completed Git work or handled evidence. Confirmed resumption preserves lifetime counters and all original records, refuses active/uncertain owners and never changes session pause/Archive or dispatches a job. Fix now, automatic controller/session selection, direct-harness Git authority and the broader issue scope remain unfinished.

Managed backup creation, inventory, integrity inspection and durable permanent image deletion are connected through owner/client Connect RPC, CLI and desktop Settings. Schema v23 retains ordinary jobs/receipts while immutable external deletion intents survive database rollback. This does not complete permanent session deletion, workspace snapshot/restore or managed database restoration; see the [storage contract](cmds-delidev-storage-contract.md).

Local desktop registration now has explicit owner-verified revoked-client recovery and a current-client revocation guard in the app. The CLI/native boundary retains the original revoked identity and private pairing history, uses a separate candidate and exact durable retry, and preserves server sessions/settings and independent Workers. It does not add automatic repair, remote saved-profile recovery, a new RPC or database migration; see the CLI and desktop contracts and validation records in pull requests, issues and CI logs/artifacts.

Local connection and registration permission guidance distinguishes device authorization, ownership and owner-only filesystem access, including macOS/Linux private modes. Permission failures never establish revocation or authorize automatic repair; existing data remains preserved under the desktop contract.


Manual PR Fix now uses generated `PullRequestFixQuery`, gated by the typed Codex Git capability when its explicit form opens. The user selects a current project, whose critical selector receives focus once when the form opens; asynchronous inventory/capability updates do not steal later keyboard focus. Each request retains exact decimal set/problem revisions and original content version. The connection-scoped mutation registry shares one stable PR key across rows/views and preserves the exact original wire request through uncertainty, navigation and malformed acknowledgments, including late responses after unmount. Only validated original attempt/session/set acknowledgments release that request; retries replay the original receipt. Paused/archived/recovering sessions are not resumed implicitly. Accepted work remains visibly unhandled until independently verified push proof is published. Handled rows display the original attempt ID, execution ID, verified pushed commit and server transaction handling time, and offer no Dismiss or new Fix action. The handling time remains separate from the original problem observation timestamp. Capability reads are disposed with the closed form; project choices use the ordinary configuration inventory. See the integration contract for supported profiles and separate real-native acceptance limits.

### Original Grok tools, questions and Plan controls

For the pinned public first-input profile in the harness contract, the session displays each immutable original tool observation in publication order, including streamed arguments, original descriptor, pending/resolved stages, preview/applied results, current native mode, remembered Write provenance and original Plan-file revision. Inert details never initiate filesystem access or execution. Malformed, mixed or foreign retained records display unavailable state without response controls. Request observations also retain the original `proposal_json` string as inert evidence within the existing 512 KiB complete-event bound. Validate present values as nonempty, NUL-free, well-formed UTF-8 text before rendering; notifications cannot carry proposal bytes and unrelated event keys remain invalid. Historical records may omit these bytes. Go independently checks the original payload and exact proposal digest before accepting any new response; frontend disclosure cannot reconstruct or grant that authority.

Session and Inbox share the original `grok` interaction view and existing retained mutation mechanism. Treat a server-produced null generic `questions` field as absent. Preserve the native 32-question/64-option, 16 KiB question/description, 4 KiB label and 256 KiB aggregate limits rather than narrowing valid requests. Numeric request identity uses its exact decimal string, including values outside signed int64 and `-0`. The single Write/Plan decision selector receives focus when its form appears. Native questions expose exact original question keys/options, answers and notes, explicit cancellation that continues input, and explicitly selected partial answers for interview skipping. Original Write offers once, current native session edits and rejection. Native Plan shows original proposal content and revision with approve/cancel/abandon semantics, without a common synthetic Plan gate. Changing a proposal invalidates an older draft through original arrival/proposal identity. Keep current actor/execution authority, exact interaction revision and request UUID; explicit uncertain receipt retries reuse the same mutation and wire bytes rather than creating another response.

Delivery remains visibly unconfirmed until the original native resolution/result accepts it. Original tool result, native execution outcome and workspace cleanup remain independently presented. `closed-first-tools` displays exact reported decimal counters and separate cleanup state without continuation or first-text history claims. Existing first-text and user-history rendering remain separate, and mixed families are unavailable. The CLI uses the same server-owned interaction/response union. This implementation does not claim native Read deny support where Grok emits no client request, richer native Stop terminal proof, repository/continuation or complete issue #964 acceptance.

### PR handling in Activity

The existing Activity page displays immutable problem observation/dismissal, attempt-state and dedicated verified-handled metadata with the original PR, actor, version and source references. Failure, uncertainty and successful attempts remain distinct; success never implies handling. The original source disclosure uses two independent disposable authenticated ResourceQuery reads only after explicit inspection. It checks the retained set/source/version scope, labels recorded versus current revisions and exposes no collection, dismissal, resumption or execution control. Closing or leaving Activity disposes the reads; returning requires a new explicit inspection. The manual fix verifier now supplies dedicated verification creation only after original push and cleanup proof. Timeline fixtures do not establish real-account/native remediation acceptance.

## Codex Fork presentation (#1092)

The completed-session action is gated by `CODEX_SESSION_FORK_V1` and the absence
of retained fork-origin metadata. A completed child remains ineligible for another
fork in the initial root-only native profile. A mounted
connection controller retains its name/workspace draft, exact uncertain request
and accepted job through conversation navigation; Escape hides the modal without
losing that operation. The name input receives focus. A changed source revision
requires discarding the fresh draft and inspecting the new boundary. Default
workspace copying is independent; offer explicit Local sharing only for a Local
source with fresh same-machine Worker proof. Managed Worktree sources retain only
the independent workspace choice, preserving the child after parent deletion. Poll `GetSessionFork` only by the accepted job ID, stop automatic
polling on terminal/uncertain state and offer explicit refresh. Open the child
only after verified publication. The [fork contract](cmds-delidev-forks-contract.md)
keeps Go ownership and current eligibility authoritative. Component tests do not
establish native desktop or other-platform acceptance.

## Independent subscription identity composition

AI Subscription uses System capability 17 and schema-v2 service-native accounts without searches or Provider-dependent requests. `subscription-accounts.tsx` owns explicit service-only creation, default-off recovery notifications and existing managed Codex login/cancel/authentication-refresh/logout; original request/revision, native owner and Settings lifetime remain authoritative. Native Models retain a closed service/matching harness independently of API Providers. Reconfiguration-required Agents need explicit current-model/account reset before routing; retired original documents are read-only historical attribution. Portable UI accepts v2 and API-only v1 without reserializing original bytes. Price, usage and request diagnostics preserve independent service attribution. Follow [AI Subscription settings](apps-delidev-subscription-settings-contract.md) and [managed subscriptions](cmds-delidev-subscription-contract.md); these interfaces do not establish real-account or platform acceptance.

### Network routing settings

Server preferences and each Runner Device inspection open a Network settings task dialog under the existing categories. Use authenticated generated Network/Resource queries, exact revision selection and explicit uncertainty controls for profile writes/deletion. Preserve write-only proxy credentials, profile pagination and the distinct desired/effective/native route states from the [network contract](cmds-delidev-network-contract.md). A current control generation never implies native use, inference or account readiness.

Encrypted Worker export starts from its bounded original public recipient and separately displays authenticated ciphertext digest. Ciphertext is transient presentation, not persistent query state. Prepare/Import/Status on this computer reaches a closed trusted-window bridge for only its already registered matching Worker; Go performs protected storage and cryptography. Other Runner Devices use their equivalent CLI. Preparation/import does not register, start, stop or replace a Worker. All asynchronous file/native/RPC results belong to the current Settings opening; leaving disposes local presentation without replay or implicit native cancellation.

### Codex child configuration

Native harness options capability-gate Codex child configuration with System capability 22 and original Runner Device capability 11. Preserve omitted model/effort/concurrency defaults and disabled saved values on older servers. Explicit settings use the exact registered native model under the parent's selected account and are frozen before execution; native compatibility is checked before input. Display the saved canonical child identity/revision separately from native observations and requested settings. The existing configuration RPC, CLI and generated client remain the product write boundary; reading or editing these fields never launches or controls a child.

OpenCode 1.18.32 foreground children use the shared read-only Subagents disclosure under independent capability 23. Validate its complete original task/child graph and closed exact response counters before rendering a bounded page; native task content remains inert. No child controls are added. Follow the subagent contract.

Session Context uses authenticated Connect queries for native observations and
current per-profile compaction eligibility. Bind the observed exact session
revision before accepting the action, retain the original mutation across
navigation/response loss, and poll context resources without replaying a native
command. Show unavailable counters explicitly, preserve uncertain action guidance
and require independent server/Worker capabilities. The control remains outside
Settings and does not alter ordinary conversation outcomes or queued input.

Original OpenCode automatic compaction progress uses the same bounded inert
context disclosure as Codex, retaining its own harness and native part reference.
Show started/completed independently of current context tokens, which remain
unavailable when unreported. Native summaries and continuation users do not
replace the canonical conversation. The private OpenCode manual controller does
not enable the desktop action before independently negotiated product support.

OpenCode General Chat Fork additionally requires independent System 26/Worker 15 and a macOS/Linux Runner Device. Offer only completed Build plain-text root boundaries without children, context actions or native workspace activity; preserve exact mutation replay and the retained operation across navigation. Present inherited conversation/files and paused-child Resume in product terms; preparation cannot claim native model selection or new usage.

Server-owned subscription onboarding follows [AI Subscription settings](apps-delidev-subscription-settings-contract.md) and [managed subscriptions](cmds-delidev-subscription-contract.md#server-browser-login-and-account-naming). Add starts ordinary browser login without Runner Device/code controls. Trusted native local/remote callback ownership, independent capability 30, transient success-bound naming and departure without business cancellation are cross-domain invariants. Preserve the shared Settings tokens, 720px form, 40px controls, 8px corners and narrow stacked actions.

Shared Tauri CLI installation follows [prebuilt dependencies](repository-prebuilt-dependencies-contract.md). Desktop selection uses the direct `tauri-runtime-cef` dependency and runtime-owned entry point; mobile DevHud uses the direct `tauri-runtime-wry` dependency. macOS/Linux require Chromium sandboxing. Windows permits the owner-approved unsandboxed Chromium exception until executable-host broker support is available upstream. Linux GTK initialization uses GTK4; file dialogs use XDG portal/Tokio and confirmation dialogs use `zenity`. IPC restrictions, private profile ownership and shutdown-before-purge rules are retained.

### Native sidecar executable discovery

The cleared sidecar environment retains its existing narrow OS allowlist. Its PATH begins with OS utility defaults, then at most 64 absolute inherited entries from a bounded 32 KiB PATH, with empty/relative entries and duplicates removed. macOS also includes `/opt/homebrew/bin` and `/usr/local/bin`; Linux includes `/usr/local/bin`. Windows deduplicates case-insensitively and uses the trusted OS root utility paths. No shell startup files run and no additional credential, proxy or configuration variables are inherited. This lookup permits discovery only; the chosen executable identity, version and native protocol still require Go verification.
