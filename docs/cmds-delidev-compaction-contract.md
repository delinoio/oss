# DeliDev native session compaction

> Runtime ownership behavior follows [runtime ownership observations](cmds-delidev-ownership-contract.md). Its authentication, nonblocking observation and retained-handle rules supersede runtime ownership restrictions below; component/source ownership and unrelated validation remain separate.


## Direct startup source identity

[Direct startup](cmds-delidev-execution-startup-contract.md) permits v4 source assignments and retains their exact restore assignment family. Compaction reuses the original private resolved executable identity, rehashes it, and validates the actual source process and original checkpoint. No current installation inspection, PATH replacement or numeric version baseline can authorize a source action. Existing native acknowledgment, complete history, context, account/Worker and independent cleanup proofs remain required.

## Status and ownership

This is the common boundary for issues #1093 (Claude), #1202 (Codex) and
#1203 (OpenCode). Claude owns its implemented settled-boundary product profile.
Codex implements its independently negotiated settled-boundary product profile.
OpenCode implements its independently negotiated settled-boundary product profile.
A reservation never advertises support or enables dispatch. Each complete native
profile joins native adapters, durable action/checkpoint ownership, authenticated
product interfaces and replacement-process verification before activation.
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
| CompactSessionRequest | expected_execution_id | 2 |
| CompactSessionResponse | session | 4 |

The action is distinct from its Worker job and ordinary input/execution.
The request uses an exact session mutation and expected predecessor
execution identity. Its response joins the current session/job and original
action, including reference-only receipt replay. Profile availability must be
checked independently of the common capability; a reservation grants neither.
These allocations must be present on main before dependent implementations use
them, as required by the structure contract.

The existing `CompactSessionRequest.mutation = 1` and response `job = 1`,
`request_id = 2`, `replayed = 3` assignments are recorded in the immutable
baseline. They were introduced by merged PR #1221 and retain their current wire
meanings. The two additional fields above are reserved only; the active RPC and
generated bindings do not yet contain them. The current native profiles still
use the original revision-bound mutation and job receipt, so their implementation
does not satisfy this additional shared request/response boundary.

After the new reservations reach main, dependent implementation must bind
`expected_execution_id` to the exact original successful execution in the
admission transaction and actor-bound receipt identity. The response must read
its current `session` and original `job` together under current authorization,
including receipt replay. Existing `request_id` already identifies the original
action; it must remain equal to the accepted action ID rather than the Worker
job ID. These reservations add no capability, native command authority or
migration and do not establish issue #1203 acceptance.

The existing schema-24 entities, jobs, receipts and cancellation tables provide
the generic durable storage boundary. Compaction allocates no migration and leaves
the independently activated accounting, diagnostic and subscription migrations
26–28 in their original order; their historical reservations remain unchanged.
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


## Codex continuation context lineage

An ordinary settled Codex execution that observed compaction, or inherited a
verified compaction boundary, retains a Worker-private optional context proof
inside its original continuation checkpoint. Legacy checkpoints without context
keep their original omitted-field encoding. The context proof pins complete
native history, every original automatic/manual context turn and both item IDs,
the original protected rollout path/digest and exact ordered input provenance.
Context proof is not a public resource, a substitute for independent cleanup,
or permission to adopt another native endpoint or private home.

Before a replacement starts, compare the original rollout bytes in the derived
protected home. After Resume, compare complete history and every context marker
before the original latest input verification. Retain all inherited context
records across subsequent ordinary inputs, preserving the complete prior history
prefix rather than carrying only the last manual action. A known failed or
interrupted ordinary turn retains its own original terminal facts and explicit
Resume requirement; closed historical turns are not rewritten as successes.
Open context items, native uncertainty, pending interactions, child history,
missing records or changed complete history cannot yield this proof.

The domain's version-2 manual action/result union is independently closed to the
Codex profile; version-1 Claude results retain their original required lifecycle
fields. Codex success requires original action/checkpoint identity, distinct
source/new native turns, live/durable context IDs, acknowledged lifecycle,
complete history digest and independent cleanup. Missing live response usage is
an empty unavailable inventory, never a fabricated measured zero. A Codex failed
or uncertain action cannot borrow Claude's failed-command Resume checkpoint.
This domain union alone does not advertise the reserved product capability or
establish durable native command authority.

## Codex manual product profile

System capability 15 and Worker capability 5 identify the shared product boundary;
System capability 24 and Worker capability 13 independently enable pinned Codex
`0.151.0`. SessionContext capabilities 3/4 expose retained automatic observations
and currently eligible manual actions. Original source success, ready dispatch,
no pending input/interaction/child, unchanged configuration and current original
Worker authority are required in the acceptance transaction. Context reads bind
the exact session revision; the desktop uses one retained UUID-v7 mutation.

Version-2 Codex results are a closed union with separate acknowledgment, lifecycle,
complete history digest, live/durable item identities, repeated-action count and
private checkpoint reference. They cannot carry Claude summary/result fields.
Preserve original conversation outcomes and input receipts. Native send claims
synchronize before one `thread/compact/start`; a lost response remains uncertain.
Original process, active quota observers, proxy listener and workspace leases must
join before checkpoint retention. Successor execution restores the original action
journal and complete native history, then retains that inherited context in its
own continuation checkpoint. Archive/Stop/revocation cannot convert a late report
into successor authority. Managed ChatGPT uses its original exclusive native
account lease; API execution retains its original server-only credential scope.

The pinned resumed Codex process has no raw-response-event option. Manual API
actions therefore collect original completed HTTP response identities/counters
at the Go relay, separately from native cumulative/context observations. Store
only a hashed response identity, action/account/model ownership and nullable
integer counters. Bind the genuine source native turn separately; leave the
action's unobserved native turn absent. This source is refused in Worker ordinary
response publications, preventing double charging. Missing counter splits remain
unavailable, and actual cost remains unavailable. Accepted compaction actions with
no exact response have a separate coverage count. No unavailable observation is
a fabricated zero, and a transport response cannot prove native compaction success.

## OpenCode original context and private manual controller

Pinned OpenCode `1.18.32` automatic compaction now joins the original accepted
input with separately owned native compaction-user/part, summary assistant,
`session.compacted` event and marked native continuation. Summary and native
continuation content never create a DeliDev input receipt or replace canonical
conversation messages. The ordered execution outbox publishes only bounded
context lifecycle metadata; original new step-finish sources retain their separate
once-only accounting, while finalized assistants remain overlapping observations.
A missing or conflicting lifecycle cannot grant terminal/continuation authority.

When present, the selected model's known or user-declared context limit is copied
into immutable `opencode_context` execution configuration before its first digest.
The native initializer independently checks that exact configured limit. This is
metadata provenance, not measured provider capacity; omitted legacy limits stay
unknown. A saved limit with an unknown metadata source is also omitted from
the execution snapshot and preserves the legacy digest. No output-limit default
is invented. Later model changes cannot
rewrite that snapshot.

New known/declared-limit snapshots pin closed context policy `native-v1`, enabling
the pinned native pruning policy explicitly. Omitted legacy policy keeps its
original native configuration and digest. The checkpoint settings digest includes
the enabled policy; restoring a source cannot silently change it. Native pruning
may complete after the original idle arrival. Final original history reads may
retain its completed timestamp-only marker after validating the entire part
against the original private inventory; this atomic comparison grants no input,
event or settlement authority and rejects any output or metadata change.

Private checkpoints retain the complete current native message/part inventory,
all original automatic/manual context records and independently validated pruning
markers alongside immutable ordinary input histories. Pruning may add only the
pinned native completed timestamp to an originally completed owned tool part;
changed output, input, metadata or identity is refused. Earlier conversation
history and canonical tool output remain retained. Replacement copies the
original SQLite/WAL/SHM into an independently owned home and compares every native
message/part through the original local API before another input. No database rows
are reconstructed or rewritten by DeliDev.

The pinned native auxiliary summary task can update a marked native continuation
user's ancillary summary after idle. A separate original API read may retain that
closed metadata when the complete original identity/base still matches. That read
cannot fill a missing compaction event, acknowledge another input, establish
assistant settlement or publish canonical conversation content. A transport gap
within compaction remains uncertain; it cannot synthesize the missing lifecycle.

The private manual controller accepts only a fresh independently restored
successful original checkpoint under the unchanged native model/agent/settings.
It synchronizes one original action-bound send claim before one summarize request
with explicit original provider/model and `auto=false`. Its asynchronous HTTP
request is bounded, cancellation-owned and joined at cleanup while the original
event reader drains native arrivals. HTTP acknowledgment, native lifecycle, full
history and cleanup remain separate. A lost HTTP response cannot be resent or
retained as a successful checkpoint even if native lifecycle arrived. Repeated
manual actions preserve the original input/outcome and complete inherited context
through each fresh process and subsequent ordinary input.

The public OpenCode profile activates System capability 25, Worker capability 14
and SessionContext capabilities 5/6 independently of Codex and Claude. Worker
negotiation requires the original detected, protocol-verified pinned OpenCode
installation; the common capability 5 cannot imply either native profile. Manual
admission requires a ready successful child-free boundary with no queued input,
current original account/configuration, exact revision, independent accepted
version-2 completion and current Worker support. Version-3 action/result JSON
remains a closed OpenCode union; legacy Claude version 1 and Codex version 2 retain
their original meaning and bytes.

The Worker synchronizes separate original registration, command, native Resume
and native summarize claims before their effects. Its private checkpoint binds
server/device/job/instance/revision/assignment, immutable original source digest,
complete ordinary/context lineage and the exact accepted report. Repeated actions
validate every prior accepted action and retained source independently. Ordinary
successors claim the last action's closed workspace owner and restore its full
native snapshot while retaining the original ordinary assignment/report owner.
Missing, changed or uncertain claims, reports, history or cleanup never grant a
new command or replacement checkpoint. A lost native acknowledgment cannot be
resent, including after Worker replacement.

Successful original manual summary step-finish sources are retained once under
their action, original account/model and native summary identity. Finalized summary
counters do not add another unit, and missing exact provider-response telemetry
remains separately unavailable. API/client/CLI/context desktop flows share the
existing authenticated revision-bound RPC and durable mutation receipt. The
desktop requires both common and independent OpenCode support plus the exact
server-observed eligible session revision. Actual scripted-provider native checks
and unperformed real account/platform acceptance remain separate in PR/issue/CI
validation records.

## Complete immutable assignment decoding

Compaction inputs retain the complete original execution and replacement input,
including maximum-length escaped prompts. A strict typed decoder admits at most
3 MiB of compaction input and 4 MiB of its job document consistently in private
store reads, authenticated Worker dispatch, server scope/settlement and deletion
copy derivation. The Connect receive bound covers the finite serialized job.
Larger ordinary execution or other jobs do not acquire this exception. Trailing
documents, unknown fields and invalid original assignments remain rejected.
The primary WatchWork claim receipt adds only a separate 1 KiB Record metadata
allowance and rechecks its embedded job through the owning typed decoder.
Same-instance reconnect preserves the original claim identity, revision and
assignment bytes without another claim mutation or native effect.

Retained Claude/Codex compaction checkpoint readers preserve their strict 10 MiB ceiling. OpenCode uses one strict 12 MiB canonical retention/restoration envelope: the native document keeps its independent 8 MiB bound, the accepted compaction input keeps 3 MiB and closed ownership metadata reserves 1 MiB. Every reader uses the same envelope, including repeated-action restoration, without relaxing typed, canonical, claim, journal or native evidence checks. Session context uses the same typed 3 MiB compaction input decoder as acceptance and settlement, so large original assignments remain visible through queued and completed states.

The closed compaction HTTP usage source retains sparse nullable token observations even when active pricing exists. Publication and historical estimate verification share source-aware validation and the immutable selected basis. Known priced components may form a partial subtotal; unknown cache splits or absent counters remain unavailable. Ordinary native response validation is unchanged, and exact replay/restart cannot charge the compaction twice or reprice history.
