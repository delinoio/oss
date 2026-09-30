# DeliDev storage operations

## Scope

Go owns managed database backups, session deletion, Worker snapshots and recovery.
The approved completion work includes every remaining issue #964 requirement;
actual account/private-GitHub access and platform distribution validation remain
deferred. This document distinguishes the implemented backup observation surface
from permanent session deletion and Worker-local snapshots, which remain pending. Managed backup deletion and database restore are implemented separately below.

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

## Managed database restore

Issue #1080 adds owner/paired-client `SystemService.RestoreBackup` and
`GetBackupRestore`, with the `MANAGED_BACKUP_RESTORE_V1` status capability (wire
value 7, preserving published session-forwarding value 2 and user-services value 3).
`InspectBackup` also returns the exact committed live `restore_revision`. Inspection
is an observation; eligibility is independently rechecked at replacement.

`delidev backup restore --id ID --expected-revision 1 --size-bytes BYTES
--modified-at TIME --sha256 SHA256 --expected-restore-revision REV --confirm`
uses the original inspected image metadata, digest and live revision. Preserve the
global UUID-v7 `--request-id` on uncertain responses. `backup restore-status
--id REQUEST-ID` observes that exact original receipt after restarting the server.
No command implicitly starts a server. Omitted live revision differs from explicit
zero. Workers and revoked clients cannot inspect, restore or read restore receipts.

The thirty-second cancellable operation holds both the managed-file gate and
exclusive store gate under the server's process lock. It checks original actor,
server identity and exact event revision, then refuses live claimed/uncertain jobs,
active/running/recovery/archiving sessions, uncertain/stopping workspace ownership,
pending credential removals/integration operations, and any forward that is not
stopped with both original peer cleanup flags confirmed. Restore never stops a
Worker to create eligibility. Concurrent mutations/claims cannot cross the final
validation boundary; a second restore cannot publish in the old epoch.

Copy the image through the inspection identity/hash/sidecar checks into a private
staging file. The 8 GiB image bound still applies. Reject corrupt, newer-schema,
foreign-server, replaced or deletion-obligated images without changing live logical
state or the source. Supported older schemas migrate only in staging through the
existing backup-first migration, retaining the pre-migration copy. Capture a
synchronized current `VACUUM INTO` safety image, including committed WAL content,
outside the replaceable database. Transform only the candidate in one transaction.

The safety image supplies current device descriptors/verifiers, merged deletion
tombstones, deleted-project policies, model suppressions, backup publication claims
and permanent backup-removal jobs/receipts. Remove tombstoned entities and deleted
session children, including indexed transcript content through existing cascades.
Current paired clients retain their present authorization; old/revoked/deleted
clients gain none. Pairing codes and execution grants/references are discarded.
Workers must pair again; no Worker files or credential payloads are restored.
Restored account and integration definitions are disconnected, without historical
connection/validation/removal authority. Protected credential storage stays untouched.

Every restored session is paused and recovery-required. Nonterminal historical
jobs are canceled with a typed quarantine problem; schedules are disabled and their
next-run timestamps cleared. Historical assignment copies cannot grant native
recovery/continuation authority. Historical forwards transition through their
ordinary Stop model without reopening sockets or fabricating claimed-peer cleanup;
unknown original cleanup remains stopping. Keep original evidence in the source and safety
images rather than manufacture cleanup or replay input. Permanent backup-removal
jobs alone retain their current external obligation and controller semantics.
Restore request UUIDs remain globally reserved across rollback and unjournaled
crash evidence, without filesystem reads inside ordinary mutations. Historical
receipt digests remain reserved, while ordinary receipt contents are
replaced by a quarantine marker that `Replay`/`Mutate` reject as recovery-required.
Replacement resource revisions exceed both versions; the event high-water mark
advances beyond both timelines and expires older cursors for a coherent resnapshot.

`backup-restores/` is synchronized in its parent before staging or closing the
live database, so a newly created journal root is durable before replacement.
Failure preserves live state and the source without accepting an attempt.
It retains at most 64 attempt directories, without eviction. Each
contains the synchronized safety image, candidate/staging evidence and a versioned
actor/server/request/image-bound receipt. `active.json` is the external publication
barrier, binding original live, safety and candidate SHA-256 values. After a confirmed
WAL checkpoint and SQLite closure, synchronize that barrier before atomic
same-volume platform replacement and synchronization of both directories. No
restore code unlinks live WAL/SHM/journal files. The source backup remains unchanged.

Before atomic replacement, the server synchronizes stopped lifecycle intent under
the same lifecycle gate. A failed stop-intent barrier preserves the original live
image behind the prepared journal; startup records rollback. A crash after rename
cannot leave running intent that permits automatic `server ensure`. Exact receipt
replay does not suppress a later explicitly restarted epoch. Publication ends the
old server epoch and returns `published`, which is distinct from verified startup. Any uncertain outcome
after SQLite closure also ends the epoch. Startup, under the exclusive server lock
and before opening SQLite, accepts exactly the journal-pinned original or candidate
fingerprint: preserve the original and record `rolled-back`, or record `restored`
for the replacement. Changed safety/live bytes, foreign identity, sidecars or
conflicting journal evidence fail closed without overwriting either outcome.
Recovery synchronizes the observed outcome and its receipt before retiring the
active barrier. Each completed external receipt must still match its immutable
SQLite request marker before startup migration or authorization. Manual replacement
with an older image cannot silently bypass that safety boundary. A lost acknowledgment or exact retry reads the retained receipt,
never publishes another image. Unjournaled interrupted staging remains evidence;
it is not an accepted restore and cannot be blindly resumed.

Restore logs contain only validated request/backup UUIDs, closed state, correlation
and safe error codes. Hashes, paths, database bodies and credentials are not logged.
This operation proves database replacement/recovery only; it does not establish
real-account, Worker workspace, native harness or platform-distribution acceptance.

## Remaining implementation

Permanent session deletion requires irrevocable intent, stopped ownership,
offline-Worker progress and managed-backup removal. Database restore preserves
current tombstones and external backup-deletion obligations; it does not implement
permanent session deletion. Worker-local snapshots must
faithfully preserve all repositories, ignored files, unpushed commits and symlinks
before deleting any managed source. Those operations are not yet exposed by the
backup APIs; the complete requirements remain authoritative.

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

### Restore and optional user-service control

Authenticated user-service native intent and effects share the original server
lifecycle gate with managed restore. An in-flight install/start/stop/remove
therefore prevents restore publication; restore holding the gate prevents a
new service control from crossing its old-epoch boundary. The unchanged private
service records remain outside the restored database, and the original service
wrapper respects the resulting durable product Stop after joined exit. No
login registration or service controller may infer explicit Start or session
Resume from replacement. See the [user-service contract](cmds-delidev-user-services-contract.md).
