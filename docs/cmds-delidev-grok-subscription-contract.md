# DeliDev Grok Build subscription contract

## Scope

This contract owns the Grok Build subscription lifecycle in `cmds/delidev-cli`,
its Connect interface and the desktop AI Subscription flow. Implementation is
partial; System 39 (server-owned login) is advertised only behind the explicit login switch. Implemented: the protocol identifiers below
(declared only), the bound credential bundle library, the protected-vault
generations, the protected Take/Finish lease handlers on the server, the
Worker-side credential custody described under Storage, the fixed xAI OAuth
profile, the server-owned Go browser login (loopback listener, PKCE, token
exchange, ID-token validation, bundle mapping and durable exchange claim), the
service-aware callback forwarding described under
[Server-owned browser login](#server-owned-browser-login), the server-owned
device-code login described under
[Server-owned device login](#server-owned-device-login) and the server-owned
refresh (the `refresh_token` grant under the account lease with a durable claim,
described under [Server-owned refresh](#server-owned-refresh)) and the
server-owned logout (provider revocation under the account lease, protected
cleanup and the Worker-lease logout retry, described under
[Server-owned logout](#server-owned-logout)) together with the
Worker-side confinement of a native refresh. System 39 covers both login
methods (decision D1: one capability for browser and device login together) and
is advertised only when the server enables Grok subscription login
(`server.Config.GrokSubscriptionLogin`, default off). The implementation is
complete without the switch, but no client can start a Grok login yet, and a
client that sees the capability expects to reach the flow, so the switch stays
off until a product entry point (the desktop AI Subscription flow) can start
both methods; no entry point sets it yet. The advertisement claims no
real-account acceptance: the opt-in real-account run was not performed. Not
activated: Worker-side watch dispatch, the native process execution that
produces a native refresh, and Grok execution (System 40, Worker 21).
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

These allocations are declared in the protos and generated bindings. GetStatus
advertises System 39 (browser and device login together) only when
`server.Config.GrokSubscriptionLogin` is set; it never lists System
40, and AttachWorker never lists Worker 21, the Worker does not report 21 and
the server rejects it as an unknown capability. GetSubscriptionProgress
populates `grok_diagnostic` only for a failed or uncertain server-owned Grok
browser or device login (phase `LOGIN`; see
[Server-owned browser login](#server-owned-browser-login) and
[Server-owned device login](#server-owned-device-login)) or refresh (phase
`REFRESH`; see [Server-owned refresh](#server-owned-refresh)) and never for a
Codex or Claude operation. The owning feature change advertises and activates
the remaining capabilities:

- System `GROK_SUBSCRIPTION_LOGIN_V1 = 39`.
- System `GROK_SUBSCRIPTION_EXECUTION_V1 = 40`.
- Worker `MANAGED_GROK_SUBSCRIPTIONS_V1 = 21`.
- `GetSubscriptionProgressResponse.grok_diagnostic = 10`, preserving Codex field 7.
- `GrokDiagnostic` fields detected version 1, supported version 2, phase 3,
  stable code 4, safe message 5, guidance 6 and correlation ID 7.
- Closed `GrokDiagnosticPhase` members UNSPECIFIED 0, DISCOVERY 1, VERSION 2,
  PROFILE 3, RUNTIME 4, LOGIN 5, REFRESH 6, LOGOUT 7, MODELS 8, EXECUTION 9,
  HISTORY 10 and CLEANUP 11.

These ledger reservations grant no execution or active schema; advertising 39
claims only the server-owned login. The storage and lease layer under Storage
does not depend on them. Existing System 30/35/36/37 and Worker 19 ownership
remains unchanged. The planned login reuses Request/Progress/Cancel/Callback
and protected Take/Finish Subscription RPCs (Take/Finish are implemented for Grok accounts). The selected account service and
`device_code` choose the flow; methods never switch automatically.

The fixed official xAI OAuth profile validates PKCE, state, nonce and tokens.
Browser callbacks bind the original IPv4 loopback address and ephemeral port.
Remote desktop callbacks retain the same original address and use a trusted
native receiver plus authenticated Connect forwarding. Device authorization
honors RFC 8628 pending, slow-down, denial and expiry; see
[Server-owned device login](#server-owned-device-login).

### Capability advertisement table

Each capability has one switch and one default. A switch is set only by a product
entry point that can actually reach the feature; no entry point sets any of them
yet. Tests pin every row in `grok_reservations_test.go`.

| Capability                                                                 | Switch                                | Default                                               | Advertised when   |
| -------------------------------------------------------------------------- | ------------------------------------- | ----------------------------------------------------- | ----------------- |
| System 39 `GROK_SUBSCRIPTION_LOGIN_V1` (browser and device login together) | `server.Config.GrokSubscriptionLogin` | off                                                   | the switch is set |
| System 40 `GROK_SUBSCRIPTION_EXECUTION_V1`                                 | none in this contract revision        | never advertised                                      | never             |
| Worker 21                                                                  | none in this contract revision        | never advertised, rejected as unknown by AttachWorker | never             |

### Server-owned browser login

`RequestSubscription` for a Grok subscription account with `machine_id` omitted
and `device_code=false` creates the same `ServerSubscriptionOperation` as the
Codex server login (15-minute expiry, original actor, epoch and finish
generation). The login action is described here, and refresh under
[Server-owned refresh](#server-owned-refresh) and logout under
[Server-owned logout](#server-owned-logout); `device_code=true` selects the
[device login](#server-owned-device-login) instead, and the Codex lifecycle still
refuses every Grok account (`subscriptionAccount`); the service-neutral server
gate is `serverSubscriptionAccount`. The Go flow lives in
`internal/subscription/grok_oauth.go` (profile, discovery, PKCE, ID-token
validation, mapping) and `internal/server/grok_login.go` (listener, one-shot
callback, claim, settlement). No native Grok process takes part in login.

- **Profile.** Issuer `https://auth.x.ai`, public client
  `b1a00492-073a-47ea-816f-4c329264a828`, scopes `openid profile email
offline_access grok-cli:access api:access conversations:read
conversations:write workspaces:read workspaces:write`, `referrer=grok-build`.
  Endpoints come from OIDC discovery and must live on the issuer origin
  (`issuer` equal to the constant, https, host `auth.x.ai`, no credentials,
  query or fragment); a metadata list that contradicts S256, the `none` token
  authentication method or ES256 fails discovery. S256 is the only challenge
  method sent. A test pins every value.
- **Listener.** One `tcp4` listener on `127.0.0.1` and an ephemeral port (always
  at least 1024), path `/callback`. The authorization URL
  (`response_type`, `client_id`, `redirect_uri`, `scope`, `code_challenge`,
  `code_challenge_method`, `state`, `nonce`, `referrer`, in that order; no other
  key) is returned only through the transient `GetSubscriptionProgress` `url`
  field. State, nonce and verifier are 43 characters of unpadded base64url from
  `crypto/rand`. The server validates its own URL with the same rules as the
  desktop receiver before publishing it.
- **One callback.** The listener accepts only `GET /callback` from a loopback
  peer with `Host` exactly `127.0.0.1:<port>`, query keys limited to `code`,
  `state`, `scope`, `error`, `error_description`, `error_uri` and `iss`, each
  once, and a state equal (constant time) to this login's. Any other request is
  refused (404, 400 or 409) without consuming the one-shot gate and without any
  outbound request, so stray local traffic cannot end the login. The first valid
  callback wins; a second one, a late one and a forwarded one after a direct one
  are refused. `error=access_denied` with the right state ends the login as
  failed (`PermissionDenied`) without a token request. The listener closes as soon
  as the callback is accepted and on every other exit path (cancellation, the
  15-minute window, server shutdown, any failure); a closed-port probe in the
  tests proves it.
- **Exchange.** The exchange claim is persisted first
  (`ServerSubscriptionOperation.CodeConsumed`, with `CodeSubmissionID` set to a
  fresh identifier or the forwarding delivery identifier, and `LoginMethod`
  `SubscriptionBrowserCallback`), in a transaction that also rechecks the actor,
  epoch, cancellation and expiry. Only then is one `POST` sent to the discovered
  token endpoint as a public client (`grant_type=authorization_code`,
  `client_id`, `redirect_uri`, `code`, `code_verifier`; no client secret, no
  `Authorization` header, no redirect following, no retry). A definitive provider
  rejection (HTTP 400 or 401 whose entire body is one OAuth `error` object; see the
  [response table](#token-endpoint-response-table)) settles the login as failed.
  A transport error, cancellation (`CancelSubscription`, shutdown or expiry during
  the request), timeout, unreadable or oversized body, unexpected status or a
  malformed success body is uncertain: the operation ends in
  `RECOVERY_REQUIRED` (account fenced, operation retained, `grok_diagnostic`
  code `recovery_required`) and the code is never resent. A claimed exchange is
  never released automatically.
- **Validation and mapping.** The ID token must be ES256-signed by a key from the
  issuer's JWKS (selected by `kid`; only ES256 is accepted), carry the exact
  issuer, the client as audience (a multi-valued audience also needs
  `azp`), the login's nonce (constant time) and an unexpired `exp` (60-second
  skew), and a nonempty `sub`; the userinfo `sub` must match. The bundle is built
  by `subscription.BuildGrokBundle` and re-validated by `ParseGrokAuthFile`.
  The token-to-`auth.json` mapping is an **unverified assumption** (it was never
  compared with the real CLI's file because the real-account run was not
  performed): `key` is the access token, `refresh_token` the refresh token,
  `create_time` the exchange time and `expires_at` that time plus `expires_in`;
  `user_id` is the `user_id` claim else the verified `sub`; `principal_type` and
  `principal_id` are the claims of those names (ID token, then userinfo), else
  `user` and the user identifier, and one without the other is refused; `email`,
  `first_name` (`first_name` or `given_name`), `profile_image_asset_id`,
  `team_id` and `coding_data_retention_opt_out` are copied only when present.
  Whether the IdP accepts a redirect on an arbitrary ephemeral loopback port for
  this client was observed only for the native CLI's own ports and is likewise
  unverified.
- **Publication.** A verified bundle is sealed with `SealGrokBundle` for the
  finish generation, staged under `credentials.AccountLogin` and published with
  the existing identity commitment (`grokIdentityCommitment`,
  `uniqueSubscriptionIdentity`): a duplicate identity ends the login as failed
  (`Conflict`, nothing stored) and a different principal of the same user
  succeeds. Success records the connection, the generation and the identity
  commitment and settles `SUCCEEDED`; progress then returns the generation and the
  transient `suggested_name` (the profile email, else `Grok`). The account
  document carries `subscription.server_operation.id` and the generation exactly
  as the desktop's naming verification expects.
- **Forwarding.** `ForwardSubscriptionCallback` is service-aware: a Grok account
  delivers its `code`/`state`/`scope` query (and no other key) once to the
  in-process login. The operation, original actor, server epoch, waiting state,
  unexpired window, the published Grok authorization host, the state of this login
  and the dynamic loopback port must all match; the dispatch is recorded
  (`CallbackForwarded`) before delivery, and a duplicate, late, wrong-state,
  wrong-account or wrong-operation delivery is refused. Codex forwarding (fixed
  port 1457) is unchanged.
- **Settlement.** Failed, canceled and expired logins clear the pending
  operation, disconnect the account and leave no credential or vault reference. A
  failed login records `grok_diagnostic` (phase `LOGIN`, a closed code, fixed safe
  message and guidance, the operation as correlation id) in the server operation;
  cancellation and the expiry window record none. A server restart marks an active
  operation recovery-required; a restart that happened before the exchange claim
  is settled automatically as a failed login (`recoverGrokLogin`), and one after
  the claim stays in recovery. Account logout and deletion are described under
  [Server-owned logout](#server-owned-logout).

### Server-owned device login

`RequestSubscription` for a Grok subscription account with `machine_id` omitted,
action `LOGIN` and `device_code=true` runs the RFC 8628 device authorization
grant in Go (`internal/subscription/grok_device.go` for the client,
`internal/server/grok_device.go` for the driver). It shares the operation model,
15-minute window, actor/epoch fences, finish generation, identity commitment
and settlement of the browser login. The method is the explicit flag: a device
request never opens a loopback listener or an authorization URL, a browser
request never contacts the device endpoint, and neither falls back to the
other. Replaying the same mutation id returns the original operation; the same
id with the other method is a conflict.

- **Authorization.** One `POST` of `client_id` and the browser flow's scope
  string (no client secret, no `Authorization` header, no redirect following, no
  retry) to the discovered `device_authorization_endpoint` (issuer origin only),
  else the fixed `https://auth.x.ai/oauth2/device/code`. The response must carry
  a `device_code` (printable ASCII, at most 2048 bytes, kept as owned bytes), a
  `user_code` matching `[A-Z0-9-]{4,32}` (for example `XXXX-XXXX`), a
  `verification_uri_complete` and/or `verification_uri` (https, host exactly
  `accounts.x.ai`, an absolute path, no credentials or fragment; a query may carry
  the provider's own routing or tracking parameters, each key a short token used
  once and each value short printable text, and the complete URI must carry a
  `user_code` equal to the user code, which is preserved with the safe
  parameters), a positive `expires_in` (at most 24 hours) and an optional
  `interval` of 1 to 60 seconds (default 5). The complete URL is preferred. Any
  other response ends the login as failed (`Unavailable`) before anything is
  claimed. The real provider's scopes, `interval`, `expires_in` and error bodies
  were never captured: these values are RFC 8628 defaults and the browser
  profile's scopes, and are an **unverified assumption**.
- **Presentation.** The verification URL and user code are returned only through
  the transient `GetSubscriptionProgress` `url` and `user_code` fields while the
  operation is waiting. `validServerLoginProgress` is service-aware: for Grok it
  accepts only an https `accounts.x.ai` URL without credentials or fragment plus
  a user code of the existing `[A-Z0-9-]{4,32}` shape. The URL, user code and
  device code never reach logs, documents, events or `grok_diagnostic`; the
  account document records only the closed login method (`LoginMethod` device
  code), the exchange claim and the outcome. Settled operations return neither
  field.
- **Polling.** The loop waits one interval before the first poll and between
  polls, so requests never come faster than the effective interval. `POST
/oauth2/token` (discovered endpoint) carries `grant_type`
  `urn:ietf:params:oauth:grant-type:device_code`, `client_id` and `device_code`.
  `authorization_pending` continues; `slow_down` adds 5 seconds to the interval
  for this and every later poll; `access_denied` ends the login as failed
  (`PermissionDenied`, the same code as a denied browser login);
  `expired_token` and the local deadline end it as expired. The local deadline is
  the minimum of the provider's `expires_in` and the remaining operation window
  (15 minutes), measured on an injectable clock; the loop waits out a remainder
  shorter than the interval and then expires without another poll. Any other
  OAuth error (HTTP 400 or 401) is a definitive rejection (failed,
  `PermissionDenied`).
- **Claim rule (decision).** The exchange claim (`CodeConsumed`,
  `CodeSubmissionID`, `LoginMethod` device code) is persisted once, immediately
  before the first token poll is sent, because every poll can redeem the device
  code. A definitive pending, slow-down, denied, expired or rejected answer is
  not uncertain. From the claim on, a poll whose outcome is unknown (transport
  error, cancellation or shutdown during the request, timeout, unreadable or
  oversized body, unexpected or rate-limit status, malformed success) ends in
  `RECOVERY_REQUIRED` (account fenced, operation retained, `grok_diagnostic` code
  `recovery_required`) and the device code is never sent again; no poll is
  retried. The device code lives only in memory, so a restart cannot resume
  polling: an interrupted login whose exchange was claimed stays in recovery and
  one that was never claimed settles as a failed login (`recoverGrokLogin`).
- **Outcome.** Cancel before any poll (including during the authorization
  request or the first wait) and cancel between polls settle as canceled, the
  latter keeping the recorded claim; a cancel during an in-flight poll is
  uncertain and ends in recovery. A server shutdown is not a cancel: after the
  claim, a shutdown between polls ends in recovery exactly like a crash (the device
  code lives only in memory and cannot be resumed), while a shutdown before the
  claim settles as canceled. Denial, expiry and rejection settle as failed
  (with `grok_diagnostic` for failures), expired (no diagnostic) and never leave a
  bundle, generation, connection or vault reference.
- **Bundle.** On tokens the same server function as the browser flow
  (`grokBundleOutcome`) verifies the ID token (signature, issuer, audience, time
  and subject; a device authorization request carries no nonce, so there is none
  to compare), reads userinfo and maps the bundle with `BuildGrokBundle`, so both
  methods yield identical bundles for the same token response. Sealing,
  duplicate-identity handling and publication are unchanged.

The trusted native receiver (`apps/delidev/src-tauri/src/oauth.rs`) exposes
`grok-subscription-open` and `grok-subscription-reopen` for the server-returned
browser URL. It validates the fixed `https://auth.x.ai/oauth2/authorize` shape
(public client `b1a00492-073a-47ea-816f-4c329264a828`, S256 PKCE, nonempty
state and nonce, redirect exactly `http://127.0.0.1:<port>/callback`), opens it
once. A local desktop binds nothing because the server listener receives the
redirect. A remote desktop uses forwarding: `ForwardSubscriptionCallback` is
service-aware for Grok (Grok host, state and the dynamic port bound for that
login) with Codex behavior unchanged, so the receiver's production default
`grok_remote_forwarding` is enabled (when it is disabled, a remote open is
refused with `Incompatible` before any listener or browser exists). The receiver
binds that IPv4 loopback port, accepts one
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

- DeliDev Grok Build subscription support follows `cmds-delidev-grok-subscription-contract.md`. System 39 (server-owned browser and device login) is advertised only behind `server.Config.GrokSubscriptionLogin`; System 40 and Worker 21 are declared but unadvertised until the owning feature activates them; the server-owned browser login, device login and refresh and progress field 10 (failed login or refresh only) are implemented. Declarations grant no native execution or migration. Preserve existing capability ownership, server-owned OAuth/protected credentials, original account leases and same-account/Worker history; verify the subscription profile at or above `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) by native validation before activation.

## Storage

Use optional existing Account/server-operation metadata and protected-store
references; add no database migration. Claim exchange and refresh durably before
sending. Uncertain results remain in recovery and are never resent. The browser
login claim is the optional `ServerSubscriptionOperation` `code_consumed`,
`code_submission_id`, `callback_forwarded` and `login_method` fields (shared
with the Claude server login) and the optional `grok_diagnostic`; the refresh
claim is the optional `refresh_claimed` field (omitted when false, so older
records are unchanged); none holds a
URL, code, verifier, state, nonce or token, which exist only in server memory for
the life of the login or refresh. Service
bundle validation binds service, issuer, user and principal and rejects duplicate
identity ownership. Native identity and complete history have a separate owned
checkpoint. Preserve older records without granting continuation authority.

### Grok bundle

`internal/subscription` owns the `GrokBundle` library (`grok_bundle.go` makes no
network or RPC call and needs no migration; the OAuth client in `grok_oauth.go`
performs its calls only through a transport the caller supplies). A bundle holds exactly one native `$GROK_HOME/auth.json` scope entry
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

Native login execution and Worker-side watch dispatch for Grok are separate
lifecycle work and are not activated by this storage contract. The Worker half
of a native refresh (custody, verification and cleanup of the latest
`auth.json`) is implemented and tested under
[Native refresh confinement](#native-refresh-confinement); the process execution
and watch dispatch that would produce one are not.

### Server-owned refresh

`RequestSubscription` for a connected Grok subscription account with `machine_id`
omitted, action `REFRESH` and `device_code=false` creates a
`ServerSubscriptionOperation` with action `refresh` (15-minute window, original
actor, epoch and finish generation). A refresh is available only to an account
that has a connection, a current generation and an identity commitment, and
cannot overlap another operation, an observation or a managed title reservation;
`CancelSubscription` remains login-only. The refresh is the explicit trigger: no
timer, maintenance task or pre-execution hook starts one yet. The Go flow lives in
`internal/subscription/grok_refresh.go` (the `refresh_token` client and the
refreshed-bundle mapping) and `internal/server/grok_refresh.go` (lease, claim,
settlement and recovery).

- **Lease.** The queued operation is claimed with `NativeStarted` and a claimed
  pending operation, with no Worker lease. That is the account's exclusive lease:
  Take, another operation and the login/refresh/logout gates all observe it, and
  a Worker Take of the server operation is refused.
- **Claim before send.** The stored generation is opened against this server and
  account, discovery supplies the JWKS and userinfo endpoints, and only then
  `refresh_claimed` is written in its own transaction that rechecks the actor,
  epoch, window, generation and the stored identity commitment. After the claim
  commits, the operation can only settle as an outcome of the one request below.
- **Request.** Exactly one `POST` to `https://auth.x.ai/oauth2/token` as a public
  client: `grant_type=refresh_token`, `client_id` and `refresh_token` only (no
  client secret, no `Authorization` header, no scope, no redirect following, no
  retry). The token endpoint is fixed; the refresh grant does not depend on a
  discovery answer.
- **Response.** A success needs an access token, a bearer token type and a
  positive `expires_in`; `refresh_token` and `id_token` are optional. An omitted
  or unchanged `refresh_token` means no rotation and the current one is kept. An
  ID token, when present, is verified like the login's (ES256 signature from the
  JWKS, issuer, audience and time; a refresh carries no nonce). The userinfo
  endpoint is read with the new access token; without an ID token its `sub` is
  the only subject evidence and must be present. The identity is mapped with the
  login mapping (`BuildGrokBundle`) and must equal the bound identity (user,
  principal type, principal ID); profile members the provider omits are carried
  over from the current bundle. `GrokRefreshed` then requires a later expiry and a
  changed key or refresh token. The rotation, field names and `invalid_grant`
  behavior of the real provider were not observed: the implementation and its
  tests model RFC 6749, and the opt-in capture stays unperformed.
- **Outcomes.** Success publishes the next generation (below). A definitive
  provider refusal (HTTP 400 or 401 whose entire body is one OAuth `error`
  object, for example
  `invalid_grant`) means the token did not rotate: the operation settles as
  `FAILED` with `grok_diagnostic` code `unauthenticated`, the stored bundle is
  kept, and the account health becomes `expired` until the owner signs in again.
  Everything else that happens after the claim is uncertain, because the
  provider may already have rotated the token that is now unrecorded: a
  transport failure, cancellation or shutdown, a timeout, a truncated or
  oversized body, an unexpected status (including 5xx), a malformed success, an
  unverifiable ID token or userinfo, an identity that differs from the bound
  one, and a crash at any later point. These end in `RECOVERY_REQUIRED` (account
  fenced, operation and claim retained, `grok_diagnostic` code
  `recovery_required`), and the old refresh token is never sent again. A failure
  before the claim (vault read, bundle validation, discovery, claim
  preconditions) sent nothing: it settles as `FAILED` with the stored credential
  untouched and the account health unchanged.
- **Publication.** The next bundle is sealed for the finish generation, staged
  under `credentials.AccountLogin` and published by one account commit that
  records the generation, clears the operation's lease and settles `SUCCEEDED`;
  the identity commitment is unchanged. Only after the commit are unreferenced
  generations removed. A removal that cannot be confirmed does not undo or fence
  the committed refresh: the superseded generation is unreferenced, never
  granted and removed by the next cleanup (it is logged as
  `grok_refresh_cleanup_pending`). A staging or commit failure retires the staged
  generation only after proving the account does not reference it. A settlement
  that fails before it commits fences the still-pending operation with the
  refresh `grok_diagnostic` (`recovery_required`), and a lost commit
  acknowledgement after the commit leaves the published refresh as it is. A claimed
  refresh is not discarded by a late cancellation or actor change, because its
  result is the only usable credential.
- **Restart.** A restart marks an active refresh recovery-required. One that
  never reached the claim sent nothing and settles automatically as a failed
  refresh (`recoverGrokRefresh`, health restored to ready), dispatched by the same
  subscription scanner as `recoverGrokLogin`; one with a claim stays in recovery
  and is never resolved automatically. Backup restore is
  blocked while a refresh is pending or fenced (the existing restore preflight
  covers the pending operation and the recovery fence), so a restored image can
  never resend a claimed token.
- **Logging.** Records use phase `REFRESH`, a closed step, the operation id as
  correlation id and stable codes; no token, URL, path or identity value is
  logged, and the fixture asserts it.

Fault-injection seams (`grokFaultHook`, test-only) cover the points after the
claim, after the response, after staging and after the commit.

### Server-owned logout

`RequestSubscription` for a connected Grok subscription account with `machine_id`
omitted and action `LOGOUT` creates a `ServerSubscriptionOperation` with action
`logout` (original actor, epoch and finish generation). The request is the
acceptance: it sets the account health to `revoked`, cancels the account's queued
executions and retires its queued titles, and a Take for new execution is refused
with a typed `UNAVAILABLE` problem ("This Grok account is logging out", logged as
`grok_subscription_take_logging_out`; `domain.Account.GrokLoggingOut` is the
shared predicate) while the logout waits, revokes and cleans up. It follows the
same lease rules as login and refresh: it cannot overlap another operation, an
observation or a recovery fence, `CancelSubscription` stays login-only, and a
separate account is never blocked. The Go flow lives in
`internal/subscription/grok_revoke.go` (the revocation client) and
`internal/server/grok_logout.go` (lease, claim, settlement, cleanup and recovery).

- **Wait for the original execution.** The operation is claimed only while no
  Worker lease exists. A Worker lease is released only by a Finish that confirmed
  the original process exit and the removal of `auth.json`, `auth.json.lock` and
  every attributed temporary from that Worker's private home (see
  [Native refresh confinement](#native-refresh-confinement)), so a claimed logout
  implies every Worker private home holds neither file. The claim starts a fresh
  15-minute window; waiting never expires an accepted logout. Because a lease is
  released only by a confirmed cleanup, an uncertain Worker cleanup keeps the
  account fenced and the logout waiting.
- **Claim before send.** The stored generation is opened against this server and
  account (the server reads the vault itself and never delivers the bundle to a
  Worker for a Go-owned logout), and `grok_revocation = claimed` is written in
  its own transaction that rechecks the epoch, lease, generation and the stored
  identity commitment. After the claim commits, the operation can only settle as an
  outcome of the one request below.
- **Request.** Exactly one `POST` to `https://auth.x.ai/oauth2/revoke` (RFC 7009,
  the fixed `revocation_endpoint` of the issuer metadata) as a public client:
  form fields `token` (the refresh token), `token_type_hint=refresh_token` and
  `client_id` only (no client secret, no `Authorization` header, no redirect
  following, no retry), through the same owned outbound route as login and
  refresh. The request parameters and the real provider's responses were not
  observed: the implementation and its tests model RFC 7009, and the opt-in
  capture stays unperformed.
- **Outcomes.** HTTP 200 is revoked. HTTP 400 or 401 with the OAuth error
  `invalid_token` or `invalid_grant` is an already-invalid token and counts as
  revoked. Any other 4xx is a definite refusal; 5xx, a transport failure, a
  timeout, cancellation or shutdown, an oversized body and any unexpected status
  are uncertain. A definite refusal and an uncertain outcome both end in
  `RECOVERY_REQUIRED` (account fenced, health `failed`, operation and claim
  retained, `grok_diagnostic` phase `LOGOUT` code `recovery_required`), the token
  is never sent again, and the protected bundle and its account references are
  never deleted. A failure before the claim (vault read, bundle validation, claim
  preconditions) sent nothing: it settles as `FAILED` with the credential intact
  and the account stays connected and `revoked`, so a new logout may be requested.
- **Cleanup.** A proven revocation is recorded durably (`grok_revocation =
revoked` and `revoked_generation` equal to the current generation) before any
  deletion. Only then is every vault generation of the account removed
  (`cleanupSubscriptionReferences` with nothing kept), and then the account is
  published as disconnected in one commit (no connection, generation, identity
  commitment or owner machine; health `disconnected`; the operation `SUCCEEDED`).
  A failed removal or publication keeps the account fenced with the revocation
  proven; the scanner retries only this cleanup with bounded backoff
  (`recoverGrokLogout`), so the token is never revoked twice.
- **Restart.** A restart marks an active logout recovery-required. One that never
  reached the claim sent nothing and settles automatically as a failed logout
  (health stays `revoked`); one with a confirmed revocation resumes its cleanup;
  one with only a claim stays in recovery and is never resolved or resent
  automatically. Backup restore is blocked while a logout is pending or fenced
  (the existing restore preflight covers the pending operation and recovery
  fence), and a restored image never keeps a revoked generation
  (`revoked_generation` retains recovery ownership).
- **Worker-lease logout (Take/Finish).** The protected Take/Finish `logout` action
  of the singleton lease stays available for a Worker-routed pending logout. Take
  of a logout lease and a Finish that deletes the bundle both require
  `revoked_generation` to equal the current generation, so the bundle is never
  deleted without a proven revocation (a Finish without it fences the account and
  keeps the generation). A logout whose Worker could not remove `auth.json` or
  `auth.json.lock` is fenced on its original lease; only that lease's exact Worker
  Device, Runner Device, instance and server epoch can report Finish again, and a
  confirmed retry releases the lease and removes every generation. A different
  Worker or instance is refused and the fence stays. Fenced leases of other
  actions are never released by a retry.
- **Deletion.** Deletion reuses logout-before-delete: a connected Grok account
  (generation, connection, pending operation, lease or recovery fence) cannot be
  deleted, so deletion proceeds only after the logout settled with the cleanup
  confirmed. An unproven revocation, an unconfirmed cleanup and a lease fenced
  by an unconfirmed Worker cleanup all block deletion. Failed-login cleanup
  (System 41) never adopts a Grok logout or a claimed Grok login; a settled
  Grok logout and a failed Grok login are ordinary disconnected accounts for the
  disconnected-account batch cleanup, which retains any account that still owns
  a protected vault reference.
- **Protected credentials.** `subscription.CredentialFiles` lists
  `grok/auth.json` and `grok/auth.json.lock` beside the Codex and Claude entries,
  so presence checks and retirement of a retained profile cover the Grok
  credential and its advisory lock file (and their attributed temporaries).
  Diagnostics never read a Grok bundle (the Doctor inspection is unavailable for
  the service), and restore never restores a Grok generation.
- **Logging.** Records use phase `LOGOUT` (lease, claim, revocation, settlement)
  or `CLEANUP` (vault removal and disconnect), a closed step, the operation id as
  correlation id and stable codes; no token, URL, path, email or identity value
  is logged, and the fixtures assert it.

Fault-injection seams (`grokFaultHook`, test-only) cover the points after the
revocation claim, after the revocation response, after the revocation is
recorded and after the vault removal.

### Server operation lifecycle table

Every server-owned Grok operation passes through the same stages, and each cell
below has one durable outcome. "Fenced" means the account is
`RECOVERY_REQUIRED` (`recovery_required` set, health failed, operation retained);
the diagnostic always carries the phase of the operation (`LOGIN` or `REFRESH`)
and the operation as correlation id. The scanner settles only a fenced operation
whose exchange was never claimed; a claim is never released automatically.

| Stage reached                       | Event                                                                         | Outcome                                                                                                                                           | Recovery kind        | Diagnostic                            |
| ----------------------------------- | ----------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------- | ------------------------------------- |
| queued, not leased                  | restart                                                                       | fenced by the startup fence, then settled `FAILED` by the scanner (`recoverGrokLogin` or `recoverGrokRefresh`); a refresh restores health `ready` | automatic            | `unavailable`                         |
| queued, not leased                  | cancel (login only)                                                           | `CANCELED`, pending cleared                                                                                                                       | none                 | none                                  |
| leased, no claim                    | shutdown or cancel                                                            | `CANCELED`                                                                                                                                        | none                 | none                                  |
| leased, no claim                    | window expiry                                                                 | `EXPIRED`                                                                                                                                         | none                 | none                                  |
| leased, no claim                    | restart                                                                       | fenced, then settled `FAILED` by the scanner                                                                                                      | automatic            | `unavailable`                         |
| leased, no claim                    | failure before the claim (vault, discovery, listener)                         | `FAILED`, credential untouched                                                                                                                    | none                 | the failure code                      |
| claimed device login, between polls | server shutdown                                                               | fenced, the claim is retained                                                                                                                     | manual, never resent | `recovery_required`                   |
| claimed device login, between polls | user cancel                                                                   | `CANCELED`, the claim stays recorded and no further poll is sent                                                                                  | none                 | none                                  |
| claimed, request not sent           | restart or crash                                                              | fenced, the claim is retained                                                                                                                     | manual, never resent | `recovery_required`                   |
| claimed, request sent               | transport failure, timeout, shutdown, cancel, unparsed or unexpected response | fenced                                                                                                                                            | manual, never resent | `recovery_required`                   |
| claimed, request sent               | definitive refusal (see the [response table](#token-endpoint-response-table)) | `FAILED` (refresh also sets health `expired`)                                                                                                     | none                 | `unauthenticated` or the refusal code |
| claimed refresh, response received  | identity, ID token, userinfo or mapping not verified                          | fenced                                                                                                                                            | manual               | `recovery_required`                   |
| claimed login, response received    | ID token, userinfo or mapping not verified                                    | `FAILED`, nothing stored (there is no credential to protect)                                                                                      | none                 | the failure code                      |
| claimed, response verified          | settlement committed                                                          | `SUCCEEDED`, next generation published                                                                                                            | none                 | none                                  |
| claimed, response verified          | settlement write failed before the commit                                     | fenced, staged generation retired                                                                                                                 | manual               | `recovery_required`                   |
| any stage                           | settlement commit acknowledgement lost after the commit                       | the committed outcome stands, nothing is rewritten                                                                                                | none                 | as committed                          |
| any stage                           | cancel after a claim                                                          | ignored: a claimed operation never settles as canceled                                                                                            | manual               | `recovery_required`                   |

### Commit acknowledgement ambiguity

A transaction can commit and still report an error (a lost acknowledgement). Each
durable transaction of the Grok flows therefore has one reconcile rule, and none
of them marks a committed transaction as failed or fences it again. The finish
paths of login and refresh reconcile through `fenceUnsettledGrokOperation`: it
re-reads the durable operation and fences it only while it is still the account's
pending native-started operation.

| Transaction                                                  | Ambiguous error                                                   | Reconcile rule                                                                                                                                                                                                                                                                     |
| ------------------------------------------------------------ | ----------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| lease (`grok.server.refresh-lease`, `grok.server.claim`)     | the lease may be durable with no owner running                    | the runner re-reads the operation: a durable lease (native-started, pending) is fenced with the phase diagnostic so the scanner settles it as `FAILED`; an operation that is still queued is left for the next scan (`TestGrokLeaseWithALostAcknowledgementIsFencedForTheScanner`) |
| exchange claim (`claimGrokExchange`, `claimGrokRefresh`)     | the claim flag may be durable although the request was never sent | the request is not sent after any claim error, so the operation settles `FAILED` with the stored credential untouched and the flag is inert on a settled operation; a claim whose commit succeeded is the only way a request is sent                                               |
| stage (vault write of the next generation)                   | the generation may exist                                          | an unpublished generation is retired only after proving the account does not reference it                                                                                                                                                                                          |
| publish (`grok.server.finish`, `grok.server.refresh-finish`) | the settlement may be committed                                   | re-read: a settled operation is the outcome; a still-pending one is fenced with the phase diagnostic; superseded generations are removed by the next cleanup                                                                                                                       |

### Token endpoint response table

The authorization-code exchange and the refresh grant share one response parser
(`parseGrokTokenBody`). A body is parsed only when it is exactly one JSON object:
another JSON value, a stray comma or any other non-whitespace byte after the first
object makes the response unparsed, whatever its status. Both grants are claimed
before the request, so an unparsed response is never a published success and never
a definitive rejection that would release the claim. The table is pinned by
`TestGrokTokenResponseTable`, which runs every row against both grants.

| Status                                                     | Body                                                          | Reason                     | Settlement after the claim         |
| ---------------------------------------------------------- | ------------------------------------------------------------- | -------------------------- | ---------------------------------- |
| 200                                                        | one valid token object (trailing whitespace allowed)          | success                    | publish                            |
| 200                                                        | token object followed by another JSON value or any other byte | `GrokReasonTokenResponse`  | `RECOVERY_REQUIRED`                |
| 200                                                        | missing or invalid member, not JSON                           | `GrokReasonTokenResponse`  | `RECOVERY_REQUIRED`                |
| 400, 401                                                   | exactly one object with a non-empty `error`                   | `GrokReasonTokenRejected`  | `FAILED`, the token did not rotate |
| 400, 401                                                   | an `error` object followed by any other byte or JSON value    | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| 400, 401                                                   | no `error` member or not JSON                                 | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| other 4xx, 5xx, redirect                                   | any body                                                      | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| transport error, cancellation, truncated or oversized body | none                                                          | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |

### Native refresh confinement

A native `grok` process may refresh the credential itself inside a Worker's
private home. That refresh stays inside the lease: before release the Worker
reads the latest `auth.json` once and requires the original bound identity,
requires the native process to have exited, retires `auth.json`, its
`auth.json.lock` advisory lock file and every attributed temporary through the
held home handle, and reports cleanup as confirmed only when none remains (the
lock file is a retirement target and counts in the presence check, so a lock that
cannot be removed leaves cleanup unconfirmed). A mismatched identity, an
unreadable or truncated file, a failed process or an unconfirmed cleanup returns
no bundle and no refresh confirmation, so the server keeps the lease and sets
`RECOVERY_REQUIRED`. The process execution and watch dispatch that would produce
a native refresh remain separate work.

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

| Interleaving                                                                                           | Outcome                                                                                                                                                   | Test                                                                                                                                                                                                                                                                                                          |
| ------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Take of the queued original                                                                            | Lease committed, stored envelope re-opened and delivered once                                                                                             | `TestGrokLifecycleMovesBundleOnlyThroughProtectedTakeAndFinish`                                                                                                                                                                                                                                               |
| Take on an unverified or below-baseline installation, or for execute                                   | Refused before any lease; account untouched                                                                                                               | `TestGrokTakeRefusesUnsupportedAndUnverifiedWork`, `TestGrokTakeRequiresSubscriptionBaselineVersion`                                                                                                                                                                                                          |
| Take replayed after a lost first response                                                              | Denied; the exact unfinished lease is fenced; a finished or foreign lease is untouched                                                                    | `TestGrokReplayedTakeFencesTheUnfinishedLease`, `TestGrokLateReplayedTakeDoesNotFenceAReleasedAccount`                                                                                                                                                                                                        |
| Replaying request canceled, past its deadline or from a revoked Worker while the fence runs            | The fence still commits on a bounded, detached context; the lease is never stranded without `RecoveryRequired`                                            | `TestGrokReplayFenceIsDetachedFromTheReplayingRequest`                                                                                                                                                                                                                                                        |
| Take whose stored envelope is invalid or missing, or whose operation is canceled during the vault read | Nothing delivered; lease kept and account fenced under the current generation                                                                             | `TestGrokTakeFailuresAfterTheLeaseCommitFenceWithoutDelivery`                                                                                                                                                                                                                                                 |
| Installation rediscovered after Take                                                                   | Finish still accepted from the exact owner                                                                                                                | `TestGrokFinishSurvivesInstallationRediscoveryAfterTake`                                                                                                                                                                                                                                                      |
| Finish with a stale lease revision, generation or lease                                                | Denied before any write; no fence; the genuine owner can still finish                                                                                     | `TestGrokFinishWithAStaleLeaseIsDeniedWithoutFencing`                                                                                                                                                                                                                                                         |
| Finish with a rebound, foreign, native, empty or wrong-identity bundle, or an unrotated refresh        | Refused; account fenced; nothing staged                                                                                                                   | `TestGrokFinishRejectsReboundAndForeignBundles`, `TestGrokRefreshRequiresSameIdentityAndRotation`, `TestGrokLoginRejectsDuplicateIdentityAndAdmitsDifferentPrincipal`                                                                                                                                         |
| Finish reports of login, refresh and logout (success, native refresh, cleanup)                         | Table-driven: released only when fully confirmed (or a failed login with confirmed cleanup); otherwise fenced under the existing generation               | `TestGrokFinishReportsMapToExplicitOutcomes`, `TestGrokFinishFencedLogoutKeepsTheReferencedGeneration`                                                                                                                                                                                                        |
| Finish publishes, then removes the superseded generation                                               | Removal only after the document stops referencing it; logout removes every generation                                                                     | `TestGrokFinishDeletesTheReferencedGenerationOnlyAfterPublication`                                                                                                                                                                                                                                            |
| Finish aborted after staging (revoked Worker, store failure)                                           | Account fenced; current generation kept; staged generation retired after the account is proven not to reference it                                        | `TestGrokFinishFailedPublicationKeepsTheCurrentGeneration`, `TestGrokRetireUnpublishedGenerationRequiresProofOfNonPublication`                                                                                                                                                                                |
| Finish aborted and the staged generation cannot be removed                                             | Account fenced; current generation kept; the staged generation stays vault-owned, never granted, removed by the next cleanup                              | `TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration`                                                                                                                                                                                                                         |
| Cancellation wins while the new generation is staged                                                   | Generation never published; staged generation removed                                                                                                     | `TestGrokFinishCanceledDuringStagingRemovesTheStagedGeneration`                                                                                                                                                                                                                                               |
| Removal of the superseded generation fails after the commit                                            | Error surfaced and account fenced; the published generation is kept, the superseded one is removed by the next cleanup                                    | `TestGrokFinishCleanupFailureAfterPublicationKeepsThePublishedGeneration`                                                                                                                                                                                                                                     |
| Request ends while the superseded generation is removed after the commit                               | Removal continues on a bounded, detached context; the committed publication is neither undone nor fenced                                                  | `TestGrokFinishCleanupIsDetachedFromTheRequestAndBounded`, `TestGrokRetireUnpublishedGenerationSurvivesAnEndedRequest`                                                                                                                                                                                        |
| Finish retried after it committed                                                                      | Receipt replay; no vault write or removal; account untouched                                                                                              | `TestGrokFinishReplayRepeatsNoVaultWork`                                                                                                                                                                                                                                                                      |
| Worker instance lost, or server restarted, while a lease is held                                       | Lease fenced under its generation; later Finish and Take refused; the vault is unchanged                                                                  | `TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish`                                                                                                                                                                                                                                                       |
| Server crash between staging and the commit                                                            | Same as a restart: lease fenced; the staged generation is an unreferenced vault object, never granted, removed by the next cleanup                        | `TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration`                                                                                                                                                                                                                         |
| Worker materializes a bound envelope, a rebound or malformed one, or an unsafe home                    | Written once with owner-only permission; otherwise nothing written                                                                                        | `TestGrokMaterializationWritesPrivateAuthFileFromBoundEnvelope`, `TestGrokMaterializationRejectsReboundEnvelopeBeforeWriting`, `TestGrokMaterializationRefusesNonPrivateOrLinkedHome`                                                                                                                         |
| Home renamed away and replaced after binding, before or after the path check                           | The credential is written, read and retired only in the bound directory; the substitute is never written, read or modified                                | `TestGrokWriteIsAnchoredToTheBoundHomeNotItsPath`, `TestGrokWriteDoesNotFollowAPlantedAuthFileLink`, `TestGrokHomeRejectsALooseningAfterBinding`, `TestGrokHomeBindingFailsClosed`                                                                                                                            |
| Fresh login in a home that already holds a credential or temporary                                     | Refused; a leftover is never sealed as the login result                                                                                                   | `TestGrokFreshLoginRefusesLeftoverCredentialFiles`                                                                                                                                                                                                                                                            |
| Native process rotates, fails, changes identity or leaves an unparsable file                           | Rotated credential sealed; otherwise no bundle; the file is retired in every case                                                                         | `TestPrepareGrokFinishSealsRotatedCredentialAndRetiresFile`                                                                                                                                                                                                                                                   |
| Remnant that cannot be removed, or a hard-linked credential                                            | No bundle; cleanup unconfirmed; lease stays fenced                                                                                                        | `TestPrepareGrokFinishWithholdsBundleWhenCleanupIsUnconfirmed`, `TestPrepareGrokFinishWithholdsBundleForHardLinkedCredential`                                                                                                                                                                                 |
| Hard link created after the credential was inspected and before its name is unlinked                   | No bundle; cleanup unconfirmed (retained-handle link count and identity rescan); lease stays fenced                                                       | `TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheUnlink`, `TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheTemporaryUnlink`, `TestGrokWriteRefusesAnOlderLinkedCredential`                                                                                                                                |
| Home renamed away or replaced after materialization                                                    | No bundle and no cleanup for refresh, login and logout; nothing read or removed in the substitute; the original keeps its file                            | `TestPrepareGrokFinishRefusesARenamedOrReplacedHome`, `TestGrokHomeVerifiesTheBoundDirectoryIdentity`                                                                                                                                                                                                         |
| Finish prepared for a lease that never bound a home                                                    | Cleanup never confirmed                                                                                                                                   | `TestPrepareGrokFinishRefusesAnUnboundHome`, `TestGrokMaterializationBindsTheHomeForFinish`, `TestGrokMaterializationReleasesTheHomeWhenItFails`                                                                                                                                                              |
| Worker crash or restart with the native file on disk                                                   | The server fences the lease (row above); the plaintext stays in the private home until Worker-side recovery, which is not activated; nothing is resent    | Server side: `TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish`                                                                                                                                                                                                                                          |
| Server refresh with rotation, without rotation (omitted or unchanged token) or without an ID token     | Next generation staged, published by one commit and superseded generations removed; identity commitment unchanged                                         | `TestGrokRefreshReplacesTheBundleWithRotation`, `TestGrokRefreshWithoutRotationKeepsTheRefreshToken`, `TestGrokRefreshWorksWithoutAnIDToken`                                                                                                                                                                  |
| Server logout claim, request and outcomes                                                              | Claim durable before the one revocation request; revoked and already-invalid settle; 4xx, 5xx and timeout fence with the bundle kept and are never resent | `TestGrokLogoutRevokesTheRefreshTokenThenDeletesTheBundle`, `TestGrokLogoutAlreadyInvalidTokenCountsAsRevoked`, `TestGrokLogoutUnprovenRevocationLeavesRecoveryAndKeepsTheBundle`, `TestGrokLogoutTimeoutOrShutdownDuringRevocationIsUncertain`, `TestGrokRevokeClassifiesEveryOutcome`                       |
| Server logout crash points and cleanup failure                                                         | No resend and no bundle deletion without recovery state; a proven revocation only repeats the cleanup                                                     | `TestGrokLogoutCrashPointsNeverResendOrDeleteWithoutRecoveryState`, `TestGrokLogoutCleanupFailureIsFencedRetriedAndNeverRevokesTwice`, `TestGrokLogoutBeforeTheClaimRestartKeepsTheCredentialAndAllowsANewLogout`                                                                                             |
| Logout during execution, serialization and deletion                                                    | Waits for the original lease; new execution is typed unavailable; one account serialized, others independent; deletion only after confirmed cleanup       | `TestGrokLogoutWaitsForTheOriginalExecutionLease`, `TestGrokLogoutSerializesWithOtherOperationsOnOneAccountOnly`, `TestGrokDeleteRequiresAConfirmedLogoutFirst`, `TestGrokDeleteIsBlockedByUnprovenAndUnconfirmedLogouts`, `TestGrokCleanupTreatsTheBundleAsProtected`, `TestGrokCleanupFailedLoginSelection` |
| Worker-lease logout, Worker home removal failure                                                       | Revocation required; fenced on failure; same Worker retry succeeds; other Worker refused                                                                  | `TestGrokWorkerLogoutRequiresRevocationAndRetriesOnlyOnTheOriginalOwner`, `TestGrokWorkerLogoutWithoutRevocationFencesInsteadOfDeleting`, `TestGrokFencedRefreshLeaseIsNotRetryable`                                                                                                                          |
| Server refresh claim and lease                                                                         | The claim is durable before the token request; the account stays exclusively leased; no other operation, Take or logout is admitted                       | `TestGrokRefreshWritesTheClaimBeforeTheTokenRequestAndHoldsTheLease`                                                                                                                                                                                                                                          |
| Provider refuses the refresh token (`invalid_grant`)                                                   | Plain failure; bundle kept; health `expired`; exactly one request                                                                                         | `TestGrokRefreshFailuresNeverReplaceTheBundle`                                                                                                                                                                                                                                                                |
| 5xx, truncated body, malformed success, unverifiable ID token or userinfo, changed identity            | Bundle kept; `RECOVERY_REQUIRED`; claim retained; the token is never resent                                                                               | `TestGrokRefreshFailuresNeverReplaceTheBundle`                                                                                                                                                                                                                                                                |
| Failure before the claim (for example discovery)                                                       | Plain failure; no token request; the credential is refreshable by a later request                                                                         | `TestGrokRefreshProviderFailureBeforeTheClaimSendsNothing`                                                                                                                                                                                                                                                    |
| Crash after the claim, after the response or after staging                                             | `RECOVERY_REQUIRED` after restart; at most one request and none after the restart; the current generation unchanged                                       | `TestGrokRefreshFaultInjectionSendsOneRequestAndFencesUncertainOutcomes`                                                                                                                                                                                                                                      |
| Crash after the commit                                                                                 | Refresh published; the superseded generation is removed by the next refresh                                                                               | `TestGrokRefreshFaultInjectionSendsOneRequestAndFencesUncertainOutcomes`                                                                                                                                                                                                                                      |
| Restart before the claim                                                                               | Settles as a failed refresh; credential intact; health restored                                                                                           | `TestGrokRefreshRestartBeforeTheClaimFreesTheOperation`                                                                                                                                                                                                                                                       |
| Removal of the superseded generation fails after the refresh commit                                    | Refresh stays published and unfenced; the old generation awaits the next cleanup                                                                          | `TestGrokRefreshCleanupFailureAfterThePublishDoesNotUndoIt`                                                                                                                                                                                                                                                   |
| Restore while a refresh is claimed, fenced or pending                                                  | Restore refused; settled refresh admitted                                                                                                                 | `TestBackupRestoreBlocksAnUnsettledGrokRefresh`                                                                                                                                                                                                                                                               |
| Native refresh rewrites `auth.json` and leaves `auth.json.lock`                                        | Rotated credential sealed and reported with refresh and cleanup confirmed; both files removed                                                             | `TestNativeRefreshFinishReturnsTheRotatedCredentialAndRetiresBothFiles`, `TestRetireGrokAuthFileRemovesTheNativeLockFile`                                                                                                                                                                                     |
| Native refresh with a foreign identity, an unreadable or truncated file, or a failed process           | No bundle and no refresh confirmation; files retired; the server keeps the lease fenced                                                                   | `TestNativeRefreshFinishNeverConfirmsAMismatchedOrUnreadableCredential`, `TestGrokFinishReportsMapToExplicitOutcomes`                                                                                                                                                                                         |
| Native refresh with a lock or temporary that cannot be removed, or a swapped home                      | No bundle, no cleanup confirmation; lease stays fenced                                                                                                    | `TestNativeRefreshFinishWithholdsEverythingWhenCleanupIsUnconfirmed`, `TestNativeRefreshFinishRefusesASwappedHome`                                                                                                                                                                                            |

The Worker holds the only copy of a freshly produced bundle in memory until
Finish; a Finish whose response is lost leaves the fenced lease for recovery
rather than resending a credential. Owner recovery, cancellation and removal of a
fenced Grok account are separate lifecycle work and are not activated here.

## Security

Login, refresh, execution and logout share one exclusive account lease. Logout
blocks new execution and completes only after original execution exit, provider
revocation of the refresh token and credential cleanup are confirmed; deletion
reuses logout-before-delete (see [Server-owned logout](#server-owned-logout)).
Workers use the exact verified binary and a fresh private home. Managed bundles
move only through protected Take/Finish. Native refresh is confined to the lease;
verify the latest bundle and original identity, process exit and authentication
file cleanup (`auth.json` and `auth.json.lock`) before release. Backup/restore
and deletion preserve protected credential and unsettled original cleanup
ownership, including a claimed Grok refresh.

Never retain tokens, authorization codes, device codes, login URLs or raw native
stderr in database documents, logs, events or frontend caches. The server-owned
browser login logs only closed values (phase `LOGIN`, step, operation id as
correlation id, stable codes); the tests assert that no URL, code, verifier,
state, nonce or token reaches the logs or the database files. Its outbound calls
use the explicit server outbound route, no proxy fallback, no redirects and
no connection reuse, and the token response is bounded to 64 KiB. Do not import or
modify a user's existing Grok home. Native callbacks remain bound to the original
trusted window, server, operation and Settings lifetime.

## Logging

Use structured Go/Rust logs with stable phase/code and original operation
correlation. Server-owned Grok login records use phase `LOGIN`, refresh
records use phase `REFRESH`, logout records use phase `LOGOUT` and the logout's
vault and metadata cleanup records use phase `CLEANUP`, each with a closed step and the operation id as
correlation id. Diagnostics exclude credentials, URLs, paths, identity and raw native
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

The server-owned browser login is covered by `internal/subscription/grok_oauth_test.go`
(profile pins, discovery, URL shape, exchange classification, ID-token negative
matrix, mapping) and `internal/server/grok_login_test.go` (a fake IdP with JWKS,
token and userinfo endpoints from `internal/subscription/grokidp`, which only
tests import): success, every refused callback shape, each failure without a
second token request or leaked value, uncertain exchange, cancellation before and
during the exchange, expiry, shutdown, restart, duplicate identity, forwarding and
closed-port probes. The server-owned refresh is covered by `internal/subscription/grok_refresh_test.go`
(request shape, rotation modes, identity and ID-token refusals, outcome
classification), `internal/server/grok_refresh_test.go` (the same fake IdP with
rotation, omission, invalid_grant, 5xx, truncation, identity change, crash-point
fault injection counting token requests, restart recovery, cleanup failure and a
log and database leak scan), `internal/domain/grok_subscription_test.go` (claim
validation), `internal/store/backup_restores_test.go` (restore) and
`internal/worker/grok_refresh_test.go` (native refresh confinement with a fake
native rewrite). The server-owned logout is covered by
`internal/subscription/grok_revoke_test.go` (request shape and outcome
classification against the fake provider's `/oauth2/revoke`),
`internal/server/grok_logout_test.go` (revocation outcomes, crash points,
cleanup failure, execution wait, serialization, deletion, batch cleanup, Worker
retry and the leak scan), `internal/domain/grok_subscription_test.go` (claim
validation) and `internal/store/backup_restores_test.go` (restore). The opt-in `TestManualNativeGrokLoginBundleIsAcceptedByNativeGrok`
writes the Go-produced `auth.json` into a scratch home and requires `grok models`
and ACP `cached_token` to accept it; it skips unless
`DELIDEV_NATIVE_GROK_EXECUTABLE` and
`DELIDEV_NATIVE_GROK_ACCOUNT_OWNER_APPROVAL=1` are set and needs the account
owner to approve in a browser. It has not been run, so the token-to-`auth.json`
mapping and the ephemeral-port redirect remain unverified.

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
