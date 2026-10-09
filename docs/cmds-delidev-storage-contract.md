# DeliDev storage operations

## API format generation storage

Capability 9 keeps bounded server-owned generations in account JSON, with no
SQLite migration. Exact receipts and current preferences are committed atomically.
Backup restoration preserves a live OAuth account only while its current connection
points to the original OAuth credential reference; a retained historical connection
alone cannot restore live ownership. Historical disconnected accounts clear all
connection generations and observations. Portable configuration excludes every
generation and key reference. See the
[account contract](cmds-delidev-accounts-contract.md#connected-api-format-changes).


## Startup metadata without migration

[Direct startup](cmds-delidev-execution-startup-contract.md) stores the current bounded startup record in existing session JSON and mirrors it into the original terminal job JSON. Readiness and first failure remain separate immutable observations. Report receipts use existing durable mutations without changing the claimed assignment revision or its input bytes. Explicit retry retains the failed job/input and creates distinct IDs. Original private executable identity, process and outbox journals remain Worker-owned. No table or SQLite migration is added.

## Request diagnostic retention

Schema 27 adds bounded metadata-only request diagnostic rows and session/execution indexes under the existing synchronized pre-migration backup boundary. It preserves all historical state, title claims, backup obligations and usage; no historical requests are synthesized. Rows retain immutable event-time attribution, exact revision checks and original publication receipts. Proxy revisions publish session invalidation events atomically at the unchanged session-state revision, while native observations share their original session publication. The 4 KiB row and 10,000-per-session admission limits do not evict history; existing observations may settle. Session deletion cascades rows, and reference-only old receipts cannot recreate them. Single-record and page reads reject row/body identity, session, execution or revision mismatches without exposing partial records. See the [diagnostics contract](cmds-delidev-diagnostics-contract.md) for publication/cancellation/read ownership.


## Scope

Go owns managed database backups, session deletion, Worker snapshots and recovery.
The approved completion work includes every remaining issue #964 requirement;
actual account/private-GitHub access and platform distribution validation remain
deferred. This document covers managed backup observation, creation/deletion, permanent
session deletion, database restore and Worker-local workspace snapshots and restoration.

## Subscription retirement (issue #1235)

Migration 28 implements [issue #1235](https://github.com/delinoio/oss/issues/1235)
after the real Claude accounting and request-diagnostics migrations at 26 and
27. Reservations reached main before implementation. Complete accounting and diagnostics precede real migration 28 and independent server capability 17; no empty predecessor is permitted.

The reset backs up first and atomically retires legacy subscription
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

## Account OAuth attempts (issue #1146)

Real migration 29 follows accounting 26, request diagnostics 27 and subscription
identity 28, whose reservations reached main before these dependent feature implementations. It adds the private
`account_oauth_attempts` table and `account_oauth_layout=pkce-once-v1` marker;
foreign layouts fail the backup-first transactional upgrade. Attempt body/index
identity and exact revisions agree, with an 8 KiB metadata ceiling and no account
foreign key or cascading cleanup deletion. Codes, verifiers, keys and browser
URLs never enter the table, receipts, events, resources or portable bundles.

Current unresolved attempts/cleanup block managed restore. A candidate copies
only the current private attempt table; old images and receipts cannot acquire
new exchange authority. A fresh server lifetime interrupts awaiting attempts
and fences claimed exchanges for explicit original local recovery. The
[OAuth contract](cmds-delidev-account-oauth-contract.md) owns the original dispatch,
protected reference and cancellation/publication gates.


## API OAuth token generations

Real migration 31 follows real 26–30 and the main-established reservation. It
adds private `account_oauth_credentials` metadata and the exact
`account_oauth_credentials_layout=token-generations-v1` marker. Rows bind current
account/connection/provider identity, accepted public profile, token references,
expiry, refresh claim and cleanup. Access/refresh tokens remain only in Vault.
No foreign key or deletion cascade may remove uncertain ownership. Retire metadata
only after ordinary account cleanup confirms all protected references removed.
Restore copies current metadata with the current attempt table; historical images
cannot replace token generations or acquire a refresh claim. Unsettled refresh or
cleanup blocks restore. Follow the account OAuth contract for HTTP and publication.

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
image publication and job settlement. Creation settlement checks the original
backup deletion index in the same SQLite transaction as its terminal job update.
An already accepted pending or completed deletion produces the existing typed
`RecoveryRequired` failure with the original job and image identities. Deletion
after committed success preserves that historical success. Revoked authority, corrupt ownership or a
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
Settings retains uncertain requests within the active Backups category and
presents pending/completed jobs with separate cleanup failures. Category
departure discards local retries. Accepted deletion cannot be canceled. Logical validated image bytes
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
of the general 1,000-link bound; one immutable plan is capped at 4 MiB. Worker pages retain at most 20 envelopes
and 4 MiB total ownership JSON; the native Connect response allowance includes
its bounded JSON/base64 overhead. Ordinary command JSON retains its 1 MiB bound. Unknown
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

Workspace snapshots now join the original deletion plan through reserved snapshot
UUIDs on immutable claimed storage jobs, including interrupted copies with no output.
The Worker joins job/workspace owners and removes published snapshots, staging, removal
claims/intents, retirement receipts and restoration bindings before acknowledging.
A stored workspace supplies its validated original manifest before deleting its only
copy; restored independent Git is removed solely within the managed root. Original
Local/source checkouts remain protected. Completed-proof retries verify these exact
paths remain absent. Dependent Sidechats join this ownership graph through their
original child plans and unpublished Fork metadata claims before parent removal.
Future session-owned native services must join the same acknowledgement boundary
before exposure.

Permanent deletion reconciles each present original `workspace-removals/<job>`
namespace under the session, observation and snapshot namespace gates before
ordinary copy cleanup. The immutable job/session/snapshot references must match
its original synchronized intent and version-2 claim. The claim binds the exact
intent digest and native root identity; its immutable inventory and existing
partial-removal journal authorize only the original pinned entries. This step
runs no native execution or input replay and cannot create a missing claim.
Missing, malformed, legacy or mismatched proof, replaced roots and changed/new
entries remain protected with `recovery_required`; completion remains pending.
Retain the intent, claim and journal through validated removal and the normal
acknowledgement boundary. The generic Worker callback checks removal namespace
absence only, as for staging, and never traverses a later reappearance.

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

Worker-local workspace snapshots and the desktop storage-management surface use the separate storage service below; database backup observation does not grant workspace or database restoration. Managed database restore uses its explicit lifecycle above. Permanent dependent Sidechat deletion follows its original native read-only ownership contract and composes through this deletion boundary. Independent attachment/cache cleanup retains its existing source-bound owners. Parent workspace storage fails closed on unresolved dependent jobs or extra resources; it cannot report their permanent deletion. The complete requirements remain authoritative.

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

Settings retains up to 20 accepted jobs per operation type in active-category memory
and observes each directly through `GetBackupCreation` or `GetBackupDeletion`.
History page changes do not replace these identities. Category departure or leaving Settings releases
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

## Worker-local workspace storage (issue #1079)

Every storage RPC checks owner/paired-client role authority at the service boundary
before processing its request, including direct internal calls without HTTP
middleware. Reads, mutations and receipt replay recheck current paired-device
authorization in their owning transaction. Missing or Worker principals cannot
observe or control storage operations.

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
Session terminals independently block every storage operation until their original
process cleanup is verified, including terminals labeled exited, closed or
uncertain. Agent Stop and accepted Close alone do not release workspace ownership.
Stopping a forward or labeling it stopped alone cannot release ownership. The server examines the complete bounded session-job inventory,
not only its first page. Extra resources in the managed root also block parent
cleanup. Acceptance pauses dispatch and clears continuation intent atomically;
first dispatch, Resume, continuation, preparation and initial execution claims
independently require present storage. New forwards and retained live socket
authority also require present storage; original cleanup reports remain usable.
Terminal creation, input/resize acceptance and non-close dispatch or claim receipt
reads likewise require present storage. Original close and cleanup authority
remains available while storage is pending, uncertain or stored.
Archive still preserves files. Successful
restoration and recovery remain paused and require explicit later Resume.

The authenticated owning Worker performs five-minute cancellable operations
under the existing session lock plus the independent per-session observation gate
after joining original session-bound read owners and independently reconciling process ownership
and rejecting active execution claims. One private manifest covers the complete
ordered repository set or General Chat directory. Bounds are 8 GiB, 8,192 entries
and an 8 MiB private manifest, with bounded 128 KiB copying. One shared remaining
byte/entry budget applies during workspace and independent Git-store copying.
Capture conservatively reserves the full 8 MiB manifest allowance plus 256 bytes
per repository for copied Git config rewrites before copying payloads; this bounded
headroom can reject data close to 8 GiB even when its final metadata would be smaller.
Git roots count once, Git-created config files reserve an entry before writing,
overlapping administration directories count only when new,
and excess or growing-file bytes never enter the copied payload. Published snapshot
inventory is capped at 4,096 entries. Creation and cleanup hold a Worker-wide
cross-process publication gate from count admission through durable publication;
reject at capacity before staging. A busy gate fails with conflict and no output,
so independent sessions cannot both consume the final slot. Preview remains
available at capacity, and explicit snapshot deletion can release a slot. Removal intents separately allow the two
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
into that independent store with relative worktree configuration. Preserve shared
branch reflogs, including references to reset unpushed commits; overlay only
colliding selected-worktree administration files such as its own HEAD log. Original Local
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
the namespace transition cannot prove that removal ever began. Version-2 claims additionally bind the claimed native root identity. Legacy
claims cannot authorize new unlink. Bounded anchored deletion selects only
original pinned entries and rechecks regular-file hashes/size/mode, symlink text/
kind and named/opened identity immediately before unlink. New entries are never
selected, and atomic empty-directory removal refuses remaining unknown contents.
Changes during removal retain the claim, remaining bytes and recovery uncertainty;
partial recovery checks the same intent and root identity. Final root removal
first synchronizes a bounded metadata-only transition in
`storage-removal-root-claims/<operation>.json`, binding the original
operation/session/snapshot/action, intent digest and native root identity. Claim
the empty root with a no-replace rename into the separate private
`workspace-removal-roots/<operation>-<private-UUID>` namespace before anchored identity, exact
mode and empty-inventory verification. Synchronize both namespace parents before
recording the claimed state. The old removal name is never an unlink operand;
a directory or symlink appearing there is preserved and blocks completion.
On Linux and other POSIX platforms, retain the opened final root, remove its
search permission, and repeat the anchored identity check immediately before
`unlinkat`; this closes the retained-root-handle path to the private parent
during the final name operation. Darwin cannot unlink by directory handle, so
it transfers the verified writable root with an exclusive directory-fd rename
into the fresh operation-private
`workspace-removal-quarantine/<operation>-<private-UUID>` namespace, rechecks
the native identity, removes search permission there, and unlinks only the
quarantined name. The old private namespace remains a recovery boundary.
Repeat the anchored check after the last mutation checkpoint as well. If a
retained parent moves the original and the name operation selects a replacement,
the second check retains recovery ownership before any post-unlink receipt can be
published. Linux additionally verifies the opened directory's post-unlink
link count so a retained-parent race in the final kernel interval remains
recovery-required; Darwin verifies the opened identity and unlinked path after
the quarantine unlink.
Keep unlink preparation separate from the durable receipt recorded after native
unlink. Publish that receipt with a bounded cancellation-independent context
before honoring the caller's cancellation. Recovery with a missing root and no unlink receipt remains uncertain,
including interruption between unlink and receipt publication. With the original
receipt, recovery synchronizes both parents and independently requires both
names absent before recording completion. Missing or replaced proof/root never
reconstructs removal authority from absence or matching bytes. The workspace
owner retires only a validated final-root proof after its durable removed state
and final namespace absence, before generic session-copy cleanup; legacy intent
and journal retirement remains separate. A canonical proof that reappears after
that boundary is absence-only. Retire the remaining proof records only at the
acknowledged-report boundary after checking final namespace absence.
Permanent deletion resumes only the original final-root transition and includes
its namespace, proof and target-attributed atomic-write remnants in both removal
and completed-proof replay inventories; generic copy cleanup cannot remove a
reappearing final root. Scratch cleanup keeps
its separate operation-owned enumeration. Only create/cleanup and restore create
scratch. After exclusive directory creation, synchronize an external versioned
claim binding the exact operation/request digest, preparation, session/machine,
snapshot/action and native root identity before copying. Direct failure cleanup,
explicit recovery and permanent deletion verify that claim through the opened
removal root before touching entries. Preview grants no scratch authority;
missing/legacy proof or replacement directories remain protected without adoption.
Keep claims until coordinated permanent deletion checks scratch absence; a later
reappearing directory cannot pass generic Worker copy cleanup. Verification and directory
synchronization remain mandatory. A second
repository copy failure or cancellation cannot remove either original repository.
Failures after a namespace transition retain recovery uncertainty and private
copies. Cancellation/failure after verified snapshot publication but before source
removal also retains uncertainty: explicit recovery registers that same retained
snapshot while settling the incomplete cleanup and preserving present sources.
Creation promotes its original external staging claim with the exact snapshot
manifest SHA-256 only after the successful synchronized no-replace rename,
post-rename content verification and comparison to the original native root
identity. Snapshot inspection, recovery and permanent deletion require this
publication proof; creation recovery also matches the entire original request.
Matching copied bytes or operation IDs cannot establish publication. Missing,
legacy or mismatched proof leaves foreign snapshots and original staging/source
data protected behind recovery-required ownership.
Terminal metadata and session state/events commit together after the
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
rename publishes it at the original canonical owned destination. Rewalk the
renamed live root against the pinned complete snapshot inventory before publishing
restoration ownership. Changed, missing or additional bytes retain the pending
proof and the live namespace for recovery; do not erase or reconstruct them. Existing files
are never replaced and no per-repository partial restore is reported. Failed
unpublished restoration removes only its operation-owned staging with an
independent bounded cleanup context; unconfirmed scratch cleanup retains
recovery-required ownership instead of settling a terminal failure. A private
restore binding preserves the original logical workspace identity while allowing
self-contained Git stores at that same path. Its pending form grants no ownership;
only the original operation/snapshot/hash-bound proof synchronized after successful
no-replace publication permits restore recovery, restored execution identity or
coordinated deletion. A matching foreign destination and absent scratch cannot
prove publication. Version-2 proof additionally pins native filesystem directory
identity (Unix device/inode or Windows volume/file index) for the published root,
all repositories and their independent Git stores. Recheck it around subsequent
Git identity inspection and coordinated deletion; byte-identical replacement
directories cannot borrow prior publication. Ordinary file/index/config changes
and new commits retain identity. Missing or legacy proof remains uncertain and
cannot be reconstructed from current paths. Lost proof preserves uncertainty
without repeating the rename.
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
capacity/free bytes before/after. Successful inspect, restore and delete results
use the inspected snapshot's pinned source bytes and the session's post-action
retained inventory, excluding other sessions. Deleting a snapshot contributes no
confirmed live-source removal. All byte counts use canonical decimal strings.
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

### Retirement storage boundary

`retired_configurations` preserves the complete original record columns and document bytes independently of live `entities`. Ordinary Get, mutation, routing, catalog and export never consult it. Only explicit owner/client historical reads and usage labels may use it; their retired Resource projection has schema 2. Permanent tombstones and redacted historical receipts prevent resurrection. The schema marker is `service-accounts-v2`. The integrated migration also rebuilds the notification-delivery table, preserving every prior claim while extending its closed kind constraint to account-scoped subscription recovery. Its independent `subscription_notification_layout=account-recovery-v1` marker is required together with the identity marker; a partially composed or unrelated version-28 layout is recovery-required. This composition precedes migration 28's first main activation and does not alter historical migration 17.

Before creating retirement tables or changing configuration, live upgrades reject original connections, protected generations/identity commitments, pending operations, leases, recovery fences, unremoved device browser profiles and affected claimed/uncertain native work. Failure rolls back the entire composed transaction and preserves the synchronized pre-migration backup. A private restore candidate has explicitly historical ownership without current local authority; it migrates before replacement preparation/publication, preserves original retired documents and merges current retirement/tombstone evidence. This exemption never applies to ordinary startup.

Affected Agents retain names, options, templates and original model IDs with server-owned `reconfiguration_required`; ordered surviving account weights remain unchanged. Project account restrictions preserve their configured flag even when all IDs retire. Only affected Schedules disable their timer and retain accepted occurrence history with explicit reset guidance. Session/snapshot/transcript/usage bodies and their revisions remain unchanged. Pricing retains original API attribution and independently stores the service identity of newly configured native models.


## Desktop workspace storage and permanent deletion

The connection-owned `SessionStorageProvider` in `apps/delidev/src/session-storage.tsx`
presents current workspace state, usage preview, snapshot creation, original
operation inspection/cancellation/recovery and paginated snapshot inspection,
restoration and permanent deletion. Session details open the workflow; closing the
modal or navigating retains original request/job/session identity. original Local checkouts do not expose
managed storage actions. Independent storage/deletion capabilities produce explicit
older-server update guidance before product actions.

Storage acceptance is displayed separately from Worker success and independently
verified native cleanup. Exact decimal byte counts retain integer precision;
logical source, retained snapshot and removed bytes remain separate from nullable
filesystem free measurements. Cleanup confirmation pins the exact successful
preview job and current session revision. Snapshot removal has separate irreversible confirmation. Restoration
and recovery disclose paused outcomes and preserve original uncertainty.

Permanent deletion requires its own explicit confirmation of managed native,
workspace, backup removal. In-flight native work is stopped
through its original owner; original Local checkouts and independent Forks remain
outside removal authority. `GetSessionDeletion` observes the accepted original
session independently of resource removal, exposing Worker, database and backup
progress. Closing/navigating cannot resubmit either deletion or storage. Unknown
acknowledgments retain original wire requests in the connection mutation registry;
explicit retries use identical UUID/revision/selection bytes. A replacement
connection follows the existing connection-memory lifetime and never adopts
another server's operation.

Removal append journals have a separate 64 MiB bound because each transition repeats original and private paths; immutable manifests and claims retain the 8 MiB bound. Capacity-triggered atomic journal compaction drops superseded restoration transitions while preserving exact active prepared/renamed/directory-mode replay and per-entry removed receipts against the unchanged original header. Failure/cancellation cannot replace a journal with partial proof. Snapshot publication by either create or cleanup makes later failure recovery-required; original recovery must retain that published snapshot before reporting settlement. CLI snapshot deletion requires explicit `--confirm`.

Source preview/capture/cleanup observation enforces one shared 8,192-entry/8-GiB allowance for the workspace and all original Git administration/object inventories before hashing payloads; per-repository limits cannot multiply the retained observation. Oversized aggregate observations publish no preview digest. Permanent deletion and completed-proof replay include each original removal append journal in the same immutable job-derived path inventory. Reappearing journals block acknowledgement without granting replay new deletion authority.

New source capture/observation admission reserves the largest original/private name at each path component plus snapshot/Git wrappers and the native removal root, within the platform path ceiling and the 4,096-byte relative replay bound. Insufficient headroom fails before snapshot publication or source removal. Original `.git` entries are checked before Git path resolution: regular linked-worktree pointer files are supported; symlinked administrative entries are rejected without copying or changing their external target.

Source observation counts restored Git stores already covered by the complete managed workspace root only once. The root inventory still binds every retained Git byte and change. Failed or canceled explicit recovery restores its original uncertain predecessor as the retry anchor. Exact acknowledged failed/canceled recovery reports discard their pending replay receipts while retaining predecessor removal intent, including after response loss; these terminal attempts cannot block Worker reconnect or grant source-removal authority.

Storage recovery preserves the pinned snapshot logical source bytes for inspect, restore and delete. Deletion intents retain that count before unlink under the original namespace claim; missing legacy count stays uncertain. Partial unpublished restore staging is removable only through its original external operation claim and native root identity, without completed-inventory equality. New storage admission reserves a job slot within the 4,096-job permanent deletion inventory bound and rejects atomically at capacity. Each new operation reserves its complete eight-claim lost-report recovery lineage. Cleanup also reserves a full restore plus its eight recoveries (18 total slots); recovery consumes only its remaining original lineage. While stored, inspection and other non-restore operations preserve the complete restore lineage. Repeated lost reports cannot exhaust restore/reconciliation capacity before the supported final recovery.

The CLI snapshot inventory negotiates `WORKSPACE_STORAGE_V1`, requires the owning session UUID and preserves opaque Resource cursors through `--page-token`; `--limit` accepts 1–100. After failed/canceled explicit recovery, the desktop retains the attempt but uses the refreshed original uncertain predecessor for its next request. A lost report reply replaying acknowledged uncertainty discards only the pending report receipt and marks the journal reported, preserving original removal intent/claim ownership.

All snapshot namespace publication, storage inspection/restore/delete/recovery, retained inventory scans and permanent-session copy removal share the same cross-process gate after the session/observation locks. Hold it through native effects and final inventory accounting; contention fails before any effect.

- Claimed removal accepts only immutable directory permissions or the exact writable native mode authorized by an original durable directory-mode transition. Retain that transition through journal compaction, partial recovery and failure; check directory identity and permissions again before unlink. Preserve retained-writer permission changes as unresolved recovery.

- A stale original storage report may acknowledge only an independently completed explicit recovery, with its exact immutable assignment, original instance/device, revision, input and recovery-claim digest. Preserve the original UUID receipt through the ordinary report transaction without another state/native mutation. The Worker clears only that pending report receipt and marks its journal reported; the successful recovery owns removal-intent retirement. Uncertain or unproven originals retain their receipt and native evidence.

Compaction acceptance and execution credential publication require present workspace storage. Pending, uncertain or stored workspaces cannot grant native compaction authority.

Recovery after the top-level removal rename may create a missing original claim only when its private namespace remains intact and matches the complete synchronized intent, and neither a claim nor its journal exists. Partial or fully absent namespaces require the preexisting original claim. Malformed or replaced claims stay uncertain.

Workspace-storage recovery retains original preparation/manifest evidence twice
and at most eight immutable claim references. Its input is capped at 3 MiB and
its typed job entity at 4 MiB, with an 8 MiB native Connect response allowance
for JSON/base64 overhead. Each original request retains the 1 MiB bound, and
ordinary storage jobs/entities keep their original limits. Writes, strict reads,
Worker dispatch, cancellation, report/receipt reconciliation and permanent
deletion use the same ownership-specific rule. This finite exception can be
removed only after immutable original evidence is stored by reference; it grants
no new filesystem, execution, replay or inferred cleanup authority.

Primary WatchWork inspects storage inputs through the owning typed decoder both
before and inside claim admission. Its strict claim-receipt decoder reserves
1 KiB for Record metadata separately from the unchanged job bound, then selects
the recovery, compaction or ordinary job decoder. Unknown fields, duplicate keys,
trailing documents, malformed exceptions and oversized ordinary jobs remain
rejected. Same-instance reconnect delivers the original claimed ID, revision and
input bytes without another claim mutation or native effect.

Storage observations preserve the exact accepted snapshot ID, session, machine,
digest, byte size, creation instant and repository count. Successful original
deletion changes only its Deleted tombstone. Failed inspect/restore/delete recovery
uses the same pinned metadata and cannot replace the historical record.

Storage publications in shared private directories use target-attributed atomic
temporary names: `.pending-<UUID>.json-<decimal suffix>`, plus
`.pending-<UUID>.pending-<decimal suffix>` for claim-journal compaction. The storage
writer exception leaves ordinary private atomic-write behavior unchanged. Permanent
session deletion and completed-proof replay inventory those five shared namespaces
with a separate 65,536-entry limit per directory. Original session/job target names
retain cleanup authority even for partial contents; unrelated operations remain
untouched. Unknown legacy names, malformed names, non-private files and overflow
retain pending cleanup. A remnant that reappears after completion blocks receipt
replay and is preserved. Per-operation staging/removal/snapshot directories retain
their existing native identity and complete-inventory ownership gates.

A failed or canceled nested recovery retains its immediate target in the first
immutable claim/parent as the retry anchor. The flattened original remains
historical reconciliation evidence; it does not bypass unresolved intermediate
recovery ownership. Snapshot inventories include the original native root mode
in their digests. Managed workspace roots require the original private writable
mode before capture or rename, so changing root permissions after preview
rejects without moving the workspace or publishing a snapshot. Removal validates
that same root-mode authority alongside the native identity and independently
journaled child-directory mode transitions.

Storage admission reserves eight total original-group recovery attempts, counting
canceled and failed accepted attempts as retained deletion obligations. Native
uncertainty claims have their separate eight-member bound. Terminal attempts do
not reset the reservation count or strand an earlier still-supported successor.
Exact request replay consumes no additional attempt; exhausting the finite
recovery-attempt bound preserves evidence and returns explicit resource guidance.

Published snapshots bind the native identities of the original managed root and its chat/repository directories. Recovery verifies that publication and those identities, then uses its pinned source bytes and digest without rewalking mutable original files or external Git stores. A published Create settles as succeeded; Cleanup with its original source still live settles as failed with source preserved. Unsupported later files and unavailable external Git do not strand that settlement. Directory replacement remains uncertain; pre-amendment snapshots retain their previous checks. Windows cross-volume original Git stores remain external observations under the same aggregate budget.

The shared server workspace read scope admits new file, directory, diff, private PR and terminal reads only while storage is present. Pending, uncertain and stored state closes that scope before publication. Previously admitted responses and cleanup retain their original separate ownership checks.

The connection-owned desktop controller polls an active retained storage anchor for external deletion. Authenticated session NotFound releases the stale local operation lock and allows finishing/switching the view, while in-flight or uncertain mutation receipts remain retained. Independent deletion status continues to distinguish pending cleanup, completed cleanup and unavailable status; absence alone cannot prove cleanup. Transient or authorization errors do not release an original operation.

Removal journals use newline-framed records. Validate the complete prefix before atomically discarding an unterminated final append; malformed complete records remain uncertain and unchanged. Retain per-entry removed receipts through compaction. Recovery accepts an absent entry only with its own durable renamed/removal proof, and rejects reappearing settled entries. Legacy cleared records grant no missing-entry authority.

Storage results retain at least the surviving snapshot size in retained bytes and bind successful cleanup to its original canonical preview digest, including recovered cleanup. CLI snapshot inventory emits revision as a decimal JSON string.

Atomic journal compaction represents each settled removal with one inventory-bound original-path proof; it does not repeat generated private paths. The original immutable inventory bounds all such proofs, including deep-directory generated-name expansion. Recovered successful cleanup emits and validates the original snapshot source/preview digest before server settlement; authenticated native-result fixtures also verify the final accepted job state.

## Added hosted-provider defaults (migration 30)

Real 30 follows implemented OAuth 29 and actual accounting/diagnostics/retirement 26–28. It stores the private `provider_presets_layout=hosted-additions-26-v1` marker and seeds only the 26 explicitly allocated hosted preset identities. Historical defaults migrations retain their original six-ID set. Existing managed UUIDs, Off state, custom providers/accounts/models and explicit deletion of original presets remain authoritative. No Account, credential or model is created. Upgrade synchronizes the original backup first and publishes all predecessor/layout/seed changes atomically; failure leaves the original version and image intact. A current-store reopen validates the exact layout marker and never seeds again.

Sidechat storage retirement decodes original storage/recovery inputs with the owning strict 3 MiB rule, even when no Sidechat is selected. Its private retirement wrapper permits the existing complete 4 MiB deletion plan plus 4 KiB fixed operation metadata, consistently at publication and restart. The allowance preserves complete original child cleanup obligations and grants no additional native removal authority or ordinary entity capacity.

Server-owned subscription login retains optional original server_operation metadata and a disjoint native_started credential fence. Pending, claimed or recovery-required server ownership blocks managed restore and deletion like original Worker ownership; a terminal record grants no external authority. Restart preserves original pending/runtime obligations as recovery-required without relaunch. No new SQLite migration is required. Follow the managed subscription contract.
## Pre-release database baseline reservation

The [pre-release reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset)
reserves baseline 32 after real schema 31. Its complete implementation directly
initializes the current functional layout and removes upgrades from schemas
1–31. Unsupported DBs and backups retain their original files and sidecars;
no startup, inspection or restore may silently convert or reset them. This
reservation adds no executable migration or runtime capability.

## Device-owned Claude authentication metadata

Claude subscription ownership uses optional server-owned Account JSON under
capabilities System 38 / Worker 20. `native_profile_id` and `owner_machine_id`
are present together, and usable generation/connection requires the original
profile plus keyed identity commitment. Optional `native_operation` retains
original actor/action/machine/server epoch, bounded start/expiry, closed state,
login method, original code submission/consumption metadata and safe diagnostic
fields. The original exclusive lease remains fenced after restart, owner loss
or uncertain native cleanup. Code claims are durable before memory retention;
consumption is durable before delivery. URL/code bytes and native identity text
never enter SQLite, receipts, events, jobs or backups. No SQLite migration is
added and no existing migration/reset reservation is activated.

Worker-private canonical profile ownership and process/lease journals remain
on the original Runner. The official CLI owns its authentication files and
secure-store entries. Profile cleanup requires original native logout/status,
joined processes and inode-bound bounded removal. Missing/foreign/symlinked or
oversized native state is uncertain, never cleanup proof. Server disconnect and
configuration deletion remain blocked while profile/owner, generation, pending,
lease or recovery remains. Native history retention is separate from server
transcripts and grants no credential transfer.

Backup restore retains ownership references only as quarantined historical
metadata with readiness removed and recovery required. It cannot adopt a
Worker's profile or enable execution on another machine. Portable configuration
v2 omits protected subscription ownership; imported account preferences start
without authentication. Native checkpoints serialize no profile paths or auth
files and pin only the accepted profile reference through their comparison
digest. Follow the [subscription contract](cmds-delidev-subscription-contract.md#native-claude-subscriptions).
## Failed subscription cleanup jobs

Server-owned `cleanup-failed-subscriptions` parent jobs and `cleanup-failed-subscription` children use existing generic entities, jobs and receipts, without a database migration. The subscription owner validates their closed input/checkpoint/results, original actor/server, fixed account revisions and original login/deletion identities. Only confirmed account cleanup transactions advance child revisions. Terminal retained attempts cannot rerun automatically; tombstone, deletion receipt, child result and parent counts are atomic. Narrow pending-job filtering occurs before bounds so unrelated Worker history cannot hide the one active batch.

Managed restore eligibility includes queued cleanup parents/children as unsettled ownership. The existing image transformer cancels every historical nonterminal cleanup job and quarantines its receipts. Maintenance selects only pending original live jobs; historical canceled or terminal jobs cannot acquire account deletion authority. Follow the [subscription contract](cmds-delidev-subscription-contract.md#failed-subscription-cleanup-reservations).

Each batch child records native and credential-attempt fences before external cleanup. A confirmed native checkpoint can resume only before the credential attempt begins. An interrupted credential attempt with no confirmed account checkpoint is retained without repeating vault effects, even when its previous outcome write failed. A fresh explicit batch can retry that account.

## Inline Worker models and endpoint-only completion reservation

DB baseline 32 directly initializes the complete Model-free current schema: inline Worker definitions, source/native-ID pricing and immutable execution/usage attribution. Remove persistent catalogs and model indexes/suppressions. Earlier DBs and backups are unsupported. The owner waives earlier DB retention and permits explicit DB/sidecar reset; protected credentials and native ownership retain their original cleanup authority. No conversion or placeholder migration is permitted.

Follow the complete [catalog amendment](cmds-delidev-catalog-contract.md#inline-worker-models-and-endpoint-only-completion-reservation) and [current-only reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset). This reservation changes no runtime support or native/account acceptance.

## Selected skill snapshots

Follow the sessions contract's Explicit native skills section. Worker inventories
contain private package paths; public resources contain only opaque identities,
revisions and ownership references. Complete selected packages are private,
revision-pinned and independently copied into native runtimes. Exact staging
retries verify retained snapshots rather than reread changed source packages.
Removed queue rows and typed edits retain original snapshot references,
including replaced selections in retired bindings until confirmed session cleanup. Permanent session deletion
includes those references even before any native job is claimed and waits for
original paired Worker removal. Restore quarantine cannot reacquire execution or
deletion authority. No automatic accepted-snapshot eviction or relational migration
is introduced.

Private skill preparation journals preserve original request/actor/device proofs
before dispatch and complete Worker file/root claims before copy publication.
They remain outside portable backups. Restore receipt quarantine is an unknown
outcome and cannot authorize preparation cleanup. Cleanup requires positively
absent original receipts after joined handlers or exclusive restart; original
paired-device removal tombstones prevent delayed copying and root replacement.
Accepted snapshots retain ordinary session deletion ownership. Skill-only and mixed skill/image plans always observe original skill snapshot absence on completed replay, without acquiring execution or workspace namespace authority.

Active server staging journals have a 4,096-entry admission bound. Confirmed
accepted or removed outcomes move atomically to exact-ID hash-sharded private
terminal receipts before active-record removal. Worker intents retain capacity
while original snapshot bytes exist. Confirmed original session deletion validates
those intents and moves compact removal tombstones outside the active bound.
Terminal lookups occur only for the exact request; maintenance never scans lifetime
receipt history. Tombstones reject delayed dispatch and preserve replacements.
Prepared result recovery normalizes delivery instances only through the complete
immutable original preparation proof; ordinary inventory scope checks stay strict.
## Session image removal and restore

Image attachment jobs retain closed references and the original Worker device. Empty entity session scope preserves independent Fork lifetime. Synchronized permanent deletion drops only its session owner. Last-owner images join original Worker plans; image-only plans grant no workspace authority. Native cleanup joins before deletion. Offline Workers retain durable pending work. Completion and replay require the original deletion receipt and absent data and metadata; reappearing files remain protected. Purge requires original Worker acknowledgement.

Backup exclusion includes every current image owner. Restore discards historical image jobs, preserves current ownership and completed removal metadata, and quarantines retained references. Restored Ready or Claimed records grant no upload, input or readback authority. Jobs and backups contain no image bytes or paths. This adds no SQLite migration.

## Independent project prompt storage

Issue #1828 uses immutable `project_prompt_history` entities with the exact project ID and an empty session ID. Append/prune and confirmed clear use ordinary durable receipt transactions. Project deletion removes live history atomically. Session deletion and first-input editing do not own these records. No SQLite migration is added.

Live history removal emits deletion metadata without native/configuration tombstones or source-receipt redaction. This permits captured-history restoration while project tombstones retain their original authority.

Managed backups capture history. Open and backup validation check closed documents, project ownership, unique acceptance sequences and the 100-entry bound. Restore follows captured history, except current project deletion tombstones remain authoritative. Portable configuration exports exclude history. Clear removes live history only, without securely erasing immutable older backups. Restoring a backup may restore its captured history. Logs contain operation/project/correlation IDs and safe codes, never prompt text.

## Typed Job history during backup restore

The private restore candidate uses the owning storage Job decoder for retained
history. Supported compaction and workspace recovery Jobs retain their closed
type-specific document bounds; ordinary Jobs retain the generic 1 MiB bound.
This does not enlarge other documents or the aggregate transformation bounds of
100,000 documents and 256 MiB. Preserve original input, output and attribution,
and quarantine nonterminal historical Jobs without granting native authority.
Unknown fields, duplicate keys, invalid UTF-8, trailing JSON and oversized typed
documents remain rejected before candidate publication. Source backup bytes and
the live database remain unchanged on rejection.
