# DeliDev schedules and occurrence contract

## Scope
Issue #964 requires server-owned recurring execution, schedule CRUD/pause/resume, next-run inspection, Run now, occurrence history, Overlap/Skip/Wait, offline skipping and reference-deletion disabling. This contract preserves that complete boundary. The current increment implements the private calendar/domain/storage primitives and Worker availability evidence; the scheduling coordinator, lifecycle RPCs/CLI and native scheduled-session acceptance remain pending. No public scheduling capability or real scheduled execution is claimed from these primitives.

## Runtime and Language
Go, the existing server-owned SQLite database, and `github.com/robfig/cron/v3` at the existing pinned version `v3.0.1`, now a direct dependency. Go's bundled IANA timezone data is linked for platforms without a system timezone database. Native Git or harness processes are never launched by domain validation or database primitives.

## Users and Operators
The server owns configuration, timers, accepted occurrence identity and history. An authorized owner/client will configure schedules; Workers retain execution-machine ownership. Local requires independently authenticated originating Worker provenance. A viewing client, supplied machine UUID or loopback endpoint cannot establish that provenance. General Chat schedules are excluded.

## Interfaces and Contracts
`ScheduleDefinition` contains name, enabled state, prompt, project, Agent Worker, execution machine, Worktree/Local selection, ordered explicit repository starting references, input mode, cron expression, IANA timezone and overlap policy. Defaults are Worktree, Execute and Overlap. This is editable selection, without resolved account, templates, effective native settings or origin assertions. Local forbids starting-reference overrides, as ordinary Local session creation does.

The calendar uses five fields: minute, hour, day of month, month and day of week. Ranges, lists, steps and named months/weekdays follow the pinned parser. Inline timezone overrides, seconds fields, interval descriptors and implicit `Local` timezone are rejected; the explicit timezone field is authoritative. `NextRun` returns the first absolute UTC instant strictly after its supplied boundary. DST gaps are skipped and repeated wall times are distinct UTC instants. The pinned parser stops searching after five years; an adapter searches overlapping bounded windows over one Gregorian cycle so valid leap-day schedules spanning a non-leap century remain reachable. Impossible calendars return a typed error without inventing a due time. Remove that adapter when an upgraded parser supplies the required search horizon natively.

`Schedule` adds server-owned configuration revision, creator identity, optional non-secret Local origin, next UTC due instant, last accepted occurrence sequence and disabling problem. Enabled schedules require a matching calendar instant and no disabling problem. Timer progress cannot change the configuration/origin/problem or decrease/reset occurrence order. Configuration updates use a separate monotonic configuration revision in addition to the ordinary resource revision; advancing a timer does not rewrite the configuration revision used by historical occurrences.

`ScheduleOccurrence` retains the original schedule/configuration revision, transaction-allocated sequence, cron/manual trigger, absolute due and acceptance times, initiator, immutable creation selection and Local origin, accepted overlap policy, state, optional owned session, typed skip/failure result and terminal time. Cron retains its exact minute boundary; Run now uses its server acceptance time. Canonical UTC due encoding supports a unique cron-instant index. The internal creation selection is not session provenance: only the coordinator can create a `SCHEDULED` session with its reference-only `ScheduleOrigin`. Ordinary session creation continues to reject a caller-selected scheduled source.

States are waiting, active, succeeded, failed, stopped and skipped. Waiting owns no session until it is dispatched and is permitted only under Wait. Active owns exactly one session whose schedule/occurrence/configuration/trigger and project/Agent/machine/workspace all match. Active publication cannot adopt an ordinary or foreign session. Only the supported waiting-to-active/failure and active-to-terminal transitions are allowed; terminal records cannot be rewritten or returned to execution. Native cleanup and the independent session outcome must be established by the coordinator before terminal publication; these storage transitions alone do not prove completion.

Future coordinator requirements remain normative: Overlap creates independent Worktree sessions and explicit same-machine Local sharing. Skip retains a skipped occurrence while a previous run is active. Wait retains accepted immutable occurrences and releases the earliest sequence only after preceding runs end. Approval/input waiting and disconnected uncertain execution remain active. Accepted Wait work survives server restart and schedule edits/pause/deletion. Offline due runs receive skipped history without creating a catch-up execution backlog. The current storage ceiling is 1,000 accepted waiting occurrences per schedule; overflow is atomic and never evicts prior work, and the coordinator must retain a visible bounded-capacity failure/skip rather than silently discarding a due instant.

## Storage
SQLite schema 11 adds unique `(schedule_id, sequence)` and cron `(schedule_id, due_at)` indexes, bounded due/pending/history lookup indexes, and session source-link lookup. `AppendScheduleOccurrence` validates the original resource revision/configuration, immutable selection/origin/policy, exact due instant and next calendar step, then writes timer/sequence and occurrence within one transaction. Existing mutation receipts provide durable request replay. An independently detected duplicate cron instant rolls back the timer and sequence even if the timer had been rewound. Occurrence updates preserve original selection bytes and session ownership.

History is ordered by acceptance sequence with bounded record and aggregate-byte pages. Wait-head lookup uses the same sequence. Occurrences remain independently retained after schedule configuration deletion. A failed mutation, conflicting revision or waiting-capacity error cannot partially advance the timer, create a session or remove an accepted occurrence. Public receipt reconstruction, pagination scope/cursors and coordinator session/job creation must be integrated through the dedicated product boundary before activation.

Migration preserves a validated, synchronized pre-migration backup and is atomic. Existing schema-10 inbox entries are left intact; older inbox migrations run only for older versions. Unrecognized pre-schema-11 schedule/occurrence entities fail migration while preserving the database and backup, because earlier versions supplied no accepted execution contract for those records. Fresh databases and migrated databases both receive the same schema. Historical migration fixtures remove the schema-11 additions before representing an older database.

## Worker Availability
`worker_instances.available_since` records the beginning of the current continuous observed 45-second lease. A different process instance, a lease gap or a backward clock step resets it. Heartbeats retain that beginning only while continuity remains supported by observations. Existing leases migrate conservatively from their last observed heartbeat, without inventing earlier online history.

`WorkerAvailableAt` requires both coverage of the original due instant and a currently valid observed lease. A reconnect after the due instant cannot backdate availability. This is bounded connection evidence, not physical uptime attestation; the scheduling coordinator must independently validate machine/device authorization, server uptime at the due boundary and accepted session authority. Already accepted Wait occurrences use current availability when eventually released, rather than being discarded for an intervening disconnect.

## Security
Definition documents cannot carry native effective settings or credentials. Retain Local origin identifiers only and recheck their current authorization at product mutation/dispatch boundaries. Database primitives do not grant new execution, native response, resume or Worker authority. Session provenance is derived from the owning occurrence and cannot be forged by ordinary CreateSession input. Configuration or schedule deletion never authorizes deletion of original Local checkouts or accepted session resources.

## Logging
Calendar/domain/storage validation returns typed safe failures. The future coordinator must log bounded schedule/occurrence/session identifiers, trigger/policy/state and safe error codes through structured logging; prompts, Worker tokens, native diagnostics, filesystem contents and account secrets must not enter logs. No new background scheduling loop is started by this storage increment.

## Build and Test
Run `go test -race -p 1 ./cmds/delidev-cli/...` and `go vet ./cmds/delidev-cli/...`. Calendar tests cover timezone conversion, named fields, DST gaps/repetitions and a leap-century interval. Real SQLite fixtures cover concurrent receipt replay, atomic timer/sequence rollback, independent cron uniqueness, FIFO pages, immutable accepted selection, exact session linkage, full wait-capacity rejection, history retention after schedule deletion/restart, migration backup/rollback and preserved schema-10 inbox records. Worker lease tests cover original availability, late reconnect, expired lease, instance change and clock rollback.

## Dependencies and Integrations
The scheduling coordinator must use the existing session/input/workspace preparation transaction and first-dispatch snapshot/routing boundary. Waiting schedules resolve Agent configuration/templates/account routing at actual first execution; they never freeze a premature account or follow later edits to the already accepted prompt/selection. Stop/Archive, native cleanup, inbox/activity/usage and permanent deletion must preserve ordinary session ownership and independent outcome/recovery. The forthcoming ScheduleService/CLI owns create/list/get/edit/delete/pause/resume/history/Run now; generic configuration writes remain unable to activate schedules.

## Change Triggers
Update this contract, the command and protocol contracts, project/catalog entries, scoped AGENTS and evidence ledger when coordinator execution, lifecycle APIs, recurrence semantics, retention, limits or native scheduled execution change. Passing private primitive tests is not complete schedule acceptance.

## References
- [Complete requirements](cmds-delidev-requirements.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Worker workspaces](cmds-delidev-workspace-contract.md)
- [Pinned cron documentation](https://github.com/robfig/cron/blob/v3.0.1/doc.go)
- [Pinned cron calendar search](https://github.com/robfig/cron/blob/v3.0.1/spec.go)
