# DeliDev native session compaction

## Status and ownership

This is the common boundary for issues #1093 (Claude), #1202 (Codex) and
#1203 (OpenCode). Claude owns its implemented settled-boundary product profile.
Codex and OpenCode have independent reserved profiles; a reservation never
advertises support or enables dispatch. The integrated implementation must join
native adapters, durable action/checkpoint ownership, authenticated product
interfaces and replacement-process verification before activating either profile.
The existing harness and session contracts retain their separate acceptance limits.

Go owns acceptance, durable action/job state, native execution and recovery.
The owner and paired clients use one authenticated `SessionService.CompactSession`
operation through the CLI's `session compact` command and the desktop control.
Workers cannot accept owner product actions. Protocol definitions belong to
`protos/delidev/v1/session.proto` and shared types to their existing owners;
Go and TypeScript bindings must be regenerated together when activated.

## Shared reservations

`protos/delidev/allocations.json` reserves these existing-declaration additions
under issue #1203 with #1093 and #1202 as shared consumers:

| Declaration | Member | Number |
| --- | --- | --- |
| EntityKind | ENTITY_KIND_COMPACTION | 32 |
| SystemCapability | SYSTEM_CAPABILITY_NATIVE_SESSION_COMPACTION_V1 | 15 |
| WorkerCapability | WORKER_CAPABILITY_NATIVE_SESSION_COMPACTION_V1 | 5 |
| SessionChange | compaction_job | 9 |

The action is distinct from its Worker job and ordinary input/execution.
The planned request uses an exact session mutation and expected predecessor
execution identity. Its response joins the current session/job and original
action, including reference-only receipt replay. Profile availability must be
checked independently of the common capability; a reservation grants neither.
These allocations must be present on main before dependent implementations use
them, as required by the structure contract.

The existing schema-24 entities, jobs, receipts and cancellation tables provide
the generic durable storage boundary. This prerequisite allocates no migration
and does not change executable schema 24 or the existing reservations 25–27.
If implementation requires additional tables or indexes, reserve that migration
on main before using it. Never skip pending versions with empty migrations or
reinterpret an unknown historical layout.

## Admission and ordering

For Codex and OpenCode, accept manual compaction only after an accepted successful
execution, settled original native history and independently verified checkpoint
and cleanup. Reject active, failed, stopped, paused, archived, recovery-pending,
interaction-pending and child-pending boundaries. Claude's separately proven
settled-boundary profile retains its own failed-compaction and explicit Resume
requirements; these cannot broaden Codex or OpenCode admission.

In one authorized transaction, bind the original actor, UUID-v7 request, exact
session revision, predecessor execution/checkpoint, immutable account/model and
configuration to one action/job. Competing clients cannot create another owner
for that boundary. Serialize admission and dispatch with queued input, Stop and
Archive. Already accepted input wins its race; later input waits behind an
accepted action. Compaction creates no synthetic DeliDev user input and consumes
no ordinary queued input. Stop/Archive/revocation preserve the original action and
independent cleanup obligations rather than manufacturing completion.

Before any native side effect, recheck live account/provider/configuration and
budget authority, original Worker/machine/process ownership, and the native
profile. Restore independently verified original history into a fresh owned
runtime with fresh scoped credentials and unchanged original account/model.
Persist a single native send claim before the request. Lost responses and exact
product retries inspect retained original state without another native request.

## Native observations and successful context

Each adapter must publish its original compaction observations with the original
session, input and native message/part/item ownership through the ordered
acknowledged outbox. Reject foreign identities, changed repeated observations,
premature completion and conflicting lineage. Recognition or decoding does not
establish execution semantics.

Keep native acknowledgment, original lifecycle, provider/native failure,
settlement, resulting history and process/workspace cleanup separate. Only their
independently verified complete proof may publish a checkpoint usable by the
next input. HTTP success, idle, an absent pending list or a closed public flag
alone cannot prove success. Partial, contradictory or unverifiable results keep
recovery required and block successor dispatch; reconciliation cannot resend.

Canonical DeliDev conversation messages and prior execution outcomes remain
retained. Native summaries, retained tails and pruning describe native context
only. Bind every repeated compaction and supported completed tool history into
the complete checkpoint lineage, and verify that lineage in a fresh process.
Do not manually rewrite native database rows to manufacture acceptance proof.
Native context counters preserve measured zero and nullable unavailable values.
Usage retains original provenance and coverage; inherited or overlapping sources
cannot become duplicate charges or fabricated exact totals. Coordination with
the usage work does not imply completion of issue #1099.

## Pinned OpenCode profile and acceptance

Issue #1203 uses OpenCode `1.18.32` and its existing owned API profile. Its native
summarize operation must select the original provider/model explicitly and use
`auto=false` once for manual actions. Automatic parts/events require separate
original-session/input/message/part proof. The pinned native implementation owns
summary creation, native compaction records, retained tails and tool pruning;
DeliDev does not replace it with its own summary.

The pinned [summarize handler](https://github.com/anomalyco/opencode/blob/545f51d26cc39a907d2867492d498d9607ea5fa4/packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts#L273)
and [compaction implementation](https://github.com/anomalyco/opencode/blob/545f51d26cc39a907d2867492d498d9607ea5fa4/packages/opencode/src/session/compaction.ts)
are the native source references; source inspection is not native acceptance.

Required acceptance includes an isolated real native conversation against a
scripted provider, summarize, continuation, two compactions with retained tails
and completed tool history, then restoration into a fresh owned process. Assert
unchanged account/model and preserved original transcript. Negative cases cover
HTTP success with incomplete/failed native settlement, lost response without
resend, competing clients and queue/Stop/Archive/revocation races, malformed or
foreign histories, conflicting observations and incomplete/overlapping usage.
Retain complete lineage without hand-editing native database rows.

Run the required Go race/vet, protocol/generated-client and frontend checks for
their respective implementation changes. Record independent evidence in
pull requests, issues and CI logs/artifacts, separating source inspection,
fixture checks, real native scripted-provider execution and unperformed platform/account
acceptance. No new native version, subscription login, fork/child controls,
active/failed-session manual action, cross-account/model routing or independent
DeliDev summary is part of the OpenCode profile.

## Logging

Use structured metadata-only action/job/request ownership and closed processing
stage/error codes. Keep credentials, prompt/summary bodies, native raw payloads,
private runtime paths and checkpoint bytes out of diagnostics. Retained action
state must distinguish failed and uncertain outcomes without an implicit retry.

## Codex original context items and private action adapter

The pinned Codex `0.151.0` adapter treats `contextCompaction` item start and
completion as a closed ordered context observation under the original owned
thread and active turn. Its `automatic` observations pass through the existing
acknowledged execution outbox, authenticated resource reads and desktop context
presentation. They grant no input receipt, canonical assistant message, additive
usage, current token count or successor checkpoint. The server independently
joins start before completion and refuses terminal settlement or continuation
while a context item remains open. Unknown/foreign turns, changed repeats,
reversed timestamps and reused items fail closed.

The private manual controller accepts an original successful continuation proof,
unchanged effective settings and a complete settled native history. It checks
native goals/queue absence and refuses child-bearing histories. It claims one
`thread/compact/start` request in memory before writing to the native wire; the
Worker's durable claim remains independently required before using this primitive.
A lost/rejected response never clears the native attempt. Acknowledgment does not
authorize new input or retention. Success requires the original new native turn,
one completed context item, native terminal state, independently read complete
history and an unchanged original history prefix. This private primitive alone
cannot activate the product's reserved manual capability.

Retain distinct live and durable context item identities. The pinned
[history projection](https://github.com/openai/codex/blob/78c290807ce710180111df227df3b7a4fe845452/codex-rs/app-server-protocol/src/protocol/thread_history.rs)
replays `ContextCompacted` into an `item-<index>` identity while excluding live
`ContextCompaction` item lifecycle upserts. Do not replace the live ID or pretend
they are equal. Their join requires the same original turn, exactly one new
compaction-only history turn and the complete unchanged prior history. Each
repeated action retains both IDs, original action/turn identities and the whole
history digest. Reject incomplete, reordered, foreign, reused or child-bearing
history rather than reconstructing it.

Private retention pins the original rollout within the original protected native
home. Verify its file digest before launching a replacement. Native Resume may
append settings metadata; independently compare complete native history and
unchanged original effective settings after Resume before another command or
input. Retain the original ordinary input digests and source turn across repeated
manual actions without manufacturing a new user input. Successful process and
workspace cleanup still require independent Worker ownership proofs.

Pinned `thread/resume` has no `experimentalRawEvents` field. Original resumed
compaction response usage therefore remains unavailable when the native profile
provides no live response observation. Never copy overlapping token snapshots or
historical response usage into a new charge, substitute a requested setting for
an observed setting, or add an unsupported raw-events resume parameter.
