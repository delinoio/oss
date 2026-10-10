# DeliDev Claude native context and manual compaction contract

## Scope

The feature owns the authenticated native-context read and manual compaction action
for Claude Code 2.1.236 API sessions. Implementation belongs to feature-owned
`session_compaction.go` files in `internal/domain`, `internal/server`,
`internal/worker`, and `internal/cli` under `cmds/delidev-cli`, plus the context
projection in `internal/server/session_context.go`. Session RPC declarations stay
in `protos/delidev/v1/session.proto`; generated Go, TypeScript and Connect Query
bindings follow the canonical pipeline. Existing automatic compaction publication
and native manual-action/history validation remain independent owners.

The shared-compaction reservation the originating change landed on main at `574c1a92c` with
identifiers and a common contract for the feature. This Claude implementation adds
no existing-message field or shared enum member, consumes none of that PR's
reserved numbers, and introduces no migration. Its session-owned context enum
does not advertise support for another harness. Before any later shared activation,
use those established main reservations and compose this profile's operation,
action/job identity, eligibility and checkpoint proof with that common contract.
The dedicated Claude contract path preserves the planned common document's owner.
Its planned shared action resource and response join remain future extensions;
the current Claude response retains its original durable job and request/action
identity. Common reservations do not advertise support for Codex or OpenCode.

## Runtime and Language

Go runs the server, CLI and outbound Worker. The selected native executable must
be the original verified Claude Code 2.1.236 installation. JavaScript clients use
the generated protobuf and Connect RPC interfaces. Other harnesses and Claude
subscription sessions have no manual-compaction capability in this profile.

## Users and Operators

Owners and paired clients can inspect context and explicitly request compaction.
Only the original machine's current paired Worker can claim, register relay
authority and report an action. A context observation never grants execution.

## Interfaces and Contracts

- `SessionService.GetSessionContext` takes a session UUID-v7 and returns a bounded
  JSON observation plus closed `SessionContextCapability` values. Native
  observations and currently eligible manual compaction are separate capabilities.
- `SessionService.CompactSession` takes the ordinary mutation envelope: session
  ID, exact expected session revision and UUID-v7 request ID. The request ID is
  also the distinct native action identity. Its receipt references one durable
  `compact-session` job; exact retry reads that job's current state and never
  creates another command.
  The mutation digest includes the original owner/client principal; another
  authorized client cannot borrow its receipt, and revocation is checked before
  replay. No credential value enters this identity.
- CLI parity is `delidev session context --id ID` and
  `delidev session compact --id ID --revision N [--request-id ID]`. Reads need no
  mutation ID. Preserve the accepted request ID on retry; inspect the returned
  job and event stream for settlement.

Acceptance requires the exact preceding successful, cleaned version-2 execution,
settled native run/command/tool/task/callback state, unchanged immutable selection,
current account/connection, live owning Worker, ready preparation and budget gate.
The transaction queues the action and pauses dispatch before later input can be
claimed. Optimistic revision conflict rejects a request that lost that boundary.
No conversation input, routing choice or execution outcome is replaced.

The Worker restores the original closed history without submitting input, advances
the workspace lease to the action, obtains fresh digest-only relay authority,
synchronizes the command claim and sends `/compact` once. Native validation requires
the original command echo, fresh applied settings, independent `compact_result`,
exact boundary/summary on success, original local-command result, completed command
and final idle. An outer `success` result with `compact_result=failed` remains failed.

The result preserves original native envelope IDs, outer kind/error separately from
compact status, exact Before and optional After/duration/cumulative-dropped values,
preserved-message/segment/logical-parent provenance and the original summary anchor
and text. Missing measurements remain unavailable; a reported zero remains zero.
There is no current-utilization estimate or billing derived from cumulative native
usage, model ledgers or provider traffic. Existing response usage is unchanged.

The context JSON has `session_id`, optional `execution_id`, nullable `current_tokens`,
`automatic_boundary`, `automatic_summary` and `manual_action`. This profile always
returns `current_tokens: null`; historical boundaries cannot supply current tokens.
Resource projections carry decimal revision strings and observation timestamps.
Automatic projections retain original progress documents; manual projection carries
action/execution identities, job state, stable problem, original result and acceptance/
finish timestamps. It excludes the action assignment's private paths and old prompt.
The read is one authorized transaction with a two-second deadline and 1 MiB bound.

A clean success restores only the pre-action ready intent. Preexisting pause, Stop,
Archive or recovery cannot be cleared by an action result. Other completion paths,
including forwarding cleanup, cannot archive over unresolved action ownership. A settled native failure
retains its checkpoint, preserves the preceding conversation outcome, pauses FIFO
and requires explicit Resume before later input. Queued Stop/Archive cancels the
undispatched action without claiming native effects. Claimed cancellation, lost
delivery, invalid output or cleanup uncertainty retains recovery and cannot resend.
A valid report racing after claimed cancellation stays as observed job evidence;
it does not replace the prior usable checkpoint, release ownership or finish Archive.
Worker revocation releases canceled undispatched action ownership without inventing
native recovery, preserves the preceding checkpoint and leaves FIFO paused. Claimed
actions retain recovery-required ownership. Account disconnect cancels both ordinary
execution and manual-action jobs.

### cmds/delidev-cli/internal/cli constraints

- Provide `session context --id` and `session compact --id --revision` through the authenticated Connect APIs in `cmds-delidev-claude-compaction-contract.md`. Context is read-only; compaction retains the supplied UUID-v7 request identity and returns the durable job without claiming native success.

### cmds/delidev-cli/internal/domain constraints

- Manual compaction follows `cmds-delidev-claude-compaction-contract.md`. Keep its distinct action/job/reference and original compact status separate from conversation input/outcome and usage; validate exact immutable restore assignments, native provenance, missing versus zero measurements and explicit Resume after failure.

- Permanent-deletion copies for manual compaction retain a distinct original action UUID, never a replacement conversation execution ID. Reject missing, malformed or mixed action ownership; existing deletion plans retain their exact bytes and digest. Follow `cmds-delidev-claude-compaction-contract.md`.

### cmds/delidev-cli/internal/server constraints

- Native context and manual compaction follow `cmds-delidev-claude-compaction-contract.md`. Owner/client-only acceptance atomically pauses FIFO and retains one revision/request-bound job; reference-only receipt replay must support compaction. Preserve prior outcomes, exact native result/cleanup and queued versus claimed cancellation, including account disconnect. Worker revocation releases canceled undispatched compaction ownership while keeping FIFO paused and the preceding checkpoint intact; claimed jobs retain native uncertainty. Context reads cannot infer current utilization or expose private assignment paths.

- Manual-compaction mutation receipts bind the original owner/client principal as well as session/revision/request. Recheck authorization before replay; another authorized client cannot reuse the accepted receipt. Keep credentials outside its digest input under `cmds-delidev-claude-compaction-contract.md`.

### cmds/delidev-cli/internal/store constraints

- Manual-compaction deletion plans derive the original action UUID from its immutable claimed assignment. Include every original owning Worker before purge; ordinary execution IDs cannot identify action runtimes or checkpoints. Follow `cmds-delidev-claude-compaction-contract.md`.

- Manual compaction uses additive session/job JSON and existing atomic events/receipts under `cmds-delidev-claude-compaction-contract.md`. Preserve original execution history and schema versions. Account cancellation inventories must include action assignments by their exact original account; queued cancellation is distinct from claimed native cleanup. Every Archive completion remains pending while a distinct compaction claim owns cleanup.

### cmds/delidev-cli/internal/worker constraints

- Include original manual-compaction runtimes and action checkpoints in the same deletion/removal and completed-proof inventory. Join original job, native/process and workspace ownership, preserve unrelated action checkpoints and never remove a replacement merely from an earlier completed receipt. Follow `cmds-delidev-claude-compaction-contract.md`.

- Public manual compaction follows `cmds-delidev-claude-compaction-contract.md`. Restore only the independently pinned cleaned predecessor, advance the exact workspace lease, create and validate the private root/jobs/action-job scope before either registration or command journaling, synchronize one command claim and never resend interrupted starts. Join native/workspace cleanup before exclusive atomic checkpoint retention; replacement validates original journals, exact assignment revision/instance, ownership and digests and keeps failed status Resume-only. Uncertainty logs use only a closed action phase and stable code.

## Storage

Server storage is additive JSON in existing sessions/jobs/events/receipts. Session
`compaction_job_id` marks unresolved ownership, `last_compaction_job_id` preserves
the latest accepted action for reads, and `compaction` pins the most recent cleaned
native checkpoint. Prior jobs, outcomes, input messages, automatic summaries,
revisions and usage remain unchanged. No schema version, data rewrite or migration
is introduced, so no migration backup is needed for this extension.

Private Worker command/registration claims live under the action job directory.
Each claim creates missing private root/jobs/job directories and validates existing
components before its exclusive create; shared or symlinked scopes are rejected rather
than repaired. Compaction does not rely on the ordinary execution publisher to
create this scope.
Registration and command claims synchronize both file and parent before their
dependent side effect. Complete, partial and identical existing claims cannot be
overwritten, removed or retried, even if the outer job journal was lost. Concurrent
claim creation grants at most one writer.
After native and workspace cleanup join, an exclusive atomic
`compaction-checkpoints/<action-id>.json` file binds server/device/instance/job and exact assignment revision, canonical
assignment digest, original assignment/completion and native checkpoint/reference.
Checkpoint v2 also pins canonical registration and command claim digests. The
original private claims must survive with the same action, original execution,
registration request and credential digest before native restoration. Neither the
outer completed journal nor native snapshot can manufacture missing claim proof.
Unproven v1 checkpoints remain preserved and recovery-required; no automatic file
upgrade, claim regeneration or native resend adopts them.
It is bounded to 10 MiB. A successor requires independent server-pinned digests,
the exact original finished/reported Worker journal and original binding/publication
journals; reading or filename discovery cannot create authority. Include these
files and retained native runtimes in coordinated backup/deletion.

Permanent session deletion retains each claimed compaction's original action ID
separately from its conversation execution. The synchronized Worker plan includes
the action runtime, private job claims/journal, process ownership and action-bound
checkpoint. Cleanup joins the original job lock through final report publication,
native/process and workspace owners before removal. Completed deletion receipts
recheck this same inventory; a restored runtime or checkpoint remains pending
without deletion authority, and unrelated session checkpoints are preserved.

Completed action checkpoints can cross a Worker process replacement with fresh
native ownership and relay credentials. Failed checkpoints retain explicit Resume.
An interrupted `started` journal keeps its original report receipt and never executes
again; uncertain action ownership has no automatic recovery or command retry. The
existing conversation-recovery operation must not be treated as action recovery.

## Security

Owner/client reads and mutations enforce current authorization before receipt replay.
Worker credentials cannot invoke either product RPC. Native processes use private
runtimes, reconstructed bounded lookup context and the fixed paired-server relay;
no user configuration, account credentials or ambient proxy is inherited. Manual
relay grants allow only immutable-account Anthropic Messages creation. Revalidate
device, instance, session, cancellation and account generation on every acquisition.
Native diagnostics remain in the private native history; public failures use stable
typed messages. Stop/revocation joins process ownership separately from delivery.

## Logging

Use structured `slog` records for acceptance/replay, action start, verified cleanup
and uncertainty, with session/job/action/request IDs, a closed action phase, compact
outcome or stable code.
Never log prompts, summaries, diagnostic bodies, protocol frames, native paths,
provider keys or relay tokens. Existing native controller phase logs remain separate.

## Build and Test

Required validation is `go test -race ./cmds/delidev-cli/...`,
`go vet ./cmds/delidev-cli/...`, `pnpm proto:check` and generated-client typecheck/test.
Focused fixtures cover atomic acceptance/report receipts, stale revision/auth denial,
settled eligibility, FIFO ordering, zero versus missing counters, outer success with
failed compact status, queued controls, interrupted claims and immutable restore.

`TestManualNativePublicSessionCompaction` is opt-in through
`DELIDEV_NATIVE_CLAUDE_EXECUTABLE`, uses disposable runtimes and a scripted loopback
provider, and covers success, rejection, insufficient history, cancellation and
post-action Worker replacement without resending `/compact`. Ordinary tests never
execute user-installed harnesses. Actual native runs, platform/account limits and
unresolved evidence belong to the independent issue-1093 evidence record.

## Dependencies and Integrations

This action composes the existing pinned Claude manual lifecycle, native history/
checkpoint implementation, workspace continuation lease, Worker journal and relay.
It adds no dependency or image/font asset. The context RPC is the shared boundary
for future desktop presentation; this issue does not invent UI utilization metrics.

## Change Triggers

Update the scoped domain/server/worker/CLI and protocol/client `AGENTS.md` owners,
this contract, project index and independent evidence when action eligibility,
native support, wire projection, checkpoint ownership, cancellation or accounting
meaning changes. Shared enum allocation and schema migrations remain separately
governed; never use another unmerged feature's reserved numeric identity.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [DeliDev project](project-delidev.md)
- [Session contracts](cmds-delidev-sessions-contract.md)
- [Native harness contracts](cmds-delidev-harness-contract.md)
- [Worker process ownership](cmds-delidev-process-contract.md)
- [Usage semantics](cmds-delidev-usage-contract.md)
- [Feature ownership](cmds-delidev-structure-contract.md)
- [Repository defaults](repository-defaults.md)
- The feature
