# DeliDev same-account native session forks

## Direct startup source identity

[Direct startup](cmds-delidev-execution-startup-contract.md) permits source assignments with private execution version 4. Freeze the original successful readiness digest into the Fork input without rewriting source assignment bytes. The Worker resolves and rehashes only the original private resolved executable. Publication retains the original startup selection and successful readiness digest in the child-owned Fork boundary before returning the child. The Worker also retains the original resolved executable identity in the child-owned runtime before native Fork publication. Child execution uses this retained selection and private identity even after parent deletion purges the source-owned creation job and journal. An older child without retained selection may read only its surviving original creation seed; missing evidence requires recovery and cannot select a replacement executable. Independent parent deletion cannot transfer or erase child ownership. Existing separately negotiated authentication, platform, transcript, account/Worker, workspace-copy and native mutation/cleanup limits remain unchanged.

## Scope

Issue #1092 adds independent native Codex forks to the Go session, Worker and
workspace owners. Issue #964 and the existing session, harness and workspace
contracts remain normative. Fork does not imply Sidechat, account switching,
transcript replay or support for unknown native history.

Canonical owners are `cmds/delidev-cli/internal/{server,worker,workspace,harness/{codex,opencode},cli}`, additive `protos/delidev/v1` and generated clients, with presentation in `apps/delidev`.

## Runtime and Language

Go owns business logic, durable jobs and native/workspace operations. The desktop
uses existing React/TypeScript and authenticated Connect Query. Codex is pinned
to the repository's independently verified native `0.151.0` profile.

## Users and Operators

Owner and paired clients request/observe Fork through Connect, CLI or desktop.
Only the original authorized Worker may inspect its private source and create a
child. Provider/account authorities remain the original immutable selection. Independent Codex Fork supports API authentication and separately negotiated managed ChatGPT authentication under issue #1979 below. Managed ChatGPT Sidechat follows the separately negotiated protected lease, joined cleanup and final credential write-back profile in the Sidechat contract. Ordinary Fork capability does not grant managed authentication.

## Interfaces and Contracts

### Acceptance and publication

Owner/paired clients use authenticated `SessionService.ForkSession` and
`session fork`. A UUID-v7 request, exact source revision and completed native
turn identify the immutable boundary. Only an original root session is eligible;
reject a published forked child before accepting another job, even after that
child completes its own turn. Acceptance creates one durable Worker job;
the child is published only after both native history and every workspace are
verified and native process cleanup is independently confirmed. Exact receipt
replay observes the original job/child without repeating native side effects.

Sessions with observed native subagents retain version-1 paused completion under
the subagent contract. Their closed child inventory cannot satisfy Fork's
independent version-2 checkpoint requirement or promote that completion.

Status observation and acceptance-receipt reads require current owner/client
authority in the same read transaction before reading any job or child document.
Worker credentials and missing principals cannot use this client-only observation
RPC. A stale paired-client principal cannot bypass device revocation.

The initial profile preserves the original Worker, account, connection,
provider, model and exact immutable configuration. Current eligibility is checked
at acceptance, Worker claim, publication and later continuation. Source queued
inputs remain source-owned; the child has no copied queue, approvals, interactions
or executable historical work. The child retains source/session/boundary links.
Source continuation is excluded while a fork owns its boundary; this reservation
does not rewrite the source session or advance routing.

First child dispatch applies the ordinary fresh Worker-instance gate, including
rejection of observations more than one second in the future. A backward host
clock move cannot promote a disconnected Worker into a queued-input claim or
turn an empty Resume into dispatch readiness.

### Public commands and child continuation

`delidev session fork --id <source> --revision <revision> --turn-id <native-turn>
--name <child> [--workspace worktree|general-chat|local] [--local-worker-dir <scope>]
[--request-id <uuid-v7>] [--wait]` has the same authenticated semantics as Connect.
`delidev session fork --job-id <accepted-job> [--wait]` observes without mutation.
`--wait` observes for at most 135 seconds within a 145-second command deadline;
timeout returns the original job and cannot cancel or repeat Fork.
Private scope paths are CLI/Worker inputs and never part of public RPC rollout
selection. A copied child starts paused and requires a new queued input plus
explicit Resume. Its first turn resumes the child checkpoint, and subsequent
FIFO/Resume uses the child's verified completion on the same new history. Bind
lost-report recovery to that fork runtime for the first child execution;
the fresh execution ID identifies the original report rather than its native home.
Keep original account/connection and configuration even after current Agent edits;
current eligibility failures retain input instead of rerouting. Child metadata
exposes its source/boundary link; source transcript records remain source-owned.

First-child execution publication must bind the exact native child thread from
the immutable fork assignment. Its accepted new input must use a fresh turn,
never the inherited last source turn. Check these identities before changing
progress or queue accounting; rejected publications leave source, child, queue
and original job records unchanged.

### cmds/delidev-cli constraints

- Codex fork Local sharing is limited to original Local manifests with no parent-owned checkouts. Reject managed Worktree sharing before job acceptance and again before Worker native inspection/preparation and server publication; parent deletion retains those paths. Independent Worktree copying remains available. Follow `cmds-delidev-forks-contract.md`.

- Independent managed ChatGPT Fork follows issue #1979 and `cmds-delidev-forks-contract.md#managed-chatgpt-independent-fork--issue-1979`. System 53 / Worker 29 separately authorize original-account native Fork; ordinary Fork and managed Sidechat capabilities do not. Preserve exact settled tool history, original source/settings/reviewer/generation, credential-free inspection, exclusive EXECUTE Take, capture/join/plaintext cleanup and durable Finish before publication. Child runtime/workspace/checkpoint lifetime remains independent after parent deletion; no migration, replacement credentials, command replay or path rewrite.

### cmds/delidev-cli/internal/cli constraints

- `session fork` uses authenticated Fork/GetSessionFork and preserves accepted job/request identities on bounded wait failure. Keep dispatch in `dispatch_session.go`, retain the two-minute Worker bound with a 145-second command deadline, and never replay creation while observing a job. Follow `cmds-delidev-forks-contract.md`.

### cmds/delidev-cli/internal/domain constraints

- Same-account Codex forks follow `cmds-delidev-forks-contract.md`. Keep source boundary reservations read-only, original actor/current account checks at acceptance/claim/publication, once-only journaled native Fork, private rollout proof, complete multi-repository snapshot checks across native creation, separate opened roots for copy reads/writes, synchronized copied files/directories, independent child queues and immutable continuation settings. Unknown native/cleanup outcomes never authorize another Fork.

- Fork-origin metadata is a validated child-owned immutable seed with original Worker cleanup device and checkpoint/input digests. Permanent-deletion work may contain a bounded child fork-runtime ownership reference before any execution copy exists; parent work must never adopt a published child runtime. Follow `cmds-delidev-forks-contract.md`.

- Observed native subagents retain version-1 paused completion even after every child closes. Fork requires an independent version-2 checkpoint; child observation or cleanup cannot promote that completion. Preserve both the subagent and fork contracts.

- Native Fork accepts API-authenticated Codex sources and the separately negotiated bounded OpenCode Unix General Chat profile. OpenCode freezes all five original operation UUIDs, a complete native message/part clone map and child-owned inherited transcript provenance before publication; inherited messages grant no queue/input or accounting authority. Reject managed subscription configuration before acceptance and Worker journaling until Fork owns a separately verified protected lease and joined credential write-back; native source inspection grants no authentication authority.

- The existing 1 MiB Worker job output bound also covers the complete OpenCode Fork identity map. Worker preflight must reserve the entire serialized result before native mutation; native message/part inspection maxima cannot authorize truncation or an oversized durable output.

- Fork publication retains the original v4 startup selection on the child before parent-owned creation jobs can be purged. Use that child-owned digest/selection for independent execution; missing older seed evidence requires recovery, never a replacement executable. Response usage accepts optional bounded original startup version metadata, and compaction attribution reads the exact completed source job readiness without rewriting assignments. Follow the Fork, startup and usage contracts.

- OpenCode independent Fork children retain their original native creation request and closed original publication proof atomically in the immutable child-owned seed at verified publication. Bind the expected request to the child, original input/output digests, runtime/checkpoint, Worker and complete immutable selection before recovery admission; a mutable valid UUID grants no authority. First and later completed-report recovery use resumed native claim version 2 and the independent Fork runtime, preserving current binding/input and original Worker device. Legacy omitted markers require the exact retained completed Fork input/output, digest, child/runtime/native checkpoint and immutable selection; missing or foreign proof stays pending. Parent deletion cannot become a new-child recovery dependency. No migration or protocol allocation.

- Manual compaction of a settled first Fork-child turn clears the one-shot Fork import in its fresh restore and retains the exact independent Fork runtime as history. Preserve legacy assignment 3 to restore 2, startup assignment 4 to restore 4, all immutable source/account/configuration/checkpoint and cleanup facts, negotiated harness capability and Sidechat read-only/dependent-parent gates. No native Fork replay, allocation or migration.

### cmds/delidev-cli/internal/harness/opencode constraints

- Native summaries/forks must retain original source identities, complete history, nullable usage and protected runtime ownership. Never manufacture history, rewrite native database rows, infer native acceptance from HTTP success or replay uncertain native mutations.

- Issue #1210 preparation owns only bounded Unix plain-text General Chat native Fork, complete ID-clone/history proof, explicit no-change relocation and copied-source deletion. Use an unregistered fresh nonce without inference; native agent/model absence is an explicit preparation state. Claim every mutation once and reconcile exact owned native state after response loss without resend. Preserve inherited paths/usage as provenance, not filesystem or accounting authority.

- Native checkpoint metadata uses the strict declared 8 MiB decoder ceiling, retaining canonical bytes and independent complete private-file inventory verification. Fork source inventory exposes content-free original IDs only after full private checkpoint/profile validation; it grants no native mutation or inference authority.

- Fork source eligibility reserves the complete serialized child checkpoint before workspace copying or any native claim, including duplicated histories, full identities and metadata bounds under 8 MiB. Conservatively retain all source file descriptors in the estimate and reserve the separate 64 KiB fresh SQLite runtime inventory profile. Capacity exhaustion grants no mutation; unexpected native auxiliary growth cannot enlarge this profile. Follow `cmds-delidev-forks-contract.md`.

### cmds/delidev-cli/internal/process constraints

- Unpublished Fork owner retirement requires joined completed native scopes, the retained directory identity when available, an exclusive recovery lock, empty-index removal and synchronized parent/absence checks. A path or arbitrary empty index cannot reconstruct original ownership; foreign and uncertain evidence stays preserved. Callers retain their original job admission fence.

### cmds/delidev-cli/internal/server constraints

- Same-account Codex forks follow `cmds-delidev-forks-contract.md`. Keep source boundary reservations read-only, original actor/current account checks at acceptance/claim/publication, once-only journaled native Fork, private rollout proof, complete multi-repository snapshot checks across native creation, separate opened roots for copy reads/writes, synchronized copied files/directories, independent child queues and immutable continuation settings. Reject sessions with retained fork-origin metadata before accepting another fork job, including children with a completed turn. Verified pre-native copy failure settles failed and releases the source reservation; unproved cleanup remains uncertain. Unknown native/cleanup outcomes never authorize another Fork.

- Fork children retain their immutable publication seed and original Worker cleanup device independently of parent records. First continuation must survive parent permanent deletion; child deletion before first input must remove only its digest-bound runtime and owned workspace. An unresolved unpublished fork blocks parent deletion intent. Preserve the stable original Git registration source after comparing common directories; never use a removed parent managed path as child cleanup authority. Follow `cmds-delidev-forks-contract.md`.

- Reject version-2 completion reports when original execution progress contains observed subagents. Fork independently requires a version-2 checkpoint, so a closed observed tree cannot authorize a fork or upgrade version-1 completion.

- First fork-child dispatch uses the same stale/future Worker-instance checks as ordinary initial and continuation dispatch, with only the existing one-second future tolerance. A rejected clock observation cannot consume queued input, publish execution work or mark an empty Resume ready.

- First fork-child lost-report recovery derives `HistoryExecutionID` from the immutable assignment's fork runtime, independently of the fresh execution/report ID. Preserve the original thread, accepted input, terminal outcome and paused recovery gates; later continuations retain their independently assigned history identity.

- Bind first fork-child `ThreadBound` publication to the exact assigned native child thread and reject `InputAccepted` reuse of the inherited source turn. Validate both before changing progress or queue accounting; a rejected event cannot change the child, source or original execution job.

- Fork status observation and acceptance-receipt reads require current owner/client authority inside the same read transaction before retrieving job or child documents. Worker or missing principals cannot observe this client-only RPC; a stale paired principal cannot bypass current device authorization.

- Fork acceptance, claim and publication require a present workspace with no pending or uncertain storage owner. Workspace storage waits for every original fork reservation to settle before admitting work; neither operation can bypass the other's retained ownership.

- The independent OpenCode General Chat Fork profile requires System 26/Worker 15, a pinned Unix API-only Build/Execute source and complete plain-text history. Freeze all native operation identities at admission; recheck original account/connection, installation, Worker and source revision at claim/publication. Atomically clone only mapped complete canonical text into a paused empty-queue child with explicit inherited provenance; never copy input authority or usage. Follow `cmds-delidev-forks-contract.md`.

- Remote repository saves accept credential-free URLs with no checkout or Worker validation child. Pin URL/source enum in session and schedule requests. Require capability 19 at managed-clone and independent-Fork admission and assignment; validate independent Fork results without acquiring the parent Git lifetime. Follow the workspace/protocol contracts.

### cmds/delidev-cli/internal/skills constraints

- Joined original session deletion retires accepted snapshot intents. Accepted copies have no automatic expiry. Runtime and independent Fork copies retain their own lifetimes.

### cmds/delidev-cli/internal/store constraints

- Failed independent Fork deletion copies retain their original child process owner. Enrich legacy pending plans only from digest/revision/instance/session/machine/device/runtime-matched immutable job assignments; missing or succeeded-job proof stays unresolved. Preserve unrelated omitted fields, original deletion identities and acknowledged receipts. Stale acknowledgements cannot omit the enriched work. Follow the Fork contract; no RPC or migration.

### cmds/delidev-cli/internal/worker constraints

- Same-account Codex forks follow `cmds-delidev-forks-contract.md`. Keep source boundary reservations read-only, original actor/current account checks at acceptance/claim/publication, once-only journaled native Fork, private rollout proof, complete multi-repository snapshot checks across native creation, separate opened roots for copy reads/writes, synchronized copied files/directories, independent child queues and immutable continuation settings. Pre-native file/HEAD/index snapshot drift must use verified rollback of all owned copies; preserve uncertainty for unproved Git process cleanup and for drift after native creation. Native rollout descendants use owner-only Windows DACL validation and Unix write-bit refusal; Windows permission emulation cannot prove private access. Normalize Git index paths to the Worker-native namespace before canonical comparison. Unknown native/cleanup outcomes never authorize another Fork.

- Rejected Codex fork-source inspection must remove its proven-unused fresh runtime after confirmed source-process closure, synchronize the parent and verify absence before a definite failure releases the reservation. Unjoined inspection or unconfirmed removal remains uncertain; successful inspection retains its runtime for the original native creation.

- Apply the same unused-runtime cleanup to all later pre-native fork workspace rejections after joined source inspection and independently confirmed owned-copy rollback. A `RecoveryRequired` workspace result provides no rollback proof and retains the runtime even before native creation. Revoke removal authority before attempting the child-native process; unknown inspection or possible native child state remains retained. Log closed phases and stable codes only.

- OpenCode General Chat Fork independently verifies the original accepted ordinary v2 report, claims/outbox, cleanup and full native checkpoint under the exact account/connection before copying. Preparation uses a fresh unregistered nonce and five synchronized once-only native claims; keep its envelope/claims outside the native snapshot inventory but inside the child-owned runtime. First ordinary child input independently proves native agent/model before public ThreadBound; retained preparation absence grants no observed selection. Inherited canonical messages and native usage remain provenance only. Follow `cmds-delidev-forks-contract.md`.

- OpenCode Fork reads source/child execution checkpoints with the strict 9 MiB private checkpoint bound; Claude/Codex compaction readers retain the strict 10 MiB bound. OpenCode compaction retention and all restoration readers use one strict 12 MiB canonical envelope, reserving its independently bounded 8 MiB native document, 3 MiB accepted input and 1 MiB closed ownership metadata. Canonical bytes, original native history and all identity/claim checks remain mandatory. Complete OpenCode Fork mappings must fit the existing 1 MiB job output before any workspace copy or native mutation. Reserve a closed 64 KiB General Chat manifest envelope; oversized results fail with ResourceExhausted without truncation or native claims.

- Permanent deletion verifies original OpenCode fork checkpoints with the strict 9 MiB decoder and legacy Codex fork checkpoints with their declared bound. Preserve canonical bytes, digest and original job/runtime/session/machine checks before any removal.

- Negotiate capability 19 before managed workspace or independent Fork cloning. Preserve original source enums, URL, once-only ownership and process-exit uncertainty through leases, recovery, snapshots and deletion; Git authentication belongs to the selected Worker's helper/SSH environment. Follow the workspace/protocol contracts.

- Published Fork runtimes retain their original resolved startup executable independently of source-owned job journals. Resolve the child-owned identity first; legacy journal fallback cannot select a replacement after missing original evidence. Follow the Fork and direct startup contracts.

- Unpublished independent Fork failure owns the original child Git process index. Retire only completed original scopes under the retained directory identity after definite pre-native rejection, independently of caller cancellation. Failed-Fork deletion carries the exact private child owner; started/complete deletion replay treats retired owner paths as absence-only. Foreign, missing original proof or uncertain cleanup stays recovery-required; published children remain independent. Follow the Fork contract.

### cmds/delidev-cli/internal/workspace constraints

- OpenCode General Chat Fork uses the closed `opencode-general-chat-v1` copy profile with an independent 8,192-entry/256 MiB bound. Preserve omitted legacy Codex profiles and their bounds; apply the chosen bound to initial inspection, actual copy and both final comparisons. Only independently owned sibling managed session roots may be relocated; no links, Git administration or special files may enter this copy.

- Remote Worktree sources follow the workspace contract: persist original clone ownership before Git, require native commitments and joined process cleanup, retain uncertain outcomes and legacy accepted requests, and keep independent Fork Git stores and metadata-only Sidechat lifetimes. Managed session clones are an explicit scratch-creation exception with session ownership; they grant no Local folder deletion authority.

- Fork scanners exclude native `.git` aliases by anchored same-parent identity, not case folding. Omit only declared top-level administration; reject General Chat/nested administration, preserve distinct ordinary case-sensitive `.GIT` entries, never follow gitdir pointers, and retain the original snapshot marker presence/identity across copy/final verification; later scans cannot create replacement authority. Follow the fork contract.

## Storage

### Native and workspace ownership

Use pinned Codex `0.151.0` `thread/fork` with inclusive `lastTurnId`, explicit
child cwd and runtime workspace roots. Path import may name only the exact
validated rollout returned for the original Worker-private runtime. Reject
incomplete, active, paginated, child, goal and unsupported histories. The initial
profile supports complete user/assistant text and reasoning turns (at most 128
turns / 4 MiB, with a 64 MiB rollout); API Fork and Sidechat retain the text/reasoning profile. Managed independent Fork separately admits the closed settled-tool profile below; other rich auxiliary histories remain unsupported. Unknown
creation or cleanup outcomes retain uncertainty and never authorize replay.

Project forks default to separate detached worktrees at every actual source
HEAD, including unpushed commits, index changes, working changes and ignored or
untracked files. No fetch may move that boundary. General Chat copies its owned
files to a new owned directory. Explicit Local sharing requires independently
authenticated same-machine Worker authority and an original Local source whose
checkouts are all user-owned. Reject Local sharing from managed Worktree sources
before acceptance and again at Worker preparation/publication: parent deletion
owns those paths. Use an independent Worktree copy instead. Copying is bounded to 256 MiB and 100,000 entries per tree and a two-minute
Worker deadline, with per-chunk cancellation;
source observations are compared before and after all repositories. Unsupported
files, mixed snapshots or a failed second copy cannot publish a partial child.
Fork scans bind each parent’s native `.git` entry with anchored, non-following
identity checks. Exclude only the declared repository’s top-level administration;
reject its native case aliases in General Chat and nested directories. Distinct
ordinary `.GIT` entries remain copyable on case-sensitive filesystems. Recheck
the original marker presence and native identity in transient snapshot evidence
through inspection, copying and final verification. Later scans cannot replace
this pin. Identity changes retain snapshot conflict and cannot publish a child. Never follow gitdir pointers.
Source reads and destination writes use separate opened filesystem roots;
canonical destination validation rejects linked targets before copying. Copied
files and directories are synchronized before the ready manifest is published,
and original permission modes are restored independently of the Worker umask.
Git-reported index paths use the native Worker separator and path comparison
rules before canonical ownership checks; Windows slash-separated Git output
must not be compared as an unnormalized Unix path.

Optional preparation/session/execution JSON fields and job kinds are additive.
Existing records retain their omitted-field bytes. Fork uses the existing
synchronized job journal, UUID-v7 receipts, private native runtime and atomic
SQLite state/events; no destructive schema migration or external object storage
is introduced. The Worker stores one canonical synchronized `fork-completion.json`
whose exact digest binds the native child and its immutable manifest/configuration.

### Permanent deletion and independent child lifetime

Publication retains the immutable configuration/boundary seed and original Worker
cleanup device on the child. First continuation verifies that seed and its own
preparation/checkpoint, without requiring any surviving parent session or fork
job. Parent permanent deletion removes only the parent-owned job journals and
native histories; it preserves published child runtimes and managed worktrees.
Deletion before the child's first input still requires the owning Worker's
acknowledgement of both its workspace and exact digest-bound fork runtime.
The cleanup plan carries bounded identities/digests, never native paths or history.
Interrupted child cleanup retains its original tombstone and checkpoint ownership
proof. A completed acknowledgement is reusable only while every original copy
remains absent. An unpublished unresolved fork blocks accepting irreversible
parent deletion until that original operation proves cleanup or publication.

A managed child copies from the parent's actual repository path while retaining
the original manifest's stable registration source, after comparing both Git
common directories. Recovery and removal use that stable source after the parent
managed workspace has been removed. Explicit Local still owns no checkout files.

Managed database restore follows the shared storage contract and refuses
replacement while a claimed or uncertain fork retains native ownership.
Restoration preserves published fork metadata and original execution selection,
while retaining paused dispatch and requiring recovery. Database publication
alone cannot prove native cleanup or authorize continuation or another Fork.

## Security

### Failure ownership

Acceptance, actor, Worker instance, original assignment, complete checkpoint,
workspace manifest and exact native request are independently bound. Current
source revision and authority are checked again when claiming and publishing.
The ordinary synchronized Worker journal precedes native creation; a started
operation is never retried after reconnect. Definite pre-native/copy failures may
settle failed without a child. File, HEAD and index drift detected before native
creation must roll back every verified owned copy and release the source job
reservation only after cleanup succeeds. Keep the original pre-copy observation
through preparation so changes before the first copy are also rejected before
native creation. Unproved Git process ownership or failed cleanup remains
uncertain; the same drift detected after native creation remains uncertain.
Unknown creation or cleanup remains uncertain,
retains the private runtime/workspace and holds the source reservation for
operator investigation. No source execution claim is advanced by inspection.

Rejected source-history inspection removes its proven-unused fresh runtime only
after independently confirmed source-process cleanup. Synchronize its parent and
confirm absence before returning a definite rejection; failed removal or unjoined
inspection retains uncertainty. Successful inspection keeps the runtime for the
original native creation attempt.

Workspace request derivation checks all repository manifest eligibility and the
complete request structure before creating a child process index or running Git.
An unborn later repository, duplicate repository ID or missing primary therefore
cannot leave an unpublished child process scope after definite rejection. Actual
source HEADs are read only after those checks; native read/ownership failures
retain recovery classification and process evidence.

Unpublished independent Fork preparation owns its child Git process index.
Create that index exclusively before HEAD reads; General Chat creates no index
when there are no repository reads. Retain the original native directory identity
through inspection and copying. After a definite pre-native rejection, independently
reconcile completed original scopes, verify that same empty index, remove only
the index and its original recovery lock, synchronize parents and confirm absence.
Any unjoined, foreign, changed or uncertain owner preserves recovery-required
classification and its evidence. Published children keep their independent owner.

Failed-Fork permanent deletion copies carry an omitted-zero private child process
owner from the immutable validated original ForkJobInput. Normal deletion and
completed-proof replay inventory that exact index and recovery lock. Legacy pending
plans may add this omitted owner only after original assignment digest, revision,
instance, session, machine, device and runtime checks. Keep unrelated omitted bytes
unchanged. Rebind only an untouched Worker proof whose digest equals the same plan
with the newly added fields omitted; started/complete legacy removal without those
original proofs stays unresolved. Reappearing retired indexes are absence-only and
never gain removal authority from a completed or started deletion proof. This adds
no public capability, RPC allocation or database migration.

Every later pre-native workspace rejection applies the same unused-runtime
cleanup after joined source inspection and independently verified owned-copy
rollback. A `RecoveryRequired` workspace result may leave owned copies without
attempting rollback and cannot authorize runtime removal, even before native
creation. Disable this removal authority before attempting to open the native
child process: possible child state and unjoined inspection always remain
retained. Log only the closed runtime phase and stable failure code.

The product never selects a rollout path. Native metadata selects one exact
regular Worker-private `sessions/*.jsonl` file, with canonical rooted accesses,
link/special-file refusal, immutable byte proof and before/after identity checks.
Rollout descendant permissions use owner-only Windows DACL checks through the
private-state security owner; Unix keeps its existing group/other write-bit
refusal. A private home alone cannot hide an unprotected linked file. Go's
emulated Windows permission bits are not an ACL observation.
Secret Local proof is dedicated write-only authority, excluded from receipts.
Fork receives no registered inference grant; only later ordinary execution may
receive one after current eligibility checks. Imported source workspaces remain
original-owned; the fork coordinator never advances their execution claim.

## Logging

Keep structured `log/slog` start/finish, accepted request/job/source identities,
native request ownership, stable outcome codes and ordinary process/preparation
lifecycle evidence. Never log rollout paths/bodies, copied bytes, prompts,
configuration content, credential values, private environments or raw native
errors. An uncertain result remains independently visible in its retained job.

## Build and Test

Use temporary Worker/database/repository/native-provider fixtures without user
credentials. Keep deterministic implementation verification separate from
installed native, real-account, other-platform and release acceptance. Required
checks are root `go test -race ./cmds/delidev-cli/...`, root
`go vet ./cmds/delidev-cli/...` and `pnpm proto:check` for schema changes.

Frontend changes additionally require `pnpm test` from `apps/delidev`. Native
fixtures are explicitly opted in with `DELIDEV_NATIVE_THREAD_EXECUTABLE`; use
private generated homes and keyless scripted loopback providers. Required output
packages are generated before compilation, and repository-owned `dist` output
is removed from the final worktree.

## Dependencies and Integrations

Reuse existing workspace closed inspections, process ownership/cleanup, Worker
journals/outboxes, immutable execution checkpoints, current account selection and
Connect generation. Native Fork is the pinned official app-server method; no
transcript synthesis, cross-account routing, SDK/engine or hosted service is added.

## Change Triggers

Update this contract, project index, issue-owned evidence, session/workspace/harness
contracts and the relevant scoped Go owners' `AGENTS.md` files for profile or ownership changes.
Update protocol/client contracts, their AGENTS files and generated bindings for
RPC changes. Keep desktop contracts/AGENTS synchronized with presentation changes.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [DeliDev project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Session contract](cmds-delidev-sessions-contract.md)
- [Workspace contract](cmds-delidev-workspace-contract.md)
- [Harness contract](cmds-delidev-harness-contract.md)
- [Issue #1092](https://github.com/delinoio/oss/issues/1092)
- [Complete issue #964 requirements](cmds-delidev-requirements.md)

Workspace storage and fork ownership compose at the original source boundary. When
workspace storage is supported, fork acceptance, claim and publication require a
present workspace without a pending or uncertain storage operation. Storage
admission waits for unresolved fork jobs to settle; a stored workspace must be
explicitly restored before it can be forked.

## Bounded OpenCode General Chat fork (#1210)

The independent System 26 / Worker 15 profile uses pinned OpenCode `1.18.32`
(commit `545f51d26cc39a907d2867492d498d9607ea5fa4`) and the existing authenticated
ForkSession/CLI/desktop job. Only existing Unix non-VCS General Chat Build/Execute
sources qualify: latest successful accepted v2 completion, joined cleanup,
original API account/Worker/model/configuration, and complete plain user/assistant
text with step-start/finish. Reject Plan, reasoning, tools, snapshots, media,
compaction, children, remembered permissions, native workspace routing and
already-forked sources. Recheck current authority at acceptance, claim,
publication and child execution; reserve the source against dispatch, file
writers, Stop, Archive and cleanup throughout capture. Original queued input
remains on its source.

Copy all regular General Chat files, including hidden/ignored files and executable
modes, into an exclusively created independent sibling. Bound this profile to
8,192 entries / 256 MiB, compare complete source inventories before/after native
creation, reject links/special entries/private overlap/active owned writers,
and retain cleanup only for the new job-owned artifacts. These checks detect
same-user changes without claiming to prevent every external filesystem writer.

The preparation runtime stages only independently verified native SQLite/WAL/SHM,
regenerates isolated configuration/instructions and uses fresh authenticated
loopback control plus a correctly formatted fresh execution nonce that is never
registered with the relay. Preparation has no protected-key or upstream authority;
an attempted inference fails relay authority before key retrieval. Ordinary
execution still requires registration. No synthetic or replayed prompt establishes
fork metadata.

Read the copied original source session and full ordered history before one
synchronized native `POST /session/{sourceID}/fork` with empty body and no
messageID. Source-directory routing is intentional. Independently prove a unique
new root and complete order-preserving message/part ID replacement, child-local
session references and remapped assistant parent IDs. Every other supported field
is unchanged; historical assistant paths are provenance only.

After that proof, claim once and call authenticated native
`POST /experimental/control-plane/move-session` with the exact child ID,
`destination.directory` and explicit `moveChanges=false`. Both directories must
have the original global native project identity. Independently re-read the
session/history/status/interactions and permit only documented directory/path
and native update-time changes. Never capture/apply/discard Git changes, rewrite
database rows or substitute path aliases. Native child agent/model absence is a
closed preparation state, never an effective selection observation.

Require child permission absence before the once-claimed native PATCH, preserve
closed original unrelated metadata, set the fork request marker and explicit
empty permission array, then independently read both back. Prove no parentID or
foreign child references before deleting only the copied original source session
through its once-claimed native DELETE. Require original-source not-found,
sole-child inventory, unchanged child history and no pending work. The original
runtime/checkpoint/files remain untouched.

Join the preparation native process before synchronized private checkpoint
publication. Retain complete source lineage/ID map/history digests, relocated
child/root, independent file inventory, exact configuration and absent-versus-
observed native selection. The first real child input restores this child profile,
uses explicit immutable agent/provider/model/variant and independently observes
native selection/acceptance. Later ordinary checkpoints preserve inherited history
without adding its usage. Publish child, cloned canonical transcript and source
link atomically after native/files/cleanup proof, with empty paused queue.

Lost fork acknowledgment may reconcile only a unique complete new root in the
original owned runtime, using complete source/history comparison and durable
intent; never resend. Move/PATCH/delete uncertainty likewise reconciles only their
exact observed state. Partial/multiple children, changed history or unproved
cleanup remain recovery-required and block child input. Retry the original product
receipt after server/Worker replacement without native replay. Keep native fixtures,
real accounts and platform/package acceptance distinct in PR/issue/CI records.

The desktop checks the complete retained OpenCode transcript before presenting
Fork. Its cancellable authenticated Resource RPC read is keyed to the original
session revision and native thread, spans at most 10,000 records/8 MiB and
rejects repeated/incomplete pages, tool/artifact content and non-empty changes.
An original-revision read before and after pagination detects changed sources.
Submit refreshes the profile and does not use stale success after read failure.
Desktop preflight captures one immutable source/revision/turn and draft generation
before refreshing. Discard or replacement invalidates that generation; recheck it
after each asynchronous prerequisite and before ForkSession admission. Late reads
cannot submit the old source or populate another draft. Hiding the operation
retains its original preflight, accepted job and exact uncertain retry.
A definite rejected draft retains its diagnostic while hidden and reopened in
that original context. Explicit discard or replacement with another source or
purpose clears only terminal rejection presentation from the connection-owned
mutation registry. The clearing operation rechecks current ownership and cannot
remove a pending/uncertain original request or an accepted job. Reopening a
discarded source starts a clean draft; managed subscription admission is unchanged.
These reads grant no child/native authority: Go independently validates complete
canonical history at acceptance/publication, and the Worker verifies complete
native history before exposing the child.

OpenCode source and child checkpoint readers use the strict 9 MiB private checkpoint ceiling while retaining canonical and native identity verification. Before copying the workspace or claiming any native mutation, the Worker verifies the complete source identity inventory and reserves the full serialized result, including a 64 KiB closed General Chat manifest envelope, under the existing 1 MiB job output limit. Sources whose complete mappings do not fit return ResourceExhausted with the original session preserved. The native history inspection maxima do not waive this publication bound; mappings are never truncated.

Permanent deletion reads original OpenCode fork-completion metadata with the strict 9 MiB checkpoint decoder and the legacy Codex profile with its declared decoder bound. The original digest, canonical bytes and job/runtime/session/machine bindings remain required; larger valid private checkpoints do not strand deletion and replacements grant no cleanup authority.

The same pre-copy eligibility reserves the complete child native checkpoint under its 8 MiB ceiling: original histories, two cloned-history copies, the complete message/part identity proof, metadata bounds and every original file descriptor. The fresh copied SQLite runtime has a separate 64 KiB serialized file-inventory profile. Capacity rejection preserves the source before workspace copying, runtime creation or native claims. Unexpected native auxiliary growth remains unsupported uncertainty and cannot enlarge this admitted profile.

## Image ownership at the Fork boundary

Independent Fork publication atomically adds child ownership for images in the original accepted prefix through the completed native boundary. Later source inputs grant no child ownership. Child references survive source deletion. Last-owner deletion retains the original Worker device and joined native cleanup. Follow the image-input and storage contracts.

### OpenCode child completed-report recovery

Verified OpenCode Fork publication retains the exact original creation request in the immutable child-owned seed. First and all later child executions use that marker and the independent Fork runtime for read-only completed-report recovery, without a synthetic first execution at the runtime ID or a surviving parent job. Native resumed claim version 2 is separate from execution assignment versions 3 and 4. Current binding/input requests, original assignment revision/digests, accepted inputs, Worker device, checkpoint and cleanup remain independently checked; successful reconciliation preserves the report and leaves dispatch paused.

Legacy seeds with an omitted marker may resolve only through the exact retained succeeded Fork job, original input digest and validated complete input/output, source/child/runtime/native checkpoint, original device, snapshot/account/connection and configuration. Missing, deleted, changed or ambiguous proof retains recovery; no marker comes from a native ID and Fork is never repeated. The additive private field is omitted from old records and needs no migration or protocol allocation.

OpenCode lost-report recovery checks a child-owned immutable closed creation proof before admitting inspection work. Verified Fork publication atomically retains the expected creation request, child ID, original accepted output digest and digest binding of the complete original Fork boundary (including accepted input digest, native checkpoint/runtime, Worker and selection). This retains no source prompt or protected native content and survives independent parent purge. Changed valid creation UUIDs, checkpoint/runtime/selection or proof ownership reject before admission. Older seeds without that proof must compare any recorded marker against the exact retained completed original Fork input/output; absence remains recovery-required. The proof is trusted server-owned publication metadata, never authority derived from a later mutable marker; no RPC, allocation or migration.

A settled eligible first child turn may become a manual compaction source under the compaction contract. Its new continuation restore clears the one-shot Fork import and retains the child-owned Fork runtime as history, preserving legacy 3-to-2 and startup 4-to-4 assignment profiles. Independent child lifetime and Sidechat dependent/read-only ownership remain separate.

## Managed ChatGPT independent Fork — issue #1979

Record System capability 53 `MANAGED_CODEX_FORK_V1` and Worker capability 29 in the complete feature PR. Preserve existing ownership, including Sidechat 27/16, protected subscriptions 3 and managed Sidechat 47/26. These declarations compose complete runtime support; they alone prove no native/account/platform acceptance. No SQLite migration is added.

Version 1 with explicit or omitted independent purpose retains the existing RPC and child publication format. Admission, claim, publication and child continuation require the separate managed Fork capability and original account/connection. Freeze original actor, source revision, completed native turn, immutable configuration and reviewer, generation, startup identity, Worker machine/device/instance and claimed job revision. Busy lease, pending lifecycle, recovery and active server/Worker observations refuse admission before protected credentials or native work. No new login, account fallback, sandbox expansion or reviewer substitution is permitted.

Inspect the original source without authentication files. Only the newly owned child runtime receives the original account's exact Fork EXECUTE Take. Native Fork makes one inclusive boundary attempt; capture the final bundle, join the original child, compare/remove/scan plaintext and commit the original Finish receipt before publication. Lost responses and uncertain creation or cleanup retain original recovery authority without retry. Logs contain only structured identities and safe phases.

The managed history profile retains complete native user text, assistant text and reasoning plus terminal `commandExecution` and `fileChange` items. Command source is ordinary agent or user shell, without plugin/script provenance. Executed completed/failed commands require an exit code; declined commands retain no-send semantics. File changes must be terminal. Preserve item and turn IDs, order, status, source, exact command/cwd, output, exit, paths, patches and native metadata in the whole normalized JSON comparison. Never replay tools or rewrite inherited tool paths. Existing authenticated typed skill-package rehoming remains separate and preserves opaque/user text. Reject active, unknown, rich-media, goal, child-agent and compacted histories. Retain the original 128-turn, 4 MiB history and 64 MiB rollout bounds, complete pagination, unique identities and before/after source digest checks.

The child privately retains a complete inherited-history digest and count. Its first Resume rereads the complete closed profile and verifies that digest before new input, in addition to original settings, reviewer, native idle and latest-turn checks. The digest grants no replay or runtime authority.

Existing workspace ownership applies unchanged: Worktree copies actual HEAD and dirty contents, General Chat copies owned files, and Local preserves its original machine rules. Publish paused with an empty queue. Independent parent deletion preserves child-owned checkpoint, runtime and workspace; later turns retain the original account and immutable reviewer/branch-prefix provenance. Parent-dependent read-only Sidechat remains a separate overlay and deletion profile.

Automated fixtures and builds validate these boundaries. Installed native, real-account, remote-machine, platform and manual visual acceptance remain owner-assigned and are not claimed by those checks.
## Inherited elapsed observations (issue #2052)

Completed inherited OpenCode user Messages copy original server-owned turn timing with their existing immutable `inherited_from` source session/execution/input/native provenance. Child remapping does not rewrite either observation, and later child input captures a separate interval. Timing is copied only after the original canonical/native mapping checks; it adds no Fork eligibility or native history proof. Fork creation/job/checkpoint/digest inputs omit display timing and retain their prior closed Worker shape.
