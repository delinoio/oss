# Project: DeliDev

The desktop owns shared scroll-continuation state and presentation with domain
adapters retaining response-validation, initial-read, payload-disposal and
mutation authority. See the [shared desktop pagination contract](apps-delidev-desktop-contract.md#shared-scroll-continuation).

Files uses the original Worker-owned read-only observation boundary with a desktop lazy tree, one serial read owner and disposable preview. Per-directory metadata stays in the open scope; cancellation fences late publication, and only complete parent ranges prove absence. Back preserves the conversation/composer and restores accepted navigation while discarding bytes. Follow the [desktop](apps-delidev-desktop-contract.md#session-file-explorer) and [Files](cmds-delidev-files-contract.md) contracts; no protocol, native change or migration is added.

## Key-preserving format amendment

After main reservation PR #1666, ProviderInventory capability 9 owns connected API
format changes through the dedicated atomic Account RPC. Current/retained
server-owned connection generations share the original protected key, preserve
original executions and continuations, and require fresh explicit validation for
new sessions. Capability 7 and OAuth reservation 8 keep their separate meaning.
Follow the [account](cmds-delidev-accounts-contract.md#connected-api-format-changes),
[catalog](cmds-delidev-catalog-contract.md#key-preserving-api-format-change-reservations)
and [desktop](apps-delidev-desktop-contract.md#key-preserving-api-format-editing)
contracts. No migration, native change or format conversion is introduced.


The [OAuth format selection reservations](cmds-delidev-account-oauth-contract.md#oauth-api-format-selection-reservations)
extend the existing API account format boundary to accepted OAuth profiles through
ProviderInventory capability 8 and Start/attempt fields 4/7. PR #1657 established them on main
before implementation; preserve original login, credentials, receipts, account
connections and execution history without a migration.

API account format selection uses the allocation closure in PR #1646. The
[catalog contract](cmds-delidev-catalog-contract.md#api-account-format-selection)
owns per-key protocol profiles, schema-3 API compatibility and portable version 4;
legacy account defaults and original connection/execution ownership remain intact.
The implementation adds no database migration or Worker assignment fields.

Direct execution startup owns System 43 / Worker 23 after PR #1645's main-first
closure. The [startup contract](cmds-delidev-execution-startup-contract.md) replaces
manual inspection and numeric execution gates with actual original-process
initialization, bounded failure metadata and explicit proven no-send retry.
Account/Worker/history, credentials, revisions and independent cleanup remain
cross-domain invariants; existing supported feature/platform limits remain and
no SQLite migration is added.

Historical development-session `start_preparation` observations remain readable
and preserved under the startup contract, so account deletion can check retained
references. These observations grant no inspection, execution or retry authority.

Main desktop local Workers now have automatic same-owner registration/start and native supervision, with durable same-process manual Stop and original-child-only normal Quit. Existing CLI/service and saved-connection Workers retain independent ownership. Server connection, controller presence, account/harness eligibility and session cleanup remain independent; no protocol allocation or migration is required. The desktop, CLI and current-user service contracts define this boundary.

Failed-login subscription cleanup uses System capability 41 after main-first reservation PR #1614. The [subscription contract](cmds-delidev-subscription-contract.md#failed-subscription-cleanup-reservations) owns durable server batches, original login/credential authority, atomic deletion receipts and restore quarantine. The [Settings contract](apps-delidev-subscription-settings-contract.md#automatic-failed-login-cleanup) owns the one-click action and disposable status presentation. Existing Claude 38 and Grok 39/40 reservations retain ownership; no migration or Rust/native change is added.

Native execution option selection follows the [catalog contract](cmds-delidev-catalog-contract.md#native-execution-option-selection). Available settings pass unchanged to native execution; unavailable saved values remain explicit, and all four Codex API/subscription permission modes preserve authentication, applied-setting and cleanup/recovery ownership. No protocol or database migration is required.

## Goal
Run personal AI sessions across projects, accounts, native harnesses, and execution machines with durable single-user ownership. Issue #964 remains normative, with the explicit owner startup/presentation amendment in #1137; implementation and real-environment evidence are distinct.

Issue #1137 makes a fresh main desktop launch sufficient to start its own admitted local runtime and verify the authenticated product connection. Native-service scope admission remains Go-owned; same-process Stop, renderer lifecycle, saved-window authority and independent server/Worker/session ownership stay separate. Each main process owns at most one resident CLI with an OS-assigned random loopback port pinned within that process. Normal Quit, crash and forced termination retire the app-owned CLI/server; graceful Quit retains the 35-second limit and original-child exit proof. External runtimes and independent Workers remain separate. Private generation proof precedes bearer release, while immutable device/pairing/recovery authority and Saved addresses are preserved. Routine startup/sidebar/tray use product wording; lifecycle, registration and saved-connection controls keep persistent original ownership and can present their supported remedies inline; Connection & diagnostics remains optional. The [desktop contract](apps-delidev-desktop-contract.md) defines the implementation; record actual platform acceptance and unresolved limits in issue #1137, its pull requests and CI logs/artifacts.

Issue #1088 adds Worker-owned session terminals with native Unix PTY/Windows ConPTY processes, authenticated create/control/output operations and equivalent CLI commands. The desktop presents original terminals in full-pane typed session tabs with a pinned WebGL emulator, bounded scrollback and serialized original input/resize ownership. Presentation close and tab departure detach observation and discard unsent bytes without stopping original processes. Shared connection-owned resource tabs retain conversation/Sidechat drafts and controllers, persistent original Info and inactive Browser Hide cleanup; reopening selects original resources without creation. Agent Stop preserves terminals; Archive and storage deletion join their independent exact cleanup gate. The [terminal contract](cmds-delidev-terminals-contract.md) and [validation records in PR #1226](https://github.com/delinoio/oss/pull/1226) distinguish fixture/cross-build validation from native platform, remote Worker and release acceptance; this increment does not complete the remaining issue #964 scope.

Codex native flows use a common minimum SemVer `0.151.0` with no upper bound under the [harness contract](cmds-delidev-harness-contract.md). Preserve actual immutable executable/version attribution and independently verify native protocols and account authority. The [desktop contract](apps-delidev-desktop-contract.md) defines bounded sidecar lookup, and the [subscription Settings contract](apps-delidev-subscription-settings-contract.md) defines safe original-operation diagnostics. Feature PRs may record schema allocations with their implementation; optional document metadata adds no migration. Record fixture/build/native initialization and real account/platform evidence separately in pull requests and CI.

## Project ID
`delidev`; the Go component is `delidev-cli` and its executable is `delidev`.

Repositories use required remote URLs with optional Local folder connections under the [workspace contract](cmds-delidev-workspace-contract.md). System 37 and Worker 19 activate only after main reservations in PR #1377. Desktop registration, CLI/configuration transfer and session/schedule preparation share this source authority; independent managed clones preserve Fork, Sidechat and storage/deletion lifetimes without rewriting accepted history.

## Domain Ownership Map
- `apps/delidev`: desktop presentation and native host.
- `cmds/delidev-cli`: Go CLI, server, Worker, storage and native adapters.
- `protos/delidev/v1`: versioned Connect schema; `protos/gen/go/delidev/v1`: generated Go bindings.
- `packages/delidev-api-client`: generated TypeScript client and bounded transport/synchronization helpers.

The [desktop File menu](apps-delidev-desktop-contract.md#native-tray-and-menu-bar) opens a new product window with Command+N on macOS and Control+N on Windows/Linux through one native app-level handler. Close Window retains Command/Control+W and existing Local/Saved window ownership.

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
- [Session creation preferences (issue #1682)](apps-delidev-desktop-contract.md#session-creation-preferences-issue-1682)
- [AI Subscription settings](apps-delidev-subscription-settings-contract.md)
- [Worker workspace contract](cmds-delidev-workspace-contract.md)
- [Session file explorer and Git comparisons](cmds-delidev-files-contract.md)
- [Automatic session titles](cmds-delidev-session-titles-contract.md)
- [Portable configuration](cmds-delidev-configuration-transfer-contract.md)
- [Owned process contract](cmds-delidev-process-contract.md)
- [Worker-owned session terminals](cmds-delidev-terminals-contract.md)

- [Optional current-user services](cmds-delidev-user-services-contract.md)
- [Native subagent observations](cmds-delidev-subagents-contract.md)
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Direct execution startup](cmds-delidev-execution-startup-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Read-only diagnostics](cmds-delidev-diagnostics-contract.md)
- [Diagnostics presentation](apps-delidev-diagnostics-contract.md)
- [Saved client connections](cmds-delidev-connections-contract.md)
- [Session development-server forwarding](cmds-delidev-forwarding-contract.md)
- [GitHub integration profiles](cmds-delidev-integrations-contract.md)
- [Account lifecycle and AI API Keys presentation](cmds-delidev-accounts-contract.md)
- [Managed Codex subscriptions](cmds-delidev-subscription-contract.md)
- [OpenCode Go subscriptions](cmds-delidev-opencode-go-subscription-contract.md)
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
- New session and New general chat keep independent accepted-request Agent Worker/Runner Device pairs per stable native server/device scope under the [session creation preferences contract](apps-delidev-desktop-contract.md#session-creation-preferences-issue-1682). The bounded, private native document stores only identity metadata outside server backups and configuration transfer. Exact-ID current resource eligibility, manual drafts, Local proof and original creation receipts retain their separate authority; preference failure or recovery never resends CreateSession. This feature adds no RPC, protocol allocation or database migration.

- Token-first GitHub profile creation uses existing System capability 34 and two always-visible Classic/Fine grained creation shortcuts. Go permits an undeclared owner only for draft form preparation; confirmation, saved-profile forms and repository access retain explicit owner rules. Native opening stays closed, click-driven and guarded by the original Settings visit, with no new allocation or migration. See the [integration contract](cmds-delidev-integrations-contract.md#official-forms-and-local-browser-opening) and [desktop contract](apps-delidev-desktop-contract.md#github-profile-settings).

- Confirmed desktop subscription/API-account configuration deletion closes its task dialog and refreshes the current category once, without a completion screen or cleanup-count read. Open tasks retain exact pending/uncertain identities for explicit retry. X/Escape/local Cancel disposes the task scope without hidden-operation UI or category locking; late results cannot continue a client deletion or reach a fresh task. Independent native browser cleanup and offline acknowledgments remain authoritative under the desktop, subscription Settings and browser contracts.

- macOS desktop builds with `debug_assertions` use an explicit development CEF Mock cookie key without signing credentials. System/development CEF paths share metadata, an exclusive native-host lease and durable cleanup of both copies; Go account/PAT/OAuth credentials remain OS-protected. Every other build retains System cookie storage. Follow the [desktop](apps-delidev-desktop-contract.md) and [browser](cmds-delidev-browser-contract.md) contracts; development observations grant no production Keychain or shutdown acceptance.

- macOS credential access permits OS-owned Keychain authentication in the server user's session, including background work, cleanup and Doctor. Approval continues the original operation; cancellation and unavailable UI preserve protected references and typed recovery. Linux and Windows retain their noninteractive adapters. Follow the [credential](cmds-delidev-credentials-contract.md) and [diagnostics](cmds-delidev-diagnostics-contract.md) contracts; isolated automatic fixtures do not prove interactive platform acceptance.

- macOS development launches retain a verified bundle per run. Only its Go server uses the explicitly registered local self-signed certificate and stable certificate-bound identifier. Original server/Worker files survive rebuilds and abnormal desktop exit. Code-change checks precede fresh OAuth and protected reads/writes; old ad-hoc items retain their original user authorization/repair boundary. Follow [local development signing](apps-delidev-desktop-contract.md#local-development-signing-and-recovery) and the [credential contract](cmds-delidev-credentials-contract.md). Public RPC/capability/schema and release-signing ownership are unchanged; fixture evidence cannot establish real account/platform acceptance.

- Desktop Settings has four ordered groups: AI, Coding, Device management and System. Repositories, Git Profiles and Git remain independent Coding menus; Runner Devices and Paired devices belong to Device management. Git presents global fetch/remediation policy while Server preferences retains routing/network settings. Both policy editors use the existing complete SETTINGS singleton and Connect configuration authority; grouping grants no new capability or migration. Existing category IDs and lifetimes remain stable, with `git-workflow` as the additional presentation category.
- Agent Worker creation/editing uses Harness → account source groups → source-specific Models → Configure under System capabilities 33/36, reserved on main by PRs #1351 and #1371. Models Settings is removed; Usage owns model details and Token pricing. The [desktop wizard](apps-delidev-desktop-contract.md#agent-worker-wizard) and [catalog](cmds-delidev-catalog-contract.md#agent-worker-model-selection) contracts preserve atomic model/Worker saving, legacy APIs and historical attribution without a migration or native/account readiness grant. Shared Reasoning effort/Subagent effort autocomplete provides advisory harness hints and exact direct input under the [desktop presentation contract](apps-delidev-desktop-contract.md#agent-worker-core-and-optional-presentation), without discovery or new execution support.
- Server preferences is an inline revision-bound singleton under the [desktop contract](apps-delidev-desktop-contract.md#server-preferences). Complete reads admit the existing saved values or Go defaults without writes. Explicit Save adopts the returned full document/ID/revision in the same form; dirty drafts and original uncertain requests survive refresh/reconnect, while category departure disposes presentation. Git retains its separate scoped New/Edit policy workflow. This UI amendment changes no RPC, schema, storage or execution capability.
- Sidechat follows the [native read-only contract](cmds-delidev-sidechat-contract.md): retain the complete original parent snapshot and account separately from the child enforcement overlay, reference workspace roots without taking deletion ownership, and join dependent child cleanup before removing parent files. Retain original-job-bound unpublished metadata claims across restart and reconcile only their exact child roots after process-owner cleanup. Independent Fork retains its separate lifetime. System 27 / Worker 16 reservations grant no native or product support.
- New repository registration follows the [folder workflow](apps-delidev-desktop-contract.md#projects-repositories-and-configuration-actions) in issue #1142: the native picker grants selection only, fresh same-computer proof binds the Worker, and Go owns read-only canonical inspection and atomic publication. Optional GitHub identity enrichment uses pre-established Worker capability 6/attachment field 3; raw URLs and credentials stay outside renderer/server metadata. Registration shares the current Settings visit disposal policy, while existing edits preserve explicit configuration. The Add repository dialog also offers credential-free HTTPS/SSH Clone and explicit revision-bound GitHub profile/repository selection through System 31/32 and Worker 18. PATs authorize server Metadata reads only; fresh originating Worker proof and its existing Git/SSH credentials own durable Clone. Server report publication completes registration independently of the dialog, with published Local checkouts preserved after failure. See the workspace, integration and protocol contracts.
- Settings is a regular `Surface.Settings` destination using the shared rail/category pane and compact drawer under issue #1236. The active category retains workflow state across reflow, same-category reselection and same-identity reconnect; category departure or leaving Settings disposes its local state and late continuations without changing saved effects or connection-owned conversation/New session workflows. Page-level Escape and active rail reselection preserve the visit. Settings-internal New Project and targeted Repositories entries and visible destination focus follow the [desktop contract](apps-delidev-desktop-contract.md#settings-screen-and-visit-lifetime-issue-1236). Home New project/Create a project opens an independent [project creation dialog](apps-delidev-desktop-contract.md#project-creation-outside-settings) over the current surface, with its own disposal and exact original retry ownership; it does not enter Settings or replace conversation/New session drafts.
- Issue #1146 implements inventory capability 5, entry connection-method field 9, two closed enums and real migration 29 for OpenRouter OAuth after real migrations 26–28. Reservations reached main before dependent implementation. The [OAuth contract](cmds-delidev-account-oauth-contract.md) preserves the complete authenticated Go/CLI/native/desktop lifecycle, server-owned protected credentials, once-only exchange and original local recovery. Settings lifetime/window/server generations fence callbacks; uncertainty never authorizes another exchange. Keep the issue open until real-provider/platform acceptance is complete.
- Issue #1235 implements independent subscription service identity with System capability 17 and real migration 28 after accounting 26 and diagnostics 27. Reservations reached main first; complete independent feature PRs merge in dependency order. The [subscription](cmds-delidev-subscription-contract.md), [protocol](protos-delidev-v1-contract.md) and [storage](cmds-delidev-storage-contract.md#subscription-retirement-issue-1235) contracts preserve historical attribution, configured-empty deny-all and original native ownership/cleanup while keeping accounts/models/snapshots independent of API Providers. Desktop AI Subscription now uses service-only account creation and the existing explicitly selected Codex login/cancel/authentication-refresh/logout RPC; Worker model selection, portable transfer, pricing, usage and diagnostics retain independent attribution. System capabilities 18/19 and Worker capability 8 add original-owner five-minute quota observations, explicit server-wide refresh, default-off account recovery notifications and confirmed reset-credit consumption using a durable official operation key. Sparse observations preserve last success; consumption outcomes never manufacture quota recovery. Metadata or fixture/build support does not establish native or real-account acceptance.
- Execution-device presentation uses `Runs on` for the New session machine selector and `Runner Device` / `Runner Devices` for former Execution Worker labels and messages. Agent Worker and technical Worker terminology remain distinct; machine/protocol/storage IDs, CLI commands, logs, error codes and the `execution-workers` Settings category value stay unchanged. The [desktop contract](apps-delidev-desktop-contract.md) owns the presentation boundary.
- Go owns business logic; clients use authenticated Connect and preserve exact request/revision identities.
- Grok public request journals preserve typed payloads plus bounded original JSON bytes for independently verified proposal digests. Server admission precedes public response authority; byte evidence never grants native or filesystem access. Follow the [harness contract](cmds-delidev-harness-contract.md).
- Local and remote operation preserve original native ownership, explicit authorization, credential isolation and uncertainty.
- New Claude children require original task-source metadata and verified parent-tool ownership before content/history updates, including nested children. Content/history never creates child ownership; see the [subagent contract](cmds-delidev-subagents-contract.md).
- Each original Claude Agent/Task parent tool owns at most one observed child across Worker composition and atomic server publication, including after that child's completion and across later executions of the same session. Clients retain the original hierarchy without child controls; see the [subagent contract](cmds-delidev-subagents-contract.md).
- Shared Worker/server validation rejects telemetry absent from its incoming native source and closes every supplied child usage report to its native source schema, requiring exact nullable normalized counter parity before atomic publication. Native report bytes remain unchanged and independent reports remain non-additive; unavailable usage cannot be supplied without its original report. See the [subagent contract](cmds-delidev-subagents-contract.md).
- Desktop child reads validate the complete bounded page before exposing content. Foreign/malformed envelopes, unsupported native profiles, inconsistent known-parent/source coverage or invalid exact usage parity make the whole page unavailable; earlier supported retained facts and parents on other pages remain permissible. See the [desktop](apps-delidev-desktop-contract.md) and [subagent](cmds-delidev-subagents-contract.md) contracts.
- Sessions with observed native subagents retain version-1 paused completion after independent cleanup. Same-account Codex Fork requires a version-2 checkpoint and cannot promote that child history; see the [subagent](cmds-delidev-subagents-contract.md) and [fork](cmds-delidev-forks-contract.md) contracts.

- Native relay handlers join started response-writer cancellation callbacks before returning, so downstream connection reuse cannot inherit an earlier request's deadline mutation.
- Plaintext loopback provider and inference requests require Direct or an explicit bypass. Reject proxied plaintext before connection, preserve verified HTTPS proxy routing, and never silently fall back.
- Explicit outbound proxy credentials remain isolated across provider, inference and GitHub response bodies and metadata: guard header names/values and keep response trailers private under the [network contract](cmds-delidev-network-contract.md).
- Proxy mode/host/port edits cannot carry an omitted existing credential to a new peer. Clear the new profile association or accept explicit replacement input, retaining independently pinned old routes under the [network contract](cmds-delidev-network-contract.md).
- Network profile deletion retains one private server-bound cleanup obligation before public removal. Current authorized owner/client mutations recover it after cancellation, revocation or restart without impersonating the original actor; accepted deletion and absent-profile proof precede native cleanup.
- Managed database restore preserves current network profile/generation/selection authority and the machine metadata required to administer Worker routes, without restoring Worker authorization. It refuses pending private network credential intents under the shared credential gate. Historical database routing cannot reactivate old proxy credentials.
- Network profile publication retains one private server-bound credential intent across vault/SQLite failure. Exact retries and replacement cleanup preserve original actor/input/receipt ownership; uncertainty cannot admit another unpublished native generation.

- Codex fork children survive parent deletion. Explicit Local sharing is limited to user-owned Local source checkouts; managed Worktree sources require independent copies. Go rechecks ownership at acceptance, preparation and publication, and the desktop offers only the supported choice. See the [fork contract](cmds-delidev-forks-contract.md). The current Fork authentication profile is API-only; managed subscription sources require a separate protected lease implementation and are rejected before acceptance.
- Real native/account/platform evidence remains distinct from deterministic fixtures, cross-compilation and packaging.
- Keep complete issue #964 requirements and unresolved acceptance items visible.
- Workspace storage and session forwarding share transactional ownership exclusion: storage requires both original peer cleanup confirmations; new forwarding and live socket authority require present storage, while original cleanup remains authorized. Worker observations and destructive storage/deletion also share an independent session gate through anchored reads and native cleanup, preserving views during execution.

- Managed subscription operations and uncertain leases block replacement; restored credential references remain fenced because the external vault is not restored.
- Manual PR fixes use explicit original evidence/project ownership, eligible sessions and Worker Git authentication; the server lookup PAT never authorizes publication. Native completion requires independent push/cleanup proof before exact evidence handling. Bounded automatic fixes reuse the same gates for explicitly linked PRs and independently enabled policies, retaining one durable chain and explicit Stop/Archive authority. Unperformed native/account acceptance remains separate.

- Managed database restore preserves current revocations and external permanent deletion obligations, quarantines historical execution and ends the original server epoch. Temporary recovery images participate in permanent erasure; the storage contract owns their lifecycle.
- PR activity preserves immutable source/version/actor metadata across Go, generated clients, CLI and desktop. Attempt success cannot establish verified handling; only a dedicated original verification source can project that outcome.
- Token Usage displays zero for a fully present empty recorded response summary only in its six top-level cards. Missing execution/compaction coverage remains explicit; details, charts, native accounting and ledger evidence retain their original meanings. See the [usage contract](cmds-delidev-usage-contract.md).
- Session Usage tables chain vertical scrolling natively to main content while retaining horizontal containment, nine columns and existing controls under the [desktop contract](apps-delidev-desktop-contract.md). Scrolling cannot change filters, queries or accounting evidence.
- Negotiated native usage keeps Codex responses and verified Grok closed inputs as distinct accounting units across Go, CLI and desktop. Grok retention requires original input/history/closure and independently confirmed cleanup; its totals never imply pricing, actual cost or estimated-budget contribution. See the [usage contract](cmds-delidev-usage-contract.md).

Explicit stopped Codex API account selection for issue #1097 spans Go,
authenticated Connect, CLI and generated clients. Original candidates,
provider/model, terminal/cleanup and portable-history gates retain all prior
attribution; new execution requires explicit Resume and a fresh scoped grant.
Subscription/provider/model switching and automatic fallback remain excluded.
See the [sessions contract](cmds-delidev-sessions-contract.md) and
[proxy contract](cmds-delidev-proxy-contract.md) for the account-switching boundary.
Record the controlled native A-to-B result separately from unperformed
desktop/real-account/platform acceptance in pull requests, issues and CI runs.

- Fresh authenticated main-desktop Worker relaunch recovery uses newly recorded immutable original-controller generation/registration/canonical-scope/desktop-client/PID/kernel-birth evidence, synchronized before admission and rechecked under final Go arbitration. Uncertain legacy/unknown proof remains blocked; same-process Stop, pending admission, updater/service and borrowed lifetimes retain their owners. Controller exit never proves native cleanup or authorizes replay/Resume. Follow the [desktop](apps-delidev-desktop-contract.md#automatic-main-window-local-worker-management), [CLI](cmds-delidev-contract.md#main-desktop-automatic-worker-management) and [process](cmds-delidev-process-contract.md#original-worker-controller-observation) contracts.

## Diagnostic resource-kind invariant

Generic diagnostic consumers must accept the complete closed stored resource-kind inventory, including child-agent and session-forward records, without interpreting retained counts as execution readiness.

## Change Policy
Update the owning domain contract when behavior changes. Update this index only for ownership, its domain catalog or cross-domain invariants. Record each implementation/validation increment in pull requests, issues and CI logs/artifacts; do not add repository evidence documents. A validation-only increment does not require editing this index or an AGENTS file.

## References
- https://github.com/delinoio/oss/issues/964
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)
- [Structure and compatibility](cmds-delidev-structure-contract.md)

## Home navigation invariant

The desktop provides a dedicated **New Chat** action and projectless start screen under the [desktop contract](apps-delidev-desktop-contract.md#dedicated-general-chat-creation). Reuse ordinary Agent Worker session execution, with independent connection-memory creation drafts and exact requests; no new protocol, migration or tool-free authority is introduced.

Home (Sessions/New Session/New General Chat) keeps independent bounded 50-record reads and connection-owned minimal navigation projections with no row-count cutoff. Accepted Home metadata grows with reached inventory; ordinary query payloads keep their eight-inactive-query bound. Preserve original resource/project identity, selected conversation/drafts, scope generation cancellation, and the mounted local/saved server controllers. Other destinations use shared domain-adapted continuation with bounded full-payload windows and unchanged explicit first-read admission. See the [desktop contract](apps-delidev-desktop-contract.md).

- Workspace storage and Codex forks share source ownership exclusion: forks require present storage at acceptance, claim and publication, and storage waits for unresolved fork jobs. Stored workspaces require explicit restoration before a fork.

- Workspace storage and session terminals share transactional ownership exclusion: storage waits for every terminal's independently verified process cleanup, while terminal creation, input/resize and non-close dispatch require present storage. Original close and cleanup authority remains available under the [storage](cmds-delidev-storage-contract.md) and [terminal](cmds-delidev-terminals-contract.md) contracts.

## Device appearance invariant

Device appearance includes the issue #2025 complete styling snapshot and bounded custom-theme library. It is a native desktop-owned preference shared across local and saved-server windows, independent of every server configuration, pairing, backup and configuration transfer. Its controller stays above connection state and follows the [desktop appearance contract](apps-delidev-desktop-contract.md#device-appearance-issue-1238).

The device language preference follows the [localization contract](apps-delidev-localization-contract.md). Appearance uses a search combobox with fixed English/native self-names, System pinned first and English-name ordering. Search remains presentation-only; explicit supported choices use the existing native revisioned save boundary.

## Session terminal deletion invariant

Interactive terminals belong to the prepared session's original Worker and primary workspace. Accepted input bytes remain private dispatch data for that Worker; all public terminal resources retain pending metadata while omitting those bytes. Agent Stop preserves them. Archive and permanent deletion join their independently confirmed process-tree cleanup; deletion cannot dispatch workspace removal before that join or bypass it during final purge. An accepted uncertain close report atomically retains the cleanup obligation under a fresh close identity; exact receipt replay cannot replace that next assignment or release workspace deletion. Replacement Workers may adopt only exact close reconciliation after a fresh current-instance server claim, preserving original evidence and synchronized shutdown output-loss observations without creating a shell or replaying controls. Missing shutdown observations conservatively expose possible abandoned output, independently of cleanup proof. A synchronized cleanup acknowledgement permits local terminal ownership metadata retirement, with interrupted retirement retained for local retry even after database deletion. Replacement acknowledgement recovery reads only exact committed receipts under the same current device/machine; it cannot revive native work or expose resource content. See the [terminal contract](cmds-delidev-terminals-contract.md) and [storage contract](cmds-delidev-storage-contract.md).

Creation synchronizes the original process-owner index before native-start
intent. Pre-native restart reconciliation requires that retained index;
missing or changed ownership cannot establish cleanup or authorize replay.

Desktop terminal history reads, polling, manual refresh and selection require
advertised system terminal support. Unknown or unsupported status cannot issue
terminal reads or expose cached terminal errors; see the
[desktop contract](apps-delidev-desktop-contract.md).

Remaining-feature delivery follows the [allocation workflow](cmds-delidev-structure-contract.md#allocation-workflow): complete feature PRs may include shared numeric and migration allocation records with their implementation and merge in dependency order. Reservation support and actual native/account/platform acceptance remain distinct.

Native input accounting and request diagnostics activate their main-established independent capabilities with real migrations 26/27. Preserve once-only original Claude/OpenCode source attribution, nullable precise counters, immutable pricing and metadata-only requested/observed diagnostic evidence across authenticated RPC, CLI and desktop. The [usage](cmds-delidev-usage-contract.md) and [diagnostics](cmds-delidev-diagnostics-contract.md) contracts own these boundaries; no fixture establishes real-account acceptance.
- Worker network bootstrap pins an original protected recipient and pending/paired identity, then independently reconciles desired/effective generation through authenticated control. Stale state blocks fresh work while original active generation and cleanup remain immutable. The separately claimed Codex API/title tunnel keeps upstream credentials in Go, verifies native proxy/shell policy and joins before cleanup; public native-route use is independent of control readiness and provider success. Follow the [network contract](cmds-delidev-network-contract.md).
- Network product controls share existing Server preferences/Runner Device settings and exact Connect/CLI semantics; protected local preparation/import has a closed trusted-window bridge to Go. System capability activation, Worker negotiation, encrypted cache publication, authenticated control readiness and actual native route use remain independent. Settings visits cannot retain late callback authority or start/restart a Worker implicitly.

Workspace storage exposes original-job snapshot, usage preview, cleanup, inspection, restore, deletion and recovery through authenticated Connect RPC, CLI and the connection-owned desktop controller. Preserve independent terminal/forwarding cleanup gates and logical-versus-physical byte measurements under the [storage contract](cmds-delidev-storage-contract.md); runtime/account/platform acceptance remains separate from fixtures.

Codex subagent settings require independent server/Worker capabilities, immutable canonical child model identity and same-account relay narrowing under `cmds-delidev-subagents-contract.md`; requested defaults never establish an observed child model or independent child control.

Issue #1208 extends native subagent observation to OpenCode 1.18.32 foreground tasks under the existing subagent contract and independent System/Worker capabilities. Preserve the one-level dual task/child ownership proof, same account/model, separate usage, original Stop/cleanup and paused child-bearing completion; implementation and native/account/platform acceptance remain separately recorded in the integration PR.

- Bounded Unix plain-text OpenCode General Chat Fork uses independent System 26 / Worker 15, native ID-clone/history proof, explicit no-change relocation, preparation without inference and independent copied files. The [fork contract](cmds-delidev-forks-contract.md) owns this profile; Codex support does not imply OpenCode or Sidechat authority.

- The canonical 35-ID API provider registry preserves original wire IDs and shares product ordering across RPC/CLI/desktop. Real default migration 30 follows actual 26–29, retaining existing identities and deletions. Fixed native/private inspection authority is owned by the [provider inspection](cmds-delidev-providers-contract.md), [catalog](cmds-delidev-catalog-contract.md) and [activation](cmds-delidev-provider-activation-contract.md) contracts; discovery grants no inference or subscription authority.


Workspace storage exposes original-job snapshot, usage preview, cleanup, inspection, restore, deletion and recovery through authenticated Connect RPC, CLI and the connection-owned desktop controller. Preserve independent terminal/forwarding cleanup gates and logical-versus-physical byte measurements under the [storage contract](cmds-delidev-storage-contract.md); runtime/account/platform acceptance remains separate from fixtures.


- Signed updates use independent System 28/Worker 17 admission, original device/generation receipts, joined idle replacement and retained old binaries. Desktop install requires original trusted-window confirmation and preserves live server/harness lifetimes. System 29 SSH setup pins exact host identity and the server-compatible signed Worker release, with protected credentials and original remote operation inspection. An unset production public-root declaration blocks real signing/downloads; fixture/build evidence remains separate from production account/platform acceptance. See the [updates](cmds-delidev-updates-contract.md) and [SSH setup](cmds-delidev-ssh-setup-contract.md) contracts.
- [DeliDev signed updates](cmds-delidev-updates-contract.md)
- [DeliDev SSH Worker setup](cmds-delidev-ssh-setup-contract.md)

AI Subscription browser login and naming compose across Go server ownership, generated Connect capability 30 and trusted native window callbacks. Account identity is independent of Runner Devices; execution/quota still retain their original Worker selection and credential leases. ChatGPT login precedes optional naming. Claude independently selects its owning Runner under capability 38 and then uses login/name steps; Grok remains unsupported. Follow the subscription, desktop and protocol contracts and distinguish fixtures/builds from actual account/packaged-platform acceptance. Shared reservations reached main in PR #1332; this optional JSON amendment adds no migration.

Ordered account source routing follows the catalog, desktop, sessions, protocol and
portable configuration contracts. One Harness retains source-specific models and
accounts; only confirmed complete quota exhaustion can advance a new session,
and observed recovery restores priority for later sessions. Existing executions
and historical Usage attribution remain immutable. Reservation PR #1371 precedes
activation; schema-3 Agents and portable version 3 add no SQLite migration.

The owner-approved [pre-release compatibility reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset)
reserves database baseline 32, protocol 2 and Worker attach field 10 with its
complete implementation. Reservations leave current runtime behavior unchanged.

## Native Claude subscriptions

PR #1612 established System 38, Worker 20 and the complete protocol declaration
closure on main before implementation. The extension composes the existing
subscription, desktop, harness, protocol and storage owners. Claude Code
`2.1.236` owns login/status/logout and execution in an original account-specific
profile on the explicitly selected local or remote Runner. The server retains
only opaque profile/owner/generation/operation metadata and a keyed identity
commitment; native credentials never transfer. One exclusive lease serializes
lifecycle and execution, and original cleanup gates logout/deletion/recovery.
No migration is added. Existing personal login import, external tokens,
Console/API login, cross-device authentication and Claude quotas/credits remain
excluded. Actual account and packaged-platform acceptance remain separate from
fixtures/builds. See [the subscription contract](cmds-delidev-subscription-contract.md#native-claude-subscriptions).
The owner-approved native Claude subscription extension spans the existing
subscription, desktop, harness, protocol and storage owners. Record System 38,
Worker 20 and the complete login-code/progress/native-identity declarations in
the owning feature PR. Authentication remains in an original installed
Claude Code account profile on the selected Runner Device; server metadata does
not grant credentials, cross-device execution or cleanup authority. The
reservation prerequisite activates no support and adds no migration. Follow
[the subscription contract](cmds-delidev-subscription-contract.md#planned-native-claude-subscriptions).

The approved inline-Worker-model replacement composes the current-only DB 32 /
protocol 2 reset. Its complete recorded declarations and ownership are defined
in the catalog, structure, storage, desktop, protocol, usage, transfer and
subagent contracts. Independent Models and persistent API catalogs are removed
only with complete activation. Earlier DB retention is waived by the owner;
explicit reset does not convert history or grant native/credential cleanup.
Earlier backups remain unsupported.

- Subscription Auto cleanup includes failed initial ChatGPT server logins and fully disconnected configurations for all supported subscription services. Explicit failed-login deletion uses the same server-owned durable cleanup controller and retains the original deletion command across cleanup revision changes. Follow the subscription, account and subscription Settings contracts; preserve reference/vault/native ownership checks and terminal-attempt semantics without protocol allocation or migration.

Issue #1699 composes desktop-wide inline causes and supported remediation under the desktop, diagnostics and subscription Settings contracts. Each owning workflow shares original controllers, confirmations and uncertain-request authority; presentation adds no repair capability or automatic Doctor inspection. Bounded inventory facts cannot grant eligibility, and account-storage inspection and the removed Search shortcut remain removed. Record validation and remaining native/account/platform limits in PRs and CI rather than repository evidence documents.

- Explicit native skills share composer, joined Worker observation, immutable session input, private Codex invocation and durable snapshot deletion ownership across the existing sessions, harness, desktop, storage and protocol contracts.
Image inputs follow the [image input contract](cmds-delidev-image-input-contract.md): private Worker byte ownership, immutable ordered references and original session/Fork/Sidechat cleanup apply across desktop, protocol, server and harness flows. Issue #1746 alone permits its complete declarations and activation in one PR without a database migration. Creation-only attachment guidance (issue #2062) shows static localized format, limit and supported execution requirements on plus hover/focus under the [desktop contract](apps-delidev-desktop-contract.md#creation-composer-toolbar-issue-1858); it adds no route reads or execution authority.
Routing preview presentation follows the [desktop contract](apps-delidev-desktop-contract.md#agent-worker-routing-preview-presentation). Compact source comparisons preserve server ordering and every decision field. Only complete identical ordered final evidence may be deduplicated. Task-scoped safe account/provider projections share four read slots across the category owner and response generations; names grant no routing or execution authority.
Server-owned ChatGPT quota uses independent System 46 under the [subscription ownership contract](cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota--issue-1728). The explicit issue #1728 batch exception permits same-PR allocation and activation; original Worker/execution/reset-credit ownership and confirmed cleanup remain independent.

Managed ChatGPT/Codex Sidechat (issue #1829) composes System 47, Worker 26,
existing Sidechat 27/16 and managed authentication Worker 3. The session detail
action menu exposes Open Sidechat only for the original eligible completed
source and negotiated Runner. Independent subscription Fork remains unsupported.
See [managed Sidechat](cmds-delidev-sidechat-contract.md#managed-chatgpt-sidechat--issue-1829)
for protected lease/Finish publication, read-only continuation and dependent
cleanup ownership. No new RPC or SQLite migration is added.

## Project prompt history ownership

Issue #1828 adds server-persisted project first-message history under the sessions, storage, protocol, desktop and API-client contracts. Go owns immutable 100-entry acceptance order, authenticated pagination and receipt-bound confirmed clear. Desktop owns text-only boundary keyboard recall. History survives source-session deletion, belongs to project deletion, is captured by managed backups and is excluded from portable configuration. System 48 / entity 35 add no Worker, native or migration authority.

Subscription Account details follows the [subscription Settings modal boundary](apps-delidev-subscription-settings-contract.md#account-details-dialog-issue-1824): category-owned read-only metadata survives virtualized row eviction, while explicit management retains its original fresh account authority.

## Automated distribution

The owner-authorized release coordinator includes DeliDev stable version selection,
exact `delidev-v<semver>` tags and ends after tag preparation. Those tag pushes
start independent macOS/Linux x64/arm64 download-only GitHub publication.
Deployment failures are recovered in the original tag workflow; coordinator
success does not establish publication. Windows production signing is unavailable and both Windows targets
are skipped. Published inventory is immutable and contains no updater manifest.
The [packaging contract](apps-delidev-packaging-contract.md#automated-download-only-releases)
owns production macOS credentials, provisioning/notarization, retained candidates
and readback verification. The [updates contract](cmds-delidev-updates-contract.md#download-only-automated-release-exception)
preserves complete six-target manifest authority for a future new version.
Workflow fixtures and packaging do not complete issue #964 or establish real
installed-platform, WidgetKit or account acceptance. Operational credential
registration and first publication remain separate from repository configuration.

- Ordinary session tools may use the executing machine’s existing gh login through the bounded Worker-owned selector in the [harness contract](cmds-delidev-harness-contract.md#ordinary-execution-github-cli-context). Server integrations, native provider credentials, remote machine identity, Sidechat restrictions and user-owned configuration cleanup remain separate.

Server quota V2 uses System 50 under the [subscription contract](cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota-v2--issue-1854). It independently reads an access-token-only server profile before, during and after Worker execution while retaining original writer/reference/cleanup authority. Explicit Worker quota and reset-credit ownership remain separate; no migration or Worker protocol change.

### Auxiliary tray ownership

Issue #1967 adds one process-owned `tray-status` bundled CEF presentation document
outside the Local/Saved product-window registry. The desktop/localization
contracts own its retained quota-first snapshot, exact instance admission,
original-target navigation, native fallback, monitor placement and joined Quit
boundary. Its separate renderer has no Connect transport, product queries,
credentials, filesystem authority or device preference writes. Automated build
and fixture evidence remains distinct from installed platform acceptance.

Project settings and server defaults follow the [project behavior contract](cmds-delidev-catalog-contract.md#project-behavior-settings-issue-1965). System 52 owns complete schema-2 configuration and portable v5. Explicit original project context governs routing/fetch/remediation; newly published typed native plans share the existing durable response controller. No migration or new native authority is introduced.

- Codex Agent Worker Fast mode selection and original native diagnostics follow the catalog, desktop and diagnostics contracts for issue #1961. No allocation/migration or actual subscription acceptance is implied by fixture evidence.

- Issue #1953 adds the System Keyboard shortcuts category after Appearance and a device-owned version-1 `shortcuts.json` preference. Native authorized product-window read/update/publication uses expected revisions and atomic replacement; `ShortcutPreferenceProvider` stays above connections. Seven existing actions support explicit save/discard/default/disable, with one committed binding source for dispatch/help/ARIA. Preserve fixed editing/newline/Escape and K/N/W/1–9 reservations; keep preferences outside server backups/portable transfer and uncertainty recovery read-only. See the desktop and localization contracts.

- New Project can open independently scoped existing repository registration in either Home or Settings. The desktop contract owns modal/draft lifetime, confirmed-identity catalog resolution and read-only retry; existing repository/native/Connect authority remains unchanged.

### New-session defaults and branch prefixes

Issues #2054/#2057 compose System 56, Worker 38, schema-3 Project/Settings and portable v6 under the catalog, desktop, session, harness, protocol/client and transfer contracts. Preserve existing capability52 and all historical configuration/native ownership. Automatic creation mode, explicit literal prefix overrides and immutable execution provenance grant no native/account/workspace support.

Codex AI approval review composes the harness, catalog, sessions, proxy and usage domains under issue #1980. System 55 and Worker 30 retain separate configuration/adapter gates. Preserve immutable effective reviewer verification, original lifecycle observations, same-account bounded canonical relay and unproved model attribution; Sidechat always applies its original User/read-only/never overlay. This feature changes neither account ownership nor the database schema.

Independent managed ChatGPT Fork is owned by the [Fork contract](cmds-delidev-forks-contract.md#managed-chatgpt-independent-fork--issue-1979), composing separate System 53 / Worker 29 with protected subscription lifecycle. Preserve independent child lifetime, immutable reviewer and branch-prefix provenance, and exact settled native tool history; Sidechat retains its separate read-only dependent lifetime.

## Same-question Sidechat retry

Issue #2061 adds authenticated same-question Sidechat retry with System 57 / Worker 31. Preserve original public child/snapshots/reference ownership, freeze the latest accepted completed parent turn, retain all native generations and prior answer/usage history, and select a replacement answer only after verified completion. Complete dependent cleanup covers every retry runtime. See [the Sidechat contract](cmds-delidev-sidechat-contract.md#same-question-retry--issue-2061); no migration or broader native/account support is implied.

### Situation-specific notifications (#2055)
The Inbox, storage, subscription, Schedule and desktop contracts jointly own granular client choices, durable future-only operational claims and the trusted native original-connection observer under System 58. Preserve legacy preferences/receipts, real migration 32 ownership, original-source eligibility, separate quota recovery consent and explicit OS permission. EN/KO Settings/search and fresh original-target navigation are part of this boundary. Automated checks do not establish installed native/account/platform acceptance.

Conversation Revert (#2045) is owned by the existing compaction/context, sessions,
harness, usage, protocol and desktop contracts. It composes one exact original
mutation with independent native replacement-history/cleanup proof, unsent
prompt editing, immutable previous history/usage and original account/Worker
ownership. System 62 / Worker 36 activate no filesystem rewind or migration.
