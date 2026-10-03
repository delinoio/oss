# DeliDev AI Subscription Settings

## Scope

Issue #1143 owns the compact AI Subscription presentation; #1235 composes its independent service identity in `subscription-accounts.tsx`, `subscription-settings.tsx` and `subscription-catalog.ts`. The shared Settings application screen, 17 categories, stable `subscription-accounts` category ID and short AI Subscription label remain authoritative under [the desktop contract](apps-delidev-desktop-contract.md). The category description is “Manage your subscriptions and connect more accounts.” Managed Codex login/cancel/refresh/logout uses the existing authenticated SubscriptionService and original Worker credential owner. Native quota and reset-credit operations belong to #1096/#1104; unsupported Claude/Grok lifecycle remains explicit. Account preferences, native authentication, quota observation and real-account/platform acceptance remain independent.

## Runtime and Language

React/TypeScript presentation follows the shared issue #1256 Settings body contract: system font, semantic light/dark tokens, left-aligned 1040px maximum column, 32px/24px/compact padding, 40px controls and 8px corners. Flat divided rows preserve the 40px provider mark, alias, separate connection state, up to two ordered quota windows and Refresh/Disconnect/ellipsis controls. Your subscriptions precedes Connect a subscription and the initially collapsed Advanced settings disclosure. Successful empty inventory uses the shared 160px-minimum horizontal icon/help region. Container queries retain wrapping identity/actions/quotas; service choices are flat ChatGPT/For Codex, Claude/For Claude Code and Grok/For Grok Build rows. Negotiated service-account support enables explicit Add account; unsupported servers retain disabled Coming soon controls. Shared context-pane/drawer navigation and all original lifecycle/metadata guards remain intact.

## Users and Operators

DeliDev users manage saved subscription account metadata on their selected server. Server operators own account authorization and supported lifecycle capabilities. The selected service, negotiated capability and original native profile establish each operation independently; account preference creation alone establishes no login or quota authority.

## Interfaces and Contracts

The frontend catalog uses typed ChatGPT / For Codex, Claude / For Claude Code and Grok / For Grok Build descriptors in that order. Local SVG marks come from the pinned MIT LobeHub source recorded beside the assets. Explicit server-owned `subscription_service` chooses the brand; editable alias/provider names cannot. No masked identity is inferred from the credential commitment or editable metadata. Presentation fixtures may supply independently attributed masked example identities.

The production controller negotiates System `SUBSCRIPTION_SERVICE_ACCOUNTS_V1 = 17` and uses generated Connect Query `ListResources` with the exact subscription account type before pagination. It never filters fetched pages locally. AI Subscription has no search, Provider filter, Provider inventory/preset/model discovery request or native Provider creation. Account creation writes schema v2 with one closed service and no `provider_id`, starts disconnected and defaults recovery notifications off. Advanced explains preferences, historical retirement and configured-empty deny-all restrictions. Preferences and confirmed deletion retain accessible ellipsis ownership and exact metadata retries. Account details retain additional quota windows, health, enablement, independent service attribution and exhaustion. Unknown, mixed or malformed account pages are unavailable as a whole.

Codex management requires explicit account and Runner Device selection. Login, authentication refresh and logout retain the original UUID-v7 request and exact account revision; uncertain results permit only the owning original request retry. Authentication refresh cannot be presented as quota refresh. Logout confirms the exact inspected account revision and preserves preferences/history while Go cancels execution and waits for original native cleanup. Login cancellation is explicit, tied to the retained operation, and does not start another login. Only that opening's accepted login can display its short-lived URL/code; progress is polled while active and cleared on cancellation, authority loss or read failure. HTTPS official login authority is independently bounded before presentation. No protected Worker bundle is requested by the renderer. Claude/Grok native login remains unavailable in this profile.

A capability-read error has an explicit retry and is never classified as planned support. Initial loading, successful empty, retained-data failure, permission denial, expired authentication and unsupported service-account capability remain distinct. API validation, provider enablement and model discovery never grant subscription lifecycle authority.

`SubscriptionSettingsView` is a pure frontend presentation seam. Supported fixtures can supply exact-account callbacks and independently owned operation state. Refresh all calls one supplied server-wide callback, with no frontend page traversal, provider aggregation or discovery request. Fixtures render independent account authentication/quota outcomes. Disconnect first confirms the exact account and describes preserved metadata/history, then calls its owning callback. Busy blocks another operation; uncertain and cleanup-pending states expose only an owning original-operation retry callback and cannot issue a new request identity. Future production adapters must prove generated capabilities, current authorization and exact native profile before supplying these callbacks.

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
