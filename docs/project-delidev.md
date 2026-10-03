# Project: DeliDev

## Goal
Run personal AI sessions across projects, accounts, native harnesses, and execution machines with durable single-user ownership. Issue #964 remains normative, with the explicit owner startup/presentation amendment in #1137; implementation and real-environment evidence are distinct.

Issue #1137 makes a fresh main desktop launch sufficient to start/reuse a compatible ordinary local runtime and verify the authenticated product connection. Native-service scope admission remains Go-owned; same-process Stop, renderer lifecycle, saved-window authority and detached server/Worker/session lifetime stay independent. Routine startup/sidebar/tray use product wording; lifecycle, registration and saved-connection controls live in persistent Connection & diagnostics. The [desktop contract](apps-delidev-desktop-contract.md) defines the implementation; record actual platform acceptance and unresolved limits in issue #1137, its pull requests and CI logs/artifacts.

Issue #1088 adds Worker-owned session terminals with native Unix PTY/Windows ConPTY processes, authenticated create/control/output operations and equivalent CLI commands. The desktop provides a bounded text terminal view. Agent Stop preserves terminals; Archive and storage deletion join their independent exact cleanup gate. The [terminal contract](cmds-delidev-terminals-contract.md) and [validation records in PR #1226](https://github.com/delinoio/oss/pull/1226) distinguish fixture/cross-build validation from native platform, remote Worker and release acceptance; this increment does not complete the remaining issue #964 scope.

## Project ID
`delidev`; the Go component is `delidev-cli` and its executable is `delidev`.

## Domain Ownership Map
- `apps/delidev`: desktop presentation and native host.
- `cmds/delidev-cli`: Go CLI, server, Worker, storage and native adapters.
- `protos/delidev/v1`: versioned Connect schema; `protos/gen/go/delidev/v1`: generated Go bindings.
- `packages/delidev-api-client`: generated TypeScript client and bounded transport/synchronization helpers.

New schedule creation adds frequency presets and a creation-only three-section layout under the [desktop contract](apps-delidev-desktop-contract.md#new-schedule-creation-issue-1152), while strict schedule definitions and server recurrence authority remain unchanged.

## Domain Contract Documents
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
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Read-only diagnostics](cmds-delidev-diagnostics-contract.md)
- [Diagnostics presentation](apps-delidev-diagnostics-contract.md)
- [Saved client connections](cmds-delidev-connections-contract.md)
- [Session development-server forwarding](cmds-delidev-forwarding-contract.md)
- [GitHub integration profiles](cmds-delidev-integrations-contract.md)
- [Account lifecycle and AI API Keys presentation](cmds-delidev-accounts-contract.md)
- [Managed Codex subscriptions](cmds-delidev-subscription-contract.md)
- [OpenRouter account OAuth PKCE](cmds-delidev-account-oauth-contract.md)
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
- Sidechat follows the [native read-only contract](cmds-delidev-sidechat-contract.md): retain the complete original parent snapshot and account separately from the child enforcement overlay, reference workspace roots without taking deletion ownership, and join dependent child cleanup before removing parent files. Independent Fork retains its separate lifetime. System 27 / Worker 16 negotiation is independent of ordinary Fork and cannot expand the closed native enforcement profile.
- New repository registration follows the [folder workflow](apps-delidev-desktop-contract.md#projects-repositories-and-configuration-actions) in issue #1142: the native picker grants selection only, fresh same-computer proof binds the Worker, and Go owns read-only canonical inspection and atomic publication. Optional GitHub identity enrichment uses pre-established Worker capability 6/attachment field 3; raw URLs and credentials stay outside renderer/server metadata. Registration shares the current Settings visit disposal policy, while existing edits preserve explicit configuration.
- Settings is a regular `Surface.Settings` destination using the shared rail/category pane and compact drawer under issue #1236. Uninterrupted visits retain category/workflow state across reflow and same-identity reconnect; navigation away disposes Settings-local state and late continuations without changing saved effects or connection-owned conversation/New session workflows. Page-level Escape and active rail reselection preserve the visit. Targeted New Project/Repositories entries and visible destination focus follow the [desktop contract](apps-delidev-desktop-contract.md#settings-screen-and-visit-lifetime-issue-1236).
- Issue #1146 reserves inventory capability 5, entry connection-method field 9, two closed enums and private migration 29 for OpenRouter OAuth. Establish reservations on main before implementation; migration 29 depends on real migrations 26–28. The [OAuth contract](cmds-delidev-account-oauth-contract.md) retains the approved server/CLI/native/desktop scope, server-owned credentials, once-only exchange and original local recovery. The prerequisite activates no OAuth support and leaves the issue open.
- Issue #1235 implements independent subscription service identity with System capability 17 and real migration 28 after accounting 26 and diagnostics 27. Independent PRs retain main-first reservations; the approved integrated PR follows the narrow structure exception. The [subscription](cmds-delidev-subscription-contract.md), [protocol](protos-delidev-v1-contract.md) and [storage](cmds-delidev-storage-contract.md#subscription-retirement-issue-1235) contracts preserve historical attribution, configured-empty deny-all and original native ownership/cleanup while keeping accounts/models/snapshots independent of API Providers. Desktop AI Subscription now uses service-only account creation and the existing explicitly selected Codex login/cancel/authentication-refresh/logout RPC; Models, portable transfer, pricing, usage and diagnostics retain independent attribution. System capabilities 18/19 and Worker capability 8 add original-owner five-minute quota observations, explicit server-wide refresh, default-off account recovery notifications and confirmed reset-credit consumption using a durable official operation key. Sparse observations preserve last success; consumption outcomes never manufacture quota recovery. Metadata or fixture/build support does not establish native or real-account acceptance.
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

Home (Sessions/New Session) keeps independent bounded 50-record reads and connection-owned minimal navigation projections with no row-count cutoff. Accepted Home metadata grows with reached inventory; ordinary query payloads keep their eight-inactive-query bound. Preserve original resource/project identity, selected conversation/drafts, scope generation cancellation, and the mounted local/saved server controllers. Other destinations retain manual paging. See the [desktop contract](apps-delidev-desktop-contract.md).

- Workspace storage and Codex forks share source ownership exclusion: forks require present storage at acceptance, claim and publication, and storage waits for unresolved fork jobs. Stored workspaces require explicit restoration before a fork.

- Workspace storage and session terminals share transactional ownership exclusion: storage waits for every terminal's independently verified process cleanup, while terminal creation, input/resize and non-close dispatch require present storage. Original close and cleanup authority remains available under the [storage](cmds-delidev-storage-contract.md) and [terminal](cmds-delidev-terminals-contract.md) contracts.

## Device appearance invariant

Device appearance is a native desktop-owned preference shared across local and saved-server windows, independent of every server configuration, pairing, backup and configuration transfer. Its controller stays above connection state and follows the [desktop appearance contract](apps-delidev-desktop-contract.md#device-appearance-issue-1238).

## Session terminal deletion invariant

Interactive terminals belong to the prepared session's original Worker and primary workspace. Accepted input bytes remain private dispatch data for that Worker; all public terminal resources retain pending metadata while omitting those bytes. Agent Stop preserves them. Archive and permanent deletion join their independently confirmed process-tree cleanup; deletion cannot dispatch workspace removal before that join or bypass it during final purge. An accepted uncertain close report atomically retains the cleanup obligation under a fresh close identity; exact receipt replay cannot replace that next assignment or release workspace deletion. Replacement Workers may adopt only exact close reconciliation after a fresh current-instance server claim, preserving original evidence and synchronized shutdown output-loss observations without creating a shell or replaying controls. Missing shutdown observations conservatively expose possible abandoned output, independently of cleanup proof. A synchronized cleanup acknowledgement permits local terminal ownership metadata retirement, with interrupted retirement retained for local retry even after database deletion. Replacement acknowledgement recovery reads only exact committed receipts under the same current device/machine; it cannot revive native work or expose resource content. See the [terminal contract](cmds-delidev-terminals-contract.md) and [storage contract](cmds-delidev-storage-contract.md).

Creation synchronizes the original process-owner index before native-start
intent. Pre-native restart reconciliation requires that retained index;
missing or changed ownership cannot establish cleanup or authorize replay.

Desktop terminal history reads, polling, manual refresh and selection require
advertised system terminal support. Unknown or unsupported status cannot issue
terminal reads or expose cached terminal errors; see the
[desktop contract](apps-delidev-desktop-contract.md).

- Worker network bootstrap pins an original protected recipient and pending/paired identity, then independently reconciles desired/effective generation through authenticated control. Stale state blocks fresh work while original active generation and cleanup remain immutable. The separately claimed Codex API/title tunnel keeps upstream credentials in Go, verifies native proxy/shell policy and joins before cleanup; public native-route use is independent of control readiness and provider success. Follow the [network contract](cmds-delidev-network-contract.md).
- Network product controls share existing Server preferences/Runner Device settings and exact Connect/CLI semantics; protected local preparation/import has a closed trusted-window bridge to Go. System capability activation, Worker negotiation, encrypted cache publication, authenticated control readiness and actual native route use remain independent. Settings visits cannot retain late callback authority or start/restart a Worker implicitly.

Codex subagent settings require independent server/Worker capabilities, immutable canonical child model identity and same-account relay narrowing under `cmds-delidev-subagents-contract.md`; requested defaults never establish an observed child model or independent child control.

Issue #1208 extends native subagent observation to OpenCode 1.18.32 foreground tasks under the existing subagent contract and independent System/Worker capabilities. Preserve the one-level dual task/child ownership proof, same account/model, separate usage, original Stop/cleanup and paused child-bearing completion; implementation and native/account/platform acceptance remain separately recorded in the integration PR.

- Bounded Unix plain-text OpenCode General Chat Fork uses independent System 26 / Worker 15, native ID-clone/history proof, explicit no-change relocation, preparation without inference and independent copied files. The [fork contract](cmds-delidev-forks-contract.md) owns this profile; Codex support does not imply OpenCode or Sidechat authority.

- The canonical 35-ID API provider registry preserves original wire IDs and shares product ordering across RPC/CLI/desktop. Real default migration 30 follows actual 26–29, retaining existing identities and deletions. Fixed native/private inspection authority is owned by the [provider inspection](cmds-delidev-providers-contract.md), [catalog](cmds-delidev-catalog-contract.md) and [activation](cmds-delidev-provider-activation-contract.md) contracts; discovery grants no inference or subscription authority.
