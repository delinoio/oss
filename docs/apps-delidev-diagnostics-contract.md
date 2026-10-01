# DeliDev Diagnostics Presentation

## Scope

Issue #1144's approved v2 layout reorganizes Settings Diagnostics for the single server owner and authorized paired clients. `apps/delidev/src/doctor.tsx` and `doctor.css` own presentation of the existing read-only report. The [desktop contract](apps-delidev-desktop-contract.md) retains modal ownership; the [diagnostics contract](cmds-delidev-diagnostics-contract.md) retains observations, authorization and report bounds.

## Runtime and Language

React/TypeScript renders the existing authenticated Connect Query observation in the desktop webview. Rsbuild owns frontend compilation; the native host and Go service are unchanged.

## Users and Operators

The single server owner and authorized paired desktop clients inspect the selected server. Worker and account records retain independent evidence limits.

## Interfaces and Contracts

### Hierarchy and visible evidence

Doctor owns one level-1 Diagnostics title, the original scope paragraph, right-aligned Refresh diagnostics action and subordinate Observed at row with the exact server-returned UTC string. The outer category heading is suppressed only for Diagnostics; accessible category announcement remains. Three independent cards appear in order: Database read check, Owner credential and Inference probes. Retain known/unknown labels, positive read-success, informational owner-credential and neutral not-performed treatments, with text/icon semantics and no overall health score.

Issue #1137 renames the existing Settings category to Connection & diagnostics without changing its stable ID. Settings passes that closed title to Doctor's existing header, retaining one category title; standalone Doctor keeps Diagnostics. A separately labelled Connection subsection opens the persistent native controls outside SettingsLifetime. Selecting the category still reads only Doctor; opening, hiding or disposing Settings cannot discard the connection panel's original lifecycle/registration requests and confirmations. Doctor's report, refresh and disclosure lifecycle remain independently read-only.

Server information aligns version, OS/architecture, protocol version, database schema and bound endpoint in semantic label/value rows. Server identity is a closed native disclosure. Keep the owner-credential caveat visible.

Server storage shows the exact result classification, safe code/guidance and all five byte fields: database file, write-ahead log, logical database size, filesystem capacity and space available to the server. Keep the separately-sampled/no-sum/no-reclaimable-space caveat visible. Retained resources contains bounded resource kind/count rows in a closed disclosure; unavailable/empty notices remain outside it. Partial storage measurements survive later measurement failures.

Runner Devices occupies full width. Each always-open record shows its name, reported version/platform, observed stream state, enabled/disabled state, last contact and every retained harness installation/version/protocol classification within the existing four-entry bound. Failure codes and guidance stay inside the owning record and outside disclosures. Installation details contains machine identity, discovery timestamps and reported capabilities. The full original description preserves discovery, handshake, account-readiness and process-cleanup distinctions. Machine inventory availability and completeness notices stay visible.

Protected account storage occupies full width. Each record shows account identity, exact result classification, safe code and guidance. Connection identity has its own closed disclosure. Retain exact connection-reference scope, superseded classification, availability/completeness notices and the distinction from provider authentication, execution readiness and quota. Preserve the original final guidance to AI accounts and Runner Devices. Keep the user-facing Runner Device heading/help separate from stable backend/category identifiers.

### State and compatibility

Keep one active-gated `SystemQuery.getDoctor`, explicit refresh and existing query defaults/bounds. Disclosures change visibility only: no RPC, discovery, login, repair, inference, native operation or mutation. Loading announces Reading server diagnostics and disables refresh. Refresh retains prior data/time; failure exposes the safe Problem/correlation surface and labels the last returned observation. Initial denial/failure cannot establish a successful summary or empty inventory.

Strict UTF-8/1 MiB and legacy/future/malformed handling remain unchanged. Legacy reports show original fields and the capacity/protected-storage limitation; unsupported/malformed reports establish no health. Unknown field-level enums remain Unknown/unavailable without adding the first-session checklist's stronger whole-report validator. Missing versus empty inventories and true/false/unknown completeness remain distinct, with at most 50 machine/account records rendered.

Validate canonical decimal-string uint64 values and format through BigInt. Preserve measured zero and exact large values; missing/null/numeric/noncanonical/overflow bytes are Unavailable, invalid resource counts Unknown. No floating-point conversion, sums, ratios, percentages or reclaimable estimates.

Native `details/summary` controls are keyboard operable with visible focus. State follows server identity, machine identity and account/connection identity, never array position. Missing identities receive response-scoped keys; changed identities and another connection cannot inherit open state. Category navigation and responsive changes retain state within one opening. Pass actual Settings visibility to the local reset boundary; inactivity alone is not close. Actual Close, Escape or navigation-driven hiding clears all disclosures; the merged issue #1138 opening disposal is authoritative and ordinary reopening starts at AI Subscription. Closing disclosures saves/replays nothing and introduces no server/native cancellation. State remains memory-only.

### Styling and responsive behavior

Use existing system fonts, white content `#ffffff`, text `#202632`, muted `#5b6577`, border `#d8dee8` and accent `#2563d8`. Panels use 1px borders, 12px corners, 24px padding and 20px gaps; controls are at least 40px high. Exact numeric values use tabular numerals and long values wrap. All CSS is statically scoped to Diagnostics; no assets, fonts, dependency, inline styles or CSP relaxation.

Summary cards use three columns at CSS viewport widths of at least 1100px, one below. Server/storage panels use two columns at least 1200px, one below. Preserve Settings padding, independent vertical scrolling, fixed header, 240px sidebar and compact category select below 760px. Validate 1920×1080, 1440×900, 960×640, 640×480 and 200% zoom for overflow, focus and reachable controls; native window minimum geometry is unchanged.

## Storage

Disclosures are native memory-only presentation state within the original connection and Settings opening. There is no Web Storage, persisted settings, file output or new query cache. Exact server-returned values remain observations under the existing query owner.

## Security

Render every name, identity, endpoint, capability and guidance as inert English text. Retain strict decoding and safe Problem/correlation handling. Disclosures cannot access credentials or authorize an operation, and styling cannot relax CSP.

## Logging

Preserve existing service operational logs. This presentation adds no logging of report content, identities or endpoints.

## Build and Test

Run `pnpm test` in `apps/delidev`, including `doctor.test.tsx`, existing device diagnostics fixtures and isolated temporary Go server integration. Fixtures cover hierarchy/all fields, failure ownership, canonical integers, query/lifecycle/disclosure identities, deferred refresh, permissions/correlation, compatibility/inventory bounds and inert HTML-like text. Browser geometry and keyboard smoke, actual zoom and packaged platform/CEF acceptance are independent evidence. Record commands, revision and unavailable checks in pull requests, issues and CI logs/artifacts. Prepare generated/LFS inputs as needed and remove generated repository-owned `dist` after validation.

## Dependencies and Integrations

Reuse React, Connect Query, the existing shared QueryClient and native `details/summary`; there is no new package, font or external asset. Settings retains authoritative category, opening, modal and connection ownership.

## Change Triggers

No API/schema/generated binding, backend, persistence, authorization, credential access, migration, feature flag, dependency or release/deployment behavior changes. Do not log report content, identity or endpoint. Preserve AI Subscription, Backups, modal focus containment, opener restoration, navigation locks and underlying session/composer behavior.

Update this presentation contract, the desktop/diagnostics links and scoped frontend AGENTS when hierarchy, lifecycle, bounds or styling guarantees change. Update the project index for domain ownership/catalog changes and record validation independently.

## References

- [DeliDev project](project-delidev.md)
- [Desktop client](apps-delidev-desktop-contract.md)
- [Read-only diagnostics](cmds-delidev-diagnostics-contract.md)
- [Repository defaults](repository-defaults.md)
- [Issue #1144](https://github.com/delinoio/oss/issues/1144)
