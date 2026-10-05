# DeliDev AI Subscription Settings

## Scope

Issue #1143 owns the compact AI Subscription presentation; #1235 composes its independent service identity in `subscription-accounts.tsx`, `subscription-settings.tsx` and `subscription-catalog.ts`. The shared Settings application screen, 17 categories, stable `subscription-accounts` category ID and short AI Subscription label remain authoritative under [the desktop contract](apps-delidev-desktop-contract.md). The category description is “Manage your subscriptions and connect more accounts.” Managed Codex login/cancel/refresh/logout uses authenticated SubscriptionService and the independent server credential owner. Native quota and reset-credit operations belong to #1096/#1104; unsupported Claude/Grok lifecycle remains explicit. Account preferences, native authentication, quota observation and real-account/platform acceptance remain independent.

## Runtime and Language

React/TypeScript presentation follows the shared issue #1256 Settings body contract: system font, semantic light/dark tokens, left-aligned 1040px maximum column, 32px/24px/compact padding, 40px controls and 8px corners. Flat divided rows preserve the 40px provider mark, alias, separate connection state, up to two ordered quota windows and Refresh/Disconnect/ellipsis controls. Your subscriptions precedes Connect a subscription and the initially collapsed Advanced settings disclosure. Successful empty inventory uses the shared 160px-minimum horizontal icon/help region. Container queries retain wrapping identity/actions/quotas; service choices are flat ChatGPT/For Codex, Claude/For Claude Code and Grok/For Grok Build rows. Negotiated service-account support enables explicit Add account; unsupported servers retain disabled Coming soon controls. Shared context-pane/drawer navigation and all original lifecycle/metadata guards remain intact.

## Users and Operators

DeliDev users manage saved subscription account metadata on their selected server. Server operators own account authorization and supported lifecycle capabilities. The selected service, negotiated capability and original native profile establish each operation independently; account preference creation alone establishes no login or quota authority.

## Interfaces and Contracts

The frontend catalog uses typed ChatGPT / For Codex, Claude / For Claude Code and Grok / For Grok Build descriptors in that order. Local SVG marks come from the pinned MIT LobeHub source recorded beside the assets. Explicit server-owned `subscription_service` chooses the brand; editable alias/provider names cannot. No masked identity is inferred from the credential commitment or editable metadata. Presentation fixtures may supply independently attributed masked example identities.

The production controller negotiates System `SUBSCRIPTION_SERVICE_ACCOUNTS_V1 = 17` and uses generated Connect Query `ListResources` with the exact subscription account type before pagination. It never filters fetched pages locally. AI Subscription has no search, Provider filter, Provider inventory/preset/model discovery request or native Provider creation. Account creation writes schema v2 with one closed service and no `provider_id`, starts disconnected and defaults recovery notifications off. Advanced explains preferences, historical retirement and configured-empty deny-all restrictions. Preferences and confirmed deletion retain accessible ellipsis ownership and exact metadata retries. Account details retain additional quota windows, health, enablement, independent service attribution and exhaustion. Unknown, mixed or malformed account pages are unavailable as a whole.

Add account requires inventory capability 17, independent server-login capability 30 and the trusted desktop native browser control. One deliberate ChatGPT action creates an account named ChatGPT and immediately requests ordinary browser login with omitted machine selection. Duplicate clicks, Strict Mode, status polling and reconnect cannot start another account or login. Claude Code and Grok retain explicit Coming soon/unsupported guidance and cannot create an apparently supported login. Existing disconnected ChatGPT accounts can start the same flow deliberately. Authentication refresh and logout use the server lane without a Runner Device; logout confirms the exact current revision. Existing quota controls keep their original Worker authority and never infer an owner from server login.

Only the original operation's typed succeeded status and matching freshly read connection/generation permit Account name. Prefill once from valid email, provided display name or the service name; subsequent polling never overwrites edits. Name entry focuses once. Save account name validates the existing 256-byte UTF-8 alias bound, reads the current revision and patches only the alias JSON token so protected uint64 lease revisions remain exact. A revision conflict retains the draft for another explicit save; uncertain mutations retain their exact request identity/bytes. Save returns to inventory. Later, Back and application navigation retain the default-name account and accepted login without implicit cancellation.

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

## Storage

All state belongs to the current Settings visit. Filters, details, Advanced state, drafts, confirmations and retry presentation survive category changes within that visit and are discarded on navigation away under #1138. Accepted server/Worker effects continue; visit disposal guards late continuations and leaves sibling QueryClient workflows and session drafts intact.

## Security

No credentials, login URLs/codes, identity fixtures or quota values enter query keys, logs, browser storage or analytics. Only active original login/status reads poll; Settings disposal drops scoped presentation and never cancels or repeats accepted native work. Settings itself has no modal focus or Escape-to-leave behavior. Actual child dialogs and the shared compact navigation drawer retain their own focus containment, Escape and opener restoration; ellipsis/confirmation Escape is handled locally; icon actions have accessible labels.

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
