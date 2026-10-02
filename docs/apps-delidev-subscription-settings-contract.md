# DeliDev AI Subscription Settings

## Scope

Issue #1143 owns the UI-first AI Subscription redesign in `apps/delidev/src/account-settings.tsx`, `subscription-settings.tsx` and `subscription-catalog.ts`. The shared Settings application screen, 17 categories, stable `subscription-accounts` category ID and short AI Subscription label remain authoritative under [the desktop contract](apps-delidev-desktop-contract.md). The category description is “Manage your subscriptions and connect more accounts.” Native Codex lifecycle/quota work belongs to #1095/#1096; Claude requires independently verified lifecycle support. This stage does not implement those adapters or a new RPC/schema, migration, dependency, feature flag, polling or persistent frontend state.

## Runtime and Language

React/TypeScript presentation follows the shared issue #1256 Settings body contract: system font, semantic light/dark tokens, left-aligned 1040px maximum column, 32px/24px/compact padding, 40px controls and 8px corners. Flat divided rows preserve the 40px provider mark, alias, separate connection state, up to two ordered quota windows and Refresh/Disconnect/ellipsis controls. Your subscriptions precedes Connect a subscription and the initially collapsed Advanced settings disclosure. Successful empty inventory uses the shared 160px-minimum horizontal icon/help region. Container queries retain wrapping identity/actions/quotas; planned providers are flat ChatGPT/For Codex, Claude/For Claude Code and Grok/For Grok Build rows with disabled Coming soon controls. Shared context-pane/drawer navigation and all original lifecycle/metadata guards remain intact.

## Users and Operators

DeliDev users manage saved subscription account metadata on their selected server. Server operators own account authorization and supported lifecycle capabilities. Future native-provider adapters must establish that authority before the frontend exposes live operations.

## Interfaces and Contracts

The frontend catalog uses typed ChatGPT / For Codex, Claude / For Claude Code and Grok / For Grok Build descriptors in that order. All are currently planned with disabled Coming soon controls; Grok has no schedule or waitlist. Local SVG marks come from the pinned MIT LobeHub source recorded beside the assets. Existing accounts have no explicit native brand or masked identity fields in the generated server contract: production rows use generic marks and omit masked identity, regardless of editable names. Presentation fixtures may supply explicit branded/masked example identities independently of wire resources.

The production controller continues using generated Connect Query `ListResources` with the exact subscription account type and provider ID before pagination. It never filters fetched pages locally. Cursors remain scoped to account type/provider; pagination appears only for a current or next page. Advanced preserves search, provider filters and their bounded server pages, disconnected account metadata creation and provider configuration. An applied provider filter and Clear action remain visible outside Advanced. API inventory excludes native subscription providers, so their configuration metadata comes from a separate bounded generated `ListResources` provider page, with its own original cursor, first/next controls and retry. Search narrows native provider choices within that bounded metadata page; account filtering remains server-side. This read is not a fallback for account filtering or lifecycle eligibility. Native metadata page navigation stays locked while a metadata draft or operation owns the workflow. Preferences and existing confirmed deletion are reachable from the accessible ellipsis disclosure, and account details retains additional windows, health, enablement, provider state and exhaustion. Existing metadata creates retain byte-identical uncertain retries.

Production supplies no subscription lifecycle callbacks because the generated server contract does not advertise verified native connection, quota-refresh or disconnection operations. Provider enablement, API validation, list refresh and model discovery cannot grant that authority. The shared account-detail screen also excludes API lifecycle controls for subscription accounts, including pending cleanup. A capability-read error has an explicit retry and is never classified as planned support. Initial loading, successful empty, retained-data failure, permission denial, expired authentication and unsupported account-type filtering remain distinct.

`SubscriptionSettingsView` is a pure frontend presentation seam. Supported fixtures can supply exact-account callbacks and independently owned operation state. Refresh all calls one supplied server-wide callback, with no frontend page traversal, provider aggregation or discovery request. Fixtures render independent account authentication/quota outcomes. Disconnect first confirms the exact account and describes preserved metadata/history, then calls its owning callback. Busy blocks another operation; uncertain and cleanup-pending states expose only an owning original-operation retry callback and cannot issue a new request identity. Future production adapters must prove generated capabilities, current authorization and exact native profile before supplying these callbacks.

Each window preserves its ID, order, remaining fraction, observation time and reset time. Finite fractions within [0, 1] become percentages and native progress elements with text equivalents; zero is observed zero rather than unknown. Missing/invalid values and future observation times have no valid bar. Observations older than five minutes, future observation times or elapsed resets are stale. Unknown, stale, failed and unsupported remain distinct; retained last-success values can remain visible with stale/failed text. An elapsed reset never implies recovery. No provider/window values are pooled. Refresh failure presentation retains the original successful windows/time supplied by its owner. An active surface schedules one presentation-only expiry at the next known freshness/reset boundary, without network requests; hidden/disposed surfaces clear it.

## Storage

All state belongs to the current Settings visit. Filters, details, Advanced state, drafts, confirmations and retry presentation survive category changes within that visit and are discarded on navigation away under #1138. Accepted server/Worker effects continue; visit disposal guards late continuations and leaves sibling QueryClient workflows and session drafts intact.

## Security

No credentials, identity fixtures or quota values enter query keys, logs, browser storage, analytics or background polling. Settings itself has no modal focus or Escape-to-leave behavior. Actual child dialogs and the shared compact navigation drawer retain their own focus containment, Escape and opener restoration; ellipsis/confirmation Escape is handled locally; icon actions have accessible labels.

## Logging

Read and mutation failures use the existing typed, redacted transport diagnostics and Problem presentation. This UI stage adds no provider telemetry or log stream. Operational troubleshooting must retain stable action/error classifications without recording credentials, masked identities, quota values or original request payloads.

## Build and Test

Run `pnpm test` from `apps/delidev`, after generating required client inputs and hydrating consumed LFS assets. Presentation quota/callback/state fixtures, generated router/account regression fixtures, Settings disposal checks and the feature-specific temporary Go-server integration cover the UI boundary. Bundle original mark notices with frontend output; remove generated `dist` directories from the final worktree. Record browser and native visual/keyboard evidence separately in pull requests, issues and CI logs/artifacts. Component/browser fixture success establishes no real-provider login, native quota collection or native platform acceptance.

## Dependencies and Integrations

Use the existing React/TypeScript, Rsbuild, React Query and generated Connect Query workspace dependencies without adding packages. Integrate with the existing Account resource, provider inventory, bounded provider metadata reads and Settings-local mutation registry. Native provider lifecycle/quota follow-ups remain separate from presentation fixtures and require generated capabilities/operations.

## Change Triggers

Update this contract, the desktop contract, project index and owning frontend AGENTS rules when hierarchy, lifetime, identity, quota or action-authority boundaries change. Keep the docs catalog current for ownership changes. Imported mark changes require pinned source/modification notices and the repository license contract to stay synchronized. A future wire change must update its protocol and API-client contracts alongside generated sources.

## References

- [Project index](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Credentials](cmds-delidev-credentials-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [API client](packages-delidev-api-client-contract.md)
