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
Worker-side confinement of a native refresh, and the Grok subscription
execution described under [Grok subscription execution](#grok-subscription-execution)
(execute lease, `cached_token` ACP profile, credential custody and recovery),
which is implemented but switched off: it is reachable only when the server
enables Grok subscription execution (`server.Config.GrokSubscriptionExecution`)
and the Worker enables Worker 21 (`worker.Config.ManagedGrokSubscriptions`), and
no product entry point sets either switch yet. System 39 covers both login
methods (decision D1: one capability for browser and device login together) and
is advertised only when the server enables Grok subscription login
(`server.Config.GrokSubscriptionLogin`, default off). The login implementation is
complete without that switch, but no client can start a Grok login yet, and a
client that sees the capability expects to reach the flow, so the switch stays
off until a product entry point (the desktop AI Subscription flow) can start
both methods; no entry point sets it yet. The advertisement claims no
real-account acceptance: the opt-in real-account run was not performed. Not
activated: the System 40 and Worker 21 advertisements (they wait for the
real-account native verification on Grok Build 1.0.52 or newer, which also
depends on the inspection fix of the version-baseline work), the native process
execution that produces a native refresh, and Worker-side watch dispatch of
lifecycle work.
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
`server.Config.GrokSubscriptionLogin` is set. Advertising it promises a usable
server-owned request path for a Grok account: `RequestSubscription` with the
machine omitted accepts a browser login (`device_code=false`) and a device login
(`device_code=true`) for a Grok service account, `GetSubscriptionProgress` presents
each (transient URL and user code, diagnostic, generation) and `CancelSubscription`
cancels it, pinned by `TestGrokLoginCapabilityGuaranteesTheServerOwnedRequestPathForBothMethods`;
the switch stays off by default until a product entry point sets it. The server
never lists System
40, and AttachWorker never lists Worker 21, unless the server enabled Grok
subscription execution. The Worker does not report 21 unless it was enabled and
the server echoed it, and a server that did not enable execution rejects 21 as
an unknown capability unless the Runner Device still owns an execute lease: disabling
new execution must not strand the cleanup of a lease already granted, so the
original Worker is answered with the echo of 21 on its first attach, before it reports it (a Worker reports 21 only after the server echoed it), and negotiates 21 for recovery only, and execute Takes stay refused. `GetSubscriptionProgress`
populates `grok_diagnostic` only for a Grok account's original operation: a
Worker-owned lifecycle operation (see [Grok diagnostics](#grok-diagnostics)), or
a failed or uncertain server-owned browser or device login (phase `LOGIN`; see
[Server-owned browser login](#server-owned-browser-login) and
[Server-owned device login](#server-owned-device-login)) or refresh (phase
`REFRESH`; see [Server-owned refresh](#server-owned-refresh)). It never does so
for a Codex or Claude operation. The owning feature change advertises and
activates the remaining capabilities:

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
[Server-owned device login](#server-owned-device-login). The desktop opens the
device verification page only through the closed native
`grok-device-authorization-open` action, which uses the system default browser
and never the General/Local link destinations. The destination is closed by
[the verification address rule](#verification-address-rule): the server, the
renderer predicate (`grokDeviceVerification`) and the native validator
(`validate_grok_device_authorization`) accept exactly the same addresses, so an
address the server publishes is always one the desktop can open. The browser
authorization predicate (`grokLoginURL`) and its native validator
(`validate_grok_subscription_authorization`) run their own shared table. Both
tables live in `apps/delidev/src/grok-url-validation-fixtures.json` and run in
the Go, TypeScript and Rust tests, so the layers cannot disagree.

### Capability advertisement table

Each capability has one switch and one default. A switch is set only by a product
entry point that can actually reach the feature; no entry point sets any of them
yet. Tests pin every row (`grok_reservations_test.go`, `grok_execution_test.go`).

| Capability                                                                 | Switch                                                              | Default                                                           | Advertised when                                                                                                                                         |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------- | ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| System 39 `GROK_SUBSCRIPTION_LOGIN_V1` (browser and device login together) | `server.Config.GrokSubscriptionLogin`                               | off                                                               | the switch is set                                                                                                                                       |
| System 40 `GROK_SUBSCRIPTION_EXECUTION_V1`                                 | `server.Config.GrokSubscriptionExecution`                           | off                                                               | the switch is set                                                                                                                                       |
| Worker 21 `MANAGED_GROK_SUBSCRIPTIONS_V1`                                  | `worker.Config.ManagedGrokSubscriptions`, and the server echoing it | off, rejected as unknown by a server without the execution switch | the Worker enabled it and the server enabled execution, or (recovery only) the Runner Device still owns an execute lease while the server switch is off |

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
  failed login records `grok_diagnostic` (phase `LOGIN`, a closed code, the shared
  locally reconstructed message and guidance without a detected version, the
  operation as correlation id) in the server operation;
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
  a `device_code` (printable ASCII 0x21 to 0x7e including quote and backslash, at most 2048 bytes,
  validated on its decoded value so every legal JSON escape of a printable byte
  such as `\/`, `\"`, `\\` or `\u0041` is accepted, kept as owned bytes and sent
  percent-encoded), a
  `user_code` matching `[A-Z0-9-]{4,32}` (for example `XXXX-XXXX`), a
  `verification_uri_complete` and/or `verification_uri` (each held to the
  [verification address rule](#verification-address-rule); the complete URI must
  carry a `user_code` equal to the user code, and its safe parameters are
  preserved), a positive `expires_in` (at most 24 hours) and an optional
  `interval` of 1 to 60 seconds (default 5). Member names are case-sensitive: a
  name that equals one of these members only when case is ignored is an alias
  and refuses the whole response, as in the
  [token endpoint response table](#token-endpoint-response-table). The complete URL is preferred. Any
  other response ends the login as failed (`Unavailable`) before anything is
  claimed. The real provider's scopes, `interval`, `expires_in` and error bodies
  were never captured: these values are RFC 8628 defaults and the browser
  profile's scopes, and are an **unverified assumption**.
- **Presentation.** The verification URL and user code are returned only through
  the transient `GetSubscriptionProgress` `url` and `user_code` fields while the
  operation is waiting. `validServerLoginProgress` is service-aware: for Grok it
  accepts only an address that satisfies the
  [verification address rule](#verification-address-rule) plus a user code of
  the existing `[A-Z0-9-]{4,32}` shape. The URL, user code and
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
  (15 minutes),
  and every poll is itself bounded by the time left before it (a response that would
  arrive later is never accepted and the poll settles as uncertain), measured on an injectable clock; the loop waits out a remainder
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

#### Verification address rule

The device verification address (`verification_uri_complete`, else
`verification_uri`) is validated as a plain string, not as a parsed URL, so no
normalization can make the layers disagree. The Go server
(`subscription.GrokVerificationURL`, which also guards the transient progress
response), the renderer (`grokDeviceVerification`) and the native host
(`validate_grok_device_authorization`) implement the same rule:

| Part        | Rule                                                                                                                                                                                                          |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Length      | At most 2048 bytes, all printable ASCII (0x21 to 0x7e)                                                                                                                                                        |
| Origin      | Exactly the prefix `https://accounts.x.ai`: no port, credentials, host case difference, trailing dot or backslash                                                                                             |
| Path        | One or more non-empty segments of `[A-Za-z0-9._~-]`, each introduced by a slash, none of them `.` or `..`, with an optional trailing slash; the page itself is the provider's choice and no escape is allowed |
| Fragment    | Never                                                                                                                                                                                                         |
| Query       | Optional and never empty: `key=value` pairs joined by `&`, each key `[A-Za-z0-9_.-]{1,64}` used once, each value at most 256 bytes of `[A-Za-z0-9._~-]` and well-formed percent escapes of printable ASCII    |
| `user_code` | When present, `[A-Z0-9-]{4,32}`; the server and the renderer also require it to equal the displayed code, and a complete verification URI must carry it                                                       |

The server publishes an address unchanged, only after it passes the rule, so the
renderer pins the first valid pair for the whole login. The native host does not
receive the displayed code, so it checks only the closed destination.
`TestGrokVerificationURLMatchesTheSharedDesktopTable` (Go), the shared-table
tests in `grok-subscription-login.test.tsx` and
`device_validator_matches_the_shared_url_table` (Rust) run every row of the
`deviceVerification` table, which states each layer's expected result.

#### Durable operation reads

Both login methods watch the durable operation while they wait. Every request
that can redeem the claimed credential (the browser code exchange and each
device poll, including the first one) is preceded by a durable reread
(`grokSendGate`) that runs after the exchange claim committed: the claim
transaction only proves the state at its own commit, and the watcher polls on a
timer, so a cancellation committed behind the claim must still stop the request.
The read has
three results, and only a successful read can prove a cancellation: the
operation was canceled, fenced, removed or replaced, or its actor vanished
(`stopped`). A read that fails for any other reason (the store cannot answer) is
`unreadable` and proves nothing. A step that ends because of an unreadable state
sends no further request. Which outcome it settles as depends only on whether
the exchange was already claimed (`grokInterruptedOutcome` is the one
classification):

| Durable read or cause of the end                       | Exchange claimed | Settles as                                                                        | Test                                                                           |
| ------------------------------------------------------ | ---------------- | --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| readable, operation continues                          | any              | the step continues                                                                | the device and browser completion tests                                        |
| readable, confirmed stop                               | no               | `CANCELED`, nothing claimed or sent                                               | `TestGrokDeviceLoginCancelAtEachPhaseHasADistinctOutcome`                      |
| readable, confirmed stop                               | yes              | `CANCELED` with the claim kept; settlement rereads the durable operation          | `TestGrokDeviceLoginCancelCommittedJustBeforeAPollSendsNoFurtherPoll`          |
| readable, confirmed stop right after the device claim  | yes              | `CANCELED` with the claim kept, the first poll is never sent                      | `TestGrokDeviceLoginCancelCommittedJustAfterTheClaimSendsNoPoll`               |
| readable, confirmed stop right after the browser claim | yes              | `CANCELED` with the claim kept, the code exchange is never sent                   | `TestGrokLoginCancelCommittedJustAfterTheClaimSendsNoExchange`                 |
| unreadable, seen right after the device claim          | yes              | `RECOVERY_REQUIRED`, the first poll is never sent, the claim is kept              | `TestGrokDeviceLoginUnreadableStateJustAfterTheClaimSendsNoPollAndFences`      |
| unreadable, seen by the pre-poll check                 | yes              | `RECOVERY_REQUIRED`, no further poll, the claim is kept and the code never resent | `TestGrokLoginUnreadableDurableStateIsNeverACancellation`                      |
| unreadable, seen by the watcher while waiting          | yes              | `RECOVERY_REQUIRED`, no further poll                                              | `TestGrokLoginUnreadableDurableStateIsNeverACancellation`                      |
| unreadable, seen by the watcher while waiting          | no               | `CANCELED`, nothing claimed or sent, no fence                                     | `TestGrokLoginUnreadableDurableStateIsNeverACancellation`                      |
| server shutdown (device login)                         | yes              | `RECOVERY_REQUIRED`                                                               | `TestGrokDeviceLoginShutdownBetweenPollsKeepsAClaimInRecovery`                 |
| server shutdown                                        | no               | `CANCELED`                                                                        | `TestGrokDeviceLoginShutdownBetweenPollsKeepsAClaimInRecovery`                 |
| operation window over, server alive                    | any              | `EXPIRED`                                                                         | `TestGrokDeviceLoginLocalDeadlineIsTheMinimumOfExpiresInAndTheOperationWindow` |
| request in flight when the context ends                | yes              | `RECOVERY_REQUIRED` (the request may have redeemed the credential)                | `TestGrokDeviceLoginCancelAtEachPhaseHasADistinctOutcome`                      |

A refresh sends its single request right after its claim and rereads nothing in
between, so it has no pre-send read; an interruption of that request is
uncertain (`RECOVERY_REQUIRED`).

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

The desktop browser flow is implemented in `apps/delidev/src/grok-subscription-login.tsx` and enabled only when the server advertises System capability 39. The explicit `Use a device code` action beside Add account (same capability, no separate device capability) sends `device_code` true once, displays the validated transient `accounts.x.ai` verification page and user code with Copy code and Open verification page (system default browser through the closed native action), and clears them on every terminal state; see the subscription Settings contract. The approved desktop flow preserves service order, existing marks, navigation
and theme. Add account starts browser login; Use a device code is explicit.
Waiting offers reopen/copy/open-page/cancel as appropriate: reopen is hidden after the `Incompatible` refusal, and nothing is offered once cancellation was acknowledged or observed (Copy link, Copy code, Copy address, Open verification page and the pinned code included) (see the [state machine](#desktop-browser-sign-in-state-machine)). Confirmed success
advances to account naming. Preparing, waiting, canceled, expired, failed and
recovery remain distinct. Exact original-request retry does not create a new
login. Polling, reconnection and Strict Mode grant no mutation authority.
The form retains a 1040px settings column, 720px login width, 40px controls,
8px corners, stacked narrow-screen actions, English/Korean text, keyboard access,
status announcements and name focus. Leaving clears display/timer/callback
authority without canceling an accepted server operation.

Device verification open lifecycle (renderer and native host must agree on
every cell). `grok-device-authorization-open` is stateless and binds no
listener, but every admitted dispatch owns a per-opening cancellation flag that
the native host registers under the same lock that Dispose, window close,
`stop` and revocation exhaustion hold. Admission therefore either refuses a
retired scope, a stale epoch or a stopped host, or registers a dispatch that the
following retirement cancels. The dispatcher checks that flag and starts the
browser process under one commit lock, and every retirement of a dispatch flag
(`browser_opener::retire`) stores under that same lock, so retirement and the
commit-to-spawn transition are mutually exclusive: a retirement either lands
first and refuses the dispatch, or waits until the process exists. A process
that was spawned and then retired is terminated while it runs, and a dispatch
whose retirement completed before the process finished never reports success
(`Stopped`); a launcher that already handed the page to the browser cannot be
recalled. The flag is never the host-wide stop flag, and an unrelated opening or
window is never cancelled. Every retirement sets the flags of the dispatches it
retires before it clears, removes or joins any retained callback listener, so a
slow listener join can never delay a retirement: a dispatch that is pending when
`stop`, revocation exhaustion, Dispose or window close begins is refused or
terminated even while the join is still waiting. `stop` cancels once before it
waits for the attempts lock and once more while holding it, so a dispatch that
was admitted just before the host stopped is retired too. The Rust tests cover
each retirement (`stop`, revocation exhaustion, Dispose, window close) against a
pending dispatch with a blocked listener join, and `stop` racing an admission.

| Retirement point                                | Process       | Dispatch result        |
| ----------------------------------------------- | ------------- | ---------------------- |
| before the commit                               | never started | `Stopped`              |
| while committing (blocked on the commit lock)   | spawned       | `Stopped`, terminated  |
| after the spawn, launcher running or exited     | terminated    | `Stopped`              |
| after a successful exit, before the final check | exited        | `Stopped`, not success |
| after the dispatch returned                     | exited        | unchanged (`Ok`)       |

Every flag that reaches a browser spawn is written only through
`browser_opener::retire`, whichever caller owns it. The writers and the test
that orders each with the commit-to-spawn transition (a writer started while a
dispatch holds the commit lock cannot change its flag until the process exists):

| Flag                                  | Production writers                                                          | Ordered by                                                                     |
| ------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| per-opening device cancel flag        | Dispose, window close, `stop`, revocation exhaustion                        | the device-opening retirement tests                                            |
| per-attempt callback stop flag        | dropping the attempt                                                        | the attempt retirement tests                                                   |
| `Connector::exiting` (external links) | `Connector::retire_exiting`: supervision stop request, owned-child shutdown | the shared fixture run against each writer; a source scan forbids a bare store |

An installed browser launch (ordinary links only, never OAuth authorization)
observes `Connector::exiting` before each spawn attempt and during its grace
period; a browser it already started is detached and never terminated.

The renderer side of this lifecycle (which phase offers Open verification page, when the opening owes a native Dispose, what a late result may change) is the `device-waiting`, `device-opened` and `device-address` rows of the [state machine](#desktop-browser-sign-in-state-machine) below, which is generated from the same table the controller runs: a device login owes no native Dispose until Open verification page is requested, a pending open is retired by Dispose on Back, unmount, abort, cancellation and every terminal state, and the verification page, the pinned code and the address are offered nowhere once a cancellation was acknowledged or observed. A healthy status read recovers only the polling notice; notices from explicit actions and one-shot native steps stay until the user acts again. An auxiliary open or reopen never consumes the retry closure of a replayable mutation (account creation, login request, cancellation, naming).

A connected Grok account in AI Subscription management (`subscription-accounts.tsx`)
follows System capability 39 alone for Refresh login and Log out (there is no
separate Grok refresh or logout capability, and capabilities 30 and 38 never
enable Grok). Without capability 39 both controls stay disabled behind the
Grok-specific `grok-subscription.lifecycle.unavailable` guidance; the generic
unsupported-service remediation is not used for Grok. An existing disconnected
Grok account offers no sign-in action.

Quota/reset credit/pricing, enterprise SSO, external identity providers, imported
user login, account switching, cross-Worker resume, Fork, Sidechat and manual
compaction are excluded.

### Desktop and server activation order

The desktop controller is a client of the server-owned Grok browser flow and
owns no OAuth authority itself. It creates a Grok service account, then calls
`RequestSubscription` with an omitted machine ID (the server-owned path),
polls `GetSubscriptionProgress` for the original authorization link and cancels
through `CancelSubscription`. It never sends a machine ID and never selects the
Worker-owned path for Grok. The server layers own the flow itself (the Grok
branch of the server-owned request, progress and cancellation) and the single
switch that advertises System capability 39; the desktop layers add no server
code and grant no capability.

The desktop layers therefore merge after the server layers. Merged alone, a
desktop revision sees a server without capability 39 and keeps every Grok
control in its disabled state, so no request that the server cannot serve is
ever sent.

| Server revision                         | Desktop revision            | Grok row                                                    |
| --------------------------------------- | --------------------------- | ----------------------------------------------------------- |
| Without the server-owned Grok flow      | with the Grok controller    | Coming soon (capability 39 not advertised)                  |
| With the flow, capability 39 switch off | with the Grok controller    | Coming soon (capability 39 not advertised)                  |
| With the flow, capability 39 switch on  | with the Grok controller    | Add account, Refresh login and Log out follow capability 39 |
| With the flow, capability 39 switch on  | without the Grok controller | Coming soon (the desktop never offers the row)              |

Turning the capability 39 switch on is a server-layer decision made after the
desktop controller exists; the desktop never infers it and never retries
against a server that does not advertise it.

### Desktop browser sign-in state machine

The state machine covers both sign-in methods (`browser` and `device code`); a method never switches mid-login and an event of the other method is ignored.

`apps/delidev/src/grok-login-machine.ts` is the single source of truth for the browser and device-code sign-in controller in `grok-subscription-login.tsx`. The controller never decides on its own what to show: every status read, native step and mutation outcome is one event applied to a transition table, and the resulting phase fixes the stage, the visible actions, the problem notice, the retained original request and the native binding. The tables below are generated from that table by `src/grok-login-machine.test.ts`, which also tests every phase and event pair and fails when this section is stale (regenerate with `UPDATE_GROK_LOGIN_DOCS=1 pnpm exec vitest run src/grok-login-machine.test.ts` from `apps/delidev`).

Rules the table enforces:

- A cancellation observed by a status read (the canceled flag while the server still reports preparing or waiting) is handled exactly like the acknowledged local cancellation: the native binding is disposed, the original link, device code and verification address are released and no Cancel, Open browser again, Open verification page, Copy link, Copy code, Copy address or Retry action remains. Late native results, callbacks and cancel outcomes are ignored.
- A retained original request (`start`, `cancel` or `name`) is explained only by its own notice (`requestUnconfirmed`, `cancelUnconfirmed`, `nameUnconfirmed`) and keeps it across every uncertain replay. A definite refusal drops the request and names the refusal (`requestFailed`, `cancelFailed`, or `nameUnconfirmed` with an editable name); `requestUnconfirmed` and `cancelUnconfirmed` therefore exist only together with a Retry original request button.
- Side observations never replace the notice of a retained request: an unreadable status sets `statusUnconfirmed` only when no other problem shows, a readable status clears only that notice, and native open, reopen, verification-open and callback failures are advisory while a request is retained. Open browser again, Open verification page and Cancel stay disabled while a request is retained, and a replay is the only way to settle it.
- A refused Take the server keeps queued (`unsupported` together with a Grok diagnostic, classified by `progressDisposition`) is provisional: `poll-refusal-provisional` leaves the phase, the retained request and the native binding unchanged and only recovers the polling notice, so Cancel stays available and the next response is adopted. A later `preparing` or `waiting` response clears the diagnostic notice, `succeeded` names the account and every other response settles. `unsupported` without a Grok diagnostic is final (`poll-unsupported`).
- A success, terminal state or observed cancellation drops any retained request. Success, every terminal state and cancellation dispose the native binding and clear the original link.
- Stage, help text and action visibility are derived from the phase alone; the controller keeps only the exact retained closure, the original link and the one-shot native flags.
- The controller's lifetime ends only through the triggers of the [controller lifetime table](#controller-lifetime): each one disposes the pending login and clears the view, so a screen never outlives the native control (and therefore the selected server) it was created under.

<!-- grok-login-machine:begin -->

#### Phases

| Phase                 | Method      | Stage         | Native binding                | Cancel | Open browser again | Copy link | Open verification page | Copy code | Copy address | Name field | Waiting guidance |
| --------------------- | ----------- | ------------- | ----------------------------- | ------ | ------------------ | --------- | ---------------------- | --------- | ------------ | ---------- | ---------------- |
| `desktop-required`    | both        | `unsupported` | `none`                        | -      | -                  | -         | -                      | -         | -            | `hidden`   | `stage`          |
| `starting`            | both        | `preparing`   | browser `owed`, device `none` | -      | -                  | -         | -                      | -         | -            | `hidden`   | `none`           |
| `preparing`           | both        | `preparing`   | browser `owed`, device `none` | yes    | -                  | -         | -                      | -         | -            | `hidden`   | `none`           |
| `awaiting-link`       | both        | `waiting`     | browser `owed`, device `none` | yes    | -                  | -         | -                      | -         | -            | `hidden`   | `none`           |
| `opening`             | browser     | `preparing`   | `owed`                        | yes    | -                  | yes       | -                      | -         | -            | `hidden`   | `none`           |
| `waiting`             | browser     | `waiting`     | `owed`                        | yes    | yes                | yes       | -                      | -         | -            | `hidden`   | `waiting`        |
| `waiting-no-browser`  | browser     | `waiting`     | `owed`                        | yes    | yes                | yes       | -                      | -         | -            | `hidden`   | `waitingLink`    |
| `waiting-link-only`   | browser     | `waiting`     | `owed`                        | yes    | -                  | yes       | -                      | -         | -            | `hidden`   | `waitingLink`    |
| `device-waiting`      | device code | `waiting`     | browser `owed`, device `none` | yes    | -                  | -         | yes                    | yes       | -            | `hidden`   | `deviceWaiting`  |
| `device-opened`       | device code | `waiting`     | `owed`                        | yes    | -                  | -         | yes                    | yes       | -            | `hidden`   | `deviceWaiting`  |
| `device-address`      | device code | `waiting`     | `owed`                        | yes    | -                  | -         | yes                    | yes       | yes          | `hidden`   | `deviceWaiting`  |
| `preparing-canceling` | both        | `preparing`   | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `none`           |
| `waiting-canceling`   | both        | `waiting`     | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `none`           |
| `naming`              | both        | `naming`      | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `editable` | `none`           |
| `canceled`            | both        | `canceled`    | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `stage`          |
| `expired`             | both        | `expired`     | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `stage`          |
| `unsupported`         | both        | `unsupported` | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `stage`          |
| `recovery`            | both        | `recovery`    | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `stage`          |
| `failed`              | both        | `failed`      | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `stage`          |
| `start-refused`       | both        | `failed`      | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `stage`          |
| `disposed`            | both        | -             | `disposed`                    | -      | -                  | -         | -                      | -         | -            | `hidden`   | `none`           |

Retry original request is shown exactly while a retry kind other than `none` is retained. While it is retained, Cancel, Open browser again and Open verification page are disabled and the naming field is locked (`locked` replaces `editable`). `owed` means a native Dispose is owed when the opening is left: a browser login owns its receiver for the whole opening, a device login only once Open verification page was requested. The device code and address are shown only in the phases that offer Copy code and Copy address and are cleared with them. A copy notice belongs to the value it copied (link, code or address): it is kept exactly while the current phase still offers that value and is cleared the moment it does not, even when the phase still offers another copy action.

#### Events

| Event                         | Method      | Trigger                                                                                                                            |
| ----------------------------- | ----------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `poll-preparing`              | both        | A status read reports preparing                                                                                                    |
| `poll-preparing-canceled`     | both        | A status read reports preparing with the canceled flag                                                                             |
| `poll-waiting`                | both        | A status read reports waiting                                                                                                      |
| `poll-waiting-canceled`       | both        | A status read reports waiting with the canceled flag (another client or a lost cancel response)                                    |
| `poll-canceled`               | both        | A status read reports the canceled state                                                                                           |
| `poll-expired`                | both        | A status read reports the expired state                                                                                            |
| `poll-unsupported`            | both        | A status read reports the unsupported state without a Grok diagnostic                                                              |
| `poll-refusal-provisional`    | both        | A status read reports the unsupported state together with a Grok diagnostic: a refused Take the server keeps queued                |
| `poll-recovery`               | both        | A status read reports recovery required                                                                                            |
| `poll-failed`                 | both        | A status read reports the failed state                                                                                             |
| `poll-succeeded`              | both        | A status read reports success and the fresh account verifies generation and operation                                              |
| `poll-error`                  | both        | A status read fails or its result cannot be verified                                                                               |
| `link-ready`                  | browser     | The original authorization link passes validation for the first time                                                               |
| `code-ready`                  | device code | The validated device verification address and user code pass validation for the first time                                         |
| `verification-open-started`   | device code | Open verification page is requested (the opener may be admitted before it finishes)                                                |
| `verification-open-succeeded` | device code | The native opener opened the verification page                                                                                     |
| `verification-open-failed`    | device code | The native opener failed to open the verification page                                                                             |
| `open-succeeded`              | browser     | The native receiver confirms the first open                                                                                        |
| `open-incompatible`           | browser     | The first open meets the Incompatible refusal                                                                                      |
| `open-failed`                 | browser     | The first open fails for any other reason                                                                                          |
| `reopen-succeeded`            | browser     | Open browser again succeeds                                                                                                        |
| `reopen-incompatible`         | browser     | Open browser again meets the Incompatible refusal                                                                                  |
| `reopen-failed`               | browser     | Open browser again fails                                                                                                           |
| `callback-take-failed`        | browser     | Taking the native callback fails (never retried)                                                                                   |
| `callback-forward-failed`     | browser     | Forwarding the taken callback fails (never retried)                                                                                |
| `replay-started`              | both        | A start, cancel or name save request (first run or Retry original request) begins                                                  |
| `auxiliary-started`           | browser     | Open browser again begins                                                                                                          |
| `start-accepted`              | both        | Account creation and RequestSubscription succeed                                                                                   |
| `start-refused`               | both        | Account creation or RequestSubscription is refused definitively, or its receipt fails the local account, alias or operation checks |
| `start-uncertain`             | both        | Account creation or RequestSubscription has an uncertain outcome                                                                   |
| `cancel-accepted`             | both        | CancelSubscription is acknowledged                                                                                                 |
| `cancel-refused`              | both        | CancelSubscription is refused definitively, or its receipt fails the local account checks                                          |
| `cancel-uncertain`            | both        | CancelSubscription has an uncertain outcome                                                                                        |
| `name-saved`                  | both        | The account alias save is verified                                                                                                 |
| `name-refused`                | both        | The account alias save is refused definitively, or its receipt fails the local alias or account checks                             |
| `name-uncertain`              | both        | The account alias save has an uncertain outcome                                                                                    |
| `dispose`                     | both        | The task is left, Settings closes or the controller unmounts                                                                       |

#### Transitions

Each table lists the events that change something in that phase. Every other event is ignored in that phase: the state, the retained request and the native binding stay unchanged (this covers late native results after a cancellation and any outcome that arrives after the login settled). The `opening` phase exists only inside one status-read tick, so a status read never meets it. `Native` shows the next phase's native binding; `Dispose issued` marks the transition that sends the native Dispose.

##### `desktop-required`

| Event     | Next phase | Problem | Retained request          | Native     |
| --------- | ---------- | ------- | ------------------------- | ---------- |
| `dispose` | `disposed` | clear   | drop the retained request | `disposed` |

##### `starting`

| Event             | Next phase      | Problem                  | Retained request                | Native                                  |
| ----------------- | --------------- | ------------------------ | ------------------------------- | --------------------------------------- |
| `replay-started`  | `starting`      | clear                    | drop the retained request       | browser `owed`, device `none`           |
| `start-accepted`  | `preparing`     | clear                    | drop the retained request       | browser `owed`, device `none`           |
| `start-refused`   | `start-refused` | set `requestFailed`      | drop the retained request       | `disposed` (Dispose issued for browser) |
| `start-uncertain` | `starting`      | set `requestUnconfirmed` | hold the original start request | browser `owed`, device `none`           |
| `dispose`         | `disposed`      | clear                    | drop the retained request       | `disposed` (Dispose issued for browser) |

##### `preparing`

| Event                      | Next phase            | Problem                                              | Retained request                 | Native                                  |
| -------------------------- | --------------------- | ---------------------------------------------------- | -------------------------------- | --------------------------------------- |
| `poll-preparing`           | `preparing`           | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-preparing-canceled`  | `preparing-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-waiting`             | `awaiting-link`       | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-waiting-canceled`    | `waiting-canceling`   | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-canceled`            | `canceled`            | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-expired`             | `expired`             | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-unsupported`         | `unsupported`         | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-refusal-provisional` | `preparing`           | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-recovery`            | `recovery`            | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-failed`              | `failed`              | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-succeeded`           | `naming`              | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-error`               | `preparing`           | set `statusUnconfirmed` unless another problem shows | keep                             | browser `owed`, device `none`           |
| `replay-started`           | `preparing`           | clear                                                | drop the retained request        | browser `owed`, device `none`           |
| `cancel-accepted`          | `preparing-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `cancel-refused`           | `preparing`           | set `cancelFailed`                                   | drop the retained request        | browser `owed`, device `none`           |
| `cancel-uncertain`         | `preparing`           | set `cancelUnconfirmed`                              | hold the original cancel request | browser `owed`, device `none`           |
| `dispose`                  | `disposed`            | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |

##### `awaiting-link`

| Event                      | Next phase          | Problem                                              | Retained request                 | Native                                  |
| -------------------------- | ------------------- | ---------------------------------------------------- | -------------------------------- | --------------------------------------- |
| `poll-preparing`           | `awaiting-link`     | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-preparing-canceled`  | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-waiting`             | `awaiting-link`     | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-waiting-canceled`    | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-canceled`            | `canceled`          | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-expired`             | `expired`           | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-unsupported`         | `unsupported`       | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-refusal-provisional` | `awaiting-link`     | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-recovery`            | `recovery`          | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-failed`              | `failed`            | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-succeeded`           | `naming`            | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-error`               | `awaiting-link`     | set `statusUnconfirmed` unless another problem shows | keep                             | browser `owed`, device `none`           |
| `link-ready`               | `opening`           | keep                                                 | keep                             | `owed`                                  |
| `code-ready`               | `device-waiting`    | keep                                                 | keep                             | `none`                                  |
| `replay-started`           | `awaiting-link`     | clear                                                | drop the retained request        | browser `owed`, device `none`           |
| `cancel-accepted`          | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `cancel-refused`           | `awaiting-link`     | set `cancelFailed`                                   | drop the retained request        | browser `owed`, device `none`           |
| `cancel-uncertain`         | `awaiting-link`     | set `cancelUnconfirmed`                              | hold the original cancel request | browser `owed`, device `none`           |
| `dispose`                  | `disposed`          | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |

##### `opening`

| Event               | Next phase            | Problem                                              | Retained request                 | Native                      |
| ------------------- | --------------------- | ---------------------------------------------------- | -------------------------------- | --------------------------- |
| `open-succeeded`    | `waiting`             | clear unless a request is retained                   | keep                             | `owed`                      |
| `open-incompatible` | `waiting-link-only`   | set `remoteUnsupported` unless a request is retained | keep                             | `owed`                      |
| `open-failed`       | `waiting-no-browser`  | set `browserFailed` unless a request is retained     | keep                             | `owed`                      |
| `replay-started`    | `opening`             | clear                                                | drop the retained request        | `owed`                      |
| `cancel-accepted`   | `preparing-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `cancel-refused`    | `opening`             | set `cancelFailed`                                   | drop the retained request        | `owed`                      |
| `cancel-uncertain`  | `opening`             | set `cancelUnconfirmed`                              | hold the original cancel request | `owed`                      |
| `dispose`           | `disposed`            | clear                                                | drop the retained request        | `disposed` (Dispose issued) |

##### `waiting`

| Event                      | Next phase          | Problem                                              | Retained request                 | Native                      |
| -------------------------- | ------------------- | ---------------------------------------------------- | -------------------------------- | --------------------------- |
| `poll-preparing`           | `waiting`           | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-preparing-canceled`  | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `poll-waiting`             | `waiting`           | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-waiting-canceled`    | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `poll-canceled`            | `canceled`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-expired`             | `expired`           | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-unsupported`         | `unsupported`       | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-refusal-provisional` | `waiting`           | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-recovery`            | `recovery`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-failed`              | `failed`            | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-succeeded`           | `naming`            | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-error`               | `waiting`           | set `statusUnconfirmed` unless another problem shows | keep                             | `owed`                      |
| `reopen-succeeded`         | `waiting`           | clear unless a request is retained                   | keep                             | `owed`                      |
| `reopen-incompatible`      | `waiting-link-only` | set `remoteUnsupported` unless a request is retained | keep                             | `owed`                      |
| `reopen-failed`            | `waiting`           | set `browserFailed` unless a request is retained     | keep                             | `owed`                      |
| `callback-take-failed`     | `waiting`           | set `callbackUncertain` unless a request is retained | keep                             | `owed`                      |
| `callback-forward-failed`  | `waiting`           | set `forwardUncertain` unless a request is retained  | keep                             | `owed`                      |
| `replay-started`           | `waiting`           | clear                                                | drop the retained request        | `owed`                      |
| `auxiliary-started`        | `waiting`           | clear unless a request is retained                   | keep                             | `owed`                      |
| `cancel-accepted`          | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `cancel-refused`           | `waiting`           | set `cancelFailed`                                   | drop the retained request        | `owed`                      |
| `cancel-uncertain`         | `waiting`           | set `cancelUnconfirmed`                              | hold the original cancel request | `owed`                      |
| `dispose`                  | `disposed`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |

##### `waiting-no-browser`

| Event                      | Next phase           | Problem                                              | Retained request                 | Native                      |
| -------------------------- | -------------------- | ---------------------------------------------------- | -------------------------------- | --------------------------- |
| `poll-preparing`           | `waiting-no-browser` | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-preparing-canceled`  | `waiting-canceling`  | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `poll-waiting`             | `waiting-no-browser` | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-waiting-canceled`    | `waiting-canceling`  | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `poll-canceled`            | `canceled`           | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-expired`             | `expired`            | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-unsupported`         | `unsupported`        | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-refusal-provisional` | `waiting-no-browser` | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-recovery`            | `recovery`           | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-failed`              | `failed`             | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-succeeded`           | `naming`             | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-error`               | `waiting-no-browser` | set `statusUnconfirmed` unless another problem shows | keep                             | `owed`                      |
| `reopen-succeeded`         | `waiting`            | clear unless a request is retained                   | keep                             | `owed`                      |
| `reopen-incompatible`      | `waiting-link-only`  | set `remoteUnsupported` unless a request is retained | keep                             | `owed`                      |
| `reopen-failed`            | `waiting-no-browser` | set `browserFailed` unless a request is retained     | keep                             | `owed`                      |
| `replay-started`           | `waiting-no-browser` | clear                                                | drop the retained request        | `owed`                      |
| `auxiliary-started`        | `waiting-no-browser` | clear unless a request is retained                   | keep                             | `owed`                      |
| `cancel-accepted`          | `waiting-canceling`  | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `cancel-refused`           | `waiting-no-browser` | set `cancelFailed`                                   | drop the retained request        | `owed`                      |
| `cancel-uncertain`         | `waiting-no-browser` | set `cancelUnconfirmed`                              | hold the original cancel request | `owed`                      |
| `dispose`                  | `disposed`           | clear                                                | drop the retained request        | `disposed` (Dispose issued) |

##### `waiting-link-only`

| Event                      | Next phase          | Problem                                              | Retained request                 | Native                      |
| -------------------------- | ------------------- | ---------------------------------------------------- | -------------------------------- | --------------------------- |
| `poll-preparing`           | `waiting-link-only` | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-preparing-canceled`  | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `poll-waiting`             | `waiting-link-only` | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-waiting-canceled`    | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `poll-canceled`            | `canceled`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-expired`             | `expired`           | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-unsupported`         | `unsupported`       | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-refusal-provisional` | `waiting-link-only` | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-recovery`            | `recovery`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-failed`              | `failed`            | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-succeeded`           | `naming`            | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-error`               | `waiting-link-only` | set `statusUnconfirmed` unless another problem shows | keep                             | `owed`                      |
| `replay-started`           | `waiting-link-only` | clear                                                | drop the retained request        | `owed`                      |
| `cancel-accepted`          | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `cancel-refused`           | `waiting-link-only` | set `cancelFailed`                                   | drop the retained request        | `owed`                      |
| `cancel-uncertain`         | `waiting-link-only` | set `cancelUnconfirmed`                              | hold the original cancel request | `owed`                      |
| `dispose`                  | `disposed`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |

##### `device-waiting`

| Event                       | Next phase          | Problem                                              | Retained request                 | Native                                  |
| --------------------------- | ------------------- | ---------------------------------------------------- | -------------------------------- | --------------------------------------- |
| `poll-preparing`            | `device-waiting`    | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-preparing-canceled`   | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-waiting`              | `device-waiting`    | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-waiting-canceled`     | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-canceled`             | `canceled`          | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-expired`              | `expired`           | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-unsupported`          | `unsupported`       | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-refusal-provisional`  | `device-waiting`    | clear only `statusUnconfirmed`                       | keep                             | browser `owed`, device `none`           |
| `poll-recovery`             | `recovery`          | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-failed`               | `failed`            | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-succeeded`            | `naming`            | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |
| `poll-error`                | `device-waiting`    | set `statusUnconfirmed` unless another problem shows | keep                             | browser `owed`, device `none`           |
| `verification-open-started` | `device-opened`     | clear unless a request is retained                   | keep                             | `owed`                                  |
| `replay-started`            | `device-waiting`    | clear                                                | drop the retained request        | browser `owed`, device `none`           |
| `cancel-accepted`           | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued for browser) |
| `cancel-refused`            | `device-waiting`    | set `cancelFailed`                                   | drop the retained request        | browser `owed`, device `none`           |
| `cancel-uncertain`          | `device-waiting`    | set `cancelUnconfirmed`                              | hold the original cancel request | browser `owed`, device `none`           |
| `dispose`                   | `disposed`          | clear                                                | drop the retained request        | `disposed` (Dispose issued for browser) |

##### `device-opened`

| Event                         | Next phase          | Problem                                                   | Retained request                 | Native                      |
| ----------------------------- | ------------------- | --------------------------------------------------------- | -------------------------------- | --------------------------- |
| `poll-preparing`              | `device-opened`     | clear only `statusUnconfirmed`                            | keep                             | `owed`                      |
| `poll-preparing-canceled`     | `waiting-canceling` | set `canceling`                                           | drop the retained request        | `disposed` (Dispose issued) |
| `poll-waiting`                | `device-opened`     | clear only `statusUnconfirmed`                            | keep                             | `owed`                      |
| `poll-waiting-canceled`       | `waiting-canceling` | set `canceling`                                           | drop the retained request        | `disposed` (Dispose issued) |
| `poll-canceled`               | `canceled`          | clear                                                     | drop the retained request        | `disposed` (Dispose issued) |
| `poll-expired`                | `expired`           | clear                                                     | drop the retained request        | `disposed` (Dispose issued) |
| `poll-unsupported`            | `unsupported`       | clear                                                     | drop the retained request        | `disposed` (Dispose issued) |
| `poll-refusal-provisional`    | `device-opened`     | clear only `statusUnconfirmed`                            | keep                             | `owed`                      |
| `poll-recovery`               | `recovery`          | clear                                                     | drop the retained request        | `disposed` (Dispose issued) |
| `poll-failed`                 | `failed`            | clear                                                     | drop the retained request        | `disposed` (Dispose issued) |
| `poll-succeeded`              | `naming`            | clear                                                     | drop the retained request        | `disposed` (Dispose issued) |
| `poll-error`                  | `device-opened`     | set `statusUnconfirmed` unless another problem shows      | keep                             | `owed`                      |
| `verification-open-started`   | `device-opened`     | clear unless a request is retained                        | keep                             | `owed`                      |
| `verification-open-succeeded` | `device-opened`     | clear unless a request is retained                        | keep                             | `owed`                      |
| `verification-open-failed`    | `device-address`    | set `verificationOpenFailed` unless a request is retained | keep                             | `owed`                      |
| `replay-started`              | `device-opened`     | clear                                                     | drop the retained request        | `owed`                      |
| `cancel-accepted`             | `waiting-canceling` | set `canceling`                                           | drop the retained request        | `disposed` (Dispose issued) |
| `cancel-refused`              | `device-opened`     | set `cancelFailed`                                        | drop the retained request        | `owed`                      |
| `cancel-uncertain`            | `device-opened`     | set `cancelUnconfirmed`                                   | hold the original cancel request | `owed`                      |
| `dispose`                     | `disposed`          | clear                                                     | drop the retained request        | `disposed` (Dispose issued) |

##### `device-address`

| Event                       | Next phase          | Problem                                              | Retained request                 | Native                      |
| --------------------------- | ------------------- | ---------------------------------------------------- | -------------------------------- | --------------------------- |
| `poll-preparing`            | `device-address`    | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-preparing-canceled`   | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `poll-waiting`              | `device-address`    | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-waiting-canceled`     | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `poll-canceled`             | `canceled`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-expired`              | `expired`           | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-unsupported`          | `unsupported`       | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-refusal-provisional`  | `device-address`    | clear only `statusUnconfirmed`                       | keep                             | `owed`                      |
| `poll-recovery`             | `recovery`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-failed`               | `failed`            | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-succeeded`            | `naming`            | clear                                                | drop the retained request        | `disposed` (Dispose issued) |
| `poll-error`                | `device-address`    | set `statusUnconfirmed` unless another problem shows | keep                             | `owed`                      |
| `verification-open-started` | `device-opened`     | clear unless a request is retained                   | keep                             | `owed`                      |
| `replay-started`            | `device-address`    | clear                                                | drop the retained request        | `owed`                      |
| `cancel-accepted`           | `waiting-canceling` | set `canceling`                                      | drop the retained request        | `disposed` (Dispose issued) |
| `cancel-refused`            | `device-address`    | set `cancelFailed`                                   | drop the retained request        | `owed`                      |
| `cancel-uncertain`          | `device-address`    | set `cancelUnconfirmed`                              | hold the original cancel request | `owed`                      |
| `dispose`                   | `disposed`          | clear                                                | drop the retained request        | `disposed` (Dispose issued) |

##### `preparing-canceling`

| Event                      | Next phase            | Problem                                              | Retained request          | Native     |
| -------------------------- | --------------------- | ---------------------------------------------------- | ------------------------- | ---------- |
| `poll-preparing`           | `preparing-canceling` | clear only `statusUnconfirmed`                       | keep                      | `disposed` |
| `poll-preparing-canceled`  | `preparing-canceling` | set `canceling`                                      | drop the retained request | `disposed` |
| `poll-waiting`             | `waiting-canceling`   | clear only `statusUnconfirmed`                       | keep                      | `disposed` |
| `poll-waiting-canceled`    | `waiting-canceling`   | set `canceling`                                      | drop the retained request | `disposed` |
| `poll-canceled`            | `canceled`            | clear                                                | drop the retained request | `disposed` |
| `poll-expired`             | `expired`             | clear                                                | drop the retained request | `disposed` |
| `poll-unsupported`         | `unsupported`         | clear                                                | drop the retained request | `disposed` |
| `poll-refusal-provisional` | `preparing-canceling` | clear only `statusUnconfirmed`                       | keep                      | `disposed` |
| `poll-recovery`            | `recovery`            | clear                                                | drop the retained request | `disposed` |
| `poll-failed`              | `failed`              | clear                                                | drop the retained request | `disposed` |
| `poll-succeeded`           | `naming`              | clear                                                | drop the retained request | `disposed` |
| `poll-error`               | `preparing-canceling` | set `statusUnconfirmed` unless another problem shows | keep                      | `disposed` |
| `cancel-accepted`          | `preparing-canceling` | set `canceling`                                      | drop the retained request | `disposed` |
| `dispose`                  | `disposed`            | clear                                                | drop the retained request | `disposed` |

##### `waiting-canceling`

| Event                      | Next phase          | Problem                                              | Retained request          | Native     |
| -------------------------- | ------------------- | ---------------------------------------------------- | ------------------------- | ---------- |
| `poll-preparing`           | `waiting-canceling` | clear only `statusUnconfirmed`                       | keep                      | `disposed` |
| `poll-preparing-canceled`  | `waiting-canceling` | set `canceling`                                      | drop the retained request | `disposed` |
| `poll-waiting`             | `waiting-canceling` | clear only `statusUnconfirmed`                       | keep                      | `disposed` |
| `poll-waiting-canceled`    | `waiting-canceling` | set `canceling`                                      | drop the retained request | `disposed` |
| `poll-canceled`            | `canceled`          | clear                                                | drop the retained request | `disposed` |
| `poll-expired`             | `expired`           | clear                                                | drop the retained request | `disposed` |
| `poll-unsupported`         | `unsupported`       | clear                                                | drop the retained request | `disposed` |
| `poll-refusal-provisional` | `waiting-canceling` | clear only `statusUnconfirmed`                       | keep                      | `disposed` |
| `poll-recovery`            | `recovery`          | clear                                                | drop the retained request | `disposed` |
| `poll-failed`              | `failed`            | clear                                                | drop the retained request | `disposed` |
| `poll-succeeded`           | `naming`            | clear                                                | drop the retained request | `disposed` |
| `poll-error`               | `waiting-canceling` | set `statusUnconfirmed` unless another problem shows | keep                      | `disposed` |
| `cancel-accepted`          | `waiting-canceling` | set `canceling`                                      | drop the retained request | `disposed` |
| `dispose`                  | `disposed`          | clear                                                | drop the retained request | `disposed` |

##### `naming`

| Event            | Next phase | Problem               | Retained request               | Native     |
| ---------------- | ---------- | --------------------- | ------------------------------ | ---------- |
| `replay-started` | `naming`   | clear                 | drop the retained request      | `disposed` |
| `name-saved`     | `disposed` | clear                 | drop the retained request      | `disposed` |
| `name-refused`   | `naming`   | set `nameUnconfirmed` | drop the retained request      | `disposed` |
| `name-uncertain` | `naming`   | set `nameUnconfirmed` | hold the original name request | `disposed` |
| `dispose`        | `disposed` | clear                 | drop the retained request      | `disposed` |

##### `canceled`

| Event     | Next phase | Problem | Retained request          | Native     |
| --------- | ---------- | ------- | ------------------------- | ---------- |
| `dispose` | `disposed` | clear   | drop the retained request | `disposed` |

##### `expired`

| Event     | Next phase | Problem | Retained request          | Native     |
| --------- | ---------- | ------- | ------------------------- | ---------- |
| `dispose` | `disposed` | clear   | drop the retained request | `disposed` |

##### `unsupported`

| Event     | Next phase | Problem | Retained request          | Native     |
| --------- | ---------- | ------- | ------------------------- | ---------- |
| `dispose` | `disposed` | clear   | drop the retained request | `disposed` |

##### `recovery`

| Event     | Next phase | Problem | Retained request          | Native     |
| --------- | ---------- | ------- | ------------------------- | ---------- |
| `dispose` | `disposed` | clear   | drop the retained request | `disposed` |

##### `failed`

| Event     | Next phase | Problem | Retained request          | Native     |
| --------- | ---------- | ------- | ------------------------- | ---------- |
| `dispose` | `disposed` | clear   | drop the retained request | `disposed` |

##### `start-refused`

| Event     | Next phase | Problem | Retained request          | Native     |
| --------- | ---------- | ------- | ------------------------- | ---------- |
| `dispose` | `disposed` | clear   | drop the retained request | `disposed` |

##### `disposed`

No event changes this phase.

#### Problem notices

| Notice                   | Set by                                                                | Retry button                  |
| ------------------------ | --------------------------------------------------------------------- | ----------------------------- |
| `browserFailed`          | `open-failed`, `reopen-failed`                                        | never                         |
| `callbackUncertain`      | `callback-take-failed`                                                | never                         |
| `cancelFailed`           | `cancel-refused`                                                      | never                         |
| `cancelUnconfirmed`      | `cancel-uncertain`                                                    | only after `cancel-uncertain` |
| `canceling`              | `poll-preparing-canceled`, `poll-waiting-canceled`, `cancel-accepted` | never                         |
| `desktopRequired`        | `start without the native control`                                    | never                         |
| `forwardUncertain`       | `callback-forward-failed`                                             | never                         |
| `nameUnconfirmed`        | `name-refused`, `name-uncertain`                                      | only after `name-uncertain`   |
| `remoteUnsupported`      | `open-incompatible`, `reopen-incompatible`                            | never                         |
| `requestFailed`          | `start-refused`                                                       | never                         |
| `requestUnconfirmed`     | `start-uncertain`                                                     | only after `start-uncertain`  |
| `statusUnconfirmed`      | `poll-error`                                                          | never                         |
| `verificationOpenFailed` | `verification-open-failed`                                            | never                         |

#### Copy attempts

Copy link, Copy code and Copy address start an attempt with a new identity, and only the latest attempt may show a notice. A completion or notice timer names the attempt it belongs to; one that names an earlier attempt is ignored. An attempt ends (`retired`) the moment the current phase stops offering its value or the task is disposed, so a value that was hidden and offered again never receives the completion of the earlier attempt, and two writes that settle out of order show only the latest. Starting an attempt removes the previous notice. Events not listed for a copy state leave it unchanged.

| Copy state | Event            | Attempt named | Next copy state |
| ---------- | ---------------- | ------------- | --------------- |
| `idle`     | `started`        | unnamed       | `pending`       |
| `idle`     | `succeeded`      | latest        | `idle`          |
| `idle`     | `succeeded`      | superseded    | `idle`          |
| `idle`     | `rejected`       | latest        | `idle`          |
| `idle`     | `rejected`       | superseded    | `idle`          |
| `idle`     | `retired`        | unnamed       | `idle`          |
| `idle`     | `notice-expired` | latest        | `idle`          |
| `idle`     | `notice-expired` | superseded    | `idle`          |
| `pending`  | `started`        | unnamed       | `pending`       |
| `pending`  | `succeeded`      | latest        | `copied`        |
| `pending`  | `succeeded`      | superseded    | `pending`       |
| `pending`  | `rejected`       | latest        | `failed`        |
| `pending`  | `rejected`       | superseded    | `pending`       |
| `pending`  | `retired`        | unnamed       | `idle`          |
| `pending`  | `notice-expired` | latest        | `pending`       |
| `pending`  | `notice-expired` | superseded    | `pending`       |
| `copied`   | `started`        | unnamed       | `pending`       |
| `copied`   | `succeeded`      | latest        | `copied`        |
| `copied`   | `succeeded`      | superseded    | `copied`        |
| `copied`   | `rejected`       | latest        | `copied`        |
| `copied`   | `rejected`       | superseded    | `copied`        |
| `copied`   | `retired`        | unnamed       | `idle`          |
| `copied`   | `notice-expired` | latest        | `idle`          |
| `copied`   | `notice-expired` | superseded    | `copied`        |
| `failed`   | `started`        | unnamed       | `pending`       |
| `failed`   | `succeeded`      | latest        | `failed`        |
| `failed`   | `succeeded`      | superseded    | `failed`        |
| `failed`   | `rejected`       | latest        | `failed`        |
| `failed`   | `rejected`       | superseded    | `failed`        |
| `failed`   | `retired`        | unnamed       | `idle`          |
| `failed`   | `notice-expired` | latest        | `idle`          |
| `failed`   | `notice-expired` | superseded    | `failed`        |

#### Controller lifetime

Every trigger disposes the pending login (display, timer and callback authority only, never an accepted server operation), releases it and clears the view, so no screen, paused inventory polling or inert action outlives the authority it was created under. A login is never carried to another server: the next Add account starts a new one under the new native control.

| Trigger             | When                                                                                                                | Pending login         | View    | Parent notified |
| ------------------- | ------------------------------------------------------------------------------------------------------------------- | --------------------- | ------- | --------------- |
| `leave`             | The user leaves the task (Back or a saved name)                                                                     | disposed and released | cleared | yes             |
| `inactive`          | The Settings category stops being active                                                                            | disposed and released | cleared | -               |
| `settings-closed`   | The Settings opening that owns the controller is aborted                                                            | disposed and released | cleared | -               |
| `authority-changed` | The native control the login was created under is replaced (another server is selected), or the controller unmounts | disposed and released | cleared | -               |

<!-- grok-login-machine:end -->

### Project requirements

- DeliDev Grok Build subscription support follows `cmds-delidev-grok-subscription-contract.md`. System 39 (server-owned browser and device login) is advertised only behind `server.Config.GrokSubscriptionLogin`; System 40 and Worker 21 are declared but unadvertised until the owning feature activates them (40 and 21 are switch-gated, see [Grok subscription execution](#grok-subscription-execution)); the server-owned browser login, device login and refresh, the switched-off subscription execution and progress field 10 (failed login or refresh only) are implemented. Declarations grant no native execution or migration. Preserve existing capability ownership, server-owned OAuth/protected credentials, original account leases and same-account/Worker history; verify the subscription profile at or above `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) by native validation before activation.

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
left untouched. An execute Take is refused with `Unsupported` unless the
server enabled Grok subscription execution (see
[Grok subscription execution](#grok-subscription-execution)); a member or owner
Finish scope and any other action are always refused. Grok lifecycle work
(login, refresh, logout) is never pushed to Workers through the subscription
watch; the watch delivers only a fenced execute lease to the Worker that owns it.

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

Native login execution and Worker-side watch dispatch of lifecycle work for Grok
are separate lifecycle work and are not activated by this storage contract. The
Worker half of a native refresh (custody, verification and cleanup of the latest
`auth.json`) is implemented and tested under
[Native refresh confinement](#native-refresh-confinement); the process execution
that produces one is the execution lease under
[Grok subscription execution](#grok-subscription-execution).

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
- **Claim before send.** The stored generation is read with the vault opened under the account gate
  (concurrent runners never race to open the shared vault), opened against this server and
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
  refresh (`recoverGrokRefresh`; the health the refresh was admitted under, recorded as
  `health_before`, returns: an expired account stays expired), dispatched by the same
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
shared predicate, derived from the retained logout operation and its pending
ownership, so it holds until the logout settles whatever the health became while
it waited or was fenced for recovery) while the logout waits, revokes and cleans up. It follows the
same lease rules as login and refresh: it cannot overlap another operation, an
observation or a recovery fence, `CancelSubscription` stays login-only, and a
separate account is never blocked. A request that carries a `machine_id` is never a
Grok logout: it enters the Codex gate, which refuses a Grok account before any
mutation (`TestGrokMachineScopedRequestsAreRefusedBeforeAnyMutation`), so no
Worker-routed Grok operation can be created by a request. The Worker logout lease
guards below protect only operations seeded directly in storage. The Go flow lives in
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
- **Outcomes.** Only HTTP 200 is revoked: RFC 7009 answers 200 both for a revoked
  token and for an invalid submitted one, so an error response is a refused request
  and proves nothing about the token. Every 4xx, including a 400 or 401 whose body
  is `invalid_grant` or `invalid_token`, is a definite refusal (the token may still
  work elsewhere, so it is never counted as revoked); 5xx, a transport failure, a
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
  confirmed retry releases the lease and removes every generation. The Finish
  completes a decision that is already irreversible (the provider revoked the
  token and the Worker home is clean), and the vault removal runs before its
  settlement transaction, so that transaction never rechecks the initiating
  client: a client revoked after Take cannot make the settlement fail after the
  bundle was removed. Only the lease's own Worker, instance and epoch bind it. A
  different Worker or instance is refused and the fence stays. Fenced leases of other
  actions are never released by a retry.

  The Worker's cleanup confirmation is recorded durably on the lease
  (`cleanup_confirmed`) before the first vault byte is deleted. From that
  checkpoint on the logout needs no Worker: the settlement transaction runs under
  the server's own principal and checks only the immutable lease binding, never
  the Worker device's authorization or its current instance, so a Worker revoked
  or replaced between the vault removal and the commit cannot leave a fenced
  account that references a deleted generation. A settlement that still does not
  commit (a crash, a store failure) leaves the checkpoint, and the scanner
  (`recoverGrokWorkerLogout`) removes every generation, releases the lease and
  disconnects the account, but only when the exact Worker can no longer report
  (device revoked or instance replaced); while that Worker can report, its own
  Finish retry settles it and the scanner stays out.

  | Checkpoint | Exact Worker can report | Revoked generation proven | Settled by                                                                  | Test                                                                         |
  | ---------- | ----------------------- | ------------------------- | --------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
  | absent     | any                     | any                       | only the exact Worker's Finish (nothing was deleted yet)                    | `TestGrokWorkerLogoutScannerFinishesOnlyACheckpointedLogoutOfALostWorker`    |
  | durable    | yes                     | yes                       | the Worker's Finish retry; the scanner does not compete                     | `TestGrokWorkerLogoutScannerFinishesOnlyACheckpointedLogoutOfALostWorker`    |
  | durable    | no                      | yes                       | the scanner: every generation removed, lease released, account disconnected | `TestGrokWorkerLogoutScannerFinishesOnlyACheckpointedLogoutOfALostWorker`    |
  | durable    | no, lost during Finish  | yes                       | the same Finish commit, under the durable lease binding                     | `TestGrokWorkerLogoutSettlesAfterTheVaultRemovalWhateverHappenedToTheWorker` |
  | any        | any                     | no                        | nobody: the bundle is never deleted without a proven revocation             | `TestGrokWorkerLogoutScannerFinishesOnlyACheckpointedLogoutOfALostWorker`    |

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

### Grok subscription execution

A Grok subscription session runs with a managed account on the Worker that was
selected for it. Everything here is implemented but unadvertised: it is reachable
only when `server.Config.GrokSubscriptionExecution` and
`worker.Config.ManagedGrokSubscriptions` are both set. The server switch adds
System 40 to GetStatus and admits and echoes Worker 21 in AttachWorker; the
Worker switch makes the Worker report 21 only after the server echoed it.
Without the switches System 40 and Worker 21 are absent, the server rejects 21 as
an unknown capability, an execute Take is refused with `Unsupported` and a Grok
subscription job fails with `Unsupported` before any lease. The switches stay
off until the native subscription profile is verified with a real account on the
current stable release (at least Grok Build 1.0.52), which this change did not
perform. API execution (`xai.api_key`, the scoped DeliDev key and the loopback
blocks) is unchanged.

- **Configuration.** `domain.ExecutionConfiguration` accepts a subscription
  configuration whose harness is Grok Build and whose service is `grok`
  (`Unsupported` for any other pairing). The Runner Device must carry
  `domain.ManagedGrokSubscriptionsV1` and one verified installation at or above
  `domain.GrokSubscriptionBaselineVersion` (`1.0.46`).
- **Lease.** `TakeSubscription` with action `EXECUTE` and the claimed
  execute-session job as operation (the job revision is the expected revision)
  creates the account's exclusive singleton lease with the job as operation. The
  account must be connected, enabled and ready with a current generation and no
  queued lifecycle operation (a queued logout blocks new execution). A held lease
  answers `ResourceExhausted` first, so a second session of the same account
  waits (the Worker retries with backoff) and no login, refresh or logout can
  start while an execute lease is active; separate accounts hold separate leases
  and run in parallel. There is no shared-owner or member scope for Grok. The
  response carries the stored envelope re-opened against this server, the
  account and the generation, exactly like a refresh, and the verified
  installation observation the Take admitted. That observation is the only one the
  lease may launch: the Worker decodes it, requires a Grok Build installation at or
  above the subscription baseline with an absolute path, proves the path is its
  own canonical identity, and runs that executable and version instead of the
  job's frozen observation, which discovery may have replaced since dispatch. An
  unusable observation releases the lease with confirmed cleanup before any
  credential is written.
- **Credential custody.** The Worker binds the fresh private home
  (`<profile>/grok`, outside the workspace) before the Take, keeps the envelope
  in memory, and materializes `$GROK_HOME/auth.json` with mode 0600 through the
  bound handle only when the harness asks for it: after every native inspection
  of the empty profile and immediately before the native process starts. The
  home's stable identity must be non-empty: a home that is unsafe, replaced or
  cannot be identified is refused before the Take, so no lease is requested and no
  credential crosses the Worker boundary. The durable claim written before the
  Take records the home path and identity; the journal records `execution_started`
  before the file is written, after the owner's process index was retained.
  Registration of the execution still happens, advertises no API proxy path and
  grants no relay authority.
- **Reflection guard.** The access and refresh values of the materialized credential
  are handed to the native frame guard and the text holdback exactly like an API
  token (`PrepareCredential` returns them), so a frame that reflects them, exactly or
  base64 encoded, is refused before it reaches events or retained output.
- **ACP profile.** Subscription mode replaces only the credential source. The
  private `config.toml` keeps the selected-model pinning but carries no
  `base_url`, `env_key` or `api_backend`; the launched environment drops exactly
  `GROK_XAI_API_BASE_URL`, `GROK_MODELS_BASE_URL` and `GROK_MODELS_LIST_URL` and
  keeps HOME, USERPROFILE, XDG_*, APPDATA, LOCALAPPDATA, TMPDIR, GROK_HOME,
  `GROK_DISABLE_AUTOUPDATER` and the `GROK_{CLAUDE,CURSOR}_{SKILLS,RULES,AGENTS,MCPS,HOOKS}_ENABLED=0`
  set (every inspection keeps the loopback blocks). `initialize` must advertise
  `cached_token` then `grok.com` with a default of `cached_token` or none, and the
  adapter calls `authenticate` with `methodId` `cached_token` (the same
  `_meta.headless` object). A refused handshake, a native exit during it or a
  transport failure is a typed `Unavailable` with no native text; only the
  caller's cancellation keeps its own class. The real session, model and result
  shapes of Grok Build 1.0.52 with a cached token were **not observed**: the
  profile reuses the pinned single selected-model configuration and is an
  unverified assumption until the real-account run records it.
- **Exit.** On every exit path (success, original Stop, crash, timeout, server
  loss) the deferred step joins the native owner through its process index, which
  the credential callback retained (a private directory) before the journal says
  the execution started and before any credential exists. A started execution
  whose index is missing is never joined: absence is not completion proof, as the
  directory may have been removed while the child is alive, so cleanup stays
  unconfirmed and the lease fenced; an execution that never started has nothing to
  join. The step reads the credential once,
  retires `auth.json`, `auth.json.lock` and attributed temporaries through the
  bound home, and only then reports Finish: a joined process with the original
  identity returns the sealed credential, an unjoined process, a foreign
  identity, an unreadable file or an unconfirmed removal never confirms cleanup
  or returns a bundle. An execution that failed before the harness asked for the
  credential wrote no file itself, but its bound home may still hold one (it
  existed before the Take or appeared since, which the profile probe then
  refuses), so cleanup is confirmed only after the home is proven
  credential-free (`confirmGrokHomeCredentialFree`): the credential entries are
  retired through the bound home, none remains and the path still names the held
  directory. The home is also made credential-free before the Take; a leftover
  that cannot be proven retired is refused before any lease exists. A Finish whose response
  is lost (a timeout or a dropped connection) leaves the lease possibly held, so
  it is reported as managed execution uncertainty, not as an ordinary failed job:
  the primary lane closes and ownership is reconciled, including for a failure
  right after the Take.
- **Finish.** The server settles an execute Finish with `Succeeded` and
  confirmed cleanup: an unchanged credential keeps the current generation and
  stages nothing; a rotated credential must keep the bound identity and publish a
  later expiry with a changed key or refresh token (`GrokRefreshed`) and becomes
  the next generation, the superseded one being removed after the commit; a
  foreign or regressed credential is refused and fences the lease. Confirmed
  cleanup without a usable credential (a crash, an unreadable file) releases the
  lease under the current generation without a fence and without redistributing
  anything. Unconfirmed cleanup fences the lease like every other action. An
  execute Finish never changes the account's owner machine or identity
  commitment.
- **Recovery on the original Worker.** A restart, a lost Worker instance or a
  replayed Take fences the lease (`RecoveryRequired`). The watch then delivers
  that account only to a Worker that negotiated 21 and owns the lease (the same
  Runner Device and the same device credential: a second valid credential of the
  machine is never sent the record, because its recovery Finish would be refused); the Worker
  validates its journal, joins the owned process, retires the credential left in
  the recorded home and reports a Finish with confirmed cleanup, no success and
  no credential from the original instance. The server accepts that Finish across
  the fence only for an execute lease, the exact original Runner Device, device
  and instance, and releases the lease under the current generation and restores
  health; a refresh, login or logout lease, a different instance, a successful
  or credential-bearing report and unconfirmed cleanup stay refused. Recovery
  never retakes the lease, starts a native process or replays input. The Worker
  journals the identity of the home it bound (device and inode, or volume serial number
  and file index on Windows, read from the held handle; a journal without one is never
  cleaned) and whether it already retired the
  credential: a recorded home that is missing confirms cleanup only if no
  credential was ever written or the Worker retired it, and a directory at the path
  that is not the one the lease bound is never cleaned as if it were, because the
  original may have been renamed away with the plaintext inside (the lease then
  stays fenced for manual inspection).
- **Continuation.** Only the first input of a General Chat workspace runs, as in
  the API lane: a continuation, fork or repository workspace is `Unsupported`.
  Grok records at `domain.GrokProtocolVersion` (`1.0.41`) and version-1 results
  keep their attribution and acquire no resume authority from this change;
  same-account, same-Worker continuation remains separate work.
- **Logging.** Execution records use phase `EXECUTION` with a closed step
  (`lease`, `credential`, `finish`, plus the harness probe steps), account and
  lease identifiers, closed booleans and stable codes; recovery records use phase
  `CLEANUP`. No credential, path, native text or identity value is logged, and
  tests assert it.

### Server operation lifecycle table

Every server-owned Grok operation passes through the same stages, and each cell
below has one durable outcome. "Fenced" means the account is
`RECOVERY_REQUIRED` (`recovery_required` set, health failed, operation retained);
the diagnostic always carries the phase of the operation (`LOGIN`, `REFRESH` or `LOGOUT`)
and the operation as correlation id. The scanner settles only a fenced operation
whose exchange was never claimed; a claim is never released automatically.

| Stage reached                                         | Event                                                                             | Outcome                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Recovery kind                                                  | Diagnostic                            |
| ----------------------------------------------------- | --------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- | ------------------------------------- |
| queued, not leased                                    | restart                                                                           | fenced by the startup fence, then settled `FAILED` by the scanner (`recoverGrokLogin` or `recoverGrokRefresh`); a refresh restores the health it was admitted under (`health_before`)                                                                                                                                                                                                                                                                                                                                                                                                              | automatic                                                      | `unavailable`                         |
| queued, not leased                                    | cancel (login only)                                                               | `CANCELED`, pending cleared                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | none                                                           | none                                  |
| leased, no claim                                      | shutdown or cancel                                                                | `CANCELED`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         | none                                                           | none                                  |
| leased, no claim                                      | window expiry                                                                     | `EXPIRED`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | none                                                           | none                                  |
| leased, no claim                                      | restart                                                                           | fenced, then settled `FAILED` by the scanner                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       | automatic                                                      | `unavailable`                         |
| leased, no claim                                      | failure before the claim (vault, discovery, listener)                             | `FAILED`, credential untouched                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | none                                                           | the failure code                      |
| claimed device login, between polls                   | server shutdown                                                                   | fenced, the claim is retained                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | manual, never resent                                           | `recovery_required`                   |
| claimed device login, between polls                   | user cancel (including one committed after the last store check)                  | `CANCELED`, the claim stays recorded and no further poll is sent; the durable operation is re-read immediately before every poll, so a cancellation that precedes a poll never lets it go out                                                                                                                                                                                                                                                                                                                                                                                                      | none                                                           | none                                  |
| claimed, request not sent                             | restart or crash                                                                  | fenced, the claim is retained                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | manual, never resent                                           | `recovery_required`                   |
| claimed, request sent                                 | transport failure, timeout, shutdown, cancel, unparsed or unexpected response     | fenced                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | manual, never resent                                           | `recovery_required`                   |
| claimed, request sent                                 | definitive refusal (see the [response table](#token-endpoint-response-table))     | `FAILED` (refresh also sets health `expired`)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | none                                                           | `unauthenticated` or the refusal code |
| claimed refresh, response received                    | identity, ID token, userinfo or mapping not verified                              | fenced                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | manual                                                         | `recovery_required`                   |
| claimed login, response received                      | ID token, userinfo or mapping not verified                                        | `FAILED`, nothing stored (there is no credential to protect)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       | none                                                           | the failure code                      |
| claimed login, response received                      | cancel, shutdown or window expiry interrupts the verification                     | fenced: the tokens are issued, so the interruption never becomes a canceled login that clears the claim                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | manual                                                         | `recovery_required`                   |
| claimed, response verified                            | settlement committed                                                              | `SUCCEEDED`, next generation published                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | none                                                           | none                                  |
| claimed, response verified                            | settlement write failed before the commit                                         | fenced, staged generation retired                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | manual                                                         | `recovery_required`                   |
| logout accepted, not leased                           | restart                                                                           | health `revoked`, fenced, then settled `FAILED` by the scanner with the credential intact                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | automatic                                                      | `unavailable`                         |
| logout accepted, waiting for an existing Worker lease | restart                                                                           | the logout settles `FAILED` through the scanner; the lease predates the restart, so it stays fenced (account `failed`) until its original Worker releases it, and the account is `revoked` afterwards; a new logout request follows                                                                                                                                                                                                                                                                                                                                                                | automatic for the logout, by the original Worker for the lease | `unavailable`                         |
| logout leased, no revocation claim                    | vault read, bundle validation or claim preconditions fail                         | `FAILED`, account stays connected and `revoked`; only another logout may be requested, a refresh is refused (`Conflict`)                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | none                                                           | the failure code                      |
| logout claimed                                        | request refused (any 4xx), unknown (5xx, transport, timeout, shutdown) or restart | fenced, the bundle is kept, the token is never resent                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              | manual                                                         | `recovery_required`                   |
| logout claimed                                        | HTTP 200                                                                          | revocation recorded, the vault is cleaned and the account is disconnected                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | none                                                           | none                                  |
| logout revoked, cleanup pending                       | vault removal or disconnect fails, or restart                                     | fenced with the cleanup-pending code on every fence path, restart included (`grokFenceCode`, `TestGrokLogoutCrashPointsNeverResendOrDeleteWithoutRecoveryState`); the scanner repeats only the cleanup                                                                                                                                                                                                                                                                                                                                                                                             | automatic                                                      | `unavailable`                         |
| execute lease held                                    | server restart, lost Worker instance or replayed Take                             | fenced; the record is delivered only to the owning Runner Device credential, whose Worker retires the credential and Finishes with confirmed cleanup                                                                                                                                                                                                                                                                                                                                                                                                                                               | automatic on the original Worker                               | none                                  |
| execute lease held, logout accepted during the lease  | the lease is fenced and then cleaned up by its original Worker                    | the lease is released and the account stays `revoked` while the logout is pending or after it settled as failed or canceled without success (never `ready`; a refresh returns to its admitted health in the same cases, `TestGrokExecuteRecoveryRestoresTheHealthOfACanceledQueuedOperation`); after a restart the scanner settles the unclaimed logout first and the stale lease stays fenced for its Worker                                                                                                                                                                                      | automatic                                                      | none                                  |
| execute lease held                                    | the Worker's Finish response is lost                                              | the Worker reports execution uncertainty (the primary lane closes and ownership is reconciled); the server keeps the lease fenced for the same recovery                                                                                                                                                                                                                                                                                                                                                                                                                                            | automatic on the original Worker                               | none                                  |
| execute Take                                          | discovery replaced the installation after dispatch                                | the lease launches the installation the Take returned and the startup evidence (executable, digest and continuation journal) is re-observed for it, failing closed when it cannot be observed or contradicts a pinned digest (`TestGrokStartupEvidenceFollowsTheExecutableTheTakeAuthorized`); the bytes behind the canonical path must hash to the digest the Take admitted, so a replacement is never promoted into the evidence ([Executable identity of a lease](#executable-identity-of-a-lease)); an unusable one releases the lease with confirmed cleanup before any credential is written | none                                                           | none                                  |
| claimed (login, refresh or logout)                    | the settlement fails and the recovery-fence write fails too                       | the operation stays native-started and unfenced with no runner; the scanner selects it (`grokOrphanedOperation`) and repeats only the fence with bounded backoff, never resending a request; a settlement that committed meanwhile is left as committed                                                                                                                                                                                                                                                                                                                                            | manual, never resent                                           | `recovery_required` once fenced       |
| any stage                                             | settlement commit acknowledgement lost after the commit                           | the committed outcome stands, nothing is rewritten                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 | none                                                           | as committed                          |
| any stage                                             | cancel after a claim (login)                                                      | an uncertain claimed outcome stays fenced; a successful claimed exchange publishes, because the tokens are issued and the cancellation is too late; a failed one stays failed; a vanished actor fences a success (`TestGrokCancelRacingTheSettlementOfASuccessfulClaimedLoginIsTooLate`)                                                                                                                                                                                                                                                                                                           | manual only when uncertain                                     | `recovery_required` when fenced       |

### Queued operations behind an execution lease

A server-owned logout or refresh is accepted while an execution lease holds the
account (the accepted logout also cancels that execution). The operation is
queued and unclaimed: it sends nothing and claims nothing until the lease is gone
and the account is not fenced. The lease and the queued operation have different
owners, so the lease's Finish never reads the queued operation's actor or
cancellation, and the queued operation is never settled by the lease's outcome.
Each cell below has one outcome; the tests are in
`internal/server/grok_queued_operations_test.go` and
`internal/server/grok_execution_queued_test.go` (the same cells against a real
execute Take).

| Event on the execution lease                                                                         | Queued logout                                                                                                                                                                                   | Queued refresh                                                                                                                                                             | Test                                                                                                                                                                                                                       |
| ---------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| lease active                                                                                         | waits, health `revoked`; new execution is typed `UNAVAILABLE`                                                                                                                                   | waits; new execution is refused while the operation is pending                                                                                                             | `TestGrokLogoutWaitsForTheOriginalExecutionLease`, `TestGrokExecuteTakeIsOnlyUnavailableWhileLoggingOut`                                                                                                                   |
| Finish confirmed, credential published                                                               | lease released, the published generation is current; the logout then revokes that generation                                                                                                    | lease released; the refresh then runs on the published generation                                                                                                          | `TestGrokQueuedOperationProceedsAfterTheExecutionFinishReleasesTheLease`, `TestGrokQueuedOperationProceedsAfterEveryConfirmedExecuteFinish`, `TestGrokLogoutCancelsTheExecutionAndReachesRevocationAfterItsCleanup`        |
| Finish confirmed, not succeeded because the logout canceled the execution                            | lease released, generation unchanged, no fence; the logout proceeds                                                                                                                             | lease released, generation unchanged, no fence; the refresh proceeds                                                                                                       | `TestGrokQueuedOperationProceedsAfterTheExecutionFinishReleasesTheLease`, `TestGrokQueuedOperationProceedsAfterEveryConfirmedExecuteFinish`, `TestGrokLogoutCancelsTheExecutionAndReachesRevocationAfterItsCleanup`        |
| Finish with the actor of the queued operation revoked or the queued operation canceled               | the lease is released; the actor and cancellation of the queued operation are not read                                                                                                          | the same                                                                                                                                                                   | `TestGrokExecutionFinishDoesNotDependOnTheQueuedOperationsActor`                                                                                                                                                           |
| Finish committed, then the removal of the superseded generation fails or the acknowledgement is lost | fenced without a lease; the scanner removes the superseded generations, clears the fence (health `revoked`) and the logout proceeds                                                             | the same; the health returns to `health_before`                                                                                                                            | `TestGrokQueuedOperationSurvivesAFailedCleanupAfterTheExecutionFinish`, `TestGrokQueuedOperationSurvivesAFailedCleanupAfterARealExecuteFinish`, `TestGrokSubscriptionScannerReconcilesAQueuedFenceUntilTheRemovalSucceeds` |
| Finish with unconfirmed cleanup                                                                      | the lease stays fenced, the logout waits and nothing is revoked; only the original Worker can release the lease                                                                                 | the same; nothing is refreshed                                                                                                                                             | `TestGrokQueuedOperationWaitsForAnUnconfirmedExecutionCleanup`                                                                                                                                                             |
| Worker lost                                                                                          | the lease is fenced, the logout waits and nothing is revoked                                                                                                                                    | the same                                                                                                                                                                   | `TestGrokQueuedOperationWaitsForALostWorker`, `TestGrokExecuteRecoveryRestoresTheHealthOfAQueuedOperation`                                                                                                                 |
| server restart while the lease is held                                                               | the logout settles `FAILED` with the credential intact; the stale lease stays fenced (account `failed`) until its original Worker's Finish releases it, and the account is `revoked` afterwards | the refresh settles `FAILED` with the credential intact; the stale lease stays fenced until its original Worker's Finish releases it, and `health_before` is restored then | `TestGrokRestartSettlesAnOperationQueuedBehindAnExecutionLease`, `TestGrokStaleLeaseIsReleasedByItsExactOwnerFinish`                                                                                                       |
| server restart after the leaseless fence of a committed Finish                                       | the logout settles `FAILED`, the fence clears, the superseded generation stays for the next cleanup                                                                                             | the same; health returns to `health_before`                                                                                                                                | `TestGrokRestartSettlesAnOperationFencedAfterTheExecutionFinish`                                                                                                                                                           |

The leaseless queued fence is recognized by shape only (`grokQueuedFenceNeedsReconcile`):
a connected account that is fenced, holds no lease and owns an unclaimed logout or
refresh of the current epoch that is still queued. Its reconcile
(`grok.server.queued-fence-reconcile`) removes every vault generation except the
current one, clears the fence only after that removal is confirmed, and is retried
by the scanner with bounded backoff; a lease, a claim or a restart-fenced
operation is never reconciled this way.

### Credential-free home of an execute lease

A lease is never released over a plaintext credential this Worker did not account
for. The bound private home is made credential-free before the Take, and an
execution that never materialized a credential confirms cleanup only after the
home is proven credential-free again.

| Credential entry in the bound home                  | Before the Take            | Finish after an execution that never materialized | Test                                                                                                                                                                   |
| --------------------------------------------------- | -------------------------- | ------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| absent                                              | the Take proceeds          | cleanup confirmed                                 | `TestGrokExecutionThatNeverMaterializedConfirmsNothingWasWritten`                                                                                                      |
| regular file (leftover, or appeared after the Take) | retired, the Take proceeds | retired, cleanup confirmed                        | `TestGrokExecutionRetiresALeftoverCredentialBeforeTheTake`, `TestGrokExecutionThatNeverMaterializedRetiresACredentialThatAppearedInTheHome`                            |
| entry that cannot be retired                        | refused, no lease          | cleanup not confirmed, the lease stays fenced     | `TestGrokExecutionRefusesAHomeWhoseLeftoverCredentialCannotBeRetiredBeforeTheTake`, `TestGrokExecutionThatNeverMaterializedDoesNotConfirmAnUnretirableCredentialEntry` |

### Executable identity of a lease

A Take authorizes one installation: its path, its version and the SHA-256 of the
executable that discovery probed. Discovery reports that digest for a detected
Grok Build installation exactly as it does for Codex (the digest is taken before
the version probe and again after the protocol probe, and discovery fails when
the bytes changed in between), and no other harness or discovery state carries
one. Admission refuses an observation without a valid digest as missing discovery
evidence, and the Take returns the admitted observation. The Worker launches only
bytes that hash to the admitted digest; it never adopts the digest of whatever
bytes it finds later.

| Stage                                                  | Bytes behind the authorized canonical path                                    | Outcome                                                                                              | Test                                                                                                                                            |
| ------------------------------------------------------ | ----------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| discovery                                              | stable through the probes                                                     | the digest is reported on the detected installation                                                  | `TestDiscoveryReportsTheExecutableIdentityOfADetectedGrokInstallation`                                                                          |
| discovery                                              | changed during the probes                                                     | discovery fails (`UNAVAILABLE`); no digest is reported                                               | `TestDiscoveryRefusesAGrokExecutableThatChangesDuringTheProbe`                                                                                  |
| discovery report                                       | a digest on a non-detected, Claude Code or OpenCode entry, or a malformed one | rejected                                                                                             | `TestDiscoveryExecutableIdentityBelongsToDetectedCodexAndGrokOnly`                                                                              |
| Take admission                                         | observation without a valid digest                                            | refused as discovery evidence; no lease                                                              | `TestGrokTakeRefusalCarriesPhaseAndVersions`                                                                                                    |
| Take response                                          | the admitted observation                                                      | returned with the digest                                                                             | `TestGrokTakeReturnsTheAdmittedExecutableIdentity`                                                                                              |
| Worker, Take response decoded                          | no or malformed digest                                                        | the lease is released with confirmed cleanup before any credential exists                            | `TestAuthorizedGrokInstallationRequiresTheAdmittedDigest`, `TestGrokExecutionRefusesAnUnusableAuthorizedInstallationAndReleasesTheLease`        |
| launch target, with or without startup evidence        | hash to the admitted digest                                                   | launched; the evidence moves to those bytes when the local resolution found others                   | `TestGrokStartupFollowsBytesReplacedBeforeTheTakeAdmittedThem`, `TestGrokLaunchTargetFailsClosedWhenTheBytesDifferFromTheAdmittedDigest`        |
| launch target, with or without startup evidence        | differ from the admitted digest                                               | `RECOVERY_REQUIRED` before any credential; the evidence and the journal are untouched                | `TestGrokStartupRefusesAnExecutableReplacedAtTheSamePathAfterTheTake`, `TestGrokLaunchTargetFailsClosedWhenTheBytesDifferFromTheAdmittedDigest` |
| launch target                                          | differ from a digest the selection pinned                                     | `RECOVERY_REQUIRED`; the original native history is not continued on other bytes                     | `TestGrokStartupFailsClosedWhenTheAuthorizedExecutableDiffersFromThePinnedDigest`                                                               |
| launch target                                          | missing or unreadable                                                         | `RECOVERY_REQUIRED`; the evidence is untouched                                                       | `TestGrokStartupFailsClosedWhenTheAuthorizedExecutableCannotBeObserved`                                                                         |
| credential callback (the last gate before `auth.json`) | replaced after the launch target was verified                                 | `RECOVERY_REQUIRED`; no credential, no started journal; the lease is released with confirmed cleanup | `TestGrokPrepareRefusesBytesReplacedAfterTheLaunchTargetWasVerified`, `TestGrokExecutionNeverDeliversTheCredentialToReplacedBytes`              |
| ready report                                           | replaced after the credential callback                                        | `RECOVERY_REQUIRED`; the evidence digest is the admitted one                                         | `TestGrokStartupFollowsBytesReplacedBeforeTheTakeAdmittedThem`                                                                                  |

#### Launch binding

A digest check of the installed path proves nothing about the bytes the operating
system later executes: another updater can replace the path atomically (or write
the same inode) between the check and the exec. The Worker therefore never
launches the installed path. It copies the bytes it hashes into a private
snapshot inside the execution's own 0700 journal directory, in one read of one
open descriptor, accepts the snapshot only when that digest equals the admitted
digest and the installed path still names the file it read (identity, size and
modification time unchanged), and launches the snapshot for every native run of
the execution: the inspections of the empty private profile and the credentialed
launch alike. The installed path stays the startup evidence; the snapshot is only
what runs. The credential callback re-verifies the snapshot (identity first, then
its digest) immediately before `auth.json` is written, and the snapshot is
removed when the execution ends.

| Event                                                   | Outcome                                                                                               | Test                                                              |
| ------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| admitted bytes at the installed path                    | a private snapshot is created and launched; the credential reaches only the snapshot                  | `TestGrokLaunchBindingLaunchesOnlyTheSnapshotOfTheAdmittedBytes`  |
| installed path replaced after the bind                  | unaffected: the snapshot of the admitted bytes still runs                                             | `TestGrokLaunchBindingLaunchesOnlyTheSnapshotOfTheAdmittedBytes`  |
| installed path replaced before the bind                 | `RECOVERY_REQUIRED`, no snapshot is kept, no credential; the lease is released with confirmed cleanup | `TestGrokLaunchBindingRefusesBytesReplacedBeforeTheBind`          |
| snapshot rewritten in place before the credential       | `RECOVERY_REQUIRED` (digest), no credential                                                           | `TestGrokLaunchBindingRefusesASnapshotChangedBeforeTheCredential` |
| snapshot replaced by another file before the credential | `RECOVERY_REQUIRED` (identity), no credential                                                         | `TestGrokLaunchBindingRefusesASnapshotChangedBeforeTheCredential` |
| execution ends                                          | the snapshot is removed                                                                               | `TestGrokLaunchBindingLaunchesOnlyTheSnapshotOfTheAdmittedBytes`  |

Platform note and residual. A descriptor-based exec (`execve` of `/proc/self/fd`
on Linux, `fexecve` where it exists) would remove even the snapshot, but the
native launch crosses the owned-process supervisor boundary on Unix and macOS has
no `fexecve`, so one snapshot mechanism is used on every platform. The residual is
a writer that already runs as the Worker's own user and replaces the snapshot
inside the Worker's private execution directory in the instant between the
re-verification and the exec; such a writer can read the credential file directly,
so it is outside the boundary this binding protects. A native executable that
needs files next to its installed location is not supported by the snapshot and
fails at launch, and the normal cleanup then retires the credential.

### Stale lease after a restart

A lease granted by an earlier server epoch is stale. The restart fences the
account for it (`RECOVERY_REQUIRED`, health `failed`) even when no server
operation exists, and the fence stands until the lease is gone. The server never
retires a stale lease itself: the original Worker is the only party that can
prove its private home was cleaned, and a credential it still holds must not be
redistributed. Only a Finish resolves the lease, and the epoch binding of every
other call (Take readback, progress) is unchanged.

| Event on the stale lease                                                                  | Account document                                                                                                                                                           | Test                                                            |
| ----------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| restart with an execution lease and no server operation                                   | fenced, health `failed`, the lease kept; a second startup writes nothing                                                                                                   | `TestGrokRestartFencesAStaleLeaseOnce`                          |
| restart with a queued logout or refresh behind the lease                                  | the operation settles `FAILED` (credential intact); the lease and the fence stay                                                                                           | `TestGrokRestartSettlesAnOperationQueuedBehindAnExecutionLease` |
| Finish by the exact owner (device, machine, instance, lease), cleanup confirmed           | lease released, fence cleared; health is `revoked` after a settled logout, `health_before` after a settled refresh and `ready` otherwise                                   | `TestGrokStaleLeaseIsReleasedByItsExactOwnerFinish`             |
| Finish by the exact owner after a client revocation canceled the queued logout or refresh | as the row above: a canceled operation restores health exactly like a failed one, so an accepted logout keeps readiness `revoked` and a refresh returns to `health_before` | `TestGrokStaleLeaseKeepsTheHealthOfACanceledQueuedOperation`    |
| the same Finish reporting a rotated credential                                            | published under the same identity, generation and rotation checks as a current Finish; the settled operation stays settled                                                 | `TestGrokStaleLeaseFinishPublishesTheRotatedCredential`         |
| Finish by the exact owner, cleanup not confirmed                                          | lease and fence kept; the owner may report again                                                                                                                           | `TestGrokStaleLeaseStaysFencedUntilCleanupIsConfirmed`          |
| Finish by another Worker, Runner Device or instance                                       | denied before any write                                                                                                                                                    | `TestGrokStaleLeaseIsDeniedToEveryoneButItsOwner`               |
| Finish for a stale lease on an account the restart did not fence                          | denied before any write                                                                                                                                                    | `TestGrokStaleLeaseIsDeniedToEveryoneButItsOwner`               |
| Finish for a lease of the current epoch                                                   | unchanged: a fenced lease is released only by the logout retry rule                                                                                                        | `TestGrokStaleLeaseIsDeniedToEveryoneButItsOwner`               |

A Worker that no longer holds the lease (its instance was replaced) is the
lost-Worker row of the interleavings table, not a stale lease: its Finish stays
refused.

### Ownerless cleanup fence

A Finish that publishes a rotated credential (an execute whose native process
rotated it, a refresh, a login) clears its lease and pending operation in the
settlement commit and removes the superseded vault generations afterwards. When
that removal fails, the account is fenced for recovery with nothing left that
owns the fence: no lease for a Worker to report against, no pending operation
for the queued-fence reconcile, no running operation. The published generation
is intact, and the only outstanding work is the idempotent removal of every other
generation. The scanner owns that work (`recoverGrokOwnerlessCleanupFence`): it
retries with bounded backoff, removes every generation except the current one
(and the quota and credit generations that stay protected), then clears the fence
and returns the health to `ready`. It never reads or rewrites the credential.

| Fence shape                                                    | Owner of the fence                                    | Test                                                        |
| -------------------------------------------------------------- | ----------------------------------------------------- | ----------------------------------------------------------- |
| connected, no lease, no pending operation, no active operation | the scanner                                           | `TestGrokRotationCleanupFailureIsOwnedByTheScanner`         |
| the same after an execute that rotated the credential          | the scanner                                           | `TestGrokRotationCleanupFailureIsOwnedByTheScanner`         |
| the same after a refresh                                       | the scanner                                           | `TestGrokRotationCleanupFailureIsOwnedByTheScanner`         |
| the scanner's own removal fails                                | the scanner again, with the fence kept                | `TestGrokRotationCleanupFailureIsOwnedByTheScanner`         |
| a lease, a pending operation or an active operation is present | that lease, operation or reconcile, never the scanner | `TestGrokOwnerlessCleanupFenceSelectsOnlyTheOwnerlessShape` |

### Late cancellation and actor loss

A cancellation (or a vanished actor) that is durable when a login settles changes
the settled outcome only where it stopped the operation. `grokLateCancelOutcome`
is the one table, applied once before the vault write and again inside the
settlement transaction; `TestGrokLateCancelOutcomeTable` pins every cell.

| Settled outcome      | Claimed, cancel                | Claimed, actor gone | Unclaimed, cancel or actor gone |
| -------------------- | ------------------------------ | ------------------- | ------------------------------- |
| succeeded            | published (too late to cancel) | fenced (recovery)   | canceled                        |
| failed (own failure) | failed, diagnostic kept        | failed              | canceled                        |
| expired              | expired                        | expired             | canceled                        |
| canceled             | canceled                       | canceled            | canceled                        |
| recovery (uncertain) | recovery                       | recovery            | recovery                        |

A claimed failure (a provider refusal such as `access_denied` or a failed
verification) is a complete outcome of its own, so a cancellation that lands
before the write never rewrites it and its diagnostic stays. Before the claim
nothing was redeemed, and the claim refusal and the steps report a stop as a
failure, so it settles as canceled.

### Commit acknowledgement ambiguity

A transaction can commit and still report an error (a lost acknowledgement). Each
durable transaction of the Grok flows therefore has one reconcile rule, and none
of them marks a committed transaction as failed or fences it again. The finish
paths of login and refresh reconcile through `fenceUnsettledGrokOperation`: it
re-reads the durable operation and fences it only while it is still the account's
pending native-started operation.

| Transaction                                                                         | Ambiguous error                                                                                                  | Reconcile rule                                                                                                                                                                                                                                                                                                           |
| ----------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| lease (`grok.server.refresh-lease`, `grok.server.claim`)                            | the lease may be durable with no owner running                                                                   | the runner re-reads the operation: a durable lease (native-started, pending) is fenced with the phase diagnostic so the scanner settles it as `FAILED`; an operation that is still queued is left for the next scan (`TestGrokLeaseWithALostAcknowledgementIsFencedForTheScanner`)                                       |
| exchange claim (`claimGrokExchange`, `claimGrokRefresh`)                            | the claim flag may be durable although the request was never sent                                                | the request is not sent after any claim error, so the operation settles `FAILED` with the stored credential untouched and the flag is inert on a settled operation; a claim whose commit succeeded is the only way a request is sent                                                                                     |
| stage (vault write of the next generation)                                          | the generation may exist                                                                                         | an unpublished generation is retired only after proving the account does not reference it                                                                                                                                                                                                                                |
| publish (`grok.server.finish`, `grok.server.refresh-finish`)                        | the settlement may be committed                                                                                  | re-read: a settled operation is the outcome; a still-pending one is fenced with the phase diagnostic; superseded generations are removed by the next cleanup                                                                                                                                                             |
| Worker-lease logout Finish (`grok.subscription.finish`)                             | the vault removal may be partial or the commit unknown                                                           | the revoked bundle is deleted from the vault before the commit that drops the lease and generation; a failed removal fences the account with the lease and generation kept, and the exact Worker repeats the Finish; no second enumeration runs after the commit, so nothing can refence an already disconnected account |
| execution lease Finish (`grok.subscription.finish`) with a queued logout or refresh | the lease may be released although the Finish reports an error (failed superseded removal, lost acknowledgement) | the account is fenced without a lease while the queued operation is unclaimed; the scanner reconciles it (`grok.server.queued-fence-reconcile`) after the owed removal, so the operation proceeds                                                                                                                        |
| revocation claim (`grok.server.logout-claim`)                                       | the claim may be durable although no request was sent                                                            | settlement re-reads the claim: a durable `grok_revocation` makes the failure uncertain and fenced (the claim is retained, no later logout can overwrite it and resend the token); an absent claim is a plain failed logout with the credential intact                                                                    |
| revocation record (`grok.server.logout-revoked`)                                    | the confirmation may be durable                                                                                  | the operation is fenced as recovery; a durable confirmation is a revoked kind and only repeats the cleanup, an absent one stays claimed and manual                                                                                                                                                                       |
| disconnect (`grok.server.logout-disconnect`)                                        | the account may already be disconnected                                                                          | re-read: a settled operation (no pending operation, generation cleared) is the committed logout and is never rewritten to recovery; a still-pending one is fenced and the scanner repeats the cleanup                                                                                                                    |
| execute Take and Finish (`TakeSubscription`, `FinishSubscription`)                  | the Worker may not know whether the lease exists or was released                                                 | the Worker journals the claim before the Take; a lost Take response fences the replayed Take; a lost Finish response is execution uncertainty and the original Worker repeats the recovery Finish across the fence                                                                                                       |

### Token endpoint response table

The authorization-code exchange and the refresh grant share one response classifier (`classifyGrokTokenResponse`). A body is parsed only when it is exactly one JSON object
whose top-level member names are unique: another JSON value, a stray comma, any other non-whitespace byte
after the first object or a repeated member (the decoder would silently keep the
last one, and implementations disagree) makes the response unparsed, whatever its
status. Both grants are claimed
before the request, so an unparsed response is never a published success and never
a definitive rejection that would release the claim.

Member names are case-sensitive, as OAuth and JSON define them. The classifier
reads exactly the canonical spellings (`access_token`, `refresh_token`,
`id_token`, `token_type`, `expires_in`, `error`, `error_description`,
`error_uri`) from the decoded top-level members; it never relies on the decoder's
case-insensitive struct matching and never folds case. A name that equals a
canonical name only when case is ignored (for example `ERROR` or `Access_Token`)
is an alias: it is never read as the member, and its presence makes the body
untrustworthy. The same exact-name rule applies to the discovery metadata and to
every other provider document the Grok flows read; repeated names are compared
exactly, so two spellings that differ in case are two distinct names, and the
alias rule is what refuses them. The table is pinned by
`TestGrokTokenResponseTable`, which runs every row against both grants, and the
discovery rule by `TestGrokDiscoveryRequiresExactMemberNames`.

The device authorization response and the device poll answers follow it too: a
repeated member (for example a repeated `error`, which could turn a rejection into
a pending answer) makes the document unparsed, so a poll stays uncertain. A
poll answer (`authorization_pending`, `slow_down`, `access_denied`,
`expired_token`) is read only from a rejection of the table below, so an answer
that also carries a token member is uncertain as well.

The revocation endpoint is classified by status alone (only HTTP 200 revokes, see
[Server-owned logout](#server-owned-logout)); its body is never read, so it has no
body rows and cannot contradict its status.

The identity evidence of a grant is held to the same rule: an ID-token payload
or a userinfo response that repeats a claim name is refused (exact comparison, as
claim names are case-sensitive), because the decoder would keep the last
occurrence (`TestGrokIdentityEvidenceRejectsRepeatedClaims`).

| Status                                                     | Body                                                                                                                                      | Reason                     | Settlement after the claim         |
| ---------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | -------------------------- | ---------------------------------- |
| 200                                                        | one valid token object (trailing whitespace allowed)                                                                                      | success                    | publish                            |
| 200                                                        | a token object that also carries any of `error`, `error_description`, `error_uri` (even empty or null) or an alias of one of them         | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| 200                                                        | a token object that carries an alias of a token member (a name that differs from it only in case), or only aliases                        | `GrokReasonTokenResponse`  | `RECOVERY_REQUIRED`                |
| 200                                                        | token object followed by another JSON value or any other byte, or a repeated member                                                       | `GrokReasonTokenResponse`  | `RECOVERY_REQUIRED`                |
| 200                                                        | missing or invalid member, not JSON                                                                                                       | `GrokReasonTokenResponse`  | `RECOVERY_REQUIRED`                |
| 400, 401                                                   | exactly one object with a non-empty `error`, none of the token members and no alias                                                       | `GrokReasonTokenRejected`  | `FAILED`, the token did not rotate |
| 400, 401                                                   | an `error` object followed by any other byte or JSON value, or with a repeated member                                                     | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| 400, 401                                                   | a non-empty `error` that also carries any of `access_token`, `refresh_token`, `id_token`, `token_type`, `expires_in` (even empty or null) | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| 400, 401                                                   | an `error` object that also carries an alias of any token or error member (for example `Error_Description`, `Access_Token`)               | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| 400, 401                                                   | no canonical `error` member (including only an alias such as `ERROR`), an empty `error`, a non-string `error`, or not JSON                | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| other 4xx, 5xx, redirect                                   | any body                                                                                                                                  | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |
| transport error, cancellation, truncated or oversized body | none                                                                                                                                      | `GrokReasonTokenUncertain` | `RECOVERY_REQUIRED`                |

The revocation request of a logout is parsed more strictly than a grant, because
it settles only by status: RFC 7009 answers HTTP 200 both for a revoked token and
for an invalid submitted one, so an error response is a refused request.

| Status                                                                                               | Body | Reason                      | Settlement after the claim                      |
| ---------------------------------------------------------------------------------------------------- | ---- | --------------------------- | ----------------------------------------------- |
| 200                                                                                                  | any  | revoked                     | the revocation is recorded and the cleanup runs |
| any 4xx, including 400 or 401 with `invalid_grant` or `invalid_token`, with or without trailing data | any  | `GrokReasonRevokeRejected`  | `RECOVERY_REQUIRED`, the bundle is kept         |
| 5xx, redirect, other status                                                                          | any  | `GrokReasonRevokeUncertain` | `RECOVERY_REQUIRED`, the bundle is kept         |
| transport error, cancellation, oversized body                                                        | none | `GrokReasonRevokeUncertain` | `RECOVERY_REQUIRED`, the bundle is kept         |

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

| Interleaving                                                                                                             | Outcome                                                                                                                                                                                           | Test                                                                                                                                                                                                                                                                                                          |
| ------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Take of the queued original                                                                                              | Lease committed, stored envelope re-opened and delivered once                                                                                                                                     | `TestGrokLifecycleMovesBundleOnlyThroughProtectedTakeAndFinish`                                                                                                                                                                                                                                               |
| Take on an unverified or below-baseline installation, or for execute                                                     | Refused before any lease; account untouched                                                                                                                                                       | `TestGrokTakeRefusesUnsupportedAndUnverifiedWork`, `TestGrokTakeRequiresSubscriptionBaselineVersion`                                                                                                                                                                                                          |
| Take replayed after a lost first response                                                                                | Denied; the exact unfinished lease is fenced; a finished or foreign lease is untouched                                                                                                            | `TestGrokReplayedTakeFencesTheUnfinishedLease`, `TestGrokLateReplayedTakeDoesNotFenceAReleasedAccount`                                                                                                                                                                                                        |
| Replaying request canceled, past its deadline or from a revoked Worker while the fence runs                              | The fence still commits on a bounded, detached context; the lease is never stranded without `RecoveryRequired`                                                                                    | `TestGrokReplayFenceIsDetachedFromTheReplayingRequest`                                                                                                                                                                                                                                                        |
| Take whose stored envelope is invalid or missing, or whose operation is canceled during the vault read                   | Nothing delivered; lease kept and account fenced under the current generation                                                                                                                     | `TestGrokTakeFailuresAfterTheLeaseCommitFenceWithoutDelivery`                                                                                                                                                                                                                                                 |
| Installation rediscovered after Take                                                                                     | Finish still accepted from the exact owner                                                                                                                                                        | `TestGrokFinishSurvivesInstallationRediscoveryAfterTake`                                                                                                                                                                                                                                                      |
| Finish with a stale lease revision, generation or lease                                                                  | Denied before any write; no fence; the genuine owner can still finish                                                                                                                             | `TestGrokFinishWithAStaleLeaseIsDeniedWithoutFencing`                                                                                                                                                                                                                                                         |
| Finish with a rebound, foreign, native, empty or wrong-identity bundle, or an unrotated refresh                          | Refused; account fenced; nothing staged                                                                                                                                                           | `TestGrokFinishRejectsReboundAndForeignBundles`, `TestGrokRefreshRequiresSameIdentityAndRotation`, `TestGrokLoginRejectsDuplicateIdentityAndAdmitsDifferentPrincipal`                                                                                                                                         |
| Finish reports of login, refresh and logout (success, native refresh, cleanup)                                           | Table-driven: released only when fully confirmed (or a failed login with confirmed cleanup); otherwise fenced under the existing generation                                                       | `TestGrokFinishReportsMapToExplicitOutcomes`, `TestGrokFinishFencedLogoutKeepsTheReferencedGeneration`                                                                                                                                                                                                        |
| Finish publishes, then removes the superseded generation                                                                 | Removal only after the document stops referencing it; a confirmed logout first removes every generation, which the provider already revoked, and only then drops the lease and generation         | `TestGrokFinishDeletesTheReferencedGenerationOnlyAfterPublication`                                                                                                                                                                                                                                            |
| Finish aborted after staging (revoked Worker, store failure)                                                             | Account fenced; current generation kept; staged generation retired after the account is proven not to reference it                                                                                | `TestGrokFinishFailedPublicationKeepsTheCurrentGeneration`, `TestGrokRetireUnpublishedGenerationRequiresProofOfNonPublication`                                                                                                                                                                                |
| Finish aborted and the staged generation cannot be removed                                                               | Account fenced; current generation kept; the staged generation stays vault-owned, never granted, removed by the next cleanup                                                                      | `TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration`                                                                                                                                                                                                                         |
| Cancellation wins while the new generation is staged                                                                     | Generation never published; staged generation removed                                                                                                                                             | `TestGrokFinishCanceledDuringStagingRemovesTheStagedGeneration`                                                                                                                                                                                                                                               |
| Removal of the superseded generation fails after the commit                                                              | Error surfaced and account fenced; the published generation is kept, the superseded one is removed by the next cleanup                                                                            | `TestGrokFinishCleanupFailureAfterPublicationKeepsThePublishedGeneration`                                                                                                                                                                                                                                     |
| Request ends while the superseded generation is removed after the commit                                                 | Removal continues on a bounded, detached context; the committed publication is neither undone nor fenced                                                                                          | `TestGrokFinishCleanupIsDetachedFromTheRequestAndBounded`, `TestGrokRetireUnpublishedGenerationSurvivesAnEndedRequest`                                                                                                                                                                                        |
| Finish retried after it committed                                                                                        | Receipt replay; no vault write or removal; account untouched                                                                                                                                      | `TestGrokFinishReplayRepeatsNoVaultWork`                                                                                                                                                                                                                                                                      |
| Worker instance lost while a lease is held                                                                               | Lease fenced under its generation; later Finish and Take refused; the vault is unchanged                                                                                                          | `TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish`                                                                                                                                                                                                                                                       |
| Server restarted while a lease is held (a stale lease)                                                                   | Lease fenced under its generation, Take refused; the exact owner's Finish is the only release, see [Stale lease after a restart](#stale-lease-after-a-restart); the vault is unchanged until then | `TestGrokRestartFencesAStaleLeaseOnce`, `TestGrokStaleLeaseIsReleasedByItsExactOwnerFinish`                                                                                                                                                                                                                   |
| Server crash between staging and the commit                                                                              | Same as a restart: lease fenced; the staged generation is an unreferenced vault object, never granted, removed by the next cleanup                                                                | `TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration`                                                                                                                                                                                                                         |
| Worker materializes a bound envelope, a rebound or malformed one, or an unsafe home                                      | Written once with owner-only permission; otherwise nothing written                                                                                                                                | `TestGrokMaterializationWritesPrivateAuthFileFromBoundEnvelope`, `TestGrokMaterializationRejectsReboundEnvelopeBeforeWriting`, `TestGrokMaterializationRefusesNonPrivateOrLinkedHome`                                                                                                                         |
| Home renamed away and replaced after binding, before or after the path check                                             | The credential is written, read and retired only in the bound directory; the substitute is never written, read or modified                                                                        | `TestGrokWriteIsAnchoredToTheBoundHomeNotItsPath`, `TestGrokWriteDoesNotFollowAPlantedAuthFileLink`, `TestGrokHomeRejectsALooseningAfterBinding`, `TestGrokHomeBindingFailsClosed`                                                                                                                            |
| Fresh login in a home that already holds a credential or temporary                                                       | Refused; a leftover is never sealed as the login result                                                                                                                                           | `TestGrokFreshLoginRefusesLeftoverCredentialFiles`                                                                                                                                                                                                                                                            |
| Native process rotates, fails, changes identity or leaves an unparsable file                                             | Rotated credential sealed; otherwise no bundle; the file is retired in every case                                                                                                                 | `TestPrepareGrokFinishSealsRotatedCredentialAndRetiresFile`                                                                                                                                                                                                                                                   |
| Remnant that cannot be removed, or a hard-linked credential                                                              | No bundle; cleanup unconfirmed; lease stays fenced                                                                                                                                                | `TestPrepareGrokFinishWithholdsBundleWhenCleanupIsUnconfirmed`, `TestPrepareGrokFinishWithholdsBundleForHardLinkedCredential`                                                                                                                                                                                 |
| Hard link created after the credential was inspected and before its name is unlinked                                     | No bundle; cleanup unconfirmed (retained-handle link count and identity rescan); lease stays fenced                                                                                               | `TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheUnlink`, `TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheTemporaryUnlink`, `TestGrokWriteRefusesAnOlderLinkedCredential`                                                                                                                                |
| Home renamed away or replaced after materialization                                                                      | No bundle and no cleanup for refresh, login and logout; nothing read or removed in the substitute; the original keeps its file                                                                    | `TestPrepareGrokFinishRefusesARenamedOrReplacedHome`, `TestGrokHomeVerifiesTheBoundDirectoryIdentity`                                                                                                                                                                                                         |
| Finish prepared for a lease that never bound a home                                                                      | Cleanup never confirmed                                                                                                                                                                           | `TestPrepareGrokFinishRefusesAnUnboundHome`, `TestGrokMaterializationBindsTheHomeForFinish`, `TestGrokMaterializationReleasesTheHomeWhenItFails`                                                                                                                                                              |
| Worker crash or restart with the native file on disk                                                                     | The server fences the lease (row above); the plaintext stays in the private home until Worker-side recovery, which is not activated; nothing is resent                                            | Server side: `TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish`                                                                                                                                                                                                                                          |
| Server refresh with rotation, without rotation (omitted or unchanged token) or without an ID token                       | Next generation staged, published by one commit and superseded generations removed; identity commitment unchanged                                                                                 | `TestGrokRefreshReplacesTheBundleWithRotation`, `TestGrokRefreshWithoutRotationKeepsTheRefreshToken`, `TestGrokRefreshWorksWithoutAnIDToken`                                                                                                                                                                  |
| Server logout claim, request and outcomes                                                                                | Claim durable before the one revocation request; only HTTP 200 settles; every 4xx (including invalid_grant), 5xx and timeout fence with the bundle kept and are never resent                      | `TestGrokLogoutRevokesTheRefreshTokenThenDeletesTheBundle`, `TestGrokLogoutUnprovenRevocationLeavesRecoveryAndKeepsTheBundle`, `TestGrokLogoutTimeoutOrShutdownDuringRevocationIsUncertain`, `TestGrokRevokeClassifiesEveryOutcome`                                                                           |
| Server logout crash points and cleanup failure                                                                           | No resend and no bundle deletion without recovery state; a proven revocation only repeats the cleanup                                                                                             | `TestGrokLogoutCrashPointsNeverResendOrDeleteWithoutRecoveryState`, `TestGrokLogoutCleanupFailureIsFencedRetriedAndNeverRevokesTwice`, `TestGrokLogoutBeforeTheClaimRestartKeepsTheCredentialAndAllowsANewLogout`                                                                                             |
| Logout during execution, serialization and deletion                                                                      | Waits for the original lease; new execution is typed unavailable; one account serialized, others independent; deletion only after confirmed cleanup                                               | `TestGrokLogoutWaitsForTheOriginalExecutionLease`, `TestGrokLogoutSerializesWithOtherOperationsOnOneAccountOnly`, `TestGrokDeleteRequiresAConfirmedLogoutFirst`, `TestGrokDeleteIsBlockedByUnprovenAndUnconfirmedLogouts`, `TestGrokCleanupTreatsTheBundleAsProtected`, `TestGrokCleanupFailedLoginSelection` |
| Worker-lease logout, Worker home removal failure                                                                         | Revocation required; fenced on failure; same Worker retry succeeds; other Worker refused                                                                                                          | `TestGrokWorkerLogoutRequiresRevocationAndRetriesOnlyOnTheOriginalOwner`, `TestGrokWorkerLogoutWithoutRevocationFencesInsteadOfDeleting`, `TestGrokFencedRefreshLeaseIsNotRetryable`                                                                                                                          |
| Server refresh claim and lease                                                                                           | The claim is durable before the token request; the account stays exclusively leased; no other operation, Take or logout is admitted                                                               | `TestGrokRefreshWritesTheClaimBeforeTheTokenRequestAndHoldsTheLease`                                                                                                                                                                                                                                          |
| Provider refuses the refresh token (`invalid_grant`)                                                                     | Plain failure; bundle kept; health `expired`; exactly one request                                                                                                                                 | `TestGrokRefreshFailuresNeverReplaceTheBundle`                                                                                                                                                                                                                                                                |
| 5xx, truncated body, malformed success, unverifiable ID token or userinfo, changed identity                              | Bundle kept; `RECOVERY_REQUIRED`; claim retained; the token is never resent                                                                                                                       | `TestGrokRefreshFailuresNeverReplaceTheBundle`                                                                                                                                                                                                                                                                |
| Failure before the claim (for example discovery)                                                                         | Plain failure; no token request; the credential is refreshable by a later request                                                                                                                 | `TestGrokRefreshProviderFailureBeforeTheClaimSendsNothing`                                                                                                                                                                                                                                                    |
| Crash after the claim, after the response or after staging                                                               | `RECOVERY_REQUIRED` after restart; at most one request and none after the restart; the current generation unchanged                                                                               | `TestGrokRefreshFaultInjectionSendsOneRequestAndFencesUncertainOutcomes`                                                                                                                                                                                                                                      |
| Crash after the commit                                                                                                   | Refresh published; the superseded generation is removed by the next refresh                                                                                                                       | `TestGrokRefreshFaultInjectionSendsOneRequestAndFencesUncertainOutcomes`                                                                                                                                                                                                                                      |
| Restart before the claim                                                                                                 | Settles as a failed refresh; credential intact; health restored                                                                                                                                   | `TestGrokRefreshRestartBeforeTheClaimFreesTheOperation`                                                                                                                                                                                                                                                       |
| Removal of the superseded generation fails after the refresh commit                                                      | Refresh stays published and unfenced; the old generation awaits the next cleanup                                                                                                                  | `TestGrokRefreshCleanupFailureAfterThePublishDoesNotUndoIt`                                                                                                                                                                                                                                                   |
| Restore while a refresh is claimed, fenced or pending                                                                    | Restore refused; settled refresh admitted                                                                                                                                                         | `TestBackupRestoreBlocksAnUnsettledGrokRefresh`                                                                                                                                                                                                                                                               |
| Execute Take of the claimed job by a Runner Device with Worker 21 and a baseline installation                            | Exclusive singleton lease with the job as operation; the stored envelope is delivered once                                                                                                        | `TestGrokExecuteLeaseDeliversTheStoredEnvelopeAndSerializesTheAccount`                                                                                                                                                                                                                                        |
| Execute Take while the account holds any lease, or a login, refresh or logout while an execute lease is active           | `ResourceExhausted`; the waiting session retries with backoff; the vault is unchanged                                                                                                             | `TestGrokExecuteLeaseDeliversTheStoredEnvelopeAndSerializesTheAccount`, `TestGrokExecutionRetriesABusyAccountUntilTheLeaseIsFree`                                                                                                                                                                             |
| Two sessions on two different accounts                                                                                   | Independent leases and credentials; the first account stays exclusive                                                                                                                             | `TestGrokExecuteRunsDifferentAccountsInParallelAndSerializesOneAccount`                                                                                                                                                                                                                                       |
| Execute Take without the server switch, Worker 21, a baseline installation, the claimed Grok job or with a queued logout | Refused before any lease                                                                                                                                                                          | `TestGrokExecuteTakeIsRefusedUnlessGrokExecutionIsEnabled`, `TestGrokExecuteTakeRequiresTheExactClaimedGrokJobAndCapability`, `TestGrokSubscriptionExecutionIsRefusedUnlessWorker21WasNegotiated`                                                                                                             |
| Execute Finish with the credential unchanged, or rotated by the native process                                           | Unchanged keeps the generation; a rotation publishes the next generation under the same identity                                                                                                  | `TestGrokExecuteFinishWithUnchangedCredentialKeepsTheGeneration`, `TestGrokExecuteFinishPublishesANativeRotation`                                                                                                                                                                                             |
| Execute Finish with a foreign or regressed credential, or unconfirmed cleanup                                            | Refused and fenced under the current generation; nothing staged                                                                                                                                   | `TestGrokExecuteFinishRefusesAForeignOrRegressedCredential`, `TestGrokExecuteFinishFencesUnconfirmedCleanup`                                                                                                                                                                                                  |
| Execute Finish after a crash or an unreadable credential with confirmed cleanup                                          | Lease released under the current generation without a fence                                                                                                                                       | `TestGrokExecuteFinishReleasesWithoutAFenceWhenNothingUsableRemains`, `TestGrokExecutionCleansUpOnEveryExitPath`                                                                                                                                                                                              |
| Server restart or lost instance while an execute lease is held                                                           | Lease fenced; only the original Worker's credential-free, unsuccessful, cleanup-confirmed Finish releases it; the watch delivers it only to that Worker                                           | `TestGrokFencedExecuteLeaseIsReleasedOnlyByTheOriginalWorkersCleanup`, `TestGrokFencedLifecycleLeaseCannotUseExecuteRecovery`, `TestGrokWatchDeliversOnlyTheOriginalWorkersFencedExecuteLease`, `TestGrokWatchNeverPushesLifecycleWorkOrFencedRefreshLeases`                                                  |
| Worker recovery with a leftover `auth.json`, a lock it cannot remove, or a foreign lease                                 | Credential and lock retired and cleanup reported; otherwise nothing is reported                                                                                                                   | `TestGrokRecoveryRetiresTheLeftoverCredentialAndReportsCleanupOnly`, `TestGrokRecoveryNeverConfirmsCleanupItCannotProve`, `TestGrokRecoveryRefusesForeignOrNonExecuteLeases`                                                                                                                                  |
| Worker materialization after Take, retirement before Finish, unsafe home, unrun native                                   | `auth.json` 0600 only when the harness asks; both files gone when Finish is reported; an unsafe home receives nothing                                                                             | `TestGrokExecutionMaterializesOnlyAfterTakeAndRetiresBeforeFinish`, `TestGrokExecutionRefusesAHomeThatIsNotPrivateWithoutWritingAnything`, `TestGrokExecutionThatNeverMaterializedConfirmsNothingWasWritten`                                                                                                  |
| Native refresh rewrites `auth.json` and leaves `auth.json.lock`                                                          | Rotated credential sealed and reported with refresh and cleanup confirmed; both files removed                                                                                                     | `TestNativeRefreshFinishReturnsTheRotatedCredentialAndRetiresBothFiles`, `TestRetireGrokAuthFileRemovesTheNativeLockFile`                                                                                                                                                                                     |
| Native refresh with a foreign identity, an unreadable or truncated file, or a failed process                             | No bundle and no refresh confirmation; files retired; the server keeps the lease fenced                                                                                                           | `TestNativeRefreshFinishNeverConfirmsAMismatchedOrUnreadableCredential`, `TestGrokFinishReportsMapToExplicitOutcomes`                                                                                                                                                                                         |
| Native refresh with a lock or temporary that cannot be removed, or a swapped home                                        | No bundle, no cleanup confirmation; lease stays fenced                                                                                                                                            | `TestNativeRefreshFinishWithholdsEverythingWhenCleanupIsUnconfirmed`, `TestNativeRefreshFinishRefusesASwappedHome`                                                                                                                                                                                            |

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
`unsupported`; a recorded problem on a detected installation keeps its code; a
detected installation without a resolved path, without an observation time, or
whose observation time is later than the admission time plus one second of clock
skew, for example after the server clock moved backward, is `discovery` /
`unsupported`), a version below the subscription baseline or invalid
metadata is `version` (even when the probe stored a `profile` diagnostic for a
release between the discovery baseline `1.0.41` and the subscription baseline),
and only a discovered installation with a usable observation at or above the baseline restores its stored
diagnostic unchanged or, without one, reports `profile` with the protocol state's
code (`failed` is `unavailable`, otherwise `unsupported`); the admission time is
captured once and shared by the admission predicate and this classification
(`grokDiscoveryObserved`), so a refusal and its diagnostic always agree on a
future-dated observation. The refusal carries the diagnostic text and a
structured warning log. Login, refresh,
logout, models, execution and history producers wrap their failures with
`WithGrokDiagnostic` and the matching phase when they land.

`ServerSubscriptionOperation.GrokDiagnostic` (JSON `grok_diagnostic`) is valid only
on a Grok account's settled operation, never together with the Codex diagnostic,
and its correlation ID equals the operation ID. `GetSubscriptionProgress` for a
Grok account returns the original operation's state and `grok_diagnostic` (field 10) after the usual actor check; Codex field 7 and native field 9 stay unset,
older clients ignore field 10, and no URL or user code is returned.

Progress presentation: every Grok operation is presented by the one Grok view
above, so a Worker-owned operation and a server-owned login share their state,
canceled flag, diagnostic and expiry rules. A server-owned login additionally has
a memory-only presentation entry (never persisted, never present for a
Worker-owned operation): while the original login is waiting in this server epoch
the response carries the transient `url`, and after success, while the account
still references the committed generation, it carries that `generation` and the
`suggested_name`. A request from another or a revoked actor drops the entry.
Canceling a queued operation settles its existing server record in place (a
server-owned login always has one); only a Worker-owned operation without a
record gets a canceled record created.

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

| Action  | Reported outcome                                                    | Record state                        | Diagnostic (phase / code)                                             | Account fence                |
| ------- | ------------------------------------------------------------------- | ----------------------------------- | --------------------------------------------------------------------- | ---------------------------- |
| any     | success with `C` (refresh also with `R`)                            | `succeeded`                         | none                                                                  | no                           |
| any     | success without `C`                                                 | `failed`                            | `cleanup` / `recovery_required`                                       | yes                          |
| login   | failure with `C`                                                    | `failed`                            | `login` / `unavailable`                                               | no                           |
| login   | failure without `C`                                                 | `failed`                            | `login` / `unavailable`                                               | yes                          |
| login   | canceled with `C`                                                   | `canceled`                          | none                                                                  | no                           |
| login   | canceled without `C`                                                | `failed`                            | `cleanup` / `recovery_required`                                       | yes                          |
| refresh | failure, or success without `R`, with or without `C`                | `failed`                            | `refresh` / `unavailable`                                             | yes                          |
| logout  | failure, with or without `C`                                        | `failed`                            | `logout` / `unavailable`                                              | yes                          |
| logout  | removal of the revoked bundle fails (it runs before the settlement) | none yet; lease and generation stay | none (the operation has not settled; progress is `recovery-required`) | yes, the same Worker retries |
| other   | vault removal fails after `succeeded` / `canceled`                  | `failed`                            | `cleanup` / the removal's code                                        | yes                          |
| any     | vault removal fails after a native failure                          | `failed`                            | the native failure's diagnostic                                       | yes                          |

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
disposes. Both desktop readers apply this classification: the generic onboarding
in `subscription-login.tsx` and the Grok sign-in controller
(`grok-subscription-login.tsx`, through `grokPollEvent`). Inspect refreshes only
the diagnostics of the state on screen, and a different state is adopted by the
next poll. Grok progress carries no URL or
user code, so observing a claimed Grok operation never enters the browser path
(`drivesBrowserLogin`). Every Codex, Claude and Grok row is listed, with the Go
test that asserts the server half and the vitest case that asserts the client
half:

| Account record (server)                                   | Progress state                      | Provisional | Client disposition and polling                                                  | Go test (`internal/server`)                                                         | TypeScript test                                                                                                                                                           |
| --------------------------------------------------------- | ----------------------------------- | ----------- | ------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| queued Pending, no record                                 | `preparing`                         | no          | active: keep polling                                                            | `TestGrokProgressViewClassifiesEveryOperationState` (queued)                        | `subscription-progress.test.ts` (preparing)                                                                                                                               |
| claimed Pending, no record                                | `waiting`                           | no          | active: keep polling; no browser path for Grok                                  | same (claimed)                                                                      | `subscription-progress.test.ts` (waiting, service drives the browser)                                                                                                     |
| refused Take, original queued and uncanceled              | `unsupported` + `grok_diagnostic`   | yes         | provisional: keep polling, adopt the next response                              | same (take refused, past the record window)                                         | `subscription-progress.test.ts`; `subscription-login.test.tsx` and `grok-subscription-login.test.tsx` (provisional refusal then success, then failure, then cancellation) |
| refused Take, original claimed, canceled or gone          | `unsupported`, no diagnostic        | no          | terminal: stop polling, dispose                                                 | same (claimed, canceled flag, no pending)                                           | `subscription-progress.test.ts` (unsupported); `subscription-login.test.tsx` and `grok-subscription-login.test.tsx` (unsupported without a Grok diagnostic)               |
| settled `succeeded`                                       | `succeeded` + generation            | no          | success: verify, name the account, stop polling                                 | same (finished succeeded)                                                           | `subscription-progress.test.ts` (succeeded); `subscription-login.test.tsx` (provisional refusal then success)                                                             |
| settled `failed` (diagnostic)                             | `failed` + `grok_diagnostic`        | no          | terminal, even though it carries a Grok diagnostic                              | same (finished failed)                                                              | `subscription-progress.test.ts`, `subscription-login.test.tsx` and `grok-subscription-login.test.tsx` (failed with a Grok diagnostic)                                     |
| settled `canceled`                                        | `canceled`, never a diagnostic      | no          | terminal                                                                        | same (finished canceled, stale diagnostic)                                          | `subscription-login.test.tsx` (canceled)                                                                                                                                  |
| active record past its window                             | `expired`                           | no          | terminal                                                                        | same (active record after expiry)                                                   | `subscription-login.test.tsx` (expired)                                                                                                                                   |
| account recovery fence (any record, including a refusal)  | `recovery-required`                 | no          | terminal                                                                        | same (queued during recovery, settled under recovery, take refused during recovery) | `subscription-login.test.tsx` (recovery required)                                                                                                                         |
| any other operation ID, actor or no operation             | access denied                       | n/a         | not a response: the poll reports the original sign-in as unverified and retries | same (another operation, no operation, no subscription)                             | `subscription-login.test.tsx` (unverified status checks)                                                                                                                  |
| Codex or Claude `unsupported` (native diagnostic or none) | `unsupported`, no `grok_diagnostic` | no          | terminal                                                                        | n/a (not a Grok record)                                                             | `subscription-progress.test.ts`; `claude-server-login.test.tsx` (unsupported)                                                                                             |

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
ID; it is reused by the Grok browser sign-in controller
(`grok-subscription-login.tsx`) and by the Worker installation row
(`machine-settings.tsx`), which reads the stored `protocol.grok_diagnostic`. The
notice adds no login control; Settings enables Grok Add account only under
System capability 39.

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
correlation id; subscription execution records use phase `EXECUTION` and
recovery cleanup phase `CLEANUP` with account and lease identifiers. Diagnostics exclude credentials, URLs, paths, identity and raw native
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

Grok subscription execution is covered by `internal/harness/grok/subscription_test.go`
(the scripted ACP fixture completes a text turn after `cached_token`
authentication, typed `Unavailable` authenticate failures, credential preparation
order, the profile and environment contract and the initialize profile; the
existing `api_test.go` expectations only gained stricter API-lane assertions),
`internal/server/grok_execution_test.go` (the lease, Finish outcomes, parallel
accounts, recovery, watch, registration and capability gating) and
`internal/worker/grok_subscription_execution_test.go` (custody, every exit path,
recovery, logging and capability refusal). The orchestration inside
`executeGrokSession` is covered by these pieces and the compiler, not by an
end-to-end fixture run, and the opt-in real-account run (one prompt in a scratch
workspace with `DELIDEV_NATIVE_GROK_EXECUTABLE`) was not performed.

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

### Real-account acceptance runbook

`TestAccountAcceptance` in `cmds/delidev-cli/internal/harness/grok/account_native_test.go`
makes the actual-profile verification above repeatable. It is interactive by
design (the account owner approves each device-code login in their own browser),
never runs in CI and is skipped unless both gates are set:

| Variable                               | Meaning                                                                              |
| -------------------------------------- | ------------------------------------------------------------------------------------ |
| `DELIDEV_NATIVE_GROK_ACCOUNT=1`        | Opt in to the real-account test.                                                     |
| `DELIDEV_NATIVE_GROK_EXECUTABLE`       | Absolute path of the Grok Build CLI under test (for example `$HOME/.grok/bin/grok`). |
| `DELIDEV_NATIVE_GROK_APPROVAL_TIMEOUT` | Optional Go duration, `10s` to `30m`. Default: 5 minutes for the owner to approve.   |

Run it from an interactive terminal (the approval details are written only to
the controlling terminal, `/dev/tty` or `CONOUT$`; the run fails without one):

```sh
cd cmds/delidev-cli
DELIDEV_NATIVE_GROK_ACCOUNT=1 DELIDEV_NATIVE_GROK_EXECUTABLE="$HOME/.grok/bin/grok" \
  go test ./internal/harness/grok/ -run 'TestAccountAcceptance' -count=1 -v -timeout 45m
```

`-count=1` is mandatory. Go caches a passing result and would replay a cached
PASS without launching the CLI even after the Grok binary, its release or the
account changed; the test environment does not include the binary's contents.
Never record a run whose output ends in `(cached)`.

`-timeout` must cover the longest approval wait plus the other steps. The owner
may take up to 30 minutes (`DELIDEV_NATIVE_GROK_APPROVAL_TIMEOUT=30m`) and the
remaining steps need up to 7 minutes (6 minutes of step budgets plus a minute of
process-start slack), so the runbook uses `45m`. The test checks
`go test -timeout` first and fails before any login when the remaining time is
shorter than the approval wait plus 7 minutes; raise `-timeout` for such a run
instead of lowering the approval wait below what the owner needs.

Every native command and the scratch `git init` run under a context created for
that step alone, from `context.Background()`; no step inherits what an earlier
step left over. The budgets are:

| Step               | Command or context                                                                                      | Budget                                     |
| ------------------ | ------------------------------------------------------------------------------------------------------- | ------------------------------------------ |
| Setup              | `git init` of the scratch repository                                                                    | 30s (`Setup`)                              |
| Version            | `grok --version`                                                                                        | 30s (`Version`)                            |
| Login              | `grok login --device-auth`                                                                              | approval wait (5m default, 10s-30m)        |
| Models             | `grok models` while logged in                                                                           | 30s (`Models`)                             |
| ACP prompt turn    | `grok agent stdio` start, `initialize`, `authenticate`, `session/new`, `session/prompt`, reply settling | 3m (`Prompt`, one shared budget by design) |
| Logout             | `grok logout`                                                                                           | 30s (`Logout`)                             |
| Post-logout models | `grok models` after the logout                                                                          | 30s (`PostLogoutModels`)                   |
| Cleanup            | the cleanup `grok logout`                                                                               | 30s (`Cleanup`)                            |

The non-login budgets sum to 6 minutes, which `accountFixedBudget` (7 minutes)
must cover; `TestNativeAccountHarnessFixedBudgetCoversEveryStep` and
`TestNativeAccountHarnessEveryStepOwnsItsBudget` pin the sum and that each
command starts with its own deadline.

The test uses a fresh scratch tree (`HOME`, `GROK_HOME`, XDG and temp variables
all inside it, a scratch git repository as working directory) and the CLI with
`--no-auto-update` and `GROK_DISABLE_AUTOUPDATER=1`. Each native command runs
through `internal/process`. It then checks, in order:

1. `grok --version` prints `grok <semver> ...` at or above
   `domain.GrokSubscriptionBaselineVersion`; the version line is logged for the PR.
2. `grok login --device-auth` exits 0 after the owner approves. Expected output
   shape: an `https://` verification URL on an `x.ai` host and a user code
   `[A-Z0-9]{4}-[A-Z0-9]{4}`. The real CLI prints this prompt on stderr, so
   stdout and stderr both pass through one filter; every other line of either
   stream, including the identity line, is dropped. Only the URL and code lines
   are shown, and only on the terminal; the login fails if they cannot be
   written there. Test output carries just the fixed notice
   `waiting for account owner approval`. The scratch `auth.json` must exist.
   Short native commands (`--version`, `models`, `logout`) check stdout and
   stderr together without logging either. The ACP agent's stderr is discarded
   by design because it can carry account details.
3. `grok models` prints `You are logged in with ...`, a `Default model: <id>`
   line and at least one `grok-*` model entry.
4. Raw ACP over `grok agent stdio`: `initialize` advertises the `cached_token`
   authentication method, `authenticate` with `cached_token` succeeds,
   `session/new` returns a `sessionId`, and one `session/prompt` in the scratch
   repository finishes with `stopReason: "end_turn"`. The assistant text is
   collected from that session's `session/update` `agent_message_chunk` text
   updates and must be the single word `ok` (case and surrounding punctuation
   ignored). An empty turn, a reply for another session, non-message updates
   only, or any other text such as an authentication notice fails the step. The
   reply text is never logged, only its length on a mismatch. The agent may send
   the `session/prompt` response before its final `session/update` text frames,
   so the client keeps reading after the response (refusing any late agent
   request with `-32601`, as it does while waiting) and validates only once the
   target session's accumulated text has stopped changing for 2 seconds. With no
   text yet, it waits up to 20 seconds for the first frame; text that is still
   changing after 20 seconds fails with `ACP assistant reply did not stabilize`.
   Both bounds sit inside the 3-minute `Prompt` budget, and an agent exit ends
   the wait early. This mirrors the official xAI headless client example, which
   waits for the accumulated text length to stabilize after the prompt result.
   `TestNativeAccountHarnessPromptTurnValidation` (fake `grok`: update before the
   response, update after it, several late chunks, a late agent request, no
   update, a never-stabilizing stream) and `TestNativeAccountHarnessFrameOrderings`
   (in-process agent: a notification and an agent request before and after the
   response of each of `initialize`, `authenticate`, `session/new` and
   `session/prompt`, plus text, no-text, foreign-session, wrong-text and
   agent-exit rows) pin every ordering. The suite sends no `session/cancel`, and
   `logout` is a plain CLI command rather than a JSON-RPC pair.
5. `grok logout` exits 0, removes the scratch `auth.json`, and `grok models` then
   reports `not authenticated`.
6. The invoking user's real `auth.json` (`$GROK_HOME` or `~/.grok`) has the same
   SHA-256 before and after. The test only hashes it, never reads it for
   content, copies it, links it or prints the hash.

Cleanup always runs `grok logout` and removes the scratch directory, including
after a canceled or timed-out login, but only when every native process it
started is confirmed gone. If ownership cannot be confirmed for any native
process (including the ACP agent and the cleanup `grok logout`), the scratch tree
is retained, the run fails and the error names the retained path (never its
content); remove it manually after confirming no `grok` process still uses it.
A native process is confirmed gone when `internal/process` reports a clean stop
or, after it reports `recovery_required`, the immediately following owner
reconciliation (`ReconcileOwner`) succeeds: reconciliation is the proof, so a
recovery error that reconciliation resolves still fails that command's own
result but no longer retains the tree. The test records a suspicion per native
owner and retires it only through that owner's successful reconciliation:

| Site                  | Records a suspicion on                 | Retired by                                                 |
| --------------------- | -------------------------------------- | ---------------------------------------------------------- |
| Short command (`run`) | `Run` returns `recovery_required`      | the reconciliation `run` does next                         |
| Short command (`run`) | its reconciliation fails               | stands                                                     |
| ACP start             | `Start` returns `recovery_required`    | the reconciliation `startACP` does next (no handle exists) |
| ACP resume            | `Resume` returns `recovery_required`   | the stop path's reconciliation                             |
| ACP stop              | `Close` returns `recovery_required`    | the stop path's reconciliation                             |
| ACP stop              | `Wait` fails other than by exit status | the stop path's reconciliation                             |
| ACP stop              | its reconciliation fails               | stands                                                     |

`TestNativeAccountHarnessRunUncertaintyMatrix` and
`TestNativeAccountHarnessACPUncertaintyMatrix` script every row against both
reconciliation outcomes, and
`TestNativeAccountHarnessCleanupLogoutRecoveryIsReconciled` covers the cleanup
logout.
The cleanup logout fails the run only when the scratch credential file survives
or its ownership is uncertain; step 5 checks the explicit logout strictly. A failed check fails the test with a
message that names the check and never echoes native output, URLs, codes,
identity or key material. A failure on the current stable release keeps support
disabled until resolved.

Record the result in the pull request, not in repository evidence files: the
logged CLI version line (for example `grok 1.0.52 (...) [stable]`), the
operating system, and pass or fail for each step above. Fixture-only harness
tests (`TestNativeAccountHarness*`) use a fake `grok` script, need no account and
run in ordinary `go test`; they do not replace the real-account run.

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
