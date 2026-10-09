# DeliDev direct execution startup

## Scope

This amendment owns native execution startup in
`cmds/delidev-cli/internal/domain`, `internal/server`, `internal/worker` and
`internal/harness`, with desktop presentation in `apps/delidev/src` and the
Worker RPC in `protos/delidev/v1/worker.proto`. It covers first execution,
continuation, scheduled execution and existing Fork, Compaction and title
operations. It does not activate an unsupported operation or account family.

The owner approved removal of manual Worker inspection, separate execution
probes, `--version` subprocesses and version-number admission gates on
2026-10-07. PR #1645 established the complete protocol reservation on main at
`03429673f2976ab52b98c613f9d3cc1ff4c41d84` before implementation. The active
schemas use those exact numbers; the prior reservation itself granted no execution
or credential authority.

## Runtime and Language

Go owns acceptance, native processes, protected credentials and publication.
React/TypeScript renders authenticated Connect observations through the existing
client and query owner. No native-host or SQLite migration is introduced.

## Users and Operators

The server owner and paired clients submit execution. Only the original
authenticated execution Worker can initialize and report its assigned process.
Users retain explicit account connection, installation and executable-path edits.

## Interfaces and Contracts

### Main-first allocation closure

Under issue #964, PR #1645 reserved System `EXECUTION_STARTUP_V1 = 43` and Worker
`EXECUTION_STARTUP_V1 = 23`. Preserve System 42 and Worker 22 for the separate
inline-model proposal in PR #1642, including while that reservation is pending.
The ledger preserves these declaration reservations with `newDeclaration: true`:

| Declaration | Fields or enum members in numeric order |
| --- | --- |
| `ReportExecutionStartupRequest` | `mutation` 1, `machine_id` 2, `instance_id` 3, `observation` 4 |
| `ReportExecutionStartupResponse` | `observation` 1, `replayed` 2 |
| `ExecutionStartupObservation` | `state` 1, `phase` 2, `harness` 3, `native_version` 4, `executable_sha256` 5, `protocol` 6, `problem_code` 7, `correlation_id` 8, `input_delivery` 9, `cleanup` 10 |
| `ExecutionStartupState` | UNSPECIFIED 0, READY 1, FAILED 2, UNCERTAIN 3 |
| `ExecutionStartupPhase` | UNSPECIFIED 0, RESOLVE 1, LAUNCH 2, INITIALIZE 3, SETTINGS 4, INPUT 5, EXECUTION 6, CLEANUP 7 |
| `ExecutionStartupInputDelivery` | UNSPECIFIED 0, NOT_SENT 1, CLAIMED 2, ACKNOWLEDGED 3, UNCERTAIN 4 |
| `ExecutionStartupCleanup` | UNSPECIFIED 0, CONFIRMED 1, UNCERTAIN 2 |

Enum member names use their complete upper-snake declaration prefix. The
reservation changed no active schema, generated binding, capability advertisement,
assignment, inference route or native operation. Runtime activation now exposes
System 43, Worker 23 and the original-worker ReportExecutionStartup RPC.

### Execution behavior

The negotiated v4 assignment replaces mandatory pre-inspected installation
evidence with an immutable startup selection. Historical v1/v2/v3 assignments
retain their exact attribution and remain readable. Adapter capabilities report
implemented code, independently of installed executable or account readiness.
Codex completion checkpoints compare against the original accepted assignment,
including its original installation representation. Locally resolved executable
and observed version metadata stay in the original startup evidence; they never
rewrite the assignment or its digest. Checkpoint retention failures log a closed
stage and error code without native content or credentials.
Workers lacking the new capability receive no new execution assignment; clients receive an
update requirement rather than an instruction to run manual inspection.

Acceptance retains atomic input, configuration, routing and job ownership. The
Worker resolves only the selected harness, using its explicit path or its own
PATH when the path is omitted. Failure of an explicit selection never permits
PATH, harness, account or Worker fallback. No standalone protocol probe or
`--version` child runs before execution or merely on Worker attachment.

Initialize the actual original process once and validate its protocol, applied
model, permissions, workspace and credential mode before sending input. Versions
are optional observed metadata, never a minimum, maximum or exact-match execution
gate. Unknown versions stay unavailable; never substitute a baseline version.
Unsupported protocol shapes, methods and feature semantics fail explicitly.
Version-dependent interpretation must derive from actual negotiated/observed
protocol evidence. Existing schema, ownership and bounded-content checks remain.
Grok Build retains its bounded native configuration-integrity reads for effective
settings and configuration sources. These reads do not discover Worker readiness,
probe a separate ACP session or run a version command.

`ReportExecutionStartup` binds the exact claimed job/revision and original
machine/device/instance/server epoch. Its receipt is durable and exact replay
does not launch, grant credentials or send input again. An initializing API relay
cannot perform provider inference; successful original initialization and fresh
authorization activate only that assignment. Subscription execution preserves
its original protected generation and account lease, with no login or exchange.
Startup observation never substitutes for selected-account authority.

Continuation and native source operations additionally retain original
checkpoint, executable identity, account, Worker, workspace and history checks.
No installation refresh can silently replace historical native ownership. Title
initialization follows its actual title process; title failure cannot prevent the
accepted conversation execution. Existing feature and platform limits remain.
Source validation preserves the original assignment bytes, including legacy
v1/v2/v3 installation identity. It does not convert a completed source to v4 or
require Worker 23 for an already supported legacy source operation; each native
operation retains its own capability, protocol and original-executable checks.

### Failure presentation and retry

Remove the first-session prerequisite checklist and required inspection controls
from ordinary execution. Preserve optional explicit diagnostics and path editing.
Show an immediate session-owned failure summary with a concrete corrective action. Issue #1699 keeps available safe cause metadata and the original session/Runner controls in the owning surface; required setup does not navigate to another category. Preserve the single original controller, task draft and exact Runner identity. Unsupported installation or remote administration uses concrete manual steps and a supported explicit recheck, without new repair authority.
Optional details expose phase, observed version, stable code, original log/
correlation reference, input delivery and independent cleanup classification.
Copy only validated metadata; opening or copying details performs no inspection.
Failure guidance describes delivery and cleanup independently. Acknowledged input
and confirmed original cleanup must not be described as uncertain. Preserve safe
cause-specific correction alongside original-execution recovery guidance. These
settled observations do not prove the execution outcome or grant resend authority.

Retry is explicit and available only after positive input-not-sent and original
cleanup proof. The existing revision-checked Resume lane creates a distinct
attempt, retaining the failed history and original configuration/account/Worker.
Uncertain delivery or cleanup requires original recovery, not another send.
Retain original startup failure separately from a later cleanup failure.

Successful resolution leaves fresh execution process-index publication to the
workspace claim. A definite resolver failure before workspace/native admission
may retain a fresh empty index only through exclusive creation and synchronized
parent publication. Existing scopes, failed creation/synchronization, original
history mismatch and uncertain executable-journal publication remain uncertain.
Cleanup reconciliation requires this attempt's original index ownership; an
empty retained directory alone cannot grant proof. Resolution success advances
to Launch before workspace/runtime admission; actual native opening advances to
Initialize.


Keep the existing sidebar, composer, semantic color tokens and information drawer.
Support contained keyboard navigation, Escape, opener restoration, narrow
overlays, long-value wrapping and 200% zoom. Missing setup links directly to its
own settings; normal successful execution shows no prerequisite or ready score.

## Storage

Use bounded optional startup metadata in existing session JSON and mirror it into
the original terminal job JSON. Retain immutable ready evidence and independent
later failure/cleanup facts. Keep the original assignment
unchanged; do not fabricate native acceptance or rewrite historical snapshots.
Observation metadata is limited to 4 KiB. Existing durable request receipts and
native process/outbox journals preserve restart and unknown-write outcomes.
No new table, SQLite migration or configuration import authority is introduced.

Earlier development builds persisted optional session `start_preparation` with
phase `checking-installation`, `checking-execution-support`, `waiting-dispatch`
or `failed`, and an optional original `discovery_job_id`. Retain this typed
historical observation when reading and saving those sessions. Strict decoding
still rejects unknown fields, duplicate keys and incompatible JSON types. The
field cannot initiate inspection, select an account, authorize execution or
permit retry; current startup and original native ownership remain authoritative.
Account deletion must inspect retained session references without rejecting this
known historical field or discarding history. No migration or conversion is needed.

## Security

Preserve configured-empty deny-all policies, account validation and connection
generations, protected credential ownership, leases, budget checks, original
Local authority, revision/reference checks and independent cleanup. First resolve
and recheck the executable identity; later history-bound operations cannot choose
a replacement. Redact raw native/provider output, credentials, paths, prompts and
environment values from startup reports and logs. Optional existing machine path
editing retains its separate owner/client authorization.

## Logging

Use structured `slog` records for startup, phase transitions, safe failures,
original cleanup and explicit retry admission. Record operation/job/session/
execution correlation, closed phase/code, optional validated version and separate
delivery/cleanup outcomes. Never log raw responses or reconstruct missing proof.

## Build and Test

Check protocol allocations and generated parity with `pnpm proto:check`. Run
`go test -race ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...` and
`pnpm test` in `apps/delidev` after implementation. Ordinary fixtures use private
temporary accounts/state and cannot invoke inference or user login.

Cover absent inspection records across manual/continued/scheduled execution,
one original protocol initialization without version or separate protocol-probe
children, compatible protocol
responses from versions outside former gates, incompatible protocol and settings,
missing executables, denial, timeout, revocation, Stop/Archive races, duplicate
requests, reconnect/restart and uncertain delivery/cleanup. Verify old Worker and
assignment compatibility, original attribution and sanitized failure details.
Check 1440x900, 960x640, 640x480 and 200% zoom. Record source revision, commands,
results and limits in PRs/issues/CI; native and platform acceptance remain separate
from fixtures, compilation and packaging.

## Dependencies and Integrations

Reuse Worker Connect streams, native adapters, execution grants, original
subscription leases, existing job storage and desktop Connect Query. This
implementation adds no external dependency.

## Change Triggers

Changes update the owning harness/session/desktop/protocol/diagnostics/title
and subscription contracts, the project index and applicable scoped/root AGENTS.
Preserve historical declaration numbers and evidence boundaries. Update the
runtime contracts only with the complete implementation and its actual results.

## Desktop inline remediation

The conversation presents the original closed startup classification, safe cause and supported next actions beside the failure. Optional metadata disclosures do not hide the cause or make diagnostics required. Exact Runner setup presents the retained original machine controller in the owning task, without automatic inspection or changed selection. Session recovery exposes the original SessionTools confirmation and retained request through the same conversation; narrow layouts may open its original Info drawer from a local recovery action. Presentation moves neither cancel nor recreate owned requests. Retry continues to require the original explicit positive no-send and confirmed cleanup proof; malformed or uncertain evidence remains blocked pending original recovery.

## References

- [DeliDev project](project-delidev.md)
- [Native harnesses](cmds-delidev-harness-contract.md)
- [Session ownership](cmds-delidev-sessions-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Structure and reservations](cmds-delidev-structure-contract.md)
- [Desktop](apps-delidev-desktop-contract.md)
- [Diagnostics](cmds-delidev-diagnostics-contract.md)
- [Repository defaults](repository-defaults.md)

## Positive image rejection before input — issue #2048

ExecutionStartupObservation field 11 carries the closed ExecutionStartupFailureKind enum: zero UNSPECIFIED is omitted, and IMAGE_INPUT_REJECTED is one. This optional classification adds no capability or migration and preserves legacy observation bytes and generic behavior. A generic Unsupported error does not prove image provenance. Only the original Codex image-preparation branch before turn/start may issue an opaque proof bound to the original request, input and complete ordered input digest. Successful sends, turn transport failures, acknowledgments and attachment-resolution failures carry no such proof.

The Worker retains an exclusive synchronized metadata-only claim bound to the original authenticated server/device/instance, immutable assignment revision/digest, execution/input/request and input digest. Classification requires that unchanged claim plus separately confirmed process, workspace and protected credential cleanup. Missing, changed or uncertain evidence retains claimed/uncertain delivery and original recovery. It never retries by observation.

The server accepts IMAGE_INPUT_REJECTED only for Failed/Input/Codex/Unsupported/NotSent/confirmed cleanup, original nonempty images, the unchanged ready process identity and native thread without an acknowledged turn, and the exact claimed queue execution/request/full input. Keep original public mutation receipts and assignment revisions. Normal finalization retains the original prompt, skill bindings and ordered image references as rejected, frees pending capacity and pauses without recovery. Explicit retry retains existing original-selection ownership checks. Desktop guidance identifies image support requirements, preserves visible original rejected input/images and never substitutes attachments or starts another send.

## Operational startup progress — issue #2120

System `SESSION_STARTUP_PROGRESS_V1 = 74` and Worker
`SESSION_STARTUP_PROGRESS_V1 = 50` own the separately negotiated
`ReportSessionStartupProgress` RPC. System 43/Worker 23 and all original startup
observations remain unchanged. The request binds mutation/job/revision,
machine/instance, session, optional execution, monotonic sequence, one closed
workspace operation or existing native phase, running/completed state and
optional original repository ID/ordinal/count. All twelve request fields, the
replayed response field and both closed enums are recorded in the allocation
ledger. These observations grant no input, credentials, execution, retry or
cleanup authority; unavailable telemetry never rejects ordinary execution.

The server authenticates the original claimed job/device/instance/server epoch,
current preparation or execution and original applicable repositories before
publication and exact receipt replay. Stop, terminal settlement and departed or
changed owners reject later reports. Reports change only Session JSON and its
existing resource events, never the job revision, original assignment or native
startup/terminal receipts. Each current preparation/native attempt retains one
entry per original applicable operation, at most 100 repositories, 403 workspace
entries and six native entries. There is no observation log or migration.

Workspace callbacks describe actual setup, inspection, clone, reference,
checkout, verification and manifest publication boundaries. Completion follows
successful return only. The original interactive child reports launch completion
only after its retained Resume barrier; native adapters report initialization
completion from validated original responses and settings work at its actual
validation boundary. A successful original settings receipt confirms settings
alone, never input delivery. Noninteractive helper processes do not report agent
launch. The Worker uses one bounded nonblocking queue and joined report owner per
job; a failed report permits one exact receipt retry within the same short
transmission deadline. It never retries an operation or renews authority.

Metadata and structured logs contain only safe IDs, closed stage/state/sequence
and classified errors. Paths, URLs, prompt/credential/native/Git content,
percentages and ETA are excluded. Retained failed-stage context supplements the
original failure and independent cleanup guidance without animation or controls.
