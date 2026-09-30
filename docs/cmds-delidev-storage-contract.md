# DeliDev storage operations

## Scope

Go owns managed database backups, session deletion, Worker snapshots and recovery.
The approved completion work includes every remaining issue #964 requirement;
actual account/private-GitHub access and platform distribution validation remain
deferred. This document covers managed backup observation, creation/deletion, permanent
session deletion and Worker-local workspace snapshots and restoration. Database restoration remains pending.

## Managed backup observation

`SystemService.CreateBackup`, `ListBackups` and `InspectBackup` are available only
to the owner and paired clients. Workers cannot invoke them. The existing
actor-authenticated creation receipt reserves one backup UUID before filesystem
work; the same request retries the original image instead of creating another.

`delidev backup create [--wait]`, `backup list [--limit N] [--page-token TOKEN]` and
`backup inspect --id ID` use those same RPCs. Ordinary commands never start a
server. Settings > Backups provides creation, pagination and explicit integrity
inspection, retaining the exact creation request after an uncertain response.
The legacy `CreateBackup` RPC retains its synchronous receipt behavior for existing clients; current CLI/desktop creation uses the durable path below. Backup sizes use decimal strings in CLI JSON and BigInt in the desktop.

Listing returns UUID, byte size and modification time only; it does not establish
integrity, creation provenance or restoration eligibility. Default page size is
50, maximum 100. Signed cursors bind the complete inventory metadata and page size;
changed inventories require restarting pagination. The five-second inventory
operation examines at most 4,096 directory entries, rejects invalid published
backup identities/private-file ownership, and excludes unpublished scratch files.
It never returns a partial inventory as complete.

Inspection has a thirty-second deadline and an 8 GiB managed image limit.
Creation and pre-migration backups enforce the same exact copied-image limit
before publication, preserving the live database on rejection. Durable creation
settles a size-limit rejection as failed/resource-exhausted; it does not retry
forever. Unpublished scratch files are removed. It checks
the canonical UUID-derived path within the private managed directory, rejects
symlinks and SQLite sidecars, verifies the opened file identity, and copies bounded
chunks into an exclusive private scratch file while computing SHA-256. SQLite
opens only that scratch image in immutable read-only mode. Application/schema,
quick integrity, foreign keys and original server identity must validate; source
identity, mode, size and modification time are checked again before publication.
WAL, SHM and rollback-journal absence is also rechecked at that boundary; adjacent
state created during copying invalidates the observation even if the main image
bytes and identity are unchanged.
Scratch cleanup occurs on success and failure. A killed process can leave an
unpublished inspection scratch file; listing never treats it as a backup.

The result contains only original metadata, SHA-256, schema version and server
UUID. Client authorization is revalidated after filesystem work. Logs contain
operation, backup UUID, schema and correlation/error codes, never paths, database
content or credentials. No inspection opens a credential store, changes the
original image, restores a database or proves Worker/browser/credential recovery.
Failed reinspection removes the desktop's earlier success indication.

## Durable managed backup creation

`SystemService.RequestBackup` reserves a generic `create-backup` job and original
image UUID in one actor/server-bound UUID-v7 receipt. `GetBackupCreation` and
`ListBackupCreations` expose typed pending/succeeded/failed state, revision,
timestamps and safe problem codes. Pages are signed against the current actor and
page size (20 default, 99 maximum); at most 32 creations may remain pending.
New creation has no prior image revision. Repeating a request reads the current
original job, including after publication; changing its actor cannot borrow it.
No additional SQLite migration is required beyond the existing job schema.

The server owns a joined maintenance controller independently of the requesting
client, retries pending jobs every two seconds and bounds each image attempt to
thirty seconds. Original device authority is rechecked before copying. Lock
waiters observe cancellation while another backup holds file ownership; shutdown
joins both creation and deletion controllers before reporting server stopped.
SQLite still supplies a consistent committed image. Capture time is the actual
successful copy, not acceptance time. A canceled client does not cancel its job;
server interruption preserves pending state and the reserved identity.

If the original file was published before a lost completion acknowledgment, retry
validates and synchronizes that same file. Terminal success never recreates a
later removed image. External permanent deletion always wins, including between
image publication and job settlement. Revoked authority, corrupt ownership or a
deletion obligation ends the job with a typed failure; transient storage failure
remains pending. Unchanged failures do not grow revisions or mutation receipts.
The original creation actor reaches `BackupID` unchanged. Its final authorization
occurs under the exclusive store gate retained through image creation and
publication, after any earlier read; a revocation that commits before that gate
prevents even the unpublished image from being created. Maintenance owner
authority is used only to retain the job outcome.
Success records historical publication, not current file availability, integrity
at a later time, credential recovery or database restoration.

CLI `backup create` now returns the accepted job. `--wait` observes its existing
job until terminal state or caller cancellation without canceling creation.
Failed completion returns a typed nonzero exit with the original problem code
and accepted job/request details; interrupted waiting and failed reads retain
the last accepted result too. A pending acceptance alone is not waited success.
`backup creation --id JOB-ID` and `backup creations` work after restart. Settings
keeps exact uncertain acceptance retries, polls visible creation jobs, refreshes
the image inventory on completed publication and labels stale/unknown results.

## Permanent managed backup deletion

`SystemService.DeleteBackup` accepts an owner/client UUID-v7 request bound to the
original inspected backup ID, immutable live revision 1, byte count, modification
time and SHA-256. `ListBackupDeletions` provides signed actor/page-size-bound job
pagination (20 default, 99 maximum). The same request returns the current original
job. Other requests cannot replace an accepted deletion, and Workers cannot call
these operations. A queued acceptance is not proof of file removal.

Schema 24 combines the backup-to-job/request index and automatic-title usage/claims after the main provider migrations in 21 and 22 through the existing synchronized
backup-first migration. Deletions use the existing durable job and request-receipt
infrastructure. Backup creation/inspection/deletion share a server lock; accepted
SQL ownership immediately prevents an old creation request from recreating the
image. Before acknowledging acceptance or performing any unlink, the server synchronizes a versioned,
private, metadata-only intent in `backup-deletions/`, outside the replaceable
SQLite database. The intent binds original server, actor, request, job and image.
It is never removed after completion. Startup reconstructs missing job/receipt
metadata from those intents before serving requests and rejects conflicting or
foreign obligations. Restoring a database alone cannot revoke an external intent.
This obligation recovery does not expose database restoration as a product feature.

The controller processes bounded pages and retries pending cleanup every two
seconds, with a thirty-second cancellable attempt. It independently verifies the
original regular private file, metadata, complete SHA-256 and opened identity;
symlinks, changed bytes and adjacent SQLite/pending sidecars keep cleanup pending.
The original read handle closes before claiming or unlinking for Windows compatibility.
Deletion uses platform atomic no-replace rename into private `backup-removals/`,
synchronizes both directories, then revalidates the entire claimed image and
sidecar absence at both names. Only the claimed name is unlinked. Reopened original
names, changed claims and late sidecars retain pending recovery rather than
discarding data. Restart resumes a retained claim against its immutable external
intent; absent-image recovery synchronizes both directories. This protects
ordinary concurrent access to the published name, not hostile same-user writes
to the private recovery namespace. Successful
unlink and parent-directory synchronization precede completion publication. After
a crash between unlink and publication, confirmed absence can complete the job,
but the original removal byte count remains explicitly unknown. Completed jobs
continue to enforce their external obligation if the managed image reappears.
Completed scans validate the unchanged external intent and image absence without
repeating directory synchronization. Unfinished/uncertain cleanup and every
actual unlink still require synchronization before completion.
Every failed deletion attempt returns its safe error alongside the retained job,
including unchanged pending outcomes. Maintenance emits `backup_deletion_pending`
with only the job ID and typed code; persisting uncertainty must not hide an
ongoing filesystem failure or inflate revisions on identical retries.
At most 4,096 deletion obligations may be accepted; capacity failure preserves all
existing obligations rather than evicting them. Startup inventory separately
limits the directory to 8,192 entries and the canonical obligation filenames to
4,096. Ignored `.pending-*` atomic-write remnants count only toward the directory
bound; recovery retains them and validates every selected obligation's contents.

`delidev backup delete --id ID --expected-revision REV --size-bytes BYTES
--modified-at TIME --sha256 SHA256 --confirm` uses the original inspection values.
The global `--request-id` permits exact retry after an uncertain response.
`delidev backup deletions` lists retained status after restart.
`delidev backup deletion --id JOB-ID` and `GetBackupDeletion` read one exact
original job independently of history pagination. Settings requires
an explicit inspection and permanent-deletion checkbox bound to that exact
observation. Hiding the view or refreshing/replacing its inspection clears fresh
confirmation; already submitted uncertain requests retain their original bytes.
Settings retains uncertain requests
across category navigation within one opening, and presents pending/completed jobs with separate cleanup
failures. Accepted deletion cannot be canceled. Logical validated image bytes
removed are not a claim of reclaimed filesystem space; hard links, filesystem
snapshots and allocation remain outside that measurement.

## Permanent session deletion

`SessionService.DeleteSession` accepts an owner/paired-client UUID-v7 request,
original session ID and exact nonzero revision. `GetSessionDeletion` observes the
original job independently of the session resource, including after that resource
is removed. Workers cannot invoke these owner/client APIs. The typed
`PERMANENT_SESSION_DELETION_V1` capability advertises this boundary. CLI
`session delete --id ID --revision N --confirm [--wait]` requires explicit
irreversible confirmation; `session deletion --id SESSION-ID` observes it later.
Waiting reads the accepted job without replaying acceptance and preserves its
identity/progress on cancellation or a failed read. Revisions remain decimal-safe.
Archive continues to preserve content and is independent of this operation.

Reapplying an already durable pause preserves the session revision and events.

Go persists a synchronized private `session-deletions/<session>.json` obligation
outside replaceable SQLite **before** pausing dispatch or requesting cancellation.
It binds the original server, actor, request, revision and every immutable claimed
Worker assignment. New requests cannot replace it; exact retries return current
progress, and receipt reconstruction reserves only the original request. Uncertain
intent publication fences ordinary store access until external-journal recovery.
Admission rejects new session copies; original cancellation/cleanup may finish,
but no fresh execution, preparation, response or recovery is admitted. Startup
reapplies intent before listeners, reconstructs reference-only receipts and
repurges an older database when removal had committed. Obligations are never
evicted: 4,096 sessions and 4,096 original jobs per session are explicit bounds.
Each plan validates UUID uniqueness across the full 4,096-job capacity independently
of the general 1,000-link bound; one immutable plan is capped at 1 MiB. Unknown
ownership fails closed.

A separate authenticated Worker polling/report lane survives an interrupted
primary assignment stream. Work binds the original paired device/machine and
immutable assignment instance/revision/hash; reports additionally require a live
current instance and exact plan digest. One retained report UUID survives process
replacement and lost acknowledgements. Exact acknowledged report retries reuse
the matching actor/work-bound receipt without rewriting the external journal or
changing its revision. Missing SQL receipts still recover from the original
synchronized acknowledgement; conflicting receipts are rejected. Cleanup tombstones native admission,
joins original publication owners through final journal/report publication on
both primary and auxiliary lanes, reconciles exact retained process indexes,
and acquires the same workspace lock as preparation/execution/reads. Missing,
foreign, busy or uncertain evidence remains pending. A missing workspace without
original removal proof is not completion, except for positively failed preparations
that never produced a workspace. Worker attempts are bounded to two minutes.

Original manifests bind session, machine and preparation digest. Worktree cleanup
compares actual native common/admin directories and removes only the managed linked
worktree; dirty/untracked owned worktree files are included. Original Local
checkouts, including dirty/ignored files, are never traversed. Shared account
browser profiles are outside the deletion plan. Worker cleanup removes original
job/journal/outbox, execution runtime/history/claim, title runtime, workspace
recovery, PR startup and owned process records. It persists original workspace
proof before any unlink, persists removal stages before deleting journals, and
then retains only non-content digest/report tombstones. Filesystem traversal
never follows links, checks original file identity, observes cancellation before
each unlink and is capped at 100,000 entries per owned tree. A replaced root is
preserved as uncertain. Cleanup retry never starts native work or resends input.
Reusing a completed proof
rechecks the full removal inventory, including process records/recovery locks and
a bounded scan of matching title runtimes; a restored replacement stays pending.

Only after every original Worker acknowledgement does one SQLite transaction
remove the session and its scoped inputs, transcripts, tools, interactions,
snapshots, links/reviews, attachments, jobs, occurrences, inbox, activity and usage
records. Foreign-key cascades remove search/claim/index state; receipts touching
removed resources are redacted and UUID/kind tombstones prevent stale publication.
Shared PR remediation histories lose the deleted session's operands and retire
coordination while preserving contiguous history and lifetime counters, without
inventing a native outcome or refunding attempts. Erasure removes original
attempt-source PR activity, including reservation activity recorded before session
binding. Redaction updates the validated shared attempt and its typed index
without publishing replacement business activity; unrelated PR history remains.
Secure-delete plus a successful
WAL truncation must finish before database-removal acknowledgement. Existing
schema-24 tables suffice; no destructive migration or fresh schema baseline is
introduced.

The joined server controller retries every two seconds, bounds each session pass
to thirty seconds and uses identity-checked private immutable SQLite inspection
to classify **every** managed backup. Images containing session entities or shared
remediation operands use existing durable backup deletion intents. Unrelated
images remain. Published replacements, corrupt/foreign images, unpublished
scratch or unresolved claimed images preserve uncertainty and block completion.
The final backup acknowledgement follows confirmed removals and directory sync.
Under the publication gate, every remaining image must match the native identity,
mode, size and modification time captured by its content inspection. New or
replaced images stay pending until a fresh pass classifies their contents;
completion does not assert physical free-space recovery, and reclaimed bytes
remain explicitly unknown. Repeated scans retain completed obligations and
reapply removal to stale restored managed data. Logs contain operation/UUID,
revision and stable error codes only, never paths, prompts or credentials.

Workspace snapshots now join the original deletion plan through reserved snapshot
UUIDs on immutable claimed storage jobs, including interrupted copies with no output.
The Worker joins job/workspace owners and removes published snapshots, staging, removal
claims/intents, retirement receipts and restoration bindings before acknowledging.
A stored workspace supplies its validated original manifest before deleting its only
copy; restored independent Git is removed solely within the managed root. Original
Local/source checkouts remain protected. Completed-proof retries verify these exact
paths remain absent. Future session-owned native services and dependent Sidechats
must join this ownership graph and acknowledgement boundary before exposing them.
The current forwarding lifetimes participate through their existing original
client and Worker cleanup receipts. Deletion atomically requests Stop for every
forward; offline or uncertain peers keep both forwarding records and database
removal pending. Original cleanup reports remain authorized during deletion,
while new socket claims and traffic cannot reopen the session. Other unimplemented
terminal/browser-profile products are not invented by deletion. Uncontrolled filesystem snapshots and external copies are
outside the guarantee; platform/process fixtures do not establish native
Windows/Linux or real-account acceptance.

## Remaining implementation

Restoration must preserve session and image deletion obligations outside the
replaced database and apply them before serving restored state. Worker-local workspace snapshots use the separate storage service below; database backup observation does not grant workspace or database restoration. Permanent dependent Sidechat deletion, independent attachment/cache cleanup and a desktop storage-management surface remain separate issue #964 work. Parent workspace storage fails closed on unresolved dependent jobs or extra resources; it cannot report their permanent deletion. The complete requirements remain authoritative.

## Validation

Use real private SQLite/WAL backups for image/hash preservation, subsequent live
writes, corrupt/foreign/missing files, sidecars, symlinks and cancellation. Test
authenticated pagination/cursor invalidation, Worker and revoked-client denial,
CLI decimal precision, hidden-screen reads, explicit inspection and exact retries.
Also test concurrent deletion receipts, stale revisions/metadata, failed intent
persistence, changed content, missing unlink acknowledgment, database rollback
without journal rollback, exact CLI confirmation and migration from schema 20.
Run DeliDev Go race tests/vet, protocol checks, API-client tests and desktop
`pnpm test`. Keep real-platform evidence separate from fixtures and builds.

## References

- [Project](project-delidev.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Evidence ledger](cmds-delidev-evidence.md)

Creation recovery also validates the published image's original server identity
against the live scope before accepting an already existing filename. Immutable
SQLite validation rejects adjacent WAL/SHM/journal files, including during legacy
creation and migration-image checks; it never ingests external sidecar state or
opens a backup as a writable live database. Foreign/corrupt images remain intact
and end a durable creation with recovery-required rather than false success.

Settings retains up to 20 accepted jobs per operation type in opening memory
and observes each directly through `GetBackupCreation` or `GetBackupDeletion`.
History page changes do not replace these identities. Closing Settings releases
local tracking and uncertain retries without canceling accepted jobs or replaying
acceptance; a new opening observes durable history through fresh reads. Pending/failed reads poll
only while the view is active; terminal observations stop polling and each newly
observed successful revision refreshes the image inventory. Failed refreshes are
stale/unavailable, not success. Dismissing terminal tracking frees local capacity
without deleting server history, canceling work or repeating acceptance.

Schema 24 follows main's provider activation in 21, hosted defaults in 22 and the separate backup/title layouts of version 23. Upgrading a pre-merge
schema-21 backup database recognizes its existing deletion table, adds the
missing provider preset index and retains every job, receipt and external
obligation. Both schema-21 layouts receive a verified pre-migration backup;
index creation and version advancement commit atomically or leave the original
database intact. Fresh schema-24 stores include both indexes and hosted defaults from initialization.
A pre-merge schema-22 backup database retains its deletion table/jobs and receives
hosted defaults once. Main schema 22 already applied defaults: add only the
deletion table and preserve later explicit provider deletions, identities and Off
settings. Detect the prior layout before changing either table or version.

Both version-23 layouts migrate to 24 under the same pre-migration backup and transaction. The backup layout gains the missing title usage column, index and send/HTTP claim tables; the title layout gains the backup deletion index. Preserve existing usage attribution, original job identities, both once-only claims and explicit provider deletions. Never queue title inference or reseed providers while merging these layouts.

Creation publication commits a versioned metadata/digest claim in the live SQLite
metadata table after synchronizing the private copy and before an atomic
no-replace rename. Recovery matches that independent claim and original server
identity. A valid older same-server image at a reserved path, a replaced published
image, or a legacy image without a claim is preserved and reports recovery-required;
none can complete the pending job. A crash after rename retains the original
claim and exact bytes across restart without recopying the live database.

## Preserved project-index implementation notes

The following source-backed notes were relocated from the project index at `12b33a2accaf`. Their historical qualifications and unresolved acceptance boundaries are retained verbatim.

- `cmds/delidev-cli`: Go CLI, server, execution Worker, native adapters, storage, same-owner Worker bootstrap and generation-bound detached/foreground controller lifecycle; optional native OS services remain pending.

- `protos/delidev/v1`: versioned Connect RPC schemas.

Backup publication and first-start recovery preserve the original state and synchronize durable names. Completed process scopes are retired only after native completion validation and controller release. Reported pre-launch claim-publication failures can roll back only the current attempt before lease issuance, preserving prior closed ownership and all unexpected evidence. Account deletion retains all live configuration and historical session references; keyless lifecycle operations remain independent of native credential availability. Bounded resource pages account for both wire encodings, and CLI waits distinguish observed completion from timeout/cancellation. Schedule availability restarts with each server process, while Stop/Archive retain their product outcome after later native success. Relay reflection checks cover SSE metadata and sanitized native error codes. These repairs do not close the remaining implementation and platform evidence gaps recorded in the evidence ledger.

## Worker-local workspace storage (issue #1079)

`WorkspaceStorageService.RequestWorkspaceStorage` accepts typed preview, create,
cleanup, inspect, restore, delete and explicit recover actions. Owner/paired-client
requests bind a UUID-v7 receipt to the authenticated actor, exact session revision,
original action and selected preview/snapshot/recovery job. Acceptance reserves one
`workspace-storage` job and (for creation/cleanup) one snapshot UUID. The same
request returns the current original job; it never resends native work. Operation
reads and revision-checked cancellation use dedicated RPCs. Workers cannot invoke
this service. Generic owner/client Resource reads list snapshot metadata by session.
`SystemService.GetStatus` advertises `WORKSPACE_STORAGE_V1`; generated Go,
TypeScript and Connect Query clients expose the additive service.

Only inactive prepared DeliDev-owned Worktree and General Chat workspaces are
eligible. Local checkouts, active executions, unresolved native ownership,
unconfirmed cleanup, pending titles, dependent queued/claimed/uncertain jobs and
any forward without both original peer cleanup confirmations block storage.
Stopping a forward or labeling it stopped alone cannot release ownership. The server examines the complete bounded session-job inventory,
not only its first page. Extra resources in the managed root also block parent
cleanup. Acceptance pauses dispatch and clears continuation intent atomically;
first dispatch, Resume, continuation, preparation and initial execution claims
independently require present storage. New forwards and retained live socket
authority also require present storage; original cleanup reports remain usable.
Archive still preserves files. Successful
restoration and recovery remain paused and require explicit later Resume.

The authenticated owning Worker performs five-minute cancellable operations
under the existing session lock after independently reconciling process ownership
and rejecting active execution claims. One private manifest covers the complete
ordered repository set or General Chat directory. Bounds are 8 GiB, 8,192 entries
and an 8 MiB private manifest, with bounded 128 KiB copying. One shared remaining
byte/entry budget applies during workspace and independent Git-store copying.
Capture conservatively reserves the full 8 MiB manifest allowance plus 256 bytes
per repository for copied Git config rewrites before copying payloads; this bounded
headroom can reject data close to 8 GiB even when its final metadata would be smaller.
Git roots count once, overlapping administration directories count only when new,
and excess or growing-file bytes never enter the copied payload. Published snapshot
inventory is capped at 4,096 entries. Removal intents separately allow the two
snapshot wrapper entries (`workspace` and `snapshot.json`) beyond a complete
8,192-entry workspace, without admitting unexpected root content or increasing
the workspace bound. Private manifests and removal intents use their explicit
8 MiB strict JSON decoding budget; public command documents retain 1 MiB limits. Files are copied through opened anchored
parents with exclusive destinations, identity checks, full SHA-256 inventories,
mode preservation and synchronization. Root and nested directory identity,
mode, size and modification time are rechecked around inventory reads and copies;
late root entries, including writes through a retained handle after a cleanup
claim, invalidate the observation before publication or unlink. Ordinary symlinks,
including escaping links, remain links and are never opened. Windows inventories additionally retain the native file/directory symlink type and recreate it explicitly, including forward and dangling directory targets. Sockets, devices, FIFOs and other
unsupported special files block cleanup. Snapshot bytes are sensitive private
Worker data, never server data or diagnostic content.

Each repository receives an independent Git object/ref/index store preserving
base/starting/HEAD and unpushed history, staged/unstaged state and tracked,
untracked and Git-ignored regular files. Linked worktree administration is merged
into that independent store with relative worktree configuration; original Local
checkouts, shared Git registrations and source refs are preserved. Git checks
are read-only/offline with hooks, fsmonitor, maintenance, lazy fetching and ambient
Git/SSH configuration disabled. No remote push is used. External object alternates,
local/worktree config includes, Git administration symlinks and undeclared nested
Git administration (pointer files, directories and filesystem case aliases), partial-clone/promisor configuration and retained `.promisor` pack markers are unsupported and block faithful publication. Full `git fsck` and
inventory comparisons verify every recoverable copy. Whole source data and Git
state are compared again before publication and before any source removal; an
index-only change invalidates the exact cleanup preview.

Windows offline Git commands explicitly enable `core.longpaths` at command scope
because private operation/repository paths can exceed the default 260-character
limit and ambient configuration is excluded. Restored workspace identity and
cleanup checks use this same offline read-only profile. Their failure logs expose
only closed identity phases, session/repository IDs and stable error codes.
This does not modify source Git
configuration. Failed independent Git checks log only the closed commit/object/
location phase, session/repository IDs and stable error code, never paths, native
output or workspace content.
Copied Git configuration is synchronized through write-capable handles, as
required by Windows flushing; only independent copies are opened for that write
access, and original configuration remains unchanged.

Cleanup requires an exact successful original preview. Every repository is
published and re-read before a single atomic no-replace rename claims the entire
source root for deletion. An independently synchronized immutable removal intent
binds the complete source inventory already pinned in the verified published
snapshot outside that root. A fresh mutable inventory never grants deletion
authority. Claimed contents are compared against that pinned inventory; a mismatch
restores the whole source name without replacement when possible, otherwise
retains both the claim and recovery uncertainty. Cancellation is rechecked after intent persistence immediately before the
namespace claim. After verification, a separate synchronized metadata-only claim
binds the original intent digest before any unlink. An intent persisted before
the namespace transition cannot prove that removal ever began. Verification precedes bounded anchored deletion and directory synchronization. A second
repository copy failure or cancellation cannot remove either original repository.
Failures after a namespace transition retain recovery uncertainty and private
copies. Cancellation/failure after verified snapshot publication but before source
removal also retains uncertainty: explicit recovery registers that same retained
snapshot while settling the incomplete cleanup and preserving present sources. Terminal metadata and session state/events commit together after the
owning Worker report; malformed reports retain uncertainty instead of authorizing
Resume. Preview and snapshot SHA-256 reports require exactly 32 bytes encoded as
canonical lowercase hexadecimal before metadata or workspace state publication;
length alone cannot establish a usable later inspect/restore request.
Failed recovery outcomes also bind the original action: availability
stays at its previous state, removed bytes remain zero, and required retained
snapshot identity, digest and non-deleted metadata must validate before settlement. Snapshots use the existing generic metadata schema; no database migration,
history reset or new pre-migration backup is needed.

Restore is available only from the exact current cleanup snapshot of a stored
session. Its complete private manifest/hash/Git stores are revalidated, the entire
workspace is copied and verified in owned scratch, then one atomic no-replace
rename publishes it at the original canonical owned destination. Existing files
are never replaced and no per-repository partial restore is reported. Failed
unpublished restoration removes only its operation-owned staging with an
independent bounded cleanup context; unconfirmed scratch cleanup retains
recovery-required ownership instead of settling a terminal failure. A private
restore binding preserves the original logical workspace identity while allowing
self-contained Git stores at that same path. Its pending form grants no ownership;
only the original operation/snapshot/hash-bound proof synchronized after successful
no-replace publication permits restore recovery, restored execution identity or
coordinated deletion. A matching foreign destination and absent scratch cannot
prove publication. Lost proof preserves uncertainty without repeating the rename.
This comparison does not manufacture
native harness checkpoint or continuation support. Snapshot deletion requires an
explicit owner/client request and cannot remove a stored workspace's only
recoverable copy; restore it first. Deletion claims and verifies the snapshot via
the same immutable removal-intent boundary and retains historical metadata with
`deleted=true`. Removal inventories remain private through uncertain reporting.
Only a matching terminal server acknowledgment followed by the synchronized
Worker reported journal permits retirement of that original intent and its
matching verified claim. Retirement synchronizes claim removal before intent
removal so interrupted acknowledgment cleanup can retry safely. Direct
failed/canceled operations also retire their intent once acknowledged; uncertain
reports and failed recovery attempts retain the predecessor intent. Successful
explicit recovery can retire an interrupted cleanup before snapshot publication
without requiring nonexistent snapshot metadata. The acknowledgment-bound retirement receipt is synchronized before the original
journal transitions to reported and binds the exact result digest. Restart may
finish that transition from matching persisted proof without native replay. A bounded
metadata-only retirement receipt survives interruptions and retries intent
retirement at Worker startup; it never grants further native removal.

Usage results separate exact logical source bytes, all retained published snapshot
bytes, confirmed logical removed source bytes and optional measured filesystem
capacity/free bytes before/after. All byte counts use canonical decimal strings.
The independent Git stores can cost more than the removed linked worktree. These
logical counts never imply positive physical reclamation: compression, shared
blocks and concurrent allocations prevent attribution from byte subtraction.
Unsupported capacity observation stays absent. Failed/unfinished jobs do not
report confirmed removal. Native disk-full/quota failures before publication have
a redacted `resource_exhausted` classification; uncertain transitions retain their
separate recovery-required ownership. Owned scratch/retained removal data may still occupy
space during recovery and is reflected in filesystem free observations, not
misrepresented as a published snapshot. A failed capture whose independent scratch removal is unconfirmed remains recovery-required; explicit recovery removes the original complete staging tree before settling the failure.

Reconnect never repeats a started native operation. Explicit `recover` binds the
original immutable assignment(s), Worker instance/revision/digest and durable
Worker journals. It inspects existing publication/removal/restoration namespaces;
it can finish only already claimed removals against their original inventories.
It never recreates a snapshot, repeats a source rename or republishes restoration.
Changed/foreign contents preserve uncertainty and bytes. Even when both original
and claimed names are absent, cleanup/deletion recovery requires the matching
synchronized removal intent and its separately retained verified namespace claim;
filesystem absence or a pre-transition intent alone never proves removal or
confirmed removed-byte accounting. Recovery itself may be
reconciled through a bounded eight-claim lineage after another interruption.
Original jobs and snapshots remain retained; successful reconciliation atomically
settles their observed outcome and paused workspace state.

CLI equivalents are `delidev storage preview|create|cleanup|inspect|restore|delete|recover
--session-id ID --expected-revision REV`. Cleanup additionally requires
`--preview-job-id JOB --confirm`; inspect/restore/delete use `--snapshot-id ID`,
and delete requires `--confirm`. Recovery uses `--recovery-job-id JOB`.
`storage operation --id JOB` reads current status and `storage cancel --id JOB
--expected-revision REV` cancels queued work or targets an already claimed job.
The global `--request-id` preserves exact acceptance/cancellation retries. These
commands never implicitly start a server, wait by replaying a mutation or infer
success from acceptance. `snapshot list --session-id ID` uses existing
Resource pagination for retained snapshot metadata.

Schema 25 retains the complete schema-24 backup/provider/title layout and adds
only the future native-accounting table/indexes defined by the [usage
contract](cmds-delidev-usage-contract.md). Initialization and migration create the
same layout; migration publishes the existing validated synchronized backup
before one transaction and leaves historical native observations untouched.
Derived native-accounting rows cascade with permanent session removal and are
retained by Archive and consistent database backups. A conflicting preexisting
accounting layout fails without adopting or rewriting it.

The reserved migration is implemented in `migration_025.go`. It writes the
independent `native_accounting_layout=grok-closed-input-v1` metadata marker.
Opening any version-25 database without that marker fails before WAL settings,
migration or Worker state updates, preserving old unmerged version-25 files for
explicit recovery. A version number alone cannot identify their layout.
