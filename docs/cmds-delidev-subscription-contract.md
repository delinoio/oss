# DeliDev native subscriptions

## Shared account owner and execution members

A managed subscription account runs independent sessions concurrently through a
durable account owner plus execution members instead of account-wide execution
exclusivity. The managed Codex lane is negotiated by Worker
`SHARED_SUBSCRIPTION_OWNER_V1 = 67` and the device-owned native Claude lane by
Worker `CLAUDE_DEVICE_OWNED_MEMBERS_V1 = 68` (both declared in the
[protocol contract](protos-delidev-v1-contract.md)). The Worker advertises 67
only after a server echo, verified managed Codex installation and parallel-session
negotiation. An older server or Worker retains exclusive Codex execution. Where a lane is negotiated, independent sessions that
use one account on its original Runner Device run concurrently, including more
than four, with no session-count cap, setting or queue depth. Account lifecycle
authority stays exclusive.

**Representation.** Account JSON gains an additive, versioned extension. A lease
with `shared = true` is the account owner and `members` holds one record per
execution member (lease identifier, session job, admission revision, `running` or
`retained`, admission time). The owner is bound to the original account and
connection, Runner, device, Worker instance, server epoch and credential
generation. Exactly one member owns each session job; the founding member shares
the owner's lease identifier, job and revision, so that one identifier never
resolves to two different jobs; no other member may carry the owner's own job.
Historical accounts omit both fields and keep the
singleton lease and its original receipts unchanged. A singleton lease never
carries members, and a shared lease never carries a Fork or title revision.
Validation rejects duplicate jobs or lease identifiers, an unknown member state,
a non-execute owner, a founding member whose job or revision differs from the
owner's, a later member that carries the owner's job, and members without an
owner. There is no SQLite migration:
the extension is account document JSON.

**Negotiation.** Only a Worker whose current attach advertised capability 67 (and
therefore observed the server's echo) can found or join a shared owner. Every
other peer keeps one exclusive singleton lease and receives the historical
`ResourceExhausted` refusal behind any live lease. A downgraded or replaced
instance cannot join an owner bound to another instance or device.

**Admission.** The first negotiated execute Take on an idle account founds the
owner and receives the one protected bundle. A later execute Take on the live
owner from the same Runner, device, instance, epoch and generation joins as a
member and receives no authentication: the response carries `owner_lease_id`
and an empty bundle, so DeliDev never distributes a second copy of a refresh
token. All existing job, assignment, cancellation, Stop, recovery-fence, budget
and account-state checks apply per member. Pending login, reauthentication,
refresh or logout, a native observation, a recovery fence, a draining owner (no
runnable member) or an exclusive server operation refuses new members. A joined
member whose admission fails after commit is released alone with the typed
`subscription_member_not_admitted` result and never fences siblings. Fork,
compaction, native review, managed title, quota and reset-credit work keep
exclusive singleton custody and wait while a shared owner is live, so their
receipts and generation proofs keep their original meaning.

**Finish.** `FinishSubscription` carries an explicit `finish_scope`. A `MEMBER`
Finish releases exactly one running member, carries no authentication, never
reads or writes the vault, never rotates the generation and never touches a
sibling. Confirmed cleanup removes the member. Unconfirmed native or descendant
closure (for example a Stop whose `thread/closed` or descendant proof is
missing) marks the member `retained`: it stops being runnable and holds no
execution authority, but its cleanup ownership and workspace stay with the owner
until final owner containment closes. Retention is not a completed Stop. An
`OWNER` Finish retires the owner only when no other member is runnable; it
performs the single generation-specific writeback, releases retained members with
the owner and clears the lease. Only `UNSPECIFIED` keeps the legacy singleton meaning; any other unrecognized
scope value is rejected with `InvalidArgument` before the Finish is processed. A legacy-scope Finish cannot settle a shared
owner and a scoped Finish cannot settle a singleton. A failed or unconfirmed
owner Finish keeps the existing recovery fence, so credentials remain until
confirmed settlement.

The Worker never lets a lost Finish acknowledgment leave its local owner and the
server's owner/member set divergent. A `MEMBER` or `OWNER` Finish whose outcome
is unknown (a transport failure) is retried a bounded number of times with the
identical idempotent request (same request identifier and revision). A member
Finish that stays unacknowledged is kept as a pending member Finish: the last
local member settles every pending member Finish over a fresh transport before
it closes the profile and sends the `OWNER` Finish, which the server would
otherwise refuse as conflicting while it still counts that member as runnable.
An unfinished retirement keeps the owner and records a resume step; the
admission gate completes it (releasing the newly admitted member alone and
reporting a definite no-delivery failure) before any later member can join, so no
member runs on a retiring owner. A founding failure whose owner Finish stays
unacknowledged is tracked the same way. Structured logs
(`claude_finish_retrying`, `claude_shared_member_finish_pending`,
`claude_shared_member_finish_settled`, `claude_shared_owner_retirement_blocked`,
`claude_shared_retirement_resumed`) carry account, lease and counts only.

**Lifecycle.** Login, reauthentication and explicit refresh close new member
admission when accepted and take lifecycle authority only after the owner and
every member (including retained cleanup) have drained, because the lifecycle
Take is refused while any lease exists. The device-owned Claude lane accepts a
refresh behind a live shared Execute owner as the pending operation, so a user can
queue reauthentication to close admission and drain the owner; the queued
operation grants no lease, and the owner's identifier cannot read or advance its
native progress or login code. Any other lease holder keeps the refresh request
refused. An operation accepted while a lease is held is marked
`queued_behind_lease`, and a queued logout is always deferred: its 15-minute
native operation window starts when the lifecycle Take claims it (the Take
rebases `started_at`/`expires_at`), never while it waits, and progress does not
report it expired while queued. The Worker bounds its own 15-minute run after
the grant, so a long-running shared owner cannot expire the wait. An unclaimed
queued login or refresh that is not deferred and whose window elapsed is retired
as `expired` (like a queued cancellation) by the next request or the next
Execute admission, so an expired `pending` never blocks admission forever.
Logout keeps immediate readiness
revocation, cancels every account execution, joins every member's ownership and
only then runs the canonical native logout and credential deletion.

**Loss and recovery.** Loss of the shared owner (Worker stream, instance or
epoch) fences the account once, which fences every member: no member Finish,
admission, observation or credential write is accepted, no other process adopts
the owner and no input replays. Doctor, account deletion, database restore and
recovery treat any live lease, including a shared owner and its members, as
unsettled and cannot clear or reuse its credentials; a restored image retains the
owner/member set as quarantined evidence under a recovery fence. Worker recovery
of a lost shared owner joins each member's process owner (a missing directory is
accepted only for a member whose durable journal records no started execution),
then always logs out the canonical profile and records that cleanup in an
owner-level durable claim; it never infers either step from the founding
member's per-session cleanup or identity flags. Structured logs
(`subscription_lease_granted`, `subscription_member_finished`,
`subscription_member_released_undelivered`, `subscription_shared_owner_lost`)
carry account, lease, operation and counts only, never authentication, prompts or
native content.

**Device-owned Claude accounts (Worker `CLAUDE_DEVICE_OWNED_MEMBERS_V1 = 68`).**
The native Claude lane uses the same owner/member representation with its own
negotiation. The server admits a negotiated execute Take as the founding owner or
a joined member exactly as above, with no bundle for any member (this lane never
delivers authentication bytes), and uses `MEMBER` and `OWNER` Finish scopes. An
owner Finish carries the native identity commitment and confirmed cleanup that the
singleton Finish carried; a member Finish carries neither and cannot confirm an
identity. A fenced owner is cleaned only as one set through an owner-scope
cleanup Finish.

On the Worker, one account owner per connection holds the canonical profile lock
for its whole lifetime, replacing the per-execution lock with account lifecycle
exclusion: login, reauthentication and logout take the profile only after the
server owner has drained and the Worker released the lock. Each session is a
member with its own lease journal, process owner, workspace lease and publication
journal. Admission and owner retirement are serialized per account by a gate that
is never held across a busy wait. The last runnable member joins every retained
member's process owner, reads the native identity through the CLI's own status,
releases the lock and only then reports the owner Finish; an unproven closure
reports unconfirmed cleanup, which fences the account. An owner whose last member finished but whose owner Finish was never acknowledged is replaced when the server grants a fresh founding lease, which only an account with no lease receives; an owner that still has runnable members is never replaced and fences instead. Native code alone reads
and renews the authentication in the canonical secure-storage directory under its
own locks and compare-and-swap rereads; DeliDev never reads, copies or
distributes those bytes. These are mock and fixture results for pinned Claude
Code `2.1.236`, not real simultaneous renewal, owner-crash or cross-platform
acceptance.

_Profile versions._ Version 1 keeps one canonical directory as native
configuration, history and secure storage. A retained version 1 history keeps its
exact checkpoint bytes and digest and continues only in the canonical directory,
one session at a time per account on the Worker. Version 2 gives every session a
private configuration and history directory (`CLAUDE_CONFIG_DIR`) and names the
canonical profile only as the secure-storage namespace
(`CLAUDE_SECURESTORAGE_CONFIG_DIR`). Its checkpoint digest binds the private
history directory and the canonical secure-storage directory independently, and
the Worker checkpoint records `profile_version = 2` (omitted for version 1, so
version 1 bytes are unchanged). A fresh session of a negotiated Worker is
version 2; a continuation keeps the version of its predecessor and binds the
previous session's private history profile, resolved through the native profile
registry. A foreign owner, history directory or
digest fails closed.

**Codex Worker activation.** A negotiated managed Codex owner launches one
account-scoped app-server in a private `CODEX_HOME`; each execution owns a
separate native thread, event route, publication journal and workspace lease.
Only the owner captures refreshed authentication and reports an `OWNER` Finish.
Earlier members report `MEMBER` Finish without authentication. A member releases
its workspace only after its exact `thread/unsubscribe` and `thread/closed`,
native descendant inventory and its own process-index reconciliation succeed.
Missing proof retains the member workspace until the owner process and its
descendants are contained. The owner reconciles its original process index before
authentication removal and retained workspace cleanup. Unknown Finish outcomes
retain their original request identifiers and block admission until settlement.
Owner cleanup streams the complete accumulated account history with bounded
memory to detect raw and encoded credential remnants. It does not apply the
single-execution file-count or byte limits to that retained shared history;
the existing singleton cleanup limits remain in force.
Native owner loss fences every member and never replays input. Fresh checkpoints
bind an account home and exact native thread-history digest; version 1 history is
imported only after the original checkpoint and rollout are verified. Invalid or
unreadable history remains recovery-required. Native modes with process-wide
options (including image generation, sidechat, fork, revert and compaction) keep
exclusive singleton execution until their independent shared-profile behavior is
verified. Fixture and build checks do not establish real-account or native-platform
acceptance.

**Scope boundary.** The server-owned Claude setup-token lane (Worker capability
41, `claude_credential = server-token`) is explicitly outside the owner/member
model and stays exclusive: it delivers a static server-held token inside one
protected Take response, which conflicts with the rule that no member receives
authentication bytes. OpenCode Go keeps its independent key-backed relay. Grok
Build is not advertised; its bound credential bundle library and identity
commitment are owned by [the Grok contract](cmds-delidev-grok-subscription-contract.md#grok-bundle)
and grant no execution. The server-owned Grok browser login (LOGIN, `device_code`
false, no machine) runs in the server-owned request, progress, cancel and
callback-forwarding lane described under
[Server-owned browser login](cmds-delidev-grok-subscription-contract.md#server-owned-browser-login),
implemented without a native Grok process and kept unadvertised (System 39 off).
The server-owned Grok device-code login (LOGIN, `device_code` true, no machine)
shares that lane and the single System 39 switch; see
[Server-owned device-code login](cmds-delidev-grok-subscription-contract.md#server-owned-device-code-login).
The server-owned Grok refresh (REFRESH of a connected account, no machine) also
shares that lane, claims its refresh durably under the account's exclusive server
lease and never resends an uncertain refresh token; see
[Server-owned refresh](cmds-delidev-grok-subscription-contract.md#server-owned-refresh).
Grok execution on a subscription account is implemented as an exclusive execute
lease with a Worker-held private-home credential and a confined return protocol
(see [Managed execution](cmds-delidev-grok-subscription-contract.md#managed-execution));
System 40 and Worker 21 stay unadvertised behind default-off switches until the
opt-in real-account run. Mock and build evidence for this model is not real-account,
real-renewal or cross-platform acceptance; that acceptance is future work and is
not performed.

## Direct execution startup

[Direct startup](cmds-delidev-execution-startup-contract.md) removes manual installation inspection and separate execution probes from new negotiated assignments. Initialize the actual selected process and validate its protocol, credential mode and settings before input. Optional observed version metadata cannot grant or deny execution by numeric comparison. Existing protected generation, original account lease, server-owned OAuth and credential write-back/cleanup remain required; readiness never grants login, account refresh or credential ownership.

## Failed subscription cleanup reservations

System `FAILED_SUBSCRIPTION_CLEANUP_V1 = 41` and all batch/status/result declarations were reserved on main by the originating change before activation. System 38 belongs to Claude and 39/40 to Grok. The reservation itself granted no cleanup, login or deletion authority.

One explicit owner/paired-client `CleanupFailedSubscriptions(request_id)` admits a server-owned durable job and freezes every candidate account ID, revision, original initial ChatGPT server LOGIN and deletion request UUID in account child jobs. Receipt replay binds the original actor and server and returns the same current job. Only one batch can be pending per server. Admission scans the complete bounded server inventory; frontend account pages never select work. Failed, canceled, expired, unsupported and interrupted initial logins qualify, including credential-cleanup-confirmed failures. The same explicit batch also selects disconnected subscription Accounts for every supported service, including metadata-only accounts and completed logout accounts. Version-2 private child jobs distinguish original failed LOGIN cleanup from disconnected configuration deletion; version-1 children retain their original LOGIN-only meaning. Disconnected targets have no invented operation ID and must retain no active login, native process, generation, identity commitment, active Worker ownership, lease, recovery, removal or active observation. A completed Worker logout may retain its historical owner-machine routing hint; it grants no active authority without a generation, lease, pending action or recovery, and does not bypass protected-reference checks. Connected accounts, restored generations and independent Worker/native/observation ownership are excluded.

The joined server controller applies the original 30-second account attempt and account gate. It rechecks the original requester, exact account revision and login ownership. It shares the native closure proof and the explicit configuration deletion checks, including the removal of the account's retained credential files and `account-login` generations (see [Server credential retention and explicit removal](#server-credential-retention-and-explicit-removal)), protected references of any other purpose, and complete Agent/Project/retained Session references. Its settlement checkpoint advances the child's expected revision in the same transaction. Changed settings or ownership remain retained. Failed attempts are terminal retained outcomes with closed reasons, never automatic retries. A later deliberate button click can create a fresh batch. An explicit `DeleteConfiguration` can admit one failed initial ChatGPT LOGIN through the same controller under the original deletion request ID. The child freezes that requester, account, confirmed revision, LOGIN and deletion command; cleanup checkpoints advance only its effective revision. The final deletion receipt retains the original public revision and request bytes. Before that receipt exists, mutation and replay use the immutable child under the existing job parent index to reserve the public request ID for that exact deletion command; admission rejects previously used IDs transactionally. The explicit `DeleteConfiguration` response reports `Replayed=false` only for the invocation whose own cleanup run commits the `configuration.delete` receipt and the child's DELETED outcome. Completion ownership is decided by that non-replayed commit under the account gate, never by an earlier pending-child observation or by which invocation admitted the child. Any other invocation reports `Replayed=true` without repeating native or vault effects: a retry that finds the retained child already DELETED, including one whose original cleanup committed the receipt after the retry's initial replay check, a terminal child seen by the runner's child scan or its locked terminal check, and an admitting invocation whose child the background controller completed first (no foreground invocation then reports `Replayed=false`). Response IDs and receipt identity do not change. Accepted single-account work survives client departure; terminal failures require fresh observation and explicit confirmation, and receipt/status reads never retry cleanup.

Confirmed deletion, tombstone/browser obligations, the existing configuration receipt, child result and aggregate counts commit together. Restart reads only original jobs and confirmed cleanup checkpoints, never login or callbacks. An interrupted attempt without a native checkpoint remains retained; confirmed native cleanup may resume protected cleanup. Shutdown cancels and joins the controller before store/vault closure. Revocation permits server-owned retained-result bookkeeping only, never substituted account deletion authority. Restore blocks queued/pending cleanup jobs and quarantines historical nonterminal jobs and receipts, so restored work cannot regain deletion authority.

`GetFailedSubscriptionCleanup(job_id, page_token)` returns current revision/state, total/processed/deleted/retained counts and at most 50 original account results. Signed cursors bind the actor and batch. Result metadata contains only the original alias, account ID, closed outcome/reason and safe problem code. Logs contain job/operation IDs, counts, phases and safe codes. Existing generic jobs/receipts need no migration or Rust/native change.

## Native Claude subscriptions

Live native `rate_limit_event` notifications are bounded, identity-checked
private observations. The Worker consumes them without publishing account quota,
changing account readiness, granting paid-credit or fallback authority, accepting
input or completing a turn. Original native result and cleanup proofs remain
required. This lane does not activate Claude quota or reset-credit support; the
separate, explicitly linked [server-owned Claude usage tracking](#server-owned-claude-usage-tracking)
owns usage limits.

The originating change established System `CLAUDE_SUBSCRIPTIONS_V1 = 38`, Worker
`NATIVE_CLAUDE_SUBSCRIPTIONS_V1 = 20` and the complete protocol allocation
closure on main at `8a698d04d51f9db3a041b909edf85a7811f09ff3` before feature
implementation. The server advertises 38 for the existing SubscriptionService's
Claude branch, which now serves only existing device-owned accounts' execution,
machine-bound logout and operations accepted before the retirement; it never gates new sign-in. An original Worker advertises 20 only after verifying installed
Claude Code at or above the pinned `2.1.236` SemVer baseline through an isolated empty profile and confirmed cleanup.
A reserved number or ordinary installation observation never grants this lane.

Fresh machine-bound Claude login (`LOGIN`) and reauthentication (`REFRESH`) are retired: `requestClaudeSubscription` rejects them, including requests from the CLI and other clients, with `Unsupported` and server-owned sign-in guidance before it creates an operation or changes account state. The rejection runs inside the `Store.Mutate` closure after the request-receipt check, so a request accepted before the retirement still replays through its original receipt, reconciles through progress, cancellation and original Worker cleanup, and admits no new operation. A machine-bound `LOGIN` or `REFRESH` for an existing server-token account is routed to the managed handler first, which returns the same `Unsupported` retirement result instead of a generic invalid-argument error and changes nothing. Machine-bound `LOGOUT` of an existing device-owned account, execution, deletion, progress, cancellation and Take/Finish/recovery paths are unchanged. Claude login for new accounts is the [server-owned setup-token flow](#server-owned-claude-setup-token-accounts) under System 67.

For accounts and operations accepted before the retirement, an owner or authorized paired client selects the original
Runner Device. `claude auth login --claudeai`, `auth status` and
`auth logout` run on that Worker with an account-specific private
`CLAUDE_CONFIG_DIR` and matching secure-storage directory. DeliDev reconstructs
the native environment; personal profiles, imported/external tokens,
Console/API login and API relay authentication are excluded. The original CLI
owns OAuth exchange, credentials and automatic renewal. DeliDev does not read
native authentication files or distribute their bytes to the server or another
machine. Claude quota/credit inspection and redemption remain excluded from this
device-owned lane; the server-owned usage connection is the only Claude usage
source and has its own newly owned profile (see
[server-owned Claude usage tracking](#server-owned-claude-usage-tracking)).

Server-owned optional Account JSON retains an opaque native profile UUID,
original owner machine, keyed identity commitment, authentication generation
and original operation/lease metadata. The Worker computes the commitment from
bounded native identity under its private server-scoped key. The original login
also pins that commitment in its private profile owner record; fresh native
status must match before reauthentication completion or execution input. A
missing pin or changed account retains the ownership fence. Raw identity stays
local. Authentication success requires the original login process to exit,
joined owned descendants and `auth status` proving first-party `claude.ai`
subscription authentication with no API-key source. Reauthentication reuses the
same owned profile and identity, creates a new generation and preserves the
connection ID. A different native identity cannot replace that account.

One account lease serializes login, reauthentication, execution and
logout; negotiated managed Codex execution instead shares the lease as an owner
with concurrent members (see the shared account owner section). Login/reauthentication cannot enter behind another lease. Logout may
queue behind execution after atomically revoking readiness and canceling that
execution. Execution checks the service/model/harness, dedicated capability,
original owner Runner, profile and generation before consuming an input. Wrong
Runner, old Worker and API-authentication selections fail without a fallback.
Existing native permission, model, effort, continuation, history, usage,
publication and Stop checks remain required. Native subscription execution has
no API proxy path and injects no API key or relay token into the CLI.

`SubmitSubscriptionLoginCode` is an owner/client write-only operation bound to
the original actor, account revision, operation and live native login. It
accepts one bounded visible-ASCII readline input only for the native
browser-code method. The server records the original request claim before
retaining bytes in memory. `TakeSubscriptionLoginCode` permits only the original
Worker/device/instance/lease and records consumption before releasing bytes
once. Exact submit receipt replay never replenishes input. Lost memory, a lost
Take response or canceled/expired/revoked ownership never permits another code
submission or native launch. Neither code nor original browser URL enters jobs,
receipts, resources, synchronized state, shared caches or logs.

Progress exposes only the original initiating principal's live native URL,
closed login method and safe diagnostic metadata. URL validation pins the
original CLI's HTTPS host/path, client, PKCE and subscription scopes; DeliDev
never reconstructs or repairs the URL. Native browser presentation binds the
original trusted desktop window, server/lifetime, operation and exact URL.
Explicit reopening uses that same binding. Claude presentation creates no
DeliDev callback listener or code-exchange authority.

Cancellation wins before successful connection publication. A claimed operation
remains fenced until its original process joins and native logout/status plus
owned-profile removal are confirmed. Restart, offline/replaced Worker,
uncertain delivery and failed cleanup retain the original operation/lease and
recovery ownership. Reconnect does not repeat Take, native login or code input.
The original Worker may reconcile its durable original process journal and
report cleanup-only recovery under the retained lease; that path cannot publish
a login success or authorize execution. Closed success receipts remain
immutable even if their response is lost.

Logout runs the official CLI, independently proves disconnected status and then
removes only the canonical, inode-bound owned profile under the existing
2,048-file/64 MiB cleanup bound (this bound belongs to profile removal, not to the execution authentication scan). Oversized, symlinked, replaced or uncertain
profiles retain recovery; they are not reported as removed. Account deletion
requires confirmed native logout/profile cleanup and no retained owner,
generation, lease or operation. Session checkpoints retain a digest-bound
profile reference and independently bounded native history, never authentication
files. Restored ownership metadata is quarantined and cannot adopt another
Worker's credentials. Portable configuration contains no native ownership.
No SQLite migration is added; real migrations 1–31 and reserved baseline 32
remain unchanged.

Fixture, build and browser-layout results do not establish actual account login
or packaged macOS/Windows/Linux acceptance. Each platform requires real original
CLI login, multiple-account isolation, first/follow-up input, Stop/Resume,
logout/deletion and original offline/restart/cleanup evidence before support is
reported complete. Keep the outstanding matrix visible in PR/CI evidence.
Follow the desktop, protocol, harness, storage and structure contracts.

## Scope

The feature implements dedicated Codex subscription login, refresh, execution and logout in `cmds/delidev-cli`, `protos/delidev/v1/subscription.proto` and the generated DeliDev clients. The server owns authorization, encrypted credentials, generations and exclusive account leases. An explicitly selected paired Worker owns execution processes and its private authentication files. The independently negotiated server login lane owns browser login, authentication refresh and logout without a Worker. The complete product requirements remain in [the feature's snapshot](cmds-delidev-requirements.md).

Existing-login import, externally supplied token bundles, internal-only `chatgptAuthTokens` and concurrent use of one managed Codex bundle are excluded. Claude device-owned accounts follow their independently negotiated lane above for execution and logout; new Claude sign-in follows the server-owned lane under System 67. Desktop login controls and native-owner quota/reset-credit operations are implemented together in the subscription lifecycle feature PR. Full native recovery and real-account/platform acceptance remain separately identified; fixtures cannot establish them. This implementation does not complete the feature or claim a release.

## Runtime and Language

Go owns server and Worker business logic. The native profile pins installed Codex `0.151.0`; DeliDev never installs it. The managed profile uses the official app-server protocol, a fresh private `CODEX_HOME`, file-backed native authentication and the built-in OpenAI provider. API execution and discovery retain their existing ephemeral credential profile. Independent native Fork uses the API profile or the separately negotiated protected managed profile for this feature in the Fork contract. Managed ChatGPT Sidechat uses its separately negotiated protected Fork lease under the Sidechat contract. Source inspection cannot grant credentials; the exact claimed Sidechat job owns protected Take, joined native/plaintext cleanup and original-account write-back before publication.

## Users and Operators

The server owner or an authorized paired client initiates lifecycle operations. A paired Worker advertises `managed-codex-subscriptions-v1` only after an empty-home native configuration handshake and owned cleanup; the server echoes this closed capability. Execution and explicit-machine lifecycle operations select an execution machine. Independent server lifecycle operations omit that selection. Workers cannot initiate owner lifecycle operations or read another machine's bundle.

Execution admission requires that selected Worker's negotiated managed capability before claiming a session or consuming queued input. Ordinary native installation discovery alone cannot admit subscription execution; an unsupported Worker leaves the original input queued without selecting a fallback.

## Interfaces and Contracts

Subscription accounts use schema 2 with required closed `subscription_service: "chatgpt" | "claude" | "grok"` and no `provider_id`. Native models use schema 2 with `source_kind: "subscription"`, the same service and no API Provider. Services map exactly to Codex, Claude Code and Grok Build; a model's single compatible harness must match its service. API accounts/models keep their existing schema-1 provider identity and forbid subscription fields. Neither service metadata nor model registration grants native login, quota or execution support.

The historical provider-bound shape used `native-subscription`, `subscription` authentication, an empty endpoint and optional `subscription_harness: "codex"`. Migration 28 retires every legacy subscription account and those providers/models without inferring a service from any field or display name. That shape is retained only as read-only historical metadata under the [storage contract](cmds-delidev-storage-contract.md#subscription-retirement). New native-subscription Providers and edits that replace service/account/model identity are rejected.

Managed Codex lifecycle and execution select an independent ChatGPT account. Go rechecks its original generation/connection/lease and matching immutable service model at every authority boundary without loading an API Provider. Publication-only registrations expose no API relay path; independent service identity does not grant an upstream API operation. Immutable snapshots and new native response/diagnostic records carry the selected service instead of a fabricated Provider ID. Historical provider-bound snapshots and usage retain original bytes and attribution; retired selections cannot resume or silently reroute.

`SubscriptionService.RequestSubscription` accepts login, refresh and logout with a UUID-v7 mutation receipt and current account revision. An explicit machine preserves the original Worker lane; omitted `machine_id` selects the independently negotiated server lane. `CancelSubscription` cancels the original pending login. `GetSubscriptionProgress` returns short-lived browser/device presentation data only to the original initiating principal. CLI equivalents are:

```sh
delidev account login --id ID --revision N --machine-id ID --device-code
delidev account login-progress --id ID --operation-id ID
delidev account cancel-login --id ID --revision N
delidev account refresh --id ID --revision N --machine-id ID
delidev account logout --id ID --revision N --machine-id ID
```

Omitting `--device-code` selects native browser login. Ordinary acceptance/status output contains references and state, never a login bundle. The explicit progress command presents the native one-time URL/code without persisting or logging them. Cancellation may race native login completion, but its durable state wins before connection publication.

Revoking a paired initiating client atomically cancels its still-queued login, refresh and logout operations. This grants no lease, changes no credential generation, and preserves logout's accepted execution revocation and any independently granted lease. A fresh authorized request may replace a canceled queued operation. Successful execution write-back may retain the newest protected generation but cannot restore readiness after accepted logout revocation; a fresh authorized logout must complete cleanup. Claimed operations retain their original ownership and recovery obligations; revocation never proves native cleanup.

An independently authorized outbound `WatchSubscription` lane supports four concurrent ordinary account operations per Worker, plus a separate pool of four recovery attempts that ordinary operations cannot occupy. `TakeSubscription`, `PublishSubscriptionProgress` and `FinishSubscription` are restricted to the selected current Worker/device/instance. Bundles travel only in the protected Take/Finish messages, with a 64 KiB bound; they never enter jobs, execution output, events or ordinary resource responses. Take receipt replay never redistributes a bundle or authorizes another native launch.

One durable account lease spans login, refresh, execution or logout. Refresh/logout requests can wait behind an execution lease. Other sessions retain their selected account and wait after a definite busy refusal; unknown delivery is never retried. Other accounts acquire independent leases. Native execution uses an immutable subscription selector and the existing publication/history/workspace machinery. Its publication registration exposes no API proxy path and cannot obtain an API relay lease.

A lifecycle Take that races another lease grant retries only a definite busy refusal, using the exact original claim, account, operation and revision. This wait preserves the shared Worker connection and the execution that won ownership; caller cancellation joins it. Unknown delivery still closes the ownership lane and cannot be retried. The lifecycle revision remains its original positive account observation, not a requirement that later cleanup or metadata edits leave the account unchanged. Take rejects future observations and independently requires the same uncanceled queued operation, action, selected machine and currently authorized initiating actor; cancellation, replacement and recovery cannot gain authority from an old observation. Owner lifecycle requests still require the current account revision, and execution Take still requires its exact claimed job revision.

A fresh lifecycle (login, refresh or logout) Take returns `Canceled` with exact `ErrorDetail.cause = subscription_take_not_admitted` only from a rolled-back admission transaction after current account, Worker, instance, installation and update fences when the queued original is absent, replaced or canceled. Operation identity is classified first: an absent or replaced original receives the proof even when the replacement has a different action, selected machine or phase, and the replacement is never inspected or changed. This classification precedes lease, exclusive server observation and native-started server operation contention, so an unadmitted claim is retired immediately even while an unrelated lease is active; a matching, uncanceled, queued claim behind that contention still receives the retryable busy refusal and the Worker retries with its original revision and lane. Execution, quota and reset-credit Take keep contention first. It grants no lease, bundle or native authority. Replay, recovery ownership, a claimed matching operation, a matching operation with wrong action/machine/phase, transport cancellation and post-commit errors never supply this proof. The Worker recognizes both the code and exact cause, retires only that original private claim, and leaves independent account operations on the shared lane running without Finish, retry or native launch. Every unproven refusal retains the existing lane and recovery fences. Claude classifies an absent or queued replaced original after the original account, current Worker instance, verified installation, update, revision and recovery fences, before lease contention and before reading the replacement-owned action, phase, native profile or owner machine. The stale claim cannot adopt, cancel or change that replacement. A matching operation still requires its original native profile and selected machine; a canceled initial login with a retired profile retains its exact original canceled-operation proof.

A confirmed pre-native execution failure returns the byte-identical unused original bundle over protected Finish with cleanup true and success/refresh false. This includes a failed Open only when owned process closure and unchanged-file cleanup are independently confirmed. The server compares the returned bytes with the immutable vault generation before releasing the lease, preserving its generation, connection, health and any independently queued refresh/logout. Cleanup alone, missing bytes or changed authentication cannot establish this outcome; uncertain execution/refresh retains blocked original vault material and recovery ownership. Explicit logout retains its separately authorized credential-removal semantics.

If an atomic authentication write fails before publishing its destination and before native ownership, confirmed cleanup instead requires the original private home, synchronized and rechecked destination absence, and the complete bounded retained-file credential scan. A leftover temporary credential file, a missing/foreign home or an uncertain observation retains the fence. Reject every retained entry in the atomic writer's `.pending-` namespace, including empty or partial files and nested entries, independently of token matching. Published authentication follows the unchanged original-byte comparison/removal path; native-owned cleanup cannot substitute file absence for its captured-bundle proof.

The lease pins operation, account generation, Worker machine/device/instance, server epoch and original lease revision. Completion checks that original revision while preserving later metadata edits and cancellation. The connection ID remains stable across bundle rotations. The final native bundle is saved under a new immutable generation, old protected references are tombstoned, and independently confirmed native/file cleanup precedes another grant.

Read-only Doctor inspection follows the [ChatGPT saved-storage adapter](cmds-delidev-diagnostics-contract.md#chatgpt-saved-storage-adapter). Only a valid idle service-native ChatGPT account can read its exact saved `AccountLogin` generation. Pending lifecycle, leases, active/native server work, active or uncertain quota/reset-credit work, removal, recovery and unresolved cleanup exclude the read. Completed history alone does not exclude it. Request-local selectors fence same-connection rotations and changed eligibility at report publication. Readability grants no authentication, native cleanup, recovery or retry authority; native-only Claude and unactivated Grok remain unsupported. This adapter never changes subscription state or protected storage.

Finish's ownership-error guard ends after the ownership mutation and final required vault cleanup commit, before its response-resource observation. A canceled or failed presentation read cannot add a recovery fence to settled state. Failed final cleanup still fences ownership, and accepted receipt replay preserves that fence without repeating vault work.

Successful `account/read` does not prove refresh. Both Worker and server require changed token material, a strictly newer native `last_refresh` value and unchanged account/user identity. Server identity commitments enforce one managed owner for the same provider account/user across account aliases. Local logout proves native/file removal and server vault cleanup; provider-wide revocation remains best-effort and is never reported as confirmed.

The pinned native login-completed envelope includes nullable `onboardingEntrypoint` metadata. Accept null or the pinned closed `life_sciences` value without launching another onboarding flow or treating presentation metadata as authentication evidence; reject unknown values while still requiring the original login identity and successful completion.

## Server browser login and account naming

System `SERVER_SUBSCRIPTION_LOGIN_V1 = 30` activates the shared reservations established on main by the originating change. It is independent of service-account inventory capability 17 and Worker capabilities. An authenticated owner or paired client can omit `machine_id` for ChatGPT login, authentication refresh or logout. The server must have the verified Codex 0.151.0 installation. No Worker registration, startup or selection is required. Claude sign-in never uses the Runner lane: it is server-owned under System `CLAUDE_SERVER_LOGIN_V1 = 67`, and capability 38 remains only for legacy device-owned execution, machine-bound logout and operations accepted before the retirement; Grok has its own server-owned login, refresh and logout lanes under the [Grok contract](cmds-delidev-grok-subscription-contract.md) and never uses the Codex runner. API authentication, execution selection and existing quota authority do not change.

Go records the original actor, operation, server epoch, bounded lifetime, finish identity and closed progress state in optional server-owned `server_operation` metadata. Accepted request replay returns the original receipt without another native launch. Claiming requires the original queued pending operation and no Worker credential lease; `native_started` is an exclusive credential fence. An existing execution lease retains ownership while queued refresh/logout waits. Cancellation is serialized with final publication. Successful native completion, joined process closure, final-file comparison, private-runtime removal, vault staging and old-reference cleanup precede confirmed success. Uncertain cleanup and previous-epoch operations retain recovery ownership and cannot relaunch or redistribute credentials. Optional JSON metadata requires no database migration and does not alter migrations 26–30.

### Terminal original server LOGOUT cleanup recovery

System `SUBSCRIPTION_CLEANUP_RECOVERY_V1 = 64` independently advertises that `DeleteConfiguration` can also clean up and delete one terminal original ChatGPT server LOGOUT whose only remaining work is protected credential cleanup. Capability 41 keeps its meaning (failed initial LOGIN and disconnected batch cleanup). Number 64 is recorded in `protos/delidev/allocations.json`; the originally planned 90 belongs to `PULL_REQUEST_WORKSPACE_V1`. The capability adds no message field, database migration or remote flag. Older servers retain their current deletion refusal and clients show update guidance instead of sending an expanded request.

A server LOGOUT first commits its disconnected metadata (no connection, generation, identity commitment or Worker owner) and then removes protected `AccountLogin` references. A later vault failure leaves `markServerSubscriptionRecovery` metadata that has no public replacement operation: LOGIN/LOGOUT reject pending or recovery state and `DisconnectAccount` rejects ChatGPT. A LOGOUT qualifies only in exactly that shape: health `failed`, no connection, generation, identity commitment, Worker owner, native profile or lease, `recovery_required`, a queued server-owned pending placeholder equal to the original operation and actor, and a `server_operation` with `action=logout`, `native_started=false`, no `cleanup_phase`, state `recovery-required` or `succeeded`, and its original credential `generation`. Active or uncertain quota, reset-credit and observation work, replacement connections or generations, Worker leases, refresh/login operations, foreign removal metadata and a still-started native lease all reject. `RecoveryRequired` alone never grants authority.

Explicit `DeleteConfiguration` admits a version-3 child under the original deletion request. It freezes the actor, server, account, confirmed revision (also the deletion revision), original operation ID, credential generation, finish ID and original operation actor. Batch selection never creates this target. The child keeps its checkpoints privately (`started`, `native_confirmed`, `credentials_started`, `credentials_confirmed`); LOGIN-only `cleanup_phase` values are never assigned to LOGOUT metadata. The controller runs under the account gate and never sends a replacement LOGIN or LOGOUT:

1. Persist `started`, then reuse the original process/profile ownership reconciliation: the retained owner index and verified journals must exist, no controller may be live, and probes are removed or already absent. Missing, foreign or contradictory proof retains the account.
2. Persist `native_confirmed`, then `credentials_started` before vault work. An interrupted attempt after the credential fence is never repeated; restart records a terminal retained result. A restart after only `native_confirmed` resumes at the credential step; a new explicit request reproves the idempotent ownership reconciliation.
3. Remove every retained credential file of the account's permanent auth profiles and of the historical per-operation runtime, then delete every unremoved `AccountLogin` reference of this account (no count limit, owner and purpose validated before the first deletion, cancellation observed between items) through the existing vault tombstone and OS-policy path. No password is collected and no arbitrary credential is enumerated.
4. In one transaction, clear only the matching recovery state (`recovery_required`, pending placeholder, failed health, operation diagnostic), set the operation state `succeeded`, advance the child's effective revision and record `credentials_confirmed`.
5. Run the existing Agent/Project/retained Session/schedule and protected-reference checks and write the atomic deletion receipt under the original public request and confirmed revision.

Cancellation, denial, unavailable owner, a failed reference check or unresolved proof settle a terminal retained result with a closed reason and problem code. A retained `confirmation_required` result is surfaced as a definite operating-system authorization failure for both this target and the failed initial LOGIN target; a new confirmed request after the vault is authorized retries the retained tombstone. Replay, status reads, remount, reconnect and polling never start another native or vault attempt. Structured logs (`server_logout_cleanup_step`) carry only the closed phase (`begin`, `native`, `credentials`, `metadata`), result (`confirmed`, `retained`), error code and operation/correlation identifiers, never credentials, paths or native output.

### Failed initial server login cleanup

A failed, canceled or interrupted initial ChatGPT server login can release its account for explicit configuration deletion after independently confirmed cleanup. This applies only to the original server login with no connection, credential generation, identity commitment, Worker lease, native machine owner, quota/reset-credit operation or account removal. Refresh, logout, connected accounts, restored credential generations and Worker ownership retain their separate recovery boundaries. Cleanup never repeats authentication or callback forwarding and never claims provider-wide revocation.

The optional server-owned `server_operation.cleanup_phase` uses the closed values `native-confirmed` and `credentials-confirmed`; omission preserves legacy metadata and means no checkpoint. Joined native closure (the original process ownership proof, or the pre-native proof when the opener never ran) is the only proof that settles a failed initial login: one transaction records `credentials-confirmed`, clears the pending/native/recovery fences and sets disconnected health. The settlement removes no credential file and no protected reference, because routine server operations retain them. A `native-confirmed` checkpoint written by an earlier build releases through the same transaction after the same account, operation and generation match, without relaunching login. Preserve the original operation, failed/canceled/expired/unsupported outcome, safe diagnostic and current configuration preferences. A canceled server context alone cannot retain recovery after the settlement is confirmed.

A later explicitly accepted server or Worker login supersedes this completed server attempt. Worker admission retires only its fully confirmed terminal server metadata before creating the new independent pending operation; an unsettled checkpoint never permits replacement.

Server maintenance makes 30-second cleanup attempts per original operation, using the existing shared 16-account lane and account gate. It joins these children at shutdown and excludes a still-running original login. A retry after server restart resumes its durable native checkpoint without another native login. Failed attempts keep their original ownership and never run again on every 250-millisecond scan: the scanner retries the same operation in the same server process with bounded exponential backoff (one second doubling to one minute), so a transient vault failure cannot strand a durable checkpoint until restart. A completed attempt resets the delay, and an operation that no longer needs cleanup is forgotten.

Legacy claimed logins without a checkpoint require the original private process owner index and valid journals. Independently held native controller locks block recovery; a released controller permits the existing platform ownership reconciler to confirm or stop only that original process tree. Retain the index after journal retirement. Missing individual journals, foreign ownership and file/PID absence alone grant no cleanup proof. After process confirmation, remove only identity-checked original auth and discovery-probe directories, rejecting symlinks and preserving other operations. Legacy metadata with `native_started=false` proves either no native claim or previously joined cleanup; unexpected retained auth/probe files contradict that proof and remain blocked.

Vault, filesystem, authorization or state-publication failure retains the configuration and cleanup checkpoint for a later server process. The Settings deletion screen continues to require a fresh account read and explicit confirmation; cleanup never deletes configuration. Existing revision, retained-reference and independently observed browser-profile cleanup checks remain authoritative. Logs contain only original operation references, cleanup phases and stable codes. Tests use temporary synthetic accounts and native process fixtures; they do not establish installed-app, real-account or all-platform acceptance.

Browser login uses the ordinary `account/login/start` ChatGPT profile, never automatic device-code fallback. The pinned app-server disables its own browser opening; the trusted native desktop opens the returned original address once. Exact native binding replay cannot open another browser. Deliberate reopening uses only that same operation/address. Local desktop login retains the original installed Codex callback. Remote desktop login binds its registered loopback callback on both address families and forwards one bounded code/state/scope query through authenticated `ForwardSubscriptionCallback` to the original server's Codex callback. The callback requires the original state, actor, current server epoch, operation and trusted window/opening lifetime. Duplicate, foreign, expired, canceled and late callbacks are refused. Go durably claims forwarding before HTTP, inherits no proxy, follows no redirects and never resends uncertain delivery or performs another exchange.

Browser callbacks accept exactly `http://localhost:1457/auth/callback` (Codex 0.151.0) or `http://127.0.0.1:1457/auth/callback` (Codex 0.159.2). Preserve the original URL, state and callback authority through browser binding and forwarding; the two spellings cannot replace one another within an accepted operation. Forward only to fixed IPv4 loopback and retain the original callback HTTP Host. Remote native receivers retain both loopback listeners and require that original Host. Other hosts, numeric IPv4 spellings, ports, paths and duplicate query fields are rejected. URL rejection logs contain only the fixed failure classification and original operation reference. This compatibility rule does not recover prior failed accounts or establish actual account/platform acceptance.

Codex 0.151.0 can send `/cancel` to an occupied preferred port 1455 before using its registered fallback 1457. The server first owns a private 1455 listener, refuses a conflict, and retains it through joined native closure. This prevents cancellation of another login and lets the original Codex listener choose its registered 1457 callback. A fallback conflict fails explicitly. Remove this bounded workaround only when the pinned native protocol provides an exclusively owned callback port without foreign cancellation. The remote desktop never remaps its callback or substitutes device authentication.

Typed progress separates preparing, waiting, succeeded, canceled, expired, unsupported, failed and recovery-required. Successful login exposes its exact current generation and one transient name suggestion, chosen from email, provided display name, then ChatGPT within the existing 256-byte UTF-8 alias bound. Native login success remains the authentication evidence; JWT identity claims only project consistent identity. Suggestions belong to the original actor/operation/generation, expire with its lifetime and remain only in memory until the user explicitly saves a name. They never enter resources, receipts or logs. Refresh/logout cannot suggest names.

Settings Add account is an explicit event that creates a default service-named account and immediately requests browser login. Mount, Strict Mode, polling and reconnect cannot repeat creation/login. Only confirmed original success advances to editable name entry. Save uses the current revision and changes only the alias while preserving server-owned bytes; a conflict retains the draft for a fresh explicit save. Later and navigation retain accepted default-name metadata and authentication. Navigation discards the Settings presentation/native callback receiver and rejects late continuations without business cancellation. Only Cancel login requests cancellation.

### Project requirements

- Failed subscription cleanup follows `cmds-delidev-subscription-contract.md#failed-subscription-cleanup-reservations` after main reservation the originating change (System 41). Preserve server-owned complete batches, original actor/login/revision, shared vault/reference deletion checks, atomic result/tombstone receipts, joined shutdown and restore quarantine. Terminal failures require a new explicit batch; status/restart never relaunches login or callbacks. No database migration or Rust/native change.

- The feature server quota uses System 46 and the separate optional server quota owner under `cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota--issue-1728`. The owner explicitly permits allocation and activation in one complete batch PR without the prior main merge under the shared allocation workflow; existing allocation ownership and migration dependencies remain unchanged. Preserve original actor/epoch/connection/generation, once-only claims, confirmed native/file/reference cleanup, joined shutdown and strict Worker/reset-credit ownership. No new RPC, Worker capability, migration or Rust/native change. Real-account/installed-native/remote/platform acceptance is owner-performed and nonblocking for this batch; fixtures establish no such acceptance.

- Server quota V2 follows the feature and `cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota-v2--issue-1854`: record and activate System 50 in the complete feature PR, preserving 18/46/49. Omitted-machine manual/batch/maintenance quota uses a separate server access-token-only native profile even during Execute. Refuse token refresh, retain captured protected references through rotation, and keep quota cleanup uncertainty independent of execution leases. Preserve explicit Worker/reset-credit semantics, lifecycle/deletion/restore fences and joined shutdown; no RPC, Worker allocation or migration. Real native/account/remote/platform acceptance is owner-assigned and nonblocking for this batch.

### cmds/delidev-cli/internal/cli constraints

- `account list` accepts optional closed `--account-type api|subscription` and `--provider-id UUID` selectors through the list-only Connect request. Preserve the unfiltered default for existing callers; filters are applied before SQL pagination and bound to cursors. Snapshots and event streams must not inherit them.

- Generic JSON configuration selects schema 2 only for service-native subscription accounts/models or retired Agent repair, preserving API schema 1 and the version-1 result envelope. Subscription list requests never combine a Provider selector. Follow the independent subscription identity and portable bundle contracts.

- Subscription login/refresh/logout may omit --machine-id only after capability 30 negotiation. Explicit machine selection retains the Worker lane; no CLI account command implicitly starts server/Worker. Device-code auth remains an explicit CLI alternative, never browser fallback. Dedicated original login-progress output may show transient original state/name/generation; operational logs must not contain it.

- Server-owned ChatGPT reset credits follow the feature under the subscription contract. System 49 separately negotiates omitted-machine consumption through the settled original server credential generation; active executions and explicit machines retain the Worker lane. Preserve immutable actor/epoch/connection/generation/inventory/key/selection, once-only durable sends, explicit same-key reconciliation after confirmed native/file cleanup, independent quota outcomes, cross-domain ownership fences and joined shutdown. No new RPC, Worker/entity allocation or migration. Keep fixture/build checks separate from real native/account/platform acceptance.

### cmds/delidev-cli/internal/domain constraints

- Optional Account.subscription state contains only server-owned generation references, identity commitments and actor/lease fences. Configuration cannot manufacture or replace it; historical accounts omit it unchanged. Keep closed action/phase/capability values under `cmds-delidev-subscription-contract.md`.

- A restored managed subscription may retain a valid historical generation without a connection only while recovery-required. This is quarantined evidence; ordinary usable generations still require subscription-authenticated connections. Follow the storage and subscription contracts.

- Keep service-native subscription identity closed to ChatGPT/Claude/Grok and its exact harness; OpenCode Go is the separate closed key-backed OpenCode identity under its owning contract. API Provider identity remains separate. Match complete account/model identity in routing and immutable snapshots; retired Agents require explicit reconfiguration before resolution.

- Native subscription observations preserve sparse quota fields, authoritative credit counts and original generation-bound operation keys under the subscription contract. Only fresh positive native evidence may clear exhaustion; typed failure/reset outcomes cannot grant recovery.

- Subscription server_operation is optional server-owned closed metadata. A machine-less pending claim is valid only for its exact server operation/actor; native_started requires no Worker lease and the original generation. Active/recovery ownership retains pending authority. Terminal metadata grants no credential use. Keep APIs, immutable historical service attribution and real migrations unchanged.

- Claude subscription optional ownership pairs native profile and owner machine, with original operation/actor/epoch/lifetime, once-only code claim/consumption and closed safe diagnostics. Usable generations require native identity commitment and subscription connection. Metadata grants no credentials; preserve account/model/harness matching, historical identity and no migration under the subscription/storage contracts.

- The feature reference pricing uses validated exact ModelIdentity (one API Provider UUID or SubscriptionService plus native ID). Collector rates are exact bounded decimals; missing and zero remain distinct. Identity/reference metadata grants no account or execution support. Follow the catalog, usage and network contracts.

- Paid-credit observations retain original ChatGPT quota ownership under System 76 / Worker 51. Project only bounded per-bucket flags, nullable exact decimal strings and original successful timestamps; omit sparse balances, reject malformed/reflected values and preserve last-good evidence. Capability omission grants no support, generation replacement clears balances, and configuration saves grant no observation or spending authority. Follow the subscription contract; no migration.

### cmds/delidev-cli/internal/harness/claude constraints

- Original installed Claude Code (SemVer precedence at or above the 2.1.236 baseline; compared against its observed version, not the constant) owns subscription OAuth and automatic renewal through private account-specific configuration and secure storage. Use official auth login --claudeai, auth status and auth logout; never inspect auth files, personal profiles or external tokens.

- Validate the original closed CLI URL without reconstruction and accept one bounded readline approval input. Join the original login process and descendants before independent subscription status confirmation. Clear URL/code/native identity buffers after their bounded use; retain only safe structured diagnostics.

- Keep native subscription initialization distinct from API/discovery authentication. Reconstruct environment paths; no API key, relay bearer/base URL or host-managed/imported auth enters subscription execution. Existing permission/model/effort, original input/history/continuation and Stop proofs remain mandatory.

### cmds/delidev-cli/internal/harness/codex constraints

- Accept the installed Codex 0.159.2 observation fields in thread Start/Resume/read, settings notifications, empty agent-message questions and quota/response-usage metadata under the harness contract. Discard private observations without replacing immutable effective settings, enabling plugins/environments/Daybreak or inferring billing cost. Populated questions remain a private extension. Keep unknown-field rejection, original history and managed-account recovery fences. Event failure logs use closed validation stages, never raw methods or payloads.

- Follow the parent and repository instructions. Managed authentication is a separate explicitly leased profile under `cmds-delidev-subscription-contract.md`. Require native login completion, file-backed ChatGPT authentication and the official built-in provider; reject inherited overrides, API keys and external tokens. Successful account/read alone never proves refresh. API execution/discovery retain their ephemeral profile.

- Discovery and subscription lifecycle processes pin `features.plugins=false` before launch and verify disabled plugins through the complete bounded normalized native feature inventory before granting a client. Missing, enabled, malformed or incomplete observations reject the profile and join the original native owner. Thread execution retains its independent feature policy. Keep disposable runtime cleanup bounds unchanged; disabling downloads cannot recover prior failed accounts or establish real-account acceptance.

- Private quota reads accept optional nullable `ordinaryUsageAllowed` booleans, `accountId` strings and opaque `rateLimitUpsell` JSON from the official Codex response. Discard these fields before projection; they grant no identity, routing, recovery or consumption authority. Preserve strict unknown-field, typed-field, duplicate-key and envelope-bound checks.

- Pinned native quota/reset-credit adapters omit unit parameters where the original protocol requires it, project bounded sparse metadata and preserve the official idempotency key and closed outcomes. Exclude display/billing fields and credential reflection; native updated notifications cannot grant authentication or execution authority.

- Server quota V2 follows the feature and `cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota-v2--issue-1854`: record and activate System 50 in the complete feature PR, preserving 18/46/49. Omitted-machine manual/batch/maintenance quota uses a separate server access-token-only native profile even during Execute. Require and discard only the original external-token login’s closed success completion with no managed login ID, error or onboarding payload before reading quota; this metadata grants no renewal authority. Refuse token refresh, retain captured protected references through rotation, and keep quota cleanup uncertainty independent of execution leases. Preserve explicit Worker/reset-credit semantics, lifecycle/deletion/restore fences and joined shutdown; no RPC, Worker allocation or migration. Real native/account/remote/platform acceptance is owner-assigned and nonblocking for this batch.

- Quota privacy checks reject exact original bucket and reset-credit IDs against protected raw values and complete Base64 forms at every length. Strip only the adapter-owned final window suffix; retain long substring checks without short-word substring rejection. Worker reads/updates and server-owned quota reads share `ValidateQuotaSecrets` before publication under the subscription contract.

### cmds/delidev-cli/internal/knownmodels constraints

- Never read subscription credentials, run harnesses, create resources or infer account/readiness authority. Log only catalog version/date and stable failure codes. Fixtures use isolated temporary state and injected transports.

### cmds/delidev-cli/internal/server constraints

- Subscription cleanup includes fully disconnected service-native accounts without synthetic login IDs. Explicit failed initial ChatGPT deletion uses the shared durable cleanup controller, preserves the original public deletion revision/receipt while checkpoint revisions advance, and never retries a terminal attempt without fresh explicit confirmation. Retain original actor/native/vault/reference checks; no new RPC, protocol allocation or migration. Follow the subscription and account contracts.

- Managed Codex subscriptions follow `cmds-delidev-subscription-contract.md`. Keep explicit provider selection, a separately authorized Worker lane, one durable per-account login/refresh/execution/logout lease, original actor/revision/generation fences and immutable latest-bundle write-back. Never redistribute a Take receipt or uncertain generation, expose bundles in records/logs, grant API relay authority from subscription registration, or delete unresolved ownership.

- Require the selected Worker's negotiated managed Codex capability before claiming subscription execution or consuming its queued input. Ordinary native installation discovery does not establish managed authentication support.

- Initiator revocation must atomically settle its still-queued subscription lifecycle operations without granting a lease or changing credential generations. Preserve accepted logout revocation, independent active leases and all claimed/native ownership; only a fresh authorized request may replace the canceled queued operation. Successful native write-back must preserve logout-revoked health until a fresh authorized logout completes cleanup.

- Lost subscription ownership must reject progress reads and publication and purge the in-memory URL/device-code presentation under the account gate. Retain the original pending operation, lease and recovery evidence; cache invalidation never proves native cleanup or grants a retry.

- Recovery-required subscription ownership rejects every new Finish before vault staging/deletion and at the final transaction. An already accepted Finish receipt may replay read-only without changing a later retained lease, pending operation or recovery fence.

- Quota/reset-credit capabilities 18/19 and Worker observation capability 8 follow the subscription contract. Queue complete server-side refresh batches, persist original send claims before native work and retain uncertain consumption for explicit same-key reconciliation. Publish account recovery and its deduplicated account-scoped Inbox record atomically; never grant a second active credential writer.

- Quota maintenance checks due eligibility read-only before mutation and rechecks it transactionally. If every candidate was concurrently queued, roll back the empty batch; do not emit recurring no-op receipts or change signals.

- Joined successful native-owner cleanup settles unpublished quota reads as failed while preserving last observed values and future read/execution admission. Retain original-key uncertainty only for possible reset-credit consumption.

- Independent subscription capability 30 admits omitted-machine login/refresh/logout under the subscription contract. Original actor/epoch/request/lifetime and native_started metadata form a disjoint exclusive credential fence; Worker leases cannot coexist. Reuse verified isolated Codex, joined process/file cleanup and protected generations. Claim callback forwarding durably before fixed loopback HTTP, reject foreign/duplicate/late state and never retry uncertain delivery. Restart retains recovery without relaunch. Suggestions stay original-success-bound and memory-only until explicit name Save; logs retain only opaque IDs/actions/codes.

- Subscription browser callbacks permit only the exact registered localhost and 127.0.0.1 port-1457 `/auth/callback` addresses under the subscription contract. Retain the original URL/state/Host; forwarding always connects to fixed IPv4 loopback and never substitutes one accepted spelling for another. Reject duplicate query fields and malformed state. Log URL rejection with only its fixed classification and original operation reference. Preserve prior recovery fences.

- Server subscription failures retain actual Codex version and the first safe native failure separately from cleanup/recovery. Login logs use original operation correlation without account identifiers. Publish diagnostics only to the original authenticated actor/operation and preserve uncertain ownership without resending work. Follow the subscription and protocol contracts.

- Subscription cleanup failures log only closed native-process/auth-file/runtime stage and reason enums, the original operation reference and bounded inventory counters/limits. Preserve the public recovery classification, auth-file comparison, original process joins, directory identity and symlink refusal. The execution authentication scan has no inventory limit; profile removal keeps its 2,048-file/64 MiB bound. Runtime diagnostics contain no paths, native output, file content or credentials; they cannot clear existing recovery fences.

- Claude subscription business logic belongs in claude_subscriptions.go under System 38, which serves existing device-owned accounts and operations accepted before the retirement of machine-bound LOGIN/REFRESH (new sign-in is server-owned under System 67 in claude_managed.go, and that lane reports the same typed `Unsupported` retirement result for a machine-bound LOGIN or REFRESH on an existing server-token account). Bind original owner/client operation and Worker/device/instance/lease, claim codes before memory retention and consumption before once-only release. Idle progress/code polling is read-only. Cancel/revocation/expiry wins before connection publication; wrong Runner fails before input consumption. Retain uncertain ownership and admit original cleanup-only recovery without login/code replay. No Claude bundles, credential files, raw identity, URL/code persistence or quota inference in this device-owned lane; the server-owned usage lane stores only its own claude-usage vault references and reads only its own newly created usage profile.

- `desktop_credentials.go` owns read-only startup Keychain checking for app-owned macOS servers. Page current connected API accounts, resolve immutable selected profiles, skip keyless/disconnected/removal/subscription ownership, and read exact current references. OAuth reads bypass refresh/exchange/cleanup. Clear returned buffers even on failure, recheck original client authorization, retain original attempt on observation and join canceled native work before vault closure. Only explicit predecessor-bound Retry may replace terminal failure; never change account health/revisions/receipts or send provider HTTP. Follow the desktop/credential contracts.

- The feature active Execute quota admission uses only the original initialized native process under its exact account/connection/generation/lease/revision/epoch/Worker/device/instance and initiating-actor proofs. Explicit, omitted-machine, Refresh all and maintenance share eligibility without saved discovery/version gates. Preserve strict idle quota and reset-credit/reconciliation discovery checks; registry absence grants no new native owner. Follow the subscription contract; no allocation or migration.

- Server quota V2 follows the feature and `cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota-v2--issue-1854`: record and activate System 50 in the complete feature PR, preserving 18/46/49. Omitted-machine manual/batch/maintenance quota uses a separate server access-token-only native profile even during Execute. Refuse token refresh, retain captured protected references through rotation until confirmed native cleanup (the obsolete reference is then retained, never routinely retired) before terminal publication, and keep quota cleanup uncertainty independent of execution leases. Preserve explicit Worker/reset-credit semantics, lifecycle/deletion/restore fences and joined shutdown; no RPC, Worker allocation or migration. Real native/account/remote/platform acceptance is owner-assigned and nonblocking for this batch.

- Server subscription maintenance initializes the shared vault and reads the exact original `AccountLogin` generation under `accountGate`, sharing API account synchronization and cancellation. Release the gate before native work; retain retryable open failures and the original operation/cleanup owner. Follow the credential contract. When the gate wait or vault read fails because the operation context was canceled or reached its deadline while the parent server context is live, the pre-native absence proof (no auth home, no process owner, probe index cleanup) runs under a fresh bounded 30-second owner context instead of the ended operation context. A verified proof confirms cleanup so the original operation settles as Canceled or Expired without recovery; a failed proof or retained native evidence keeps recovery required. No opener or login retry runs, and finish still joins cleanup to the original operation and generation.

Worker subscription RPCs allow up to one minute for response headers so a first
protected credential read can finish native code validation or OS authentication.
The original caller context still cancels the request earlier. This bounded wait
does not retry uncertain credential delivery or replace the original lease.
Managed Claude delivery logs read start, completion, duration and success without
credential contents.

A fresh subscription lifecycle Take may return Canceled with exact cause `subscription_take_not_admitted` only inside its rolled-back admission transaction after original account/Worker/instance/installation/update fences when the queued original is absent, replaced or canceled. Replay, claimed/recovery state, wrong action/machine and post-commit errors never carry this proof. No lease, bundle or native authority is granted. Claude rejects a stale queued replacement claim after original current-Worker/update/revision and recovery fences, before lease contention and replacement-owned action/phase/profile/machine checks. Keep matching-operation profile/machine validation and the original canceled retired-profile proof; never adopt or cancel the replacement.

- Automatic reset credits use actor/connection/generation-bound typed consent and atomic durable episode admission under System 77. Require the original failed Execute job/native thread/turn and active Worker 52 credential lease; reject idle adoption, free-text authority and restart replay. Logout/generation replacement clear consent. Preserve original same-key explicit reconciliation and cleanup; follow the subscription contract.

### cmds/delidev-cli/internal/store constraints

- A single-account subscription cleanup child reserves its original public configuration deletion request/revision before the final receipt exists. Both mutation and replay check that immutable command through the existing job parent index; admission rejects already used public IDs transactionally. Preserve ordinary batch and restored-job semantics; no migration. Follow the subscription/account contracts.

- The feature reserves migration 28 for service-native subscription identity and legacy configuration retirement. Keep versions 26 and 27 in their existing order and implement both real predecessors before activating 28. Reservations use one original `pr` or owning `issue`; retain that provenance after an implementation PR exists. The planned reset is backup-first and atomic, preserves historical bytes/attribution and configured-empty deny-all restrictions, and refuses unsettled native ownership or cleanup without inferring a service.

- Migration 28 follows real 26/27, backs up first and atomically retires original provider-bound subscriptions without inference. Preserve original record bytes in history-only storage and forbid live Get/admission fallback. Keep tombstones/receipt redaction, configured-empty deny-all, surviving ordered weights and affected Schedule reset. Refuse unsettled original native/protected/browser ownership; only a private historical restore candidate has the explicit no-current-authority migration exemption.

- Worker update admission and idle checks run under the claim transaction, including original terminal/forward/subscription/deletion ownership. Filter matching pending/signed scopes before applying query bounds. Preserve current installation metadata across managed restore, and never release uncertain update fences through history pagination or restored receipts.

### cmds/delidev-cli/internal/worker constraints

- OpenCode event subscriptions must precede the original input claim and consume the owned native connected record before sending. Preserve arrival order and original JSON event IDs without treating them as SSE replay cursors. Retain bounded private native bodies for separate typed observers; transport recognition grants no completion, interaction, usage or publication authority. Do not ignore unknown frames, silently evict identity history or infer EOF/idle as completed work. Reopen only through the harness contract's once-only same-process native event reconciliation: verify original process/authentication/effective configuration, retain all input/reply/publication claims, consume listener readiness before bounded repeated reads, and publish no partial result on uncertainty. Keep private read proofs separate from native event identities; only positive original reply evidence can resolve delivery. Stop, Archive, revocation and owned closure must cancel/join the cycle, and recovered terminal/history/cleanup remain separate boundaries. Stream failure blocks new input and cancels pending mutations without releasing their durable claims; retain prior valid observations and join the reader/watchdog on closure.

- The internal OpenCode owned API interface must retain original initializer settings and claims, validate input before consuming subscription authority, and serialize event dequeue with typed observation. Canceling only a waiting consumer must leave the original stream, queued arrivals and observer usable; actual subscription loss, owner cancellation, EOF and bounded-capacity failures still require reconciliation. Bind an unobserved retained in-memory input only to its original stream/root for inspection or cleanup, never as restart/replay authority. Keep native interruption, owned Stop intent, explicit recovery and generic joined Close separate; closure alone cannot publish successful input or synthesize a Stop receipt. Real Worker/native claim integration with fixture relay authority does not prove public dispatch, account registration or publication.

- Managed Codex subscriptions use only the selected installed Codex 0.151.0 and a fresh private file-authentication profile under the protected lease. Journal original claims before native work, never retry uncertain delivery, verify native identity and independent last_refresh/token changes, join native cleanup and save the final bundle before another grant. Accept all four Codex permission modes for API and subscription execution; default omits the sandbox override and full-access may expose managed authentication files to native tools. Reject non-canonical or workspace-overlapping `CODEX_HOME` paths before writing `auth.json`; preserve original identity, cleanup and recovery proofs without claiming unrestricted same-user isolation. Keep recovery evidence and synthetic test credentials isolated; follow `cmds-delidev-subscription-contract.md`.

- Codex thread publication and checkpoint retention/read must compare the exact provider selected by the immutable accepted authentication profile: built-in OpenAI for managed subscriptions and the existing relay provider for API execution. Copy private checkpoint comparison flags only from that accepted configuration; do not infer a profile from a native observation or widen uncertain-lease recovery.

- Close and join the managed subscription lane after uncertain delivery/completion so the server retains lost leases as recovery-required. A durably acknowledged operation failure must not interrupt unrelated accounts on that lane.

- Quota/credit observation dispatch shares the original active Codex process through a joined account-owner registry. Idle observations use one short exclusive lease. Preserve official operation keys after uncertainty; quota/read and credit/consume do not perform inference or substitute for authentication refresh. Join registry users before closing the native process.

- Idle quota/reset-credit observation publication failure does not itself imply credential-owner recovery. Collect the unchanged valid native bundle and independently join native/file cleanup before completing the short credential lease; retain original operation uncertainty/key for explicit credit reconciliation. Failed bundle/native/file validation still fences ownership.

- Claude subscription Worker 20 requires verified original Claude Code at or above the 2.1.236 baseline (the `--version` output must match the observed installation version) and joined empty-profile cleanup. Persist original lease/process/code claims before native effects; use one canonical private account profile and scoped secure storage, original-machine owner and exclusive lock (lifecycle operations and Workers that did not negotiate Worker 68 keep the whole-execution lock; a negotiated Worker's owner holds it for the owner's lifetime, see the shared account owner section). Official login/status/logout owns auth; raw identity stays local and the original keyed commitment is pinned in private profile ownership before later status comparison or execution input. Original recovery joins process journals and confirms logout/status plus bounded owned-profile removal before reporting cleanup. Execution reuses stream-json/history with no API auth injection or auth-file checkpoints. Never replay uncertain Take/login/code or claim actual platform acceptance from fixtures.

Subscription lifecycle Take treats only Canceled with exact cause `subscription_take_not_admitted` as an operation-local no-grant refusal. Retire its original private claim without Finish, retry or native launch; preserve other accounts on the joined lane. Execute, ordinary Canceled, RecoveryRequired, unknown delivery and uncertain Finish retain existing lane/recovery fences.

- Automatic reset credits use Worker 52 only with verified managed Codex. Publish the closed failed-turn marker after original execution progress, claim and consume through the same retained native lease, and fence/join its observer before native cleanup. No idle process, restart, Sidechat or Fork may adopt automatic spending. Preserve explicit reconciliation and original uncertainty under the subscription contract.

## Storage

Optional server-owned `Account.subscription` JSON preserves historical account bytes when absent and adds no destructive schema migration. It retains only generation references, keyed identity commitments, pending-operation metadata, original actor and lease fences. Configuration writes cannot manufacture or change this state. Unresolved ownership prevents configuration deletion. Managed database restore also refuses pending, leased or recovery-required ownership. Restored subscription references remain disconnected and recovery-required; historical generations and claims remain evidence, while the external vault is unchanged and supplies no restored authority. Clear an allocated subscription state with no generation, identity, pending operation, lease or recovery fence; settled logout or failed login owns no external reference to quarantine. Grok accounts follow the same service-agnostic quarantine; their backup images carry no bundle material and restored cleanup ownership is retained unchanged (see the [Grok contract](cmds-delidev-grok-subscription-contract.md#grok-backup-and-restore)).

The existing OS-backed [credential vault](cmds-delidev-credentials-contract.md) stores bundles with `account-login` purpose and immutable UUID-v7 references. Native plaintext exists only in the exclusive server or Worker private runtime. A Worker removes it after owned process closure; a server-owned operation instead retains the account-owned credential file in the account's permanent auth profile and every `account-login` generation until explicit logout or account deletion (see [Server credential retention and explicit removal](#server-credential-retention-and-explicit-removal)). Execution installs credential cleanup at the owned auth-file write boundary, before publisher setup and registration, for both current and predecessor runtime selections. Failures before native ownership remove and synchronize the original auth file; a failed native Open permits removal only when its process cleanup is independently confirmed. Uncertain native ownership retains the protected lease and cannot authorize optimistic deletion. Execution retains original history and checks its bounded remaining files for credential remnants. Worker journals contain only original claim/completion identities and cleanup outcomes, never bundle bytes or token digests. Temporary login presentation lives only in server memory.

Uncertain delivery or completion closes the Worker subscription lane and joins its children so the server records lost ownership. An acknowledged failed operation retains its confirmed completion and leaves unrelated accounts on that lane active. Missing refresh write-back, unconfirmed cleanup, Worker loss/replacement or server restart retains the original lease as recovery-required. The old generation cannot be redistributed. Recovery publication purges the in-memory login presentation under the account gate while retaining the original lease and pending operation. Progress reads reject recovery-required or previous-epoch ownership, and the lost Worker cannot republish a URL or device code. These lifecycle commands neither erase recovery evidence nor clear an uncertain lease; independent native recovery remains a separate product boundary.

## Security

Disposable subscription lifecycle processes disable native plugins before launch and verify the normalized disabled feature through the harness profile. This prevents unrelated startup/account-change catalog downloads from overflowing cleanup inventory. Discovery uses the same download restriction; actual session execution retains independent feature authority. Keep the original 2,048-file and 64 MiB runtime cleanup limits, identity checks, symlink refusal, original process joins and final auth-file comparison. Disabling downloads grants no recovery of prior failed logins, credential adoption, automatic retry or authentication authority.

Uncertain protected execution completion escapes ordinary job-error reporting and closes the primary work lane after joined cleanup. The original started job claim and protected completion journal remain retained; the server records the lost execution lease as recovery-required. Workspace cleanup failures preserve this completion uncertainty, and receipt replay cannot manufacture a successful write-back. A successful protected Finish RPC can acknowledge a fenced account; it permits ordinary execution reporting only after final bundle capture and independent cleanup, or the separately verified unused-original pre-native outcome. Acknowledgment alone cannot replace the started job claim.

Once subscription ownership is recovery-required, every new Finish is denied before vault staging or deletion and again at its final transaction. Only an already accepted receipt may replay through this handler; that read-only replay preserves any later retained lease, pending operation and recovery fence. A late background completion cannot replace the independent recovery path.

Remote transport retains authenticated TLS; loopback development retains the existing protected local RPC boundary. Authorization is rechecked after vault I/O and at publication, including the original initiating client's revocation. Managed login starts with no existing authentication file and requires the original native login-completed observation. Bundle validation rejects API keys, external auth modes, unknown credential fields, foreign identities, symlinks and oversized files. JWT claim comparisons are identity consistency checks, not signature verification or substitutes for native OAuth completion.

Login confirmation, refresh and logout share one strict native `account/read` response profile. Codex 0.159.2 serializes the known optional `workspaceRouting` field even with `experimentalApi: false`. Omission and null remain valid for earlier/native disconnected responses. A present object requires the exact original bundle's `chatgptAccountId`, a nonempty account ID of at most 24 KiB without credential-field whitespace/control separators, an HTTPS origin of at most 8,192 bytes without user information, path, query or fragment, and the closed `accountRoutingOverride` enum `NO_CONSTRAINT`, `us` or `us_cr`. An explicit origin port must be nonempty decimal in the unsigned 16-bit range. Discard this metadata after validation; it cannot select Go endpoints, supply credentials, grant authentication or enter resources, receipts or logs. Continue rejecting unknown and duplicate fields at every object depth. Logout requires explicit `account: null`, required OpenAI authentication, absent/null routing and independently absent native `auth.json`. The original native login completion and independently changed file/token evidence for refresh remain mandatory; compatibility never clears an existing recovery fence.

The merged native configuration must retain file storage, ChatGPT login and the built-in OpenAI provider without inherited endpoint, command authentication, bearer, query or header overrides. Native execution never inherits unrelated system logins. Private files do not promise an OS sandbox against unrestricted same-user access. Managed execution accepts `default`, `read-only`, `workspace-write` and `full-access` for both API and subscription accounts. Default leaves native sandbox selection unset; full-access permits native tools to access managed authentication files. Keep the fresh `CODEX_HOME` outside every accepted workspace root after canonical-path checks and reject overlapping/aliased paths before writing `auth.json`. These location and cleanup checks do not promise isolation from unrestricted native tools. All tests use isolated temporary state and synthetic credentials.

Both native thread start and resume recheck the merged configuration for the actual execution workspace before sending the mutation. A successful startup-directory handshake cannot authorize a workspace-specific provider or authentication override.

Thread publication and checkpoint retention/read also bind the native provider to the immutable accepted authentication profile. Managed execution requires built-in OpenAI; API execution retains its existing relay provider. The private checkpoint comparison copies the profile only from accepted configuration, preserves API checkpoint bytes and cannot promote an uncertain subscription lease into recovery authority.

Managed execution captures native identity and the final authentication bundle once before closing the native wire, including before the normal terminal Close. Earlier exits perform the same bounded read before deferred closure, with no automatic retry. After native/process closure, credential cleanup requires the remaining auth file to match those captured bytes, scans retained history and only then submits protected write-back. A terminal event alone establishes none of these cleanup or authentication facts.

Credential cleanup scans retained native files for raw token material and padded or unpadded standard/URL Base64 copies under the existing file/count/byte bounds. Finding a remnant leaves cleanup unconfirmed and retains recovery ownership without erasing the original native history.

Ordinary Codex closure fences new protocol writes, closes stdin on the retained original process and allows at most three seconds for native EOF shutdown before forced cancellation. Join the original process controller and any input-close operation before returning. Codex's temporary helper aliases normally retire through native destructor cleanup; forced cancellation can leave them behind. Protocol failure, caller cancellation and expired grace retain immediate original-owner termination. Managed execution additionally retains the original startup helper proof described below. The subsequent authentication scan still refuses remaining symlinks and credential remnants. A graceful exit is process evidence only: it cannot release an existing recovery fence, change a protected completion receipt or establish bundle/file cleanup by itself. Closed structured shutdown logs distinguish timeout and joined cleanup without paths or native content.

### Original managed Codex execution helpers

Before launching the original managed Thread process, pin the canonical private native home, open directory identities for `tmp/arg0`, the canonical verified executable's open identity, and a bounded inventory of at most 32 existing helper-directory entries. After initialized native startup and before product input admission, retain a private in-memory manifest bound to the exact original process identity and execution owner. On Unix, only one newly observed helper directory can supply retirement authority for exactly `apply_patch`, `applypatch`, and `codex-execve-wrapper`, plus `codex-linux-sandbox` on Linux only, matching the pinned Codex startup alias set per OS (macOS and other Unix hosts create the first three; Linux creates all four). Capture each original symlink identity and its exact canonical executable target. Existing directory names cannot supply new removal authority. An initialized startup with no new directory records no alias authority; later links still fail the retained credential scan. Windows regular batch scripts grant no Unix alias removal authority.

After independently joining the original native tree, validate the canonical home, executable and retained directory chain without following helper entries. Compare the complete original alias inventory before the first unlink, then recheck each original alias at its own anchored removal boundary. Retire only those unchanged aliases and synchronize their retained original parent. Native destructor removal can satisfy original absence only through the unchanged retained parent and its synchronization. Absence of an original basename also requires a bounded scan showing that no captured directory or alias identity survives under another basename in the retained parent or helper directory; a renamed or hard-linked identity leaves cleanup unconfirmed and is never deleted or adopted. Do not erase the helper directory, lock files, unrelated entries or native history. Missing startup proof, ambiguous new directories, changed or transplanted ownership, alias targets, executable or directory identities, and removal/synchronization failures leave cleanup unconfirmed. The final check-to-unlink interval is not an adversarial same-user filesystem sandbox.

A failed original managed Thread startup after capture uses the same order: only a confirmed original native join permits retirement of that startup's captured proof before handles are released. A retirement failure is returned as recovery-required together with the original startup error so the Worker retains the credential lease and still runs its final comparison and scan. The error carries a joined-tree marker; Worker callers run the unused-original authentication cleanup for a marked failure but never report `CleanupConfirmed=true` while helpers remain unretired.

Run the final protected `auth.json` comparison/removal and the complete bounded raw/encoded-credential scan after a confirmed original process join even when helper retirement fails. Both helper retirement and authentication cleanup must succeed to report `CleanupConfirmed=true`. Successful final bundle capture can report `Succeeded=true` for subscription Finish while the original execution remains failed after accepted input. Preserve that failure, original history, once-only reporting and input delivery; never replay input or infer execution success from credential cleanup. Historical recovery records cannot reconstruct this in-memory startup proof and are never cleared automatically. Arbitrary links, pending files, special files, credential remnants and the existing inventory limits retain their recovery fences.

Server-owned access-only quota refresh (`QuotaProtocol`, no managed authentication) uses this same original-startup proof with its canonical native home. Pin the home, helper directories, executable and bounded inventory before launching the quota native process, and capture the initialized original aliases after startup and before `config/read` verification, external authentication or any quota read. After the original native tree is joined by ordinary `Close`, retire only the unchanged captured aliases exactly once, before the server runtime performs its `auth.json` absence check and `subscription.CleanupRuntime`. A successful read publishes quota and a failed read settles as failed with `CleanupConfirmed=true`, so later eligible refreshes are not blocked. A failed quota startup after capture follows the failed-startup order above: retirement happens only after the confirmed join, a retirement failure stays recovery-required so the failed-open path skips runtime removal, and a successful retirement lets the existing failed-open cleanup remove the runtime. Missing startup proof, a replaced alias inode or target, a renamed helper directory, a hard-linked or foreign alias, or an unconfirmed process join keep `CleanupConfirmed=false` and the uncertain phase, and unlink nothing. Access-only quota still performs no inference, token renewal, managed-authentication creation or credit consumption, and the generic symlink refusal, file/byte limits and `auth.json` absence check of `subscription.CleanupRuntime` are unchanged. `CreditProtocol` and `SubscriptionProtocol` do not capture this proof. Historical quota runtimes that retain the original aliases (three, or four on Linux) without an in-memory startup proof stay fenced across restart reconciliation, and no removal authority is inferred from their filenames or targets.

Helper diagnostics, including those of quota owners, use only the original operation reference, closed `startup`/`retirement` stage and `verified`, `missing-startup-proof`, `identity-changed`, `inventory-invalid`, `alias-changed`, `unlink-failed` or `sync-failed` reason with bounded alias/removal counters. Independent execution-authentication diagnostics identify `auth-file` or `retained-credential-scan`, closed verification/filesystem/bundle/identity/link/pending/special-file/limit/remnant reasons and bounded inventory counters on failure. Do not log paths, targets, native messages, account identities, contents or credentials. Controlled original-helper, changed/foreign/historical proof, injected filesystem failure, accepted-input extension abort and retained credential fixtures are separate from installed native/account/platform acceptance.

## Logging

Use structured `slog` events for accepted operations, grants, completion, capability availability and recovery failures. Log only opaque account/operation/lease/machine identities, closed action, cleanup classification and stable error code. Native protocol messages, token bundles, JWT identities, URLs, device codes and filesystem paths never enter these logs.

Managed native failure logs additionally retain the closed internal `stage`: `login-start`, `login-completion`, `login-cancel`, `account-read`, `bundle-validation` or `local-logout`. The stage identifies the first failed check within its existing safe phase and original operation correlation. It does not change public diagnostic schemas, retain native field values or authorize retry.

Server cleanup failures additionally emit `server_subscription_cleanup_failed` with the original operation reference, closed stage/reason and bounded `file_count`, `observed_bytes`, `file_limit` and `byte_limit` counters. Stages distinguish `native-process`, `auth-file`, `runtime-validation`, `runtime-inventory`, `runtime-removal` and `runtime-sync`. Reasons distinguish unconfirmed native cleanup, failed protected reads, bundle mismatch, filesystem/identity failure, symlink/non-regular entries and file/byte limit overflow. Inventory counters stop at the first failure; an individual file size above the byte limit is capped at limit plus one before accumulation to prevent overflow. No path, filesystem error text, native output, content, identity or credential is retained. Internal typed runtime failures preserve the original public recovery error and cannot alter cleanup proof, account state or retry authority.

## Build and Test

Run `go test -race ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, `pnpm proto:check`, and the API client's tests/typecheck. Generate bindings through pinned root Buf tooling. Controlled native-process fixtures cover browser/device progress, completion, cancellation, file rotation, unchanged refresh evidence, logout and symlink refusal. Real loopback Connect/SQLite fixtures cover lease races, protected-channel authorization, generation fencing, lost write-back, cancellation, identity uniqueness, independent accounts, API relay denial and secret-free outputs/database files.

These fixtures do not authenticate real accounts, execute hosted inference or establish installed-Codex/desktop/Windows/Linux/release acceptance. Record actual executed checks, source revisions and unresolved limits in the feature, its pull requests and CI logs/artifacts.

Lifecycle fixtures verify normalized plugin disablement before any login request, rejection of incomplete observations, unchanged thread feature policy and sequential independent account login without changing the first saved configuration or protected generation. Cleanup fixtures preserve exact file/byte limits, identity/symlink refusal, auth-file equality and secret-free failure counters. The opt-in installed empty-home logout smoke waits for asynchronous startup work, rejects plugin cache creation and checks joined private-runtime removal after native closure. It uses no browser, real account credentials, OAuth completion or inference.

Account-response fixtures cover omitted/null/validated routing across browser/device completion, refresh and logout, malformed/foreign metadata, unknown/duplicate fields and secret-free stage logs. The explicit `DELIDEV_NATIVE_INITIALIZE_EXECUTABLE` empty-home logout smoke exercises an installed Codex's actual `account/read` response and joined process/private-runtime cleanup without opening a browser, authenticating a user or invoking inference. This check cannot establish real OAuth completion or packaged-platform acceptance.

## Dependencies and Integrations

Reuse authenticated Connect, the server vault, current Worker discovery, owned process supervision and existing Codex session/history publication. No provider brokerage, native retry emulation, account/model failover or new dependency is added.

## Change Triggers

Update the account/harness/session/protocol/client contracts and affected scoped `AGENTS.md` owners when the native profile, public operations, generations or recovery boundaries change. Record implementation status and validation in the feature, its pull requests and CI logs/artifacts; do not add repository evidence documents. Update the project index only for ownership, domain catalog or cross-domain invariant changes.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [Project index](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Credential storage](cmds-delidev-credentials-contract.md)
- [Native harnesses](cmds-delidev-harness-contract.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Wire contract](protos-delidev-v1-contract.md)
- [Pinned official account protocol](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server-protocol/src/protocol/v2/account.rs)
- [Pinned native authentication storage](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/login/src/auth/storage.rs)
- [Codex 0.159.2 account response profile](https://github.com/openai/codex/blob/ff6aec96948b70d94983af2641a6b67c94faeff5/codex-rs/app-server-protocol/src/protocol/v2/account.rs)

## Native quota and reset credits

System capabilities 18 (`SUBSCRIPTION_QUOTA_V1`) and 19
(`SUBSCRIPTION_RESET_CREDITS_V1`) negotiate the two product operations separately.
Worker capability 8 retains the verified managed Codex profile for fresh idle native-owner admission. Active original Execute quota reads use the initialized execution protocol as described below. The
pinned upstream source is commit `78c290807ce710180111df227df3b7a4fe845452`.
`account/rateLimits/read` omits its unit parameters; the native wire encoder's
explicit `OmittedParams` profile preserves the ordinary structured-parameter
requirement and common bounds. No inference or billing probe supplies quota.

Go queues one bounded observation per supported connected account,
using the last original native owner's machine. Inactive maintenance and the foreground cadence are described in the server-owned quota V2 section. Individual and whole-inventory
refreshes are authenticated SubscriptionService operations with UUID-v7 receipts;
refresh-all derives its complete account set on the server, independent of client
pagination. An idle read takes a short exclusive credential lease. An active
execution uses its registered original Codex process and lease; it cannot create
a second credential writer. Explicit-original, omitted-machine, Refresh all and due
maintenance use the same active-execution eligibility. For that read, missing or
unverified saved installation discovery and numeric version metadata cannot deny
an already initialized direct-v4 execution. Admission and send/publication retain
the original account, connection, credential generation, Execute lease/revision,
server epoch, authenticated Worker device, current machine/instance, negotiated
managed subscription and observation capabilities, and initiating actor. The
Worker observation registry uses only the actual original initialized Codex
client; an absent registry owner permits no read or replacement process. Idle
quota, lifecycle operations and reset-credit consumption/reconciliation retain
their existing saved-installation and independent cleanup requirements. This
boundary adds no RPC, capability or database migration. Original native rate-limit updates pass the same
bounded projection. Failed observations preserve the last successful timestamp,
values and exhaustion state. Sparse null fields retain original values, window
identities and individual observation times. Comparable windows use their minimum
remaining fraction; elapsed resets cannot establish recovery.

The server-owned optional subscription observation records retain exact account,
connection, credential generation, machine, actor and original operation identity.
A durable send claim precedes every explicit native request. Claim replay never
grants another send. Queued operations become stale on credential generation
replacement, rather than silently inheriting a new generation. Stop, revocation,
pending lifecycle cleanup and lost native ownership retain their original fences.
Only confirmed native/file cleanup releases an idle credential lease.

Reset-credit inventory preserves the authoritative signed-64-bit count separately
from a bounded detail list. Null details mean unavailable; an empty list is an
observed empty list. Native credit IDs, reset type/status and grant/expiry times
are metadata; native titles, descriptions, unrelated billing content, account identities and token
reflection never enter resources, receipts, logs or history. The desktop requires
explicit confirmation tied to the displayed account revision, connection,
generation and inventory identity. The CLI requires `--confirm`, with either the
returned credit ID or explicit native next-credit selection when only the count
is available. Configuration saves cannot manufacture observations.

Codex quota reflection validation checks exact original bucket IDs after removing only the adapter-owned final `primary`, `secondary` or `spend` suffix, and exact reset-credit IDs, against every nonempty protected identity/token and its complete standard/URL Base64 forms at any length. Both managed Worker and server-owned reads include the display name parsed from the original ID-token name or profile claim. The external access-token-only adapter has no parsed display-name source and retains its existing inputs. Existing long substring checks remain; incidental short substrings stay valid. Worker explicit reads, rolling updates and server-owned quota reads share this guard before publication. Rejection preserves the last good quota and exhaustion state independently of native cleanup.

The accepted UUID-v7 operation is the official `idempotencyKey` for
`account/rateLimitResetCredit/consume`. Uncertain consumption never automatically
requeues or receives a replacement key. Explicit generation-checked
`ReconcileSubscriptionCredit` preserves that original key and selected credit.
Closed outcomes retain `reset`, `alreadyRedeemed`, `nothingToReset` and `noCredit`.
The latter two describe truthful non-reset outcomes. Every attempted consumption
performs a separate quota read afterward; failure retains previous quota and
cannot manufacture a recovery notification.

Recovery notification preferences default to false. Only a fresh observed
transition from confirmed exhaustion to positive usable quota creates an Inbox
entry, atomically with account publication. The original observation source index
deduplicates it across receipts. Recovery Inbox records have account/connection
scope and no session, interaction or execution authority. Existing independent
read-state and once-only notification presentation claims apply. Native delivery
accepts only the opaque original claim/inbox IDs and a closed recovery kind; its
fixed title/body reveal no account alias, provider identity, quota value or secret.
Presentation is available only while the trusted desktop process runs.

Validation must distinguish isolated native/provider processes, SQLite and
response-loss fixtures from installed real-account credit consumption, quota
recovery and platform notification acceptance. Keep the issues open until their
remaining real-environment acceptance is satisfied.

Quota maintenance performs a read-only due check and transactional eligibility recheck, publishing a durable receipt only for a changed batch. Account deletion transactionally removes its account-scoped recovery Inbox entries through ordinary event/tombstone publication. Idle quota/reset-credit publication uncertainty remains an original-operation problem when the unchanged valid bundle and independent native/file cleanup are confirmed; finish the credential lease without fabricating quota recovery, then permit only explicit same-key credit reconciliation. Failed credential capture or unconfirmed cleanup still fences the native owner.

Unpublished quota reads become failed after independently joined successful native credential/process/file cleanup. Preserve the last good quota values and allow a new explicit or scheduled read; consumption uncertainty remains reserved for reset-credit operations with the original official key. Migration 28 bounds relevant configuration/device metadata and checks affected original session/job references in SQLite without loading unrelated retained history.

### Native version and failure diagnostics

Codex sign-in attempts share the harness contract's minimum SemVer `0.151.0`, with no upper bound. Discovery of an executable, verified initialization and actual account login remain separate. Server operations retain optional safe `diagnostic` metadata in their existing versioned document; no SQLite migration is needed. Progress publishes it only to the authenticated original initiator. Retain the actual detected version, minimum, closed discovery/version/profile/runtime/launch/initialize/confirm/login/models/execution/history/cleanup phase, stable error code, locally reconstructed reason/guidance and original operation correlation ID. Validate closed metadata rather than retaining arbitrary Worker/native error strings.

Missing executables report discovery with no detected version. Invalid or lower versions report version validation; newer versions run actual initialization. Keep the first native failure when cancellation, process cleanup or protected-file cleanup subsequently fails; the independent recovery state and cleanup result remain authoritative. A cleanup-only failure uses cleanup phase. Persist metadata through restart and transient presentation disposal without login URLs, device codes, credentials, identity, paths or raw output. Success clears failure metadata. Logs retain bounded version/phase/code/operation correlation and separate original outcome from cleanup/recovery, without account identifiers. No diagnostic may trigger login replay, callback resend or automatic retry.

## Server-owned ChatGPT quota

System `SERVER_SUBSCRIPTION_QUOTA_V1 = 46` separately negotiates quota reads for idle server-owned ChatGPT accounts. For this issue, the owner's explicit batch instruction permits allocation and activation in one complete feature PR without a prior main reservation merge. This exception changes no other allocation, migration or native authority rule.

The existing QUOTA request can omit `machine_id` under capability 46. Explicit machines retain capability 18 and the strict Worker claim/receipt lane. An active execution uses its original Worker process and lease, including an omitted-machine request resolved to that original lease. Worker reset-credit consumption retains capability 19 and original Worker ownership. The separately negotiated server lane is defined for this feature below. Authentication success or capability 30 alone grants no server quota authority.

Optional `server_quota_generation` records eligibility only after successful original server login/authentication refresh and validated publication of the generation. First observation, complete Refresh all and four-minute maintenance derive eligible accounts on the server. Inactive server maintenance becomes due when the same-generation server quota and Worker observation request times are each at least the shared four-minute maintenance interval old (`subscriptionQuotaMaintenanceInterval`): nothing is admitted at 3m59.999s and exactly one owner at 4m, including under concurrent scans. This advance margin is separate from the unchanged five-minute freshness limit used by the rail, paid-credit and domain routing; failures and omissions never extend freshness. While an authenticated product window is visible and focused (app use), one connection-owned automatic Refresh all cadence admits the complete server-derived eligible inventory every 30 seconds through the existing `RefreshAllSubscriptionQuotas` method, capability 50 and a fresh UUID-v7 request ID; see the desktop contract. Acknowledgement is admission, never native evidence. A previous settled server generation can become eligible at startup only from its original successful non-native-active owner, valid current protected reference. Missing, restored or uncertain evidence grants no eligibility. No SQLite migration or credential format conversion occurs.

The independent `server_quota` record retains original actor, server epoch, operation, finish receipt, connection, credential generation, request time and closed queued/sending/succeeded/failed/uncertain phases. A durable claim grants one bounded quota read in the verified original Codex process. The shared private-home adapter performs no inference, login, authentication refresh or credit consumption. It uses sanitized native environment and checks the explicit server network selection before native startup. The current subscription profile has no proxy adapter; non-Direct selections return Unsupported without direct fallback, network traffic or proxy credential access.

Server quota ownership excludes lifecycle requests, Worker credential grants, observations, configuration deletion, saved-storage diagnostics and database restore. Sixteen joined account tasks run independently; shutdown cancels and joins them before protected stores close. Original native/process/file closure and unchanged final authentication bytes precede the durable cleanup checkpoint and quota/result publication. Read failures after confirmed cleanup retain last-success windows/time and permit a later explicit or due read. Unconfirmed cleanup retains original recovery and cannot relaunch. Publication rechecks actor, generation and connection; stale or revoked reads settle as failed after cleanup without publishing windows. Restart never repeats a previous queued or sending operation. Independently checkpointed cleanup can settle a failed original read; absent confirmation preserves recovery.

Successful projection shares sparse-window merging, minimum remaining fractions and atomic recovery Inbox deduplication. Elapsed resets alone grant no recovery. Frontend and CLI refresh negotiate capability 46 before omitting the machine, retain exact revisions/selectors/retries and expose unsupported/failed/pending states. Capability 46 alone grants no server reset-credit consumption; that operation independently requires capability 49. Structured logs contain operation/generation references, closed phases, safe error codes and cleanup confirmation only.

The owner will perform real-account, installed-native, remote and platform acceptance separately. This batch's automated fixture/build validation does not establish those results; that skipped acceptance is nonblocking for this feature PR under the owner's explicit instruction.

Managed ChatGPT Sidechat follows [the feature’s protected Fork profile](cmds-delidev-sidechat-contract.md#managed-chatgpt-sidechat). EXECUTE Take admits only its exact claimed original Fork job. Protected Finish writes a metadata-only original-job receipt after actor/source/lease rechecks; that receipt gates child publication. Independent managed Fork follows the separate System 53 / Worker 29 profile in the Fork contract and uses the same original-job receipt lifecycle.

Managed subscription installation refusal describes missing verified Codex installation evidence, not an exact or minimum native version. Existing bounded version metadata, detected state, path, protocol, observation and capability predicates remain authoritative. Safe guidance directs the user to the selected Runner Device’s installed Codex and protocol verification without exposing native paths or output.

## Server-owned ChatGPT reset credits

Managed Sidechat admission rejects an active original server-credit owner before
creating a Fork job. Protected Take and Finish retain their original account,
credential generation and independent cleanup fences.

System `SERVER_SUBSCRIPTION_RESET_CREDITS_V1 = 49` independently enables explicit
reset-credit consumption for an idle eligible server-owned ChatGPT account.
Record allocation and complete implementation together under the allocation
workflow. Preserve capabilities 19, 30 and 46. Reuse RequestSubscriptionObservation
and ReconcileSubscriptionCredit without a new RPC, Worker capability, database
migration or credential conversion.

This section describes the historical System 49 managed-auth lane, which keeps
serving its accepted operations. New omitted-machine requests use the System 101
access-only lane described in [Server-owned ChatGPT reset credits V2](#server-owned-chatgpt-reset-credits-v2).
An omitted machine selects the original settled server credential generation
and protected AccountLogin reference; explicit-machine requests retain the
Worker lane. Never manufacture a Runner, transfer accepted consumption or acquire
a second credential writer. Fresh inventory and explicit confirmation bind the
account revision, connection, generation and inventory ID. A positive count
permits an available unexpired exact credit, or explicit native next-credit
selection only when details are unavailable. Unknown, future or stale inventory
requires a successful quota refresh first.

The separate optional `server_credit` record retains the original UUID as the
official idempotencyKey, actor, epoch, connection, credential generation,
inventory identity and immutable credit/next selector. Each explicit attempt
has separate current server epoch, attempt/finish IDs, a durable send claim,
provider outcome and independent cleanup checkpoint. A monotonic ever-sent flag
retains possible earlier consumption even when reconciliation has not sent. Quota-only records retain
their meaning. The shared narrow native selector validates Worker and server
ownership separately. Verify actual native readiness, original authorization and
the configured Direct route before send. Unsupported routing fails before launch
without direct fallback, native traffic or proxy-secret access.

Receipt replay, status, polling, reconnect and restart never relaunch consumption.
Possible-send uncertainty retains the original key and selection. Only explicit
reconciliation by the still-valid original actor, connection and generation can
create a new attempt after independently confirmed original process/file cleanup.
The provider key and selector remain unchanged, including alreadyRedeemed results.
Unconfirmed cleanup keeps recovery fenced. Another actor or new inventory cannot
transfer a retained attempt.

Checkpoint reset/alreadyRedeemed/nothingToReset/noCredit before a separate quota
read and cleanup. Read failure cannot erase consumption, replace last-success
quota or invent recovery. Active credit ownership fences lifecycle, execution
credentials, account deletion, failed-subscription cleanup, saved-storage
inspection and backup/restore. The existing bounded controller joins native tasks
before protected stores close. Cleanup checkpoints remain independent of outcome
and finish publication; lost finish or restart cannot authorize another send.

Desktop Review, Confirm and original-operation reconciliation negotiate 49,
retain exact request retries and preserve English/Korean guidance and Settings
lifetime. CLI consumption/reconciliation require --confirm; omitted-machine
consumption independently negotiates 49. Missing support, zero/unknown inventory,
pending ownership and failures remain truthful product states. Logs retain only
closed operation/code/cleanup metadata without native content or credentials.
Fixture/build/package evidence remains separate from installed-native,
real-account, remote-machine and platform acceptance. Record revision, commands,
results and unresolved limits in PRs, issues and CI artifacts.

## Server-owned ChatGPT quota V2

System `SERVER_SUBSCRIPTION_QUOTA_V2 = 50` expands omitted-machine QUOTA, Refresh all, four-minute inactive maintenance and the foreground 30-second automatic Refresh all to the selected server, independent of Worker registration, connectivity, verified installation, retained Worker ownership or an active Execute lease. Record allocation, declarations, generated bindings and complete activation together in the owning feature PR under the allocation workflow. Preserve System 18, 46 and 49 and explicit-machine legacy QUOTA/reset-credit semantics. No Worker capability, RPC, credential conversion or SQLite migration is added. Clients require capability 50 before issuing the expanded omitted-machine or batch request; older servers receive compatibility guidance without an automatic Worker fallback.

V2 uses a separate quota-only native profile. Go reads the exact sealed AccountLogin generation, validates the original account/user commitment and supplies only access token, account and plan to official `account/login/start` external `chatgptAuthTokens` authentication. The native process uses ephemeral authentication, disabled plugins and the official provider, receives no ID or refresh token and writes no managed auth.json. Refuse every external-token refresh callback with one fixed safe error; rejected/expired access tokens fail the observation without renewing credentials. Native initialization includes bounded configuration and readiness reads and is not claimed to emit only the quota HTTP request. Validate and discard the pinned external login’s once-only `account/login/completed` success notification (`loginId`/`error`/`onboardingEntrypoint` null or omitted, typed `success: true`) before the quota request; failed, foreign, malformed and unknown completion fields fail closed. This private completion metadata grants no managed-login, callback or renewal authority. Use one bounded `account/rateLimits/read`, existing strict sparse projection and explicit server network routing. Unsupported server profiles fail with server guidance and never select a Worker.

The V2 durable quota operation records `access_only: true` to separate its ownership from historical managed-auth quota claims. Capture original actor, epoch, operation, connection and generation; recheck them before protected-reference use, before external authentication and before publication. Lifecycle changes, removal, actor revocation and unresolved account recovery retain their original fences. Serialize account admission and protected-reference capture only for bounded store/vault work; hold no account transaction or lock during native/network work. Cancel the original read on binding changes and discard stale results.

Access-only quota can coexist with an original Execute credential writer. Its durable process/cleanup obligation and retained AccountLogin reference are independent of that lease. Execution completion can rotate its generation, but must protect the captured observer reference until quota process cleanup is confirmed. Before terminal quota publication, account serialization validates the original operation/actor/connection/generation. The obsolete captured AccountLogin reference is then retained, not deleted: it grants nothing and only explicit logout or account deletion removes it, so terminal settlement performs no vault work and writes no retirement fence. Preserve the current generation, every other reference and any still-owning lease/observation/credit/native writer. A failed publication retains the original quota fence with its durable native-cleanup checkpoint; it never changes Execute ownership. Prior-epoch confirmed native cleanup settles without reopening native work. Rejected queued claims use positive no-native-work cleanup through the same settlement boundary. A retirement-pending fence written by an earlier build is released by the legacy transition after the same account, operation and generation checks, without deleting the retained reference. Quota cleanup uncertainty blocks another quota launch, lifecycle/reference deletion and database restore; it does not release, replace or fail the original Execute lease or account. Restart and receipt replay never send an accepted quota operation again. Startup reconciles prior-epoch quota cleanup only through the retained original process owner index and independently released controller proof, plus bounded original private runtime/probe cleanup. Missing ownership evidence remains uncertain; reconciliation never launches authentication or a quota read. Join quota tasks on shutdown. Historical quota claims without access_only retain their exclusive managed-auth interpretation and are never relaunched through V2.

Preserve last-success quota windows/timestamps on failures, truthful pending/failed/stale status, observed-recovery deduplication and bounded private reflection checks. Log only closed phases/errors/cleanup and operation identities, excluding authentication, user/account identities, quota values and native bodies. Synthetic protocol/ownership fixtures and build checks remain distinct from real native/account/remote/platform acceptance, which the owner performs separately and has explicitly made nonblocking for this batch.

## Server-owned ChatGPT reset credits V2

System `SERVER_SUBSCRIPTION_RESET_CREDITS_V2 = 101` independently negotiates server-owned reset-credit consumption that needs no Runner Device and may run beside an active same-account Execute. Record the allocation, generated bindings and complete activation together under the allocation workflow. Preserve System 19, 30, 46, 49 and 50, Worker 52 and every historical accepted operation. Reuse RequestSubscriptionObservation, ReconcileSubscriptionCredit, SetAutomaticResetCreditConsent and the Worker marker publication; add no business RPC, Worker capability or SQLite migration. Clients require capability 101 before a new omitted-machine consumption. Older servers receive the existing server-update guidance and never fall back to a Runner Device. An explicit machine selector keeps the historical Worker lane, and an accepted Worker or System 49 operation keeps its original lane, controller and interpretation.

An omitted selector selects the exact sealed AccountLogin generation and creates a `server_credit` record with `access_only: true`. The historical managed-auth record shape is unchanged. The server admits it while the account has no Runner, a disconnected or unverified Runner or an active same-account Execute, and takes no credential writer, lease, native process or write-back from that Execute. Fresh inventory and explicit confirmation bind revision, connection, generation and inventory ID, with an immutable exact credit or count-only next selection and the original UUIDv7 provider key. Before the first send the server rechecks the confirmed inventory, including available status, expiry and that the grant time is not in the future.

Go reads the sealed bundle under short account serialization, validates its original identity commitment and passes only access token, account and plan to the distinct credit native profile ([harness contract](cmds-delidev-harness-contract.md#server-access-only-reset-credit-profile)). Hold no account transaction or lock during native or network work. Verify the configured Direct route and actual readiness before send. Persist exactly one durable send claim before the official consume request, checkpoint the closed outcome, then run a separate quota read. A failed read cannot erase consumption, replace last-success quota or invent recovery.

Before the send claim, any change cancels the original read and spends nothing: revoked actor, connection or credential-generation change including the Execute's own rotation, epoch change, competing owner or restart. A canceled claim settles as `canceled` through the same settlement boundary. After a possible send the operation keeps its original outcome or uncertainty. Receipt replay, polling, restart and status never resend. Only explicit same-key reconciliation by the original actor, connection and captured generation can create a new attempt after independently confirmed process and private-file cleanup; it follows the captured generation across Execute rotations and never borrows live consent or a replacement generation.

The captured AccountLogin reference is protected through Execute settlement and generation rotation until joined native cleanup is confirmed. Settlement then confirms that the current reference exists and retains the captured reference as an obsolete generation (`server_quota_reference_retained`, `server_credit_reference_retained`): it grants no authority and only explicit logout or account deletion removes it. A valid provider outcome whose checkpoint failed is recorded durably before settlement, so a crash still leaves the outcome for reconciliation. An uncertain possible send keeps its reference for reconciliation. A failed publication retains the original credit fence. Credit cleanup uncertainty fences only the credit owner, lifecycle, deletion and restore; it never fails or releases the Execute lease and never changes its native process or credential writer. Startup reconciles only prior-epoch cleanup with a per-owner bound and never sends.

### Automatic server consumption

The existing default-off consent, exact original failed Execute marker and Worker 52 publication are unchanged. With confirmed consent and a valid marker, one account transaction freezes a `ServerAutomaticCreditProof` (episode, server epoch, original lease and job, Worker device and instance, lease revision, exact consent snapshot with its ConfirmedAt timestamp, and the typed block) in the episode and the server operation, and reserves the generation for server ownership. The same transaction queues either the one stale-inventory server quota read tied to that episode or the single selected credit. Worker 52 receives no consumption observation for the new lane, and a Worker cannot claim or publish a server operation. Cleaned, idle, foreign, Sidechat and Fork sources, free text, overload, session budgets and paid-spend limits cannot admit.

Before the original lease finishes, the exact consent snapshot, lease and generation must remain current; consent revocation, replacement (including disable then re-enable) or any actor, connection, generation or epoch change cancels the unsent claim with zero spend. The original lease's first successful Finish clears consent and rotates the generation. If the exact consent was still current, that Finish records the successor generation on the episode, so Worker cleanup or disconnect after admission does not revoke the claim, while a later rotation, revocation or restart cannot adopt it. Selection (earliest expiry, grant time, ID), one operation per episode, the 1,024-marker bound and fresh-observed-recovery rearming are unchanged. The refresh settles its quota result first; its captured reference is retained whether the same transaction admitted the credit or declined to, and no retirement-pending fence is written. The legacy fence of an earlier build protects the captured generation from rotation cleanup and blocks new server reads until the legacy transition releases it (startup reconciliation and a slow background retry run that release, which deletes nothing).

Historical Worker-owned automatic observations remain on their original lane. Logs carry only closed phase, code, operation and cleanup facts; they never contain credentials, account identity or native bodies. Fixture and build evidence stays separate from installed-native, real-account, remote-machine and platform acceptance, which this change does not perform.

## Managed ChatGPT automatic titles

`GenerateSessionTitleJob` version-3 assignments use the existing `SUBSCRIPTION_ACTION_EXECUTE` lease under the [session title contract](cmds-delidev-session-titles-contract.md#protected-chatgpt-subscription-titles). `TakeSubscription` admits only the exact claimed title job on its original Worker process, requires the frozen settled generation and identity lineage, and commits the one-time inference claim with the lease; the lease records the claimed title job revision. `FinishSubscription` revalidates structural lineage before vault staging and again inside the receipt transaction, without session-state checks that would fence the account, and writes a metadata-only `managed_title` receipt only for a successful, cleaned Finish. Automatic reset-credit episodes never attach to a title lease. Busy, refreshing or recovery-fenced accounts keep the title queued or uncertain and never redistribute credentials. Between auxiliary admission and the title's own Take, a claimed title reserves its account: primary `TakeSubscription`, refresh requests and Worker observations return a definite `RESOURCE_EXHAUSTED` busy result until the inference claim commits, cancellation is requested, the job settles or the claiming Worker instance is no longer live. A title `Take` denied before its transaction commits, at any gate (account state including an accepted logout, recovery fence, job claim, authority, generation, capability or cancellation) while its one-time inference claim is unconsumed, returns the same typed `subscription_title_not_delivered` result with code `CANCELED` and commits nothing; a consumed claim keeps the generic recovery denial. A title `Take` that fails after its lease commits but before any credential returns releases the exact lease and returns the typed `subscription_title_not_delivered` result (never `RECOVERY_REQUIRED`), which the Worker treats as a definite no-delivery failure rather than an uncertain execution; an unconfirmed release keeps the recovery fence. System 59 / Worker 32 `MANAGED_CODEX_TITLES_V1` add no RPC, action or migration.

## OpenCode Go subscriptions

The [OpenCode Go contract](cmds-delidev-opencode-go-subscription-contract.md) owns the exact key-backed `opencode_go` exception, fixed server relay profile, original native session header and independently confirmed cleanup. Identity 4, System 54 and Worker 28 retain separate ownership; System 52 remains Project behavior. Native login and quota authority remain unavailable. No migration is added.

Independent managed Fork now follows the separately negotiated System 53 / Worker 29 profile in [the Fork contract](cmds-delidev-forks-contract.md#managed-chatgpt-independent-fork). It reuses the exact accepted Fork EXECUTE lease and durable Finish receipt, including original generation, actor, cleanup and uncertainty checks. Legacy internal Sidechat-named receipt storage also serves this independent profile without changing existing receipt bytes or requiring a migration.

## Situation quota exhaustion notifications

Only ChatGPT quota observations can publish this metadata-only operational Inbox kind. The original connection must move from complete fresh known usable evidence to explicitly confirmed exhausted evidence in a strictly ordered successful observation. Unknown/sparse/stale/failed observations, credit reset, reconnect, import and restore cannot establish that transition. Notification preference and display claims do not alter per-account quota recovery consent or start recovery. Follow `cmds-delidev-inbox-contract.md`; native/account acceptance remains independently recorded.

## Automatic reset-credit consent

System `AUTOMATIC_RESET_CREDIT_CONSENT_V1 = 77` owns the closed
`SetAutomaticResetCreditConsent` revision-checked mutation. Worker
`CODEX_QUOTA_BLOCK_V1 = 52` owns bounded original failed-turn quota markers.
Record declarations and complete implementation together; no migration or new
native action is added. Existing manual consumption, server credit lanes and
explicit Resume retain their independent ownership.

On a shared account owner, an automatic-credit episode and its frozen server
proof bind the lease, job and revision of the running member whose session
reported the quota block, never the founding owner's. The claim is authorized
only while that member is running; a finished or retained member, or the
founding member's unrelated job, cannot continue it.

Consent defaults off and binds the authenticated actor, account connection and
credential generation. Enabling requires explicit confirmation; disabling is a
separate exact mutation. Logout and every replacement credential generation clear
consent. Portable exports remove subscription state and restored credentials clear
it. Generic configuration saves must preserve the entire server-owned state, so
older clients cannot omit these fields or manufacture consent.

Only the closed original Codex `usageLimitExceeded` marker, or
`rateLimitExceeded` with fresh observed subscription exhaustion, can qualify.
Free text, context limits, session budgets, server overload and paid spend limits
grant no spending authority. The server validates the retained failed Execute
job, immutable selection, native thread/turn, Worker device/instance, server epoch
and original active credential lease before atomic admission. Sidechat, Fork,
idle processes, replaced Workers and already cleaned native sessions cannot spend.

One durable account episode owns at most one UUIDv7 reset-credit operation and
its original provider idempotency key. Repeated errors, status, restart and lost
acknowledgments do not replay sends. Bounded original-turn fences survive fresh
quota recovery so delayed redelivery cannot spend again. Fresh observed recovery
alone rearms a new exhaustion episode, independently of notification preference;
elapsed reset times do not rearm. At the 1,024 retained-marker bound, automatic
admission stops conservatively rather than evicting a spending fence.

A fresh positive inventory selects a copy of eligible, unexpired Codex credits by
expiry (no expiry last), grant time and full ID, preserving display order. A
positive count without individual details permits only native next-credit
selection. Stale inventory permits one read-only refresh on the same original
lease; missing, zero or failed results do not admit consumption. Episodes admitted
under System 101 run on the server instead: the refresh is one server quota read
and consumption one server access-only credit, both frozen to the episode as
described in [Automatic server consumption](#automatic-server-consumption), and
the Worker only publishes the marker. Only a historical Worker-owned observation
accepted earlier still uses the retained observer, which publishes and claims it
once before joined native cleanup. No idle Worker lease may adopt an automatic
operation. Explicit
same-key reconciliation retains the original selector and provider key but removes
automatic lease authority and follows the existing confirmed-cleanup/recovery
contract. Consumption outcomes and quota recovery remain separate.

The approved Settings confirmation uses the existing task lifetime, retained
mutation, theme tokens and English/Korean copy. Closing Settings does not revoke
accepted server processing. No consumption resumes a failed session automatically.

## Paid-credit execution admission

Included-quota exhaustion remains recorded independently from new managed
ChatGPT/Codex execution admission. One shared Go predicate permits that admission
only for the exact `codex` paid bucket with native `hasCredits=true` or
`unlimited=true`, plus independently observed explicit `spend_control_reached=false`.
Each observation must be nonfuture and no older than five minutes at the original
routing or transaction observation time. Missing, stale, future, unavailable or
reached evidence blocks the exception. Exact balance is display-only; never
calculate availability from it or combine buckets.

Routing, fresh dispatch/continuation and new execution-authority registration
apply the same predicate. Keep health, authentication, removal, model/project
compatibility, connection/credential generation, Worker capability and lease
restrictions. Registered original grant/receipt ownership remains authoritative
after credit evidence expires for completion, cleanup and exact registration
replay. Other live ownership fences still apply. A new paid registration also
requires the live selected Worker to advertise `managed-codex-subscriptions-v1`,
as dispatch does; a same-instance reattachment that withdraws it denies new
registration without storing a grant, while an already registered exact grant
keeps replay, completion and cleanup. API, Claude and OpenCode retain
their existing exhaustion rules.

Paid admission never clears included exhaustion, changes quota scores, creates
recovery notifications or rearms automatic reset-credit episodes. Native Codex
remains the final input authority; native rejection retains the existing failure
path without replay, account substitution or automatic Resume. Structured logs
use closed admission classifications and opaque original IDs without balances,
credentials or raw native content. No RPC, allocation or migration is added.

## Paid-credit observations

System `SUBSCRIPTION_PAID_CREDITS_V1 = 76` and Worker `SUBSCRIPTION_PAID_CREDITS_V1 = 51` independently negotiate the narrow ChatGPT/Codex paid-credit projection. Reuse authenticated original quota reads, leases, connection/credential generations, actor checks, joined native cleanup and existing refresh actions. No RPC, purchase, consumption action or SQLite migration is added. An older Worker omits these fields; the server rejects a paid-credit publication from a Worker without capability 51.

Each native bucket owns its exact bounded ID, required hasCredits/unlimited flags, nullable balance and successful observation timestamp. Accept nonnegative plain decimal strings of at most 64 bytes without numeric conversion. Explicit null is unknown, zero is a real value, and unlimited takes presentation precedence. Never sum buckets or infer balance from quota, reset credits, plan type or hasCredits. Omitted credits/balance fields retain the last successful bucket and its timestamp; failed, malformed or reflected reads retain evidence without refreshing it. Credential-generation replacement clears observations. Ordinary configuration saves and legacy clients cannot replace protected subscription observations. Original identity/token reflection checks include paid bucket IDs and exact balance strings, including short/encoded secrets. Raw responses, unrelated billing/display text and credentials remain private.

Account rows show a compact Paid credits card above quota, with a 24px tabular balance, a smaller credits suffix and the original observation status/time. Multiple account-row buckets use a localized balance count and retain separate original timestamps. Manage subscription uses the same numeric presentation in separate labelled buckets in source order. Never aggregate balances.

Format valid nonnegative decimal strings through string arithmetic and locale grouping, with two fractional digits and half-up rounding. Display actual zero as `0.00` and positive values below `0.01` as `<0.01`. Keep the unchanged exact source string available through a labelled information disclosure. Hover or focus reveals it; click, Enter and Space support persistent disclosure and touch. Escape closes the disclosure without closing its parent and retains or restores the original trigger focus when appropriate. Long values and bucket labels wrap within the existing parent scrolling boundary.

Keep loading, unavailable/update, unknown, zero, positive and unlimited states distinct. Unlimited takes presentation precedence. A failed read with no observed balance shows Balance unavailable and the failure explanation, never zero. Retained failures and stale/future observations preserve original timestamps; freshness ends after five minutes and presentation cannot refresh evidence. Use semantic surface, border and text tokens, 8px card corners, compact padding and 12px secondary text in both themes and EN/KO. Preserve account actions, quota semantics, protected projections and credential-generation clearing. Presentation grants no account, execution, native or platform acceptance.

## Server-owned Claude setup-token accounts

System `CLAUDE_SERVER_LOGIN_V1 = 67` and `CLAUDE_MANAGED_EXECUTION_V1 = 68` and Worker `MANAGED_CLAUDE_EXECUTION_V1 = 41` are independent of capabilities 38, 20 and 30. A new Claude account whose first `RequestSubscription` omits the machine becomes `subscription.claude_credential = "server-token"`; device-owned accounts keep execution and machine-bound logout, ChatGPT and API flows are unchanged, and no existing profile is inspected, imported, migrated or converted. A machine-bound Claude `LOGIN` or `REFRESH` is rejected as unsupported, so a device-owned account can never sign in again. Reconnection is new-account-only: a connected, logged-out or device-owned account is never given a server credential, and the token is never replaced.

The server runs the installed, verified Claude Code (SemVer precedence at or above the `2.1.236` baseline) `setup-token` on a PTY in a private runtime under `subscription-runtime/auth/<operation>` with an isolated environment. It requires a joined successful native exit and exactly one distinct, fully validated emitted token. The final PTY capture need not contain the native success heading. The server bounds and privately retains the screen, and never exposes the token or approval code in the UI, logs, transcripts, resources, progress documents or ordinary RPC responses. Safe failure logs may record only closed parser conditions and counts, never native screen text or token bytes. Once the single-use approval code is delivered to that child, the server never polls for or redelivers it; the operation stays pending until the original child exits, is canceled or expires. Unknown, malformed, ambiguous or oversized output, a rejected code, a non-zero exit or uncertain cleanup cannot connect the account. The progress response reuses the existing fields: browser-code login method, original URL, generation, default name `Claude` and the closed native diagnostic.

The token is sealed in a closed, versioned envelope bound to server, account and generation in the existing `AccountLogin` vault ownership and published through its staging receipts. Its keyed commitment proves possession of that credential and is not a human identity. A Worker advertising capability 41 receives the envelope only in the protected Take response of one exclusive original execution lease; a legacy-only Worker never does. A replayed Take receipt never redistributes it; when the exact original lease is still unfinished, the replay fences it as recovery-required so the original Worker can confirm cleanup, and a replay after joined cleanup changes nothing. The Worker sets `CLAUDE_CODE_OAUTH_TOKEN` only in the supervised Claude child, uses a fresh private configuration directory, never passes `--bare`, and clears its buffers after joined termination. Finish with joined cleanup releases the lease; unconfirmed cleanup, restart or lost ownership fences the account until the original Worker confirms cleanup. The Worker journals its claim before the Take request, so a response lost after the lease committed (or whose fields could not be persisted) leaves a pre-delivery journal with no generation or lease revision and no started execution; the original Worker's recovery accepts exactly that claim under the server lease's identity and generation, and any journal field that contradicts the lease, operation or instance still fails closed.

An expired or rejected token ends the execution explicitly with no API fallback, no other account or Runner and no automatic login. Disconnect revokes DeliDev readiness at acceptance, cancels admitted work, waits for the original Worker lease and cleanup to clear, then settles the logout together with a durable credential-removal checkpoint in the same transaction and only afterwards removes the account's retained credential file and every vault generation, clearing the checkpoint once the explicit removal is confirmed; an offline Worker or uncertain cleanup retains the fence. A logout accepted behind a lease keeps readiness revoked when that lease clears. Its 15-minute operation window restarts when the original lease clears rather than at acceptance, moving the operation's start time together with its deadline so the persisted window never exceeds 15 minutes, and progress does not report it expired while it is still queued behind the lease. If a server restart fenced the logout as recovery-required, the original Worker's cleanup releases only the lease and the recovery fence stays, so the restart scanner completes the old-epoch logout and vault removal instead of stranding it. A settled login records no checkpoint and retires no earlier generation. If the login's metadata commit returns an ambiguous error after the sealed token was staged, the server reads the stored account before touching the vault: a generation already published as the connected, succeeded login keeps its token, an account that did not publish removes only the staged token, and an unreadable account deletes nothing so the recovery scan settles it. Finishing any server-owned Claude operation also clears its unconsumed single-use approval code and presentation from server memory. A failed or interrupted removal never rewrites the settled state: the checkpoint alone selects the account for a bounded retry that preserves the settled generation, and a stale checkpoint deletes nothing. A settled login that an earlier build left with a `credentials-pending` checkpoint is released by the legacy transition: after the account, operation and generation match, only the checkpoint is cleared and nothing is deleted. A database restore clears a server-token account's connection but retains its historical generation, operation and recovery fence while the vault is outside the image; that generation is never authorized again. The restart scanner selects such an account (no connection, a retained generation, recovery required and no lease), removes every retained credential file and vault reference of the account including the historical generation and any checkpoint obligation, and settles it as a logged-out account without relaunching a login, so reconnecting means connecting a new account. If the restored image still carries an execution lease, the original Worker's cleanup releases only the lease and keeps the disconnected health and recovery fence for that scan. Worker cleanup that crosses a recovery fence never restores readiness on its own: the server first re-opens the settled generation's vault envelope for this server and account, and only a positive result returns the account to ready. A missing, malformed, foreign or unreadable credential (for example after a Take whose credential load failed) releases the lease and fence but leaves the account failed, so no later session is admitted only to repeat the failed Take; the account can still be disconnected and reconnected. Local deletion makes no claim about provider-side token revocation, quota or reset credits. The token expires, has no Remote Control or claude.ai connector access and is not renewed automatically. No provider relay or SQLite migration is added.

## Persistent account profiles

Server and Worker login, authentication refresh, logout, quota and credit
operations resolve permanent per-account profiles (`auth`, `quota`, `credit`,
`subscription_probe`) instead of `subscription-runtime/<kind>/<operation>` or
`managed-auth/<lease>` homes. Access-only quota and credit profiles never contain
managed `auth.json`; a managed `auth` profile retains the account-owned
credential file after a joined server operation (a Worker still retires its own
transient file after joined cleanup). Native-created helper links, caches and
history are retained and never block a later Refresh. A live process, an invalid
owner, an unsafe retained file or an interrupted credential write blocks with a
closed cause. Historical per-operation runtimes are never deleted by
completion: the historical access-only quota shape settles once as failed through
an idempotent migration receipt that leaves the directory and helper links
untouched, retains the obsolete credential reference and never
replays the request (see the
[harness contract](cmds-delidev-harness-contract.md#legacy-access-only-quota-migration)).
The managed shared Codex owner's account-wide history home
(`runtimes/accounts/<account-id>/codex`) is registered in place as a
`shared_owner` profile whose lock the founding owner holds until the owner
retires (see the
[harness contract](cmds-delidev-harness-contract.md#shared-codex-owner-account-home)).
The Claude device-profile directory is registered in place and removed only by
its logout.
If native removal completes before the registry deletion is durable, original
logout recovery may clear only the matching absent Claude auth registration
after joined process cleanup. It does not reopen the deleted home or repeat
native login or logout. A remaining, referenced or changed profile keeps the
account cleanup pending.

Explicit deletion of a server account removes every server-owned profile of that
account (`auth`, `quota`, `credit`, `subscription_probe` with their native
caches) in the same locked admission step that checks credential references and
before the account record is deleted; a busy, referenced or unremovable profile
rejects the deletion so the same request can be retried, and a logout alone
still retains the profiles. The target-attributed temporary files of an
interrupted credential write (`.pending-<target>-*`) block reuse as
`credential_file_present` and are removed with the credential files by explicit
logout and account deletion.

## Server credential retention and explicit removal

Server-owned subscription operations separate three obligations that earlier
builds coupled: joining the native process and comparing credential bytes,
deleting credential files, and retiring obsolete vault generations. Only the
first is part of an operation outcome.

Routine retention. ChatGPT browser and device login, authentication refresh,
server quota and credit operations, the server-owned Claude setup-token
lifecycle, and Grok login and refresh retain after completion (or failure) the
account-owned credential file in the account's permanent `auth` profile
(`codex/auth.json`, `claude/.credentials.json`) and every protected `account-login`
generation. They perform no vault `Delete` (the only exception is the generation
the same operation staged when its publication is provably absent), call no
shared reference-retirement helper and record no obsolete-reference checkpoint,
so a failed vault deletion can neither fail an operation nor set a
deletion-only recovery fence. A failed native join, final credential byte
comparison, identity validation or vault publication still fails the operation
and keeps its recovery fence.

Authority. The published vault generation is the sole credential authority. A
retained file or an obsolete generation grants no readiness, Take, refresh or
redistribution. A refresh writes the exact published generation over the retained
file before launch. A new login has no published generation, so the profile-owned
removal replaces a retained file before the native login starts and success still
requires a newly accepted native login and a validated result; a retained
authenticated file never infers success. Missing or contradictory publication or
rotation evidence retains its independent recovery fence.

Admission. A retained regular file with private permissions, owned by the
current user, in the original account `auth` profile is admitted
(`native_profile_credential_retained`). A symlink or other non-regular entry,
group or world access, a foreign owner and an interrupted credential write
(`.pending-<target>-*`) block the owning operation with the closed cause
`credential_file_present`. Access-only quota and credit profiles still receive no
managed auth file, and an unexpected auth file there remains an integrity error.

Explicit removal. Explicit server logout (ChatGPT, Claude and Grok) and explicit
account deletion remove every account-owned retained credential file and every
`account-login` generation before confirmed completion: the enumeration is
complete (no reference count limit, including 257 or more generations), owner and
purpose are validated before the first deletion (a reference of another owner or
purpose denies the whole removal and unrelated credentials are never touched),
cancellation is observed between items, and removal reuses the vault tombstones,
the original mutation receipts and exact receipt replay. A failure retains the
original logout or deletion obligation and is never reported as success. Account
deletion removes only `account-login` generations of a subscription account after
its ordinary authority, revision and retained-resource checks; a protected
reference of any other purpose, or any other retained resource, still refuses the
deletion, and the account's server-owned profiles are removed afterwards as
before.

Worker Finish. The Worker-owned retirement of obsolete generations after a
Finish is unchanged in meaning (every generation except the kept and protected
ones) but its 256 limit is a per-call retirement budget, not a refusal limit. A
Finish retires up to 256 obsolete generations per call and never refuses because
server operations retained more; the remainder stay vault-owned and
non-authoritative until a later Finish retires another budget or explicit logout
or account deletion removes them. A reference of another owner or purpose still
denies the call before its first deletion. Worker credential cleanup, protocol
wire fields, capability IDs and the shared Worker recovery enum values are
unchanged; the protected recovery capture is protected from this helper while its
lease is held and is retired with the lease.

Legacy transition. Cleanup-only state written by earlier builds is released
idempotently under the account gate: the `native-confirmed` checkpoint and the
recovery fence of a failed initial ChatGPT login, and the `credentials-pending`
checkpoint of a settled Claude login. The release requires that the original
native process is proven closed (retained ownership journals, no live or foreign
controller) and that the account, operation and generation still match. It
releases only the credential-deletion checkpoint: the original outcome,
diagnostics, receipts and configuration are preserved, a failed initial login
settles failed and disconnected and requires explicit sign-in, failed health is
never promoted to ready, and a live or foreign process, a changed generation,
restored authority, an active Worker lease or Worker Execute recovery stays
fenced. Restart and replay never relaunch sign-in. The recovery shape and the
`credentials-pending` checkpoint of an explicit logout remain obligations that
the explicit removal completes.

Logging. Structured records carry opaque identifiers and closed vocabularies
only, never credential bytes, native text or paths: `server_credential_retained`,
`native_profile_credential_retained`, `native_profile_credential_replaced`,
`server_quota_reference_retained`, `server_credit_reference_retained`,
`server_subscription_legacy_transition` (`result` is `confirmed` or `retained`)
and `server_credential_removal` (`result` is `confirmed` or `retained`, with the
closed `stage` `files` or `references`).

## Automatic managed ChatGPT execution recovery

A managed ChatGPT EXECUTE lease whose Finish reported unconfirmed authentication
cleanup stays retained and fences the account (`recovery_required`): ordinary
Finish refuses the fence and a Take must never redistribute the old refresh-token
generation. Worker capability `MANAGED_CODEX_RECOVERY_V1 = 72` negotiates an
automatic, Worker-only reconciliation path. It requires
`MANAGED_CODEX_SUBSCRIPTIONS_V1` and is advertised by the Worker only after the
server echoed it in `AttachWorkerResponse.supported_worker_capabilities`.
Older peers keep the existing fence, and the original Take and Finish requests,
digests and receipts are unchanged.

Protected capture. Before a negotiated managed execution deletes its transient
authentication, the Worker calls `SyncSubscriptionRecoveryCapture` with a durable
capture identity (its request ID), the exact unused original bundle and the latest
validated bundle. The server accepts it only for the exact live (or fenced) lease
of the authenticated Worker device, requires the original to equal the delivered
generation, the latest to keep the account's immutable identity commitment and to
be the original or a valid refresh of it, and stores both bundles as
`account-login` vault references (`SubscriptionRecoveryCapture`: the latest under
the capture ID, the original under a deterministic derived ID). SQLite stores only
non-secret metadata. The capture grants no readiness, Take, quota access or
redistribution: Take reads only the published generation, and the Worker Finish
retirement protects the capture (`protectedWorkerReferences`) while its lease is
held and retires it with the lease. A replayed request (crash or lost response)
reuses the same capture; a newer capture replaces and retires the previous one.
Every deletion of the transient authentication passes this one gate, including
the pre-native cleanup of an unused original bundle (there the original is also
the latest): the capture is confirmed first, or the file stays and the cleanup is
unconfirmed. If the capture cannot be confirmed the Worker keeps the
authentication file in place, so the fenced lease can still be recovered from it. The Worker journals
the exact authentication home before any credential is written, the stable
server identity, the capture identity and its digest; the journal never holds
secret bytes and the home path is never logged.

Offer and lane. `WatchSubscription` offers a fenced ChatGPT EXECUTE row
(`RecoveryRequired`, lease action execute, matching machine) or a committed
recovery settlement only to its original Worker and only with capability 72.
Without a fenced row every ordinary row is offered. Otherwise recovery rows share
each offered four-row window with ordinary rows and keep at least one slot; both the
recovery rows and the ordinary rows are offered through rotating windows, so more
than four fenced accounts, or more ordinary rows than the remaining slots, cannot
starve each other. The Worker keeps two independent in-flight pools of four:
ordinary account operations and recovery attempts (ChatGPT, Claude and Grok
fenced-execution recovery). A full ordinary pool never blocks a recovery
attempt and recovery never consumes an ordinary slot, so a continuous ordinary
backlog cannot starve a fenced account. The Worker starts at most one in-flight
attempt per account and retries transient failures after 1, 2, 4, 8 and 16
seconds and then every 30 seconds. Missing or foreign proof is terminal for the
current obligation: it is not retried until the lease, generation or settlement
changes or the Worker reconnects. Attempts run inside the lane's wait group and
are cancelled and joined on shutdown.

Evidence binding. The Worker accepts only its own retained original journal for
the same server, account, lease, operation, generation, lease revision and
original instance. A replacement instance of the same original Worker may
reconcile only that journal; the server requires the authenticated Worker device
and machine to equal the lease, the original instance to equal the lease
instance, the caller to be the machine's current instance and the lease to be
fenced. Another Worker, a revoked device, a replaced generation, restored or
foreign journals and a live execution of the same lease in the running process
are rejected. A shared owner is marked live by its founding lease; the final owner
Finish clears that marker whichever member's lease carries it (the last member may
be a joined member), even while the Finish outcome is still unconfirmed, so a
closed owner never defers recovery until the Worker reconnects. Every original process owner (job, shared owner and members) is
joined through the process journals before the home is touched; an owner without
an index is accepted only with the journaled no-launch evidence of the
[process contract](cmds-delidev-process-contract.md#managed-chatgpt-recovery-process-reconciliation); shared-owner
members must each be retained members of the owner, and the owner Finish
equivalent waits until the request lists every member the server holds. Dependent
credential users (access-only quota or credit readers) block owner retirement.
The journaled authentication home must be an absolute, clean, canonical directory
inside the Worker root. A home that no longer exists proves cleanup only when its
path is lexically contained in that root, its nearest existing ancestor is a real
directory of the root (never a symlink) and the Worker's own native profile
registry no longer registers the home, which is what an explicit session or
storage deletion leaves. A home the registry still names but the filesystem no
longer shows, or an unbound path, never proves cleanup: the former retries as
`cleanup-unconfirmed`, the latter is `ownership-evidence-invalid`.
A shared owner retains at most 256 members in any state; admission refuses a
further member with the existing exclusive-lease busy result, so the complete
member set always fits one recovery request and every valid fenced owner stays
recoverable.

Reconciliation. The Worker reads its capture, removes only the original transient
`auth.json` (when it equals the captured latest bundle or the captured original,
the exact generation the Take delivered), proves the retained
history with the complete streaming scan and journals the cleanup and one durable
recovery request before reporting. History, sessions, caches and helper links are
preserved and no input is replayed. Legacy journals written before capture
support that still hold a valid retained `auth.json` first capture that latest
bundle (without an original) and then settle; retained authentication that is not
a usable bundle is the proven absence of usable credentials, and has no server
capture, so that `auth.json` is the only copy of its evidence: it stays in place,
outside the scan, and is removed only after the whole retained-history scan has
passed. A canceled scan, an unreadable entry or a removable remnant therefore
leaves the next attempt the same evidence instead of a terminal absence of
proof. The decision that the file held no usable credential is journaled
(`recovery_no_credential`) before the file is deleted and is never cleared, so a
crash or a failed local checkpoint write after the removal, a lost response and a
server `blocked` outcome (members or credential users not yet settled) all retry
the same `cleanup-without-credential` report under a fresh request identity
instead of ending as `credential-proof-unavailable`. A stored server capture
always takes precedence over that decision. The retry re-proves the retained
history, but only structurally, because the removed bytes are no longer available
as a needle; that is the inherent residual of deleting the sole evidence. The
legacy capture request owns a second copy of the retained bundle, which is
cleared as soon as the bounded call returns on success and failure. No evidence at all
stays protected with the closed diagnostic `credential-proof-unavailable` and no
fabricated receipt. A capture refusal proves the bundle unusable only when it
carries the distinct cause `subscription_recovery_bundle_rejected` (the bundle is
not the delivered generation or a refresh of it, is unparsable, or names another
identity). Every other refusal, such as a binding or authorization failure, a
withdrawn capability, stale lease evidence or a changed current instance, says
nothing about the bundle: the Worker keeps the only surviving `auth.json` and
retries.

Capture receipt buffers. The server commits each capture receipt to the original
and latest bundles through one temporary concatenation. That buffer holds raw
credentials, so it is cleared as soon as the commitment is computed, with the
request-owned bundle buffers cleared on return.

Retirement of a released capture. A successful Finish that releases the lease
drops the capture metadata and records the capture's vault references in its
receipt. The success is acknowledged only after those references are deleted
(deleting an already removed reference succeeds). A failed deletion returns the
retryable `unavailable` result without fencing the settled account, the Worker
resends the same Finish, and the exact receipt replay deletes again, so no
captured original or latest bundle outlives the lease when the account performs
no later credential operation.

Settlement. `RecoverSubscription` uses a recovery request identity distinct from
the original Take and Finish requests. With a valid capture the server publishes
one immutable generation named by the recovery request and, after the original
references are retired, restores readiness. With cleanup proven but no usable
credentials it denies and retires every original reference and settles the
account disconnected, so only the existing explicit login can reconnect it; no
login starts automatically. A refresh or logout that was accepted while the lost
lease was live and is still queued is retired in the same transaction as a
disconnected decision (its server record, when it has one, settles as canceled),
so it cannot block the explicit login or be claimed against an empty generation;
a restored decision keeps it. The decision is committed first (the lease is
released, the recovery fence and a `recovery_settlement` checkpoint remain), the
protected references are then retired completely, and only the final receipt
under the recovery request clears the fence. An incomplete deletion keeps the
checkpoint and the fence, admits no execution or login, and the original request
resumes the same retirement; an exact receipt replay mutates nothing. Because only the original Worker device can complete it, a committed
settlement is one row of the original-Worker hold table below.
A positive credential decision needs positive evidence: an unparsable or
identity-changed capture, or a stored generation that is absent or unrecoverable
in the vault, settles the account disconnected, while any other vault read
failure (for example an unavailable vault) is returned and keeps the fence so a
later attempt can decide. A report of
blocked cleanup, unsettled members or active credential users records the closed
reason in `recovery_block` on the account document and settles nothing. Closed
reasons are `credential-proof-unavailable`, `cleanup-unconfirmed`,
`ownership-evidence-invalid`, `members-unsettled`, `credential-users-active` and
`active-owner`. There is no new screen, CLI retry or automatic login; the existing
account observation exposes the state and reason.

Original-Worker holds. Some managed subscription states can be completed only by
the Worker device that owns them, because the credential evidence they protect is
private to that Worker and every server operation that finishes them checks the
exact lease device, machine and original instance. `workerRecoveryHoldKind` is the
closed set; `accountWorkerRecoveryHold` mirrors the rows `WatchSubscription` offers
back to the owning machine, so one predicate classifies removal, revocation and
account deletion.

| Hold kind (log `hold`) | Account state                                                                                                                                                                                                                                | Completed only by                                                                       |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| `live_execute_lease`   | `lease` action execute, no recovery fence, any provider                                                                                                                                                                                      | the running Worker's own Finish; removal closes its connections, which fences the lease |
| `fenced_lease`         | `recovery_required` with a retained `lease`: managed ChatGPT or Grok execute, or a Claude lease of any action. Includes `cleanup_unconfirmed`, shared owners with members, and leases with or without `recovery_capture` or `recovery_block` | the original Worker device (recovery, or its retried cleanup-only Finish)               |
| `recovery_settlement`  | `recovery_settlement` with the lease already released and the fence kept                                                                                                                                                                     | the original recovery request from the original Worker device                           |

A fenced ChatGPT lifecycle lease (login, refresh, logout) is never offered back and
is not a hold.

| Path                                                      | Live execute lease                                          | Fenced lease                                | Recovery settlement                         |
| --------------------------------------------------------- | ----------------------------------------------------------- | ------------------------------------------- | ------------------------------------------- |
| `RemoveWorker`                                            | refused (conflict)                                          | refused (conflict)                          | refused (conflict)                          |
| `RevokeDevice` of the Worker device                       | allowed, logs `worker_device_revoked_with_recovery_pending` | allowed, same log; the account stays fenced | allowed, same log; the account stays fenced |
| `RemoveWorker` after the owning device is revoked or gone | allowed, logs `worker_removal_orphaned_recovery`            | allowed, same log                           | allowed, same log                           |
| Configuration deletion of the account                     | refused (lease)                                             | refused (fence)                             | refused (fence and hold)                    |

`RemoveWorker` waits while any such state names the machine and its bound device is
still a registered, unrevoked Worker device, so removal never retires the only
principal able to finish them. Revocation is the emergency credential cut-off and is
never refused: it keeps the state fenced, its protected references and fence are not
released, and the structured warning names the account and hold. A revoked or
missing bound device can no longer recover anything, so removing its machine cannot
make the account less recoverable and is allowed; this is how a permanently lost
Worker is abandoned (revoke it, then remove it). Account deletion keeps requiring a
disconnected account with no lease, fence or generation, which every hold implies.

Retained history scan. The authentication cleanup scan of execution history has no
file-count, file-size or aggregate limit, for the singleton and the shared path.
Every regular file streams through a fixed window that overlaps by the longest
token encoding, so raw and Base64 remnants across chunk boundaries are found with
bounded memory. Pending entries, special files, unreadable entries, remnants and a
changed root identity still fail closed without deleting history, and the scan
observes cancellation. Every scan path observes the Worker's execution (shutdown)
context: the normal singleton and shared-owner cleanup, the unused-original
cleanup after a failed native startup, the capture synchronization before the
deletion and the recovery scan all stop with the existing fail-closed canceled
result, so a very large retained profile never keeps a goroutine alive after
shutdown and the lease is left for recovery. A stopped execution's own request
context does not cancel its cleanup.

Transport. A capture synchronization or read may carry both the original and the
latest maximum-size (64 KiB) bundle plus protobuf framing, the mutation and the
ownership fields, so the Worker's subscription client limits each message to two
maximum bundles plus 64 KiB of overhead in both directions.

Logging. Structured records use the phase `subscription-recovery` on the server
and `codex-recovery` on the Worker with opaque account, lease, request and capture
identifiers, counts, the closed blocked reason, the retry delay and the cleanup
stage and reason; they never contain credentials, bundle digests, paths or native
output.

## Claude native version admission and diagnostics

Native `auth status --json` may include the optional `analyticsDisabled`,
`projectsDirectory` and `configDirectory` presentation fields. The adapter accepts
only a boolean analytics value and string directories when declared; explicit
null values fail for both authenticated and disconnected status. Omitted fields
remain compatible. The adapter discards these fields and preserves all first-party
subscription and disconnected-profile checks. These fields grant no credential,
profile or execution authority; unknown fields remain unsupported.

Server-owned setup-token login, Runner subscription selection and the Worker lifecycle admit any verified Claude Code installation whose valid SemVer precedence is at or above the pinned `2.1.236` baseline, using `domain.HarnessVersionAllowed`. Admission only permits the real native protocol, settings, authentication and cleanup checks to run. The `claude auth` and `--version` verification compares the executable output with the observed installation version, never with the baseline constant. A malformed or lower version stays unsupported before any native launch.

`NativeSubscriptionDiagnostic.detected_version` carries the observed executable version, including on the server login path, and `phase` carries the real failing native phase. A server login rejected during Claude protocol discovery reports Discovery, not Launch. `required_version` stays in the proto and Go validator as the pinned minimum baseline for wire compatibility; clients must not present it as an exact requirement. An authentication-status failure after successful version validation never advises installing a specific version. Server and Runner messages for unsupported installations do not name a fixed version.

## Server-owned Claude usage tracking

System `CLAUDE_SERVER_USAGE_V1 = 110` negotiates a separate, explicitly linked, read-only Claude usage connection of a connected server-token Claude account. It is independent of the setup-token lane (capabilities 67 and 68) and of the device-owned lane (38 and 20). Without it, clients show server-update guidance and submit nothing; an older client never sees or changes the optional state. The connection is used only to fetch usage limits. It never replaces the execution setup-token, never enters Worker execution or Take, and a failure never changes execution readiness, health, routing or the account name. The connection is never created automatically for an existing account and adds no SQLite migration.

Typed `SubscriptionService` operations carry UUID-v7 request IDs, the original account revision and the usage generation: `StartClaudeUsage`, `GetClaudeUsageProgress` (a read; it never launches, retries or extends native work), `SubmitClaudeUsageCode`, `ConfirmClaudeUsageLink`, `CancelClaudeUsage` and `DisconnectClaudeUsage`. Start carries the observed linked usage generation (empty when none) and its request ID becomes the operation and the new usage generation. A replay of the exact original request returns the original receipt and never launches another sign-in, exchange or credential adoption. Mutations are owner or paired-client operations bound to the initiating principal; Worker credentials are rejected. A mutation that races a live read or login may find the account revision advanced once and conflicts; the caller retries with a fresh revision.

Optional protected state lives in the account's subscription metadata (`claude_usage`): the linked usage generation, the operation (state, epoch, actor, code claim and consumption markers, the staged claude-usage reference and the pending validated identity), the validated identity (email, organization, plan and a keyed identity commitment), the explicit association time, the current claude-usage reference, the durable read marker (phase, epoch, connection, generation, actor, last attempt and a refresh claim), the last-success windows and time, the backoff deadline, the closed failure code, the reconnect-required flag and the removal obligations. Generic configuration writes cannot overwrite it: a save restores the stored state over the incoming document and re-projects the informational windows from it. Portable configuration export omits protected subscription state and import refuses it. Database restore clears it, so restored accounts have no usage authority, no scheduling and no credentials; restore is refused while a usage login, read or cleanup is unsettled.

The login runs the original `claude auth login --claudeai` on the server in one exclusively locked, persistent `usage` native profile of the account (Claude Code only), independent of Runner availability and execution leases. `CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` name that exact private profile, server-side browser opening is suppressed with a verified no-op opener (a host without one fails closed) and the profile is never a personal, legacy execution or external profile. The original authorization address is validated again by the server and the client. It must be the original auth-login profile with the manual approval-code callback; the localhost callback variant is refused (`login_manual_code_required`) because a remote server cannot receive it and DeliDev never substitutes another callback. The address opens only through the existing trusted client-native authorization binding. The single bounded approval code is a write-only input: the server records the claim before retaining bytes in memory, records consumption before releasing them once, and neither code nor address enters receipts, resources, logs or synchronized state. A first admission requires empty native credential storage; anything present is refused (`profile_unsafe`) and is neither adopted nor deleted.

After a joined successful exit the server reads the validated OAuth identity from the original `auth status --json` (a first-party subscription with email, organization identifier and plan) and the native `claudeAiOauth` record from this profile only: the exact profile-derived macOS Keychain item with the documented file fallback, or the profile-owned `.credentials.json` with owner-only permissions on Linux and Windows. A symlink, unsafe permission, foreign owner, interrupted-write remnant, ambiguous or unrecognized storage fails closed (Windows storage other than the profile file is `storage_unsupported`). The access token, refresh token, expiry, granted scopes (at least `user:inference` and `user:profile`) and the pinned client binding are validated by bounded rules. The bundle is sealed into a versioned envelope bound to server, account, usage generation and an immutable reference identity and stored under the vault purpose `claude-usage`, which is never an execution `account-login` reference. Native credentials stay in the profile only while a login or refresh-login is running and are removed after capture; the vault reference is the sole authority. Neither Worker Take, export, ordinary RPC, backup content, logs nor the renderer receives authentication bytes, and transient buffers and approval codes are cleared at their lifetime boundaries.

The validated email, organization and plan are shown for review. Nothing is published, scheduled or displayed until the user confirms with `ConfirmClaudeUsageLink`. The confirmation is a user assertion of association; the server never matches aliases, infers identity equality with the setup-token or changes the execution account name. Back or Cancel before confirmation discards the staged reference and leaves no usage evidence. A new explicit reconnect repeats the review and, on confirmation, replaces the generation, clears the previous usage evidence and retires the superseded reference; an unconfirmed reconnect leaves the previous connection and evidence in place but pauses reads. Cancel joins the original login and discards unconfirmed credentials. Closing the dialog (Escape) only hides accepted work.

Usage is fetched only by `GET https://api.anthropic.com/api/oauth/usage` with the current usage OAuth Bearer, `anthropic-beta: oauth-2025-04-20`, a JSON content type and a DeliDev user agent, with a five-second deadline, a one-MiB response bound, a fixed origin, no redirect following and the configured server network route; an unsupported route fails (`route_unsupported`) without a direct fallback. The endpoint and the native renewal are internal interfaces, not a documented stable public API. Native `get_usage`, cached values and header-derived values are never evidence. Only `five_hour` and `seven_day` are normalized. Each present window needs a finite utilization from 0 through 100 and a valid or null `resets_at`; remaining is `1 - utilization / 100` (25 and 50 give 75% and 50%) with comparison group `claude`. Missing or null windows are unavailable, never zero. A recognized window that is malformed, a duplicate key or an out-of-range value fails the whole observation. Unrelated fields are ignored. Only a validated nonempty 2xx response publishes windows and advances the observation time; failed, empty, oversized, redirected and malformed reads keep the previous windows and time and record only the closed failure code.

The usage windows are informational. They are projected into the account's quota windows with `blocking = false`, so routing, exhaustion and recovery decisions never consume them. This is the recorded decision for the open question about blocking: Claude usage never blocks routing unless a later contract states otherwise.

One server usage-read owner joins explicit row and Manage refresh, Refresh all and the inactive maintenance pass. At most one read per account is in flight; overlapping triggers join it. The existing four-minute due interval and five-minute presentation freshness apply: the 30-second foreground trigger (and an older client's Refresh all, `trigger` unspecified) is due-gated and cannot force a fetch before four minutes after the previous attempt (not due at 3m59.999s, due at 4m), while an explicit user refresh may fetch sooner. Before publication the read rechecks the account connection, usage generation, credential reference, actor and server epoch and discards a result whose authority changed. For 403 and 429 the `Retry-After` seconds or HTTP date is honored (default five minutes, cap one hour, at least one second); every trigger, including a manual one, is suppressed until the deadline.

Renewal is native and supervised. Before the access token expires, or exactly once after a definite 401 within a read, the server runs one serialized `claude auth login --claudeai` refresh-login in the same exclusive profile whose child-only environment carries only `CLAUDE_CODE_OAUTH_REFRESH_TOKEN`, `CLAUDE_CODE_OAUTH_SCOPES` and the optional `CLAUDE_CODE_OAUTH_CLIENT_ID`. A durable claim precedes the child. The child is joined, the OAuth identity must be unchanged, the rotated bundle is captured, staged under a new immutable reference and switched in, the profile storage is emptied again and the GET is retried at most once. An interactive address, a rejected or unsupported renewal, an unchanged bundle, a foreign identity, an unjoinable child or a lost rotation never repeats an exchange and requires an explicit reconnect while the previous evidence stays. A restart never repeats a browser sign-in, an uncertain refresh or an unconfirmed removal: a login of a dead epoch fails closed with its storage removed, an interrupted fetch is idle again (a GET has no side effect) and an interrupted refresh requires a reconnect.

Disconnect stops and joins live usage work, revokes reads immediately, records every credential reference and the native storage as durable removal obligations, removes only the usage credentials, references and usage profile and reports completion only after that cleanup is confirmed; a replay retries an incomplete cleanup and never reports false success. The execution setup-token and readiness are untouched. Explicit account logout revokes usage at acceptance and its explicit removal retires the usage connection, and explicit account deletion removes the usage references and profile under the existing authority and deletion gates; a busy profile keeps the original obligation pending. The Worker Finish retirement never counts or retires a claude-usage reference.

Settlement and retirement invariants of the usage lane:

- Disconnect validates and revokes before it cancels. `DisconnectClaudeUsage` binds the initiating actor into its immutable input, commits the revocation, and only then (for a non-replayed request, or a replay while the stored state is still the revoked one it produced) cancels and joins the live run outside the account gate. A stale, conflicting or replayed request never interrupts another client's current login or read. Logout acceptance and configuration deletion stop and join live usage work before taking the gate; `retireClaudeUsage`, which runs under the gate, fails closed with a retryable `Unavailable` result while a canceled run is still stopping instead of racing its native profile.
- A read registers as the account's single live run before the account gate is released and before the usage token is opened, so a concurrent disconnect, logout or deletion always finds and cancels it. A login claim is rechecked after registration and before any native resource opens. A read whose actor, connection, generation or credential changed is discarded without publication, but its matching durable marker is still settled, so the account never stays `reading`.
- Every claude-usage reference whose vault state is unproven becomes a durable removal obligation (`retired_credentials`) before the operation is reported settled: an uncertain `Vault.Put`, a failed deletion of a staged reference after an unpublished login commit or rotation, and the staged or confirming reference of an operation that enters recovery. A recovering or failed operation never retains a pending identity or staged reference. A login whose native cleanup is unproven (including a failed profile lease release, also after a failed open) is never offered for confirmation and stages nothing. A refresh whose profile lease release fails settles with `cleanup_pending` instead of reporting the profile released.
- A replay of an approval-code claim whose first acknowledgment was lost restores the in-memory bytes while the committed claim is unconsumed; a consumed claim is never restored.
- Database restore is refused while any usage ownership is unsettled: an in-flight read, a native-started or preparing, waiting, confirming or recovering login, a pending cleanup or a nonempty `retired_credentials` list.
- On shutdown the usage workers are stopped and joined before the account secrets close, so a canceled worker can still settle against the vault. The maintenance scanner derives due automatic reads from its single account scan per iteration.

Structured records use opaque identifiers and closed vocabularies only (`claude_usage_login_accepted`, `claude_usage_login_started`, `claude_usage_code_claimed`, `claude_usage_code_delivered`, `claude_usage_linked`, `claude_usage_cancel_accepted`, `claude_usage_disconnect_accepted`, `claude_usage_read_started`, `claude_usage_read_finished` with `result`, `failure` and `backoff_ms`, `claude_usage_refresh_rotated`, `claude_usage_cleanup` and `claude_usage_retire` with `result` `confirmed` or `retained`). They exclude identity, quota values, credentials, native bodies and paths. The desktop presentation is specified in the [subscription settings contract](apps-delidev-subscription-settings-contract.md#claude-usage-tracking).

Fixture and mock results do not establish installed-native login or renewal, real-account fetches or per-platform credential and cleanup acceptance. The macOS Keychain service derivation (a fixed prefix plus the first eight hexadecimal digits of the SHA-256 of the exact configuration directory and the current OS account), the `.credentials.json` record shape, the `CLAUDE_SECURESTORAGE_CONFIG_DIR` and `CLAUDE_CODE_OAUTH_*` environment behavior and the manual-code auth-login address are unverified against a live Claude Code installation and are recorded as outstanding acceptance, separately for each supported platform.
