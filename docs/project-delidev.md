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
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Read-only diagnostics](cmds-delidev-diagnostics-contract.md)
- [Diagnostics presentation](apps-delidev-diagnostics-contract.md)
- [Saved client connections](cmds-delidev-connections-contract.md)
- [Session development-server forwarding](cmds-delidev-forwarding-contract.md)
- [GitHub integration profiles](cmds-delidev-integrations-contract.md)
- [Account lifecycle and AI API Keys presentation](cmds-delidev-accounts-contract.md)
- [Planned OpenRouter account OAuth PKCE](cmds-delidev-account-oauth-contract.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Provider and model catalog](cmds-delidev-catalog-contract.md)
- [Native Codex model observations (pending)](cmds-delidev-native-models-contract.md)
- [API provider activation](cmds-delidev-provider-activation-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Explicit outbound networking](cmds-delidev-network-contract.md)
- [Session acceptance and input queue](cmds-delidev-sessions-contract.md)
- [Same-account Codex session forks](cmds-delidev-forks-contract.md)

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
owns the lifecycle and remaining snapshot/restore/Sidechat integration limits.

## Cross-Domain Invariants
- Issue #1146 reserves inventory capability 5, entry connection-method field 9, two closed enums and private migration 29 for OpenRouter OAuth. Establish reservations on main before implementation; migration 29 depends on real migrations 26–28. The [planned OAuth contract](cmds-delidev-account-oauth-contract.md) retains the approved server/CLI/native/desktop scope, server-owned credentials, once-only exchange and original local recovery. The prerequisite activates no OAuth support and leaves the issue open.
- Issue #1235's planned subscription identity split reserves independent System capability 17 and migration 28. Establish both on main before dependent implementation and activate 28 only after real migrations 26/27, preserving their order. The [structure](cmds-delidev-structure-contract.md), [protocol](protos-delidev-v1-contract.md) and [storage](cmds-delidev-storage-contract.md#planned-subscription-retirement-issue-1235) contracts own this prerequisite; it changes no runtime support. The later reset must preserve historical attribution, deny-all restrictions and unsettled native ownership/cleanup gates while keeping subscriptions independent of API Providers.
- Execution-device presentation uses `Runs on` for the New session machine selector and `Runner Device` / `Runner Devices` for former Execution Worker labels and messages. Agent Worker and technical Worker terminology remain distinct; machine/protocol/storage IDs, CLI commands, logs, error codes and the `execution-workers` Settings category value stay unchanged. The [desktop contract](apps-delidev-desktop-contract.md) owns the presentation boundary.
- Go owns business logic; clients use authenticated Connect and preserve exact request/revision identities.
- Grok public request journals preserve typed payloads plus bounded original JSON bytes for independently verified proposal digests. Server admission precedes public response authority; byte evidence never grants native or filesystem access. Follow the [harness contract](cmds-delidev-harness-contract.md).
- Local and remote operation preserve original native ownership, explicit authorization, credential isolation and uncertainty.
- Native relay handlers join started response-writer cancellation callbacks before returning, so downstream connection reuse cannot inherit an earlier request's deadline mutation.
- Plaintext loopback provider and inference requests require Direct or an explicit bypass. Reject proxied plaintext before connection, preserve verified HTTPS proxy routing, and never silently fall back.
- Explicit outbound proxy credentials remain isolated across provider, inference and GitHub response bodies and metadata: guard header names/values and keep response trailers private under the [network contract](cmds-delidev-network-contract.md).
- Proxy mode/host/port edits cannot carry an omitted existing credential to a new peer. Clear the new profile association or accept explicit replacement input, retaining independently pinned old routes under the [network contract](cmds-delidev-network-contract.md).
- Network profile deletion retains one private server-bound cleanup obligation before public removal. Current authorized owner/client mutations recover it after cancellation, revocation or restart without impersonating the original actor; accepted deletion and absent-profile proof precede native cleanup.
- Managed database restore preserves current network profile/generation/selection authority and the machine metadata required to administer Worker routes, without restoring Worker authorization. It refuses pending private network credential intents under the shared credential gate. Historical database routing cannot reactivate old proxy credentials.
- Network profile publication retains one private server-bound credential intent across vault/SQLite failure. Exact retries and replacement cleanup preserve original actor/input/receipt ownership; uncertainty cannot admit another unpublished native generation.

- Codex fork children survive parent deletion. Explicit Local sharing is limited to user-owned Local source checkouts; managed Worktree sources require independent copies. Go rechecks ownership at acceptance, preparation and publication, and the desktop offers only the supported choice. See the [fork contract](cmds-delidev-forks-contract.md).
- Real native/account/platform evidence remains distinct from deterministic fixtures, cross-compilation and packaging.
- Keep complete issue #964 requirements and unresolved acceptance items visible.
- Manual PR fixes use explicit original evidence/project ownership, eligible sessions and Worker Git authentication; the server lookup PAT never authorizes publication. Native completion requires independent push/cleanup proof before exact evidence handling. Automatic execution and unperformed native/account acceptance remain separate.

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

## Change Policy
Update the owning domain contract when behavior changes. Update this index only for ownership, its domain catalog or cross-domain invariants. Record each implementation/validation increment in pull requests, issues and CI logs/artifacts; do not add repository evidence documents. A validation-only increment does not require editing this index or an AGENTS file.

## References
- https://github.com/delinoio/oss/issues/964
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)
- [Structure and compatibility](cmds-delidev-structure-contract.md)

## Home navigation invariant

Home (Sessions/New Session) keeps independent bounded 50-record reads and connection-owned minimal navigation projections with no row-count cutoff. Accepted Home metadata grows with reached inventory; ordinary query payloads keep their eight-inactive-query bound. Preserve original resource/project identity, selected conversation/drafts, scope generation cancellation, and the mounted local/saved server controllers. Other destinations retain manual paging. See the [desktop contract](apps-delidev-desktop-contract.md).

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
