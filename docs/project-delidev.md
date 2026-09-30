# Project: DeliDev

## Goal
Run personal AI sessions across projects, accounts, native harnesses, and execution machines with durable single-user ownership. Issue #964 remains normative; implementation and real-environment evidence are distinct.

Issue #1088 adds Worker-owned session terminals with native Unix PTY/Windows ConPTY processes, authenticated create/control/output operations and equivalent CLI commands. The desktop provides a bounded text terminal view. Agent Stop preserves terminals; Archive and storage deletion join their independent exact cleanup gate. The [terminal contract](cmds-delidev-terminals-contract.md) and [issue evidence](evidence/delidev/issue-1088/validation.md) distinguish fixture/cross-build validation from native platform, remote Worker and release acceptance; this increment does not complete the remaining issue #964 scope.

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
- [Storage operations](cmds-delidev-storage-contract.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Complete issue #964 requirements](cmds-delidev-requirements.md)
- [Protocol contract](protos-delidev-v1-contract.md)
- [TypeScript client contract](packages-delidev-api-client-contract.md)
- [Desktop client contract](apps-delidev-desktop-contract.md), including [Agent Worker core/optional presentation](apps-delidev-desktop-contract.md#agent-worker-core-and-optional-presentation) and its [issue #1158 evidence](evidence/delidev/issue-1158/agent-settings.md)
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
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Provider and model catalog](cmds-delidev-catalog-contract.md)
- [Native Codex model observations (pending)](cmds-delidev-native-models-contract.md)
- [API provider activation](cmds-delidev-provider-activation-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Session acceptance and input queue](cmds-delidev-sessions-contract.md)
- [Planned shared native session compaction](cmds-delidev-compaction-contract.md)
- [Retained inbox and read-state contract](cmds-delidev-inbox-contract.md)
- [Retained conversation search](cmds-delidev-search-contract.md)
- [Retained activity](cmds-delidev-activity-contract.md)
- [Exact native response usage](cmds-delidev-usage-contract.md)
- [Schedules and durable occurrences](cmds-delidev-schedules-contract.md)
- [Implementation and evidence ledger](cmds-delidev-evidence.md)

Permanent session deletion uses owner/client Connect and equivalent confirmed CLI
commands, durable intent outside SQLite, original Worker cleanup acknowledgements
and managed-backup erasure. Forwarding peers independently confirm cleanup;
offline or uncertain ownership remains pending. Original Local checkouts, other
sessions and shared account profiles are preserved. The [storage contract](cmds-delidev-storage-contract.md)
owns the lifecycle and remaining snapshot/restore/Sidechat integration limits.

## Cross-Domain Invariants
- Execution-device presentation uses `Runs on` for the New session machine selector and `Runner Device` / `Runner Devices` for former Execution Worker labels and messages. Agent Worker and technical Worker terminology remain distinct; machine/protocol/storage IDs, CLI commands, logs, error codes and the `execution-workers` Settings category value stay unchanged. The [desktop contract](apps-delidev-desktop-contract.md) owns the presentation boundary.
- Go owns business logic; clients use authenticated Connect and preserve exact request/revision identities.
- Local and remote operation preserve original native ownership, explicit authorization, credential isolation and uncertainty.
- Real native/account/platform evidence remains distinct from deterministic fixtures, cross-compilation and packaging.
- Keep complete issue #964 requirements and unresolved acceptance items visible.
- Managed database restore preserves current revocations and external permanent deletion obligations, quarantines historical execution and ends the original server epoch. Temporary recovery images participate in permanent erasure; the storage contract owns their lifecycle.
- PR activity preserves immutable source/version/actor metadata across Go, generated clients, CLI and desktop. Attempt success cannot establish verified handling; only a dedicated original verification source can project that outcome.
- Negotiated native usage keeps Codex responses and verified Grok closed inputs as distinct accounting units across Go, CLI and desktop. Grok retention requires original input/history/closure and independently confirmed cleanup; its totals never imply pricing, actual cost or estimated-budget contribution. See the [usage contract](cmds-delidev-usage-contract.md).

Explicit stopped Codex API account selection for issue #1097 spans Go,
authenticated Connect, CLI and generated clients. Original candidates,
provider/model, terminal/cleanup and portable-history gates retain all prior
attribution; new execution requires explicit Resume and a fresh scoped grant.
Subscription/provider/model switching and automatic fallback remain excluded.
See the [sessions contract](cmds-delidev-sessions-contract.md),
[proxy contract](cmds-delidev-proxy-contract.md) and
[issue #1097 evidence](evidence/delidev/issue-1097/stopped-account-switch.md)
for the controlled native A-to-B result and unperformed
desktop/real-account/platform acceptance.

## Change Policy
Update the owning domain contract when behavior changes. Update this index only for ownership, its domain catalog or cross-domain invariants. Record each implementation/validation increment in its own `docs/evidence/delidev/issue-<number>/` file. A validation-only increment does not require editing this index or an AGENTS file.

## References
- https://github.com/delinoio/oss/issues/964
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)
- [Structure and compatibility](cmds-delidev-structure-contract.md)
- [Relocation inventory](evidence/delidev/pr-conflict-structure/document-relocations.json)

## Home navigation invariant

Home (Sessions/New Session) keeps independent bounded 50-record reads and connection-owned minimal navigation projections with no row-count cutoff. Accepted Home metadata grows with reached inventory; ordinary query payloads keep their eight-inactive-query bound. Preserve original resource/project identity, selected conversation/drafts, scope generation cancellation, and the mounted local/saved server controllers. Other destinations retain manual paging. See the [desktop contract](apps-delidev-desktop-contract.md) and [issue #1161 evidence](evidence/delidev/issue-1161/home-navigation.md).

## Session terminal deletion invariant

Interactive terminals belong to the prepared session's original Worker and primary workspace. Agent Stop preserves them. Archive and permanent deletion join their independently confirmed process-tree cleanup; deletion cannot dispatch workspace removal before that join or bypass it during final purge. Replacement Workers may adopt only exact close reconciliation after a fresh current-instance server claim, preserving original evidence without creating a shell or replaying controls. See the [terminal contract](cmds-delidev-terminals-contract.md) and [storage contract](cmds-delidev-storage-contract.md).

Creation synchronizes the original process-owner index before native-start
intent. Pre-native restart reconciliation requires that retained index;
missing or changed ownership cannot establish cleanup or authorize replay.

Desktop terminal history reads, polling, manual refresh and selection require
advertised system terminal support. Unknown or unsupported status cannot issue
terminal reads or expose cached terminal errors; see the
[desktop contract](apps-delidev-desktop-contract.md).
