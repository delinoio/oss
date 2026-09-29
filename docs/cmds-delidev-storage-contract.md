# DeliDev storage operations

## Scope

Go owns managed database backups, session deletion, Worker snapshots and recovery.
The approved completion work includes every remaining issue #964 requirement;
actual account/private-GitHub access and platform distribution validation remain
deferred. This document distinguishes the implemented backup observation surface
from permanent session deletion and database restoration, which remain pending. Managed backup deletion and Worker-local workspace storage are implemented separately below.

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
across navigation, and presents pending/completed jobs with separate cleanup
failures. Accepted deletion cannot be canceled. Logical validated image bytes
removed are not a claim of reclaimed filesystem space; hard links, filesystem
snapshots and allocation remain outside that measurement.

## Remaining implementation

Permanent session deletion requires irrevocable intent, stopped ownership,
offline-Worker progress and managed-backup removal. Restoration must preserve
deletion obligations outside the replaced database. Worker-local workspace snapshots use the separate storage service below; database backup observation does not grant workspace or database restoration. Permanent dependent Sidechat deletion, independent attachment/cache cleanup and a desktop storage-management surface remain separate issue #964 work. Parent workspace storage fails closed on unresolved dependent jobs or extra resources; it cannot report their permanent deletion. The complete requirements remain authoritative.

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

Settings retains up to 20 accepted jobs per operation type in connection memory
and observes each directly through `GetBackupCreation` or `GetBackupDeletion`.
History page changes do not replace these identities. Pending/failed reads poll
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
unconfirmed cleanup, pending titles and dependent queued/claimed/uncertain jobs
block storage. The server examines the complete bounded session-job inventory,
not only its first page. Extra resources in the managed root also block parent
cleanup. Acceptance pauses dispatch and clears continuation intent atomically;
first dispatch, Resume, continuation, preparation and initial execution claims
independently require present storage. Archive still preserves files. Successful
restoration and recovery remain paused and require explicit later Resume.

The authenticated owning Worker performs five-minute cancellable operations
under the existing session lock after independently reconciling process ownership
and rejecting active execution claims. One private manifest covers the complete
ordered repository set or General Chat directory. Bounds are 8 GiB, 8,192 entries
and an 8 MiB private manifest, with bounded 128 KiB copying. Published snapshot
inventory is capped at 4,096 entries. Files are copied through opened anchored
parents with exclusive destinations, identity checks, full SHA-256 inventories,
mode preservation and synchronization. Ordinary symlinks, including escaping
links, remain links and are never opened. Sockets, devices, FIFOs and other
unsupported special files block cleanup. Snapshot bytes are sensitive private
Worker data, never server data or diagnostic content.

Each repository receives an independent Git object/ref/index store preserving
base/starting/HEAD and unpushed history, staged/unstaged state and tracked,
untracked and Git-ignored regular files. Linked worktree administration is merged
into that independent store with relative worktree configuration; original Local
checkouts, shared Git registrations and source refs are preserved. Git checks
are read-only/offline with hooks, fsmonitor, maintenance, lazy fetching and ambient
Git/SSH configuration disabled. No remote push is used. External object alternates,
local/worktree config includes, Git administration symlinks and nested external
Git pointers are unsupported and block faithful publication. Full `git fsck` and
inventory comparisons verify every recoverable copy. Whole source data and Git
state are compared again before publication and before any source removal; an
index-only change invalidates the exact cleanup preview.

Cleanup requires an exact successful original preview. Every repository is
published and re-read before a single atomic no-replace rename claims the entire
source root for deletion. An independently synchronized immutable removal intent
binds its complete inventory outside that root. Claimed contents are compared
again before bounded anchored deletion and directory synchronization. A second
repository copy failure or cancellation cannot remove either original repository.
Failures after a namespace transition retain recovery uncertainty and private
copies. Cancellation/failure after verified snapshot publication but before source
removal also retains uncertainty: explicit recovery registers that same retained
snapshot while settling the incomplete cleanup and preserving present sources. Terminal metadata and session state/events commit together after the
owning Worker report; malformed reports retain uncertainty instead of authorizing
Resume. Snapshots use the existing generic metadata schema; no database migration,
history reset or new pre-migration backup is needed.

Restore is available only from the exact current cleanup snapshot of a stored
session. Its complete private manifest/hash/Git stores are revalidated, the entire
workspace is copied and verified in owned scratch, then one atomic no-replace
rename publishes it at the original canonical owned destination. Existing files
are never replaced and no per-repository partial restore is reported. A private
restore binding preserves the original logical workspace identity while allowing
self-contained Git stores at that same path. This comparison does not manufacture
native harness checkpoint or continuation support. Snapshot deletion requires an
explicit owner/client request and cannot remove a stored workspace's only
recoverable copy; restore it first. Deletion claims and verifies the snapshot via
the same immutable removal-intent boundary and retains historical metadata with
`deleted=true`.

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
misrepresented as a published snapshot.

Reconnect never repeats a started native operation. Explicit `recover` binds the
original immutable assignment(s), Worker instance/revision/digest and durable
Worker journals. It inspects existing publication/removal/restoration namespaces;
it can finish only already claimed removals against their original inventories.
It never recreates a snapshot, repeats a source rename or republishes restoration.
Changed/foreign contents preserve uncertainty and bytes. Recovery itself may be
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
