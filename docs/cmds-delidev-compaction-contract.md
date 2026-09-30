# DeliDev native context and manual compaction contract

## Scope

Issue #1093 owns the authenticated native-context read and manual compaction action
for Claude Code 2.1.236 API sessions. Implementation belongs to feature-owned
`session_compaction.go` files in `internal/domain`, `internal/server`,
`internal/worker`, and `internal/cli` under `cmds/delidev-cli`, plus the context
projection in `internal/server/session_context.go`. Session RPC declarations stay
in `protos/delidev/v1/session.proto`; generated Go, TypeScript and Connect Query
bindings follow the canonical pipeline. Existing automatic compaction publication
and native manual-action/history validation remain independent owners.

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
Account disconnect cancels both ordinary execution and manual-action jobs.

## Storage

Server storage is additive JSON in existing sessions/jobs/events/receipts. Session
`compaction_job_id` marks unresolved ownership, `last_compaction_job_id` preserves
the latest accepted action for reads, and `compaction` pins the most recent cleaned
native checkpoint. Prior jobs, outcomes, input messages, automatic summaries,
revisions and usage remain unchanged. No schema version, data rewrite or migration
is introduced, so no migration backup is needed for this extension.

Private Worker command/registration claims live under the action job directory.
After native and workspace cleanup join, an exclusive atomic
`compaction-checkpoints/<action-id>.json` file binds server/device/instance/job and exact assignment revision, canonical
assignment digest, original assignment/completion and native checkpoint/reference.
It is bounded to 10 MiB. A successor requires independent server-pinned digests,
the exact original finished/reported Worker journal and original binding/publication
journals; reading or filename discovery cannot create authority. Include these
files and retained native runtimes in coordinated backup/deletion.

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

## References

- [DeliDev project](project-delidev.md)
- [Session contracts](cmds-delidev-sessions-contract.md)
- [Native harness contracts](cmds-delidev-harness-contract.md)
- [Worker process ownership](cmds-delidev-process-contract.md)
- [Usage semantics](cmds-delidev-usage-contract.md)
- [Feature ownership](cmds-delidev-structure-contract.md)
- [Repository defaults](repository-defaults.md)
- [Issue #1093](https://github.com/delinoio/oss/issues/1093)
