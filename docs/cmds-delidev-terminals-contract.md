# DeliDev Session Terminals Contract

## Scope

The feature adds interactive terminals owned by the session's execution Worker.
Canonical code lives in `cmds/delidev-cli/internal/{terminal,process,worker,server,store,cli}`,
the additive `TerminalService` and Worker messages in `protos/delidev/v1`, the
generated API client, and `apps/delidev/src/session-terminals.tsx`. This contract
does not authorize arbitrary-machine terminals or inbound Worker listeners.

## Runtime and Language

Go owns product validation, native execution and terminal lifecycle. Unix uses
PTYs inside the existing independent process supervisor; Windows uses ConPTY
attached to the original atomically assigned Job Object. React presents the
authenticated Connect operations. Terminal stderr shares the native byte stream.

## Users and Operators

Owner and client devices create and control terminals on a prepared active
session. The selected paired Worker claims native operations and reports their
outcomes through outbound authenticated connectivity. Viewing clients may
disconnect and reattach without replacing the shell.

## Interfaces and Contracts

The server advertises `SYSTEM_CAPABILITY_SESSION_TERMINALS_V1` with value 14;
the Worker must advertise `WORKER_CAPABILITY_SESSION_TERMINALS_V1` with value 4.
Every preliminary attachment, including reconnect, retains terminal and forwarding
support before the independent Codex title probe. Only verified title support waits
for the later capability negotiation; probing cannot withdraw live-shell access.
These independent enum spaces preserve the merged user-service system value 3.
Desktop history reads, polling, manual refresh and selection wait for advertised
system terminal support. Unknown or unsupported status shows its capability
notice without terminal requests or cached terminal errors.
The explicit Terminals toolbar/menu gesture resolves a complete authenticated bounded inventory after capability success. Reuse the remembered starting/running terminal without close intent, otherwise the first eligible terminal in retained inventory order. If none is reusable, create exactly one only when all retained ownership is independently cleanup-verified (including an empty inventory), using the gesture's original session/revision, future shell override and 24×80 dimensions. Coalesce pending activations; read failures, incomplete/malformed pages and unsettled ownership never authorize creation. Read Retry alone cannot repeat the gesture. Uncertain creation retains only explicit same-request recovery; confirmed rejection requires a fresh gesture. Departure cancels unsent intent while accepted/uncertain mutation ownership survives. + remains explicit additional creation. Mount, reconnect, polling, tab selection and exit remain read-only. Explicit
creation/selection opens a full-pane tab by original terminal ID while retaining
conversation authoring and original mutation controllers. Presentation Close or
inactive selection releases only the client attachment; it never closes the shell
or discards original terminal mutation authority. Reopen performs no creation.
Creation requires a ready
original workspace, current Worker instance/lease and current session revision.
Workspace storage admission waits for independently verified cleanup of every
terminal. Terminal creation, input/resize acceptance and non-close dispatch/claim
receipt reads require present storage in their owning transaction; a pending,
uncertain or stored workspace cannot authorize native work. Original close claims
and cleanup reports remain available to reconcile retained process ownership.
The terminal retains the session/project, machine, original Worker instance and
claimed paired device. Input and resize require the current running terminal,
its revision and an available owning Worker. Close is a separate durable intent
that takes precedence over queued create/input/resize operations. Replayed
product receipts return current records instead of replaying native operations.

- `TerminalService.CreateTerminal` accepts a session mutation, optional absolute
  shell override and dimensions. Terminal IDs and all request/operation IDs are
  canonical UUID-v7 values.
- `ResourceService` lists and inspects `ENTITY_KIND_TERMINAL` records using its
  existing bounded filters, revisions, authorization and pagination.
- `TerminalService.ControlTerminal` accepts the closed input/resize/close action
  enum and a terminal mutation. At most one input/resize/create operation is
  pending per terminal. Input contains 1–32768 original bytes. Dimensions are
  1–500 rows and 1–1000 columns, initially 24×80 in the desktop and CLI.
- `TerminalService.WatchTerminalOutput` attaches to an existing terminal with
  an optional epoch UUID and uint64 sequence. It streams original bytes, explicit
  gaps and content-free heartbeat/current-terminal metadata. Reattachment uses
  this same operation; it never creates a shell.
- `WorkerService.WatchTerminals` is an auxiliary channel joined to the current
  primary Worker stream. `ClaimTerminal` durably claims the exact operation;
  `ReportTerminal` records bounded native facts; `PublishTerminalOutput` accepts
  ordered original byte frames. Every call revalidates machine/instance/device
  authority. Replacement Workers may claim only explicit close reconciliation
  on the same original device, never redispatch an earlier native operation.

Terminal assignment and metadata watchers capture the store's post-commit
notification before each authorized read. Output observers capture one
service-wide constant-memory broadcast under the output mutex before copying
retained frames. Only an authorized, validated new frame closes and replaces
that broadcast; exact retries and rejected frames do not signal progress.
Every wake reauthorizes through the original read/dispatch rules. Keep the
100 ms safety tick, 10-second heartbeat and uncertain-close eligibility delay.
Notifications do not change serialized controls, receipts, cursors, gaps,
retention or joined shutdown. Content-free debug stages and wait durations
separate notification delivery from native/storage costs; never log bytes.

The CLI exposes `delidev session terminal create|list|inspect|input|resize|output|reattach|close`.
`--id` targets the session for create/list and the terminal for other operations.
Mutations use the common durable `--request-id` and `--revision`; creation also
accepts `--shell`, `--rows` and `--columns`. Input reads exact bytes from
`--input FILE|-`, requires piped stdin or a file, and cannot share credential
stdin. Output/reattach accepts `--epoch`, `--after` and `--follow`; follow emits
versioned JSON byte frames with Base64 data and canonical decimal-string
sequences, preserving partial UTF-8 and uint64 precision. Without follow, it
returns one observation, which can be a metadata heartbeat.
Parsed follow streams preserve caller cancellation and caller deadlines without
the ordinary 30-second CLI command limit. Non-follow observations retain that
bounded command deadline, including explicit `--follow=false`.

On macOS the default shell comes from the effective native account's `UserShell`
directory-service field. Linux reads that account's passwd shell through owned
`getent`. Windows uses configured OpenSSH `HKLM\SOFTWARE\OpenSSH\DefaultShell`,
or the native system `cmd.exe` when that setting is absent. Failed discovery or
an invalid configured/default/explicit executable never selects another shell.
Explicit overrides must be absolute existing regular executable files, resolved
on the Worker. Unix shells receive `-i`; Windows shells receive no synthetic
Unix flags. The child receives bounded ordinary account/system context without
the Worker's pairing or provider credentials.

The launch directory is fixed to the original ready manifest's primary
repository, or its General Chat directory. The existing anchored continuation
identity verification covers every repository before native launch and permits ordinary dirty files
and commits. It does not take or replace the agent execution lease. Native
startup, input/resize, shell lookup and workspace verification remain bounded;
unknown native outcomes become `uncertain`, requiring exact close reconciliation.
After failed or timed-out input/resize, an unconfirmed cleanup retains its
process-tree reconciliation problem and guidance; the triggering control error
may replace that problem only after cleanup is independently verified.

Agent Stop preserves terminals. Archive atomically queues closes for only the
selected session and remains `archiving` until all agent/title/preparation and
terminal cleanup gates succeed. The central session publication barrier also
covers late completion/recovery paths. Only the expected live-terminal
`RecoveryRequired` result defers completion; lookup, decoding and history-bound
failures roll back the report and its receipt so the exact report remains
retryable after repair. Permanent session deletion atomically requests original terminal closes and
keeps cleanup reports admissible. Its separate workspace-removal lane receives
no work until all session terminals have independently joined their process
trees; final database purge rechecks the same cleanup barrier. Exact deletion
retries preserve the accepted close identities. An accepted uncertain close
report atomically replaces its finished operation with a fresh close identity;
exact report retries retain the same next identity without mutating original
result facts. Worker assignment waits at least ten seconds after the latest
uncertain terminal write, including across stream reconnects, while explicit
current-Worker close claims remain available. Each fresh close reconciles only
the original owned process evidence and can never replay creation, input or
resize. Uncertain or offline terminal ownership keeps deletion pending. Direct terminal deletion refuses unconfirmed
ownership. Close joins the original process tree and output machinery before
reporting cleanup. Missing/changed ownership never authorizes PID termination.

### cmds/delidev-cli/internal/apiproxy constraints

- Retain OpenCode tool-call frames through the validated terminal DONE marker, independently of finish_reason. Malformed/truncated streams, repeated calls or cancellation discard all buffered executable frames; preserve their original order only after terminal validation.

### cmds/delidev-cli/internal/cli constraints

- Session terminals follow `cmds-delidev-terminals-contract.md`: native side effects require original durable claims, exact Worker/process/workspace ownership and independent joined cleanup. Preserve client reattachment, bounded ordered bytes and explicit gaps without another shell; Agent Stop preserves terminals while every Archive/deletion boundary waits for their cleanup. Parsed output/reattach `--follow` streams retain caller cancellation/deadlines without the ordinary 30-second command limit; non-follow observations retain that limit. Never use PID absence as termination authority.

### cmds/delidev-cli/internal/domain constraints

- Session terminal records and closed action/state enums follow `cmds-delidev-terminals-contract.md`; keep native cleanup independent of observed exit and retain exact original operation identities. Pending input bytes belong only to accepted private dispatch; public resources retain operation/state metadata while omitting those bytes.

- Explicit stopped Codex API account selection follows the sessions/proxy contracts. Require exact terminal/cleanup/checkpoint evidence and current eligibility from the original candidate snapshot; retain revisioned selection history, pause until explicit Resume and never reroute automatically. Preserve original usage/assignments and read the predecessor checkpoint under its complete original account/connection pair while creating a fresh successor grant. Explicitly switching away and back after reconnection must retain the original checkpoint connection independently of the fresh selected connection. Missing or account-bound history cannot switch; every switched relay request independently rejects remote history references.

- Grok public tool, interaction and terminal documents follow the pinned the feature harness/protocol contracts. Keep original request namespaces and lexical numeric IDs through the Grok-only decimal identity, nullable question data, exact decimal uint64 counters and exclusive response/terminal families. Original native Plan decisions cannot be converted to common Plan approval. Null/mixed/foreign response fields must fail before a native encoder or side effect.

- Repository-inspection metadata uses the independent `repository-inspection-metadata-v1` Worker capability. Validate/deduplicate it alongside titles/forwarding/terminals without changing existing values or the closed inspection input. Follow the workspace/protocol contracts.

- A terminal workspace-storage job may retain one immutable `storage_reconciled_by` reference established only while settling an uncertain original through successful explicit recovery. It authorizes no native replay; verify original device/instance, immutable assignment and the successful recovery claim before acknowledging a stale original report.

### cmds/delidev-cli/internal/harness/codex constraints

- Legacy `item/fileChange/outputDelta` is bounded inert patch-tool output with explicit original patch provenance, separate from patch updates and terminal status. Preserve exact thread/turn/item ownership, strict fields and receipt replay; text grants no file/action/completion authority or broader managed Fork history profile. Follow the harness tool contract.

### cmds/delidev-cli/internal/harness/opencode constraints

- Foreground child success/error projections require the same closed native message/part predicate: completion time, ended text/reasoning and terminal tools. Unfinished histories retain running partial telemetry; only the independent original verified Stop/joined-scope cleanup source may interrupt an unfinished abort. Missing/busy status never supplies settlement.

### cmds/delidev-cli/internal/server constraints

- Workspace storage admission requires independently verified cleanup for every session terminal, including exited, closed and uncertain records. Terminal creation, input/resize acceptance and non-close dispatch/claim receipts require present storage in their owning transaction; original close claims and cleanup reports remain available to reconcile retained owners.

- Session terminals follow `cmds-delidev-terminals-contract.md`: native side effects require original durable claims, exact Worker/process/workspace ownership and independent joined cleanup. Public terminal resource projections must omit pending input bytes, including mutation/receipt responses, generic reads/snapshots and output metadata. Preserve exact bytes only in authenticated original Worker watch/claim dispatch. Accept the shared 64 KiB terminal result JSON envelope, including both valid escaped 4,096-byte paths. Reject unknown/disallowed failure codes and contradictory running problems; persist only the validated classification plus server-owned diagnostic text, stripping remote cause/correlation data while retaining exact original receipt identity. Preserve client reattachment, bounded ordered bytes and explicit gaps without another shell; fresh cursorless attachment to an empty ring exposes unknown prior retention as a gap; loss-only gap notifications retain the acknowledged cursor and never replay retained bytes; each attachment reports the monotonic persisted loss flag once independently of later metadata revisions, while fresh attachment observes that retained fact anew; Agent Stop preserves terminals while every Archive/deletion boundary waits for their cleanup. Never use PID absence as termination authority. Replacement terminal report retries may acknowledge only an exact already-committed receipt under the same current paired device/machine and live terminal-capable Worker lease; return no resource content and never mutate absent receipts. Original instance authority remains mandatory for new reports.

- An accepted uncertain terminal close atomically replaces only its finished close operation with a fresh close identity. Exact report and deletion receipt retries preserve that next intent; original result facts and cleanup barriers remain authoritative. Stream dispatch waits ten seconds after the latest uncertain terminal write, including reconnects, without granting shell creation, input, resize or cross-device authority. Follow `cmds-delidev-terminals-contract.md`.

- Request diagnostics follow the diagnostics contract. Publish native-input projections with original Worker events; proxy observations belong only to the original joined lease. Initial/send claims recheck authority, terminal settlement preserves original provenance after Stop/revocation, and reads remain owner/client-only. A send claim does not establish provider acceptance.

- Storage admission reserves eight total original-group recovery attempts, counting canceled and failed accepted attempts as retained deletion obligations. Native uncertainty claims have their separate eight-member bound. Terminal attempts do not reset the reservation count or strand an earlier still-supported successor. Exact request replay consumes no additional attempt; exhausting the finite recovery-attempt bound preserves evidence and returns explicit resource guidance.

- New workspace file, diff, PR and terminal reads require present storage through the shared transactional workspace read scope; pending, uncertain and stored sessions cannot publish a new read. Original admitted result/cleanup authority remains independently validated.

- OpenCode automatic context progress must match the original execution harness and native part identity. Apply started before completed atomically, retain the canonical conversation and refuse terminal/continuation with an open boundary. Native context users and summaries grant no new input or inferred current token count.

- The feature captures acceptance/end UTC once in the original validated primary-input/terminal publication transactions and receipts. Steer cannot reset timing. Atomically update every original primary-user part through bounded indexed selection; reject changed ownership/acceptance and preserve late-user, legacy and inherited Fork attribution. READY, pre-send rejection and queue/startup time cannot fabricate accepted timing. Strip display timing from every native job/checkpoint/digest projection.

- The feature / the originating change owns `CreateTerminalRequest.creation_mode` field 5, optional original-session candidate `preferred_terminal_id` field 6, and closed `TerminalCreationMode` values 0/1. Resolve every toolbar reuse-or-create in the original authenticated receipt transaction; inventory grants only a preference, never final selection. Revalidate the preferred candidate within the same session/current Worker instance before first-eligible fallback; preserve unspecified explicit additional creation and +, fields 1–4, existing capabilities, current Worker/workspace/revision checks and independent cleanup. Pending input/resize permits inspection reuse; close intent does not. Follow the terminal/protocol contracts; no migration or new native authority.

### cmds/delidev-cli/internal/skills constraints

- Preparation and cleanup share one original snapshot lock. Verify complete original root and resource ownership before removal, observe absence, and retain compact exact-ID terminal tombstones outside active staging capacity. Never adopt replacement, changed or unlisted bytes.

### cmds/delidev-cli/internal/store constraints

- Managed creation and pre-migration publication share the 8 GiB inspection/deletion bound. Reject oversized copies before publication, preserve the live database and settle durable size-limit failures terminally.

- The Worker must synchronize immutable native continuation evidence after owned cleanup and before its completion journal/report. Version-2 completion binds the exact checkpoint file digest; version-1 terminal history cannot be silently upgraded. Keep native path/default context private, exclude prompt/instruction/answer/output/credential contents, and match exact assignment-input/configuration/account/connection/runtime/terminal/input ownership on read. Missing, altered, linked, oversized or contradictory evidence must neither be rebuilt nor authorize input. A checkpoint cannot resolve a lost server report or semantic interaction uncertainty. Include retained checkpoint files in the session's coordinated backup/deletion boundary when implemented.

- OpenCode live Stop claims one original abort and immediately blocks answers without retrying after claim/HTTP uncertainty. Require separate native interruption, original terminal/idle/storage and fresh pending/status observations; normal completion racing Stop must remain normal completion. Pinned native tool effects can remain pending after abort, so retain them until original owned process/journal cleanup and joined event closure, never synthesize rejection/answers. Cleanup preserves missing earlier acceptance and cannot grant history/continuation authority. Already-buffered late original acceptance must still satisfy its original claim/body checks and cannot repeat an answer or cleanup. Pre-assistant scheduling and failed native observation use the separate original owned-stop intent because native abort can race runner setup; that path performs no HTTP or acknowledgment reconstruction and must retain recovery after verified owner cleanup.

- OpenCode Worker checkpoint envelopes bind the original live claim journal, assignment/configuration/account/connection, acknowledged terminal sequence, input mode/prompt digest and native closed checkpoint. Recheck exact publication and mutation ownership around native inspection and synchronized storage. Keep the bounded 9 MiB envelope in its existing private job directory outside the inventoried native runtime; preserve conflicting files and report recovery without replay or fabricated server cleanup acceptance. Read-only inspection requires independent original references and an exact file digest. Version-1 reporting remains paused and cannot be upgraded from this file into continuation/recovery authority. Include the envelope and complete runtime in future coordinated backup/deletion.

- Schema-v19 PR CI/conflict evidence follows the integration contract. Preserve original v18 bytes/receipts during index migration. Select CI problems only from complete recomputed required/evaluated terminal failure; bind each version to immutable shared original proof and never use workflow-wide attempts to resurrect unchanged results. Conflict transitions preserve dedup through unknown reads and restart, while verified resolution or changed refs/commits permits a new version. Collection families remain independent, with legacy feedback receipt compatibility; no storage or local Dismiss grants execution.

- Session terminals follow `cmds-delidev-terminals-contract.md`: accepted pending input is private dispatch data, never public resource content; preserve its exact bytes for original Worker claims while the common RPC projection omits them. Native side effects require original durable claims, exact Worker/process/workspace ownership and independent joined cleanup. Preserve client reattachment, bounded ordered bytes and explicit gaps without another shell; Agent Stop preserves terminals while every Archive/deletion boundary waits for their cleanup. Only the expected live-terminal recovery result may defer Archive completion; propagate lookup, decoding and history-bound failures to roll back the report receipt and preserve exact retry. Never use PID absence as termination authority.

- Permanent deletion requests original terminal closes atomically, preserves their cleanup reports and exact close identities, and blocks workspace-removal dispatch plus final purge until independently joined terminal cleanup. Follow `cmds-delidev-terminals-contract.md`; accepted deletion cannot reopen native terminal authority.

- Validate subagent native/product/execution ownership and original Claude parent-tool claims with one bounded complete-batch query before writes. Include retained terminal claims from earlier executions even when proposed native/product IDs are fresh. Do not rescan large source-coverage JSON separately for every child; preserve cross-execution conflict detection without a schema migration.

- Terminal report receipts retain only their closed kind and original terminal/machine/device UUIDs after entity or session purge, rebuilding that allowlist rather than preserving arbitrary result fields. This metadata proves an exact accepted report acknowledgement and grants no resource resurrection, new claim or native operation; ordinary deleted receipt content remains redacted. Follow `cmds-delidev-terminals-contract.md`.

- Proxy diagnostics retain metadata before credential lookup. Only the first in-progress, unsent metadata revision may acquire guarded request settings and the safe caller request ID; send claims and terminal/later revisions preserve them exactly. Follow the diagnostics contract.

- Backup creation settlement rechecks the original irreversible deletion index in its terminal job transaction. Pending or completed deletion wins with the existing RecoveryRequired failure; deletion after committed success preserves history. Never replay a terminal creation to recreate an image; follow the storage contract.

- The feature uses the single existing mutation UTC clock after receipt lookup and bounded indexed original primary-user selection for atomic terminal timing. Verify session/execution/input/thread/turn and original acceptance before writes; rollback all changed-owner records with the original terminal publication. Keep contents/indexes and exact receipt timestamps unchanged; no legacy backfill or migration.

### cmds/delidev-cli/internal/worker constraints

- Grok text/terminal parsing preserves original session, native prompt and event identities and requires the prompt result plus independent turn/prompt completion observations to agree. Preserve each native stop reason, integer counters and singleton model provenance without combining streamed context estimates, primary-turn usage or unreported auxiliary requests. Missing/foreign/rich native data remains unsupported, never fabricated or silently published. Parser/native-fixture success cannot replace the durable input, interaction, publication, Stop or history controller.

- Grok closed-text history comparison must derive its authority from the original controller after acknowledged native close and joined cleanup. Retain input/chunk/terminal/usage facts before publication callbacks, pin the original home, anchor bounded reads and reject links, shared writes, replacement and changed original records. Native file modes may be readable only beneath the verified private Unix home; Windows keeps strict per-entry ACLs. Preserve first returned file digests, never repair or replay from inspection, and do not treat partial text/history comparison as native continuation or public completion authority.

- Grok stopped-text comparisons must come from the same original controller after its reader and Stop writer join. Retain independent interrupted or raced-success terminal facts and original input/output/chunk digests before callbacks; zero chunks require an interrupted terminal and SHA-256 of empty output, never a zero-output success; return deep copies only after unchanged configuration, terminal/idle/cleanup and original claim checks. Failed publication or missing facts cannot create comparison authority. No stopped observation supplies successful native history, continuation, public reporting or another native operation by itself.

- Native tool observations are data, never permission or product terminal/file authority. Keep command aggregate output distinct from streamed deltas, preserve unavailable native counters and advisory parse/source/plugin metadata, and distinguish tool failure/decline from whole-turn outcome. Validate closed command/patch unions, lifecycle/thread/turn ownership and bounded content. Publish supported command/patch observations through the same durable outbox and native-item identity/completion index as text messages. Retain start/completion snapshots, sequenced patch/input observations and nullable streamed output; never infer a missing stream from aggregate output. Bounds must reject intact updates without consuming sequence or changing prior evidence. Remaining typed tools without dedicated publication stay explicitly unhandled; never drop them or claim full execution support from parser fixtures.

- A resumed Codex binding remains input-blocked until its exact latest terminal turn and ordered original input digests match the preceding accepted execution. Compare complete original effective defaults/permissions and current idle metadata; never fall back to an older turn, switch history modes, recreate a thread or resend prior input. Failed/interrupted outcomes require coordinator-authorized explicit Resume after independent cleanup and interaction reconciliation. Preserve unrelated recovery/pause, reject incomplete/oversized history intact and publish no historical content as new events. Native checkpoint verification is private; public later-turn jobs compose immutable ownership, all-history queued-input/native-turn deduplication, current account authority and a fresh scoped relay credential.

- Claude task edges, background membership, tool return, original-input result and native-run completion are independent. Preserve original task/tool/child ownership, terminal patch versus notification vocabulary, and known pending work after root completion; an empty background set never finishes tasks or authorizes another input. Forwarded child snapshots/context must retain original blocks and metadata without synthetic stream events/indices or root acceptance. Validate all child tools before ownership publication, retain active owned child callbacks after a root result, and reject terminal transitions that abandon observed descendants. Status, summaries, permissions and retry metadata are observations, never authority. Session-wide transfer and synthetic continuation/run boundaries require separate reconciliation before public dispatch.

- Native Claude bare transport can omit the first user record or persist it after its answer. Never grant Resume from a successful terminal or last-prompt metadata; original ordered transcript proof must pass before another process can send input. Keep bare discovery separate from full API execution evidence. Manual compaction caveats may attach only to the exact prior conversation or the same proved successful summary, with matching original preserved tail; neither form permits arbitrary detached history.

- Claude Worker-private checkpoint retention binds the original job/session/machine/history runtime, assignment/configuration digests, account/connection, input/native identity and acknowledged terminal to the consumed native closed capability. Keep its canonical outer file bounded to 9 MiB with the inner 8 MiB bound; reject mismatched original outcomes, same-turn Steer and changed ordered workspace roots. Read only the original fixed runtime file using the independently accepted version-2 completion hash and immutable configuration, then independently validate native bytes/history and terminal classification. Never create missing runtime state, derive comparison authority from that file or promote private persistence evidence into public Worker dispatch/recovery. Original conversation outcome, manual/automatic failure and explicit Resume remain independent.

- OpenCode terminal composition must consume the original stream and only its separately verified same-process reconciliation, block unimplemented native families and compare every stored message/part with completed supported projections. Require the original live API's settled terminal/idle state, stored-history proof and original creation/input journal plus exact live product-authorized response claims. Keep native registry notifications ancillary, and preserve unfinished step-start evidence after native errors without inventing step usage. Terminal publication requires original input/final usage records on the server; exact receipt replay cannot grant cleanup, completion reporting, Stop authority or continuation.

- OpenCode terminal API errors retain owned typed HTTP status classification: 401 authentication, 403 permission and 429 exhaustion; other/missing statuses stay unavailable. Native error evidence takes precedence over finish text, without retaining provider names, response diagnostics or retry authority. Failed turns cannot invent assistant text or completed-step usage, erase missing totals, or imply original process cleanup.

- Original OpenCode stopped-history cleanup uses its separate live Stop boundary. Require the exact sent claim, independent terminal/idle and either native interruption or confirmed abort HTTP; normal completion racing Stop stays normal. Compare every stored message/part and remaining pending proposal against the original observer before joined owner cleanup. Only known interrupted tool requests may remain pending; absent inventories cannot imply acceptance/rejection. Never convert owner-only, uncertain-reply, failed or earlier ordinary cleanup into fresh stopped-history proof, and never grant Worker publication or continuation from this native proof alone.

- Claude native `thinking_tokens` progress preserves separate exact unsigned estimated total/delta counters after original acceptance. These estimates cannot create provider usage, billing, a message identity or terminal evidence; reject missing/null/foreign/mixed shapes.

- Claude input terminal publication requires the separately correlated original result, command closure and native idle, acknowledged original transcript/result usage and no unsettled work/responses. Native idle has no input identity; do not fill it in or promote uncorrelated interruption/API failure. Preserve exact subtype/reason/error, original outcome and prior Stop/recovery. Clean original-controller EOF and completed workspace lease remain separate from terminal publication. EOF failure must join forced cleanup without minting completion. The original version-1 public completion stays paused; only the separately verified continuation profiles below may retain v2 proof. Never enable FIFO through an unproved checkpoint, failed outcome or permission change. Desktop terminal disclosure independently validates ownership/classification and keeps cleanup unconfirmed until the accepted report.

- Claude interrupted-denial completion requires original settled callback/context/session-result records, separate cancelled-command/idle envelopes and the independently joined original native error-exit cleanup before stopped terminal publication. Preserve absent native input correlation, replay only the exact outbox receipt and never repeat cleanup after lost acknowledgment. Server/desktop must validate original references and reject mixed Stop/input-terminal authority. The version-1 report confirms workspace cleanup independently; keep prior recovery, active history ownership and paused dispatch until a separately verified recovery profile exists.

- Retire workspace removal inventories only after the matching terminal server acknowledgment and durable reported Worker journal; failed recovery and uncertain reports preserve predecessor intent. Synchronize the acknowledgment-bound retirement receipt, including the exact result digest, before transitioning the original journal to reported. Startup may finish that transition only from matching persisted proof. Retain a bounded metadata-only retirement receipt for interrupted cleanup and retry it at startup without native replay.

- Session terminals follow `cmds-delidev-terminals-contract.md`: native side effects require original durable claims, exact Worker/process/workspace ownership and independent joined cleanup. Synchronize the private terminal-process root and original owner index before recording a creation start intent, so a pre-native crash can reconcile its retained empty index without inferring cleanup from missing ownership. Preserve client reattachment, bounded ordered bytes and explicit gaps without another shell; Agent Stop preserves terminals while every Archive/deletion boundary waits for their cleanup. Every terminal journal reader and writer shares the 68 KiB envelope limit around the shared 64 KiB terminal report, including room for valid report bytes and ownership metadata; reject oversized writes before replacing retained evidence. Failed/timed-out controls preserve the process-tree reconciliation problem whenever cleanup is unconfirmed; only independently verified cleanup permits the control problem to replace it. Never use PID absence as termination authority.

- Shutdown must synchronize conservative loss for every live terminal, then cancel every terminal before waiting. Join distinct terminal owners concurrently with a shared 25-second reconciliation budget inside the native service's 30-second grace, retaining independent native bounds, complete goroutine joins and uncertainty whenever cleanup cannot be proved. Follow `cmds-delidev-terminals-contract.md` and the user-service contract.

- A claimed close retains its exact displaced pending-operation ID in terminal-bound private metadata through uncertain reports. Only an acknowledged independently verified cleanup may retire the matching prepared, result-less journal by direct lookup. Preserve claimed/started/finished, foreign and malformed evidence; failed claim RPCs grant no removal authority. Follow `cmds-delidev-terminals-contract.md`.

- Confirmed terminal reports synchronize a reported phase before local retirement. Only independently verified cleanup may retire completed scopes, the empty original owner index, its released recovery lock and shutdown observation. Retain original evidence for uncertainty or unacknowledged results; retry interrupted acknowledged retirement locally without replaying any RPC or native operation. A replacement may retry only the exact finished original report identity/bytes to read an already committed receipt; absent receipts grant no report or native authority. Synchronize its acknowledgement before retirement. Scan journals in bounded 4,096-entry batches with retained directory position so a backlog cannot suppress all later retirement work. Follow `cmds-delidev-terminals-contract.md`.

- A fresh close assignment following an accepted uncertain close is a separate original-owner reconciliation attempt. Never rewrite or reuse the previous operation's immutable finished result; retain its exact acknowledgement retry and unconfirmed process evidence. Follow `cmds-delidev-terminals-contract.md`.

- Advertise process-owned terminal and forwarding capabilities on every preliminary attach, including reconnect. Implemented capability advertisement does not wait for native or title probing and cannot withdraw access to existing shells. Follow the terminal, forwarding and direct-startup contracts; Worker attachment performs no native profile probes.

- Explicit stopped Codex API account selection follows the sessions/proxy contracts. Require exact terminal/cleanup/checkpoint evidence and current eligibility from the original candidate snapshot; retain revisioned selection history, pause until explicit Resume and never reroute automatically. Preserve original usage/assignments and read the predecessor checkpoint under its original account scope while creating a fresh successor grant. Missing or account-bound history cannot switch; every switched relay request independently rejects remote history references.

- Terminal replacement recovery may adopt only the original exact close operation after a fresh authenticated server claim. Preserve original journal identity and finished result bytes, with separately synchronized current-instance close claim/report receipts. Retrying interrupted close grants only original-owner reconciliation, never create/input/resize replay; auxiliary reports require confirmed current-instance close authority. Missing or changed native ownership remains uncertain. Follow `cmds-delidev-terminals-contract.md`.

- Capture the final managed execution bundle and native identity once while the native wire is still open, before terminal Close. Earlier exits may use the same bounded read before their deferred Close; never query a closed wire or retry a failed capture. After joined native cleanup, require the retained auth file to match the captured bytes before removal and protected write-back.

- Terminal shutdown synchronizes original terminal/instance-bound output loss before cancellation and retains the joined outcome. Replacement close claims may carry that loss, but must independently reconcile original process ownership; shutdown observations grant no create/input/resize or spontaneous report authority. Missing shutdown observations conservatively mark output loss for terminals that may have run; only the original positive pre-native or joined creation proof can exclude that loss.

- OpenCode automatic context records stay outside canonical input/summary text while every genuine new step-finish source retains once-only accounting. Publish only original frozen lifecycle facts, require all context boundaries closed at terminal and independently compare complete native inventory across replacement. Preserve known/declared model context in immutable configuration without inventing unavailable limits. Manual product admission and negotiated support retain their independent original action/report gates.

## Storage

Terminal records use the existing additive generic entity store and event/
receipt transaction. No destructive migration or schema bump is required.
There are at most eight live terminals per session, 128 retained terminal
records per session and 32 live terminals per machine. States are `starting`,
`running`, `exited`, `closed` and `uncertain`; cleanup is an independent fact.
Pending input bytes exist only in the private accepted operation until its outcome
or close is committed. Every public Resource projection omits `pending.input`,
including mutation/receipt responses, generic inspect/list/snapshot reads, CLI
results and output-stream metadata. Operation IDs, action, claim and pending state
remain visible. Original authenticated Worker watch/claim assignments retain exact
dispatch bytes, independently of the public projection. A malformed terminal
document never falls back to its private bytes in a public resource. Mutation receipts retain references and input digests, not
a second input or output copy. Terminal history is retained after cleanup.

Worker-private synchronized operation journals retain original IDs, semantic
digests, claim/report IDs, phase and bounded native results; they never contain
input/output bytes. Creation synchronizes its private terminal-process root and
original owner index before recording native-start intent. A restart before
native launch can reconcile that retained empty index; a missing or changed
index still cannot establish cleanup. Native-start intent is synchronized
before side effects.
An interrupted started operation is reported uncertain instead of repeating
input, resize or shell creation. An interrupted close can reconcile the same
original process owner again. A replacement instance may adopt only an exact
close assignment after independently claiming current server/device authority.
It preserves original operation IDs, digest, claim/report IDs and finished result
bytes while retaining separate instance-bound close-recovery claim/report IDs.
Claim and report response loss reuse those replacement receipt identities;
another replacement must claim its own authority. Auxiliary report retry waits
for a synchronized confirmed current-instance close claim. Missing or changed
process evidence remains uncertain; adoption cannot create a shell or resend a
control. Finished results retry their exact report. Confirmed report
acknowledgements are synchronized as a separate reported phase before metadata
retirement. A claimed close retains its exact displaced pending-operation ID in
terminal-bound private metadata before reporting can clear it, including through
uncertain close reports and replacement. Acknowledged independently verified
cleanup retires only that matching prepared, result-less journal by direct bounded
lookup. Failed claim RPCs grant no removal; claimed/started/finished and foreign or
malformed evidence retains its independent recovery lifetime. Independently verified cleanup then prunes completed native scopes
and retires only the empty terminal owner index, its private recovery lock and
the shutdown observation, before removing the operation journal. Unacknowledged
or uncertain cleanup retains original process evidence. Interrupted acknowledged
retirement retries locally, including after server-side record deletion,
without another claim, report or native operation. If the report committed but
its acknowledgement was lost before replacement, the replacement retries its
exact original instance/request/operation/result bytes only to read the accepted
receipt. The server requires the same currently authorized device/machine and a
live terminal-capable Worker lease, acknowledges without resource content, and
never mutates an absent receipt. New reports still require original current
instance authority. Report receipts retain only their closed kind and original
terminal/machine/device UUIDs through entity/session purge; ordinary deleted
receipt content is redacted. Older receipts without ownership metadata require
the original resource to remain available. The Worker synchronizes acknowledgement
before independent local retirement. Journal scans advance through bounded
4,096-entry batches rather than refusing every scan of a larger backlog.

Worker shutdown synchronizes an original-terminal/instance-bound conservative loss
observation for every live terminal before cancelling any output. It then cancels
every terminal before waiting and joins independent native owners concurrently,
with a shared 25-second reconciliation budget inside the service's 30-second
stop grace. Existing native termination bounds and joined output/handle ownership
remain authoritative; an exhausted budget cannot prove cleanup. The manager joins
every shutdown goroutine and retains each native result
before dropping the live entry. A replacement reads that observation only for
an independently claimed close; it carries loss monotonically but still
reconciles the original process index, never borrowing cleanup authority from
the shutdown record. A missing shutdown observation conservatively marks output
loss when the terminal may have run, including abrupt Worker death and older
Workers. Original unclaimed/pre-native creation proof excludes impossible
output, while an independently joined creation result retains its own loss
fact. Missing shutdown or process evidence never proves cleanup. Terminal reports share a 64 KiB JSON limit
between Worker and server, covering both accepted 4,096-byte paths even under
worst-case six-byte JSON escaping. Journal reads and writes share a 68 KiB
envelope bound that reserves room for ownership metadata around a maximum-sized
report; oversized writes fail before replacing retained
ownership. A proven pre-native original creation journal can reconcile a lost
claim acknowledgement without inventing a process.
Native process journals retain the existing independent ownership/cleanup proof.

Output is ephemeral: each server ring retains at most 512 KiB and 1,024 frames, with 128 rings
and LRU eviction bounding retention to 64 MiB. Ring eviction uses exact
serialized access order, independent of host clock resolution and timestamp
ties. Frames are at most 32 KiB. Worker output uses a bounded 64-frame queue
and backpressure. Identical most-recent
frame retries are acknowledged; changed bytes or reordered frames fail.
Eviction, server restart, missing prefixes, forward cursors and abandoned exit
output expose a gap. A fresh cursorless attachment to an empty ring also exposes
a gap because an ephemeral empty ring cannot prove that prior output never
existed. This conservative notification applies before the first observed native
frame as well as after eviction or service restart; complete retained output
starting at sequence one does not acquire this gap. A loss-only notification preserves the acknowledged cursor;
only epoch changes, missing prefixes and invalid forward cursors replay the
retained suffix. Each attachment observes the monotonic persisted output-loss
flag once, independently of later terminal metadata revisions; a fresh
attachment observes that retained fact again. Every shell has one native output epoch; reconnect does
not restart its producer. Normal exit joins/drains output before cleanup is
reported; forced close may abandon bytes and exposes that fact.

## Security

Owner/client product authority is separate from machine-bound Worker authority.
No terminal is created merely by mounting a view, observing output, reconnecting
or replaying a request. Only original independently verified process scope
ownership permits termination, including shell-discovery children. Client
disconnect ends its observation; Worker reconnect within the same process
retains the original shell. Worker shutdown joins all owned terminals. Worker
replacement loses input authority and requires explicit cleanup reconciliation.
Shell paths, cwd, terminal input/output and account context never enter logs or
native process journals. Transport uses the existing authenticated loopback/TLS
and exact origin rules.

## Logging

Structured logs identify terminal/session/machine IDs, accepted action, replay,
state, cleanup fact and stable error code. Interrupted auxiliary channels log
the machine and safe code. Existing process-scope logs retain ownership IDs and
safe lifecycle facts. Never log raw bytes, shell command text, paths, tokens,
environment values or raw OS errors.
Worker-reported problems accept only the closed native terminal classifications:
`invalid_argument`, `not_found`, `conflict`, `permission_denied`, `unavailable`,
`missing_input`, `unsupported`, `recovery_required`, `resource_exhausted`,
`canceled` and `internal`. Unknown or disallowed codes and problems on running
results are rejected. Before public persistence, the server retains only that
classification and its own bounded message/guidance, discarding remote message,
guidance, cause and correlation ID. Receipt identity still binds the original
report bytes, so exact acknowledgement retries remain read-only.

## Build and Test

Run root `go test -race ./cmds/delidev-cli/...` and
`go vet ./cmds/delidev-cli/...`; protocol changes require `pnpm proto:check`.
Generate and test the API client and run `pnpm test` in `apps/delidev` after
hydrating its required LFS assets. Remove repository-generated `dist` output
after validation. Controlled fixtures cover Unix TTY/resize/descendant cleanup,
ConPTY native ownership, Local/multi-repository Worktree/General Chat directory
selection, request/acknowledgement loss, interrupted-operation refusal, ordered
bytes, split UTF-8, reattachment gaps and Stop/Archive/deletion barriers.

Keep executed checks and retained fixtures distinct from native Windows/Linux,
real remote Worker, native desktop visual and release acceptance. The desktop
provides the pinned WebGL terminal emulator described by the desktop contract,
with bounded scrollback, exact original byte/cursor order, atomic input admission
and serial retained input/resize controls. Its accepted creation and explicit
selection survive bounded history payload eviction. Hiding or tab departure
aborts observation and discards unsent bytes without stopping the original
process; uncertainty and independent native cleanup remain authoritative.
Desktop presentation may hide original terminal IDs after authenticated monotonic
schema-v1 `exited` observations with exact boolean `cleanup_verified: true`.
Connection-memory dismissal does not delete server history, grant process cleanup
or retire uncertain original controls. Older restored history cannot revive a
presentation tombstone; stream completion and lookup failure are not exit proof.

Byte-stream consumers
retain all native control bytes. Record source revisions, commands, results and unresolved limits in the originating change,
The feature and CI logs/artifacts under the root validation policy. Historical
validation remains available at its original Git revisions; do not add repository
evidence documents.

## Dependencies and Integrations

Reuse the existing PTY, native Windows API, process ownership, workspace
verification, SQLite receipts/events, Connect and Connect Query dependencies.
Agent, preparation and title lifecycle remain separate owners joined by Archive
and permanent deletion. Terminal-process journals retain metadata-only ownership
proof; deletion never infers process completion from missing PIDs or journals.
No new inbound server or external account/provider dependency is introduced.

## Change Triggers

Update the project index, session/process/workspace contracts, protocol/client/
desktop contracts, validation records in PR/issues/CI and scoped `AGENTS.md` when terminal
ownership, limits, shell selection, native replay, cleanup or presentation
contracts change. Generate all Go/TypeScript/Connect Query bindings together.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [Project index](project-delidev.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Owned processes](cmds-delidev-process-contract.md)
- [Workspaces](cmds-delidev-workspace-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [API client](packages-delidev-api-client-contract.md)
- [Desktop](apps-delidev-desktop-contract.md)
- PR validation
- [Historical issue validation](https://github.com/delinoio/oss/tree/a7a47662bdabdc652da97ce7d01449a008663b0a/docs/evidence/delidev/issue-1088)
- [Frozen historical ledger](https://github.com/delinoio/oss/blob/a7a47662bdabdc652da97ce7d01449a008663b0a/docs/cmds-delidev-evidence.md)
- [Repository defaults](repository-defaults.md)
- The feature

- [Microsoft pseudoconsole creation and teardown](https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session)
- [Go PTY package API](https://pkg.go.dev/github.com/creack/pty)

## Atomic toolbar terminal admission — the feature / the originating change

`CreateTerminalRequest.preferred_terminal_id = 6` records the optional original
session candidate preference under the same / the originating change ownership. Every
toolbar gesture uses atomic admission after complete inventory inspection;
inventory alone cannot select a terminal. Admission prefers that ID only if it
remains eligible in the same original session and current Worker instance, then
uses the first eligible terminal. Empty, missing, foreign or retired preferences
grant no authority and fall back to ordinary eligibility. Unknown IDs are not
read outside the original session; malformed IDs and preferences on additional
creation are rejected. Exact receipts retain the preference bytes.

`CreateTerminalRequest.creation_mode = 5` owns the closed
`TerminalCreationMode` enum: `UNSPECIFIED = 0` preserves explicit additional
creation, including existing clients and +; `REUSE_OR_CREATE = 1` resolves
explicit toolbar admission in the authenticated receipt transaction. Preserve
fields 1–4, existing responses, System 14 / Worker 4 and every original native
claim. Unknown modes are rejected. Reuse returns a reference to an original
starting/running terminal on the current Worker instance without close intent,
including pending input/resize. Otherwise every original terminal must be exited
or closed with independent cleanup verified and no pending operation before
creation. Concurrent clients receive the same newly accepted terminal. Exact
request replay retains actor-bound receipts without dispatching another shell.
No capability, migration or native protocol change is added.
