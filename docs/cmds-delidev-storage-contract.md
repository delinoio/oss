# DeliDev storage operations

## Scope

Go owns managed database backups, session deletion, Worker snapshots and recovery.
The approved completion work includes every remaining issue #964 requirement;
actual account/private-GitHub access and platform distribution validation remain
deferred. This document covers managed backup observation, creation/deletion and permanent
session deletion and database restore. Worker-local snapshots remain pending.

## Planned subscription retirement (issue #1235)

Migration 28 is reserved for [issue #1235](https://github.com/delinoio/oss/issues/1235)
after the real Claude accounting and request-diagnostics migrations at 26 and
27. Establish this reservation and independent server capability 17 on main
before dependent implementation. The executable sequence remains at 25; no
empty predecessor, schema-version bump or retirement runs in this prerequisite.

The required reset must back up first and atomically retire legacy subscription
Accounts, native-subscription Providers and their provider-bound Models into
read-only historical metadata with original IDs, revisions, timestamps and
document bytes. Tombstone their IDs, exclude them from live configuration/export
and refuse outstanding or uncertain native ownership, connection/removal or
cleanup before changes. Never infer services or recreate accounts. Preserve
surviving Agent account order/weights and Project restriction `configured` flags,
including configured-empty deny-all; retired model references require explicit
reconfiguration. Disable only affected schedules with a retained reset reason,
preserving accepted occurrences and unrelated API configuration.

Keep session, snapshot, transcript and usage bytes/attribution intact. Retired
subscription sessions require a new explicitly configured session, without
Resume, dispatch or automatic account fallback. Migrate older backup candidates
before restore publication; imports/receipts cannot resurrect retired IDs.
Portable bundle version 2 carries service-native configuration, while API-only
version-1 imports remain supported and legacy subscription graphs are rejected
atomically with recreate guidance. Implementation must compose the independent
managed-account ownership and cleanup boundary from issue #1095.

## Planned account OAuth attempts (issue #1146)

Migration 29 is reserved for the private `account_oauth_attempts` table under
[issue #1146](https://github.com/delinoio/oss/issues/1146), following real
migrations 26–28. Establish the reservation on main before dependent
implementation; the executable sequence still ends at 25. Do not create empty
predecessors, reuse an allocated version or activate this table early.

The [planned OAuth contract](cmds-delidev-account-oauth-contract.md) owns the
complete lifecycle. Attempt metadata binds actor/server/provider and revision,
original start/completion IDs, reserved account/create/connect IDs, process
generation, state/revision/times, keyed comparison commitments, exact protected
references and cleanup status. Do not require an existing account foreign key,
cascade deletion of recovery evidence, or include attempts in public resources,
snapshots/events or portable export. SQLite, receipts and backup images contain
no raw verifier, code, key or callback/authorization URL.

Fresh/upgrade paths must share the verified backup-first transaction and preserve
existing account/provider/settings/claim state and explicit default deletions.
A durable claim commits before HTTP outside locks; no restart, copied image,
timeout, cancellation or retry can reacquire exchange authority. Original local
recovery may finish only an exactly sealed credential or accepted result without
HTTP. Keep account-less staged references, partial disconnected accounts and
unresolved cleanup; unknown provider response never proves failure or revocation.
This reservation implements no table, migration, exchange or restore feature.

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

Manual-compaction copies additionally bind the immutable original action UUID,
separate from the conversation execution UUID, and remove its native replacement
runtime and retained `compaction-checkpoints` file through this same ownership and
completed-proof inventory. Deletion closes action authority and waits for its
original job/process/workspace owners; unrelated session checkpoints are preserved.
See the [compaction contract](cmds-delidev-claude-compaction-contract.md).

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

Future session-owned native services, dependent Sidechats and workspace snapshots
must join this ownership graph and acknowledgement boundary before exposing them.
The current forwarding lifetimes participate through their existing original
client and Worker cleanup receipts. Deletion atomically requests Stop for every
forward; offline or uncertain peers keep both forwarding records and database
removal pending. Original cleanup reports remain authorized during deletion,
while new socket claims and traffic cannot reopen the session. Session terminals participate through their original close claims and independently
joined cleanup reports under the [terminal contract](cmds-delidev-terminals-contract.md).
Deletion atomically requests close without replacing an existing close identity,
withholds workspace-removal work until every terminal confirms cleanup, and
rechecks cleanup before database purge. New terminal creation/input/resize cannot
reopen a deleting session. Original cleanup reports and exact receipt retries
remain admissible. An accepted uncertain terminal close atomically retains the
cleanup obligation under a fresh close identity; replaying its original report
cannot change that identity or release workspace deletion. Browser-profile/snapshot products remain separate. Uncontrolled filesystem snapshots and external copies are
outside the guarantee; platform/process fixtures do not establish native
Windows/Linux or real-account acceptance.

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
Receipt reads require the exact original owner or paired-client principal recorded
in the external journal, including after rollback and restart. Another currently
authorized actor cannot read the receipt by knowing its request UUID.

The thirty-second cancellable operation holds both the managed-file gate and
exclusive store gate under the server's process lock. It checks original actor,
server identity and exact event revision, then refuses live claimed/uncertain jobs,
active/running/recovery/archiving sessions, uncertain/stopping workspace ownership,
pending credential removals/integration operations, any private network credential publication/deletion intent, and any forward that is not
stopped with both original peer cleanup flags confirmed. Any unfinished external
session deletion also blocks replacement, including the intent-before-SQL window. Restore never stops a
Worker to create eligibility. Concurrent mutations/claims cannot cross the final
validation boundary; a second restore cannot publish in the old epoch.

Copy the image through the inspection identity/hash/sidecar checks into a private
staging file. The 8 GiB image bound still applies. Reject corrupt, newer-schema,
foreign-server, replaced or deletion-obligated images without changing live logical
state or the source. Supported older schemas migrate only in staging through the
existing backup-first migration, retaining the pre-migration copy until recovery settles. Capture a
synchronized current `VACUUM INTO` safety image, including committed WAL content,
outside the replaceable database. Transform only the candidate in one transaction.

The safety image supplies current device descriptors/verifiers, merged deletion
tombstones, deleted-project policies, model suppressions, backup publication claims
and permanent backup-removal jobs/receipts. Remove tombstoned entities and deleted
session children, including indexed transcript content through existing cascades.
Device replacement copies each complete current document, including protected
browser inventory: original profile identities/revisions, pending deletion
request IDs and completed cleanup states survive an older source image. Offline
and revoked clients retain their obligations. The historical Device documents
are discarded rather than merged back into that current inventory.
Use the permanent deletion redactor for shared remediation operands and their
source-linked activity before removing the original session graph.
Schema-25 native accounting retains its original verified unit, attribution and
first-retention timestamp from the selected image without backfill. Current
session tombstones remove its derived rows through the same foreign-key cascade;
retained accounting cannot grant execution or restore native ownership.
Current paired clients retain their present authorization; old/revoked/deleted
clients gain none. Pairing codes and execution grants/references are discarded.
Workers must pair again; no Worker files or credential payloads are restored.
Restored account and integration definitions are disconnected, without historical
connection/validation/removal authority. Protected credential storage stays untouched. Pending, leased or recovery-required managed subscription ownership blocks replacement. Restored subscription metadata retains its historical generation and ownership evidence under a recovery-required fence, with no account connection. The external vault is not restored, so neither an older reference nor database publication authorizes another credential grant. Clear an allocated subscription state with no generation, identity, pending operation, lease or recovery fence; settled logout or failed login owns no external reference to quarantine.

Current network profiles, immutable credential-generation references and server/Worker route selections are copied from the current safety image with their exact bodies preserved and resource revisions freshened by the ordinary restore rule; historical network routing cannot replace current explicit authority. The server holds the shared credential gate through restore eligibility and publication, preventing a native network/account write from crossing the private-intent-before-SQL boundary.

Current machine descriptors required by retained Worker network routes are also
copied from the safety image, replacing historical metadata for matching IDs and
freshening their resource revisions. Owner/client route reads, explicit clearing
and subsequent profile deletion remain possible even when the backup predates
the Worker. These descriptors do not retain Worker credential verifiers,
instances or execution grants; Workers still require fresh pairing.

Every restored session is paused and recovery-required. Nonterminal historical
jobs are canceled with a typed quarantine problem; schedules are disabled and their
next-run timestamps cleared. Historical assignment copies cannot grant native
recovery/continuation authority. Historical forwards transition through their
ordinary Stop model without reopening sockets or fabricating claimed-peer cleanup;
unknown original cleanup remains stopping. Keep original evidence in the unchanged
source and temporary safety images through recovery, without manufacturing cleanup or replaying input. Permanent backup-removal
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
temporarily contains the synchronized safety image, candidate/staging evidence and a versioned
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

After the active barrier is durably retired and the completed SQLite marker is
validated, startup removes the receipt-owned safety, candidate and staging-migration
images under exclusive process ownership before serving. The prepared external
journal pins each staging-migration copy's original UUID, metadata and SHA-256,
with a bounded unique inventory captured before publication. Exact fingerprints,
private paths and original server identity are checked first; changed or unexpected
images remain recovery-required. Legacy journals without this inventory cannot
adopt retained migration copies from their current bytes; empty or already-removed
copy directories still permit synchronized cleanup. A retry finishes directory synchronization after
an interrupted unlink. Metadata-only journals remain reserved for exact retries.
Unaccepted staging is preserved; any remaining restore database image blocks the
final permanent-session backup acknowledgement. The selected source backup is
never removed by restore. This closes the restore namespace over later permanent
erasure without inventing cleanup of uncertain original images.

Restore logs contain only validated request/backup UUIDs, closed state, correlation
and safe error codes. Hashes, paths, database bodies and credentials are not logged.
This operation proves database replacement/recovery only; it does not establish
real-account, Worker workspace, native harness or platform-distribution acceptance.

## Remaining implementation

Worker-local snapshots must
faithfully preserve all repositories, ignored files, unpushed commits and symlinks
before deleting any managed source. Those operations are not yet exposed by the
backup observation APIs; the complete requirements remain authoritative.

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

Backup publication and first-start recovery preserve the original state and synchronize durable names. Completed process scopes are retired only after native completion validation and controller release. Reported pre-launch claim-publication failures can roll back only the current attempt before lease issuance, preserving prior closed ownership and all unexpected evidence. Account deletion retains all live configuration and historical session references; keyless lifecycle operations remain independent of native credential availability. Bounded resource pages account for both wire encodings, and CLI waits distinguish observed completion from timeout/cancellation. Schedule availability restarts with each server process, while Stop/Archive retain their product outcome after later native success. Relay reflection checks cover SSE metadata and sanitized native error codes. These repairs do not close the remaining implementation and platform evidence gaps. Record those gaps in pull requests, issues and CI logs/artifacts.

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

## Terminal report acknowledgement after purge

Entity deletion and permanent session purge retain only the closed terminal-report
receipt kind plus original terminal, machine and paired-device UUIDs, rebuilding
that allowlist from the accepted receipt. All resource content and other receipt
fields remain redacted. This lets a same-device replacement confirm an exact
already-committed report after response loss and retire independently joined
local ownership evidence. It grants no new report, native operation or resource
resurrection. Legacy unbound receipts require the original resource to remain
available. See the [terminal contract](cmds-delidev-terminals-contract.md).

### Automatic PR source ownership

Automatic PR attempts add optional original discovery-link identity/revision
metadata to the existing attempt document; revisions use canonical decimal
strings. This requires no new migration, protocol number or fabricated historical
source proof. The final start transaction verifies that original link's current
session/project and pause/Archive/recovery state before charging the durable
stable-PR chain. Explicit session controls retain their separate server-owned
automation suppression bit, without changing historical outcomes or unpausing a
failed queue. Missing or replaced source ownership cannot authorize a native claim.
Follow the [automatic coordinator contract](cmds-delidev-integrations-contract.md#bounded-automatic-pr-remediation-issue-1082).
