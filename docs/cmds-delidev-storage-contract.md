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
The approved completion work includes every remaining the feature requirement;
actual account/private-GitHub access and platform distribution validation remain
deferred. This document covers managed backup observation, creation/deletion, permanent
session deletion, database restore and Worker-local workspace snapshots and restoration.

## Subscription retirement

Migration 28 implements the feature
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
managed-account ownership and cleanup boundary from the feature.

## Account OAuth attempts

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

`SystemService.CreateBackup`, `ListBackups` and `InspectBackup` are restricted
to the owner and paired clients. Workers cannot invoke them. Before complete
reset activation, the actor-authenticated synchronous creation receipt reserves
one backup UUID before filesystem work; the same request retries the original
image instead of creating another. Current durable creation retains its original
job and image identity under the durable path below.

`delidev backup create [--wait]`, `backup list [--limit N] [--page-token TOKEN]` and
`backup inspect --id ID` use the owning creation, listing and inspection RPCs.
Current creation uses `RequestBackup` rather than synchronous `CreateBackup`.
Ordinary commands never start a server. Settings > Backups provides creation, pagination and explicit integrity
inspection, retaining the exact creation request after an uncertain response.
Before complete protocol-2/baseline-32 reset activation, the legacy `CreateBackup` RPC retains its synchronous receipt behavior for existing clients; reservations alone do not change that behavior. At complete activation, the [reset contract](cmds-delidev-structure-contract.md#pre-release-compatibility-reset) retires synchronous support: the historical declaration preserves allocation provenance and returns Unsupported after authorization without a receipt, backup file or current job. Current CLI/desktop creation uses the durable path below. Backup sizes use decimal strings in CLI JSON and BigInt in the desktop.

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
Assembly and every plan reader share the 4,096-job allowance for original jobs
and distinct Worker owners. Each plan validates job and device UUID uniqueness
across that full capacity independently of the general 1,000-link bound; one immutable plan is capped at 4 MiB. Worker pages retain at most 20 envelopes
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
legacy attempt-source PR Activity, including reservation activity recorded before session
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
reapply removal to stale restored managed data. Each fully inspected unrelated
image adds a synchronized private classification checkpoint bound to the original
session deletion ID, immutable intent digest, server, backup ID, inspected SHA-256,
schema and native file identity/change generation. Omitted checkpoints are legacy
compatible. A sibling `.backup-scan.json` record in the existing private deletion
journal retains at most 4,096 clean facts and 3 MiB, separately from the immutable
4 MiB Worker plan. Strict closed decoding and original intent binding apply at
recovery; malformed, foreign or orphaned records remain pending.

Subsequent bounded passes reuse only unchanged native generations, modes, sizes
and modification times. Access time is not an identity field. A changed or
replaced image invalidates only its own fact; new images require full immutable
copy/content inspection. Retain completed facts across cancellation, restart and
database rollback. A canceled/failed copy creates no clean fact, and unknown
scratch, sidecars or claimed removals still block completion. Classification
metadata grants no deletion authority; containing images retain the existing
original durable backup deletion path. Recheck the current complete inventory
under the publication gate through the final deletion acknowledgement so late
publication cannot inherit an earlier clean scan. Diagnostics may include the
bounded retained checkpoint count, without image paths or content. Logs contain operation/UUID,
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

The feature adds owner/paired-client `SystemService.RestoreBackup` and
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

Request authorization, input checks and admission waits retain their thirty-second
bound. After admission, the original restore runs under the joined server epoch
with the exact actor and request identity, without a whole-operation deadline.
Client wait cancellation or response loss cannot cancel preparation or manufacture
rollback, replay or a new identity. Explicit server shutdown and listener failure
cancel the original operation and join it before retiring account secrets, SQLite
or the process lock. Stage failures remain explicit; existing prepared/published
barriers, source images and receipts retain their original startup recovery.
No automatic retry is added. The operation holds both the managed-file gate and
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
legacy source-linked Activity before removing the original session graph.
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

## cmds/delidev-cli/internal/cli constraints

- Project first-prompt history follows the sessions/storage/desktop/protocol/client contracts for the feature. Preserve immutable project-owned text with empty session IDs, atomic 100-entry acceptance order, actor-bound confirmed clear receipts, scoped byte-bounded reads and text-only boundary recall. System 48 / EntityKind 35 add no Worker capability or migration. Session deletion preserves history; project deletion removes it, managed backups capture it and portable exports exclude it. Never log prompt text.

- `session delete --id ID --revision N --confirm [--wait]` and `session deletion --id ID` share authenticated Connect semantics. Preserve original UUID/revision and accepted progress after uncertain reads or cancellation; waiting cannot resubmit deletion or report pending cleanup as success. Follow `cmds-delidev-storage-contract.md`.

- Workspace storage commands use the authenticated WorkspaceStorageService, retain exact UUID-v7 receipts/revisions, and observe the original job without replaying native operations. Follow cmds-delidev-storage-contract.md.

- Managed database restore follows `cmds-delidev-storage-contract.md`: exact inspected image/live revision and actor-bound external receipts, exclusive settled ownership, immutable current safety/deletion authority, paused/quarantined historical work, and pre-open journal recovery. Preserve typed publication-versus-startup outcomes; uncertain retries never republish or revive native claims.

- `snapshot list --session-id ID [--limit N] [--page-token TOKEN]` negotiates workspace-storage support and uses authenticated Resource pagination. Preserve original opaque tokens, exact metadata revisions and explicit older-server guidance.

## cmds/delidev-cli/internal/domain constraints

- Return safe deletion-attempt errors with retained job state even on unchanged retries, so maintenance logs persistent failures using only job ID and typed code. Preserve stable revisions for identical pending outcomes.

- Deletion recovery caps canonical obligations at 4,096 separately from the 8,192-entry directory bound. Pending atomic-write remnants consume only directory capacity and remain preserved; validate every retained obligation before reconstruction.

- Permanent session deletion uses `MaxSessionDeletionJobs` for its 4,096-copy and original Worker-owner capacity. Validate UUID uniqueness within that full allowance; do not reuse the unrelated 1,000-link helper for the ownership plan.

- Permanent deletion accepts original workspace-storage jobs with optional reserved snapshot UUIDs. Preserve omitted legacy fields and bind each nonempty snapshot ID only to its storage copy.

- Workspace-storage recovery alone may carry a 3 MiB input containing two individually bounded original preparation/manifest copies and at most eight original claim references. The owning workspace/store decoders narrow this typed allowance; ordinary job/entity/command bounds stay unchanged.

## cmds/delidev-cli/internal/imageinput constraints

- Synchronize immutable journals, data writes and deletion tombstones before reporting success. Identical chunks may replay; conflicting bytes or ownership fail. Deletion receipts prevent delayed writes from recreating bytes.

- Retained deletion proof is read-only. Reappeared files, symlinks and changed receipts fail proof and remain untouched. Do not infer cleanup from a missing journal alone.

## cmds/delidev-cli/internal/server constraints

- Preserve durable request receipts, typed revision checks, atomic state/events/routing, independent outcome/archive/recovery, uncertainty before retries, and deletion tombstones.

- Keyless account cleanup/deletion must remain usable without an OS credential store. Skip vault access only with validated immutable keyless API provider ownership; preserve relay cancellation, cleanup generations, receipt replay and credential-bearing staged-intent reconciliation.

- Permanent session deletion uses owner/client Connect mutations and original-job reads plus an independent owning-Worker cleanup lane under `cmds-delidev-storage-contract.md`. Join maintenance at shutdown. Forward cleanup reports remain admissible during deletion, while session controls, new copies and native socket authority are closed; final purge requires independently confirmed original peers.

- Worker workspace storage follows cmds-delidev-storage-contract.md. Protect active/Local work and unresolved dependents; reserve jobs and publish snapshot metadata, job outcomes and paused session events atomically. Resume and new forwards cannot race accepted storage jobs. Storage requires every forward to have both original peer cleanup confirmations; unavailable storage closes live socket authority while preserving cleanup reports. Reconnect never repeats started native effects. Ephemeral file/diff/private PR reads are fenced by the owning Worker's independent session observation/storage gate through native cleanup; job admission alone cannot prove those owners are absent.

- Every WorkspaceStorageService handler requires owner/paired-client role authority before request processing, independently of HTTP middleware. Revalidate current paired-device authorization in the transaction used for reads, mutations and receipt replay; missing and Worker principals cannot observe or control storage jobs.

- Accepted NetworkService receipt replay retains the original request and replay classification after later resource deletion, returning `deleted` with no resource or new protected work. Only an authoritative NotFound read denotes deletion; authorization, cancellation and storage failures remain errors.

- Completed profile-deletion retries inspect any existing private cleanup intent but never create another intent or reopen the vault after the obligation has been retired. Preserve recovery of genuinely pending accepted deletions and rejection of altered receipt input.

- Before removing a network profile, synchronize one private server-bound deletion intent with original actor/request/revision. Current authorized owner/client mutations recover it independently of original actor revocation. Require an accepted original deletion receipt and authoritative absent profile before native removal; uncertain proof or failed cleanup blocks replacement, while an absent receipt clears metadata only. Retire the intent after confirmed cleanup within the bounded RPC.

- Failed or canceled workspace-storage recovery restores its immediate uncertain assignment predecessor as the retry anchor, including recovery-of-recovery; the flattened ultimate original does not erase intermediate ownership. An uncertain recovery retains its assignment chain; terminal failure cannot become recovery authority or settle the predecessor.

- Workspace-storage admission reserves the new job and its complete eight-claim recovery lineage within the 4,096-job permanent deletion plan bound. Cleanup and other operations while stored also preserve the full restore/recovery lineage. Each explicit recovery consumes only its original remaining lineage capacity. Full inventory rejects before job/session publication.

- Storage recovery retains full immutable original preparation/manifest and claim evidence within its dedicated 3 MiB input/4 MiB job bound. Read, cancellation, report validation, explicit recovery-chain settlement and original-report reconciliation use the same typed decoder; ordinary storage jobs remain bounded at 1 MiB.

- Snapshot observations and recovery outcomes preserve every field of the accepted snapshot metadata. Only successful original deletion may transition Deleted; failed recovery projects inspection without rewriting immutable size, creation time, digest or ownership.

- Worker-only permanent-deletion reads preserve all 4,096 original copies within a 4 MiB ownership envelope/page and at most 20 envelopes. Exact retiring-assignment inspection uses the same bound and authorization; no truncation or native cleanup inference is permitted.

- Current protocol-2 backup creation retains only durable RequestBackup jobs and joined publication. Retired synchronous CreateBackup preserves original authorization refusals and returns Unsupported without receipts, files or fallback. Its declaration retains immutable numeric provenance; current restore/inventory fixtures publish the original durable job explicitly. Follow the storage/reset contracts.

## cmds/delidev-cli/internal/store constraints

- `backup create --wait` returns a typed nonzero exit for failed or unconfirmed completion and retains the accepted request/job result on failure, interruption and read errors. Waiting never creates a replacement job.

- Schema 24 reconciles the backup and automatic-title layouts of version 23 after provider activation (21) and hosted defaults (22). Preserve original title usage and both once-only inference/HTTP claims; add only missing title columns/indexes/tables. Preserve backup jobs from the pre-merge version-21/22 backup layouts, add any missing provider index and seed defaults only when that layout has not received them. Preserve explicit deletions from main version 22; synchronize a pre-migration backup and commit all changes atomically.

- Backup job observation uses owner/client `GetBackupCreation` and `GetBackupDeletion` independently of bounded history pages. Keep accepted IDs and exact revisions through navigation, refresh inventory after observed completion, and never replay a mutation to poll status. Follow `cmds-delidev-storage-contract.md`.

- Final backup-copy authorization uses the original actor under the exclusive store gate held through VACUUM/publication. Never substitute maintenance owner authority or rely on a prior unlocked authorization read when revocation can win before the copy.

- Managed backup deletion uses existing durable jobs/receipts and schema-24 indexing, plus immutable synchronized `backup-deletions/` intents outside SQLite before unlink. Preserve exact inspected revision/metadata/hash, original actor/request identity, startup obligation reconstruction, creation-replay suppression, bounded pending retries and source preservation on mismatch. Never evict deletion obligations or equate logical image bytes with reclaimed disk space; follow `cmds-delidev-storage-contract.md`.

- DeliDev managed backup operations follow `cmds-delidev-storage-contract.md`. Keep owner/client-only authorization, bounded metadata pagination, exact creation retries and source-preserving integrity inspection. Inspection must read an identity-checked private copy, reject sidecars/foreign server identity, recheck authorization after I/O and never imply restoration or credential/Worker recovery. Preserve exact byte counts and clear stale success after failed reinspection.

- Run package Go tests and vet. Use real temporary SQLite/Git/process resources for integration tests; generate protocol bindings from the schema.

- Persist native questions through the exact durable outbox/receipt path with execution-scoped unique request ownership, bounded open counts/bytes and unchanged original question content after closure. Native waiting flags are observations, never answer/approval/closure authority. Terminal publication closes unanswered requests atomically without fabricating responses; uncertain Worker loss cannot prove closure. Resource reads expose retained interaction documents; dedicated owner/client response acceptance and owning-Worker claims govern non-secret answers, while protected answers require their own delivery/retention boundary and inbox read state uses its independent owner/client API. Reject native response publication without its durable owner claim. Schema migrations preserve prior transcripts/indexes and synchronized backups.

- Project/Agent configuration deletion must remain independent of retained session lifetime. Disable matching schedules atomically and preserve session/snapshot/occurrence/input/workspace records. Existing Agent snapshots require the original deletion tombstone when their selectable configuration is gone. Retain only final project Agent/account restrictions with the project tombstone for already established snapshots; both continuation and active relay authorization must preserve those restrictions. Missing/corrupt evidence never becomes unrestricted access, and unstarted sessions cannot freeze deleted settings. Preserve current account/Worker/cleanup authority. Schema migrations publish validated, synchronized backups from private pending targets before beginning migration and cannot rerun earlier schedule/inbox ownership migrations against current records. Configuration deletion is not permanent session or managed-backup erasure.

- Backup success requires a validated synchronized file and synchronized parent directory after publication, including exact retries after an uncertain directory sync.

- Failed first SQLite initialization must close the database before removing only attempt-owned fresh files; preserve existing databases and orphaned sidecars, and keep the scope lock through cleanup.

- SQLite URI construction must preserve escaped local paths and represent Windows drive letters in the absolute path, never the URI authority.

- `device pair-local` is explicit same-owner-scope bootstrap for a separate paired desktop client. It cannot start a server or use remote/stdin authority; persist the original grant request under a private exclusive lock, preserve existing/revoked/damaged identities, and validate endpoint/server/version before pairing or reuse. `device inspect` and `worker inspect` validate native private storage offline and return only non-secret metadata, never token material.

- Retained Claude Write/Edit results preserve exact create/update, original text, replacement flags and structured hunk metadata only through independent digests and original main-history ownership. Keep tool families distinct, require exact nested keys and complete non-null scalars, and reject user-modified proposals or auxiliary result forms until separately proved. Checkpoint restoration must never open a result path, reapply a patch, overwrite current workspace state or claim native undo/backup authority.

- Original OpenCode native checkpoint retention requires that same API's successfully joined completed/stopped-history cleanup and unchanged verified configuration. Keep private canonical evidence bounded to 8 MiB, pin original process/creation/input/history identities and effective-setting/credential digests, and preserve explicit Resume after Stop or failure. Inventory the complete closed runtime, including SQLite WAL and artifacts, under the independently owned root with 8,192-entry/256 MiB bounds; reject linked, foreign-owned, shared-writable, nonregular or changed files without truncation. Windows descendants retain strict private ACLs. Recheck every captured identity after reading, preserve original bytes across caller copies, and never repin an observed failure or changed runtime. Read-only file comparison cannot grant Worker report acceptance, replacement-process authority or continuation; fresh native launch still requires an empty runtime.

- OpenCode inline Read/Shell restoration requires positive original closed-observer evidence, exact ordered tool part identities/digests across the full lineage, completed uncompacted inline results and independently captured interaction eligibility. Only confirmed one-time permissions and the separately verified Read remembered-allowance profile may accompany those tools. The original version-1 Read profile requires an empty loaded-instruction list; completed loaded-instruction history uses the separate version-10 profile below. Shell must observe a non-null exit without an output-file reference. Attachments, interruption, clipping, provider execution and unsupported tool state cannot gain replacement authority. Native v1 remembered permissions are process-local; copied SQLite alone cannot restore an `always` decision. Preserve legacy files/report versions, revalidate the full native history after replacement, and carry predecessor tool evidence through subsequent text inputs without replaying any tool.

- OpenCode checkpoint tool-proof version 2 may retain independently confirmed native `once` permission claims, never remembered allowances. Require original HTTP and native acceptance, exact once-only response bytes/claim/receipt, original arrival/input/tool ownership, closed noncanceled/nonrejected request and completed supported inline tool. Keep version-1 interaction-free proof readable, preserve ordered original history and canonical bounded permission identities through later inputs, and reject response-ID reuse as new input/resume/process ownership. Report recovery and process replacement cannot send an old reply, rewrite original product acceptance, restore a pending request from history, or infer `always` state from SQLite.

- OpenCode tool-proof version 6 adds original completed inline Glob/Grep and TodoWrite histories. Preserve native inline search truncation without inventing an output artifact. TodoWrite input/result lists must match exactly, with explicit unclipped metadata and original `todo.updated` evidence. Retain each input’s latest Todo event/digest independently of tool ownership; compare the native session list before original closure and after replacement through one temporary read-only route. Copy only the existing verified SQLite/WAL state, preserve ordered/custom Todo fields and explicit clears, and never replay a search or list update. Prior observations remain immutable, absent legacy Todo state cannot be reconstructed, and the bounded permission/Question profiles remain independent.

- OpenCode private single-root Git replacement requires positive snapshot-proof version 1, independent of tool eligibility. Bind every original step/snapshot/patch reference and full prior lineage, preserve the exact native index/config/HEAD, and export all referenced trees plus the original index tree into a self-contained pack. After native cleanup, keep the workspace lease through separately journaled, credential-free, bounded Git export children under the original job; join every child before pinning the final complete runtime inventory and closing the lease. Validate the closed native config key family and exact worktree before Git reads; disable hooks, ambient config, replacement objects, lazy fetch and network protocols. Remove alternates only from the new private archive, verify the pack, full object/index closure and tree types, and never change original native files or workspace contents. A once-failed capture cannot be repinned. Read-only inspection never starts Git or accesses external object stores. A durably claimed replacement stages only the exact retained self-contained archive at the pinned native project/worktree hash path, alongside exact SQLite/WAL/SHM; it never runs checkout/reset/revert, adopts an old credential or replays tools. Public single-repository Worktree and authenticated Local continuation/recovery additionally retain the original prepared Git ownership, primary path, immutable route/account and independent workspace lease checks. Multiple roots additionally require the native local-reference profile; Windows non-VCS identity and unsupported history remain gated. Recovery never exports a new pack or silently resumes input.

- Claude streaming Stop retains either an original aborted assistant snapshot or the separately observed block/message stream closures followed by native retry cancellation. Preserve this closed evidence kind, original partial text, distinct native envelope IDs, bounded exact retry counters/status/error enum, native interruption context and uncorrelated session-result usage. Neither provisional stream closure nor retry status proves completion or grants another provider request. Require current cancellation, exact original input/message ownership, no unfinished content/interactions or unconfirmed replies and atomic partial-message/terminal publication. Complete storage finality retains an interrupted block, never an invented provider message-stop. Keep native process cleanup separate from the workspace report, retain prior recovery and leave v1 completion paused. Pending tools, unsupported content and completion races preserve recovery until separately reconciled.

- Historical schemas in `testdata/schema/` are fixed input evidence. Never regenerate older fixtures for a new version. Seeded records may be copied into a fixed fixture through the test-only common-column helper; production never guesses unknown layouts this way.

- `migration-reservations.json` allocates pending work separately from executable migrations. Record changes to reservations in the owning feature PR. Version 25 from an unmerged branch is not proof of schema identity; preserve unidentified data and return recovery-required.

- Permanent session deletion follows `cmds-delidev-storage-contract.md`: synchronize irrevocable metadata-only intent outside SQLite before pause/cancellation, retain original actor/request/assignment ownership and non-content tombstones, and reconstruct obligations before serving restored state. Purge only after every original Worker and both forwarding peers confirm cleanup; remove all containing managed backups, redact retained receipts, and keep offline/uncertain cleanup pending without reclaimed-byte claims.

- Reapplying permanent deletion intent must preserve existing paused session revisions and events when no new transition is needed.

- Exact acknowledged Worker deletion-report retries with a matching actor/work-bound SQL receipt are read-only. Preserve original metadata and reject conflicting receipts; reconstruct a missing SQL receipt through the original synchronized-intent recovery path.

- Permanent deletion includes each original claimed workspace-storage snapshot reservation, even before output exists. Persist those typed UUIDs in the synchronized immutable deletion plan; no database migration or inferred native completion is required.

- Manual fix completion retains exact canonical handled versions after original assignment, native, cleanup and verified-push proof. Activity retirement removes only its additional snapshot; an outcome alone cannot establish handling.

- Manual push proof ordering uses original server-observed assignment/report and cleanup barriers. Worker wall time is metadata only; clock skew cannot override server handling timestamps or strand a matching verified push.

- Managed database restore follows `cmds-delidev-storage-contract.md`: exact inspected image/live revision and actor-bound external receipts, exclusive settled ownership, immutable current safety/deletion authority, paused/quarantined historical work, and pre-open journal recovery. Receipt reads require the exact original actor plus current authorization, including after rollback and restart. Preserve typed publication-versus-startup outcomes; uncertain retries never republish or revive native claims.

- Managed restore refuses pending, leased or recovery-required subscription ownership before replacing SQLite. Restored subscription references remain disconnected and recovery-required because the external vault is not restored; preserve historical generation/actor/lease evidence without redistributing it. Clear an allocated subscription state with no generation, identity, pending operation, lease or recovery fence; settled logout or failed login owns no external reference to quarantine. Follow `cmds-delidev-storage-contract.md` and `cmds-delidev-subscription-contract.md`.

- Workspace-storage recovery has a dedicated 4 MiB job entity bound, with strict 3 MiB input and individually bounded original request validation. Ordinary storage jobs retain 1 MiB entities. Apply the same typed rule on write, original assignment reads, settlement, replay and permanent deletion without truncating ownership.

- Migration 30 seeds only the explicit 26 added hosted IDs after real 29. Freeze historical seeds to their original six IDs; preserve existing managed UUID/Off, custom identities, original preset deletions and no-reseed startup. Validate the exact private layout marker and full-registry inventory bound with overflow/unknown/duplicate rejection. Follow the provider activation and storage contracts.

- Exchange code/verifier and returned printable-ASCII keys remain owned, zeroizable byte buffers through JSON encoding/decoding. Transport cancellation after a valid key is returned does not discard it: settle under independent bounded original-actor authority, honoring serialized business cancellation before sealing. Expired awaiting attempts with no claimed credential cleanup do not block managed restore; exchanging/saving/recovery and cleanup obligations remain blocking.

- Parent permanent deletion capacity counts only newly created dependent journals; retained child journals preserve their exact operation/request identities without consuming a second slot.

- Exact source/native-ID prices use immutable retained versions and independent typed Automatic/Manual policy metadata. Recheck policy inside the observation/refresh transaction; Manual wins. Successful unsupported/no-match refreshes clear only the Automatic active pointer. Never rewrite earlier usage, estimates or pricing. Network reads remain outside SQL; first retention can use only the bounded joined cache snapshot. Provider deletion atomically retains original non-secret source metadata in the existing retired configuration table for read-only pricing/history; live Get never adopts it and refresh may not change retired prices.

- Permanent session deletion assembly and readers share `domain.MaxSessionDeletionJobs` for original jobs and distinct Worker obligations. Validate device uniqueness across all 4,096 owners without the unrelated 1,000-link helper. Preserve the 4 MiB serialized plan limit, immutable per-owner work and exact acknowledgements; no omitted owners or allocation/migration.

## cmds/delidev-cli/internal/worker constraints

- Permanent session deletion follows `cmds-delidev-storage-contract.md`: retain metadata-only original assignment/report proofs and native admission tombstones, hold outer job ownership through final journal/report publication on both lanes, then join publisher/workspace/process owners before removing managed copies. Original Local checkouts and shared account profiles never enter removal paths; missing/foreign/offline evidence remains pending and cleanup retry cannot replay native work.

- Reusing a completed session deletion proof requires absence of the full removal inventory, including job/session process records and recovery locks plus bounded matching title runtimes. Preserve restored replacements as pending.

- Worker-local workspace snapshots follow cmds-delidev-storage-contract.md and cmds-delidev-workspace-contract.md. Verify every repository and an independent Git closure before whole-root removal, preserve sources on failure, publish restoration atomically into an unoccupied owned destination, and retain exact interrupted-operation ownership for explicit recovery.

- Permanent deletion removes snapshot/staging/removal/retirement/restore artifacts only from its immutable storage job and snapshot UUIDs after joining original job and workspace owners. Complete-proof retry must verify every such path remains absent; uncertain storage journals cannot hide reserved copies.

- Workspace read and WatchWork lanes retain independent execution ownership but share the workspace Manager's per-session observation/storage gate through anchored read and native child cleanup. Session-bound version-2 read indexes participate in permanent deletion and completed-proof absence checks. Preserve unknown or legacy unassigned owners as recovery-required, without native replay.

- Snapshot staging removal requires the workspace owner's original operation-bound native-directory claim. Join job owners before validated cleanup, include staging claims in permanent-deletion absence inventories, and verify staging remains absent before generic copy cleanup; preserve unknown or reappearing staging. Storage removal namespaces additionally require the workspace owner's original intent, version-2 claim and pinned journal/inventory reconciliation under the existing gates. Generic Worker copy cleanup checks only absence of staging/removal namespaces and never traverses a replacement; retain needed removal proof until that check succeeds.

- Acknowledged failed/canceled storage recovery discards only its pending report receipt after exact result/journal validation, including original report replay after response loss. Preserve predecessor removal intent and keep reported recovery failures from blocking reconnect.

- Lost storage-report replies that replay an acknowledged uncertain job retire only the pending report receipt and mark the original journal reported. Validate exact assignment/input/report identities and recovery-required empty output; retain original removal intents/claims for explicit recovery, as for immediate acknowledgement.

- Large workspace-storage recovery uses its owning strict request/job decoders through dispatch, native scope checks, report acknowledgement, pending-receipt retry and retirement. Keep original IDs, instance/revision and byte digests unchanged; the 8 MiB Connect response allowance covers bounded JSON/base64 envelopes and grants no broader job or native authority.

- Permanent session deletion inventories original target-attributed storage atomic-write remnants before removal and completed-proof replay. A new or replaced remnant invalidates a completed proof without granting fresh deletion authority. Preserve unrelated owners and block on unattributed legacy files or namespace overflow; never decode partial contents as absence.

- Permanent deletion delegates final storage-root transitions to the workspace owner before generic copy cleanup. The workspace owner retires only a validated final-root claim after its durable removed state; legacy intent/journal retirement remains separate. Include both `workspace-removal-roots` and Darwin's `workspace-removal-quarantine`, plus `storage-removal-root-claims`, in original and completed-proof inventories; a reappearing final root or canonical claim is absence-only and never receives generic removal authority. Preserve original intent/native identity, post-unlink receipts, report acknowledgement and structured redacted diagnostics under the storage contract.

- Selected skill snapshot deletion must use the original private preparation intent after native owners join. Validate original machine/bindings/root/resources, observe absence, and retain the compact tombstone; generic session copy deletion cannot adopt skill snapshot roots. Include original skill paths in completed absence inventories even without workspace copies or Fork ownership; those plans grant no execution/workspace path authority.

## cmds/delidev-cli/internal/workernetwork constraints

- Same-generation import reconciliation retries obsolete protected-derivative enumeration/deletion after current cache publication; retain the committed current reference through cleanup failure. Transferred bundle issuance allows at most 30 seconds of clock skew between hosts while enforcing the original absolute expiry and five-minute lifetime.

## cmds/delidev-cli/internal/workspace constraints

- Worker-local snapshots and cleanup follow cmds-delidev-storage-contract.md. Preserve ordered all-repository manifests, commits/unpushed history, index/worktree state, ignored/untracked regular files, modes and symlinks without dereferencing links. Reject unsupported special files, external Git object dependencies and undeclared nested Git administration, including directories and filesystem case aliases.

- Cleanup/deletion recovery requires the original synchronized removal intent plus a matching verified namespace claim when both namespace names are absent. Persist the claim after inventory validation and before unlinking; an intent alone or filesystem absence is never verified removal. Version-2 claims also pin the native root identity; legacy claims cannot authorize new unlink. Remove only pinned inventory entries, verify their bytes/link/mode and anchored identity immediately before unlink, and retain changed/new entries for recovery. Directory removal must fail on remaining unknown contents. Retire both records only through the acknowledged-report boundary.

- Snapshot deletion reserves two inventory entries for its workspace/manifest wrappers beyond the 8,192-entry workspace bound; reject unexpected snapshot-root content.

- Permanent deletion validates reserved snapshots against the original session/machine/preparation and captures the stored workspace manifest before removal. Restored independent Git stays within its managed root; never run its removal against an original Local/source checkout. Permanent deletion reconciles present original removal namespaces under the session/observation/snapshot gates through their immutable job/session/snapshot-bound intent, version-2 claim, exact intent digest, native root identity and partial-removal journal. Require preexisting proof; never recapture ownership, replay native input or retire proof before validated namespace absence. Foreign roots and changed/new entries remain recovery-required.

- Workspace file/diff/private PR observations and preparation/recovery/storage/permanent deletion share a separate cross-process per-session gate through anchored handles and read-child cleanup. Keep execution leases independent so views remain usable during native runs. New read process indexes bind the original session in the version-2 namespace before launch; reconcile them before destructive work. Unknown/legacy unassigned ownership stays recovery-required. Include the session-bound namespace in deletion absence checks and reject new observations behind its deletion tombstone.

- Snapshot creation promotes its original staging claim with the exact manifest digest only after successful synchronized no-replace publication and post-rename content/native-root verification. Inspection, recovery and deletion require that original proof; matching manifests or foreign copied bytes cannot reconstruct it. Creation recovery additionally compares the complete original request digest. Missing proof retains the snapshot, source and recovery ownership.

- Recovery retains the inspected snapshot logical source count. Persist that exact count in the original claim-bound removal intent before snapshot deletion; absence cannot reconstruct it from physical metadata size. Unpublished partial restore scratch cleanup uses the original external operation claim and native root identity, without requiring completed snapshot equality. Foreign or missing proof remains protected.

- Storage recovery owns a finite 3 MiB request/4 MiB job exception until original preparation/manifest evidence can be represented by immutable reference. Strict decoding preserves unknown/duplicate/trailing/UTF-8 rejection and the original request 1 MiB bound. The allowance never bypasses contextual native ownership verification or expands ordinary storage inputs.

- Storage atomic publications in shared directories use immutable-target `.pending-<UUID>.json-<suffix>` names; claim-journal compaction uses `.pending-<UUID>.pending-<suffix>`. Permanent deletion and completed-proof replay share a bounded 65,536-entry-per-directory remnant inventory. Preserve unrelated owners; unknown legacy/malformed or non-private remnants block completion. Keep the shared security writer exception scoped to storage. Follow `cmds-delidev-storage-contract.md`.

- Published storage recovery uses the original snapshot publication claim and captured managed-directory identities with pinned source bytes/digest. Later mutable copy eligibility or external Git availability cannot revoke that completed copy; a live Cleanup source settles as preserved/failed. Replacement directories remain uncertain. Legacy snapshots retain their previous identity checks. Windows external Git stores on a different volume count toward the shared observation budget.

- Final storage-root removal uses the original intent/root identity and a durable bounded final-root transition before no-replace claiming `workspace-removal-roots/<operation>-<private-UUID>`. Verify its empty inventory, native identity and exact mode through anchored handles before unlink, and repeat the identity check after the final mutation checkpoint. On Darwin, transfer the verified writable root with an exclusive directory-fd rename into the fresh operation-private `workspace-removal-quarantine` namespace, recheck identity, then remove permissions and unlink only that quarantine name; the old private namespace remains a recovery boundary. On Linux, also require the opened original directory's link count to reach zero after `unlinkat`. Preserve replacements at the old removal name and block completion; a retained-parent race remains recovery-required without a post-unlink receipt. Restore and synchronize the verified 0700 mode on every pre-unlink failure after the permission barrier. After native unlink, persist that receipt with a bounded cancellation-independent context before honoring caller cancellation. Missing roots need the original post-unlink receipt plus synchronized, independently checked absence; unlink preparation alone grants no completion. Recovery and permanent deletion resume only this transition. The workspace owner retires only the validated final-root proof before generic session copy cleanup; legacy intent/journal retirement remains separate, and reappearing canonical claims are absence-only. Retain both namespaces/proof/remnants in deletion inventories. Follow the storage contract.

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

## Worker-local workspace storage

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
Follow the [automatic coordinator contract](cmds-delidev-integrations-contract.md#bounded-automatic-pr-remediation).

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

Dependency-blocked workspace cleanup/recovery stays durably queued. Primary Worker
pagination retains a bounded earliest skipped predecessor cursor and rescans it
on completion, store wake or heartbeat, while later independent jobs and targeted
controls remain available. Recheck the same dependent-retirement gate in the
original atomic claim; preserve one outstanding assignment and original job IDs.

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

The feature uses immutable `project_prompt_history` entities with the exact project ID and an empty session ID. Append/prune and confirmed clear use ordinary durable receipt transactions. Project deletion removes live history atomically. Session deletion and first-input editing do not own these records. No SQLite migration is added.

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

## Reviewer accounting without a migration
Optional closed reviewer selection, review progress and builtin/unknown model attribution remain in existing immutable JSON documents. Original API response-reference rows use an empty private model slot only for a proved bounded builtin reviewer scope; root/child scopes retain catalog IDs and cannot adopt those references. Response usage preserves original native digest deduplication and independent pricing snapshots. Reviewer/unknown records remain unpriced and cannot borrow a root model, retroactively rewrite historical attribution or fabricate billing evidence. No relational layout or migration allocation changes. Follow the harness, proxy and usage contracts.

## Same-question Sidechat retry

Sidechat retry adds strict JSON `sidechat_retries`, `sidechat_current_answer` and `sidechat_active_retry` in existing session records, a generated-input generation marker, and closed retry references in Fork/execution/deletion inputs. No SQLite migration is introduced. Freeze original native Fork checkpoint digests and every child-owned retry job/runtime in the existing deletion plan, including failed, unpublished and uncertain work. Retry Forks reuse the original child reference and grant no new unpublished-child metadata owner. Reconcile original journals and native processes before runtime removal; compare exact checkpoint policy/digests and retain replacement or missing proof as pending. Parent dependency retirement still joins every child obligation and preserves independent Forks. Admission dry-runs the parent/dependent outer plan with bounded headroom and checks the existing copy limit before queuing native work. Follow [same-question retry](cmds-delidev-sidechat-contract.md#same-question-retry).

## Situation notification metadata ownership
The existing private `metadata` table owns three closed version-1 envelopes without DDL: `notification-situations-v1:<client>` couples twelve values and six operational enable checkpoints to the original `notification_preferences` revision; `notification-delivery-v1:<inbox>:<client>` reserves new typed question/approval/operational deliveries; `notification-worker-baseline-v1:<machine>` retains one original device/instance/server-epoch lease baseline. Each value is at most 8 KiB and rejects unknown fields or inconsistent versions/identities. Legacy delivery CHECK values and original receipts remain unchanged. New kinds never masquerade as legacy kinds. Claims have no TTL, eviction or terminal-result retry; retained Inbox/source ownership controls their lifetime. The client scope is the paired UUID or the existing owner scope, never a caller-selected device.

Source publication, original-source eligibility, preference checkpoints and claims use the same SQLite transaction as their existing resource/preference writes. Initial opt-in preserves the old public revision. Re-enable checkpoints capture the durable `sqlite_sequence` event high-water mark, including compacted history. Candidate reads page retained metadata under the existing read deadline; ineligible earlier sources cannot hide later eligible records. Deleting a machine/account/occurrence removes its operational Inbox entries and their typed reservations. Ordinary Inbox deletion removes typed reservations in the same transaction. Machine deletion removes its baseline. Revocation suppresses current candidates/claims and clears future observation authority; it never creates a transition.

Restore overlays the current granular preference rows together with their typed metadata, retains current operational records only while their original source is retained, preserves current delivery reservations and removes orphan reservations. Existing receipt quarantine and revocations remain authoritative. Restore advances every operational enable checkpoint past both durable event timelines. Worker leases and notification baselines are cleared, so restored state establishes a fresh baseline rather than an edge. The Inbox pagination epoch also observes the restore event floor. These transformations share the existing atomic restore image publication; they cannot replay an old display grant or restore a stale preference generation. No migration allocation is consumed; real reserved migration 32 remains outside this feature.

The native connection lane separately owns a protected per-original-connection preference cache and at most 256 retained original scopes, each with 10,000 immutable local edge reservations. Its key hashes length-delimited original server/client/endpoint/runtime/profile/credential identity; raw connection material is not stored in filenames, documents or logs. The native ledger never deletes reservations to retry or evade its bound. A full or unavailable ledger suppresses new presentation and retains the original Inbox-free uncertainty. Its owner-only directory and atomic preference replacement belong to the existing trusted native storage root. Native connection events never enter server delivery tables.
## Atomic turn timing retention

Existing Session and Message JSON retains optional server-owned accepted/terminal UTC observations with no schema or migration. The existing mutation clock is captured once after receipt lookup; exact retries do not recapture time. Primary acceptance, and later matching terminal publication, share their original receipts and resource/event transaction. Terminal retention selects bounded primary-user records through the existing execution index, checks original session/execution/input/native thread/turn and accepted observation, then updates only timing with their original content and index ownership intact. Any mismatch rolls back all Message, progress, outcome, Inbox and receipt writes. Legacy omissions are never backfilled. Backups preserve retained observations; deletion uses original resource ownership. Native assignment/checkpoint/digest projections omit display timing.

### Automatic reset-credit state
Standing consent, bounded original-turn fences, exhaustion episode and original
observation operation share protected account JSON and the existing mutation
receipt transaction. No migration is added. Managed restore explicitly clears
consent while retaining required original credential/episode/operation references
under recovery quarantine; retained references grant only independent cleanup.
Portable transfer removes the entire subscription state. Generic configuration
writes must preserve server-owned fields, including omitted legacy consent and
episode fields; old clients cannot erase or manufacture spending authority.

### Current inline source layout

The protocol-2 current declaration is the complete Model-free baseline 32. It
retains all activated tables, FTS indexes, native ownership, protected cleanup,
subscription/API profiles, notifications and session-default fields, and removes
persistent Model entities, indexes and suppressions. Internal source keys encode
exact identities only as storage indexes; they are never resource UUIDs or wire
`model_id` values. Current portable bundle 4 remaps embedded Provider references
and selected Accounts while preserving all newer current configuration fields.
Earlier DBs and portable bundles are unsupported without conversion. Startup and
inspection reject earlier DBs before WAL or other writes; this does not authorize
removing native or protected credential state.

The current runtime does not execute historical migrations 001–031 or upgrade
private restore candidates. Frozen historical SQL remains test provenance only;
every earlier original database or backup is rejected with its bytes and sidecars
unchanged. A valid schema-32 restore validates and publishes its immutable current
image. Existing retained migration-copy cleanup journals keep their original
fingerprint, independent recovery and deletion authority: retirement of upgrade
code neither adopts a changed copy nor removes uncertain original state. Current
restore retains protected references, revocations, subscription quarantine and
independent native cleanup. Persistent Model search, suppression, validation and
deletion entry points return Unsupported; ephemeral decoding of an exact internal
source key grants no registry, account or native authority.

Current protocol-2 backup creation uses only `RequestBackup` and its original
actor/server-bound job. Historical `CreateBackup` declarations retain allocation
provenance but return Unsupported without a receipt, backup file or current job.
Current inventory/restore fixtures explicitly publish retained durable jobs;
retirement does not change independent backup deletion or restore obligations.

## Private waiting order and Fork image snapshots
`session-queue-order:<session-id>` is version-1 private metadata, bounded to 1,000 unique original input IDs and 64 KiB. Missing metadata means acceptance order and generation zero. Generation-only records preserve that order until the first genuine move captures IDs. Captured IDs must match the entire currently waiting set; duplicates, foreign/missing IDs, malformed versions or overflow require RecoveryRequired before dispatch. All membership writers update this metadata atomically with their queue records; content-only edits retain it. No public Queue document rank and no SQLite migration is added.

New Fork jobs retain a bounded version-1 `fork-image-snapshot:<job-id>` with exact original source, child, execution, native turn, job-input digest and ordered image references. Empty references are explicit. The private cutover marker distinguishes legacy jobs from newly admitted jobs; uncertain missing snapshots fail closed. The existing protected image records remain the byte/Worker authority. Durable session purge removes its order metadata and retires Fork snapshots only after both original job and dependent child are gone. Backup images preserve their own private order and Fork snapshots. Restore excludes these owners from the current safety-metadata overlay, validates the restored membership and advances order generation beyond both timelines to expire old waiting cursors. It does not synthesize rank, membership or image authority. Do not copy raw image bytes or native transcript into this metadata.
