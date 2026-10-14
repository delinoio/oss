# DeliDev Grok Build subscription contract

## Scope

This contract owns the planned Grok Build subscription lifecycle (not yet
implemented or advertised; only the protocol identifiers below are declared) in
`cmds/delidev-cli`, its Connect interface and the desktop AI Subscription flow.
The subscription profile declares `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) as its minimum SemVer baseline. `domain.GrokSubscriptionVersionAllowed` admits a valid SemVer at or above it (prereleases of the baseline are below it) and gates protected Take admission of a Grok lease; no capability, discovery or execution path references it. Newer releases are validated natively, not rejected by number. The API and history baseline stays `domain.GrokProtocolVersion` (`1.0.41`); the known Grok Build 1.0.52 inspection failure is a separate limitation and does not change these baselines. Reservation support is separate
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
capability, and GetSubscriptionProgress never populates `grok_diagnostic`. The
owning feature change advertises and activates them:

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
capability advertisement. Existing System 30/35/36/37 and Worker 19 ownership
remains unchanged. The implementation reuses Request/Progress/Cancel/Callback
and protected Take/Finish Subscription RPCs. The selected account service and
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
and JSON encoding errors reveal only the service and a 12-character opaque
identity hash; generic JSON/text encoding of a bundle fails closed.

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
generation it published. The retirement carries no request authority, so the
revocation that aborted the Finish cannot skip it. When the account is
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
reachable. Cleanup is confirmed only when no credential file or attributed
temporary remains; otherwise no bundle is returned and the server keeps the
lease fenced. Native refresh, login execution and Worker-side watch dispatch for
Grok are separate lifecycle work and are not activated by this storage contract.

## Security

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
