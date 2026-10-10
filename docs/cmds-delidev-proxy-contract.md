# DeliDev native API relay contract

## Original API format authority

Capability 9 account changes do not alter a registered original execution scope.
Resolve its immutable connection ID against retained server-owned generations,
then resolve the selected immutable Provider profile and original protected key
reference. Current-format validation cannot grant or revoke old-format validation;
shared account disablement, exhaustion and explicit all-generation Disconnect keep
their original revocation authority. Original streaming and subsequent requests
remain bound to the original endpoint, authentication and protocol. No key enters
Worker environments or client account responses. See the
[account contract](cmds-delidev-accounts-contract.md#connected-api-format-changes).


## Account-selected API profile authority

Every API execution admission and proxy scope uses the common server account
profile resolver from the [catalog contract](cmds-delidev-catalog-contract.md#api-account-format-selection).
The original account and connection generation determine protocol, URL and
upstream authentication, independently of the provider's legacy default. Codex
Responses can therefore use an OpenRouter account selected as Responses; a Chat
Completions account remains incompatible. Operation allowlists, tool enforcement,
stream parsing and sanitized provider errors retain their existing protocol
ownership. Unknown operations fail locally without another format or endpoint
attempt. Title generation uses the original account's Responses profile. Resume,
Fork and Sidechat never infer another account or connection from newly available
profiles. Worker assignment and credential wire formats stay unchanged.


## Metadata-only request publication

The feature adds an original-lease diagnostic projection around each single authorized HTTP invocation. Before transmission, persist one generated correlation/record identity and its send claim; exact publication receipts do not make another HTTP attempt. After response/cancellation, publish only closed status/error, observed elapsed latency, allowlisted settings and protected-value-checked opaque request/response IDs. Completion is bounded and joined before lease/storage release; failed or interrupted settlement retains uncertainty. This table is not a usage source and does not modify native retry, authorization, request/response byte forwarding or credential lifetime. Native input/effective settings remain a separate exact-identity publication. See the [diagnostics contract](cmds-delidev-diagnostics-contract.md) for limits and reads.


## Scope
`cmds/delidev-cli/internal/apiproxy` implements the internal server-only native HTTP/JSON/SSE relay. The server router now registers the relay with durable execution authority and native-reference storage. The Worker now creates/registers an execution token and supplies it to Codex for an independently accepted first assignment. The public first dispatcher now produces the immutable assignment after current configuration/account/workspace/Worker checks; only the owning Worker may register its scoped credential. This document describes the implemented relay and authority boundaries; fixture authorization does not establish complete native execution acceptance.

## Runtime and Language
Go, using the root module, `net/http` and the existing typed domain model. Native provider SSE is an internal harness protocol, not a DeliDev client event API; product communication continues to use authenticated Connect.

## Users and Operators
The server resolves immutable execution authority and reads the exact protected account connection. A native harness on the selected Worker receives only its execution credential and relay endpoint. Users do not receive general-purpose external proxy keys or caller-selectable upstream destinations.

## Interfaces and Contracts
An `Authority` acquires a revocable `Lease` for a canonical `ddv_exec_` token containing 32 bytes of URL-safe random credential material. A scope binds canonical UUID-v7 execution, session, account, connection, provider and model identities; one validated provider endpoint/protocol/authentication policy; one exact native model; and a nonempty protocol-compatible operation allowlist. Owner/device bearer tokens are not execution credentials. The server authority authenticates the digest and rechecks current execution/connection/Worker authority. The native Claude client may send one Bearer authorization and one `X-Api-Key` header together only on Anthropic Messages operations and only when both contain the exact same valid scoped token. Compare them without normalizing either credential. Different credentials, duplicate header values, malformed schemes/tokens and every dual-header request on other protocols fail before authority acquisition. The pair represents one lease; neither Worker credential header is forwarded to the upstream provider.

The `RegisterExecution` Worker RPC accepts only the SHA-256 digest of a Worker-generated token and binds it to the exact claimed job revision, execution, machine, instance, device and current server process epoch. One job can register only one immutable binding; exact receipt retries preserve it and recheck live authority. Every acquisition rechecks the claimed uncanceled job, matching immutable configuration and current execution/input identities, enabled machine, nonrevoked Worker, fresh 45-second Worker lease, active unrecovered session, current project restrictions and exact enabled/ready API account connection. The closed profiles grant Codex `0.151.0` OpenAI Responses creation/compaction, OpenCode `1.18.32` OpenAI Chat Completions for its independently validated initial/continuation assignments, Grok Build `1.0.41` Chat Completions for its verified original first-input profile, and Claude Code `2.1.236` Anthropic Messages creation for initial assignments only. OpenCode retains the exact Build/Plan mode with default native permissions; explicit effort, child/concurrency/review-model/tier overrides and another protocol remain unsupported. Claude registration and Worker launch share the same immutable input/settings validation: preserve exact native tool permission modes and supported effort, reject Codex sandbox/approval settings and unimplemented child/concurrency/review-model/tier overrides. Claude token-count operations and continuation registrations remain outside this profile. Protocol discovery alone grants no relay access. Public dispatch is integrated for Codex and OpenCode; Claude registration remains separate from its unfinished public publication/completion integration. A changed account connection, stale/replaced Worker or new server process cannot reuse a stored grant. Each supported Codex continuation uses a new job/execution/digest registration while preserving the original configuration and the explicitly retained account/connection (original unless changed by the stopped-session API operation). The retained current selection revokes every preceding grant; native references remain scoped to the original session/account/connection/model without another account fallback.

Grok registration requires the original first-assignment execution, pinned protocol, exact selected model and default native permission profile, with independently bound Execute/Plan input. First Execute instructions/templates use the exact private additive rules profile in the harness contract. Plan instructions, effort, child/concurrency/review-model/tier overrides, other protocols and continuation remain outside this profile. Native session/input publication now composes the real Worker journal/outbox and server authority, but richer publication, completion and ordinary public Grok dispatch require their separate adapters. A detected installation or original binding cannot grant those capabilities.

Only exact POST paths under `/api-proxy/v1` are recognized: `/chat/completions`, `/responses`, `/responses/compact`, `/messages`, and `/messages/count_tokens`. Each lease must specifically authorize its operation. The sole permitted query is the exact `beta=true` spelling on the two Anthropic Messages operations, as emitted by the pinned Claude native client; it is preserved upstream without changing operation/account/model scope. All other queries, including duplicate/additional parameters, equivalent encodings/casing, a trailing empty query and queries on other protocols, are rejected before provider authority is acquired. Escaped paths, arbitrary methods and absolute-form proxy destinations are rejected. JSON must contain the scope's exact `model`; duplicate fields, ambiguous control-field casing, alternate `models`, fallback `route` selectors and native `fallbacks` chains are rejected before secret access or upstream transmission. This includes null/empty `fallbacks` and case aliases: keeping the top-level model unchanged cannot grant an alternate native model. Compaction and token-count requests cannot request streaming. Native request bodies otherwise retain their bytes and provider-specific fields; the relay does not translate protocol families or invent native capabilities.

Responses `previous_response_id` and `conversation` references require an execution-authority callback to verify ownership before key access. Absence of that callback fails explicitly. Returned native response identities are observed only after response/secret checks and before delivery. The durable reference index binds session, account, connection, canonical model, reference kind and exact native identifier; a different scope cannot borrow continuity. References are capped at 10,000 per session, and session deletion removes them. Cross-execution account switching and native file/item/conversation operation families still require explicit adapter integration; this initial allowlist does not authorize those additional routes.

Successful bounded JSON and LF/CRLF SSE preserve native bytes except that native diagnostic error objects are replaced with safe protocol-shaped errors. Native machine codes from a closed allowlist also pass the protected-value check, including non-200 responses; a colliding native code falls back to the local error classification, and a colliding local body is omitted while retaining failure status. Non-colliding codes retain distinctions needed for context limits, authorization, rate limits and overload. Native streaming ends only on its protocol terminal event; truncated/invalid streams abort the downstream response without forged completion or proxy retries. Native harness retry decisions remain the harness's responsibility. HTTP 4xx/5xx status and a bounded `Retry-After` survive; provider cookies, redirect locations, diagnostic messages, challenges and request identifiers do not.

Responses creation SSE `type: error` events decode the closed native code from
the top-level `code` field. Their sanitized frame retains `event: error` and
top-level `type`, `code`, `message`, `param` and `sequence_number` fields. The
message is local and redacted; `param` is always null. A nonnegative integer
sequence number up to `9007199254740991` is retained; missing, malformed or larger
values use zero. Unknown codes use the safe `Unavailable` classification and
local code. Protected native codes fall back to the local code; an unsafe local
event aborts delivery. The error is terminal and settles the original diagnostic
and lease once. HTTP error JSON, Chat and Anthropic errors, and nested
`response.failed` errors retain their existing envelopes.

There are at most 16 concurrent authorized requests, with no wait queue; request/JSON/frame limits are 32 MiB, total SSE is 256 MiB, and the request deadline is 15 minutes. Request-body reads and each downstream write have 30-second deadlines. Dial/TLS handshakes are bounded to 10 seconds; inference response headers have a 10-minute deadline and a 32 KiB limit. Native error JSON inspection is separately bounded to 64 KiB and two seconds. Bounds are operational limits, not permission to replay a possibly accepted request.

### cmds/delidev-cli constraints

- The native API relay in `internal/apiproxy` must join any started body/deadline cancellation callback before its HTTP handler returns. Downstream connection reuse cannot inherit a prior request's late deadline mutation; preserve upstream cancellation, once-only key reads and lease release under `cmds-delidev-proxy-contract.md`.

### cmds/delidev-cli/internal/apiproxy constraints

- Follow `cmds-delidev-proxy-contract.md` and the owning harness/subagent contracts. Only Go retrieves upstream credentials; execution-scoped leases retain the original account, provider, model set and revocation authority.

- OpenCode foreground tool responses must validate complete task arguments before any executable tool-call frame reaches native code. Preserve original frames and ordering in bounded private buffers; reject task reuse, background work and unknown task fields before native submission. A later observer rejection cannot substitute for this guard.

- Guard every locally generated error body with the protected values already acquired by that request, including keys returned with an error. If the fixed local body collides, preserve the failure status and safe headers with an empty body. Pre-key denials must not read credentials for error construction; failures after stream output starts retain the original abort behavior.

- Responses creation SSE `type: error` uses top-level native codes and a flat Responses event. Keep HTTP, Chat, Anthropic and nested `response.failed` envelopes separate. Redact the message, clear `param`, bound sequence numbers to nonnegative JSON-safe integers, and retain closed classification, protected-code fallback and once-only diagnostic/lease settlement under the proxy contract.

### cmds/delidev-cli/internal/nativeproxy constraints

- Follow the network and native API relay contracts. This package owns one ephemeral authenticated loopback listener for one pinned Codex API runtime, never a public RPC or generic proxy.

- Authenticate before dialing and accept only the exact original paired server host/port. HTTPS stays an end-to-end CONNECT tunnel; plain HTTP permits only the original loopback origin and fixed Responses relay paths.

- Bound local connections, headers, copy buffers and deadlines. Cancel and join every listener, socket, tunnel and observation with the original runtime. Listener readiness does not establish native route use or inference success.

### cmds/delidev-cli/internal/outbound constraints

- Plaintext loopback HTTP destinations require Direct or an explicit matching host/IP/CIDR and optional-port bypass. Reject other selected proxy routes before opening any connection; never transmit the account key through CONNECT/SOCKS5 or silently select Direct. Verified HTTPS loopback destinations retain explicit proxy routing.

- Direct/exact-bypass HTTP transports pin localhost to literal IPv4/IPv6 loopback before the caller’s base dialer. Preserve that dialer’s context and the original TLS hostname, ordinary nonlocal DNS and explicit proxy authority; cancellation never selects another profile.

### cmds/delidev-cli/internal/server constraints

- Explicit stopped Codex API account selection follows the sessions/proxy contracts. Require an owner or paired client principal at the service boundary before validation or mutation, in addition to HTTP role checks and transactional device-revocation checks. Require exact terminal/cleanup/checkpoint evidence and current eligibility from the original candidate snapshot; retain revisioned selection history, pause until explicit Resume and never reroute automatically. Preserve original usage/assignments and read the predecessor checkpoint under its complete original account/connection pair while creating a fresh successor grant. Explicitly switching away and back after reconnection must retain the original checkpoint connection independently of the fresh selected connection. Missing or account-bound history cannot switch; every switched relay request independently rejects remote history references.

### cmds/delidev-cli/internal/worker constraints

- Grok original public binding follows the harness/session/proxy contracts. Persist original native claims before native work, but require acknowledged session publication before an input claim. Initial Plan binding also requires both original mode records and unchanged configuration. Preserve native UUID-v7 session and UUID-v4 prompt identities; this format support cannot authorize completion reporting. The shared outbox may replay only its exact retained receipt under unchanged original claims, with no native API access. Keep initial mode separate from current state and never enable richer events, continuation or public dispatch from binding alone.

- Follow `cmds-delidev-proxy-contract.md` for native API forwarding. The server relay requires digest-only Worker registration tied to an exact durable claimed execution and the current server process epoch; the Worker now registers and delivers scoped authority for immutable first Codex assignments, with public first dispatch supplying the immutable checked assignment. Recheck live session/input/Worker/account authority on every request, scope native references to session/account/connection/model, and join relay cleanup before credential deletion or server resource closure. Fix every request to its immutable account connection/provider/model/operation scope, cancel on authority loss, and verify native reference ownership. Never retry or redirect upstream requests, translate protocol families, inherit ambient proxies, derive usage from traffic, expose general proxy keys, or persist/relay reflected protected values before complete checks. Preserve native completion/failure and possible acceptance after interrupted transport.

- Codex API execution uses only the registered execution token in a private explicit environment and the fixed proxy path on the paired server origin. Never place it in argv/config/history or pass upstream keys to the Worker. Verify effective native provider authority before thread start/resume because managed policy can outrank session flags. Reject alternate auth/headers/query authority, exclude the token from child shells and preserve native retry behavior; the relay itself never retries. The Worker job loop now binds the immutable first assignment, private runtime, workspace lease, durable event outbox and terminal cleanup report. Public first dispatch integrates this runner; unimplemented native event families must still fail explicitly.

## Storage
The relay itself persists no tokens, upstream keys, prompts, responses or usage. Its server authority uses schema v7's private grant and native-reference indexes, with synchronized pre-migration backups and transactional upgrade from v1-v6. Job deletion removes its grant; session deletion removes its references. Registration receipts contain only the job identity, and reference-observation receipts contain no native content. Token delivery must not put raw credentials in SQLite, job documents, events, argv, transcripts or ordinary output. Harness JSON remains the sole usage source; relay traffic cannot estimate tokens or costs.

## Security
Plain HTTP is accepted only from an actual loopback peer; remote callers require TLS. Browser-origin/cookie/fetch metadata paths are denied. Each request accepts one bearer or `x-api-key` execution credential, with the exact equal dual-header Anthropic exception defined above. The native account key is retrieved only after body/scope authorization and a second account-gated authority check, injected into the single saved upstream endpoint, and cleared from the mutable byte buffer after use. Keyless accounts never read a key. Each lease observes durable changes and checks lease freshness at least once per second; cancellation covers finished/canceled/uncertain jobs, account disconnection/replacement, Worker/device revocation, changed restrictions and server shutdown. Account disconnection atomically persists cancellation for unfinished jobs selected on that account while preserving claimed revisions. The Worker receives that control independently of an active relay request. Disconnection and server shutdown cancel and join active relay handlers before deleting credentials or closing their storage. This confirms relay cleanup only; native process cleanup remains the execution Worker's separate responsibility.

The private transport ignores ambient proxy variables, uses system TLS verification and the explicit selected server route under the [outbound networking contract](cmds-delidev-network-contract.md), disables compression, redirects, connection reuse and replayable request bodies, and does not retry. Direct and exact-bypass localhost endpoints dial actual loopback addresses. Explicitly proxied destinations retain their original authority. Plaintext loopback upstream requests require Direct or an explicit matching bypass; otherwise the transport rejects them before connection, with no fallback or account-key transmission. Verified HTTPS loopback upstreams may still use the selected proxy. Outbound `HTTP-Referer` is fixed to `https://deli.dev`. No caller organization, project, cookie, proxy, destination or authorization header passes through. Anthropic uses its fixed API version and a bounded beta-identifier header.

Response checks reject literal, decoded JSON-string and Base64 representations of the upstream key and execution credential before delivery or reference persistence. A bounded linear stream matcher retains frames with unresolved protected prefixes at stable JSON string/number paths and SSE metadata fields (including comments and data-less frames), preventing a credential split across native delta frames from escaping incrementally. It allows at most 1,024 active paths, 4 KiB per path and 32 MiB pending frames; overflow fails closed. This is finite reflection protection, not a claim to recognize arbitrary provider transformations or covert encodings. Never use raw provider diagnostics for logs or public errors.

SSE field-name bytes use a separate bounded cross-frame matcher, including unknown and colonless fields. Metadata values and JSON deltas retain their independent matchers; benign unknown fields preserve their original bytes.

Decoded JSON object names also enter independent global and parent-path matchers before JSON-pointer escaping. Thus repeated enclosing keys cannot hide fragmented nested names, and escaped raw/Base64 credential fragments are checked before any original frame is released. Numeric JSON values enter their original value-path matcher without rounding or normalizing their native spelling, including large integers and exponents. These states share the existing active-path and pending-byte bounds.

HTTP-200 standalone error envelopes use the same protected-code/local-body fallback as non-200 errors before returning a synthesized HTTP 502 response. A successful transport status cannot bypass protected-value checks.

Every locally generated error body uses the protected-value guard once the execution credential or selected account key is available. This includes keys returned together with an error, diagnostic publication failures, upstream request/transport failures, rejected JSON and failures before SSE output starts. If the fixed local body also collides, retain the failure status and safe correlation/security headers with an empty body. Pre-key denials never read a credential solely to construct an error. A failure after SSE output starts still aborts the original response without appending a local error, forging completion or retrying the provider. Key acquisition, upstream dispatch and lease release remain once-only.

Cancellation may abort the original downstream response, but every started
body/deadline cancellation callback is joined before the HTTP handler returns.
Its response-writer ownership cannot cross into another request on a reused
downstream connection. Later requests with a revoked execution token still fail
authorization without another protected key read or upstream attempt.

## Logging
Use structured `api_proxy_request_finished` metadata: correlation/execution/session/account/provider/model IDs, closed operation and phase, stream selection, submission uncertainty, HTTP status, typed failure code and duration. `submitted` means an HTTP attempt began, not proof that upstream accepted it. Native failed terminal events retain failure classification. Keys, endpoint URLs, native model strings, prompt/output bytes and provider diagnostic text are excluded.

## Build and Test
- `go test -race ./cmds/delidev-cli/...`
- `go vet ./cmds/delidev-cli/...`

Real loopback HTTP fixtures test all five operations, unchanged native JSON/SSE, injected headers, scope escapes, native-reference authorization, secret reflection/fragmentation, bounds, cancellation, TLS verification, ignored environment proxies, dropped responses and absence of retry. Server tests additionally exercise authenticated registration/replay, durable reference ownership, canceled jobs, account revocation, server-epoch replacement, and cancellation/join before protected-key deletion. Their native-readiness/job records and provider are controlled fixtures; these tests never invoke real inference or user accounts. The Linux arm64 test binary also runs in a non-root network-disabled container. Cross-compilation for other targets does not prove native runtime acceptance. Installed macOS Codex now passes an opt-in composition through authenticated registration and this server relay to a scripted loopback provider, with server-only key injection and exact model/input/output. Its accepted readiness is simulated, and no external account inference is claimed. An additional opt-in test now drives the actual Worker attach/job loop, token delivery, workspace lease, native execution, event publication and terminal cleanup report; live revocation also cancels the native process and retains recovery. Separate public CLI first-dispatch tests now verify keyless loopback validation through native Execute/Plan completion without seeded readiness. Actual macOS arm64 OpenCode `1.18.32` also passes authenticated server registration, immutable Execute/Plan settings/input, streaming completion and original persisted-history comparison against a scripted provider. An active native provider request is canceled and joined by account disconnection before the temporary protected key is removed; a separately claimed original owned-process cleanup cannot invent native interruption or completion. Its claimed assignment/account readiness are seeded fixture state, and its mutation file is a test coordinator rather than the production Worker journal. External provider-account evidence remains required.

## Dependencies and Integrations
Depends on the existing provider validation and protected connection contracts. The server supplies durable authority and cancellation; the selected native adapter must supply verified operation compatibility and its own structured output. Primary native protocol references: [OpenAI Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create), [OpenAI streaming Responses](https://developers.openai.com/api/docs/guides/streaming-responses), [Anthropic Messages](https://platform.claude.com/docs/en/api/messages/create), [Anthropic streaming](https://platform.claude.com/docs/en/build-with-claude/streaming), and [Anthropic token counting](https://platform.claude.com/docs/en/api/messages/count_tokens).

## Change Triggers
Update the project index, CLI contract, validation records in pull requests, issues and CI logs/artifacts and scoped AGENTS with authority/lifecycle integration or changes to routes, bounds, secret handling, native references or supported protocols. Add exact native acceptance evidence before marking a harness/provider combination supported. Preserve the complete issue requirements.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References
- [Project](project-delidev.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)

### Deleted project configuration
Relay authorization continues to enforce current project restrictions while the project exists. If configuration is explicitly deleted, an established session snapshot may use only the final Agent/account restrictions atomically retained with that project's deletion tombstone. Missing or invalid retained evidence denies authority; it never implies unrestricted access. This keeps an already authorized native request independent of configuration lifetime without bypassing current account/connection, Worker, session/input or cancellation checks. New first dispatch still requires live configuration.

Provider Off is intentionally not a live relay-revocation condition. A durable grant accepted before Off may complete its original turn under the existing relay checks. Off committed before a first fresh grant is rejected at dispatch/admission and registration; do not add the activation check to the shared relay scope or current-turn interactions. See [provider activation](cmds-delidev-provider-activation-contract.md).

The Codex automatic title relay grant has the separate `session-title` purpose and is bound to its claimed title job and original completed execution. It permits exactly one Responses creation and rejects conversation/state references. Before upstream transmission it commits a durable per-job HTTP send claim; a lost response cannot resend. The proxy rebuilds the request from the frozen first message and server instructions, exact model/effort/tier, no tools and a 128-token output cap. Only a Worker that proves the pinned Codex profile can register this grant. Current account, project, Worker/device/instance, session ownership, cancellation, budget and original server epoch are rechecked before the request.

## Stopped-session account selection

The [session contract](cmds-delidev-sessions-contract.md) defines explicit Codex API account switching. The relay records only full-history versus sticky account-bound use before requesting credentials or transmitting upstream. Unchanged retained modes use a read-only authority check without a new receipt or store-change notification; transitions recheck authority and sticky state inside the mutation transaction before updating the session. It retains no request content or provider identifier in that observation. Top-level previous-response/conversation selectors and Responses item-reference input are account-bound; both creation and compaction requests receive the independent guard. Inspect each input-array element independently so an unrelated non-object cannot hide another remote item reference. Unknown legacy evidence cannot prove portability. Any retained account change forbids remote-history reference authorization, even for an otherwise registered reference under the new account. Each later job has a fresh registration; existing terminal/current-selection checks deny every earlier scope. Original reference rows and historical usage are retained under their original account/connection/model.

## Worker native outbound composition

The [network contract](cmds-delidev-network-contract.md#owned-codex-api-tunnel) owns the ephemeral Worker listener, independently of this server relay. Original Codex API/title registration and a once-only native-route claim precede listener creation. Fresh registration requires synchronized desired/effective generation; later route selections preserve the original active execution lease. The listener forwards only original paired-server traffic, retaining destination TLS and native request bytes, with no upstream key in the native environment. Original process, listener and observation cleanup must all join before a successor checkpoint or successful cleanup report. Native frame protection is finite and independent of this relay's cross-frame provider-response guards.

An explicit Codex child model snapshot permits the parent and that one canonical child model under the original account/connection. Each request is narrowed before native-reference checks, diagnostics and persistence, so child responses retain child model ownership. Title authority stays single-model. No request may introduce alternate model arrays, routing, fallback chains or independent child credentials. See the [subagent configuration contract](cmds-delidev-subagents-contract.md#codex-child-configuration).

## Original OpenCode foreground task guard

The Go relay validates complete OpenCode Chat Completions foreground task arguments before executable function-call frames reach the native process. Bounded private buffering preserves the original frames and order across split function names/arguments and intervening heartbeat frames. Reject reused `task_id`, background execution, unknown/duplicate fields and incomplete arguments before forwarding any tool-call frame. Nonstream responses use the same closed task input validation. A truncated, canceled, malformed or oversized stream discards retained private frames and grants no native side effect, direct fallback or provider resend. Native dual-proof child observation remains independently required after execution. Task arguments and buffers never enter logs, request diagnostics or storage.

## Original Codex reviewer relay
An API execution configured for AI review freezes one additional canonical native model allowance, `gpt-5.6-luna`, alongside its original root and optional canonical child model. Only Responses creation may narrow the original lease to that reviewer; it retains the same provider/profile, account, protected credential generation, cancellation and request gates. No wildcard, metadata-discovered model, fallback model, subscription-native alias or provider conversion is permitted. Recheck original assignment/account authority before forwarding. Reviewer history references occupy an independent private model slot and cannot become root/child history. No catalog model record, new account, credential or SQLite migration is introduced.

A proved reviewer relay request has the closed builtin attribution `codex-reviewer-gpt-5.6-luna`, with no borrowed catalog model ID. Diagnostics remain metadata-only. Root/child requests retain their own original attribution. A model name shared with the parent/child does not distinguish a raw native reviewer completion; preserve unknown usage attribution under the [usage contract](cmds-delidev-usage-contract.md#codex-reviewer-attribution).

## OpenCode Go subscriptions
The [OpenCode Go contract](cmds-delidev-opencode-go-subscription-contract.md) owns the exact key-backed `opencode_go` exception, fixed server relay profile, original native session header and independently confirmed cleanup. Identity 4, System 54 and Worker 28 retain separate ownership; System 52 remains Project behavior. Native login and quota authority remain unavailable. No migration is added.
