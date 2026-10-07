The current pre-release reset implements Model-based Agent schemas 1/3 and portable bundle v2. Inline Worker schema 4, bundle v4, API account protocol selection and direct execution startup remain reserved future features; their declarations grant no runtime support. Activate their complete replacements separately, reconcile the active database baseline before implementation, and preserve current files and native/credential cleanup authority.

# Project: DeliDev

API account protocol selection reserves ProviderInventory capability 7 and its complete profile/filter declarations under issue #964 before implementation. The [catalog contract](cmds-delidev-catalog-contract.md#api-account-protocol-reservations) owns this prerequisite; it adds no runtime or database authority.

Main desktop local Workers now have automatic same-owner registration/start and native supervision, with durable same-process manual Stop and original-child-only normal Quit. Existing CLI/service and saved-connection Workers retain independent ownership. Server connection, controller presence, account/harness eligibility and session cleanup remain independent; no protocol allocation or migration is required. The desktop, CLI and current-user service contracts define this boundary.

Failed-login subscription cleanup reserves System capability 41 and its closed batch/status/result declarations under issue #964. The [subscription contract](cmds-delidev-subscription-contract.md#failed-subscription-cleanup-reservations) and [protocol contract](protos-delidev-v1-contract.md#failed-subscription-cleanup-reservations) require the complete main-first reservation before implementation. This prerequisite grants no cleanup or deletion authority and adds no migration.

## Goal
Run personal AI sessions across projects, accounts, native harnesses, and execution machines with durable single-user ownership. Issue #964 remains normative, with the explicit owner startup/presentation amendment in #1137; implementation and real-environment evidence are distinct.

Issue #1137 makes a fresh main desktop launch sufficient to start/reuse a compatible ordinary local runtime and verify the authenticated product connection. Native-service scope admission remains Go-owned; same-process Stop, renderer lifecycle, saved-window authority and independent server/Worker/session ownership stay separate. Normal Quit stops only original app-owned server children, with a 35-second graceful deadline followed by original-child force and observed exit; crash/forced desktop termination preserves running sidecars. Borrowed runtimes and independent Workers remain running. Routine startup/sidebar/tray use product wording; lifecycle, registration and saved-connection controls live in persistent Connection & diagnostics. The [desktop contract](apps-delidev-desktop-contract.md) defines the implementation; record actual platform acceptance and unresolved limits in issue #1137, its pull requests and CI logs/artifacts.

Issue #1088 adds Worker-owned session terminals with native Unix PTY/Windows ConPTY processes, authenticated create/control/output operations and equivalent CLI commands. The desktop provides a bounded text terminal view. Agent Stop preserves terminals; Archive and storage deletion join their independent exact cleanup gate. The [terminal contract](cmds-delidev-terminals-contract.md) and [validation records in PR #1226](https://github.com/delinoio/oss/pull/1226) distinguish fixture/cross-build validation from native platform, remote Worker and release acceptance; this increment does not complete the remaining issue #964 scope.

Codex native flows use a common minimum SemVer `0.151.0` with no upper bound under the [harness contract](cmds-delidev-harness-contract.md). Preserve actual immutable executable/version attribution and independently verify native protocols and account authority. The [desktop contract](apps-delidev-desktop-contract.md) defines bounded sidecar lookup, and the [subscription Settings contract](apps-delidev-subscription-settings-contract.md) defines safe original-operation diagnostics. Schema allocations reach main before activation; optional document metadata adds no migration. Record fixture/build/native initialization and real account/platform evidence separately in pull requests and CI.

## Project ID
`delidev`; the Go component is `delidev-cli` and its executable is `delidev`.

Repositories use required remote URLs with optional Local folder connections under the [workspace contract](cmds-delidev-workspace-contract.md). System 37 and Worker 19 activate only after main reservations in PR #1377. Desktop registration, CLI/configuration transfer and session/schedule preparation share this source authority; independent managed clones preserve Fork, Sidechat and storage/deletion lifetimes without rewriting accepted history.

## Domain Ownership Map
- `apps/delidev`: desktop presentation and native host.
- `cmds/delidev-cli`: Go CLI, server, Worker, storage and native adapters.
- `protos/delidev/v1`: versioned Connect schema; `protos/gen/go/delidev/v1`: generated Go bindings.
- `packages/delidev-api-client`: generated TypeScript client and bounded transport/synchronization helpers.

The [desktop File menu](apps-delidev-desktop-contract.md#native-tray-and-menu-bar) opens a new product window with Command+T on macOS and Control+T on Windows/Linux through one native app-level handler. Close Window retains Command/Control+W and existing Local/Saved window ownership.

New schedule creation adds frequency presets and a creation-only three-section layout under the [desktop contract](apps-delidev-desktop-contract.md#new-schedule-creation-issue-1152), while strict schedule definitions and server recurrence authority remain unchanged.

The desktop provides connection-scoped reusable [in-app toast notifications](apps-delidev-desktop-contract.md#in-app-toast-notifications), initially for acknowledged notification-preference and immediate configuration saves. These transient observations preserve independent OS delivery, Inbox state, mutation receipts and native acceptance boundaries.

 Named Home projects provide a New session shortcut beside their collapse control under the [chat-first creation contract](apps-delidev-desktop-contract.md#chat-first-session-creation). It selects the original project once in the mounted creation draft, preserves message/mode/budget/Options and shares existing project-selection locks without creating a session on navigation.
 The standalone [Pull requests sidebar](apps-delidev-desktop-contract.md#standalone-pull-requests) uses name-only repository rows, separate read-free Details disclosures for configured GitHub identity and the complete local UUID, and native segmented state choices. Repository selection and filter edits retain the existing explicit-load boundary; the shared shell, pending PR operations and connection-scoped lifetimes remain independently owned.

## Domain Contract Documents
- [Parallel browser QA](apps-delidev-qa-contract.md)
- [macOS status widget](apps-delidev-widget-contract.md)
- [Native package verification](apps-delidev-packaging-contract.md)
- [Protected account browser](cmds-delidev-browser-contract.md)
- [Storage operations](cmds-delidev-storage-contract.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Complete issue #964 requirements](cmds-delidev-requirements.md)
- [Protocol contract](protos-delidev-v1-contract.md)
- [TypeScript client contract](packages-delidev-api-client-contract.md)
- [Desktop client contract](apps-delidev-desktop-contract.md), including [Agent Worker core/optional presentation](apps-delidev-desktop-contract.md#agent-worker-core-and-optional-presentation)
- [AI Subscription settings](apps-delidev-subscription-settings-contract.md)
- [Worker workspace contract](cmds-delidev-workspace-contract.md)
- [Session file explorer and Git comparisons](cmds-delidev-files-contract.md)
- [Automatic session titles](cmds-delidev-session-titles-contract.md)
- [Portable configuration](cmds-delidev-configuration-transfer-contract.md)
- [Owned process contract](cmds-delidev-process-contract.md)
- [Worker-owned session terminals](cmds-delidev-terminals-contract.md)

- [Optional current-user services](cmds-delidev-user-services-contract.md)
- [Native subagent observations](cmds-delidev-subagents-contract.md)
- [Direct execution startup (planned)](cmds-delidev-execution-startup-contract.md)
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Read-only diagnostics](cmds-delidev-diagnostics-contract.md)
- [Diagnostics presentation](apps-delidev-diagnostics-contract.md)
- [Saved client connections](cmds-delidev-connections-contract.md)
- [Session development-server forwarding](cmds-delidev-forwarding-contract.md)
- [GitHub integration profiles](cmds-delidev-integrations-contract.md)
- [Account lifecycle and AI API Keys presentation](cmds-delidev-accounts-contract.md)
- [Managed Codex subscriptions](cmds-delidev-subscription-contract.md)
- [Grok Build subscriptions (planned)](cmds-delidev-grok-subscription-contract.md)
- [API account browser OAuth](cmds-delidev-account-oauth-contract.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Provider and model catalog](cmds-delidev-catalog-contract.md)
- [Native Codex model observations (pending)](cmds-delidev-native-models-contract.md)
- [API provider activation](cmds-delidev-provider-activation-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Explicit outbound networking](cmds-delidev-network-contract.md)
- [Session acceptance and input queue](cmds-delidev-sessions-contract.md)
- [Claude native context and manual compaction](cmds-delidev-claude-compaction-contract.md)
- [Same-account native session forks](cmds-delidev-forks-contract.md)
- [Native read-only Sidechat](cmds-delidev-sidechat-contract.md)
- [Planned shared native session compaction](cmds-delidev-compaction-contract.md)
- [Retained inbox and read-state contract](cmds-delidev-inbox-contract.md)
- [Retained conversation search](cmds-delidev-search-contract.md)
- [Retained activity](cmds-delidev-activity-contract.md)
- [Exact native response usage](cmds-delidev-usage-contract.md)
- [Schedules and durable occurrences](cmds-delidev-schedules-contract.md)

Permanent session deletion uses owner/client Connect and equivalent confirmed CLI
commands, durable intent outside SQLite, original Worker cleanup acknowledgements
and managed-backup erasure. Forwarding peers independently confirm cleanup;
offline or uncertain ownership remains pending. Original Local checkouts, other
sessions and shared account profiles are preserved. The [storage contract](cmds-delidev-storage-contract.md)
owns the lifecycle, snapshot-copy deletion integration and remaining database-restore/Sidechat limits.

## Cross-Domain Invariants
- Token-first GitHub profile creation uses existing System capability 34 and two always-visible Classic/Fine grained creation shortcuts. Go permits an undeclared owner only for draft form preparation; confirmation, saved-profile forms and repository access retain explicit owner rules. Native opening stays closed, click-driven and guarded by the original Settings visit, with no new allocation or migration. See the [integration contract](cmds-delidev-integrations-contract.md#official-forms-and-local-browser-opening) and [desktop contract](apps-delidev-desktop-contract.md#github-profile-settings).

- Confirmed desktop subscription/API-account configuration deletion closes its task dialog and refreshes the current category once, without a completion screen or cleanup-count read. Pending/uncertain requests retain their exact identities until confirmation or category disposal. Independent native browser cleanup and offline acknowledgments remain authoritative under the desktop, subscription Settings and browser contracts.

- macOS desktop builds with `debug_assertions` use an explicit development CEF Mock cookie key without signing credentials. System/development CEF paths share metadata, an exclusive native-host lease and durable cleanup of both copies; Go account/PAT/OAuth credentials remain OS-protected. Every other build retains System cookie storage. Follow the [desktop](apps-delidev-desktop-contract.md) and [browser](cmds-delidev-browser-contract.md) contracts; development observations grant no production Keychain or shutdown acceptance.

- macOS development launches retain a verified bundle per run. Only its Go server uses the explicitly registered local self-signed certificate and stable certificate-bound identifier. Original server/Worker files survive rebuilds and abnormal desktop exit. Code-change checks precede fresh OAuth and protected reads/writes; old ad-hoc items retain their original user authorization/repair boundary. Follow [local development signing](apps-delidev-desktop-contract.md#local-development-signing-and-recovery) and the [credential contract](cmds-delidev-credentials-contract.md). Public RPC/capability/schema and release-signing ownership are unchanged; fixture evidence cannot establish real account/platform acceptance.

- Desktop Settings has four ordered groups: AI, Coding, Device management and System. Repositories, Git Profiles and Git remain independent Coding menus; Runner Devices and Paired devices belong to Device management. Git presents global fetch/remediation policy while Server preferences retains routing/network settings. Both policy editors use the existing complete SETTINGS singleton and Connect configuration authority; grouping grants no new capability or migration. Existing category IDs and lifetimes remain stable, with `git-workflow` as the additional presentation category.
- Agent Worker creation/editing uses Harness → account source groups → source-specific Models → Configure under System capabilities 33/36, reserved on main by PRs #1351 and #1371. Models Settings is removed; Usage owns model details and Token pricing. The [desktop wizard](apps-delidev-desktop-contract.md#agent-worker-wizard) and [catalog](cmds-delidev-catalog-contract.md#agent-worker-model-selection) contracts preserve atomic SaveAgentWorker model/Worker saving and historical attribution without a migration or native/account readiness grant. Shared Reasoning effort/Subagent effort autocomplete provides advisory harness hints and exact direct input under the [desktop presentation contract](apps-delidev-desktop-contract.md#agent-worker-core-and-optional-presentation), without discovery or new execution support.
- Server preferences is an inline revision-bound singleton under the [desktop contract](apps-delidev-desktop-contract.md#server-preferences). Complete reads admit the existing saved values or Go defaults without writes. Explicit Save adopts the returned full document/ID/revision in the same form; dirty drafts and original uncertain requests survive refresh/reconnect, while category departure disposes presentation. Git retains its separate scoped New/Edit policy workflow. This UI amendment changes no RPC, schema, storage or execution capability.
- Sidechat follows the [native read-only contract](cmds-delidev-sidechat-contract.md): retain the complete original parent snapshot and account separately from the child enforcement overlay, reference workspace roots without taking deletion ownership, and join dependent child cleanup before removing parent files. Retain original-job-bound unpublished metadata claims across restart and reconcile only their exact child roots after process-owner cleanup. Independent Fork retains its separate lifetime. System 27 / Worker 16 reservations grant no native or product support.
- New repository registration follows the [folder workflow](apps-delidev-desktop-contract.md#projects-repositories-and-configuration-actions) in issue #1142: the native picker grants selection only, fresh same-computer proof binds the Worker, and Go owns read-only canonical inspection and atomic publication. Optional GitHub identity enrichment uses pre-established Worker capability 6/attachment field 3; raw URLs and credentials stay outside renderer/server metadata. Registration shares the current Settings visit disposal policy, while existing edits preserve explicit configuration. The Add repository dialog also offers credential-free HTTPS/SSH Clone and explicit revision-bound GitHub profile/repository selection through System 31/32 and Worker 18. PATs authorize server Metadata reads only; fresh originating Worker proof and its existing Git/SSH credentials own durable Clone. Server report publication completes registration independently of the dialog, with published Local checkouts preserved after failure. See the workspace, integration and protocol contracts.
- Settings is a regular `Surface.Settings` destination using the shared rail/category pane and compact drawer under issue #1236. The active category retains workflow state across reflow, same-category reselection and same-identity reconnect; category departure or leaving Settings disposes its local state and late continuations without changing saved effects or connection-owned conversation/New session workflows. Page-level Escape and active rail reselection preserve the visit. Settings-internal New Project and targeted Repositories entries and visible destination focus follow the [desktop contract](apps-delidev-desktop-contract.md#settings-screen-and-visit-lifetime-issue-1236). Home New project/Create a project opens an independent [project creation dialog](apps-delidev-desktop-contract.md#project-creation-outside-settings) over the current surface, with its own disposal and exact original retry ownership; it does not enter Settings or replace conversation/New session drafts.
- Issue #1146 implements inventory capability 5, entry connection-method field 9, two closed enums and the current OpenRouter OAuth structures directly initialized in schema 32. Reservations reached main before dependent implementation. The [OAuth contract](cmds-delidev-account-oauth-contract.md) preserves the complete authenticated Go/CLI/native/desktop lifecycle, server-owned protected credentials, once-only exchange and original local recovery. Settings lifetime/window/server generations fence callbacks; uncertainty never authorizes another exchange. Keep the issue open until real-provider/platform acceptance is complete.
- Issue #1235 implements independent subscription service identity with System capability 17 and historical storage allocation 28, now initialized directly in schema 32. Reservations reached main first; complete independent feature PRs merge in dependency order. The [subscription](cmds-delidev-subscription-contract.md), [protocol](protos-delidev-v1-contract.md) and [storage](cmds-delidev-storage-contract.md) contracts preserve historical attribution, configured-empty deny-all and original native ownership/cleanup while keeping accounts/models/snapshots independent of API Providers. Desktop AI Subscription now uses service-only account creation and the existing explicitly selected Codex login/cancel/authentication-refresh/logout RPC; Worker model selection, portable transfer, pricing, usage and diagnostics retain independent attribution. System capabilities 18/19 and Worker capability 8 add original-owner five-minute quota observations, explicit server-wide refresh, default-off account recovery notifications and confirmed reset-credit consumption using a durable official operation key. Sparse observations preserve last success; consumption outcomes never manufacture quota recovery. Metadata or fixture/build support does not establish native or real-account acceptance.

The approved inline-Worker-model replacement composes the current-only DB 32 /
protocol 2 reset. Its complete main-first declarations and ownership are defined
in the catalog, structure, storage, desktop, protocol, usage, transfer and
subagent contracts. Independent Models and persistent API catalogs are removed
only with complete activation. Earlier DB retention is waived by the owner;
explicit reset does not convert history or grant native/credential cleanup.
Earlier backups remain unsupported.
