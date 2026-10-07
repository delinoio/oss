# DeliDev managed Codex subscriptions

## Failed subscription cleanup reservations

Issue #964 reserves System `FAILED_SUBSCRIPTION_CLEANUP_V1 = 41`, the
`CleanupFailedSubscriptions` and `GetFailedSubscriptionCleanup` request/response
messages, `FailedSubscriptionCleanupJob` and `FailedSubscriptionCleanupResult`,
and the closed cleanup state/outcome/reason enums in `allocations.json`.
Establish this complete reservation on main before dependent implementation.
The request reserves the original request ID; status reserves original job ID
and pagination token. Responses reserve the job, original receipt identity,
replay flag and bounded result pagination. Job metadata reserves ID/revision,
state, total/processed/deleted/retained counts and safe problem code. Results
reserve account ID/alias, outcome, reason and safe problem code.

The planned button deliberately starts one server-wide cleanup without another
confirmation. Only failed, canceled, expired, unsupported or interrupted initial
ChatGPT server logins without independent authentication or Worker ownership are
candidates. Original native and protected-credential cleanup, fresh revisions,
complete retained-reference checks and deletion receipts remain authoritative.
Ordinary disconnected accounts and active logins remain outside the batch.
Reservations introduce no active schemas, generated bindings, capability
advertisement, native cleanup, configuration deletion or database migration.

## Planned native Claude subscriptions

The owner-approved Claude extension includes browser login, reauthentication,
logout and session execution on an explicitly selected local or remote Runner
Device. It reserves System `CLAUDE_SUBSCRIPTIONS_V1 = 38`, Worker
`NATIVE_CLAUDE_SUBSCRIPTIONS_V1 = 20` and the protocol closure on main before
implementation. These reservations do not change the currently unsupported
Claude lifecycle or activate any capability.

The planned original installed Claude Code 2.1.236 process owns authentication
in one private account-specific `CLAUDE_CONFIG_DIR`. Existing personal logins,
external token input, Console/API login, credential transfer between machines
and Claude quota/credit operations remain excluded. Native credentials stay on
their original Runner Device; the server retains only profile references,
identity commitments, generations and original lifecycle/execution ownership.
Single-use browser approval input reaches only that original native login.
Login/status/logout and execution must retain independent native evidence;
status alone cannot prove token refresh, process cleanup or deletion. This
extension adds no database migration. Follow the protocol and structure
contracts for main-first reservations and independent complete feature delivery.

## Scope

Issue #1095 implements dedicated Codex subscription login, refresh, execution and logout in `cmds/delidev-cli`, `protos/delidev/v1/subscription.proto` and the generated DeliDev clients. The server owns authorization, encrypted credentials, generations and exclusive account leases. An explicitly selected paired Worker owns execution processes and its private authentication files. The independently negotiated server login lane owns browser login, authentication refresh and logout without a Worker. The complete product requirements remain in [issue #964's snapshot](cmds-delidev-requirements.md).

Existing-login import, externally supplied token bundles, internal-only `chatgptAuthTokens`, Claude subscriptions and concurrent use of one managed bundle are excluded. Desktop login controls and native-owner quota/reset-credit operations are implemented together in the subscription lifecycle feature PR. Full native recovery and real-account/platform acceptance remain separately identified; fixtures cannot establish them. This implementation does not complete issue #964 or claim a release.

## Runtime and Language

Go owns server and Worker business logic. The native profile pins installed Codex `0.151.0`; DeliDev never installs it. The managed profile uses the official app-server protocol, a fresh private `CODEX_HOME`, file-backed native authentication and the built-in OpenAI provider. API execution and discovery retain their existing ephemeral credential profile. The current native Fork coordinator uses that API profile and rejects managed subscription sources before accepting work. Source inspection cannot grant credentials; managed Fork requires its own verified protected lease and joined write-back.

## Users and Operators

The server owner or an authorized paired client initiates lifecycle operations. A paired Worker advertises `managed-codex-subscriptions-v1` only after an empty-home native configuration handshake and owned cleanup; the server echoes this closed capability. Execution and explicit-machine lifecycle operations select an execution machine. Independent server lifecycle operations omit that selection. Workers cannot initiate owner lifecycle operations or read another machine's bundle.

Execution admission requires that selected Worker's negotiated managed capability before claiming a session or consuming queued input. Ordinary native installation discovery alone cannot admit subscription execution; an unsupported Worker leaves the original input queued without selecting a fallback.

## Interfaces and Contracts

Subscription accounts use schema 2 with required closed `subscription_service: "chatgpt" | "claude" | "grok"` and no `provider_id`. Native models use schema 2 with `source_kind: "subscription"`, the same service and no API Provider. Services map exactly to Codex, Claude Code and Grok Build; a model's single compatible harness must match its service. API accounts/models keep their existing schema-1 provider identity and forbid subscription fields. Neither service metadata nor model registration grants native login, quota or execution support.

The historical provider-bound shape used `native-subscription`, `subscription` authentication, an empty endpoint and optional `subscription_harness: "codex"`. Migration 28 retires every legacy subscription account and those providers/models without inferring a service from any field or display name. That shape is retained only as read-only historical metadata under the [storage contract](cmds-delidev-storage-contract.md#subscription-retirement-issue-1235). New native-subscription Providers and edits that replace service/account/model identity are rejected.

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

An independently authorized outbound `WatchSubscription` lane supports four concurrent account operations per Worker. `TakeSubscription`, `PublishSubscriptionProgress` and `FinishSubscription` are restricted to the selected current Worker/device/instance. Bundles travel only in the protected Take/Finish messages, with a 64 KiB bound; they never enter jobs, execution output, events or ordinary resource responses. Take receipt replay never redistributes a bundle or authorizes another native launch.

One durable account lease spans login, refresh, execution or logout. Refresh/logout requests can wait behind an execution lease. Other sessions retain their selected account and wait after a definite busy refusal; unknown delivery is never retried. Other accounts acquire independent leases. Native execution uses an immutable subscription selector and the existing publication/history/workspace machinery. Its publication registration exposes no API proxy path and cannot obtain an API relay lease.

A lifecycle Take that races another lease grant retries only a definite busy refusal, using the exact original claim, account, operation and revision. This wait preserves the shared Worker connection and the execution that won ownership; caller cancellation joins it. Unknown delivery still closes the ownership lane and cannot be retried. The lifecycle revision remains its original positive account observation, not a requirement that later cleanup or metadata edits leave the account unchanged. Take rejects future observations and independently requires the same uncanceled queued operation, action, selected machine and currently authorized initiating actor; cancellation, replacement and recovery cannot gain authority from an old observation. Owner lifecycle requests still require the current account revision, and execution Take still requires its exact claimed job revision.

A confirmed pre-native execution failure returns the byte-identical unused original bundle over protected Finish with cleanup true and success/refresh false. This includes a failed Open only when owned process closure and unchanged-file cleanup are independently confirmed. The server compares the returned bytes with the immutable vault generation before releasing the lease, preserving its generation, connection, health and any independently queued refresh/logout. Cleanup alone, missing bytes or changed authentication cannot establish this outcome; uncertain execution/refresh retains blocked original vault material and recovery ownership. Explicit logout retains its separately authorized credential-removal semantics.

If an atomic authentication write fails before publishing its destination and before native ownership, confirmed cleanup instead requires the original private home, synchronized and rechecked destination absence, and the complete bounded retained-file credential scan. A leftover temporary credential file, a missing/foreign home or an uncertain observation retains the fence. Reject every retained entry in the atomic writer's `.pending-` namespace, including empty or partial files and nested entries, independently of token matching. Published authentication follows the unchanged original-byte comparison/removal path; native-owned cleanup cannot substitute file absence for its captured-bundle proof.

The lease pins operation, account generation, Worker machine/device/instance, server epoch and original lease revision. Completion checks that original revision while preserving later metadata edits and cancellation. The connection ID remains stable across bundle rotations. The final native bundle is saved under a new immutable generation, old protected references are tombstoned, and independently confirmed native/file cleanup precedes another grant.

Finish's ownership-error guard ends after the ownership mutation and final required vault cleanup commit, before its response-resource observation. A canceled or failed presentation read cannot add a recovery fence to settled state. Failed final cleanup still fences ownership, and accepted receipt replay preserves that fence without repeating vault work.

Successful `account/read` does not prove refresh. Both Worker and server require changed token material, a strictly newer native `last_refresh` value and unchanged account/user identity. Server identity commitments enforce one managed owner for the same provider account/user across account aliases. Local logout proves native/file removal and server vault cleanup; provider-wide revocation remains best-effort and is never reported as confirmed.

The pinned native login-completed envelope includes nullable `onboardingEntrypoint` metadata. Accept null or the pinned closed `life_sciences` value without launching another onboarding flow or treating presentation metadata as authentication evidence; reject unknown values while still requiring the original login identity and successful completion.


## Server browser login and account naming

System `SERVER_SUBSCRIPTION_LOGIN_V1 = 30` activates the shared reservations established on main by PR #1332. It is independent of service-account inventory capability 17 and Worker capabilities. An authenticated owner or paired client can omit `machine_id` for ChatGPT login, authentication refresh or logout. The server must have the verified Codex 0.151.0 installation. No Worker registration, startup or selection is required. Claude Code and Grok remain unsupported. API authentication, execution selection and existing quota authority do not change.

Go records the original actor, operation, server epoch, bounded lifetime, finish identity and closed progress state in optional server-owned `server_operation` metadata. Accepted request replay returns the original receipt without another native launch. Claiming requires the original queued pending operation and no Worker credential lease; `native_started` is an exclusive credential fence. An existing execution lease retains ownership while queued refresh/logout waits. Cancellation is serialized with final publication. Successful native completion, joined process closure, final-file comparison, private-runtime removal, vault staging and old-reference cleanup precede confirmed success. Uncertain cleanup and previous-epoch operations retain recovery ownership and cannot relaunch or redistribute credentials. Optional JSON metadata requires no database migration and does not alter migrations 26–30.

### Failed initial server login cleanup

A failed, canceled or interrupted initial ChatGPT server login can release its account for explicit configuration deletion after independently confirmed cleanup. This applies only to the original server login with no connection, credential generation, identity commitment, Worker lease, native machine owner, quota/reset-credit operation or account removal. Refresh, logout, connected accounts, restored credential generations and Worker ownership retain their separate recovery boundaries. Cleanup never repeats authentication or callback forwarding and never claims provider-wide revocation.

The optional server-owned `server_operation.cleanup_phase` uses the closed values `native-confirmed` and `credentials-confirmed`; omission preserves legacy metadata and means no checkpoint. Joined native closure and synchronized private-runtime removal establish the first phase. The pre-native path can also establish it when the opener never ran. Removing every account-owned `account-login` protected reference precedes the second phase. Only that final transaction clears pending/native/recovery fences and sets disconnected health. Preserve the original operation, failed/canceled/expired/unsupported outcome, safe diagnostic and current configuration preferences. A canceled server context alone cannot retain recovery after both cleanup phases are confirmed.

A later explicitly accepted server or Worker login supersedes this completed server attempt. Worker admission retires only its fully confirmed terminal server metadata before creating the new independent pending operation; an unsettled checkpoint never permits replacement.

Server maintenance makes one 30-second cleanup attempt per original operation in each server process, using the existing shared 16-account lane and account gate. It joins these children at shutdown and excludes a still-running original login. A retry after server restart resumes its durable native checkpoint without another native login. Failed attempts keep their original ownership and do not run again on every 250-millisecond scan.

Legacy claimed logins without a checkpoint require the original private process owner index and valid journals. Independently held native controller locks block recovery; a released controller permits the existing platform ownership reconciler to confirm or stop only that original process tree. Retain the index after journal retirement. Missing individual journals, foreign ownership and file/PID absence alone grant no cleanup proof. After process confirmation, remove only identity-checked original auth and discovery-probe directories, rejecting symlinks and preserving other operations. Legacy metadata with `native_started=false` proves either no native claim or previously joined cleanup; unexpected retained auth/probe files contradict that proof and remain blocked.

Vault, filesystem, authorization or state-publication failure retains the configuration and cleanup checkpoint for a later server process. The Settings deletion screen continues to require a fresh account read and explicit confirmation; cleanup never deletes configuration. Existing revision, retained-reference and independently observed browser-profile cleanup checks remain authoritative. Logs contain only original operation references, cleanup phases and stable codes. Tests use temporary synthetic accounts and native process fixtures; they do not establish installed-app, real-account or all-platform acceptance.

Browser login uses the ordinary `account/login/start` ChatGPT profile, never automatic device-code fallback. The pinned app-server disables its own browser opening; the trusted native desktop opens the returned original address once. Exact native binding replay cannot open another browser. Deliberate reopening uses only that same operation/address. Local desktop login retains the original installed Codex callback. Remote desktop login binds its registered loopback callback on both address families and forwards one bounded code/state/scope query through authenticated `ForwardSubscriptionCallback` to the original server's Codex callback. The callback requires the original state, actor, current server epoch, operation and trusted window/opening lifetime. Duplicate, foreign, expired, canceled and late callbacks are refused. Go durably claims forwarding before HTTP, inherits no proxy, follows no redirects and never resends uncertain delivery or performs another exchange.

Browser callbacks accept exactly `http://localhost:1457/auth/callback` (Codex 0.151.0) or `http://127.0.0.1:1457/auth/callback` (Codex 0.159.2). Preserve the original URL, state and callback authority through browser binding and forwarding; the two spellings cannot replace one another within an accepted operation. Forward only to fixed IPv4 loopback and retain the original callback HTTP Host. Remote native receivers retain both loopback listeners and require that original Host. Other hosts, numeric IPv4 spellings, ports, paths and duplicate query fields are rejected. URL rejection logs contain only the fixed failure classification and original operation reference. This compatibility rule does not recover prior failed accounts or establish actual account/platform acceptance.

Codex 0.151.0 can send `/cancel` to an occupied preferred port 1455 before using its registered fallback 1457. The server first owns a private 1455 listener, refuses a conflict, and retains it through joined native closure. This prevents cancellation of another login and lets the original Codex listener choose its registered 1457 callback. A fallback conflict fails explicitly. Remove this bounded workaround only when the pinned native protocol provides an exclusively owned callback port without foreign cancellation. The remote desktop never remaps its callback or substitutes device authentication.

Typed progress separates preparing, waiting, succeeded, canceled, expired, unsupported, failed and recovery-required. Successful login exposes its exact current generation and one transient name suggestion, chosen from email, provided display name, then ChatGPT within the existing 256-byte UTF-8 alias bound. Native login success remains the authentication evidence; JWT identity claims only project consistent identity. Suggestions belong to the original actor/operation/generation, expire with its lifetime and remain only in memory until the user explicitly saves a name. They never enter resources, receipts or logs. Refresh/logout cannot suggest names.

Settings Add account is an explicit event that creates a default service-named account and immediately requests browser login. Mount, Strict Mode, polling and reconnect cannot repeat creation/login. Only confirmed original success advances to editable name entry. Save uses the current revision and changes only the alias while preserving server-owned bytes; a conflict retains the draft for a fresh explicit save. Later and navigation retain accepted default-name metadata and authentication. Navigation discards the Settings presentation/native callback receiver and rejects late continuations without business cancellation. Only Cancel login requests cancellation.

## Storage

Optional server-owned `Account.subscription` JSON preserves historical account bytes when absent and adds no destructive schema migration. It retains only generation references, keyed identity commitments, pending-operation metadata, original actor and lease fences. Configuration writes cannot manufacture or change this state. Unresolved ownership prevents configuration deletion. Managed database restore also refuses pending, leased or recovery-required ownership. Restored subscription references remain disconnected and recovery-required; historical generations and claims remain evidence, while the external vault is unchanged and supplies no restored authority. Clear an allocated subscription state with no generation, identity, pending operation, lease or recovery fence; settled logout or failed login owns no external reference to quarantine.

The existing OS-backed [credential vault](cmds-delidev-credentials-contract.md) stores bundles with `account-login` purpose and immutable UUID-v7 references. Native plaintext exists only in the exclusive server or Worker private runtime; it is removed after owned process closure. Execution installs credential cleanup at the owned auth-file write boundary, before publisher setup and registration, for both current and predecessor runtime selections. Failures before native ownership remove and synchronize the original auth file; a failed native Open permits removal only when its process cleanup is independently confirmed. Uncertain native ownership retains the protected lease and cannot authorize optimistic deletion. Execution retains original history and checks its bounded remaining files for credential remnants. Worker journals contain only original claim/completion identities and cleanup outcomes, never bundle bytes or token digests. Temporary login presentation lives only in server memory.

Uncertain delivery or completion closes the Worker subscription lane and joins its children so the server records lost ownership. An acknowledged failed operation retains its confirmed completion and leaves unrelated accounts on that lane active. Missing refresh write-back, unconfirmed cleanup, Worker loss/replacement or server restart retains the original lease as recovery-required. The old generation cannot be redistributed. Recovery publication purges the in-memory login presentation under the account gate while retaining the original lease and pending operation. Progress reads reject recovery-required or previous-epoch ownership, and the lost Worker cannot republish a URL or device code. These lifecycle commands neither erase recovery evidence nor clear an uncertain lease; independent native recovery remains a separate product boundary.

## Security

Uncertain protected execution completion escapes ordinary job-error reporting and closes the primary work lane after joined cleanup. The original started job claim and protected completion journal remain retained; the server records the lost execution lease as recovery-required. Workspace cleanup failures preserve this completion uncertainty, and receipt replay cannot manufacture a successful write-back. A successful protected Finish RPC can acknowledge a fenced account; it permits ordinary execution reporting only after final bundle capture and independent cleanup, or the separately verified unused-original pre-native outcome. Acknowledgment alone cannot replace the started job claim.

Once subscription ownership is recovery-required, every new Finish is denied before vault staging or deletion and again at its final transaction. Only an already accepted receipt may replay through this handler; that read-only replay preserves any later retained lease, pending operation and recovery fence. A late background completion cannot replace the independent recovery path.

Remote transport retains authenticated TLS; loopback development retains the existing protected local RPC boundary. Authorization is rechecked after vault I/O and at publication, including the original initiating client's revocation. Managed login starts with no existing authentication file and requires the original native login-completed observation. Bundle validation rejects API keys, external auth modes, unknown credential fields, foreign identities, symlinks and oversized files. JWT claim comparisons are identity consistency checks, not signature verification or substitutes for native OAuth completion.

Login confirmation, refresh and logout share one strict native `account/read` response profile. Codex 0.159.2 serializes the known optional `workspaceRouting` field even with `experimentalApi: false`. Omission and null remain valid for earlier/native disconnected responses. A present object requires the exact original bundle's `chatgptAccountId`, a nonempty account ID of at most 24 KiB without credential-field whitespace/control separators, an HTTPS origin of at most 8,192 bytes without user information, path, query or fragment, and the closed `accountRoutingOverride` enum `NO_CONSTRAINT`, `us` or `us_cr`. An explicit origin port must be nonempty decimal in the unsigned 16-bit range. Discard this metadata after validation; it cannot select Go endpoints, supply credentials, grant authentication or enter resources, receipts or logs. Continue rejecting unknown and duplicate fields at every object depth. Logout requires explicit `account: null`, required OpenAI authentication, absent/null routing and independently absent native `auth.json`. The original native login completion and independently changed file/token evidence for refresh remain mandatory; compatibility never clears an existing recovery fence.

The merged native configuration must retain file storage, ChatGPT login and the built-in OpenAI provider without inherited endpoint, command authentication, bearer, query or header overrides. Native execution never inherits unrelated system logins. Private files do not promise an OS sandbox against unrestricted same-user access. Managed execution nevertheless requires an explicit native read-only or workspace-write permission, keeps the fresh `CODEX_HOME` outside every accepted workspace root after canonical-path checks, and refuses default/full-access or overlapping/aliased paths before writing `auth.json`; this bounds native tool access without claiming unrestricted same-user isolation. All tests use isolated temporary state and synthetic credentials.

Both native thread start and resume recheck the merged configuration for the actual execution workspace before sending the mutation. A successful startup-directory handshake cannot authorize a workspace-specific provider or authentication override.

Thread publication and checkpoint retention/read also bind the native provider to the immutable accepted authentication profile. Managed execution requires built-in OpenAI; API execution retains its existing relay provider. The private checkpoint comparison copies the profile only from accepted configuration, preserves API checkpoint bytes and cannot promote an uncertain subscription lease into recovery authority.

Managed execution captures native identity and the final authentication bundle once before closing the native wire, including before the normal terminal Close. Earlier exits perform the same bounded read before deferred closure, with no automatic retry. After native/process closure, credential cleanup requires the remaining auth file to match those captured bytes, scans retained history and only then submits protected write-back. A terminal event alone establishes none of these cleanup or authentication facts.

Credential cleanup scans retained native files for raw token material and padded or unpadded standard/URL Base64 copies under the existing file/count/byte bounds. Finding a remnant leaves cleanup unconfirmed and retains recovery ownership without erasing the original native history.

## Logging

Use structured `slog` events for accepted operations, grants, completion, capability availability and recovery failures. Log only opaque account/operation/lease/machine identities, closed action, cleanup classification and stable error code. Native protocol messages, token bundles, JWT identities, URLs, device codes and filesystem paths never enter these logs.

Managed native failure logs additionally retain the closed internal `stage`: `login-start`, `login-completion`, `login-cancel`, `account-read`, `bundle-validation` or `local-logout`. The stage identifies the first failed check within its existing safe phase and original operation correlation. It does not change public diagnostic schemas, retain native field values or authorize retry.

## Build and Test

Run `go test -race ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, `pnpm proto:check`, and the API client's tests/typecheck. Generate bindings through pinned root Buf tooling. Controlled native-process fixtures cover browser/device progress, completion, cancellation, file rotation, unchanged refresh evidence, logout and symlink refusal. Real loopback Connect/SQLite fixtures cover lease races, protected-channel authorization, generation fencing, lost write-back, cancellation, identity uniqueness, independent accounts, API relay denial and secret-free outputs/database files.

These fixtures do not authenticate real accounts, execute hosted inference or establish installed-Codex/desktop/Windows/Linux/release acceptance. Record actual executed checks, source revisions and unresolved limits in issue #1095, its pull requests and CI logs/artifacts.

Account-response fixtures cover omitted/null/validated routing across browser/device completion, refresh and logout, malformed/foreign metadata, unknown/duplicate fields and secret-free stage logs. The explicit `DELIDEV_NATIVE_INITIALIZE_EXECUTABLE` empty-home logout smoke exercises an installed Codex's actual `account/read` response and joined process/private-runtime cleanup without opening a browser, authenticating a user or invoking inference. This check cannot establish real OAuth completion or packaged-platform acceptance.

## Dependencies and Integrations

Reuse authenticated Connect, the server vault, current Worker discovery, owned process supervision and existing Codex session/history publication. No provider brokerage, native retry emulation, account/model failover or new dependency is added.

## Change Triggers

Update the account/harness/session/protocol/client contracts and affected scoped `AGENTS.md` owners when the native profile, public operations, generations or recovery boundaries change. Record implementation status and validation in issue #1095, its pull requests and CI logs/artifacts; do not add repository evidence documents. Update the project index only for ownership, domain catalog or cross-domain invariant changes.

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


## Native quota and reset credits — issues #1096 and #1104

System capabilities 18 (`SUBSCRIPTION_QUOTA_V1`) and 19
(`SUBSCRIPTION_RESET_CREDITS_V1`) negotiate the two product operations separately.
Worker capability 8 requires the verified managed Codex 0.151.0 profile. The
pinned upstream source is commit `78c290807ce710180111df227df3b7a4fe845452`.
`account/rateLimits/read` omits its unit parameters; the native wire encoder's
explicit `OmittedParams` profile preserves the ordinary structured-parameter
requirement and common bounds. No inference or billing probe supplies quota.

Go queues one bounded five-minute observation per supported connected account,
using the last original native owner's machine. Individual and whole-inventory
refreshes are authenticated SubscriptionService operations with UUID-v7 receipts;
refresh-all derives its complete account set on the server, independent of client
pagination. An idle read takes a short exclusive credential lease. An active
execution uses its registered original Codex process and lease; it cannot create
a second credential writer. Original native rate-limit updates pass the same
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
are metadata; native titles, descriptions, balances, account identities and token
reflection never enter resources, receipts, logs or history. The desktop requires
explicit confirmation tied to the displayed account revision, connection,
generation and inventory identity. The CLI requires `--confirm`, with either the
returned credit ID or explicit native next-credit selection when only the count
is available. Configuration saves cannot manufacture observations.

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
