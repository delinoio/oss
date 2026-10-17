# DeliDev Grok Build subscription contract

## Current server-owned CLI login

The active production login uses the server's installed Grok Build CLI:
`grok --no-auto-update login --device-auth`. The server owns the process through
`internal/process`, without registering, selecting or dispatching a Worker.
Both desktop account-entry actions select the existing device-code presentation.
Browser-login RPC requests are unsupported; the historical browser and direct
Device OAuth implementations described below are retained compatibility/test
primitives and are not production login dispatch targets.

Discovery probes only Grok on the server. Each account acquires its permanent
`nativeprofile.PurposeAuth` profile. The private environment sets `GROK_HOME`
below that profile and disables auto-update; it does not inherit the user's
normal Grok home or provider credentials. Login does not require the ACP
execution profile. Missing or unsupported CLI installations return a safe error.
Login settlement preserves the original discovery/version phase, observed version
and safe failure code in the original operation diagnostic.

Before launching the CLI, the server requires the profile to contain no retained
credential and records a durable exchange claim. A bounded output filter combines
separate stdout/stderr lines and publishes only the existing closed xAI device
verification URL and matching user code. Codes and addresses stay in transient
operation progress, never logs, account documents or query caches. Other native
output is discarded. The CLI owns device authorization, polling and exchange.
Success requires exit zero, confirmed owned-process reconciliation and a new
valid unexpired `auth.json`, read through the bound-home validator. The server
seals that native file's existing bundle format into the original account and
Finish generation; no manually reconstructed OAuth-token mapping grants success.

Cancellation, timeout and shutdown cancel and join the owned native child.
Because Go cannot observe the CLI's token-send boundary, interruptions after the
native claim retain recovery rather than proving that no upstream token exists.
Restart never replays the login. Retained credentials remain for explicit logout
or account deletion and cannot be adopted as a fresh login. Profiles and their
ordinary native data remain until explicit deletion.

Refresh retains the server-owned grant and original-identity checks. The request
pins the native principal type/ID. Principal metadata may come from the access
JWT returned by the authenticated token endpoint; any present ID-token/userinfo
principal must also agree. Native Team profiles bind their team principal rather
than a person's OIDC subject, without requiring user-only scopes. This follows
[the official refresh implementation](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-login/src/oidc/refresh.rs)
and [principal extraction](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-login/src/oidc/protocol.rs). Logout first
preserves the existing confirmed provider-revocation guarantee, materializes the
current protected generation in the same server-owned profile and runs
`grok --no-auto-update logout`. Native closure and credential/lock-file cleanup
must succeed before logout is acknowledged. An uncertain cleanup retains recovery.
An explicit logout retry reconciles the preceding operation's exact native process
journals before admission can replace that operation. Missing or uncertain proof
keeps the original owner and refuses the retry.

System capability 39 advertises the server-owned CLI login. System 40 and
Worker 21 retain their independent default-off execution gates.
No new public RPC, schema allocation or database migration is required.

`TestGrokCLIServerAccountAcceptance` exercises the real Connect handlers,
installed CLI and OS vault with no registered Runner. It requires an interactive
terminal and explicit `DELIDEV_NATIVE_GROK_ACCOUNT=1`; run from `cmds/delidev-cli`:

```sh
DELIDEV_NATIVE_GROK_ACCOUNT=1 go test ./internal/server \
  -run '^TestGrokCLIServerAccountAcceptance$' -count=1 -v -timeout 12m
```

Only the validated transient approval pair is printed to the controlling terminal.
The account owner approves it. The test then verifies protected publication,
durable store/vault reopen, refresh and native logout. Fixture, native account,
OS and packaging evidence remain distinct; this run grants no execution support.

## Scope

This contract owns the Grok Build subscription lifecycle in `cmds/delidev-cli`,
its Connect interface and the desktop AI Subscription flow. System 39 advertises
the current server-owned CLI login; execution remains separately gated. The
implementation inventory includes the protocol identifiers below, the bound credential bundle library, the protected-vault
generations, the protected Take/Finish lease handlers on the server, the
Worker-side credential custody described under Storage and the historical server-owned
browser login described under [Server-owned browser login](#server-owned-browser-login),
including its remote callback forwarding, and the server-owned device-code login
described under [Server-owned device-code login](#server-owned-device-code-login),
the server-owned refresh described under [Server-owned refresh](#server-owned-refresh),
the server-owned logout and logout-before-delete described under
[Server-owned logout](#server-owned-logout) and the Worker execution-exit
credential protocol described under [Worker execution exit](#worker-execution-exit),
and managed execution of a Grok session on a subscription account described under
[Managed execution](#managed-execution) (server lease, Worker supervisor,
harness profile and recovery), behind independent default-off execution switches.
The current CLI login amendment above supersedes the historical direct OAuth
login descriptions below. System 39 advertises the current CLI login. System 40 and Worker 21 remain off
pending their separate execution acceptance.
The subscription profile declares `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) as its minimum SemVer baseline. `domain.GrokSubscriptionVersionAllowed` admits a valid SemVer at or above it (prereleases of the baseline are below it) and gates protected Take admission of a Grok lease and server CLI login discovery; no managed-execution advertisement is granted by the version alone. Newer releases are validated natively, not rejected by number. The API and history baseline stays `domain.GrokProtocolVersion` (`1.0.41`); Grok Build 1.0.52 inspection is accepted when its optional `experiments` inventory is an empty array (see `cmds-delidev-harness-contract.md`), which does not change these baselines. Reservation support is separate
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

System 39 is advertised by GetStatus for the current server-owned CLI login.
The execution allocations remain declared but unadvertised: GetStatus and
AttachWorker never list System 40 or Worker 21,
the Worker does not report 21 and the server rejects it as an unknown
capability. `GetSubscriptionProgress` populates `grok_diagnostic` only for a
Grok account's original operation (see Grok diagnostics below); execution capability activation remains separate from login:

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
honors RFC 8628 pending, slow-down, denial and expiry. The desktop opens the
device verification page only through the closed native
`grok-device-authorization-open` action (fixed `https://accounts.x.ai/oauth2/device`
with at most one `user_code` query value), which uses the system default browser
and never the General/Local link destinations. Without a `user_code` the URL has
no query at all: an empty `?` or `#` delimiter is rejected. The renderer
predicates (`grokLoginURL`, `grokDeviceVerification`) and the native validators
(`validate_grok_subscription_authorization`, `validate_grok_device_authorization`)
run the shared case table `apps/delidev/src/grok-url-validation-fixtures.json`
in both TypeScript and Rust tests, so the two layers cannot disagree; the
renderer additionally requires the URL's `user_code` to equal the displayed code.

The trusted native receiver (`apps/delidev/src-tauri/src/oauth.rs`) exposes
`grok-subscription-open` and `grok-subscription-reopen` for the server-returned
browser URL. It validates the fixed `https://auth.x.ai/oauth2/authorize` shape
(public client `b1a00492-073a-47ea-816f-4c329264a828`, S256 PKCE, nonempty
state and nonce, redirect exactly `http://127.0.0.1:<port>/callback`), opens it
once. A local desktop binds nothing because the server listener receives the
redirect. A remote desktop is refused explicitly (`Incompatible`) until the
server advertises the flow: `ForwardSubscriptionCallback` is service-aware (the
ChatGPT/Codex login keeps `auth.openai.com` and fixed port 1457 byte for byte;
a Grok account relays to the dynamic IPv4 loopback port and state bound for that
very login). The receiver binds that IPv4 loopback port, accepts one
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

### Server-owned browser login

`RequestSubscription` for a Grok account with action LOGIN, `device_code` false
and no machine ID creates the server-owned operation (`ServerOperation` plus a
`Pending` operation without a Runner Device, 15-minute expiry). Any request that names a machine stays `unsupported`; refresh has its own lane under [Server-owned refresh](#server-owned-refresh) and logout under [Server-owned logout](#server-owned-logout). LOGIN with `device_code` true takes the device-code lane
described under [Server-owned device-code login](#server-owned-device-code-login). The scanner (`runServerSubscriptions`) claims the operation in
the current server epoch and starts exactly one runner (`grok_login.go`); a Grok
account never reaches the Codex runner.

The OAuth client is `internal/subscription/grok_oauth.go`. It owns the fixed
profile (issuer `https://auth.x.ai`, public client
`b1a00492-073a-47ea-816f-4c329264a828`, the observed scope list in order,
referrer `grok-build`), OIDC discovery validated against the issuer (exact
issuer; every endpoint https on the issuer origin; PKCE method list exactly
`S256`; token endpoint auth method `none`; ID token algorithm ES256), PKCE
S256/state/nonce generation (43 characters from `[A-Za-z0-9_-]`), the
public-client token exchange (no client secret, no client authentication header)
and ID-token validation (ES256 only, key selected by `kid` from the discovery
`jwks_uri`, issuer, audience exactly the client id with no additional audience,
an `azp` claim equal to the client id whenever present, nonce, expiry). Its pieces
are issue-neutral so device login, refresh and logout reuse them.

Lifecycle of one runner:

1. Discovery, then a listener bound with `net.Listen("tcp4", "127.0.0.1:0")`.
   The authorization URL is built in the observed shape and parameter order
   (`response_type`, `client_id`, `redirect_uri=http://127.0.0.1:PORT/callback`,
   `scope` with `%20`, `code_challenge`, `code_challenge_method=S256`, `state`,
   `nonce`, `referrer`) and is published only through the in-memory progress map:
   `GetSubscriptionProgress` serves it in `url` while the operation is WAITING in
   the current epoch and before the 15-minute deadline. It is never stored in a
   document, log, event or error.
2. The listener accepts exactly one callback: path `/callback`, `Host` equal to
   `127.0.0.1:PORT`, peer `127.0.0.1`. A request outside that path, host or peer
   is refused and ignored without consuming the slot. The first request that
   passes is the one callback: a wrong or missing `state`, a provider `error`
   (`access_denied` included) or a missing `code` ends the login as FAILED with
   no token request; a second callback is refused with no action.
3. The signing keys are read before the claim, so a keys failure cannot consume
   the code. The durable exchange claim (`ServerSubscriptionOperation.exchange_claimed`)
   is then committed, and only after that commit the single token request is
   sent. The claim is never cleared or rewritten.
4. A definitive answer (an RFC 6749 section 5.2 OAuth error response, invalid
   ID token, userinfo or mapping failure) settles FAILED with a LOGIN
   `GrokDiagnostic` and the operation id as correlation id. Only an explicit
   OAuth error response proves the code was not consumed: HTTP 400 or 401 whose
   JSON body carries a well-formed `error` code. Any other 4xx answer (408, 429,
   an HTML 403 from an intermediary, or a 400/401 without a structured error
   body) may have followed a received exchange and is treated as an
   uncertain outcome. A transport error, timeout, redirect, 5xx, a success status
   other than 200, a 200 whose token response is truncated, malformed or
   incomplete, or any interruption after the claim (cancellation, expiry, server
   shutdown, restart) settles RECOVERY_REQUIRED: the account stays fenced and the
   code is never sent again.
   Before the claim, cancellation settles CANCELED and the deadline EXPIRED.
   Only an explicit denial is a cancellation: a canceled pending operation, a
   pending mismatch, or an initiator whose device record is missing, revoked or
   no longer matches the actor. An operational storage read or decode failure,
   including a failed read or decode of the initiator's device record, is never
   a denial. The cancellation watcher, the queued-login claim, the WAITING
   publication, the exchange claim and the settlement transaction all apply this
   split through one actor check. A watcher read failure before the claim stops
   the flow, sends no token request, closes the loopback listener and settles
   FAILED with a LOGIN
   `GrokDiagnostic` carrying the sanitized storage error code and the operation
   id as correlation id (structured log `grok_login_watcher_read_failed` with
   the code only). The same failure while claiming the queued login settles it
   FAILED without starting it; while publishing WAITING or claiming the exchange
   it fails the flow with that code instead of a refused claim; at settlement it
   leaves the stored outcome as is, never rewrites it to CANCELED and never
   publishes credentials (log `grok_login_actor_read_failed`, code only). A
   durable cancellation or a really revoked initiator at settlement still wins
   as CANCELED, an elapsed deadline stays EXPIRED, and a failed settlement keeps
   the recovery fence. The device-code login and the native Grok CLI login share
   this behavior.
5. Success follows the Finish-equivalent server settlement: the sealed
   `GrokBundle` envelope is staged in the protected vault as the new
   `AccountLogin` generation, the keyed identity commitment is checked for
   duplicates (`uniqueSubscriptionIdentity`, same rules as the Take/Finish path;
   only a confirmed `CONFLICT` is a duplicate and settles FAILED, while any other
   scan failure propagates and settles RECOVERY_REQUIRED because the exchange
   already completed), and one transaction publishes the generation, commitment and connection. A
   cancellation, a revoked initiator, an elapsed operation deadline or a server
   shutdown that appears after the claim wins as RECOVERY_REQUIRED and the
   staged generation is retired. The publication decision is made inside the
   settlement transaction from the state at commit time, so a flow that decided
   success just before the deadline or shutdown never publishes. A successful
   login retains earlier generations of the account (`server_credential_retained`)
   instead of removing them; only the published generation authorizes use.
6. The listener is closed and its goroutine joined on every exit path before the
   outcome settles. The PKCE verifier is overwritten. A server restart marks an
   active operation recovery (`initializeServerSubscriptions`) and never binds a
   listener again.

`CancelSubscription` routes Grok accounts to the Grok branch: a queued login is
canceled at once, a running login is flagged and its runner stopped promptly.
`ForwardSubscriptionCallback` accepts, for a Grok account, only the original
operation while WAITING and before the claim, with `code`, `state` and `scope`
keys and the exact `state` of the published URL; the server relays the query to
the loopback listener of that login once.

Structured `slog` records of the flow use `phase=LOGIN` and the operation id as
`correlation_id` (`grok_login_started`, `grok_login_waiting`,
`grok_login_exchange_claimed`, `grok_login_listener_closed`,
`grok_login_finished`, ...). They never contain the URL, state, nonce, verifier,
code or a token.

Field mapping assumption (unverified): the real source of each `auth.json` member
was not captured from a real account. `subscription.BuildGrokBundle` is the one
function that maps verified material to the native entry and is pinned by tests:
`key` and `refresh_token` are the access and refresh tokens, `create_time` is the
settlement time and `expires_at` adds `expires_in`, `user_id` is the verified ID
token subject (userinfo must agree), `principal_type`, `principal_id` and
`team_id` come from userinfo with the same-named verified ID-token claim as
fallback (a missing principal fails closed), `email` and `first_name` come from
userinfo or the ID token. Whether the provider accepts an arbitrary loopback port
for this client is likewise unverified. Both need the opt-in real-account run;
until it is recorded the single switch `grokSubscriptionLoginAdvertised`
(`server_status.go`) was kept false for this historical direct OAuth flow.
The current server-owned CLI amendment activates System 39 independently. Tests use the in-process fake
authorization server `internal/grokidptest` (discovery, authorize, token, JWKS and
userinfo for the real issuer host, without network access).

### Server-owned device-code login

`RequestSubscription` for a Grok account with action LOGIN, `device_code` true
and no machine ID creates the same server-owned operation as the browser login;
the choice is stored on the `Pending` operation and the scanner starts the
device runner (`grok_device_login.go`) in place of the browser runner. A login
never switches method: a device login that fails never opens a listener or
publishes an authorization URL, and a browser login never requests a device code.
Replaying the same mutation id returns the original operation. Any
request that names a machine stays `unsupported`; logout has its own lane under
[Server-owned logout](#server-owned-logout). Capability 39 stays
under the single shared switch `grokSubscriptionLoginAdvertised`
(`server_status.go`); the current CLI amendment activates this capability.
No separate device capability exists.

The client is `internal/subscription/grok_device.go` (RFC 8628) on top of the
profile, discovery, token parsing and bundle mapping of `grok_oauth.go`.
Discovery additionally reads `device_authorization_endpoint` (https on the issuer
origin) and requires the `urn:ietf:params:oauth:grant-type:device_code` entry of
`grant_types_supported`; when either is missing the browser login is unaffected
and the device login fails with `UNAVAILABLE`.

Lifecycle of one device runner:

1. Discovery and the signing keys are read first, before any device code
   exists. The runner POSTs `client_id` (the browser-flow public client) and
   `scope` (the browser-flow scope list, space separated) as a form to the
   discovered `device_authorization_endpoint`, without client authentication.
2. The answer must carry `device_code`, `user_code` matching `[A-Z0-9-]{4,32}`, a
   verification address, a positive `expires_in` of at most 24 hours and an
   optional `interval` (default 5 seconds, at most 5 minutes; a larger value fails
   closed). The verification address must be exactly the fixed
   `https://accounts.x.ai/oauth2/device`, either plain or as the complete form
   carrying a single `user_code` equal to the answer's code; the complete form is
   preferred, anything else is refused and the login fails.
3. The runner moves the operation to WAITING and publishes the address and user
   code only through the in-memory progress map. `GetSubscriptionProgress` serves
   them as `url` and `user_code` while the operation is WAITING in the current
   epoch and before its 15-minute deadline, to the initiating principal only. The
   device code, user code and address are never stored in a document, logged,
   put in an event or returned in an error; `ForwardSubscriptionCallback` refuses
   a device login.
4. Polling sends `grant_type=urn:ietf:params:oauth:grant-type:device_code`,
   `client_id` and `device_code` to the token endpoint, first one interval after
   the presentation and then every interval, never faster. `authorization_pending`
   continues; `slow_down` adds 5 seconds to the interval for all later polls, also above
   the 5-minute cap that applies to the interval the provider demands in its
   authorization answer;
   `access_denied` settles FAILED with `PERMISSION_DENIED` (the denied terminal
   state); `expired_token` and the local deadline settle EXPIRED. The local
   deadline is `expires_in` from the answer capped by the operation's 15-minute
   expiry, and no request is sent at or after it: the deadline is rechecked after
   the claim commit (a claim that outlived it is cleared again without sending)
   and a success answered at or after it is not accepted but stays uncertain with
   its claim. Any other
   answer settles FAILED only as an RFC 6749 section 5.2 error response (HTTP 400
   or 401 with a JSON `error` code); an unstructured 4xx answer (408, 429, an HTML 403) stays uncertain and keeps the claim. The poll loop takes an injectable clock (`GrokDeviceClock`) so tests
   run on virtual time.
5. Every token request may consume the device code, so the durable exchange claim
   (`exchange_claimed`) is committed before each request. A definitive answer that
   proves the code was not consumed (pending, slow-down, denial, expiry or a structured
   OAuth error) clears the claim again before the next wait; the claim of a success is
   never cleared. An unanswered or unusable request (transport error, timeout,
   redirect, 5xx, a success status other than 200, a 200 whose token response is
   truncated, malformed or incomplete, cancellation or shutdown while in flight)
   leaves the claim standing, so the operation settles RECOVERY_REQUIRED and the
   code is never sent again. A
   restart marks every active operation of an older epoch RECOVERY_REQUIRED
   (`initializeServerSubscriptions`) and the device code, which exists only in the
   runner's memory, is gone. If clearing the claim fails the runner stops with
   recovery for the same reason.
6. Cancellation is distinct at each phase: while queued, during the device
   authorization request or between two polls (no claim stands) it settles
   CANCELED with no credential; while a token request is in flight it is
   RECOVERY_REQUIRED, exactly like a cancellation after the browser exchange claim.
7. The first success keeps its claim and builds the bundle through
   `subscription.CompleteGrokLogin`, the one function both methods call: ID token
   validation (ES256, `kid`, issuer, audience, expiry), the userinfo subject
   check and `BuildGrokBundle`. The device grant sends no nonce, so the device
   method skips only the nonce comparison (`VerifyGrokDeviceIDToken`). Settlement
   is the browser login's Finish-equivalent transaction (sealed bundle, identity
   uniqueness, generation and commitment). Equal tokens, identity and time
   produce equal bundles for both methods.

Structured `slog` records use `phase=LOGIN`, the operation id as
`correlation_id` and `method` (`browser` or `device_code`):
`grok_login_started`, `grok_login_waiting` (interval and lifetime in
milliseconds), `grok_login_device_polled` (poll number, closed outcome and
interval), `grok_login_claim_refused`, `grok_login_claim_release_failed` and
`grok_login_finished`. They never contain the device code, user code,
verification address, a token or the authorization URL.

Unverified assumptions (no real device grant was captured; the opt-in
real-account run records them): the device request carries exactly the browser
profile's client id and scope list; the answer uses the RFC 8628 members with the
fixed verification page and an uppercase user code; the pending, slow-down,
denial and expiry answers are HTTP 4xx documents with the RFC `error` values;
the provider adds no nonce to a device-grant ID token; the provider honors the
5-second default and the interval it returns. The constants live in
`grok_device.go` (`GrokDeviceDefaultInterval`, `GrokDeviceSlowDownStep`,
`GrokDeviceMaxInterval`). Tests use `internal/grokidptest`, which now also serves
the device authorization endpoint, scripted device_code answers
(`DeviceScript`) and a virtual `Clock`; the requirement set is covered by
`grok_device_test.go` (client) and `grok_device_login_test.go` (server).

### Server-owned refresh

`RequestSubscription` for a connected Grok account with action REFRESH, no
`device_code` and no machine ID creates the server-owned operation (`ServerOperation`
with action `refresh` plus a `Pending` operation without a Runner Device,
15-minute expiry). A refresh of an unconnected account, a refresh that names a
Runner Device, a refresh with `device_code` and any request while another
operation, a lease (including an execution lease, which has no pending
operation) or a recovery fence holds the account are refused at admission with
`CONFLICT`; a refresh is never queued behind a lease. The desktop
Refresh login control of a connected Grok account (which follows System
capability 39 alone) already sends exactly this request, so no desktop change is
needed. A refresh is never cancelable (`CancelSubscription` refuses it): it
finishes under exclusive ownership. The scanner (`runServerSubscriptions`) claims
the operation in the current server epoch and starts exactly one runner
(`internal/server/grok_refresh.go`); a Grok refresh never reaches the Codex
runner and never creates a `SubscriptionLease`.

The exclusive account lease of a server-owned operation is its claimed
`ServerOperation` (`native_started` with the `Pending` phase `claimed`), exactly
as for the server-owned login: a Worker Take of the account is refused while it
is held, and the scanner does not start a refresh while a Worker lease or a
recovery fence exists. The refresh client is `internal/subscription/grok_refresh.go`
on top of the profile, discovery, bounded round trip and bundle library of
`grok_oauth.go`.

Lifecycle of one runner:

1. The claim moves the operation to `claimed` and freezes the current generation
   (`ServerOperation.generation`). A stopped, expired or revoked initiator settles
   `canceled` or `expired` and sends nothing.
2. Discovery and the signing keys are read first, then the bound bundle is opened
   from the protected vault against this server, the account and the frozen
   generation. A failure here sends nothing: it settles FAILED (an unusable
   provider) or RECOVERY_REQUIRED (an invalid bundle, or a referenced generation
   whose vault object is missing, which is a local credential loss rather than a
   provider failure), and the account keeps its credential.
3. The durable refresh claim (`ServerSubscriptionOperation.exchange_claimed`,
   shared with the login's exchange claim and valid for `login` and `refresh`) is
   committed. Only after that commit the single request is sent: a form POST of
   `grant_type=refresh_token`, the public `client_id` and the bound
   `refresh_token` to the discovered token endpoint, with no client secret and no
   authentication header. The claim is never cleared or rewritten.
4. An RFC 6749 section 5.2 error response (HTTP 400 or 401 with a JSON `error`
   code) is a definitive rejection that did not consume the token: `invalid_grant`
   and every other structured error settle FAILED with a REFRESH
   `GrokDiagnostic` (`UNAUTHENTICATED`), the stored bundle untouched and the
   account usable, so a new explicit request may try again. Everything else after
   the claim is uncertain and settles RECOVERY_REQUIRED: a transport error, a
   timeout, an oversized, truncated or malformed body, a 5xx answer, any other 4xx
   answer (408, 429, an HTML 403 from an intermediary or an unstructured body), a
   non-200 success status, an interruption (shutdown, deadline) and a restart. The old
   token is never sent again, because a rotated token the server did not record
   is unrecoverable; the account stays fenced and the user must sign in again.
5. A 200 answer carries `access_token`, `token_type` `Bearer` and a positive
   `expires_in` (at most 30 days); `refresh_token` and `id_token` are optional.
   The identity must equal the bound identity (`CompleteGrokRefresh`): an ID
   token, when present, must verify (ES256 signature by `kid`, issuer, audience,
   expiry; no nonce exists for a refresh) and carry the bound subject, and the
   userinfo read with the new access token must carry the bound subject, principal
   type and principal ID. The verification of a received answer is not stopped by
   a shutdown or a passed deadline, since the answer may hold the only copy of a
   rotated token. Any failure here (identity mismatch, invalid ID token, userinfo
   failure) is RECOVERY_REQUIRED and the bundle is not replaced.
6. The successor keeps every other member of the bound `auth.json` entry, takes
   the new access token as `key`, takes the new `refresh_token` when the provider
   rotated it and keeps the bound one otherwise, and moves `create_time` to now
   and `expires_at` to now plus `expires_in`. `subscription.GrokRefreshed` must
   accept it (same identity key, later expiry, changed key or refresh token);
   otherwise it is RECOVERY_REQUIRED.
7. Success follows the Finish-equivalent server settlement
   (`finishGrokRefresh`): the keyed identity commitment must equal the stored one
   and the account must still reference the frozen generation; the sealed
   `GrokBundle` envelope is staged as a new `AccountLogin` generation in the
   protected vault and one transaction publishes it. The superseded generation is
   retained, not removed: only the published generation authorizes use, and only
   explicit logout or account deletion removes the rest (see
   [Server credential retention and explicit removal](cmds-delidev-subscription-contract.md#server-credential-retention-and-explicit-removal)).
   A cancellation of the initiator after the claim does not discard the answer. A restart
   marks an active operation recovery (`initializeServerSubscriptions`) and never
   starts a refresh again.

Fault injection (`Service.grokRefreshCrash`, tests only) simulates a process
death at three stages. A crash after the claim sends at most one request (none in
the tested stage) and the restart settles RECOVERY_REQUIRED; a crash after the
response leaves exactly one request and the restart settles RECOVERY_REQUIRED with
the old bundle; a crash after the persist leaves exactly one request and a
published new generation, which needs no recovery (the superseded generation
stays vault-owned and non-authoritative until explicit logout or deletion removes
it). A shutdown while
the request is in flight is covered the same way and also leaves exactly one
request.

Structured `slog` records use `phase=REFRESH` and the operation id as
`correlation_id`: `grok_refresh_started`, `grok_refresh_claimed`,
`grok_refresh_claim_refused`, `grok_refresh_bundle_unavailable`,
`grok_refresh_rejected`, `grok_refresh_outcome_uncertain`,
`grok_refresh_verification_failed`, `server_credential_retained` and
`grok_refresh_finished` (closed state and reason values only). The acceptance of a
login or refresh logs `grok_operation_accepted` with the phase. They never contain
a token, the refresh request, a path or an identity field, and no event or error
carries one.

Unverified assumptions (no real refresh was captured; the opt-in real-account
run records them, and this change records no real-world validation): the IdP may
or may not rotate the refresh token, and the implementation accepts both; the
answer uses the standard token members; a rejected or revoked token is answered
with an HTTP 4xx `invalid_grant` document; and the refresh answer may omit the ID
token, in which case userinfo alone proves the identity. Tests use
`internal/grokidptest`, which now also serves the refresh_token grant
(rotation, omitted ID token, scripted failures); the requirement set is covered by
`grok_refresh_test.go` in `internal/subscription` (client) and `internal/server`
(runner, fault injection and restart).

### Server-owned logout

`RequestSubscription` for a connected Grok account with action LOGOUT, no
`device_code` and no machine ID creates the server-owned operation
(`ServerOperation` with action `logout` plus a `Pending` operation without a
Runner Device). The acceptance window is 30 days, not the 15-minute login window,
because a logout may wait for the original execution for as long as it runs. A
logout of an unconnected account, a logout that names a Runner Device and a
logout with `device_code` are refused. Acceptance marks the account logging out:
its health becomes `revoked`, queued executions and titles of the account are
canceled, and `grokAccountLoggingOut` is true for as long as the logout holds the
account (queued, running or recovering). Every new Worker Take of the account is
refused with a typed `unavailable` problem ("The Grok account is logging out");
new Grok execution must use the same refusal (`grokLoggingOutProblem`) when it
lands. A logout is never cancelable (`CancelSubscription` refuses it).

The exclusive account lease of the logout is its claimed `ServerOperation`
(`native_started` with the `Pending` phase `claimed`), exactly as for the refresh:
it never coexists with a Worker lease. The scanner (`runServerSubscriptions`)
starts the runner (`internal/server/grok_logout.go`) only while no Worker lease
exists on the account, so the logout waits for the original execution exit: a
lease whose owner has not yet reported a confirmed Finish keeps the logout queued,
and nothing is sent. The revocation client is `internal/subscription/grok_revoke.go`
on top of the profile, discovery and bounded round trip of `grok_oauth.go`;
discovery additionally reads the optional `revocation_endpoint` (https on the
issuer origin, otherwise ignored).

Lifecycle of one runner:

1. The claim moves the operation to `claimed` and freezes the current generation
   (`ServerOperation.generation`). An unavailable initiator does not stop it:
   the logout was accepted and only protects the credential. A logout whose
   30-day acceptance window elapsed while it waited for the original execution
   exit is never claimed: clients were already told it expired
   (`GetSubscriptionProgress` reports EXPIRED), so the claim settles it as
   `expired`, clears the pending operation, sends nothing and returns the account
   to `ready` with its credential and generation intact.
2. The bound bundle is opened from the protected vault against this server, the
   account and the frozen generation, then discovery is read. Any failure here
   sends nothing: it settles FAILED (`logout` / `unavailable`; unusable discovery
   or no revocation endpoint) with the credential stored and the account `ready`
   again, so a new explicit logout may try again, or RECOVERY_REQUIRED (an invalid
   stored bundle).
3. The durable revocation claim (`ServerSubscriptionOperation.exchange_claimed`,
   which for a logout records that the request was attempted) is committed. Only
   after that commit the single request is sent: a form POST of `token` (the bound
   refresh token), `token_type_hint=refresh_token` and the public `client_id` to
   the discovered revocation endpoint, with no client secret and no authentication
   header.
4. Outcomes: a 2xx answer is `revoked`; the provider answers `invalid_token` or
   `invalid_grant` (HTTP 400 or 401) for an already unusable token, which counts as
   revoked (`already_invalid`); every other 4xx answer is a definite rejection and
   every other result (transport error, timeout, 3xx, 5xx, truncated or oversized
   body, shutdown or deadline) is uncertain. Both of the latter settle
   RECOVERY_REQUIRED (`logout` / `recovery_required`): the account is fenced
   (`failed`), the bundle stays stored and the revocation is not repeated by a
   restart. The credential is never deleted without a confirmed revocation, and a
   failed revocation never deletes it without the recovery state.
5. After a confirmed revocation one transaction publishes the disconnected account
   (no connection, generation, identity commitment or owner machine). It requires
   that the account still references the frozen generation and that no Worker lease
   or member exists: a Worker lease is released only after its owner confirmed
   that its private home holds neither `auth.json` nor `auth.json.lock`, so the
   absence of a lease is the confirmation that every Worker copy was removed.
   Otherwise the logout settles RECOVERY_REQUIRED. Because the protected vault
   generation still exists at that commit, the same transaction already persists
   the retained logout-cleanup recovery shape (health `failed`, RECOVERY_REQUIRED,
   the original operation with a `cleanup` / `recovery_required` diagnostic and
   its queued placeholder). A process death between the publication and the vault
   removal therefore leaves a recoverable shape that explicit account deletion
   consumes, never a disconnected `succeeded` account whose credential reference
   blocks deletion.
6. Only after the publication are the protected vault generations removed by the
   explicit removal (`removeAccountCredentials`: every generation, no count
   limit, owner and purpose validated first, cancellation observed per item). A confirmed
   removal settles the account in a second transaction: health `disconnected`,
   the operation `succeeded`, the cleanup diagnostic and the recovery fence
   cleared, so a client may observe RECOVERY_REQUIRED for the short interval
   between the two transactions. A failed removal, or a failure to record the
   settlement, keeps the persisted shape exactly as the ChatGPT server logout does;
   `serverLogoutRecoveryOwner` accepts Grok accounts, and explicit account
   deletion consumes the shape (it removes the references, never sends another
   revocation and then deletes).

A restart marks an active logout recovery (`initializeServerSubscriptions`) and
never claims it again. The original actor may retry a logout that ended
RECOVERY_REQUIRED with the credential still stored (rejected, uncertain or
interrupted revocation) by sending a new LOGOUT request: the retry replaces the
unfinished operation and sends the idempotent revocation again. When the
original actor is a client device that was revoked, the owner takes over that
retry. Another actor, a Worker lease or any other recovery fence is refused.
Revoking the client device that queued a logout behind a Worker lease does not
cancel it (`cancelQueuedSubscriptionInitiator` skips a queued server-owned Grok
logout): the logout owes the provider a revocation that needs no initiator, so
it stays queued and runs after the original execution exits.

Fault injection (`Service.grokLogoutCrash`, tests only) simulates a process death
after the revocation claim, after the confirmed revocation and after the
publication. The first two leave the claim on disk and the bundle stored, the
restart settles RECOVERY_REQUIRED and no revocation is repeated; the third leaves
a disconnected account whose unreferenced generation the next explicit cleanup
removes.

#### Original-owner cleanup retry

A Worker lease whose Finish reports unconfirmed private cleanup (a credential
file, its temporaries or `auth.json.lock` could not be proven removed) keeps the
lease, the generation and the account recovery fence, and marks the lease
`cleanup_unconfirmed`. Only the original lease owner (the same Runner Device,
Worker device, instance, epoch and lease revision, as for every Finish) may report
the cleanup again, with a fresh request identity because the first Finish
committed under the old one; any other Worker device, instance or lease revision
is refused and the lease is untouched. The Worker keeps the home it bound at
materialization and the original credential on the lease
(`retryGrokCleanup`), so the retry removes the credential only in that directory.
The first report freezes its native outcome on the lease (`first_finish`:
`succeeded` and `refresh_confirmed`): a retry only confirms the cleanup and can
keep or lower each flag but never raise it, so a reported native failure cannot
become a success, publish a bundle or clear the connection through the retry. A
lease that carries no frozen outcome (written before the field existed) is
treated as having reported a failure. A confirmed retry releases the lease and
lifts the fence that its own report set; a recovery fence set for any other reason
cannot be finished this way. The recorded outcome of the operation stays the first
failure. The server-owned logout
waits for such a lease to be released.

#### Account deletion

Deleting a connected Grok account performs the logout first
(`logoutGrokBeforeDelete`, in `DeleteConfiguration`): when the account has a
connection or generation and no other operation or recovery fence, the server
first checks the non-credential references (`validateDeletionReferences`: projects,
agents, models, accounts and retained sessions) and refuses a referenced account
as `conflict` before anything is revoked or removed, then the server
accepts a logout whose operation id is derived from the deletion request id
(`grokDeleteLogoutID`), runs it, waits for its outcome (bounded by the request)
and deletes the account at the revision the logout produced. The deletion
receipt keeps the revision the caller sent, so a retry of the same request after
the logout already advanced the account (the process stopped before the receipt,
or a later deletion step failed) recognizes its own logout by that id, continues
at the current revision and sends no second revocation; a logout the request did
not run is never adopted. A
Worker lease that has not exited keeps the logout queued and the deletion fails
as `unavailable` (the account stays leased and logging out); a failed logout is
`unavailable`; a rejected or uncertain revocation is `recovery_required` and keeps
the connected account and its bundle; an unconfirmed credential removal keeps the
retained logout-cleanup shape, which the next explicit deletion finishes. The
cleanup child settles that shape when the removal is finally confirmed: the
operation becomes `succeeded` and its cleanup `GrokDiagnostic` is cleared, so a
deletion that a reference then blocks leaves a valid disconnected account.
Deletion never succeeds without a confirmed revocation and confirmed removal.
Accounts that are already disconnected, or that were never connected, use the
ordinary deletion path and send no revocation.

#### Protected credential treatment

Failed-login cleanup (capability 41) never selects a Grok account that holds a
generation or a connection, and a Grok login fenced for recovery is not adopted
by the Codex failed-login cleanup (`failedServerLoginOwner` stays ChatGPT-only;
a failed Grok login stores no bundle, because a staged generation is retired
when its publication fails). A disconnected Grok account is a batch cleanup
target only when the vault holds no protected reference of another purpose
(`protectedAccountDeletion`); its retained `account-login` generations are removed
by the explicit deletion. The retained logout-cleanup shape is never created
by the batch. Backup images carry no bundle material and restore quarantines the
account (see [Grok backup and restore](#grok-backup-and-restore)). Diagnostics
(`doctor`) never read a Grok account's protected storage in any state.

Structured `slog` records use `phase=LOGOUT` for the logout and `phase=CLEANUP`
for the credential removal and the original-owner cleanup retry, and the
operation id as `correlation_id`: `grok_logout_started`,
`grok_logout_bundle_unavailable`, `grok_logout_discovery_failed`,
`grok_logout_revocation_endpoint_missing`, `grok_logout_claim_refused`,
`grok_logout_revocation_claimed`, `grok_logout_revocation_confirmed`
(`revocation` is `revoked` or `already_invalid`), `grok_logout_revocation_unconfirmed`
(closed reason), `grok_logout_finished`, `grok_logout_cleanup_started`,
`grok_logout_cleanup_confirmed`, `grok_logout_cleanup_pending`,
`grok_delete_logout_started`, `grok_delete_logout_completed`,
`grok_delete_logout_blocked`, `grok_subscription_take_refused_logging_out`,
`grok_subscription_finish_refused` and `grok_subscription_cleanup_retry`. The
acceptance logs `grok_operation_accepted`. They never contain a token, the
revocation request, a path, an account email or another identity field, and no
event or error carries one.

Unverified assumptions (no real revocation was captured; the opt-in real-account
run records them and this change records no real-world validation): the
revocation endpoint accepts the public-client form above, answers 200 for a
revoked token and an HTTP 400/401 `invalid_token` or `invalid_grant` document for
an already unusable one, and a refresh_token grant fails with `invalid_grant`
after the revocation. Tests use `internal/grokidptest`, which serves
`revocation_endpoint` (scripted failures, a blocking hook, request observation and
revocation of the accepted refresh token) and answers `invalid_grant` for a revoked
token; the requirement set is covered by `grok_revoke_test.go` in
`internal/subscription` (client) and `grok_logout_test.go`,
`grok_logout_delete_test.go`, `grok_owner_recovery_test.go` and
`grok_protected_credentials_test.go` in `internal/server` plus
`grok_cleanup_retry_test.go` in `internal/worker`. Logout during execution is
covered by `TestGrokExecutionRefusesNewWorkWhileLoggingOutAndLogoutWaitsForTheLease`
and `TestGrokDispatchRefusesNewWorkWhileLoggingOut` in `internal/server`, with the
real execute lease of [Managed execution](#managed-execution).

### Managed execution

A Grok Build session on a Grok subscription account runs on the Worker that took
the account lease. The account bundle is the same sealed `subscription.GrokBundle`
envelope and moves only through protected Take and Finish. Implementation:
`internal/server/grok_execution.go` (lease), `internal/worker/grok_execution_subscription.go`
(supervisor and recovery), `internal/harness/grok` (subscription profile).

**Switches.** System capability 40 (`GROK_SUBSCRIPTION_EXECUTION_V1`) and Worker
capability 21 (`MANAGED_GROK_SUBSCRIPTIONS_V1`, `domain.ManagedGrokSubscriptionsV1`)
are advertised only behind `grokManagedExecutionAdvertised` (server) and
`grokManagedExecutionEnabled` (Worker). Both default to false, so the default build
never lists 40, never echoes 21, rejects a Worker report of 21 as an unknown
capability and never reports it. Every execute Take, dispatch and execution
authority path below requires the negotiated Worker capability, so without it the
feature is inert. Tests turn the switches on to exercise the activated paths
(`TestGrokCapabilitiesAreAdvertisedOnlyBehindTheSwitch`,
`TestGrokWorkerCapabilityIsNegotiatedOnlyBehindTheSwitch`,
`TestManagedGrokCapabilityIsReportedOnlyWhenEnabledAndEchoed`).

**Dispatch and authority.** A Grok execution configuration may select a Grok
subscription account (`domain.ExecutionConfiguration.Validate` admits Grok with
the Grok service). Dispatch (`checkedExecutionAssignment`) treats the account as
managed when it is connected, has a settled generation and no recovery fence or
pending operation, requires the Runner Device to hold Worker capability 21 (a
managed Grok account is not bound to one Runner) and refuses new work with the
typed `unavailable` problem `grokLoggingOutProblem` while a server-owned logout
holds the account. Execution authority (`registration`, publication scope) accepts a managed
Grok account only under the original execute lease of the job, with Worker
capability 21, and never for a continuation or fork. A managed subscription never
receives API relay authority (`ProxyPath` is empty and the relay refuses it).

**Execute Take** (`takeGrokExecution`). The lease is keyed by the claimed
`ExecuteSessionJob` and its immutable assignment, not by a pending account
operation. It is granted only when all of these hold in one transaction: the
account is connected, enabled, ready, not fenced, not logging out and has no
pending operation; no other lease or active server operation exists (otherwise
`resource_exhausted`, which the Worker retries, so two sessions on one account
serialize); the Worker device and instance own the claimed job; the assignment
names this account and connection, the Grok service and harness, and is a first
input (a continuation or fork never receives a lease, so Grok 1.0.41 records and
version-1 results keep attribution without new resume authority); the job has no
cancellation request; and either the Runner Device has the verified subscription
installation (inspected assignments) or the assignment is direct startup (version 4),
whose executable the Worker verifies itself and whose reported version is checked
against `domain.GrokSubscriptionBaselineVersion` before authenticate. The
stored envelope is then re-opened against this server, the account and the
settled generation and delivered once; a replayed Take never redistributes it and
fences the exact unfinished lease. Two sessions on two accounts hold two leases
and run in parallel.

**Worker supervisor** (`grokSubscriptionExecution`). `beginGrokSubscriptionExecution`
takes the lease (never for a continuation or fork) and rejects a delivery without a bundle or
with a native profile. The harness runs every credential-free inspection first,
then calls the credential hook exactly once immediately before launching the
native process. The hook records the private home in the durable journal, binds
the home (`subscription.BindGrokHome`), opens the envelope and writes
`auth.json` with mode `0600`. The home is the profile's private `$GROK_HOME`
below the Worker root, never the workspace: the harness refuses a workspace that
contains or sits inside the private root. After the process is joined (success,
Stop, crash, timeout) the Worker runs the Worker execution exit
(`prepareGrokFinish`): read `auth.json` once, verify identity, seal a verified
successor for the Finish generation, delete `auth.json` and `auth.json.lock` and
only then report Finish. Unconfirmed cleanup is retried by the same owner up to
three times with fresh Finish identities; if it stays unconfirmed the lane
closes and the lease stays fenced. Every failure of a protected Finish is
execution uncertainty for the work runner (`managedExecutionUncertain`): a
journal write that fails before the report, a failed or refused RPC, a lost
response and a failed cleanup retry all close the primary lane and leave the job
journal unfinished, because the lease may still be live or its commit
unacknowledged. None of them is an ordinary terminal job error that keeps the
Worker connected. A process that was not joined is never read or
modified: the lease is reported uncleaned. A lease that never wrote a credential
is released unchanged. A materialization failure retires whatever may exist in the
named home and reports no success.

**Execute Finish** (`finishGrokExecution`). Success with confirmed cleanup and no
bundle releases the lease on the same generation. Success with a bundle requires
the refresh confirmation; the bundle must open for the generation the Finish
creates, carry the account's identity commitment and pass
`subscription.GrokRefreshed` against the stored generation, is staged in the vault
and is published atomically with the release, and the superseded generation is
removed after the commit. A bundle without the refresh confirmation is refused
without a fence. Any other report with confirmed cleanup (the Worker could not
verify the file) and any report without confirmed cleanup keeps the lease and fences the account; only the original owner
(Runner Device, Worker device and instance) may report again. A restart
(new server epoch) or a lost Worker instance fences the lease too
(`retainLostSubscriptionLeases`); the fence accepts only a cleanup-only report
from the original Runner Device and Worker device (no success, no refresh, no
bundle), and readiness returns only if the stored generation still opens. A lease
fenced by its own unconfirmed cleanup is retried with the ordinary report only
inside the epoch that granted it: once a restart made that epoch stale, the same
cleanup-only restriction applies even though the lease still carries its
unconfirmed-cleanup flag, so a pre-restart Worker can neither publish a rotated
bundle nor report success. An accepted logout keeps the account revoked after the
release and starts only then.

**Recovery on the original Worker.** The subscription watch offers a Worker its own
fenced Grok execute lease (`WatchSubscription`, Worker capability 21 required).
`runGrokRecovery` reads the retained journal, joins the supervised process of the
job, retires `auth.json`, its lock and temporaries in exactly the journaled home and
sends the cleanup-only Finish. Recovery never runs beside a live execution of the
same Worker process: the watch also delivers the lease right after the execution's
first Finish fenced it, but until the execution has finished its in-process
cleanup retries (the lease stays marked as owned by the live execution until its
Finish returns) the watch skips it without touching the home or sending anything.
When those retries end unconfirmed the lane closes, and the next watch recovers
the lease. The durable claim precedes the protected delivery, so
a journal that never recorded a generation, lease revision or home (the Take
response was lost after the server committed the lease) is the accepted
pre-delivery claim: the Finish names the generation and revision of the
authenticated server lease. A journal that recorded any of them must agree with
that lease. A refresh the interrupted process had already
written is lost by design: without the original bundle in memory its identity cannot
be verified, so recovery never reads, seals or returns the credential. If the
credential cannot be proven removed nothing is reported and the lease stays fenced.
A missing pathname proves nothing about the journaled directory, which may have
been moved away with its `auth.json`: it counts as retired only when the Worker's
native profile registry (`Registry.HomeRegistered`) no longer registers that home,
because an explicit session or storage deletion removes the profile directory and
its registry entry together. A home that is still registered but absent from disk
stays unconfirmed.

**Harness profile.** The subscription profile of `internal/harness/grok` differs
from the API profile only here. The API profile (authenticate with `xai.api_key`,
the three loopback endpoint blocks, the owned configuration with the relay
`base_url`, `env_key` and `api_backend`) is byte-for-byte unchanged and pinned by
`TestAPIProfileBytesAreUnchangedBySubscriptionSupport`.

- Environment: exactly `GROK_XAI_API_BASE_URL`, `GROK_MODELS_BASE_URL` and
  `GROK_MODELS_LIST_URL` are not set, so the native process uses its own
  subscription endpoints; every other isolation variable and the
  `GROK_{CLAUDE,CURSOR}_*_ENABLED=0` set remain. No execution key variable is set.
- Credential gate: a direct startup assignment carries no installation version,
  so the version reported by the credential-free `inspect` (identical in the
  runtime and the workspace inspection) must already be at or above the
  subscription baseline before the credential hook runs; an older executable is
  refused as `unsupported` and never receives the account login.
- Authentication: `initialize` must advertise `cached_token` then `grok.com`
  with default method `cached_token` or unset; the process must report a version at
  or above the subscription baseline; then `authenticate` uses `methodId`
  `cached_token` with `headless` true. A refusal is the typed `unavailable` error
  and carries no native text.
- Configuration: the owned `config.toml` selects the native model through the same
  local selector but declares no `base_url`, `env_key` or `api_backend`.

**Documented assumptions, unverified without a real account.** (1) The real
subscription endpoints behind the three unset variables were never captured; the
native defaults are used. (2) How `session/new` behaves after `cached_token` was not
recorded; the profile keeps the existing strict session validation. (3) That a
local model selector without a relay endpoint keeps the native subscription
routing, and that the advertised default method is `cached_token` or unset, are
inferred from `initialize` alone. These are the reason the capabilities stay off
until the opt-in run below passes.

**Logging.** Server and Worker records carry `phase=EXECUTION` and the account, lease
and operation identifiers, never a credential, path, identity, URL or native text.
Cleanup records add `cleanup_phase=CLEANUP`.

**Tests.** Server: `grok_execution_test.go`. Worker: `grok_execution_subscription_test.go`
and `grok_attach_test.go`. Harness: `subscription_test.go` (a scripted ACP process
completes a text turn with `cached_token`, mode `0600` login, no key and no
endpoint blocks). The composed end-to-end native run with a real account is the
opt-in acceptance below and is not performed by the automated tests.

### Worker execution exit

When the native process of a future Grok execution lease exits, the Worker
follows the same confined protocol as a lifecycle Finish (`prepareGrokFinish`
with action EXECUTE, `sealGrokExecutionExit`), whatever the process outcome,
because the process may have refreshed the credential itself and an unreturned
rotated refresh token is lost:

1. The held home must still be the bound directory (`GrokHome.Verify`).
2. The latest `auth.json` is read once and verified against the original bundle.
   An unreadable or invalid file, another identity, or a changed credential that
   does not pass `subscription.GrokRefreshed` (a later expiry and a changed key or
   refresh token) is not success. An unchanged credential is success with no
   bundle. A verified successor is sealed for the generation the Finish creates
   and returned with the refresh confirmation.
3. `auth.json`, its attributed temporaries and the native `auth.json.lock` are
   retired through the held handle. `GrokHome.AuthFilePresent` also reports a
   remaining lock file, so an unremoved lock keeps the cleanup unconfirmed. A
   non-regular `auth.json.lock` entry (a directory or a symlink) in a reused home
   is never replaced: `GrokHome.WriteAuthFile` refuses it before writing, while a
   non-regular `auth.json` is still replaced by name during publication.
4. A bundle is returned only with confirmed cleanup. A mismatch, an unreadable
   file or an unconfirmed cleanup reports no success and no confirmed cleanup, so
   the server keeps the lease and sets RECOVERY_REQUIRED; the lease is never
   released before the profile cleanup is confirmed.

The Worker logs `grok_subscription_finish` with `refreshed` and, for a refresh,
`phase=REFRESH` (for an execute lease `phase=EXECUTION`), and never a credential,
path or identity. The execute Take, the server-side Finish of an execute lease and
the native process supervisor that calls this protocol are described under
[Managed execution](#managed-execution). The lock-file retirement applies to every
lifecycle Finish already.

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

- DeliDev Grok Build subscription support follows `cmds-delidev-grok-subscription-contract.md`. System 39 advertises server-owned CLI device-code login. System 40 and Worker 21 retain their independent default-off execution gates; progress field 10 and the Grok diagnostic messages retain their existing allocations. Declarations grant no login, native execution or migration. Preserve existing capability ownership, server-owned OAuth/protected credentials, original account leases and same-account/Worker history; verify the subscription profile at or above `domain.GrokSubscriptionBaselineVersion` (`1.0.46`) by native validation before activation.

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
left untouched. A member or owner Finish scope and any action other than login,
refresh, logout and execute are refused. An execute Take has its own admission
under [Managed execution](#managed-execution) and needs Worker capability 21.
Grok lifecycle work (login, refresh, logout) is server-owned and is never pushed
to Workers through the subscription watch; the watch offers a Worker only its own
fenced execute lease, and only when it negotiated Worker capability 21.

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

Server-owned refresh is described under [Server-owned refresh](#server-owned-refresh),
the Worker execution-exit protocol under [Worker execution exit](#worker-execution-exit)
and managed execution under [Managed execution](#managed-execution). The
capability advertisement is not activated by this storage contract.

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

| Interleaving                                                                                                         | Outcome                                                                                                                                                                                                                                 | Test                                                                                                                                                                                                                                                                                                                                                            |
| -------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Take of the queued original                                                                                          | Lease committed, stored envelope re-opened and delivered once                                                                                                                                                                           | `TestGrokLifecycleMovesBundleOnlyThroughProtectedTakeAndFinish`                                                                                                                                                                                                                                                                                                 |
| Take on an unverified or below-baseline installation, or for execute without Worker capability 21                    | Refused before any lease; account untouched                                                                                                                                                                                             | `TestGrokTakeRefusesUnsupportedAndUnverifiedWork`, `TestGrokTakeRequiresSubscriptionBaselineVersion`, `TestGrokExecutionRequiresTheNegotiatedWorkerCapability`                                                                                                                                                                                                  |
| Take replayed after a lost first response                                                                            | Denied; the exact unfinished lease is fenced; a finished or foreign lease is untouched                                                                                                                                                  | `TestGrokReplayedTakeFencesTheUnfinishedLease`, `TestGrokLateReplayedTakeDoesNotFenceAReleasedAccount`                                                                                                                                                                                                                                                          |
| Replaying request canceled, past its deadline or from a revoked Worker while the fence runs                          | The fence still commits on a bounded, detached context; the lease is never stranded without `RecoveryRequired`                                                                                                                          | `TestGrokReplayFenceIsDetachedFromTheReplayingRequest`                                                                                                                                                                                                                                                                                                          |
| Take whose stored envelope is invalid or missing, or whose operation is canceled during the vault read               | Nothing delivered; lease kept and account fenced under the current generation                                                                                                                                                           | `TestGrokTakeFailuresAfterTheLeaseCommitFenceWithoutDelivery`                                                                                                                                                                                                                                                                                                   |
| Installation rediscovered after Take                                                                                 | Finish still accepted from the exact owner                                                                                                                                                                                              | `TestGrokFinishSurvivesInstallationRediscoveryAfterTake`                                                                                                                                                                                                                                                                                                        |
| Finish with a stale lease revision, generation or lease                                                              | Denied before any write; no fence; the genuine owner can still finish                                                                                                                                                                   | `TestGrokFinishWithAStaleLeaseIsDeniedWithoutFencing`                                                                                                                                                                                                                                                                                                           |
| Finish with a rebound, foreign, native, empty or wrong-identity bundle, or an unrotated refresh                      | Refused; account fenced; nothing staged                                                                                                                                                                                                 | `TestGrokFinishRejectsReboundAndForeignBundles`, `TestGrokRefreshRequiresSameIdentityAndRotation`, `TestGrokLoginRejectsDuplicateIdentityAndAdmitsDifferentPrincipal`                                                                                                                                                                                           |
| Finish reports of login, refresh and logout (success, native refresh, cleanup)                                       | Table-driven: released only when fully confirmed (or a failed login with confirmed cleanup); otherwise fenced under the existing generation                                                                                             | `TestGrokFinishReportsMapToExplicitOutcomes`, `TestGrokFinishFencedLogoutKeepsTheReferencedGeneration`                                                                                                                                                                                                                                                          |
| Server-owned refresh: durable claim, one request, rotation or none, identity and failure outcomes, crash and restart | Claim committed before the single request; success publishes one new generation atomically; a 4xx rejection settles FAILED with the bundle untouched; every other post-claim outcome is RECOVERY_REQUIRED and the token is never resent | `TestGrokRefreshReplacesTheBundleWithoutRotation`, `TestGrokRefreshAdoptsARotatedRefreshToken`, `TestGrokRefreshFailuresNeverReplaceTheBundleOrResend`, `TestGrokRefreshCrashMatrix`, `TestGrokRefreshClaimIsDurableBeforeTheRequest`                                                                                                                           |
| Finish publishes, then removes the superseded generation                                                             | Removal only after the document stops referencing it; logout removes every generation                                                                                                                                                   | `TestGrokFinishDeletesTheReferencedGenerationOnlyAfterPublication`                                                                                                                                                                                                                                                                                              |
| Finish aborted after staging (revoked Worker, store failure)                                                         | Account fenced; current generation kept; staged generation retired after the account is proven not to reference it                                                                                                                      | `TestGrokFinishFailedPublicationKeepsTheCurrentGeneration`, `TestGrokRetireUnpublishedGenerationRequiresProofOfNonPublication`                                                                                                                                                                                                                                  |
| Finish aborted and the staged generation cannot be removed                                                           | Account fenced; current generation kept; the staged generation stays vault-owned, never granted, removed by the next cleanup                                                                                                            | `TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration`                                                                                                                                                                                                                                                                           |
| Cancellation wins while the new generation is staged                                                                 | Generation never published; staged generation removed                                                                                                                                                                                   | `TestGrokFinishCanceledDuringStagingRemovesTheStagedGeneration`                                                                                                                                                                                                                                                                                                 |
| Removal of the superseded generation fails after the commit                                                          | Error surfaced and account fenced; the published generation is kept, the superseded one is removed by the next cleanup                                                                                                                  | `TestGrokFinishCleanupFailureAfterPublicationKeepsThePublishedGeneration`                                                                                                                                                                                                                                                                                       |
| Removal of the superseded generation fails after an Execute settlement commit                                        | The committed settlement is reported as success and nothing is fenced (the lease is already released); the published generation is kept, the superseded one stays vault-owned, is never granted and is removed by the next cleanup      | `TestGrokExecutionPostCommitCleanupFailureNeitherFencesNorStrandsTheAccount`                                                                                                                                                                                                                                                                                    |
| Request ends while the superseded generation is removed after the commit                                             | Removal continues on a bounded, detached context; the committed publication is neither undone nor fenced                                                                                                                                | `TestGrokFinishCleanupIsDetachedFromTheRequestAndBounded`, `TestGrokRetireUnpublishedGenerationSurvivesAnEndedRequest`                                                                                                                                                                                                                                          |
| Finish retried after it committed                                                                                    | Receipt replay; no vault write or removal; account untouched                                                                                                                                                                            | `TestGrokFinishReplayRepeatsNoVaultWork`                                                                                                                                                                                                                                                                                                                        |
| Worker instance lost, or server restarted, while a lease is held                                                     | Lease fenced under its generation; later Finish and Take refused; the vault is unchanged                                                                                                                                                | `TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish`                                                                                                                                                                                                                                                                                                         |
| Server crash between staging and the commit                                                                          | Same as a restart: lease fenced; the staged generation is an unreferenced vault object, never granted, removed by the next cleanup                                                                                                      | `TestGrokFinishAbortWithFailedRetirementStaysFencedAndNeverGrantsTheStagedGeneration`                                                                                                                                                                                                                                                                           |
| Worker materializes a bound envelope, a rebound or malformed one, or an unsafe home                                  | Written once with owner-only permission; otherwise nothing written                                                                                                                                                                      | `TestGrokMaterializationWritesPrivateAuthFileFromBoundEnvelope`, `TestGrokMaterializationRejectsReboundEnvelopeBeforeWriting`, `TestGrokMaterializationRefusesNonPrivateOrLinkedHome`                                                                                                                                                                           |
| Home renamed away and replaced after binding, before or after the path check                                         | The credential is written, read and retired only in the bound directory; the substitute is never written, read or modified                                                                                                              | `TestGrokWriteIsAnchoredToTheBoundHomeNotItsPath`, `TestGrokWriteDoesNotFollowAPlantedAuthFileLink`, `TestGrokHomeRejectsALooseningAfterBinding`, `TestGrokHomeBindingFailsClosed`                                                                                                                                                                              |
| Fresh login in a home that already holds a credential or temporary                                                   | Refused; a leftover is never sealed as the login result                                                                                                                                                                                 | `TestGrokFreshLoginRefusesLeftoverCredentialFiles`                                                                                                                                                                                                                                                                                                              |
| Native process rotates, fails, changes identity or leaves an unparsable file                                         | Rotated credential sealed; otherwise no bundle; the file is retired in every case                                                                                                                                                       | `TestPrepareGrokFinishSealsRotatedCredentialAndRetiresFile`                                                                                                                                                                                                                                                                                                     |
| Remnant that cannot be removed, or a hard-linked credential                                                          | No bundle; cleanup unconfirmed; lease stays fenced                                                                                                                                                                                      | `TestPrepareGrokFinishWithholdsBundleWhenCleanupIsUnconfirmed`, `TestPrepareGrokFinishWithholdsBundleForHardLinkedCredential`                                                                                                                                                                                                                                   |
| Hard link created after the credential was inspected and before its name is unlinked                                 | No bundle; cleanup unconfirmed (retained-handle link count and identity rescan); lease stays fenced                                                                                                                                     | `TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheUnlink`, `TestRetireGrokAuthFileDetectsALinkCreatedBeforeTheTemporaryUnlink`, `TestGrokWriteRefusesAnOlderLinkedCredential`                                                                                                                                                                                  |
| Home renamed away or replaced after materialization                                                                  | No bundle and no cleanup for refresh, login and logout; nothing read or removed in the substitute; the original keeps its file                                                                                                          | `TestPrepareGrokFinishRefusesARenamedOrReplacedHome`, `TestGrokHomeVerifiesTheBoundDirectoryIdentity`                                                                                                                                                                                                                                                           |
| Finish prepared for a lease that never bound a home                                                                  | Cleanup never confirmed                                                                                                                                                                                                                 | `TestPrepareGrokFinishRefusesAnUnboundHome`, `TestGrokMaterializationBindsTheHomeForFinish`, `TestGrokMaterializationReleasesTheHomeWhenItFails`                                                                                                                                                                                                                |
| Worker crash or restart with the native file on disk                                                                 | The server fences the lease (row above); the plaintext stays in the private home until the original Worker's recovery (`runGrokRecovery`) retires it; nothing is resent                                                                 | Server side: `TestGrokLostWorkerFencesTheLeaseAndRefusesLaterFinish`                                                                                                                                                                                                                                                                                            |
| Execute lease: unchanged credential, verified refresh, foreign identity, unrotated or wrong-generation bundle        | Released on the same generation; or published atomically as one new generation; or refused and fenced for cleanup-only recovery with the vault unchanged                                                                                | `TestGrokExecutionLeaseDeliversTheStoredEnvelopeAndReleasesAnUnchangedCredential`, `TestGrokExecutionRefreshReturnsTheRotatedBundleOnlyAfterIdentityVerification`                                                                                                                                                                                               |
| Execute lease: second session on the same account, sessions on two accounts                                          | The second waits behind the exclusive lease (`resource_exhausted`, retried); two accounts hold independent leases                                                                                                                       | `TestGrokExecutionLeasesSerializeOneAccountAndRunDifferentAccountsInParallel`                                                                                                                                                                                                                                                                                   |
| Execute lease: unverified exit, unconfirmed cleanup, replayed Take, server restart or lost Worker                    | Fenced; only the original owner settles it with a cleanup-only report, readiness returns only when the generation opens; nothing is redistributed                                                                                       | `TestGrokExecutionUnverifiedExitFencesThenCleanupOnlyRecoveryReleases`, `TestGrokExecutionUnconfirmedCleanupIsRetriedByTheOriginalOwnerOnly`, `TestGrokExecutionReplayedTakeFencesAndNeverRedistributesTheBundle`, `TestGrokExecutionServerRestartIsRecoveredByTheOriginalWorkerOnly`, `TestGrokExecutionRecoveryDoesNotRestoreReadinessOnAnUnusableGeneration` |
| Execute lease: logout accepted while the lease is held                                                               | New execution is refused with the typed unavailable problem; the account stays revoked and the logout starts after the release                                                                                                          | `TestGrokExecutionRefusesNewWorkWhileLoggingOutAndLogoutWaitsForTheLease`                                                                                                                                                                                                                                                                                       |
| Worker: native process exits (success, Stop, crash, timeout), is not joined, never launched or failed to materialize | The credential and lock are retired before Finish; a verified refresh is returned; an unjoined process is untouched and the lease is fenced; an untouched lease is released unchanged; a failed materialization reports no success      | `TestGrokExecutionCleansUpAfterEveryJoinedNativeOutcome`, `TestGrokExecutionNeverTouchesTheFileOfAProcessThatWasNotJoined`, `TestGrokExecutionReleasesAnUntouchedLeaseWhenTheLaunchNeverGotThatFar`, `TestGrokExecutionFailedMaterializationFencesWithoutClaimingSuccess`                                                                                       |
| Worker restart with the credential on disk                                                                           | The original Worker retires it in the journaled home after joining the process and reports cleanup only                                                                                                                                 | `TestGrokRecoveryRetiresTheOriginalHomeAndReportsCleanupOnly`, `TestGrokRecoveryRefusesForeignJournalsAndUnprovenCleanup`                                                                                                                                                                                                                                       |

The Worker holds the only copy of a freshly produced bundle in memory until
Finish; a Finish whose response is lost leaves the fenced lease for recovery
rather than resending a credential. Owner recovery of an execute lease is
described under [Managed execution](#managed-execution); cancellation and removal
of a fenced Grok account are separate lifecycle work.

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
  cleared, by `protectedAccountDeletion` while the vault holds a protected
  reference of another purpose; explicit deletion removes the retained
  `account-login` generations itself. A restored account is disconnected, so the logout-before-delete
  step of [Server-owned logout](#server-owned-logout) does not apply to it and
  never sends a revocation on its own.
- A server-owned logout that was queued, claimed or fenced when the image was
  taken is retained as quarantined evidence. The restored account has no
  connection, so the scanner never claims the logout, no revocation is sent from
  a restored image and a new logout request is refused; the protected generation
  reference stays in the vault.
- Finishing an unsettled Grok cleanup on the original Worker uses the
  owner-recovery path described under
  [Original-owner cleanup retry](#original-owner-cleanup-retry), which applies to
  a live lease of the running server. Restore retains the ownership evidence and
  never completes or reassigns the cleanup.

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
- Canceling a queued, unclaimed operation (initiator device revocation, which
  spares a queued logout, or the shared `cancelPendingLogin` body that `CancelSubscription` applies once Grok
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
`execution` and `history` phases have no producers yet; they remain covered by
the phase mapping and validation tests and attach through the same
`settleGrokOperation` when those producers land. The server-owned logout writes
its own `logout` and `cleanup` diagnostics (see
[Server-owned logout](#server-owned-logout)).

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
blocks new execution and completes only after original execution exit and
credential cleanup are confirmed; deletion reuses logout-before-delete
(see [Server-owned logout](#server-owned-logout)).
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
client disposal, server restart and uncertain exchange/refresh. The device-code
lane adds fake-IdP cases for pending, slow-down, denial, expiry, the local
deadline, per-phase cancellation, restart during polling and request spacing on
a virtual clock. Cover identity
duplication, account serialization, separate accounts, logout during execution,
cleanup failures, deletion and backup/restore. Exercise General Chat, single and
multiple repositories, Local/Worktree, Execute/Plan, native interactions, follow-up,
Stop/resume and process/server restart plus API/ChatGPT regressions.

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

#### Managed execution activation checklist

System 40 and Worker 21 may be advertised (flip `grokManagedExecutionAdvertised`
and `grokManagedExecutionEnabled`) only after an opt-in real-account run on the
current stable release, at least 1.0.52, has recorded, without logging a token:

1. the three subscription base URLs the native process uses once the loopback
   blocks are removed, and that nothing reaches a loopback blocker;
2. that `initialize` advertises `cached_token` for a home holding `auth.json`, and
   its default method;
3. that `authenticate` with `cached_token` followed by `session/new` and one prompt
   in a scratch workspace returns a completion and exits cleanly, using the owned
   configuration (no relay `base_url`);
4. whether the native process refreshes `auth.json` during the run, and that the
   returned bundle passes `GrokRefreshed`;
5. that `auth.json`, `auth.json.lock` and the private home hold no credential after
   the exit, and the real `~/.grok` is untouched.

This run is not performed by the batch that implemented managed execution.

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
