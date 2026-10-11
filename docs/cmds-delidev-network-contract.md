# DeliDev explicit outbound network contract

## Known model catalog downloads

The advisory subscription catalog is an explicit server outbound operation under
[the catalog contract](cmds-delidev-catalog-contract.md#known-subscription-model-suggestions).
Use the existing outbound resolver for one immutable route per attempt, including
its protected proxy credential handling and independent route failures. The only
destination is the compiled HTTPS repository-main raw catalog URL. No client URL,
redirect, environment proxy, native credential, inference call or direct fallback
is accepted. Bound HTTP to 15 seconds/1 MiB and cancel/join on server shutdown.
Structured logs contain catalog version/date and stable failure codes only;
responses, transport errors, proxy addresses and secrets never enter logs.


## Scope
The feature own explicit server/Worker routing, recipient-encrypted bootstrap and authenticated generation reconciliation. The feature owns the separately leased Codex API tunnel in `internal/nativeproxy`; native subscription traffic retains its independent original-owner scope.

## Runtime and Language
Go, the existing server SQLite authority and Connect RPC. No ambient proxy discovery is permitted.

## Users and Operators
Authenticated owners and paired clients configure named profiles and explicitly select a revision for the server or one registered Worker machine. Workers cannot invoke these owner operations.

## Interfaces and Contracts
NetworkService exposes SaveNetworkProfile, DeleteNetworkProfile, SelectNetworkProfile, GetNetworkRoute and ExportWorkerNetworkMetadata. System status advertises the pre-reserved `SERVER_OUTBOUND_PROXY_V1 = 6` alongside its existing independent capabilities. `network.proto` owns the service and exclusive messages; shared profile/route enum numbers are 28/29. Resource kinds `network_profile` and `network_route` participate in normal authenticated reads, snapshots and metadata events; generic configuration writes cannot create them.

Profiles use closed `direct`, `http`, `https`, `socks5` modes, a 128-byte name, canonical ASCII host/IP and nonzero port for proxies. Direct has no endpoint, bypass or credential. At most 128 profiles and 128 bypass entries per profile are permitted. Bypass entries contain exact host, literal IP or canonical CIDR plus optional port; no suffix, wildcard, environment or DNS-answer interpretation is allowed. DNS names are lowercase with no trailing dot; ambiguous numeric IPv4 spellings are rejected.

Save uses schema 1 definition JSON plus separate write-only credential JSON with visible ASCII username/password of 1–255 bytes; usernames cannot contain colon. Omission preserves an existing credential only when mode, canonical host and port remain unchanged. Editing any of those authority fields clears the new profile association unless explicit write-only input binds a fresh credential generation. Name and bypass edits alone may retain the credential; explicit clearing always removes its active association. Independently pinned old routes retain their original authority and generation until coordinated profile deletion. Creation uses the request UUID as the stable profile ID. Every mutation binds the actor, original request UUID, exact expected revision and full canonical input; credential input is bound by a domain-separated keyed commitment, never an unkeyed digest. Selected profile revisions are immutable snapshots. Editing a profile requires a later explicit selection to change effective routing. Empty profile selection means built-in Direct. The server target is empty machine ID; each Worker has a separate target and monotonically revisioned desired generation. A selected profile cannot be deleted. In-flight requests retain their original pinned route; later selections affect new requests and never replay accepted work. Profile deletion proves native credential-store cleanup separately from completion of those bounded requests.

Metadata export is an authenticated, read-only operation for an exact Worker desired generation. Its signed non-secret metadata binds format version, server ID, Worker machine ID, selection identity/generation, immutable profile configuration, issue/expiry times and a random export UUID. It expires after five minutes. Preserve the exact returned metadata bytes and signature; numeric parsing/reserialization is not verification authority. It is not an encrypted credential bundle, Worker installation receipt, recipient binding or proof of native application; those belong to .

CLI: `network profile save|delete|list|get`, `network select`, `network status` and `network export-metadata`. Mutations use the common request ID and exact decimal revisions; secret input enters stdin independently of public definition files. Ordinary output contains only public resources and signed non-secret metadata.

### cmds/delidev-cli/internal/cli constraints

- The `network` command family uses authenticated NetworkService under `cmds-delidev-network-contract.md`. Keep exact decimal revisions and request IDs; definition files, server authentication and proxy credential stdin must be independent. CLI response deadlines exceed bounded server credential work. Read status/export metadata never claims native Worker application.

- Worker network prepare/import and owner export/status preserve the original protected recipient scope. Bound public recipient input and encrypted transfers; require separately supplied ciphertext digest, private atomic output and original exact route revision. No stdin secret, decrypted derivative, private key or proxy credential may enter ordinary JSON output or logs.

### cmds/delidev-cli/internal/domain constraints

- Explicit outbound profiles and immutable selections follow `cmds-delidev-network-contract.md`. Keep closed Direct/HTTP/HTTPS/SOCKS5 modes, bounded exact-host/IP/CIDR bypass rules and separate write-only credentials; never interpret DNS answers or wildcards as bypass authority.

- Worker network bindings/status use closed route states and exact decimal-string generations under the network contract. Separate desired/effective/native generations, original recipient scope and immutable active route copies. Public key or attachment metadata grants no pairing, execution, decrypted cache or observed native capability.

### cmds/delidev-cli/internal/knownmodels constraints

- Follow the catalog and network contracts in `docs/` and parent ownership rules.

- Fetch only the compiled repository-main HTTPS URL through the server outbound route. Keep 15-second/1 MiB limits, 24-hour success/one-hour failure scheduling, private atomic cache and joined shutdown.

### cmds/delidev-cli/internal/outbound constraints

- Resolve one immutable server route per HTTP attempt. Preserve destination TLS, bounded CONNECT/SOCKS5 handshakes, cancellation and the callers' existing body/stream limits.

- Never discover ambient proxies, retry a connection, or fall back to Direct or another profile. Exact bypasses are explicit direct authority.

### cmds/delidev-cli/internal/server constraints

- Keep business logic in Go and product communication in authenticated Connect. Workers initiate outbound connections; never add a client-facing WebSocket or SSE API.

- NetworkService is owner/paired-client-only under `cmds-delidev-network-contract.md`. Preserve actor/request/revision receipts, immutable server and independent Worker selections, bounded vault generations and denial-before-cleanup deletion. All server provider, inference and GitHub clients use only the explicit server route without ambient settings or fallback. Exported Worker metadata is signed, non-secret desired-generation evidence, not native application or credential-transfer proof.

- Worker network export/control follows `cmds-delidev-network-contract.md`. Pin each pending grant to one original machine/device/recipient, preserve actor/revision receipts, and require authenticated imported-digest acknowledgement before effective generation. Recheck fresh dispatch/claim/registration under current generation while preserving original active leases and cleanup. Native route reports retain exact job/execution/instance/device/generation ownership; an observed old runtime cannot authorize another launch or fabricate current application.

- Worker Attach retries preserve immutable acceptance receipts while projecting current Machine discovery and network status in one authorized read. Recheck the paired device, original machine/instance and active Machine before returning observations; do not repeat attachment writes or promote unchecked installations. Follow `cmds-delidev-network-contract.md`.

### cmds/delidev-cli/internal/store constraints

- Pairing codes are single-use/expiring; raw codes and device tokens stay out of SQLite and ordinary output. Workers have machine-scoped authorization and initiate outbound Connect streams. Revalidate revocation and job ownership at mutation commit boundaries, and retain uncertainty after a disconnected or replaced Worker instance instead of redispatching accepted execution.

- Explicit outbound profiles/routes reuse the existing entity/event/receipt transactions under `cmds-delidev-network-contract.md`. Keep one revisioned selection per server or registered Worker, reject deletion of selected profiles, and exclude network authority from generic configuration import/export. Protected credential bytes never enter SQLite.

- Worker network transfer, pending-pairing and native-runtime pins reuse existing metadata with strict original UUID/generation/digest validation. Keep ciphertext and protected derivatives outside SQLite. Remove native job pins with ordinary/session job purge; preserve pending pairing and current routing through their independent authority and restore boundary. Periodic unchanged acknowledgements do not republish Machine state.

### cmds/delidev-cli/internal/worker constraints

- Session Git comparisons use the bounded outbound workspace read channel, original repository manifest and independent process owner. Preserve current HEAD versus index versus immutable Worktree creation-commit semantics, including explicit unborn empty-tree evidence. Never create a Local filesystem baseline, run external diff/textconv/fsmonitor helpers, silently apply clean/process filters or mutate native ownership. Working-tree diffs use a private temporary Git admin with a highest-precedence `!filter` rule so a raced attribute or index change cannot invoke a clean/process driver; check active filters before and after the diff. Keep untracked paths and submodule Gitlinks explicit, hash complete returned observations, reject oversized/mixed/foreign data, and grant no review or execution authority from patch text.

- Workers must keep receiving the outbound work stream during owned native work. Stream loss/revocation or 45 seconds without a heartbeat cancels the active operation, joins native cleanup, and durably retains its outcome before reconnect. The server sends one unresolved assignment per stream and leaves later jobs queued until the prior result resolves. Bound received assignments and never convert lost transport into successful completion or automatic re-execution.

- Claude first dispatch composes the pinned `2.1.236` Anthropic Messages profile with current Worker/account/model readiness and complete original workspace ownership. Freeze immutable selection/input/routing before the real outbound Worker runner; reject unsupported options before claiming; multiple repositories use the independently validated ordered-root profile below. Claim native input once, publish the original initialization/replay/progress/content/usage/callback/terminal facts through the shared durable outbox, join direct response controls and native EOF/workspace closure, then report version 1. Never drop unknown observations or grant later input from this completion. Targeted pre-acceptance cancellation contains the owned process and retains recovery. Accepted streaming Stop/Archive uses one synchronized original native interrupt and a bounded grace under the original Worker stream; publish its separate original Stop proof only after partial-response/context/result/command/idle validation and joined native cleanup, followed by a separate workspace completion report. Never invent a native result input ID or continuation authority. Keep opt-in installed-native scripted-provider evidence distinct from fixture protocol discovery, hosted accounts and other platforms.

- Manual Git authentication remains in the separately owned Worker bridge, never reversible harness environment variables. Pin scope/configuration, isolate local-command lookup from network authentication, authenticate push claims with a parent-only key, and cancel/join the bounded loopback bridge before proof or lease release.

- Encrypted network bootstrap follows `cmds-delidev-network-contract.md`. Prepare the original protected X25519 identity before pairing; require an independently supplied digest on import. Keep attachment replay input frozen, synchronize current generation before work and join control/account lanes with their shared original protected runtime. Stale or missing authority cannot fall back to Direct. A claimed Codex API/title listener captures immutable generation/credentials, cannot be recreated on replay, and joins before process/workspace cleanup or checkpoint proof.

- Forwarding control and traffic share the selected network runtime with the primary lane. Failure to load its protected authority cancels and joins the lane; no Direct fallback is allowed.

### cmds/delidev-cli/internal/workernetwork constraints

- Follow `cmds-delidev-network-contract.md` and parent instructions. This package owns bounded X25519-only transfer and the protected derivative cache, not authoritative profile editing, pairing admission or native execution.

- Import requires the separately obtained ciphertext digest plus independently selected exact server, endpoint, machine, device, pairing and original recipient/key identity. Encryption alone cannot authenticate an export.

- Preserve atomic complete cache publication and monotonic generation; same-generation conflicting authority is recovery-required. Native runtimes retain independent bounded Go copies while new control attempts adopt the newly reconciled cache.

## Storage
Accepted network receipts retain their original request identity and replay classification independently of the live resource. A later authoritative resource deletion returns `deleted = true` with no resource; it never recreates the entity or rewrites credentials. Authorization, cancellation and storage errors remain failures, and altered input cannot reuse the accepted receipt.

An exact completed deletion retry checks existing private recovery metadata but creates no new cleanup intent and does not reopen the vault when no obligation remains. Existing pending accepted deletion intents still require independently confirmed cleanup before replay succeeds.

Existing revisioned entities, events and actor-bound receipts hold public profile/configuration and opaque credential generation references. No SQL layout migration is required. Credentials use the protected server vault's `network-proxy` purpose and immutable profile/request UUID references. At most 256 unresolved credential generations per profile are retained, including pinned selections and exact uncertain retries; profile deletion commits denial before retryable native cleanup. Every network RPC has a 30-second cancellation bound; CLI response waits allow 35 seconds, with exact receipts retaining cleanup retries. Before public profile removal, the shared vault gate synchronizes one authenticated private deletion intent, capped at 64 KiB and bound to the server and original actor/request/revision. Save, Select and Delete reconcile that server-owned obligation under current owner/client authorization, independently of the original actor's later revocation. Only an accepted original deletion receipt plus an authoritative absent profile permits native cleanup; a proved absent receipt retires metadata without touching credentials, and uncertain proof or failed cleanup blocks new network mutations. A fresh authorized deletion of the same pending profile/revision may receive its own receipt after confirmed cleanup; it cannot reuse another actor's request ID. At most one unfinished profile deletion remains, survives restart, and clears only after confirmed native removal. Recovery runs within the initiating RPC and its cancellation bound. Before each native credential write, the shared vault gate synchronizes one authenticated private publication intent outside SQLite, capped at 64 KiB and bound to server, original actor/request/full input and immutable credential reference without secret bytes. At most one unpublished network generation may remain across all profiles. Exact retry preserves that original generation and rejects changed public or secret input. A distinct request first compares the original SQL receipt: an accepted publication clears only the intent, while a proved absent publication records cleanup denial and removes that native generation before a replacement write. Unknown receipt or cleanup proof blocks replacement; an abandoned generation cannot be reused. The intent survives cancellation, database failure and server restart, and successful publication clears it only after SQL commit. SQLite/configuration backups do not back up these credentials or their private publication/deletion intents. Configuration import/export does not acquire network authority. Managed database restore refuses unfinished private publication/deletion intents and preserves current profiles, immutable credential-generation references and server/Worker selections from its safety image, rather than restoring historical routing. It holds the shared credential gate through publication and never restores native credentials.

## Security
Managed restore retains the current machine descriptors required by preserved
Worker routes, including Workers added after the selected backup. Owners and
paired clients can read and explicitly clear those routes and delete deselected
profiles without re-pairing the Worker. Preserved metadata does not restore Worker
verifiers, leases, execution grants or native ownership.

Only server selection affects catalog, credential validation, inference relay and every GitHub adapter. Worker selections never affect server routing or listener/account selection. Each HTTP attempt resolves one immutable route and obtains its exact protected credential. Requests have no direct or alternate-profile fallback, redirects or proxy-owned retries. Direct/exact-bypass routing pins localhost to actual loopback before invoking any caller base dialer, using the existing IPv4-then-IPv6 loopback policy. Cancellation stops that same-route family selection; the original request authority still owns HTTPS SNI and hostname verification. Ordinary nonlocal DNS remains owned by the caller dialer. Explicit proxy routing retains the destination authority. Plaintext loopback HTTP destinations are rejected before any connection when a selected proxy lacks an exact host/IP/CIDR and optional-port bypass; Direct and explicit bypasses keep their account keys on the server loopback. There is no automatic route change. Verified HTTPS loopback destinations may still use the selected proxy. HTTP/HTTPS proxies tunnel permitted destinations with CONNECT, keeping proxy authorization out of URLs and origin request headers. HTTPS proxy and destination TLS use normal certificate/hostname verification. SOCKS5 uses bounded context-aware authentication/dialing. CONNECT headers are bounded to 32 KiB; handshakes are bounded to ten seconds and cancellation closes owned connections. Existing client body/header/stream/deadline bounds remain in force. A bounded response guard withholds unresolved protected prefixes and rejects literal, JSON-escaped and Base64 credential reflections in headers/bodies. Body string matching incrementally decodes valid JSON escapes, including mixed spellings and surrogate pairs in names, values and JSON SSE data; it holds only bounded matching suffixes and incomplete escapes at their original wire offsets. SSE framing uses the response media type or bounded initial-field detection for typeless streams. Only joined data fields enter JSON decoding; comments and other metadata cannot alter string state. Each record resets that state, while literal protection covers all original body bytes. Allowed content retains its exact wire bytes, and a rejection closes the original body before catalog or response publication; forms shorter than eight bytes match complete byte tokens, with ASCII letters/digits/underscore/hyphen and non-ASCII bytes treated as token continuations. The guard retains complete short candidates until another byte or EOF establishes their right boundary, and tracks the preceding emitted byte across reads. Header values have independent complete-value boundaries, and header names match case-folded protected forms because HTTP canonicalizes field names. Credential-bearing routes suppress response trailers and their announcements on a separate caller response, preserving body streaming while the transport populates only its private trailer map at EOF. These clients do not acquire trailer-based authority. It does not claim substring protection for short forms inside unrelated tokens or arbitrary transformation detection.

## Logging
Structured `network_profile_saved`, `network_profile_deleted` and `network_route_selected` metadata includes opaque profile/machine IDs, selected profile revision, closed mode for profile saves and replay classification. Publication recovery emits `network_save_cleanup_pending` and `network_save_cleanup_completed` with only opaque profile/request IDs and a stable error code for pending cleanup. Deletion recovery emits `network_delete_cleanup_pending` and `network_delete_cleanup_completed` with the same metadata limits. No credentials, endpoint host/URLs, bypass entries, raw network errors or export authentication values enter logs.

## Build and Test
Run `go test -race ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, and `pnpm proto:check`. Controlled CONNECT/SOCKS5/TLS fixtures must deny direct paths, prove exact bypass and no fallback, preserve cancellation and TLS verification, and cover all outbound client integrations plus Worker/server separation. For each outbound client, CONNECT and SOCKS5 fixtures deny direct access, return 302/307 to a second recorder, and prove the recorder receives no request. Transmitted cancellation joins the original origin and adapter without retry; relay revocation may abort the downstream response. Independent protocol capabilities, public snapshots, event history, SQLite and structured logs are checked for credential isolation. Use temporary state and injected test vaults only. Fixture success does not prove enterprise proxy, real provider/GitHub accounts, native OS credential or Worker bootstrap acceptance.

## Dependencies and Integrations
Uses the existing server vault, resource authority and generated Go/TypeScript clients. Protocol implementation follows [Go HTTP transport](https://pkg.go.dev/net/http) and [context-aware SOCKS5](https://pkg.go.dev/golang.org/x/net/proxy).

## Change Triggers
Update scoped command/protocol/package AGENTS, the project index, CLI and evidence contracts when routing, secret ownership, generations or export semantics change.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References
- [Project](project-delidev.md)
- [Protected credentials](cmds-delidev-credentials-contract.md)
- [Native relay](cmds-delidev-proxy-contract.md)
- [Repository defaults](repository-defaults.md)

## Recipient-encrypted bootstrap foundation

The feature adds `internal/workernetwork` for bounded age 1.3.2 X25519-only encryption/decryption and protected derivative storage. Its transfer binds the exact server origin/identity, machine, device, pending or original pairing, recipient/key identity, immutable route and monotonic generation. Import independently requires the SHA-256 ciphertext digest from the authenticated export response; encryption is not sender authentication. Transfer plaintext is capped at 48 KiB, ciphertext at 96 KiB and export validity at five minutes. Native keys and complete derivative contents use the existing OS-key-wrapped credential Vault with independent `worker-network-key` and `worker-network-config` purposes. Ordinary metadata retains only public recipient and opaque scope/reference/generation/ciphertext-digest fields. Missing protected material and locked key storage cannot create replacements or fallback.

One private import gate serializes publication. The complete protected derivative is written before the atomic metadata pointer; failed publication retains either the old complete cache or the new complete cache, and an uncertain first key publication refuses regeneration. Same-generation identical authority reuses its original reference despite a fresh transfer nonce, while conflicting authority and older generations are refused. After successful publication, older derivative references are denied and cleaned; already running native owners may retain only their independent bounded Go memory copy until joined cleanup. Loading a cache alone grants no offline readiness, sender authority, dispatch or observed native route use. Authenticated export/control generation reconciliation and pairing independently admit this cache. Native API application has its own runtime claim and observation, described below; cache import alone cannot grant it. Fixture/build validation remains distinct from native/account/platform acceptance.


### Authenticated Worker generation admission

`ExportWorkerNetworkBundle` is an actor/request/revision-bound owner/client mutation plus bounded authenticated ciphertext transfer. A pending single-use Worker grant pins one original machine/device/key/recipient; another prepared identity cannot reuse it. Existing paired exports require that exact original active Worker. `GetWorkerNetworkStatus` is an authenticated owner/client read. `SyncWorkerNetwork` and `ReportWorkerNativeRoute` are original-instance Worker-only control RPCs; they grant no owner operations. Public transfer pins in existing private metadata contain only original UUIDs, generation and ciphertext digest. No SQL migration or plaintext credential record is introduced.

Worker attachment reports public observation only. Exact attachment replay preserves the original acceptance receipt and does not repeat its Machine write. Its response reads current Machine discovery and network status together under the current paired device and original machine/instance authority; replaced instances, revoked devices and disabled machines remain denied. This observation grants no native execution support to unchecked or unavailable installations. Current desired generation becomes effective only after authenticated synchronization compares the original imported digest, recipient and scope. Periodic unchanged acknowledgements are read-only and retain existing receipt collision checks. Missing protected material, mismatched identity, stale generation, revocation and unsupported old-server capabilities block fresh dispatch, claim and execution registration. Stale routing does not prevent control synchronization or original cleanup. An already accepted active runtime keeps its immutable generation; a later selection cannot replace its credentials or replay its execution.

CLI preparation is `worker network prepare`; import requires `worker network import --input ... --expected-ciphertext-digest ...`, with the digest separately obtained from the authenticated export response. `worker network status` returns public recipient scope only. Owners use `network export-bundle` and `network worker-status`. Preparation precedes pairing when the selected route is required to reach the server. The original protected cache routes Worker control and protected account lanes without ambient proxy inheritance or automatic Direct fallback.

### Owned Codex API tunnel

For the pinned Codex API execution and title profiles, Go claims one original job/execution/instance/generation before creating a fresh authenticated `127.0.0.1` listener. Exact claim replay grants no listener. It accepts only the paired server's exact canonical host/effective port; no alternate hostname, credentials, port or destination is admitted. HTTPS remains an unchanged end-to-end CONNECT tunnel with destination TLS owned by Codex. Plain HTTP permits only the original loopback authority, an explicit exact bypass and the fixed Responses creation/compaction relay paths; upstream credentials never accompany plaintext execution traffic. Direct selections need no listener. Other native profiles reject non-Direct execution admission until they have their own validated adapter.

The tunnel captures a bounded immutable route/credential copy, supports explicit HTTP/HTTPS/SOCKS5 profiles, bounds local sockets to 16, headers to 32 KiB, copy buffers to 32 KiB, connection/header setup to ten seconds, idle copying to thirty seconds and total tunnel lifetime to fifteen minutes. It never retries or falls back. Cancellation joins the listener, every local/upstream socket, copying and observation before cleanup/checkpoint completion. Native readiness remains `unverified` until actual accepted-target bytes traverse the route; `observed` proves route use, not provider success or inference. Desired/effective/native generation are independent fields.

Only the ephemeral loopback credential and URL enter the isolated native environment. Reconstruct upper/lower proxy variables, clear ambient bypass/ALL_PROXY and pin `features.respect_system_proxy=false`; verify the effective merged configuration before thread start/resume. Exclude execution tokens and all proxy variables from native shell children, rejecting explicit shell setters. Upstream proxy credentials remain solely in Go. Native frames pass finite original/decoded-JSON/Base64 reflection protection before publication; this is not a claim against arbitrary transformations or cross-frame covert encoding. No credentials or raw native content enter state, events, public status or logs.

### Desktop and capability composition

System capabilities 20 (`WORKER_NETWORK_BOOTSTRAP_V1`) and 21 (`WORKER_CODEX_PROXY_V1`) expose the authenticated implementations, independently of negotiated Worker 9/10 and original native installation support. CLI export/status and desktop controls reject unsupported older servers explicitly. The allocation reservations themselves granted none of this authority.

Server preferences and Runner Device inspection contain a collapsed Network settings workspace with bounded profile pagination, explicit revision selection, write-only credential editing and deletion confirmation. Worker views keep desired/effective/native generations and closed states separate. Encrypted export accepts only a bounded original public recipient for the current server/endpoint; a pending grant may inspect its initial absent route without pairing or execution authority. Legacy signed metadata remains registered-machine-only. Ciphertext/digest are transient mutation results, never persistent query data.

The trusted desktop additionally offers Prepare/Import/Status for its already registered matching same-computer Worker. Go owns protected key creation and import. Rust authorizes the original main/saved window, fixes the Worker root, bounds ciphertext to 96 KiB and rechecks saved-window identity after its joined sidecar command. It accepts no renderer path, endpoint, key or connection selector and rejects additional/private result fields. Imports retain exact decimal generation strings. Remote Workers use the equivalent CLI, including saved-connection-scoped commands; pending preparation with the original grant remains available before a route is required to reach the server. Preparation/import neither registers nor restarts the Worker. A newly prepared running legacy Worker requires explicit encrypted import and a user-requested restart; only subsequent authenticated synchronization grants readiness. Settings disposal revokes late presentation, not accepted native/server effects.

- Same-generation import reconciliation retries obsolete protected-derivative enumeration/deletion after current cache publication; retain the committed current reference through cleanup failure. Transferred bundle issuance allows at most 30 seconds of clock skew between hosts while enforcing the original absolute expiry and five-minute lifetime.

Forwarding control and traffic use the same protected route resolver and joined lifetime as primary Worker control. Within one immutable claimed native runtime, Observed route use is monotonic despite concurrent or delayed failed sockets; this proof does not imply provider success.

## Local desktop execution relocation

Local registration endpoints remain immutable authority. Only a validated fixed Local Worker pairing may resolve a same-server private desktop execution target; every bearer request proves its generation first. Wrap the existing selected outbound transport for both proof and business requests without route fallback or ambient proxy discovery. Retired/missing authority after the durable opt-in marker fails closed; Saved/remote endpoints and execution eligibility remain unchanged.

## Provider reference token prices
`internal/tokenprices` owns the separate bounded reference-price collector. It fetches only `https://models.dev/api.json` through the server-selected outbound route. Reject redirects, ambient proxies and direct fallback. Use a 15-second request deadline, 16 MiB response limit, at most 512 providers and 50,000 models, with 10,000 models per provider. Coalesce an ongoing refresh; keep the last valid private atomic cache after failure, including restart-stable failure backoff. Success checks are due after 24 hours; failed attempts cannot retry before one hour. The server joins this owner at shutdown.

The collector grants no account, model or execution support. Automatic application requires the complete source/native-ID pricing and inline-model/reset activation in the catalog and usage contracts. Original source identity, manual policies and first-retention estimates remain independently authoritative. Log phase, duration, bounded counts and stable failure codes only; never emit downloaded content, credentials, endpoints, usage or user state.

### GitHub avatar route

Server-owned PR avatar reads use the same selected outbound resolver, credential
isolation and no-redirect policy as other GitHub attempts, with no token parameter
or origin credential headers. Only retained exact GitHub numeric user locators
are admitted. Fetches have five-second and 128 KiB bounds; supported decoding is
limited to 1024 by 1024 and emitted PNGs are sanitized and at most 64 by 64.
Client-supplied URLs, redirects, arbitrary origins and ambient proxy fallback are
never accepted. The authenticated read returns bytes rather than a remote URL,
so desktop remote-image CSP authority does not expand.
