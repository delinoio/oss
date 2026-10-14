# DeliDev Grok Build subscription contract

## Scope

This contract owns the Grok Build subscription lifecycle in `cmds/delidev-cli`,
its Connect interface and the desktop AI Subscription flow. Implementation is
partial and nothing is advertised. Implemented: the protocol identifiers below
(declared only), the bound credential bundle library, the protected-vault
generations, the protected Take/Finish lease handlers on the server and the
Worker-side credential custody described under Storage. Not activated: the
capability advertisement, native OAuth login and refresh execution, Worker-side
watch dispatch, the remote callback forwarding and Grok execution.
The subscription profile declares `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) as its minimum SemVer baseline. `domain.GrokSubscriptionVersionAllowed` admits a valid SemVer at or above it (prereleases of the baseline are below it) and gates protected Take admission of a Grok lease; no capability, discovery or execution path references it. Newer releases are validated natively, not rejected by number. The API and history baseline stays `domain.GrokProtocolVersion` (`1.0.41`); Grok Build 1.0.52 inspection is accepted when its optional `experiments` inventory is an empty array (see `cmds-delidev-harness-contract.md`), which does not change these baselines. Reservation support is separate
from implemented support and actual account/platform acceptance.

## Runtime and Language

Go owns authentication, protected credentials, account leases and Worker
execution. The desktop uses TypeScript/React and a trusted Rust callback receiver.
Grok Build runs as an independently verified native child.

## Users and Operators

The owner and paired clients select a service-native account. The server owns
OAuth operations; the selected Worker owns its original native execution and
private runtime. Separate accounts can run independently.

## Interfaces and Contracts

These allocations are declared in the protos and generated bindings but are
unadvertised: GetStatus and AttachWorker never list System 39/40 or Worker 21,
the Worker does not report 21 and the server rejects it as an unknown
capability. `GetSubscriptionProgress` populates `grok_diagnostic` only for a
Grok account's original operation (see Grok diagnostics below); advertising the
capabilities remains with the owning feature change, which activates them:

- System `GROK_SUBSCRIPTION_LOGIN_V1 = 39`.
- System `GROK_SUBSCRIPTION_EXECUTION_V1 = 40`.
- Worker `MANAGED_GROK_SUBSCRIPTIONS_V1 = 21`.
- `GetSubscriptionProgressResponse.grok_diagnostic = 10`, preserving Codex field 7.
- `GrokDiagnostic` fields detected version 1, supported version 2, phase 3,
  stable code 4, safe message 5, guidance 6 and correlation ID 7.
- Closed `GrokDiagnosticPhase` members UNSPECIFIED 0, DISCOVERY 1, VERSION 2,
  PROFILE 3, RUNTIME 4, LOGIN 5, REFRESH 6, LOGOUT 7, MODELS 8, EXECUTION 9,
  HISTORY 10 and CLEANUP 11.

These ledger reservations grant no authentication, execution, active schema or
capability advertisement; the unadvertised storage and lease layer under Storage
does not depend on them. Existing System 30/35/36/37 and Worker 19 ownership
remains unchanged. The planned login reuses Request/Progress/Cancel/Callback
and protected Take/Finish Subscription RPCs (Take/Finish are implemented for Grok accounts). The selected account service and
`device_code` choose the flow; methods never switch automatically.

The fixed official xAI OAuth profile validates PKCE, state, nonce and tokens.
Browser callbacks bind the original IPv4 loopback address and ephemeral port.
Remote desktop callbacks retain the same original address and use a trusted
native receiver plus authenticated Connect forwarding. Device authorization
honors RFC 8628 pending, slow-down, denial and expiry.

The trusted native receiver (`apps/delidev/src-tauri/src/oauth.rs`) exposes
`grok-subscription-open` and `grok-subscription-reopen` for the server-returned
browser URL. It validates the fixed `https://auth.x.ai/oauth2/authorize` shape
(public client `b1a00492-073a-47ea-816f-4c329264a828`, S256 PKCE, nonempty
state and nonce, redirect exactly `http://127.0.0.1:<port>/callback`), opens it
once. A local desktop binds nothing because the server listener receives the
redirect. A remote desktop is refused explicitly (`Incompatible`) until the
server can forward Grok callbacks: today `ForwardSubscriptionCallback` accepts
only the ChatGPT/Codex login (`auth.openai.com`, fixed port 1457) and rejects
Grok accounts, so forwarding must first become service-aware (Grok host,
state and the dynamic port bound for that login) with Codex behavior unchanged.
Once enabled, the receiver binds that IPv4 loopback port, accepts one
`code`/`state`/`scope` redirect and returns it through Take for a single
`ForwardSubscriptionCallback` call bound to the operation id. The server must
therefore emit `state` and `nonce` from `[A-Za-z0-9-_.~]` (at most 512 bytes),
an unprivileged port and no other query keys than `scope` and `referrer`. See
the [desktop contract](apps-delidev-desktop-contract.md).

General Chat and prepared Local/Worktree repositories require a complete ordered
manifest, primary cwd and original ownership of additional roots. Execute/Plan,
tools, questions and approvals retain original input authority. Follow-up input
resumes verified complete native history on the same account and Worker without
replaying previous prompts. Stop, restart and uncertain delivery reconcile only
original evidence. Grok records at `domain.GrokProtocolVersion` (`1.0.41`) and version-1 results retain attribution
and acquire no new resume authority. API execution keeps its independent support
boundary when revalidated against `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) or newer.

The approved desktop flow preserves service order, existing marks, navigation
and theme. Add account starts browser login; Use a device code is explicit.
Waiting offers reopen/copy/open-page/cancel as appropriate. Confirmed success
advances to account naming. Preparing, waiting, canceled, expired, failed and
recovery remain distinct. Exact original-request retry does not create a new
login. Polling, reconnection and Strict Mode grant no mutation authority.
The form retains a 1040px settings column, 720px login width, 40px controls,
8px corners, stacked narrow-screen actions, English/Korean text, keyboard access,
status announcements and name focus. Leaving clears display/timer/callback
authority without canceling an accepted server operation.

Quota/reset credit/pricing, enterprise SSO, external identity providers, imported
user login, account switching, cross-Worker resume, Fork, Sidechat and manual
compaction are excluded.

### Project requirements

- DeliDev Grok Build subscription support follows `cmds-delidev-grok-subscription-contract.md`. System 39/40, Worker 21, progress field 10 and the Grok diagnostic messages are declared but unadvertised until the owning feature activates them. Declarations grant no login, native execution or migration. Preserve existing capability ownership, server-owned OAuth/protected credentials, original account leases and same-account/Worker history; verify the subscription profile at or above `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) by native validation before activation.

## Storage

Use optional existing Account/server-operation metadata and protected-store
references; add no database migration. Claim exchange and refresh durably before
sending. Uncertain results remain in recovery and are never resent. Service
bundle validation binds service, issuer, user and principal and rejects duplicate
identity ownership. Native identity and complete history have a separate owned
checkpoint. Preserve older records without granting continuation authority.

### Grok bundle

`internal/subscription` owns the `GrokBundle` library (no network, RPC or
migration). A bundle holds exactly one native `$GROK_HOME/auth.json` scope entry
for issuer `https://auth.x.ai` and client `b1a00492-073a-47ea-816f-4c329264a828`
(key `<issuer>::<client-id>`) with `auth_mode` `oidc`. `ParseGrokAuthFile`
rejects, with the single stable value-free `GrokBundleInvalid` error: input above
63 KiB (`MaxGrokAuthFile`), duplicate or unknown fields, any other scope key or multiple entries, an
empty `key` or `refresh_token`, empty `user_id`, `principal_type` or
`principal_id`, unparsable RFC 3339 `create_time` or `expires_at`, an expiry not
after creation, and an `oidc_issuer` or `oidc_client_id` that differs from the
key. Optional native profile fields (`email`, `first_name`,
`profile_image_asset_id`, `team_id`, `coding_data_retention_opt_out`) are
preserved when present (including an empty string or `false`) and never invented
when absent; an explicit JSON `null` is the wrong type and is rejected, because
it would otherwise decode as absent and change meaning when re-emitted. Member
names must be spelled exactly as native writes them, in both the scope entry and
the sealed envelope: a differently cased alias is rejected rather than matched
case-insensitively and re-emitted under the canonical name. `AuthFile` re-emits the
native bytes semantically for Worker materialization; the caller clears them.
The canonical encoding is compact JSON without HTML escaping, so `<`, `>` and `&`
stay one byte, and the same encoder is used for the sealed envelope. The input
bound alone does not bound the re-encoding (U+2028 and U+2029 always expand to
six-byte escapes), so `ParseGrokAuthFile` also rejects, with the same stable
error, any file whose canonical encoding exceeds 63 KiB. A bundle the parser
accepts can therefore always be re-emitted by `AuthFile` and sealed within the
envelope bound. The envelope bound (`MaxGrokBundleEnvelope`, 64 KiB) equals the
protected vault's per-secret limit (`credentials.MaxSecretBytes`) because the
vault stores the exact sealed envelope, and the 1 KiB left between the two
bounds is reserved for the envelope members (version, kind and the three
identifiers, under 200 bytes). The largest accepted bundle therefore always
stays storable, so an accepted login or refresh is never fenced for recovery by
the vault's independent limit.

The vault object is the versioned envelope (`version` 1, kind
`grok-auth-bundle`) bound to server, account and generation by `SealGrokBundle`
and `OpenGrokBundle`; a native file, Codex bundle, Claude envelope or another
owner fails with the same safe error. Opening parses the envelope's embedded
document in place and clears every byte buffer the parser owns on every return
path (the decoded envelope member and the decoded scope entries); the shared
strict JSON decoder reads the caller's bytes without whole-document string
copies. Only the immutable parsed field strings and the JSON decoder's internal
read buffer remain until garbage collection, which Go cannot clear. It is stored only under the existing
`credentials.AccountLogin` ownership.

`IdentityKey` is a domain-separated, length-prefixed SHA-256 over service
`grok`, issuer, `user_id`, `principal_type` and `principal_id`; token rotation or
profile metadata never changes it and a different principal of the same user
yields a different key. The server persists only the server-keyed HMAC of that
key (`grokIdentityCommitment`, 64 hex characters) as
`SubscriptionState.IdentityCommitment`, so `uniqueSubscriptionIdentity` rejects a
second account with the same identity and admits a different principal. The
unkeyed key is never stored. `String`, `GoString`, `%v`/`%+v`, `slog` `LogValue`
and JSON encoding errors reveal only the service and whether a credential is held
(`present` or `empty`); no prefix or digest of the identity key is formatted,
because the unkeyed key is guessable from a user and principal identifier and a
prefix would let log readers confirm candidates offline. `GrokIdentity` formats
as redacted in the same forms. Correlation across log lines uses the account,
lease and operation identifiers. Generic JSON/text encoding of a bundle fails
closed.

Protected Take/Finish dispatches by account service. The Codex lifecycle keeps
serving only ChatGPT accounts and refuses every Grok account; a Grok account uses
its own singleton-lease lifecycle for login, refresh and logout. Take claims the
exact queued original, requires one verified Grok Build installation on the
selected Runner Device whose version passes `domain.GrokSubscriptionVersionAllowed`
(at or above `domain.GrokSubscriptionBaselineVersion`), and returns the stored
envelope (empty for a fresh login) only after the server re-opens it against its
own identity, the account and the stored generation. The legacy
`domain.GrokProtocolVersion` discovery baseline is a lower bound for discovery
and legacy execution input only; it admits no new credential authority, so an
installation between that baseline and the subscription baseline, or a prerelease
of the subscription baseline, receives no lease. A retried Take whose first
response was lost is a receipt replay: it never redistributes the envelope, and
it marks the exact unfinished lease (same operation, Worker device, Runner Device
and instance) recovery-required before the denial, because that Worker lacks the
generation and lease revision needed to Finish. A finished or foreign lease is
left untouched. A Grok execute Take, a member or owner Finish scope, and any
other action are refused. Grok lifecycle work is not pushed to Workers through
the subscription watch until the managed Grok Worker capability is declared and
negotiated.

Installation admission happens only at Take, inside the claiming transaction, and
the verified observation it returns is the only one the lease uses. Harness
discovery may replace the Runner Device's installation list at any time (missing,
failed, duplicated or newly unsupported), so a claimed lease is never re-gated on
installation metadata: the exact stored Worker, instance, epoch and lease can
always report Finish, including confirmed cleanup that releases the lease. Finish
still requires the current Worker instance, the stored owner binding and the
frozen lease generation and revision.

Finish opens the returned envelope against this server, the account and the
generation the Finish creates, so a bundle sealed for another server, account or
generation, a native file, a Codex bundle or an empty bundle is refused and
fences the account for recovery. Login derives the identity commitment and
rejects a duplicate owner before staging; refresh and later Finish calls require
the stored commitment to match, and refresh additionally requires the same
identity with a later expiry and a changed key or refresh token. The new
generation is staged under `credentials.AccountLogin` before the account
document points to it. The account keeps referencing, and the vault keeps, its
current generation until the document durably publishes the replacement (or the
logout), so a failed publication never leaves the account pointing at a deleted
generation. Only after that commit are the generations the account no longer
references removed (superseded generations, an unpublished staged generation and
every generation after a completed logout); a logout fenced for recovery keeps
its generation. A removal that cannot be confirmed marks the
account recovery-required, and an unconfirmed Worker cleanup keeps the lease
fenced. A Finish that aborts after staging (a revoked Worker, a concurrent
cancellation, a vault or store failure) fences the account and then retires the
staged generation, but only after reading the account document and proving it
does not reference that generation; a commit whose caller saw an error keeps the
generation it published. Every mandatory bookkeeping write on the Take, Finish and
abort paths runs on a bounded context that is detached from the request's
cancellation, deadline and Worker authority: the
recovery fence of a replayed Take (5 seconds), the recovery fence of an aborted
Finish, the retirement of an unpublished staged generation (5 seconds) and the
removal of superseded generations after a committed Finish (30 seconds). A
client disconnect, an expired deadline or the revocation of the very Worker being
settled therefore cannot skip a fence, strand a claimed lease without
`RecoveryRequired`, or turn a committed publication into a fence. The primary
operations (the claim, the publication, vault reads and staging) stay bound to
the request and abort, and a mandatory write that still fails is logged and left
to the next replay or recovery. When the account is
unreadable or the removal fails, the unreferenced generation stays vault-owned,
is never granted (Take reads only the referenced generation) and is removed by
the next cleanup or the logout cleanup.

Worker custody mirrors this. The Worker opens the delivered envelope against its
own server, the account and the delivered generation, then writes
`$GROK_HOME/auth.json` atomically with owner-only permission into a private,
non-linked home, clears the in-memory envelope, and writes nothing when the
envelope is rebound. The home is validated before any delivery is accepted,
including a fresh login that delivers no bundle and lets the native login create
`auth.json` itself; such a login also requires a home with no credential file or
interrupted-write temporary, so a leftover credential is never sealed as the
login result. After the native process exits it reads the file once,
seals the next generation (bound to the Finish request identifier) only when the
original identity is unchanged, and retires the file and interrupted-write
temporaries. The file must be a regular, bounded, non-symlinked entry with
exactly one directory entry: a hard-linked `auth.json` is never sealed, and
retiring a hard-linked file or temporary unlinks its name but reports the
cleanup as unconfirmed because the other entry keeps the plaintext credential
reachable. Link validation and unlink are one guarded operation on the opened
file: each credential entry is opened through the held directory handle, its link
count is read from that handle, the name is unlinked, and the same retained
handle is examined again. Retirement is confirmed only when the retained inode
has no directory entry left (on Unix its link count after the unlink is zero,
which is final because an unlinked inode cannot be linked again) and a rescan of
the held directory finds no entry with the retained file's identity, so a hard
link that another process creates after inspection and before the unlink, in this
directory or in any other, makes the cleanup unconfirmed. Materialization applies
the same guarded unlink to leftover temporaries and to a previous regular
`auth.json` and refuses to write when either keeps another entry. Cleanup is
confirmed only when no credential file or attributed
temporary remains in the same directory the credential was materialized in;
otherwise no bundle is returned and the server keeps the lease fenced. The
Worker binds the private home (`subscription.BindGrokHome`) before it accepts
any delivery by opening it once and validating the opened directory itself
(real directory, owner, permissions or ACL) through that handle; the path is
compared with the handle only to refuse a link or a directory swapped during the
bind. Every later credential operation (write, read, presence check, retirement)
is anchored to that held directory handle with openat-style, link-refusing,
handle-relative calls, so a home that is renamed away and replaced after binding
cannot receive a credential written through a path lookup or lose a file from the
substitute: there is no gap between checking the directory and using it. The
write also requires that the path still names the held directory, and Finish
verifies the same before it reads or removes anything and again after the final
absence check. A home that was renamed away or replaced (even by an empty
directory, which a path lookup reads as clean) therefore never confirms cleanup,
for logout as well, and nothing is read from or removed in the substitute path;
the original directory keeps its file for recovery. The handle is released when
Finish settles or when materialization fails.

Residual window: the native Grok child can only reach the home through its path
(`GROK_HOME`), which cannot be anchored for another process. A swap after the
last verification can make the native child read or write the substitute; the
next verification rejects the lease as uncleaned, and no Worker-side operation is
ever redirected. On Windows the same handle-relative API is used (a directory
handle with relative opens, renames and removals). The check on the handle reads
the owner and the DACL in one security-descriptor read and requires both: a
single full-access ACE for the current user, and an owner that is the process
user or the default owner of objects the process creates (the Administrators
group when the token is elevated). The owner matters because the owner of an
object can always rewrite its DACL, so an owner-only ACE list on a foreign-owned
directory would let that principal regain access while credentials are present.
Every publication on this path (the anchored `auth.json` replacement is the only
one) is an `os.Root` rename relative to the held directory followed by
`FlushFileBuffers` on the still-open renamed file, which is the anchored
counterpart of the `MOVEFILE_WRITE_THROUGH` replacement the path-based writers
use: the write does not return success until the renamed entry is committed, so
a crash after success cannot leave the leased `auth.json` missing or stale. A
directory handle cannot be flushed on Windows, and the removal of a credential
entry has no file left to flush, so the durability of an unlink relies on NTFS
metadata journaling; if a crash undoes an unlink the credential reappears in the
private home and a Finish that finds it reports the cleanup as unconfirmed, so
the lease stays fenced until the file is retired again. The Windows code is validated by cross-compilation (`GOOS=windows go vet` and
`go test -c`) only, so the Windows behavior stays fail closed: any unconfirmed
identity, owner, ACL or link count reports an unconfirmed cleanup.

Residuals that no filesystem call can close, all failing closed where they are
observable. (1) Any process of the same user that opens or copies `auth.json`
while it exists keeps its own descriptor or copy of the plaintext after the
entry is retired; retirement removes directory entries and cannot revoke what a
process already read. (2) On Unix the link check is exact: the link count of the
retained descriptor after the unlink is final. On Windows an unlinked file that
is still open does not expose an exact remaining link count, so a hard link that
another process creates in a different directory in the instant between the
count read before the unlink and the unlink itself is not detectable; a link in
the same directory is found by the identity rescan, and a link that already
existed is found by the count. The Windows lease therefore guarantees
confirmation only against same-directory links and links that predate Finish.
(3) A credential written by the native Grok child into a path other than
`auth.json` or an attributed temporary is outside the attributed-name inventory;
runtime cleanup of the rest of the home handles it.

Native refresh, login execution and Worker-side watch dispatch for
Grok are separate lifecycle work and are not activated by this storage contract.

### Grok lease interleavings

Every failure, retry, crash and restart interleaving of Take, Worker login or
refresh, Finish and cleanup has one explicit outcome. Rules that hold for every
row: the account document is the only authority for which generation is current;
a generation the document does not reference is never granted; a lease is
released only by a confirmed outcome; and no row redistributes a credential. The
server tests are in `internal/server/grok_subscriptions_test.go` and
`internal/server/grok_subscriptions_interleaving_test.go`; the Worker and
library tests are in `internal/worker/grok_subscriptions_test.go` and
`internal/subscription/grok_credential_test.go`.

| Interleaving                                                                                           | Outcome                                                                                                                                                | Test                                                                                                                                                                                  |
| ------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Take of the queued original                                                                            | Lease committed, stored envelope re-opened and delivered once                                                                                          | `TestGrokLifecycleMovesBundleOnlyThroughProtectedTakeAndFinish`                                                                                                                       |
| Take on an unverified or below-baseline installation, or for execute                                   | Refused before any lease; account untouched                                                                                                            | `TestGrokTakeRefusesUnsupportedAndUnverifiedWork`, `TestGrokTakeRequiresSubscriptionBaselineVersion`                                                                                  |
| Take replayed after a lost first response                                                              | Denied; the exact unfinished lease is fenced; a finished or foreign lease is untouched                                                                 | `TestGrokReplayedTakeFencesTheUnfinishedLease`, `TestGrokLateReplayedTakeDoesNotFenceAReleasedAccount`                                                                                |
| Replaying request canceled, past its deadline or from a revoked Worker while the fence runs            | The fence still commits on a bounded, detached context; the lease is never stranded without `RecoveryRequired`                                         | `TestGrokReplayFenceIsDetachedFromTheReplayingRequest`                                                                                                                                |
| Take whose stored envelope is invalid or missing, or whose operation is canceled during the vault read | Nothing delivered; lease kept and account fenced under the current generation                                                                          | `TestGrokTakeFailuresAfterTheLeaseCommitFenceWithoutDelivery`                                                                                                                         |
| Installation rediscovered after Take                                                                   | Finish still accepted from the exact owner                                                                                                             | `TestGrokFinishSurvivesInstallationRediscoveryAfterTake`                                                                                                                              |
| Finish with a stale lease revision, generation or lease                                                | Denied before any write; no fence; the genuine owner can still finish                                                                                  | `TestGrokFinishWithAStaleLeaseIsDeniedWithoutFencing`                                                                                                                                 |
| Finish with a rebound, foreign, native, empty or wrong-identity bundle, or an unrotated refresh        | Refused; account fenced; nothing staged                                                                                                                | `TestGrokFinishRejectsReboundAndForeignBundles`, `TestGrokRefreshRequiresSameIdentityAndRotation`, `TestGrokLoginRejectsDuplicateIdentityAndAdmitsDifferentPrincipal`                 |
| Finish reports of login, refresh and logout (success, native refresh, cleanup)                         | Table-driven: released only when fully confirmed (or a failed login with confirmed cleanup); otherwise fenced under the existing generation            | `TestGrokFinishReportsMapToExplicitOutcomes`, `TestGrokFinishFencedLogoutKeepsTheReferencedGeneration`                                                                                |
| Finish publishes, then removes the superseded generation                                               | Removal only after the document stops referencing it; logout removes every generation                                                                  | `TestGrokFinishDeletesTheReferencedGenerationOnlyAfterPublication`                                                                                                                    |
| Finish aborted after staging (revoked Worker, store failure)                                           | Account fenced; current generation kept; staged generation retired after the account is proven not to reference it                                     | `TestGrokFinishFailedPublicationKeepsTheCurrentGeneration`, `TestGrokRetireUnpublishedGenerationRequiresProofOfNonPublication`                                                        |
| Finish aborted and the staged generation cannot be removed                                             | Account fenced; current generation kept; the staged generation stays vault-owned, never granted, removed by the next cleanup                           | `TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration`                                                                                                 |
| Cancellation wins while the new generation is staged                                                   | Generation never published; staged generation removed                                                                                                  | `TestGrokFinishCanceledDuringStagingRemovesTheStagedGeneration`                                                                                                                       |
| Removal of the superseded generation fails after the commit                                            | Error surfaced and account fenced; the published generation is kept, the superseded one is removed by the next cleanup                                 | `TestGrokFinishCleanupFailureAfterPublicationKeepsThePublishedGeneration`                                                                                                             |
| Request ends while the superseded generation is removed after the commit                               | Removal continues on a bounded, detached context; the committed publication is neither undone nor fenced                                               | `TestGrokFinishCleanupIsDetachedFromTheRequestAndBounded`, `TestGrokRetireUnpublishedGenerationSurvivesAnEndedRequest`                                                                |
| Finish retried after it committed                                                                      | Receipt replay; no vault write or removal; account untouched                                                                                           | `TestGrokFinishReplayRepeatsNoVaultWork`                                                                                                                                              |
| Worker instance lost, or server restarted, while a lease is held                                       | Lease fenced under its generation; later Finish and Take refused; the vault is unchanged                                                               | `TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish`                                                                                                                               |
| Server crash between staging and the commit                                                            | Same as a restart: lease fenced; the staged generation is an unreferenced vault object, never granted, removed by the next cleanup                     | `TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration`                                                                                                 |
| Worker materializes a bound envelope, a rebound or malformed one, or an unsafe home                    | Written once with owner-only permission; otherwise nothing written                                                                                     | `TestGrokMaterializationWritesPrivateAuthFileFromBoundEnvelope`, `TestGrokMaterializationRejectsReboundEnvelopeBeforeWriting`, `TestGrokMaterializationRefusesNonPrivateOrLinkedHome` |
| Home renamed away and replaced after binding, before or after the path check                           | The credential is written, read and retired only in the bound directory; the substitute is never written, read or modified                             | `TestGrokWriteIsAnchoredToTheBoundHomeNotItsPath`, `TestGrokWriteDoesNotFollowAPlantedAuthFileLink`, `TestGrokHomeRejectsALooseningAfterBinding`, `TestGrokHomeBindingFailsClosed`    |
| Fresh login in a home that already holds a credential or temporary                                     | Refused; a leftover is never sealed as the login result                                                                                                | `TestGrokFreshLoginRefusesLeftoverCredentialFiles`                                                                                                                                    |
| Native process rotates, fails, changes identity or leaves an unparsable file                           | Rotated credential sealed; otherwise no bundle; the file is retired in every case                                                                      | `TestPrepareGrokFinishSealsRotatedCredentialAndRetiresFile`                                                                                                                           |
| Remnant that cannot be removed, or a hard-linked credential                                            | No bundle; cleanup unconfirmed; lease stays fenced                                                                                                     | `TestPrepareGrokFinishWithholdsBundleWhenCleanupIsUnconfirmed`, `TestPrepareGrokFinishWithholdsBundleForHardLinkedCredential`                                                         |
| Hard link created after the credential was inspected and before its name is unlinked                   | No bundle; cleanup unconfirmed (retained-handle link count and identity rescan); lease stays fenced                                                    | `TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheUnlink`, `TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheTemporaryUnlink`, `TestGrokWriteRefusesAnOlderLinkedCredential`        |
| Home renamed away or replaced after materialization                                                    | No bundle and no cleanup for refresh, login and logout; nothing read or removed in the substitute; the original keeps its file                         | `TestPrepareGrokFinishRefusesARenamedOrReplacedHome`, `TestGrokHomeVerifiesTheBoundDirectoryIdentity`                                                                                 |
| Finish prepared for a lease that never bound a home                                                    | Cleanup never confirmed                                                                                                                                | `TestPrepareGrokFinishRefusesAnUnboundHome`, `TestGrokMaterializationBindsTheHomeForFinish`, `TestGrokMaterializationReleasesTheHomeWhenItFails`                                      |
| Worker crash or restart with the native file on disk                                                   | The server fences the lease (row above); the plaintext stays in the private home until Worker-side recovery, which is not activated; nothing is resent | Server side: `TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish`                                                                                                                  |

The Worker holds the only copy of a freshly produced bundle in memory until
Finish; a Finish whose response is lost leaves the fenced lease for recovery
rather than resending a credential. Owner recovery, cancellation and removal of a
fenced Grok account are separate lifecycle work and are not activated here.

### Grok backup and restore

A managed database backup is a SQLite image of server metadata. The protected
vault is outside the image, so a Grok bundle, its `key` and `refresh_token`, and
the native auth file never enter a backup; only the generation reference and
keyed identity commitment do. Restore treats a Grok account like every other
managed subscription (the quarantine is service-agnostic):

- The account is disconnected: no connection and health `disconnected`.
- An account that owned a generation, identity commitment, pending operation,
  lease or recovery fence becomes recovery-required and keeps only the keyed
  identity commitment and the historical generation reference as evidence. An
  allocated state with none of those is cleared.
- A pending operation and lease in the image (for example an unsettled logout
  cleanup) are retained unchanged as quarantined evidence, including owner
  machine, device, instance and epoch. Restore grants no authority to any
  Worker, and Take, Finish and execution admission are refused until a new login.
- Deleting the account never starts a native logout on a Worker that lacks the
  original home. Deletion stays refused by the recovery fence and, once that is
  cleared, by `protectedAccountDeletion` while the vault holds unremoved
  references.
- Finishing an unsettled Grok cleanup on the original Worker needs the Grok
  owner-recovery path, which is not activated. Until then restore retains the
  ownership evidence and never completes or reassigns the cleanup.

Restore also keeps copied Worker device records readable after revoking them.

## Security

### Grok diagnostics

`domain.GrokDiagnostic` (`internal/domain/grok_diagnostic.go`) mirrors the Codex
diagnostic. It holds the detected version, the supported version
(`domain.GrokSubscriptionBaselineVersion`, always set), a closed `domain.GrokPhase`
(`discovery`, `version`, `profile`, `runtime`, `login`, `refresh`, `logout`,
`models`, `execution`, `history`, `cleanup`, one-to-one with `GrokDiagnosticPhase`),
a stable `domain.Code` enumeration, a message, guidance and an optional
correlation ID. `WithGrokDiagnostic(version, phase, err)` keeps the first native
failure, drops untrusted versions and rebuilds message and guidance locally from
phase, code and versions, so native stderr, paths, URLs, tokens and identity never
enter a diagnostic. `Validate` rejects any stored value whose text differs from
that reconstruction.

Producers: `grok.Probe` tags every failure by probe phase (version check ->
`version`; runtime preparation, launch and initialize -> `runtime`; configuration
inspection -> `profile`; authenticate -> `login`; owned cleanup -> `cleanup`) and
logs `phase`, `diagnostic_phase`, `diagnostic_code`, `code`, `cleanup_unconfirmed`,
`installed_version` and `supported_version`. The first failure stays
authoritative when owned-process cleanup is also unconfirmed
(`domain.WithGrokCleanupFailure`):

| Probe result                                            | Cleanup of the owned ACP process | Diagnostic                      | Transport error                        |
| ------------------------------------------------------- | -------------------------------- | ------------------------------- | -------------------------------------- |
| success                                                 | confirmed                        | none                            | none                                   |
| success                                                 | unconfirmed                      | `cleanup` / `recovery_required` | `recovery_required`                    |
| failed in phase P                                       | confirmed                        | P with the failure's code       | the failure's own code                 |
| failed in phase P                                       | unconfirmed                      | P with the failure's code       | `recovery_required` (discovery aborts) |
| configuration inspection leaves its process unconfirmed | n/a                              | `cleanup` / `recovery_required` | `recovery_required`                    |

Timeout identity survives normalization. `domain.SafeError` collapses every
non-`*domain.Error` into a message-only `*domain.Error`, so the probe uses
`domain.SafeTimeoutError`, which marks an `Unavailable` result of an expired
deadline with `domain.TimeoutCause` (`timeout`) and makes it match
`context.DeadlineExceeded` with `errors.Is`. `WithGrokDiagnostic` then reports
the dedicated timeout reason ("The native operation timed out.") instead of the
generic "failed or timed out" reason:

| Error reaching `WithGrokDiagnostic`                            | Code          | Marker / `errors.Is(DeadlineExceeded)` | Reason              |
| -------------------------------------------------------------- | ------------- | -------------------------------------- | ------------------- |
| raw, wrapped or joined `context.DeadlineExceeded`              | `unavailable` | by `errors.Is`                         | timeout             |
| `SafeTimeoutError` of a deadline (probe wait, launch, inspect) | `unavailable` | `Cause=timeout`, matches               | timeout             |
| `SafeError` of a deadline (no marker)                          | `unavailable` | none                                   | failed or timed out |
| `unavailable` without a deadline (native exit, no frame)       | `unavailable` | none                                   | failed or timed out |
| `unavailable` with its own cause (for example `clone_timeout`) | `unavailable` | the original cause is kept             | failed or timed out |
| cancellation                                                   | `canceled`    | none                                   | canceled            |

The diagnostic's transport error never repeats the marker, and a timeout under
unconfirmed cleanup keeps the timeout reason while the transport code becomes
`recovery_required`.

An unconfirmed cleanup never replaces the first failure's phase, code or
timeout reason, but it always forces the transport error to `recovery_required`
because discovery must stop and reconcile the owned process before retrying.
A late protocol failure that the stdout drain reports while cleanup runs counts
as the first failure, and so does a partial trailing frame left in the drain's
frame buffer: `Wait` joins the drain whether or not the owned scope was
confirmed stopped, so an unconfirmed cleanup never hides either one
(`TestProbeCleanupFailureNeverReplacesTheFirstFailure`, rows "late protocol
failure" and "partial trailing frame"). Discovery stores the diagnostic on
`Installation.Protocol.GrokDiagnostic` (JSON `grok_diagnostic`) for a Grok
installation whose validation did not succeed, which is how a populated
1.0.52 `experiments` inventory becomes a `profile` / `unsupported` observation;
a verified profile, a non-Grok harness or a mismatched version rejects it.
Protected Take classifies a refusal in this order: a missing or undiscovered
installation is `discovery` and keeps the code discovery recorded (`missing` is
`not_found`, `permission-denied` is `permission_denied`, `failed` is
`unavailable`, `incompatible` and an unreported or unchecked installation are
`unsupported`; a recorded problem on a detected installation keeps its code), a version below the subscription baseline or invalid
metadata is `version` (even when the probe stored a `profile` diagnostic for a
release between the discovery baseline `1.0.41` and the subscription baseline),
and only a discovered installation at or above the baseline restores its stored
diagnostic unchanged or, without one, reports `profile` with the protocol state's
code (`failed` is `unavailable`, otherwise `unsupported`); the refusal carries the
diagnostic text and a structured warning log. Login, refresh,
logout, models, execution and history producers wrap their failures with
`WithGrokDiagnostic` and the matching phase when they land.

`ServerSubscriptionOperation.GrokDiagnostic` (JSON `grok_diagnostic`) is valid only
on a Grok account's settled operation, never together with the Codex diagnostic,
and its correlation ID equals the operation ID. `GetSubscriptionProgress` for a
Grok account returns the original operation's state and `grok_diagnostic` (field 10) after the usual actor check; Codex field 7 and native field 9 stay unset,
older clients ignore field 10, and no URL or user code is returned.

Persistence: the Worker-owned Grok lifecycle has no server-owned operation, so
`settleGrokOperation` records each outcome on the account's `ServerOperation`
(the only record progress serves) from the paths that exist today, always keyed
by the original operation ID and never overwriting a settled outcome or another
owner's active operation:

- A refused protected Take records the admission diagnostic (`discovery`,
  `version` or `profile`) with state `unsupported` (`recordGrokTakeRefusal`). The
  operation stays queued and claimable; the next successful Take clears the
  record. Only a Take that addresses the account's own operation exactly may
  annotate it (`grokTakeRefusalTarget`, checked in the refused transaction and
  again in the recording transaction): the same operation ID, lifecycle action
  and Runner Device, still queued and uncanceled, on an account that is not
  leased or fenced, whose lifecycle shape (login without a credential; refresh
  and logout with the connected generation) and initiating actor would
  otherwise admit the claim. Every other Take is refused with the same
  admission error and leaves no trace on any operation.
- Finish settles inside its own transaction and keeps the first failure
  authoritative: a succeeded login or refresh is `succeeded` with the committed
  generation, a succeeded logout is `succeeded` without one, a cancellation is
  `canceled` without a diagnostic, and a Worker-reported native failure
  (`succeeded=false`, or a refresh without refresh confirmation) is `failed` with
  a `login`, `refresh` or `logout` / `unavailable` diagnostic even when native
  cleanup is also unconfirmed. An unconfirmed native cleanup is the `cleanup` /
  `recovery_required` diagnostic only after a success or a cancellation.
  Progress reports `recovery-required` while the account is fenced.
- Canceling a queued, unclaimed operation (initiator device revocation, or the
  shared `cancelPendingLogin` body that `CancelSubscription` applies once Grok
  login can be requested; the RPC still refuses Grok accounts today) removes the
  pending operation and settles `cancelServerOperation`'s record as `canceled`.
  A Grok operation has no server record until it settles, so a cancellation
  creates the `canceled` record from the pending operation when none exists and
  replaces an earlier refused Take's provisional record, clearing its
  `GrokDiagnostic`; the original actor's poll then reads `canceled` instead of
  access denied. A settled record of the same operation is final and is kept.
  Other services keep settling only an existing record. A claimed login keeps
  its pending operation with `canceled=true` until Finish settles it.
  `SubscriptionState.Validate` rejects a `canceled` record that carries a Grok
  diagnostic, and progress never serves one.
- A failure to remove superseded credential generations after Finish settled
  replaces a `succeeded` or `canceled` record with a `cleanup` diagnostic
  (`recordGrokCleanupFailure`); a `failed` record keeps its own diagnostic and
  the recovery fence reports the outstanding cleanup.

`GetSubscriptionProgress` (`grokProgressView`) maps every state of one original
operation, so polling never fails merely because the operation has not settled:

| Account record                                                                                           | Progress state                                             |
| -------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| queued Pending, no `ServerOperation`                                                                     | `preparing`                                                |
| claimed Pending, no `ServerOperation`                                                                    | `waiting` (`canceled=true` once cancellation is requested) |
| refused Take (`unsupported` record, still queued)                                                        | `unsupported` with the admission diagnostic (provisional)  |
| `unsupported` record whose operation is no longer queued and uncanceled (defensive; never written today) | `unsupported` without a diagnostic (final)                 |
| settled `succeeded` / `failed`                                                                           | the record's state, diagnostic and committed generation    |
| settled `canceled`                                                                                       | `canceled`; a canceled record never carries a diagnostic   |
| active record past its window                                                                            | `expired`                                                  |
| account recovery fence                                                                                   | `recovery-required`                                        |
| any other operation ID, actor or no operation                                                            | access denied                                              |

Finish against the Worker's reported outcome (`S` native success, `R` refresh
confirmation, `C` native cleanup confirmation):

| Action  | Reported outcome                                     | Record state | Diagnostic (phase / code)       | Account fence |
| ------- | ---------------------------------------------------- | ------------ | ------------------------------- | ------------- |
| any     | success with `C` (refresh also with `R`)             | `succeeded`  | none                            | no            |
| any     | success without `C`                                  | `failed`     | `cleanup` / `recovery_required` | yes           |
| login   | failure with `C`                                     | `failed`     | `login` / `unavailable`         | no            |
| login   | failure without `C`                                  | `failed`     | `login` / `unavailable`         | yes           |
| login   | canceled with `C`                                    | `canceled`   | none                            | no            |
| login   | canceled without `C`                                 | `failed`     | `cleanup` / `recovery_required` | yes           |
| refresh | failure, or success without `R`, with or without `C` | `failed`     | `refresh` / `unavailable`       | yes           |
| logout  | failure, with or without `C`                         | `failed`     | `logout` / `unavailable`        | yes           |
| any     | vault removal fails after `succeeded` / `canceled`   | `failed`     | `cleanup` / the removal's code  | yes           |
| any     | vault removal fails after a native failure           | `failed`     | the native failure's diagnostic | yes           |

Cancellation against each state of the original login:

| State when canceled                                | Pending afterwards | Server record afterwards       | Progress                         |
| -------------------------------------------------- | ------------------ | ------------------------------ | -------------------------------- |
| queued, no record                                  | removed            | `canceled`, no diagnostic      | `canceled`, `canceled=true`      |
| queued after a refused Take (`unsupported` record) | removed            | `canceled`, diagnostic cleared | `canceled`, `canceled=true`      |
| claimed (lease held)                               | kept, `canceled`   | none until Finish              | `waiting`, `canceled=true`       |
| queued refresh or logout                           | kept               | unchanged                      | `preparing` (cancel refused)     |
| no pending operation                               | n/a                | unchanged                      | cancel refused (`conflict`)      |
| already canceled (second cancel)                   | n/a                | `canceled` kept                | `canceled`; Take cannot claim it |

A Take after an installation refusal is recorded or ignored as follows (the
refusal error returned to the Worker is the same in every row):

| Take addresses                                                                               | Recorded on the account                        | Progress afterwards          |
| -------------------------------------------------------------------------------------------- | ---------------------------------------------- | ---------------------------- |
| the queued original (same ID, action, machine)                                               | `unsupported` + diagnostic                     | `unsupported` + diagnostic   |
| the original ID with another lifecycle action                                                | nothing                                        | `preparing`                  |
| the original action on another Runner Device                                                 | nothing (rejected as Worker identity mismatch) | `preparing`                  |
| another operation ID                                                                         | nothing                                        | `preparing` (original)       |
| a canceled queued original                                                                   | nothing                                        | `preparing`, `canceled=true` |
| an original whose lifecycle shape is not admitted (for example login on a connected account) | nothing                                        | `preparing`                  |
| a claimed original (lease held)                                                              | nothing                                        | `waiting`                    |
| any Take while the account is fenced for recovery                                            | nothing                                        | `recovery-required`          |

Client classification of a progress response. A refused Take is the only
response a later claim can still supersede, so it is the only provisional one.
`grokProgressView` sets `Provisional` for exactly that row and serves the Grok
diagnostic with `unsupported` only while it is provisional; the wire therefore
marks a provisional response as `unsupported` plus `grok_diagnostic` (field 10),
and a final `unsupported` (Codex or Claude, or a Grok record with no queued
operation left) never carries it. The desktop classifies by that wire signal
(`progressDisposition` in `apps/delidev/src/subscription-progress.ts`): a
provisional response keeps polling and keeps the original sign-in alive, adopts
whatever the server reports next (state, diagnostics and committed generation),
and clears its stale diagnostic and message; a final response stops polling and
disposes. Inspect refreshes only the diagnostics of the state on screen, and a
different state is adopted by the next poll. Grok progress carries no URL or
user code, so observing a claimed Grok operation never enters the browser path
(`drivesBrowserLogin`). Every Codex, Claude and Grok row is listed, with the Go
test that asserts the server half and the vitest case that asserts the client
half:

| Account record (server)                                   | Progress state                      | Provisional | Client disposition and polling                                                  | Go test (`internal/server`)                                                         | TypeScript test                                                                                                      |
| --------------------------------------------------------- | ----------------------------------- | ----------- | ------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| queued Pending, no record                                 | `preparing`                         | no          | active: keep polling                                                            | `TestGrokProgressViewClassifiesEveryOperationState` (queued)                        | `subscription-progress.test.ts` (preparing)                                                                          |
| claimed Pending, no record                                | `waiting`                           | no          | active: keep polling; no browser path for Grok                                  | same (claimed)                                                                      | `subscription-progress.test.ts` (waiting, service drives the browser)                                                |
| refused Take, original queued and uncanceled              | `unsupported` + `grok_diagnostic`   | yes         | provisional: keep polling, adopt the next response                              | same (take refused, past the record window)                                         | `subscription-progress.test.ts`; `subscription-login.test.tsx` (provisional refusal then success, then failure)      |
| refused Take, original claimed, canceled or gone          | `unsupported`, no diagnostic        | no          | terminal: stop polling, dispose                                                 | same (claimed, canceled flag, no pending)                                           | `subscription-progress.test.ts` (unsupported); `subscription-login.test.tsx` (unsupported without a Grok diagnostic) |
| settled `succeeded`                                       | `succeeded` + generation            | no          | success: verify, name the account, stop polling                                 | same (finished succeeded)                                                           | `subscription-progress.test.ts` (succeeded); `subscription-login.test.tsx` (provisional refusal then success)        |
| settled `failed` (diagnostic)                             | `failed` + `grok_diagnostic`        | no          | terminal, even though it carries a Grok diagnostic                              | same (finished failed)                                                              | `subscription-progress.test.ts` and `subscription-login.test.tsx` (failed with a Grok diagnostic)                    |
| settled `canceled`                                        | `canceled`, never a diagnostic      | no          | terminal                                                                        | same (finished canceled, stale diagnostic)                                          | `subscription-login.test.tsx` (canceled)                                                                             |
| active record past its window                             | `expired`                           | no          | terminal                                                                        | same (active record after expiry)                                                   | `subscription-login.test.tsx` (expired)                                                                              |
| account recovery fence (any record, including a refusal)  | `recovery-required`                 | no          | terminal                                                                        | same (queued during recovery, settled under recovery, take refused during recovery) | `subscription-login.test.tsx` (recovery required)                                                                    |
| any other operation ID, actor or no operation             | access denied                       | n/a         | not a response: the poll reports the original sign-in as unverified and retries | same (another operation, no operation, no subscription)                             | `subscription-login.test.tsx` (unverified status checks)                                                             |
| Codex or Claude `unsupported` (native diagnostic or none) | `unsupported`, no `grok_diagnostic` | no          | terminal                                                                        | n/a (not a Grok record)                                                             | `subscription-progress.test.ts`; `claude-server-login.test.tsx` (unsupported)                                        |

Other readers of progress never poll a Grok operation: account deletion
(`account-deletion.tsx`) serves ChatGPT and Claude accounts only and routes a
Grok account to the plain configuration delete, and the CLI `login-progress`
operation prints one read without classifying it. Adding a new Grok progress
reader requires a row in this table and a test in both languages.

Every Grok log event states what its mutation persisted. A line named
`..._recorded` is emitted only after the mutation committed a changed record;
`..._not_recorded` says the mutation committed nothing and carries a closed
`reason` (`target_changed`: the account no longer holds the exact operation;
`record_kept`: an identical refusal, a settled outcome or an earlier native
failure stays authoritative; `diagnostic_invalid`: local validation dropped the
value, defensive); `..._unrecorded` says the store failed. Nothing is logged as
an outcome before the transaction that decides it has finished.

| Event                                            | Emitted                                                                                                                                                                                                  | Persisted  | Test (`internal/server`)                                                                                                                                              |
| ------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `grok_subscription_take_installation_refused`    | inside the refused Take, which rolls back; `refusal_recordable` says recording is allowed, not that it happened                                                                                          | never      | `TestGrokTakeInstallationRefusalLogsRecordableNotRecorded`                                                                                                            |
| `grok_subscription_take_refusal_recorded`        | after the recording transaction committed the changed record                                                                                                                                             | yes        | `TestGrokTakeRefusalLogsOnlyWhatItPersisted` (exact queued original, identical refusal first write)                                                                   |
| `grok_subscription_take_refusal_not_recorded`    | the recording transaction committed nothing, with `reason`                                                                                                                                               | no         | same (identical refusal again, another operation, another action, claimed original)                                                                                   |
| `grok_subscription_take_refusal_unrecorded`      | the recording transaction failed                                                                                                                                                                         | no         | same (missing account)                                                                                                                                                |
| `grok_subscription_operation_settled`            | after Finish's transaction committed, once; a replay logs nothing                                                                                                                                        | yes        | `TestGrokFinishLogsTheSettlementOnceAfterTheCommit`; `TestGrokFinishResultTableKeepsTheFirstFailure` (one line per row, `diagnostic_attached` only for a failure)     |
| `grok_subscription_cleanup_failure_recorded`     | the cleanup diagnostic replaced a succeeded or canceled outcome and committed                                                                                                                            | yes        | `TestGrokCleanupFailureLogsOnlyWhatItPersisted` (succeeded outcome); `TestGrokFinishVaultCleanupFailureNeverReplacesTheFirstFailure` (logout success, login canceled) |
| `grok_subscription_cleanup_failure_not_recorded` | the cleanup failure is real but no record changed: an earlier native diagnostic stays authoritative (`record_kept`) or the operation changed (`target_changed`), with `cleanup_phase` and `cleanup_code` | no         | same (native failure already stored its diagnostic, another operation); the Finish matrix (logout failure, login failure)                                             |
| `grok_subscription_cleanup_failure_unrecorded`   | the recording transaction failed                                                                                                                                                                         | no         | `TestGrokCleanupFailureLogsOnlyWhatItPersisted` (missing account)                                                                                                     |
| `grok_subscription_progress_observed`            | after a read-only progress read; `provisional` mirrors the response                                                                                                                                      | n/a (read) | `TestGrokProgressViewClassifiesEveryOperationState`                                                                                                                   |
| `Grok Build native handshake failed` (probe)     | after the probe outcome and owned-process cleanup are known; no persistence involved                                                                                                                     | n/a        | `TestProbeCleanupFailureNeverReplacesTheFirstFailure` in `harness/grok`                                                                                               |

A succeeded operation's progress response carries `generation` (the generation
the account still references) so the onboarding controller can name the account;
Grok produces no suggested name. The version label is the Runner Device's single
valid Grok Build installation at settlement time. Diagnostics for the `models`,
`execution` and `history` phases and for server-side native login, refresh and
logout have no producers yet; they remain covered by the phase mapping and
validation tests and attach through the same `settleGrokOperation` when those
producers land.

The desktop reconstructs presentation from closed metadata only
(`apps/delidev/src/grok-diagnostic.tsx`, English and Korean). Guidance follows
the closed phase and code (`grokGuidanceKey`): `permission_denied` in the
`discovery`, `version`, `profile`, `runtime` or `cleanup` phase asks to restore OS
permission; any other `discovery`, `version` or `profile` failure asks to install
the supported release; every other failure, including non-access `runtime` and
`cleanup` failures (noncanonical home, unexpected profile entry, symlink, invalid
inherited environment, launch or initialization), keeps the account and operation
and asks to check the installation. The reason sentence follows the same code
table as `GrokDiagnostic.text`; `TestGrokDiagnosticReasonFollowsTheSharedCodeTable`
and `grok-diagnostic.test.tsx` keep identical copies of it. `GrokDiagnosticNotice`
shows the message, that guidance and a Technical details disclosure with detected
version, supported version, failed step, error code and a copyable correlation
ID; it is reused by Settings > AI Subscription onboarding and by the Worker
installation row (`machine-settings.tsx`), which reads the stored
`protocol.grok_diagnostic`. The catalog keeps Grok Planned/Coming soon: the
notice adds no login control.

The desktop validates versions with the same rules as the server. A nonempty
detected version must satisfy `validInstallationVersion` (mirrors
`domain.ValidInstallationVersion`), and the `version` phase names the supported
baseline only when `validSemVer` (mirrors `golang.org/x/mod/semver.IsValid`,
so `01.0.0`, `1.0.0-01` and empty prerelease or build identifiers are invalid)
accepts it; otherwise it reports invalid version metadata exactly like
`GrokDiagnostic.text`. `TestGrokVersionReasonFollowsFullSemVerValidation` and
`grok-diagnostic.test.tsx` share one version table.
A diagnostic is presented only with a nonempty supported version that passes
both checks, because the server always reports the supported baseline; a stored
or received value that omits it is dropped as untrusted.

Login, refresh, execution and logout share one exclusive account lease. Logout
blocks new execution and completes only after original execution exit and
credential cleanup are confirmed; deletion reuses logout-before-delete.
Workers use the exact verified binary and a fresh private home. Managed bundles
move only through protected Take/Finish. Native refresh is confined to the lease;
verify the latest bundle and original identity, process exit and authentication
file cleanup before release. Backup/restore and deletion preserve protected
credential and unsettled original cleanup ownership.

Never retain tokens, authorization codes, device codes, login URLs or raw native
stderr in database documents, logs, events or frontend caches. Do not import or
modify a user's existing Grok home. Native callbacks remain bound to the original
trusted window, server, operation and Settings lifetime.

## Logging

Use structured Go/Rust logs with stable phase/code and original operation
correlation. Diagnostics exclude credentials, URLs, paths, identity and raw native
content. Record validation revision, commands, results and unresolved limits in
PRs and CI logs/artifacts rather than repository evidence documents.

## Build and Test

Run CLI `go test ./...`, desktop `pnpm test`, root `cargo test` for Rust changes
and protocol generation/checks. Generate required ignored `dist` outputs before
builds and remove them from the final worktree. Apply the Windows Worker/workspace
45-minute fixture watchdog independently of product deadlines.

Cover callback/state/nonce errors, cancellation races, expiry, duplicate delivery,
client disposal, server restart and uncertain exchange/refresh. Cover identity
duplication, account serialization, separate accounts, logout during execution,
cleanup failures, deletion and backup/restore. Exercise General Chat, single and
multiple repositories, Local/Worktree, Execute/Plan, native interactions, follow-up,
Stop/resume and process/server restart plus API/ChatGPT regressions.

Verify actual OAuth/native profiles at or above `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) and real local/remote account execution
before activation. A failed profile keeps support disabled until resolved.
Fixture/build/package results do not establish actual account or OS acceptance;
record supported-platform native and packaging evidence separately.

## Dependencies and Integrations

The official [CLI reference](https://docs.x.ai/build/cli/reference) defines browser
and device login. A complete feature PR may include the allocation records. Use
`pnpm proto:generate` for Go/TypeScript bindings and compatibility facades.

## Change Triggers

Changes to capability ownership, OAuth authority, bundle identity, lease cleanup,
native history or UI lifetime require updates to this contract, the project index,
protocol/structure/desktop/subscription contracts and applicable AGENTS files.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [Project index](project-delidev.md)
- [Subscription lifecycle](cmds-delidev-subscription-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Structure](cmds-delidev-structure-contract.md)
- [Subscription settings](apps-delidev-subscription-settings-contract.md)
- [Workspace](cmds-delidev-workspace-contract.md)
- [Repository defaults](repository-defaults.md)
