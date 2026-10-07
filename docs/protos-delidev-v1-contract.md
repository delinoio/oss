# DeliDev v1 Connect contract

## Key-preserving API format change reservations

Issue #964 reserves ProviderInventory capability
`ACCOUNT_API_FORMAT_CHANGE_V1 = 9` separately from capability 7 and OAuth
reservation 8. The owner/client `AccountService.ChangeAccountApiFormat` RPC
reserves request fields mutation 1, api_protocol 2, alias 3, enabled 4,
exclude_automatic 5 and recovery_notifications 6; its response reserves account 1,
request_id 2 and replayed 3. Reuse the existing `ApiProtocol` enum.
Establish these complete allocations on main before implementation. Reservations
alone grant no format change, key access or execution authority.

The approved extension permits changing a connected API account's format while
keeping its protected key. Go owns atomic preference/profile publication and
new connection generations sharing the original protected credential reference.
Running executions and existing-session continuation retain their original
connection/profile and admission evidence. New sessions use the new format only
after explicit validation; saving sends no provider request. Preserve original
OAuth receipts, account/Worker/history attribution, immutable referenced profiles,
exact actor/revision/request retries, and explicit Disconnect/deletion cleanup
across all generations. Do not convert formats or change Provider identity or
keyless credential ownership. No SQLite migration or native change is authorized.
Capability 7 and its existing cleanup-required configuration operation remain
unchanged; capability 9 owns the dedicated extension.


## OAuth API format selection reservations

Issue #964 reserves ProviderInventory `ACCOUNT_OAUTH_API_PROTOCOL_V1 = 8`,
`StartAccountOAuthRequest.api_protocol = 4` and
`AccountOAuthAttempt.api_protocol = 7` before dependent implementation.
The fields reuse the existing closed `ApiProtocol` enum. Capability 8 extends
account format selection to accepted provider OAuth profiles; it does not replace
capabilities 5, 6 or 7 or grant OAuth registration, credential, model or native
execution authority. Reservation-only changes activate no schemas or runtime
support and require no database migration.

The implementation must bind an explicit selected profile to the original Start
receipt and private attempt JSON, and preserve it through account creation,
connection, status, completion and original-result recovery. Omitted legacy
requests and attempts retain their original default and exact receipt behavior.
OAuth authentication ownership remains independent of the selected inference
format. Only currently eligible provider/authentication profiles may be selected;
REST availability alone cannot grant OAuth support. Existing provider revisions,
actor/login/credential ownership, once-only exchange and cleanup stay intact.

The desktop uses one provider-metadata-driven format presentation for manual and
OAuth connections: explicitly choose among multiple formats, or display the sole
format. Preserve Google project binding and device approval. Existing accounts
require Disconnect and confirmed cleanup before format changes. No historical
account/execution rewrite, protocol conversion or native change is authorized.


## API account format selection

Reservation PR #1646 established the complete issue #964 allocation closure on
main before implementation: ProviderInventory capability 7, profile field 10,
account-list protocol field 5 and the closed API protocol/authentication/profile
declarations. The [catalog contract](cmds-delidev-catalog-contract.md#api-account-format-selection)
owns their activated scope, schema-3 API families and portable version 4.
Preserve legacy default tuples, disconnect and confirmed protected cleanup before
format changes, connection-generation pinning, immutable original executions and
keyless cleanup proofs. The common server resolver owns every consuming flow;
regenerate bindings from the reconciled declarations. No SQLite migration or
Worker assignment extension is introduced.


## Direct execution startup allocation and activation

PR #1645 established the complete [startup allocation](cmds-delidev-execution-startup-contract.md)
on main at `03429673f2976ab52b98c613f9d3cc1ff4c41d84` before implementation.
Runtime activation uses System 43, Worker 23, ReportExecutionStartup request/response,
ExecutionStartupObservation and the four closed startup enums with their original
ledger field/member numbers. Preserve separate System 42 and Worker 22 ownership
for PR #1642. Negotiation permits private v4 direct assignments without inspection;
the original process still proves protocol/settings and fresh account authority.
Reports bind claimed revision, original machine/device/instance/server epoch and
durable exact receipts. Ready/failure metadata uses existing session/terminal job
JSON; no assignment rewrite, credential grant or SQLite migration is added.

## Failed subscription cleanup reservations

System `FAILED_SUBSCRIPTION_CLEANUP_V1 = 41` and all batch/status/result declarations were reserved on main by PR #1614 before activation. System 38 belongs to Claude and 39/40 to Grok. The reservation itself granted no cleanup, login or deletion authority.

One explicit owner/paired-client `CleanupFailedSubscriptions(request_id)` admits a server-owned durable job and freezes every candidate account ID, revision, original initial ChatGPT server LOGIN and deletion request UUID in account child jobs. Receipt replay binds the original actor and server and returns the same current job. Only one batch can be pending per server. Admission scans the complete bounded server inventory; frontend account pages never select work. Failed, canceled, expired, unsupported and interrupted initial logins qualify, including credential-cleanup-confirmed failures. The same explicit batch also selects disconnected subscription Accounts for every supported service, including metadata-only accounts and completed logout accounts. Version-2 private child jobs distinguish original failed LOGIN cleanup from disconnected configuration deletion; version-1 children retain their original LOGIN-only meaning. Disconnected targets have no invented operation ID and must retain no active login, native process, generation, identity commitment, active Worker ownership, lease, recovery, removal or active observation. A completed Worker logout may retain its historical owner-machine routing hint; it grants no active authority without a generation, lease, pending action or recovery, and does not bypass protected-reference checks. Connected accounts, restored generations and independent Worker/native/observation ownership are excluded.

The joined server controller applies the original 30-second account attempt and account gate. It rechecks the original requester, exact account revision and login ownership. It shares the existing native/credential cleanup and configuration deletion checks, including protected vault references and complete Agent/Project/retained Session references. Its native and credential checkpoints advance the child's expected revision in the same transaction. Changed settings or ownership remain retained. Failed attempts are terminal retained outcomes with closed reasons, never automatic retries. A later deliberate button click can create a fresh batch. An explicit `DeleteConfiguration` can admit one failed initial ChatGPT LOGIN through the same controller under the original deletion request ID. The child freezes that requester, account, confirmed revision, LOGIN and deletion command; cleanup checkpoints advance only its effective revision. The final deletion receipt retains the original public revision and request bytes. Before that receipt exists, mutation and replay use the immutable child under the existing job parent index to reserve the public request ID for that exact deletion command; admission rejects previously used IDs transactionally. Accepted single-account work survives client departure; terminal failures require fresh observation and explicit confirmation, and receipt/status reads never retry cleanup.

Confirmed deletion, tombstone/browser obligations, the existing configuration receipt, child result and aggregate counts commit together. Restart reads only original jobs and confirmed cleanup checkpoints, never login or callbacks. An interrupted attempt without a native checkpoint remains retained; confirmed native cleanup may resume protected cleanup. Shutdown cancels and joins the controller before store/vault closure. Revocation permits server-owned retained-result bookkeeping only, never substituted account deletion authority. Restore blocks queued/pending cleanup jobs and quarantines historical nonterminal jobs and receipts, so restored work cannot regain deletion authority.

`GetFailedSubscriptionCleanup(job_id, page_token)` returns current revision/state, total/processed/deleted/retained counts and at most 50 original account results. Signed cursors bind the actor and batch. Result metadata contains only the original alias, account ID, closed outcome/reason and safe problem code. Logs contain job/operation IDs, counts, phases and safe codes. Existing generic jobs/receipts need no migration or Rust/native change.

## Grok Build subscription reservations

The [Grok subscription contract](cmds-delidev-grok-subscription-contract.md)
reserves System login 39, System execution 40, Worker managed execution 21 and
progress diagnostic field 10 under issue #964. The ledger also owns the new
GrokDiagnostic fields 1–7 and closed GrokDiagnosticPhase values 0–11. Establish
the complete closure on main before active schemas, generated bindings or runtime
support. Codex field 7 and existing System 30/35/36/37 and Worker 19 ownership
remain unchanged. Reservations activate nothing and add no migration.



## Native Claude subscription allocations

PR #1612 established System `CLAUDE_SUBSCRIPTIONS_V1 = 38` and Worker
`NATIVE_CLAUDE_SUBSCRIPTIONS_V1 = 20`, with the complete closure in
`allocations.json`, on main before this activation. Preserve every
existing field, Codex bundle assignment and independent capability.

Progress reserves login method/native diagnostic fields 8/9. Worker progress
reserves state/login method/native diagnostic/suggested name fields 7–10;
protected Take reserves native profile ID field 6; Finish reserves native
identity field 10. `SubscriptionLoginMethod` reserves UNSPECIFIED 0,
BROWSER_CALLBACK 1 and BROWSER_CODE 2. The native diagnostic phase enum reserves
UNSPECIFIED 0 and discovery/version/runtime/launch/login/status/execution/history/
cleanup values 1–9. Native diagnostic fields are detected version/required
version/phase/code/correlation ID 1–5; native identity is profile ID/identity
commitment 1–2. Code submission reserves mutation/operation ID/code 1–3 and
accepted response field 1. Original Worker code Take reserves account/lease/
machine/instance/operation IDs 1–5 and code/submission ID response fields 1–2.
Wholly new declarations retain one owner and explicit `newDeclaration: true`.

The existing SubscriptionService now exposes `SubmitSubscriptionLoginCode`
for the initiating owner/client and `TakeSubscriptionLoginCode` only for the
original authenticated Worker/device/instance/lease. Code is write-only bytes:
a durable claim precedes memory-only retention and durable consumption precedes
one delivery. No replay redistributes bytes. Take/Finish retain existing Codex
bundle fields; Claude forbids bundle bytes and refresh-confirmed claims and
uses only native profile/identity metadata. Progress has closed method and
safe version/phase/code/correlation diagnostics, with no transcript or identity.
Generate Go and TypeScript from the reconciled schemas. Capability 38 does not
grant Worker readiness; capability 20 requires verified original native
installation and empty-profile cleanup. Native credentials remain on the
selected Runner. No database migration is added. Follow the subscription and
structure contracts; the main reservation alone granted no support.

## Agent Worker account source routes

PR #1371 established System `AGENT_WORKER_SOURCE_ROUTES_V1 = 36` and
`SaveAgentWorkerRequest.route_models = 5` on main before implementation. The
active repeated field reuses `AgentWorkerModelSelection`, aligned with ordered
Agent schema-3 routes and exclusive with the legacy singular model. The existing
mutation/revision/receipt response remains unchanged. Capability 36 advertises
this complete configuration extension; capability 35 retains its separate known
subscription catalog reservation. Schema 1/2 APIs and accountless CLI writes
remain compatible; current clients retain schema 3 even with one remaining source.
Resource reads expose schema 3 for ordered-source Agents, explicit-format API Accounts and profile-declaring API Providers. Older clients must
treat that family as unsupported and cannot overwrite it through legacy saves.
No Worker protocol shape or database migration changes. The selected-source native
configuration remains unchanged; complete source decisions are additive server-owned
initial-execution JSON under the [catalog contract](cmds-delidev-catalog-contract.md).


## Known subscription model allocation and activation

Issue #964 reserves System capability `KNOWN_SUBSCRIPTION_MODELS_V1 = 35`
and `ProviderService.ListKnownSubscriptionModels` declarations for a public,
advisory subscription catalog. The closed catalog-source enum reserves
UNSPECIFIED 0, BUNDLED 1, CACHE 2 and ONLINE 3. `KnownSubscriptionModel`
reserves native ID/display name/order/minimum harness version/retirement date
fields 1–5. The request reserves subscription service field 1; the response
reserves subscription service/models/catalog version/updated at/source fields 1–5.
These allocations reached main in PR #1370 before dependent activation. The
complete feature activates capability 35 and the owner/client read-only RPC in
provider.proto; generated bindings use the normal compatibility pipeline.
Reservations alone grant no feature support. The active advisory read grants no
authentication, account entitlement, native discovery or execution capability.
The response echoes the exact closed service, includes at most 200 models and
uses BUNDLED/CACHE/ONLINE provenance; unsupported service values fail. No database
migration is introduced. Follow the catalog, desktop and network contracts.

## GitHub token-first onboarding reservations

Issue #964 reserves `SystemCapability.GITHUB_TOKEN_ONBOARDING_V1 = 34` for
profile-independent token identity inspection and official token-form preparation.
`GitHubTokenKind` reserves UNSPECIFIED 0, FINE_GRAINED 1 and CLASSIC 2.
`GitHubTokenIdentityState` reserves UNSPECIFIED 0, VERIFIED 1, INVALID_TOKEN 2,
ACCESS_RESTRICTED 3, SSO_REQUIRED 4, RATE_LIMITED 5 and UNAVAILABLE 6.
The five new message declarations and their fields are recorded with exclusive
ownership in `allocations.json`: token inspection request ID/token 1–2, response
request ID/state/identity/problem JSON 1–4, public identity ID/node ID/login 1–3,
form request request ID/token kind/resource owner/access 1–4 and form response
request ID/token kind/resource owner/access/URL 1–5. Establish this closure on main
before dependent implementation. This prerequisite introduces no active schema,
generated binding, advertised support, credential lifetime, browser authority or
database migration. Existing profile/revision-bound token forms remain unchanged.

### Activated onboarding boundary

After the main-first reservation closure, IntegrationService exposes owner/paired-client-only InspectGitHubToken and PrepareGitHubTokenForm and System advertises capability 34. Inspection uses write-only token bytes and returns only request-bound closed state/public identity/sanitized failure; preparation echoes closed kind, owner and access with a canonical official URL. Neither read creates a receipt, profile or credential generation. Saved-profile form revisions remain independently required. Go and TypeScript outputs are regenerated from these reserved declarations. No storage migration is added; desktop retention is limited to the live verified creation draft described in the integration contract.

Fine-grained `PrepareGitHubTokenForm` with selected-repositories access permits
an empty `resource_owner`, echoes it unchanged and omits `target_name` from the
canonical URL. Explicit valid owners remain supported. This draft-only allowance
does not change saved-profile owner requirements or revision-bound form reads.
The desktop's Classic shortcut uses public-repositories access without scopes;
existing explicit private-repositories requests retain their separate behavior.
No protocol field, enum, capability or migration is added.

## Agent Worker wizard

PR #1351 established the issue #964 allocations on main before implementation.
System `AGENT_WORKER_WIZARD_V1 = 33` advertises source-scoped account/model lists
and atomic `ConfigurationService.SaveAgentWorker`. `ListResourcesRequest` field
4 and `SearchModelsRequest` field 7 select a closed subscription service. Reject
unknown services, API/provider combinations and account selectors on other kinds.
Filter in SQL before pagination and bind the source into each cursor. Unspecified
selectors preserve legacy behavior; shared Filter, snapshots and events do not
change.

`SaveAgentWorkerRequest` carries mutation, document, typed model selection and
schema version in fields 1–4. `AgentWorkerModelSelection` uses a oneof canonical
model ID or exact executable/native ID, plus the canonical model's expected
revision. A canonical selection requires a nonzero revision; a direct ID requires
zero. The RPC reuses the existing `SaveConfigurationResponse` acknowledgement,
with narrow Buf lint exceptions on the two save methods for this deliberate reuse.
Existing RPCs, CLI operations and resource/storage schemas remain compatible.
The new path requires at least one account and one common API provider or native
subscription service. Fixed routing requires exactly one account. Go resolves or
creates the model and saves the Worker in one receipt transaction. Saved harness
compatibility is a configuration declaration, never native/account/platform proof.
No database migration is added. Follow the [catalog contract](cmds-delidev-catalog-contract.md)
and [desktop contract](apps-delidev-desktop-contract.md#agent-worker-wizard).

## Repository addition contracts

### Remote repositories

Main reservation PR #1377 established System `REMOTE_REPOSITORIES_V1 = 37` and Worker `REMOTE_WORKSPACE_CLONE_V1 = 19` before activation. System 31/32 and Worker 18 retain their separate immediate Local Clone and GitHub metadata contracts. Capability 37 permits credential-free URL registration with no checkout, Worker or local proof. Clients must verify it before repository saves/imports. Worker 19 permits managed session clones; acceptance and assignment independently require it on the selected machine.

Schema-1 Repository JSON adds required `remote_url` for explicit saves/imports and permits an empty `checkouts` list. Historical omitted URLs and original accepted preparations remain readable without conversion, extraction, migration or rewrite. Each new preparation binds `source_kind` (`remote-clone`, `local-checkout` or an internally derived `independent-fork`) and `remote_url` in the immutable request digest. Legacy omitted source kinds keep their existing linked-checkout contract. Ready managed repositories bind SHA-256 native directory commitments; these are ownership metadata, never raw native identities. The ordered complete result must match the original source kind and URL. Unknown kinds grant no execution or deletion authority.

PR #1355 activates main-established System `REPOSITORY_CLONE_V1 = 31` and
`GITHUB_REPOSITORY_PICKER_V1 = 32`, Worker `REPOSITORY_CLONE_V1 = 18`, and
`ListGitHubRepositoriesRequest`/`Response`, `RepositoryCloneGitHubSelection`,
`CloneRepositoryRequest`/`Response` fields in the allocation ledger. These
allocations reached main before active declarations, generated bindings and
capability advertisements. Listing is an explicit revision-bound
owner/client profile read. Clone is a durable originating-Worker operation
using existing job receipts; its proof token is transient and PAT bytes never
enter its assignment. System 31/32 and Worker 18 retain that separate authority.

## Metadata-only request diagnostics

Issue #1103 adds owner/paired-client `SessionService.ListRequestDiagnostics` and `SystemCapability.REQUEST_DIAGNOSTICS_V1`, with additive generated Go/TypeScript/Connect Query bindings. Typed source/state/operation enums distinguish native input publications from individual proxy HTTP send claims. Optional settings, latency, HTTP status/attempt and completion time preserve unavailable versus measured zero/false. Exact uint64 revisions/durations remain precise. Original opaque IDs are bounded validated projections, never arbitrary native JSON. Signed pages bind session, optional exact execution and page size (default 50, maximum 100). Worker credentials cannot read this product surface. See the [diagnostics contract](cmds-delidev-diagnostics-contract.md) for immutable attribution, publication, secret filtering, retention and evidence limits; this is not another usage source.


Source schemas are service-specific under `protos/delidev/v1`; shared types have
one common owner. The historical `delidev.proto` forwards imports. Existing wire
names and numbers remain unchanged. `protos/delidev/allocations.json` records main
assignments and pending reservations without advertising unimplemented support.
See the [structure contract](cmds-delidev-structure-contract.md).

Issue #1146's [OpenRouter OAuth contract](cmds-delidev-account-oauth-contract.md)
reserves inventory capability `OPENROUTER_OAUTH_PKCE_V1 = 5` independently of the
four existing account-flow gates, and `ProviderInventoryEntry.connection_method = 9`.
The new closed `ProviderConnectionMethod` enum declares UNSPECIFIED 0,
API_KEY 1, OAUTH_PKCE 2 and KEYLESS 3. `AccountOAuthState` declares UNSPECIFIED 0,
AWAITING_AUTHORIZATION 1, EXCHANGING 2, SAVING 3, CONNECTED 4, CANCELED 5, EXPIRED 6,
FAILED 7, INTERRUPTED 8 and RECOVERY_REQUIRED 9. Each new-enum member uses explicit
declaration provenance in the allocation ledger without entering the active
baseline. Establish these reservations and migration 29 on main before dependent
implementation, except the owner-approved single integrated PR. The reconciled
AccountService schema defines owner/client Start/Complete/Cancel/Status, each
with its own standard-named response, and generates both languages from their
source. Value-local Buf acronym-prefix comments preserve the reserved OAuth
enum spelling; they grant no broader lint exception. Reservations alone grant no exchange
or capability authority. Complete product support remains required before
advertisement. Older servers satisfying the four existing gates
retain manual connection without an OAuth badge.

The [planned shared compaction contract](cmds-delidev-compaction-contract.md)
reserves `EntityKind` 32, `SystemCapability` 15, `WorkerCapability` 5 and
`SessionChange.compaction_job` 9 for issues #1093, #1202 and #1203. These are
ledger reservations only: no schema declaration, generated binding, RPC or
capability advertisement is activated by the reservation change. The owning
issue is recorded directly when no implementation PR exists yet.

The additional shared RPC closure records PR #1221's existing
`CompactSessionRequest` and `CompactSessionResponse` assignments in the immutable
baseline, then reserves `CompactSessionRequest.expected_execution_id = 2` and
`CompactSessionResponse.session = 4` under issue #1203 with #1093/#1202 as shared
consumers. Establish these reservations on main before adding active fields or
generated bindings. The expected execution must join the exact original source
at acceptance and remain part of the actor-bound receipt identity. The response
must join the current session and original job in one authorized read, including
reference-only replay; its existing `request_id` remains the original action ID.
Current native compaction support does not imply these additional fields exist.
The reservation change adds no capability or migration.

Issue #1206's [native Codex model observation contract](cmds-delidev-native-models-contract.md)
activates main-established server capability 16 and Worker capability 7.
`native_models.proto` owns the owner/client-only NativeModelService acceptance,
status, immutable bounded page and cancellation operations. Request receipts bind
original actors and exact machine/account revisions; Worker WatchWork/ReportWork
retain original assignment/device/instance ownership. Generic public job resources
omit executable paths and full observations. Generate service-specific Go and
Connect Query bindings plus historical facades; subscription support remains typed
unsupported until #1095 supplies its protected lifecycle.

## Server-owned subscription login reservations

The Codex forward-version amendment reserves
`GetSubscriptionProgressResponse.diagnostic = 7`, the new `CodexDiagnostic`
message fields detected version 1, minimum version 2, phase 3, stable error code
4, safe message 5, guidance 6 and correlation ID 7 under issue #964.
`CodexDiagnosticPhase` reserves UNSPECIFIED 0, DISCOVERY 1, VERSION 2, PROFILE 3,
RUNTIME 4, LAUNCH 5, INITIALIZE 6, CONFIRM 7, LOGIN 8, MODELS 9, EXECUTION 10,
HISTORY 11 and CLEANUP 12. Establish these ledger-only reservations on main
before dependent implementation. They grant no Codex version, native operation,
diagnostic response or capability support and add no migration. Diagnostics
must exclude paths, credentials, login URLs, identities and raw native content.

The owner-approved login-first amendment reserves independent System capability
`SERVER_SUBSCRIPTION_LOGIN_V1 = 30` under issue #964. Progress fields 4–6 reserve
`state`, `suggested_name` and `generation`. The closed `SubscriptionLoginState`
enum reserves UNSPECIFIED 0, PREPARING 1, WAITING 2, SUCCEEDED 3, CANCELED 4,
EXPIRED 5, UNSUPPORTED 6, RECOVERY_REQUIRED 7 and FAILED 8.
`ForwardSubscriptionCallbackRequest` reserves account ID 1, original operation
ID 2 and write-only callback query 3; its response reserves accepted 1.
All new declarations have explicit ledger ownership. Establish these allocations
on main before dependent schemas or code. This reservation changes no active
schema, binding, endpoint, capability advertisement or migration. Omitted
`RequestSubscription.machine_id` remains unsupported until the complete
server-owned boundary activates; existing machine-bound requests retain their
original Worker authority.

## Scope

Issue #1235 reserves `SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1 = 17`
under its owning issue identity. This independent capability will negotiate
service-bearing subscription Accounts/native Models and retired-reference
projections at resource schema version 2; unchanged API resources remain version
1. It must not depend on API provider inventory capabilities. Establish this
allocation and migration 28 on main before dependent implementation. This
prerequisite changes no active schema, generated binding or capability
advertisement; generate clients from reconciled schemas when implementation
activates the reserved boundary.

`protos/delidev/v1` owns `delidev.v1`; generated Go bindings live in `protos/gen/go/delidev/v1`. Generated TypeScript messages and service-specific Connect Query descriptors live in `packages/delidev-api-client/src/gen`; its [client contract](packages-delidev-api-client-contract.md) preserves direct authenticated Connect and read-only bounded replay.

## Runtime and Language
Protocol Buffers and Connect RPC, with Go server/CLI clients. Native harness protocols never escape the Worker adapter boundary.

Managed execution's existing protected `FinishSubscription` fields encode confirmed pre-native failure with false success/refresh, true cleanup and the byte-identical unused original bundle. The server independently compares the immutable generation before releasing ownership; no schema numbers or generated declarations change. Missing/changed bytes or cleanup alone retain recovery rather than authorizing old-generation redistribution. Follow the [managed subscription contract](cmds-delidev-subscription-contract.md).

## Users and Operators
Authenticated owner clients and separately authorized outbound Workers. No unauthenticated product API or browser client.

## Interfaces and Contracts
`EntityKind.SUBAGENT` and `SystemCapability.SUBAGENT_OBSERVATION_V1` add the [read-only child observation profile](cmds-delidev-subagents-contract.md). Existing authorized `ResourceService` get/list/snapshot/event reads expose session-scoped child records. `WorkerService.PublishExecution` accepts bounded original `subagent-observed` batches with the unchanged exact receipt, sequence and atomic revision/event contract. No child-control RPC or writable configuration kind is added.

`InboxService` also owns client-scoped notification preferences, bounded metadata candidates, display claims and terminal presentation reports. A claim returns display permission only on its first durable acceptance; exact replay and competing claims cannot redisplay, and current authenticated state is read after mutations. Closed kinds/states, opaque original inbox/session/claim identities and explicit preference revisions remain separate from source bodies, read state and response authority. Follow the [inbox contract](cmds-delidev-inbox-contract.md); no RPC receipt proves OS delivery or human observation.

`SystemService.GetOverview` is an owner/paired-client read for the tray and `delidev server overview`. It returns observation time, exact uint64 counts of retained active execution ownership, unanswered open interactions in their current active session, registered Workers and currently connected authorized Workers, plus the UTC day boundaries for a separate existing usage query. Worker streams are sampled independently of one authorized database snapshot; a fresh stream also requires a current heartbeat lease, enabled machine and unrevoked Worker device. These observations never grant execution readiness or cleanup. Reads have a three-second deadline, reject an over-capacity live inventory rather than returning partial counts, and return no source documents, identities, credentials, paths, prompts or emails. Zero is a measured count; failed reads are unavailable. Ordinary status keeps its Worker-compatible metadata-only contract.

`DeviceService` owns expiring, single-use pairing grants and per-device revocation. Pairing is the sole unauthenticated RPC; it authenticates the presented one-time code inside its acceptance transaction. The initiating device generates its own random credential and sends only its SHA-256 verifier. An exact replay of an accepted pairing request recovers its acknowledgment without creating another device; a different use of a consumed grant fails. Pairing codes and device credentials never appear in ordinary resource documents, receipts, or CLI JSON. The CLI stores issuance codes in a private file and accepts joining codes through stdin.

`WorkerService` is the outbound execution boundary: an authenticated Worker attaches with a fresh process instance identity, receives bounded server-streamed work assignments, and reports results through separate unary RPCs. The server binds every assignment, acknowledgment, and result to that machine, job, and claimed process instance. A disconnect never authorizes a duplicate execution or a different-machine retry. Per-device revocation cancels active RPC contexts and rejects both later authorization and in-flight mutation commits. It atomically cancels that machine's undispatched jobs and marks claimed jobs uncertain, propagating the failure to any repository-save coordinator, session preparation and native execution/input ownership; a connection cancellation never proves an accepted native operation had no side effects. Accepted job routing remains immutable.

Each outbound work stream delivers at most one unresolved assignment; later accepted jobs stay queued until the preceding report resolves. Ten-second heartbeats continue during native work. Workers receive in a separate bounded loop, cancel owned work on stream termination/revocation or 45 seconds of silence, join cleanup, and retain the durable outcome before reconnecting. A same-instance reconnect may reuse a finished journal/report identity but cannot rerun a started operation without its completion record. Half-open TCP is not continuing execution authority. `WatchWorkResponse.cancel_job_id` is a separate control for an immutable assigned job; `cancel_requested` accompanies a replayed assignment whose cancellation was already accepted. Controls never alter its revision/digest, survive server restart and target only that job. A precanceled assignment writes its completion journal without starting native work. Worker reports classify confirmed cancellation as `canceled`, while unresolved ownership remains `uncertain`.

`SystemService.GetDoctor` retains its empty request/JSON response and now returns the bounded version-2 [diagnostic report](cmds-delidev-diagnostics-contract.md), with decimal-string capacities/counts, explicit partial inventories, original-connection observations and final client authorization. No protobuf change is required; unknown report versions cannot establish health.

The initial service boundary is `SystemService` (status, explicit stop, read-only doctor, durable manual backup), `ResourceService` (indexed get/list, coherent scoped snapshot, Connect event stream), and `ConfigurationService` (validated configuration save/delete, pure routing preview). `Resource.document_json` carries a version-1 closed Go-domain schema, capped at 1 MiB; the transport envelope carries typed entity kind, UUID-v7 identity, expected revision, and timestamps. Unknown fields, duplicate JSON keys, unsupported schema versions, and unsupported writable kinds fail. The configuration endpoint cannot write arbitrary lifecycle state or observed account health.

`ListResourcesRequest` has additive, list-only `provider_id` and closed `AccountTypeFilter` selectors. `UNSPECIFIED` preserves the historical all-account result; API and subscription select the corresponding existing account type. These selectors are valid only for account lists, are combined in server SQL before `LIMIT`, and are included in signed continuation-cursor scope. They are not fields on shared `Filter`, so coherent `GetSnapshot` and `WatchEvents` retain their existing unfiltered account scope. Unknown enum values and selectors on other resource kinds fail with `InvalidArgument`. `ProviderInventoryCapability.ACCOUNT_TYPE_FILTER` separately advertises support for this selector; desktop account surfaces require it along with the provider activation, active-model and provider-account capabilities.

`AccountService` owns API/keyless connection, disconnection and metadata-only status for owner/paired clients; Workers cannot call it. `ConnectAccount` accepts a current account mutation plus write-only bounded API-key bytes or explicit keyless selection. It records unverified readiness and an immutable connection generation, with a server-private keyed commitment binding the durable request receipt without persisting the key. `DisconnectAccount` atomically commits disconnected state and cancellation of unfinished jobs selected on that account before relay/credential cleanup. Claimed job revisions remain immutable; the existing outbound cancellation control stops their Worker processes, with separate native completion/recovery reporting. Old receipt replay cannot cancel jobs on a replacement connection. Its response includes current account metadata and optional closed, sanitized `domain.Error` JSON when cleanup remains pending; an accepted request is still retryable with its original identity/revision. General account metadata exposes that retry identity but not the private cleanup receipt ID. Old connect/disconnect receipts cannot reactivate a removed credential or alter a replacement generation. See the [account lifecycle contract](cmds-delidev-accounts-contract.md) for staging, revocation, cleanup and CLI behavior. `ValidateAccount` accepts the current account mutation and atomically records a bounded non-inference observation; its response retains that original sanitized observation alongside current account metadata on replay. Failed accepted checks use the same envelope and do not repeat HTTP. Subscription authentication remains pending; public first Codex API dispatch uses the separate readiness and immutable-assignment gate; the private execution-grant RPC below supplies scoped relay authority only for an independently authorized claimed assignment. See the [provider inspection contract](cmds-delidev-providers-contract.md) for authentication evidence, cancellation and secret boundaries.

`ProviderService` owns `ListProviderPresets`, `DiscoverModels`, `SearchModels` and `ResolveModel` for owner/paired clients. Discovery accepts a current account mutation and atomically commits catalog changes plus a server-owned account observation. It returns the original accepted observation with current account metadata on replay and never repeats key/HTTP work for an accepted receipt. Search returns bounded model records plus represented provider records, with scoped HMAC cursors that expire on model/provider changes. Resolve returns a canonical model record; display preferences do not become execution permissions. Preset creation uses ordinary `SaveConfiguration` with the concrete chosen provider document. See the [catalog contract](cmds-delidev-catalog-contract.md) for model provenance, automatic maintenance, suppression and ambiguity semantics.

`SessionService` implements owner/client session creation/listing, ordered input enqueue/edit/removal/listing, rename, `PrepareSessionWorkspace`, `RecoverSessionWorkspace` and typed preparation/native controls. Create atomically persists one session, its first queued input and its workspace job, with server-derived client identity and explicit manual/external CLI provenance. `SessionChange`, including `workspace_job`, `recovery_job` and the additive `execution_job`, returns current referenced records on exact request replay, never earlier prompt copies. Input sequence/mode are immutable; content removal retains a nonexecutable tombstone and strips current content. Queue pages use signed session-bound sequence cursors and a 3 MiB aggregate document bound. ListSessions hides fully archived sessions by default. Stop/Archive own workspace preparation for never-executed sessions and the current accepted native assignment: queued work cancels before claim, claimed work receives targeted cancellation and Archive waits for confirmed completion, while uncertainty retains recovery; the first native profile requires exact terminal publication plus owned cleanup before Archive completes. Restore stays paused. Resume may atomically claim the first input of a ready never-executed session; Resume after verified native cleanup uses the immutable continuation boundary and retains explicit intent if the queue is empty. Eligible unpaused ready sessions are automatically checked for first Codex API dispatch, without treating acceptance or a workspace as native success. `RecoverSessionWorkspace` accepts an existing session mutation and an explicit cleanup selector, queues exact original-assignment reconciliation and preserves pause. Ready or confirmed-clean recovery publishes the original job and session atomically; incomplete/mismatched evidence retains uncertainty. Cleanup never deletes a ready workspace. General configuration cannot author these state fields. See the [session contract](cmds-delidev-sessions-contract.md) for native acceptance uncertainty, Local origin proof and atomic first-dispatch snapshots and current readiness checks.

Automatic title creation is an opt-in `name_mode` extension, with omission preserving the manual receipt shape. `SystemService.GetStatus` exposes server support through `AUTOMATIC_TITLES_V1`; `AttachWorker` separately negotiates `AUTOMATIC_TITLES_CODEX_V1`. `WatchAuxiliaryWork` uses dedicated request/response messages for bounded title jobs and cancellation/heartbeat controls independently from ordinary `WatchWork`; title reports still use `ReportWork`. Auxiliary relay grants are checked against the exact original assignment and its server epoch. Follow the [automatic title contract](cmds-delidev-session-titles-contract.md).

The closed session document includes server-owned `initial_execution`, optional later-turn `current_execution`, optional `next_execution_intent`, live `execution` progress and `claimed` dispatch state; queued inputs retain immutable execution/native request identities after claim. The first dispatcher atomically freezes configuration/initial account/routing, and the continuation dispatcher advances current ownership without replacing that snapshot. Version-1 `ExecutionJobInput` is first-only; version 2 requires exact predecessor progress/completion, a bound private checkpoint/assignment digest, original history-root identity, prior input digest/mode and explicit versus automatic intent. Every turn uses a fresh job/execution and scoped credential. Existing typed Resume, enqueue and Worker RPCs compose this boundary; none of these server-owned fields becomes client-writable. Older native terminal reports without a checkpoint remain historical evidence only. See the session contract for FIFO, current-readiness and replay gates.

`WorkerService.RegisterExecution` binds a Worker-generated execution token digest to its exact claimed execution job, current machine/process/device and server process epoch. It carries a mutation identity/revision and a SHA-256 digest only; neither a raw execution token nor upstream account key is returned or stored in the job/receipt. Exact retry preserves the original binding. Replaced instances, canceled/finished/uncertain jobs, stale account connections, changed restrictions and another server epoch cannot issue or reuse authority. Only the owning authenticated Worker may call it. The response gives the fixed relative native API proxy path, resolved against the already-paired server origin by the Worker. This private RPC is not a public API-key vending operation.

`WorkerService.PublishExecution` accepts a closed normalized execution-event document from only the exact owning Worker/job revision. Events bind execution/thread/turn identities and a strictly increasing sequence; the Worker durably retains the request identity and event before publication. Exact receipt retries return the same acknowledged sequence without repeating queue accounting or transcript changes. Initial thread binding, input acceptance, bounded user/assistant message lifecycle/deltas, dedicated command/patch tool lifecycle/output/input/revision observations, plan/reasoning artifacts and immutable turn-plan/diff progress, native token observations, closed generic notices and terminal outcomes are the initial typed forms; raw native extensions and diagnostics are not accepted. A terminal event revokes further inference but does not prove owned process cleanup. Publication remains valid for retained facts during cancellation; it cannot clear a pause, revive failed outcomes or authorize another send. Token observations carry their stable UUID and nullable native counters; the server derives immutable event-time account/model provenance, retains observations without summing cumulative samples and never infers actual cost. `response-usage-observed` separately carries an opaque native response digest, nullable exact counters, cost-evidence classification and observation UUID; the server atomically deduplicates original response identity and pins assignment attribution under the [usage contract](cmds-delidev-usage-contract.md). Warnings carry a closed notice kind only. Tool observations use a dedicated closed payload and the same native-item uniqueness/completion index as text messages. Tool failure is distinct from whole-turn failure; streamed and aggregate output remain separate, and unreported counters/output remain null. Tool records cannot authorize terminal/file operations or approve native work. Plan/reasoning artifacts retain start/final snapshots plus ordered indexed deltas and share native-item completion gates. Turn-plan/diff progress retains immutable observation IDs and latest references without fabricating a native item; it never grants repository/review authority or changes turn outcome. Artifact completion may replace its streamed draft without erasing that evidence. A separate exact terminal/cleanup document is now accepted through `ReportWork` as described below; public first/continuation dispatch is integrated, while complete usage coverage and richer event adapters remain required integration.

`PublishExecution` additionally accepts closed `interaction-requested`, `interaction-closed` and `waiting-changed` forms. Requests bind the exact arrival/execution/thread/turn/item and numeric/text native request ID, retaining original typed questions and no response/permission fields. Unanswered native closure is separate from answer acceptance. Native waiting flags never resolve requests; terminal publication atomically closes remaining unanswered requests without fabricating responses. The server's execution-scoped unique request index and count/byte bounds prevent duplicate/replaced arrivals and partial overflow. Existing authenticated resource reads/snapshots/events expose retained `interaction` documents, including closed requests. Dedicated owner/client acceptance and server-side Worker claims retain non-secret response ownership; protected answers and lost-event/history acceptance reconciliation remain pending; independent inbox read state uses the dedicated API below. The Worker now journals and publishes native delivery through the dedicated form below.

Native question and terminal publication also atomically create a distinct unread `inbox` resource. Its version-1 closed domain document carries a source kind/reference and independent read state, with bounded immutable native terminal metadata for completion/failure/interruption. It carries no answer content or permission decision. Existing resource reads/snapshots/events expose these records without marking them read. See the [inbox contract](cmds-delidev-inbox-contract.md).

`InboxService` is owner/paired-client-only. `GetInboxEntry` returns a transactionally joined `InboxView` containing the inbox entry, current session and original interaction when the source is a question. `ListInbox` returns the same joined shape with optional session/project and closed source/read-state filters, at most 200 entries and a 3 MiB aggregate protobuf view bound. Signed page cursors bind all filters and the inbox event epoch; a source/read-state insertion or mutation invalidates existing pagination rather than silently skipping changed membership. Source/session details are current within each page transaction, and their events do not independently invalidate an otherwise unchanged inbox selection. `SetInboxReadState` uses the inbox entry's exact revision and an explicit read/unread enum. Its canonical receipt identity includes the acting principal and target state; reference-only accepted retries return the current joined view without reapplying an old read state. A no-op current-state mutation does not advance the revision. None of these operations changes interaction response authority or native state, and Workers cannot invoke them.

`SessionService.SteerQueuedInput` is owner/paired-client-only. Its mutation binds the selected queue ID/revision, while explicit session/execution/native-turn UUIDs prevent steering a replacement turn. The request UUID is the `ENTITY_KIND_STEER` resource identity. Under current session/account/Worker/native-profile authority, acceptance atomically retains the attempt and claims that precise input without changing sequence/mode or freeing capacity. The response joins the current attempt and `SessionChange` references; actor-bound reference-only receipts never requeue or resend on replay.

`WatchWork.steer_input` contains only job/Steer/revision metadata and is mutually exclusive with every other stream form. It targets the one active assignment independently of queued jobs, with bounded per-execution deduplication and live cancellation/heartbeats. `WorkerService.ClaimSteerInput` requires the exact attempt mutation and owning job/machine/instance/device. It rechecks live authority before accepting and returning the original queue document, including receipt replay. A claim is send ownership, not delivery proof; canceled, uncertain or no-longer-authorized claims cannot return a prompt.

`PublishExecution` accepts one closed content-free `steer-observed` payload binding attempt/input/claim UUIDs and delivery `not-sent`, `accepted` or `uncertain`. Evidence is a closed native acknowledgement/history/rejection or preflight rejection classification, with bounded stable problem codes only. Publication verifies original execution/thread/turn/Worker ownership and atomically retains observation/sequence, queue state/capacity and ordered accepted-input bindings. One uncertain observation can later gain a separate exact native resolution without erasing the original observation or paused recovery. Accepted retry receipts never repeat capacity accounting. Definitive rejection restores ordinary queue eligibility; uncertain delivery remains owned and cannot be blindly replayed. Prompt content stays in its original queue/transcript boundaries, outside control messages, claim receipts and delivery journals.

`WatchWork.question_response` carries only the active job, interaction, response UUID and expected interaction revision. It is mutually exclusive with assignments, heartbeats and cancellation forms. The server emits bounded controls independently of the ordinary job queue, deduplicates response identities per stream and preserves targeted cancellation/heartbeats. The queued job behind an execution waiting for an answer remains unclaimed. This control alone never authorizes a native send.

`InteractionService.RespondQuestion` is owner/paired-client-only. Its mutation identifies the original interaction and expected revision; the request UUID is also the immutable response UUID. The strict `response_json` document contains only non-secret `answers` keyed by original question IDs. The server validates original question/options/secret semantics and native encoded size before atomic queued acceptance under current execution authority. Receipt identity includes the acting client, original revision and complete canonical response. Reference-only retries return the current interaction without requeuing or repeating native delivery, even after closure or Worker loss; current client authorization still applies. The CLI operation is `interaction respond --id ID --revision N --input FILE|-` with the common durable `--request-id`. Acceptance and native delivery remain asynchronous, and the returned interaction exposes their current independent states.

`PublishExecution.question-delivery-observed` has one closed `question_response` payload: interaction, response and claim UUIDs, native item identity and delivery enum `not-sent`, `transmitted` or `uncertain`. It contains no answers, approval decisions or permission fields. Publication verifies the exact original claimed response and owning Worker device/job/instance together with the execution/thread/turn/item. It atomically stores one immutable delivery observation/sequence, response state, execution cursor and bounded unconfirmed-response accounting. A repeated delivery under a new event identity cannot overwrite evidence; exact receipt replay cannot count it twice. Native request closure preserves transmission evidence without confirming semantic acceptance, and terminal publication retains required acceptance recovery while preserving the independently observed native outcome and subsequent owned cleanup proof.

`PublishExecution` also accepts a content-free `question-accepted` document with original interaction/response/claim/call identities and closed `native-question-output` evidence. Only the original owning Worker may publish after its durable delivery observation. The server preserves original answer content, pipe delivery and native closure, stores acceptance evidence/sequence, transitions the response to `accepted` and decrements one unconfirmed-response count atomically. Duplicate new observations fail; exact event receipts replay without double-counting or clearing prior recovery. The native adapter must compare exact original thread/turn/call and canonical answer; request closure, waiting-clear or terminal outcome cannot supply this evidence. Lost-native-event/history reconciliation remains separate required work.

`WorkerService.ClaimQuestionResponse` accepts that exact interaction mutation plus job, response, machine and instance IDs. Only the original grant's authenticated Worker device/process can claim the queued response; current session/native/account/server-epoch authority is rechecked before acceptance and before returning content, including an exact retry. Competing claim identities cannot replace an accepted claim. A reference-only receipt retains the interaction UUID; a successful response includes its current interaction document with original non-secret answer content and immutable claim metadata. Closed, canceled, uncertain or no-longer-authorized claims return an error without answers. Native closure after a claim cannot prove delivery: it marks the response uncertain and pauses the session for recovery, preserving the independently observed outcome. Lost Worker ownership cancels unsent queued responses, marks claimed responses uncertain and leaves native closure unknown. The production Worker now journals the claim identity before RPC and send intent before native delivery, validates the returned original scope, and publishes a metadata-only delivery observation. Owner/client acceptance uses the dedicated `InteractionService.RespondQuestion` boundary described above.

`WorkerService.DiscoverHarnesses` is an owner/client operation that accepts a machine mutation identity/revision and an optional closed executable-selection document. Omission preserves existing selections; a supplied document replaces them. Acceptance atomically persists the pending machine generation and a `harness-discovery` job. A result must match all four accepted selections in canonical harness order and cannot claim protocol validation or execution capabilities from a version probe. Optional `verify_protocol` requests a separate non-inference native handshake and is bound to the discovery receipt/job generation. Only requested observations matching a supported installed-version profile may set `protocol_verified`; they still cannot grant execution capabilities. Omitting the new selector preserves the request digest of preexisting version-only discovery receipts. The server supplies observation timestamps and reconstructs safe errors, then commits machine metadata and the job outcome together. General connection revisions do not invalidate discovery, but newer discovery generations do. Worker credentials cannot invoke the owner operation; only the owning current Worker instance may report its claimed result.

### Repository-inspection metadata compatibility

Issue #1142 appends `WorkerCapability.REPOSITORY_INSPECTION_METADATA_V1 = 6` without renumbering existing values and `AttachWorkerResponse.supported_worker_capabilities = 3`. Main reserves Worker values 3, 4 and 5 for other features; the issue's historical proposed value 3 is superseded by the immutable allocation ledger. The server advertises metadata support independently of native title/forwarding/terminal support. The initial Worker attachment stays unchanged. The existing second attachment requests metadata only after server support is observed; the retained negotiation profile/request identity includes that support state, and enrichment activates only if the returned Machine confirms acceptance. Unknown and duplicate capabilities are rejected together, including duplicates in three-capability lists; a replacement/legacy Worker cannot inherit acceptance from an earlier process.

`InspectRepositoryRequest`, inspection JSON inputs and queued input bytes are unchanged. The optional result map `github_repositories` is remote → `{owner, name}` only, validated against the inspected remotes and existing GitHub validators under the 128-remote/1 MiB bounds. Results with unnegotiated, foreign, invalid or unknown/raw-URL fields are rejected. Enrichment field presence requires negotiation and a non-null map; explicit JSON null is not legacy omission. No new inspection RPC or database migration is introduced. Omitted metadata preserves existing inspection/CLI consumers; an unrelated legacy Machine decoder rejecting new capability identifiers is outside that guarantee. Go and TypeScript bindings are generated from the canonical service-specific schema, activating only the pre-established main allocations.

Repository saves return an accepted coordinator job in `SaveConfigurationResponse.job`. The server dispatches fresh read-only inspection to every configured checkout, validates preferred/base/starting remote names on each Worker, and commits the canonical repository plus successful coordinator outcome atomically only after all results arrive. Validation or revision failure preserves the previous configuration and a typed failed job; retries reuse the accepted operation. The successful job stores only the target ID/revision as its result, not a second configuration body. `--wait` waits within a bounded CLI deadline; accepted job identity remains available in failure output. Omitted `auto_fetch` defaults to true.

Version 1 uses canonical UUID-v7 entity/request identities, expected revisions, bounded pagination, typed errors and correlation metadata. Product operations share CLI/server validation. Mutations commit durable request receipts with their state/events. Streaming is Connect server streaming; snapshots carry the event cursor they represent. Expired/invalid cursors require resnapshot, and slow consumers reconnect instead of accumulating unbounded memory. Worker authorization is limited to that machine's assigned operations/events.

Page/event cursors are HMAC-bound to server identity and filter/session scope and expire after 24 hours. Pages are capped at 200 records. Snapshot overflow returns an explicit narrower-scope error instead of pretending a partial page is complete. Events carry metadata and indexed message/entity revisions; they never repeat transcript bodies. Stream sends have a 15-second write deadline and fetch at most 200 durable events at once. Authentication and correlation metadata also apply to streaming errors.

Generic resource lists and snapshots also enforce a 4 MiB aggregate budget using the greater of each resource's protobuf and protobuf-JSON encoded size plus bounded envelope overhead. This leaves room below the 5 MiB transport ceiling for cursor/response metadata. A byte-limited list resumes after its final returned resource; a snapshot instead returns correlated `ResourceExhausted` with narrower-scope guidance and no partial resources or event cursor, even when its record count fits.

The native execution form of `WorkerService.ReportWork.output_json` is a closed `ExecutionCompletion`: exact execution/input UUID-v7 identities and original harness-owned native-thread/native-turn identities, acknowledged publication sequence, native outcome and verified cleanup. Version 1 remains accepted as historical terminal proof and forbids a checkpoint digest. Version 2 requires a canonical lowercase SHA-256 `native_checkpoint_digest`, preserved exactly in the job result, binding the separately synchronized Worker-private continuation evidence. Native identity validation uses the immutable assignment’s harness: Codex remains UUID-v7, while the separate Claude profile preserves its supplied UUID-v7 session and original UUID-v4/v7 turn, and OpenCode preserves its original `ses_` session and claimed `msg_` input. OpenCode keeps its native twelve lowercase hexadecimal time/counter digits and fourteen base-62 random characters verbatim; a later assistant message cannot replace the original input boundary. Native identity support alone does not enable public Claude/OpenCode publication or dispatch, and omitted recovery-harness selection remains the historical Codex-only profile; explicit OpenCode recovery uses the independently bound comparison document below. The JSON string representation of retained Codex completions is unchanged. The checkpoint's effective native paths/settings and original content do not enter this completion message. A report is valid only after a matching retained terminal event and owned Worker cleanup. Session cleanup state and the terminal job commit together, preserve pause/recovery and use a reference-only job receipt. Invalid or missing proof retains the active execution for recovery and never returns unacknowledged input to the editable queue. Worker replacement/revocation also propagates uncertainty without erasing accepted input or terminal observations. Verified cleanup contributes to pending Archive completion; the central session publication barrier also requires independently confirmed session-terminal cleanup. Only a separate current-authority continuation claim can authorize another turn; future additional process/forward resources must join the same Archive cleanup gate.

The shared observed-settings JSON shape has an optional typed `opencode_agent` (`build` or `plan`) for the separate native default-policy profile. It must match the original harness, model and input mode; permission remains `default`, approval policy is empty, and unobserved effort/tier remain null. Claude/Codex profiles reject the foreign field. Omission does not alter retained settings JSON. This shape support does not enable OpenCode dispatch, registration or public event publication, whose authority gates remain separate.

`UsageService.GetUsageSummary` is owner/client-only under the [usage contract](cmds-delidev-usage-contract.md). It returns a coherent bounded 30-day-default summary of unique exact responses, original session/project/account/provider/model groups, optional current display labels, exact decimal-string known counters, measured/unavailable counts and explicit incomplete coverage and unavailable actual cost. Historical estimates use currency-separated decimal-string amounts, unpriced counts and original per-basis category evidence. Time and identity filters never change event attribution. No raw response/thread/turn identity or source content is exposed, and capacity failure returns no partial total.

The additive `UsageTimeGranularity` request enum accepts `UNSPECIFIED` (the existing summary-only contract) or `DAY`. `GetUsageSummaryRequest` adds `granularity` and `time_zone`; a nonempty zone is invalid with `UNSPECIFIED`, and `DAY` requires a valid explicit IANA zone. The optional response `analytics` carries applied granularity/zone, chronological calendar-day buckets with half-open Unix-millisecond bounds and exact `UsageTotals`, all bounded original provider/model groups, and optional server-aggregated `other_models`. It is present for every valid DAY request, including an empty result; absence therefore identifies an older server. Daily/model values come from the same authorized retained-response snapshot as existing groups and estimates. Existing clients remain valid, summary-only requests retain their prior shape, and generated Go/TypeScript/Connect Query sources are tool-owned.

Session terminals follow the [terminal contract](cmds-delidev-terminals-contract.md).
The additive terminal entity, typed system/Worker capabilities and
`TerminalService.CreateTerminal`, `ControlTerminal` and `WatchTerminalOutput`
use existing authenticated mutations/resources plus bounded original-byte
streaming. Worker-only watch/claim/report/publication messages bind the current
machine, instance and original paired device; claim and report receipt retries
revalidate that authority. Terminal result JSON is capped at 64 KiB to retain
both accepted 4,096-byte native paths under worst-case JSON escaping. Worker
problems require the terminal contract's closed native failure classification;
the server substitutes its own bounded diagnostic text before persistence while
receipt identity retains the original report bytes. Output carries epoch UUIDs, exact uint64 sequences,
raw bytes, explicit gaps and metadata heartbeats. Terminal public Resources omit pending input bytes in mutation/receipt responses,
generic reads/snapshots and output metadata; original authenticated Worker
watch/claim assignments retain exact dispatch bytes. Operation IDs, pending
state and original receipt digests remain intact. Terminal mutations return
current referenced records. A replacement retries an exact original-instance
`ReportTerminal` only to acknowledge an already-committed receipt under the same
current device/machine and live terminal-capable lease; this receipt-only response
omits `terminal` and grants no mutation or native authority. New reports retain
original current-instance checks. No new protobuf field or numeric reservation
is required for this acknowledgement recovery. Regenerate Go, TypeScript and Connect Query bindings
together; no output bytes enter durable events or mutation receipts.

Forwarding retains its merged `ENTITY_KIND_FORWARD = 27` and system/Worker
capability value `2`. User services retain system capability value `3`.
Terminals use the main-reserved additive entity value `31`, system capability value
`14` and Worker capability value `4`; no feature may reinterpret another's wire
values. Regenerate all bindings from this combined canonical schema.

## Storage
Protocol messages never authorize clients to access SQLite. Credentials are write-only inputs to protected storage. Entity reads, snapshots, search, usage, diagnostics, and events contain no authentication material.

## Security
Bearer authentication and exact origin enforcement include local RPC. Remote transport requires TLS. Pairing codes are expiring and single-use; revocation invalidates live streams and future authorization. Only the server resolves provider keys and account eligibility.

## Logging
Versioned typed failures carry safe recovery guidance and correlation IDs. Never serialize raw upstream errors or credential values.

### Native approval observation documents
`PublishExecution` interaction documents now admit `native-approval` with one closed `approval` payload instead of `questions`. The payload binds `harness`, native `version` and the version-specific `codex` request graph; the server rechecks immutable assignment provenance. Requests/metadata-only closure use existing interaction events and resource/inbox reads, without protobuf field additions. Every approval field is observational: neither this publication nor `RespondQuestion` grants approval response authority. Mixed request payloads, response fields, unproposed decisions and changed native identity fail atomically. Question and approval records share retention bounds and independent unread/read state.

### Approval response RPCs and controls
`InteractionService.RespondApproval` is owner/paired-client-only and accepts a mutation identity/revision plus a closed `ApprovalResponseInput` JSON document. The request UUID is the response UUID. The original `native-approval` graph selects one offered native decision or a permission grant with explicit scope; mixed question fields and broadened/unoffered authority fail before persistence. Actor-bound reference-only receipts return the current interaction on exact replay, including after closure. They cannot requeue or resend.

`WorkerService.ClaimApprovalResponse` is current owning-Worker-only, matching the exact response, interaction revision, job, machine, instance and device. `WatchWork.approval_response` field 7 is its metadata-only active-assignment control; it is mutually exclusive with job, heartbeat, cancellation, question and Steer forms. Question and approval controls share the total response bound and cannot reuse an identity across kinds. Claim receipt replay rechecks current authority and returns no response content after closure, delivery or authority loss.

`PublishExecution.approval-delivery-observed` contains one dedicated `approval_response` with interaction/response/claim UUIDs, native item identity and closed delivery enum. Content never enters this event or claim receipts. The server preserves one immutable observation/sequence and separately tracks unconfirmed acceptance; native closure, tool success and terminal cleanup cannot mark semantic acceptance. The product interaction stores `approval_response` separately from the question-only `response` field, preserving existing readers and schema version. No database schema migration is required.

`PublishExecution.approval-accepted` adds one closed content-free `approval_acceptance` payload with interaction/response/claim UUIDs, native item identity and `native-permissions-output` evidence. It is valid only for the exact retained Codex permissions grant after a possible-delivery observation from the current owning Worker. The server stores evidence/sequence under `approval_response.acceptance`, marks the response accepted and decrements outstanding acceptance once; original grant, delivery, closure and independent recovery remain unchanged. Command/file approvals, foreign/duplicate evidence and content-bearing payloads fail atomically. Exact outbox/receipt replay repeats no native send or accounting. This additive domain JSON form changes neither protobuf fields nor database schema. Native history recovery remains separate required work.

A validated `tool-completed` document can also atomically retain `native-approved-command` or `native-approved-patch` acceptance for its uniquely owned original single-use `accept`. The server derives this only from the pinned successful-execution profile, earlier tool start, original approval/response and exact delivery claim; command identity/provenance/zero exit and patch success are independently checked. Acceptance and tool completion share the event sequence and transaction. These evidence kinds are forbidden in standalone `approval-accepted` documents; there is no new RPC or protobuf field. Session/policy decisions, ambiguous/unsuccessful execution and historical recovery remain unconfirmed.

## Build and Test
Use the root pinned Buf/Go protobuf/Connect generators. Never edit generated code. Run schema formatting/lint, generation freshness, and command integration tests after protocol edits.

## Dependencies and Integrations
Root `buf.yaml`/`buf.gen.yaml`, Connect Go, protobuf, and `cmds/delidev-cli`.

## Change Triggers
Protocol changes update this contract and [command contract](cmds-delidev-contract.md), with generated output and compatible version handling in the same change.

## References
- [Project](project-delidev.md)
- [Repository defaults](repository-defaults.md)

### Completed execution recovery RPC
`SessionService.RecoverSessionExecution` accepts a session mutation revision and exact `expected_execution_id`. Owner/client actor-bound, reference-only receipts return current `SessionChange` references; the additive `execution_recovery_job` field 8 is separate from preparation recovery field 6. The new closed `recover-execution` job carries only original assignment/scope/terminal/input-digest comparison facts and workspace metadata. Its content-free output retains the original report UUID and version-2 completion; it grants no old-instance report authority or execution credential. `ReportWork` checks current recovery-job ownership, validates original/current proof bindings again and atomically reconciles the original job/session while preserving pause. Recovery reports also use reference-only receipts. Worker loss or invalid evidence retains uncertainty; no RPC can infer missing native acceptance or resend input. This additive protocol/domain change requires no SQLite schema migration.

### Local session origin
`CreateSessionRequest.local_worker_token` is an additive write-only authentication field for explicit Local creation. Owner/paired-client authorization remains mandatory. The server validates the secondary paired Worker credential and selected machine, then rechecks it inside the creation transaction; only derived `local_origin.machine_id` and `device_id` enter session state and the actor-bound request identity. The credential never enters resource documents, jobs, events, receipts or logs. Other workspace types reject the field. The CLI obtains it from `--local-worker-dir` after validating the private scope, exact endpoint and authenticated server ID/protocol. Later clients cannot change the original machine; dispatch/Resume require the retained origin device to remain valid. Native Local ready manifests additionally carry per-repository `local_identity_digest`, validated against original Worker-local Git identity without resetting current contents.

### Schedule lifecycle RPCs
`ScheduleService` is owner/paired-client-only. `SaveSchedule` accepts a version-1 strict `ScheduleDefinition` and mutation metadata: expected revision zero creates a supplied UUID-v7 identity or allocates one when the ID is omitted, while edits require its current resource revision. Its write-only `local_worker_token` proves new/changed Local origin; an unchanged Local machine may retain the previously authenticated origin without another token. Timer/configuration revisions, creator, disabling problems and occurrences are server-owned. Mutations use actor-bound reference-only receipts and join current resources on replay.

`GetSchedule` exposes the enabled state, authoritative next due instant, timezone and current revisions. `ListSchedules` filters project and optional enabled state, with bounded signed cursor pages tied to filter scope and the schedule event epoch. `ControlSchedule` requires the closed pause/resume enum and current revision. Resume computes the first future due instant and cannot clear a disabling problem; reconfiguration is required. `DeleteSchedule` removes configuration only, preserving accepted occurrences and sessions. `RunScheduleNow` uses the same configured overlap policy and durable acceptance as cron, records manual initiation, and returns its current occurrence plus any retained owned session. It is available for paused, otherwise valid schedules; disabled configurations carrying a problem require reconfiguration.

`ListScheduleOccurrences` and `GetScheduleOccurrence` remain usable after schedule deletion. History uses immutable acceptance sequence, bounded pages and a signed schedule-scoped cursor bound to the occurrence event epoch; current session state is not copied into history. Unknown fields/enums, foreign scopes, stale revisions and forged timer/origin fields fail before side effects. Worker credentials cannot invoke these RPCs. Raw secondary credentials never enter documents, receipts, events or logs. Public CLI lifecycle and native scheduling evidence are recorded separately in the command/evidence contracts.

### Referenced Project and Agent deletion
`DeleteConfiguration` accepts revision-checked Project/Agent deletion even with retained sessions. Deletion atomically disables matching schedules and retains a tombstone; existing session/occurrence/snapshot records are unchanged. For established execution snapshots, the server privately retains final project selection restrictions so current-state authorization cannot become more permissive after deletion. No deleted configuration becomes a new selectable resource or first-dispatch source. This uses the existing configuration RPC and typed errors without new wire fields; ordinary session lifecycle and receipt joins remain independent.

### Conversation search RPC
`SearchService.SearchConversations` is owner/paired-client-only and reads complete current message resources with bounded session metadata. Literal query plus session/project/Agent/original-execution-account/outcome/archive filters follow the search contract; unknown enum values fail. Signed pages bind the actor, all normalized selectors and source epoch through a keyed query commitment. Reads recheck revocation within the same transaction; both protobuf and JSON pages are byte-bounded. Search never changes read state or execution.

### Activity RPC
`ActivityService.ListActivity` is owner/paired-client-only and returns closed event/occurrence enums, original source IDs/revisions and server retention time. Execution acceptance, native terminal evidence, scheduling and PR decisions/outcomes remain distinct. Additive `ActivityPRMetadata` carries original source/revision, stable remote PR identity, exact content-version references, owner/client actor/request and typed attempt mode/state. PR transition UUIDs are immutable; `PR_HANDLING_V1` is explicitly advertised. Verified handling requires its own dedicated source identity, never a successful attempt. No public verification write or execution control is added. No source documents, prompt/output content, instructions, credentials or native thread/turn IDs enter this response. Current-source pages bind actor/session/project/source epoch, are bounded, and recheck revocation in the read transaction; original execution jobs retain account attribution.

`UsageService.GetModelPricing`, `GetPricingVersion` and `SetModelPricing` expose explicit typed nullable decimal token rates with source/date/currency/mode/exclusions. Owner/client writes use the model UUID/configuration revision and independent price revision; actor-bound reference-only receipts return the immutable accepted version even after later pricing changes. Worker access is denied and authorization is rechecked in the read/write transaction. Price versions and original estimates survive configuration changes; there is no automatic historical repricing.

### Session budget messages
`SessionService.GetSessionBudget` and `SetSessionBudget` are owner/client-only with transaction-time authorization and two-second bounds. `SessionBudgetView` combines the current session, optional budget, explicit state/coverage, selected lifetime currency evidence and separate unpriced/other-currency counts. Exact decimals remain strings; missing amount is distinct from zero. Set requires an existing-session Mutation and exactly one explicit budget or `remove=true`; actor-bound reference-only receipt replay returns current state without restoring an old budget. Creation may include the same strict domain budget. The shared `budget_reached` error is FailedPrecondition, preserves pending/accepted work and cannot imply actual-billing completeness. Generated Go/TypeScript/Connect Query bindings are tool-owned.

### OpenCode native text ownership
The existing execution-event/resource JSON documents now allow `native_parent_id` on normalized text message and plain-text reasoning artifact updates and records. OpenCode requires the exact original `msg_` parent for each `prt_` text or reasoning item, while its execution turn remains the original user input message. Start/delta/completion cannot retarget a part, omit its parent or invent a message phase. Other current publication profiles reject the added field; historical Codex documents continue to omit it without wire or schema-version changes. The additive `reasoning-text` artifact kind carries one bounded text snapshot and index-free suffix deltas, with null summary/content arrays and exact monotonic completion. Other profiles reject this OpenCode-only artifact shape. The Connect protobuf envelope and generated bindings are unchanged. This ownership field does not enable unimplemented native event families, public dispatch or continuation.

### Original OpenCode Read documents
The unchanged execution JSON envelope additionally accepts `tool-updated` for the pinned OpenCode tool profiles, beginning with `opencode-read`. Its `tool-started` snapshot is pending, intermediate snapshots retain execution sequences, and completion preserves native success or error independently from input outcome. `native_parent_id` extends tool updates/records with the same original OpenCode message ownership as text/reasoning. Read payloads contain closed typed original input, proposal, timing, result/error and metadata; they cannot carry arbitrary native extensions or grant file/network access. Other current profiles reject this tool kind, parent field and intermediate-update event. Historical Codex JSON omits all new optional fields. Protobuf bindings and SQLite schema are unchanged; call uniqueness uses the existing bounded execution index inside the original publication transaction.

### Original OpenCode usage documents
The additive `opencode-usage-observed` execution JSON event carries an observation UUID and one closed `opencode_usage` payload, independently from existing Codex usage/response fields. Its immutable `UsageKind` record uses `opencode_observation`, with original step/message source identities, decimal-string native categories, nullable total and exact unpriced native-estimate spelling. Reject mixed/cross-harness payloads and user-message parent substitution. Server-derived attribution, source uniqueness, original receipt, progress reference and resource/event publication share one transaction. This profile cannot enter the response ledger or alter budgets, and generated protobuf sources remain unchanged.

### Original OpenCode terminal publication
The pinned first-input OpenCode profile now accepts the existing `turn-finished` envelope only with a completed original user transcript record and retained finalized-assistant usage source, under all existing execution/sequence/Worker gates. Successful publication also requires every retained message complete. Preserve existing terminal inbox, late Stop/recovery behavior and reference-only receipts. A terminal observation leaves the claimed job and cleanup evidence unresolved; it cannot serve as a completion report, new input or continuation capability. No protobuf or SQLite schema changes are required.

### Original OpenCode Todo documents
The additive `opencode-todo` tool payload follows the existing original tool lifecycle, with exact `todo.input.todos` and typed result metadata. It preserves missing proposal versus explicit empty list, original string statuses/priorities and cross-kind call uniqueness. The unchanged `progress-observed` envelope additionally accepts `progress.kind=opencode-todo` with `todo.native_event_id` and ordered `todo.todos`; it cannot mix Plan/Diff or cross harness profiles. Session progress retains the optional `latest_todo_id` reference. Native session events have no message or part ownership; product message records keep those native fields empty. Server validation, event deduplication, receipts and latest reference commit atomically. Generated protobuf bindings and SQLite schema remain unchanged.

### Original OpenCode change documents
The additive `opencode-revision` artifact snapshot has a closed `revision` source/hash/files payload, empty text and absent reasoning arrays. Start and completion are identical immutable observations; no text delta applies. Original `prt_` and assistant `msg_` ownership remain mandatory, and other harness profiles reject the kind. `opencode-changes` progress carries closed `changes` source/event/input-summary identity and original optional title/body/file/patch/status with bounded independent additions/deletions. Reject mixed progress kinds, foreign input summaries and reused original events, including reuse across Todo/change kinds. The existing `latest_diff_id` references the latest observed change resource. Protobuf bindings and SQLite schema remain unchanged.

### Original OpenCode builtin and workspace documents
The additive `opencode-builtin` tool kind uses a disjoint `builtin` payload with closed name (`write`, `edit`, `apply_patch`, `glob`, `grep`), original call/input/timing/result/error and optional provider execution flag. Required `input_json` and optional `metadata_json` retain complete bounded original JSON objects as strings to preserve native number spelling and extension fields. Completed tools require original title/output/metadata, failed tools retain error without invented output, and running input/start remain immutable. Shared transactionally unique call ownership includes all supported OpenCode tool kinds.

The additive `opencode-workspace` progress has a closed `workspace` kind (`file-edited`, `file-added`, `file-changed`, `file-unlinked`), original `native_event_id` and bounded path. It cannot mix other progress payloads. Native instance notifications have no message/part index owner; the surrounding execution records the observing original stream only. Original event deduplication spans workspace/Todo/change observations, and `latest_workspace_event_id` is updated atomically with receipt/resource/event publication. Protobuf bindings and SQLite schema remain unchanged.

### Original OpenCode interaction proposals
The unchanged `interaction-requested` JSON envelope adds an optional disjoint `opencode` request payload with pinned version, original event/assistant/call and exactly one permission scope or ordered question matrix. Existing `native_item_id` is the observed original `prt_` tool, and text `native_request_id` retains `per_` for approval or `que_` for questions. Existing user-question/native-approval enum values classify inbox display only; Codex `questions` and `approval` payloads must be absent. Optional matrix flags retain absence/false without invented question IDs. Native `question` tool snapshots extend the closed builtin-name enum. Request/index/inbox publication uses existing bounds, transactions and receipts; response/closure publication is not enabled by this proposal profile. Existing generated protobuf bindings and SQLite schema remain unchanged.

Original OpenCode response JSON is disjoint from Codex: question responses use `{"opencode":{"answers":[["choice"],[]]}}` or disjoint `{"opencode":{"reject":true}}`; native permissions use `decision` values `once`, `always` or `reject`, with optional exact `feedback` only on rejection. Existing owner response and Worker claim RPCs preserve original request/revision/claim authority; no schema regeneration is required. Accepted reply events use typed native evidence with original proposal/reply/request identity, exact body digest and confirmed HTTP acceptance. Neither server queue acceptance nor transport delivery closes a request. Automatic permission closures use an additive `opencode_closure` payload with original event/proposal identities, decision and bounded observed direct-response references; they never synthesize target acceptance. Question-rejected evidence remains distinct from question-replied evidence. Protected responses and public OpenCode dispatch remain separate.

### Original OpenCode Stop evidence
The additive `opencode_stop` JSON payload is separate from direct reply and policy closure. Terminal events and retained execution progress carry original Stop/input request UUIDs, native input-part/final-assistant IDs, exact lowercase history digest, and independent HTTP/interruption/terminal/idle/pending/cleanup facts. Optional bounded unique retry event/attempt/next observations and `retry_canceled_observed` preserve the pinned acknowledged-backoff cancellation shape without synthesizing a native error or publishing provider prose. An interrupted or canceled-backoff terminal uses stopped/canceled; normal native completion racing Stop retains its original outcome.

Interaction `opencode_stop` wraps that same proof with the exact original proposal event and independently interrupted-tool flag, requires `turn-ended`, and cannot coexist with `opencode_closure`. The server binds all repeated Stop evidence within the original execution, requires its durable job cancellation and completed original input part, and permits cancellation only for unanswered or unclaimed queued requests. Exact receipts preserve sequence, native identity and inbox read state. Existing protobuf envelopes carry these versioned JSON documents; no protobuf or database migration is needed. These facts do not authorize public dispatch, new responses or continuation.


### OpenCode recovery comparison documents
The existing `recover-execution` job input adds optional `harness` and disjoint `opencode` fields. Omitted `harness` retains historical Codex behavior and omits both fields on existing Codex producers. Explicit `opencode` requires the matching payload containing `claim_version`, `creation_request_id`, `binding_request_id` and `input_request_id`; first claims use the original creation as binding, resumed claims retain a separate original creation. Other harnesses, mixed payloads and malformed/reused request identities are rejected. The server obtains these facts from original immutable assignments, never from Worker-supplied native ID spelling or checkpoint metadata. Completion validation uses this selected harness while retaining the same report/evidence JSON structure, receipt identity and paused atomic recovery semantics. Public Build/Plan OpenCode continuation uses existing version-2 execution assignment/completion documents. No protobuf field, generated binding or database schema changes are required.


### OpenCode inline-tool checkpoint eligibility
The existing version-2 completion and explicit OpenCode recovery comparison envelopes also apply to newly captured eligible closed inline Read/Shell histories. Eligibility remains private checkpoint evidence, bound by the original report digest and complete native history; no new protobuf field or generated binding is introduced. It cannot promote historical version-1 reports, infer restored permissions from SQLite, or use retained tool metadata as execution authority. Public events and retained original tool records remain unchanged across resumed executions and completed-report recovery.


The private OpenCode tool-proof version 2 additionally admits exact originally accepted one-time permission claims. This changes checkpoint eligibility, not the protobuf/public response envelope or retained product interaction shape. Historical version-1 reports cannot be promoted, old replies cannot be resent, and remembered permissions require their own restoration contract.


Private OpenCode tool-proof version 3 additionally binds confirmed direct Read `always` claims, exact ordered native allowance rules and their already-materialized prefix. The existing predecessor-bound resume claim owns the native append; public protobuf/approval/completion envelopes remain unchanged. Completion recovery stays read-only, and failed native append responses grant neither retry nor continuation authority.


Private OpenCode tool-proof version 4 additionally retains original automatic Read policy closures and their independently verified direct source context. This introduces no protobuf or public response/claim shape change. Existing `opencode_closure` documents remain distinct from direct accepted approvals and are not republished or rewritten during replacement or completed-report recovery.


Private OpenCode tool-proof version 5 additionally retains independently accepted completed Question response claims and original closure identities. It introduces no protobuf/public question-response/completion change and contains no question or answer text. The original public accepted response remains distinct from later native restoration; historical report versions cannot be upgraded.


Private OpenCode tool-proof version 6 adds eligible inline search/Todo parts and optional content-free original Todo history observations. Public protobuf, tool/progress, response and completion envelopes are unchanged. An original Todo event/digest remains separate from tool ownership and native list mutation; old reports and absent legacy observations cannot be reconstructed into new authority.

Private OpenCode tool-proof version 7 admits independently eligible completed inline Write/Edit/Apply Patch parts. No public protobuf, builtin/progress or completion envelope changes. Original input/metadata JSON stays exact, diagnostics and diff paths remain inert content, and prior proof/report versions cannot acquire file-mutation replay or restoration authority.

Private OpenCode tool-proof version 8 retains original Question dismissal claims and closure events using their existing native reject identity. Public response, builtin and completion messages do not change. Empty reject bodies stay distinct from empty answer matrices, and original stopped/Resume semantics cannot be rewritten as successful answered-question completion.

Private OpenCode tool-proof version 9 adds explicit failed Read markers and original direct/automatic rejection evidence without changing public protobuf, response, progress or completion envelopes. Absent/empty/nonempty feedback keeps its existing response semantics and original body digest. Automatic rejection provenance remains separate from a direct response and cannot add permissions or authorize replay.

Private OpenCode tool-proof version 10 adds an optional positive `instructions_loaded` marker for original completed Read parts. Complete original part digests bind paths/output; no instruction content or additional path list enters proof metadata. Public protobuf, Read/progress, interaction and completion envelopes remain unchanged. Prior versions cannot be promoted, and completed-report recovery creates no instruction-read or input authority.

Private OpenCode tool-proof version 11 adds an optional typed native permission name to original direct/automatic rejection records and permits the corresponding eligible failed tool markers. Omitted names preserve the prior Read/read contract. Public response, builtin/progress, completion and protobuf shapes do not change; original feedback and automatic source ownership remain separate, without permission grants or replay.

Private OpenCode tool-proof version 12 adds an optional typed ordinary Read error profile on original tool-part proof. This is distinct from original accepted permission rejection and introduces no public Read/progress/response/completion or protobuf changes. Original complete part digests bind error content without copying it into proof metadata; old versions, canceled replies and current-input Stop cannot acquire ordinary-error authority.

OpenCode tool-proof version 13 is private checkpoint metadata for original external-directory remembered allowances and automatic closures. Existing public permission, execution-completion and recovery messages retain their wire shapes and accepted identities; no new public permission or replay authority is introduced.

Claude callback response composition extends only the existing closed domain JSON documents. `RespondQuestion`/`RespondApproval.response_json` may contain exactly one `claude` object with `behavior`, original question-text/string `answers` for question allows, or a required denial `message`; native input edits and permission updates are excluded. An explicit true `interrupt` uses the separately verified original root-denial context/result composition below. The existing claim/control/delivery RPCs preserve immutable ownership, receipt and transport semantics. `PublishExecution` adds `claude-reply-echo-observed` with exactly one `claude_reply_echo` containing original interaction/response/claim/arrival UUID-v7 identities, native tool identity and canonical lowercase SHA-256 native-body digest. Only the original Claude profile may publish it after possible delivery, with one retained echo per response. Stored question/approval responses gain optional `claude_echo` metadata/sequence; acceptance, closure and unconfirmed counts remain unchanged. Original tool results gain optional matching `non_execution` metadata (`id` and `permission-rule` `non_execution_kind`) only with explicit native error evidence. These additive forms change no protobuf fields, generated bindings or relational schema and do not enable public Claude dispatch or continuation.

The additive execution JSON event `claude-callback-settled` carries `claude_settlement` with the original response/claim/arrival/tool/body digest and tool-message/native-result/evidence references. The interaction retains a bounded typed `claude_settlement` record and the existing response acceptance observation. This event is Claude-only; generic acceptance publications cannot inject its evidence. Settlement updates acceptance, callback closure and only the matching unconfirmed count transactionally, without changing Inbox read state or terminal/recovery. Protobuf, generated bindings and relational schema stay unchanged.

Claude callback settlement adds `native-claude-plan-approval` evidence for exact unchanged root Plan output. The original result retains all metadata; acceptance rejects edited/delegated/pending-leader output, changed plan/path and explicit null/unknown fields. Non-interrupting Plan denial retains the existing distinct native-denial evidence. This is additive JSON evidence without protobuf/generated-binding/schema changes.

Claude original interrupted denial adds `native-claude-interrupted-denial` settlement evidence with explicit original `user-rejected` non-execution metadata. Its dedicated `claude-interruption-observed` event owns only `claude_interruption` (`id` and typed `observation`) and cannot mix terminal, usage or other payloads. The observation retains original callback/tool-result references plus its independent envelope UUID, exact context or a closed uncorrelated session result with `native_input_id: null` and original typed result usage. Execution progress stores only context/result/interaction references; messages retain the original data. Result publication atomically preserves input acceptance and outcomes while setting the independent pause/recovery gate. No protobuf fields, generated bindings or relational schema change.

### Claude original streaming Stop proof
The existing Worker event JSON now admits a dedicated `claude_stop` proof only on a product `turn-finished` event with stopped outcome. It is mutually exclusive with `claude_terminal` and every other harness's Stop payload. Preserve original initiating input/request/message ownership separately from explicit absent native result input identity, original aborted-assistant versus closed-stream-before-retry evidence, exact bounded retry counters, uncorrelated native usage and joined native cleanup. The same transaction finalizes the original partial message with an interrupted block; the separate version-1 completion report still owns workspace cleanup. No protobuf source or relational schema changes are required for this additive typed Resource JSON boundary.


Claude progress JSON adds the `api-retry` kind, its optional typed `api_retry` observation and `latest_retry_id` reference. The retry payload retains original event UUID, exact unsigned decimal strings for `attempt`, `max_retries`, `retry_delay_ms`, explicit nullable `error_status` and closed `error`. Existing status/thinking documents omit this optional field and preserve their stored bytes. Original Stop retry JSON remains unchanged. No protobuf, generated-binding or relational schema changes are required.


The typed execution-event JSON adds `claude_denial` only to stopped `turn-finished` records, mutually exclusive with correlated `claude_terminal`, explicit `claude_stop` and other-harness Stop proof. It retains original initiating `input_id`, `interaction_id`, `arrival_id`, `context_id`, `result_id`, separate `command_native_id`/`idle_native_id`, explicit `native_input_id: null` and required `cleanup_verified: true`. The same proof is retained on execution progress. Existing version-1 Worker reporting confirms workspace cleanup separately. This additive Resource JSON contract changes no protobuf fields, generated bindings or relational schema.

### Original Claude citation message extension
The existing version-1 execution JSON adds `block-citation` to Claude provider-message mutations and optional typed citation collection/completion fields to block start/complete. Retained provider blocks may carry the original initial/delta/completed citation history. These fields use exact decimal uint64 coordinates and bounded closed source unions, with no opaque encrypted indices or action authority. The harness/session contracts define strict original lifecycle, completion omission and receipt semantics. This is an additive JSON extension; protobuf service signatures, generated bindings and the relational schema are unchanged.

### Session workspace observations

`SessionService.ReadSessionWorkspace` and Worker-only `WatchWorkspaceReads`/`ReportWorkspaceRead` implement the [file explorer contract](cmds-delidev-files-contract.md). Their closed JSON envelopes retain bounded exact metadata, decimal file sizes and relative paths. The secondary Worker stream depends on current primary ownership and cannot renew it; queries never enter durable jobs or mutation receipts. Worker errors carry closed codes only, and late, foreign, malformed or duplicate reports cannot release content.

The same private Worker stream additionally carries an exclusive `pr_candidate` target with a zero file query for the [native PR workspace matching contract](cmds-delidev-workspace-contract.md#current-native-matching-for-an-existing-pr-session). Its closed version-1 metadata response is capped at 4 KiB and binds original read/session/repository IDs, complete request digest, matching state and observation time. Neither response family can substitute for the other. Public `ReadSessionWorkspace` remains the existing file API and cannot submit this internal profile. This additive private JSON shape changes no protobuf signature or generated binding and creates no execution grant, durable job or receipt.

## Portable configuration operations
`ConfigurationService.ExportConfiguration`, `PreviewConfigurationImport` and `ApplyConfigurationImport` expose bounded versioned owner/client JSON documents. Preview is read-only and signed for its exact actor/server/plan. Application uses one request identity and returns the original coordinator job outcome on reference-only replay; repository inspection remains Worker-only. See [portable configuration](cmds-delidev-configuration-transfer-contract.md). Generated Go and TypeScript/Connect Query bindings must reproduce exactly.

### Grok original binding observation

The version-1 `PublishExecution` document adds an optional typed `ObservedExecutionSettings.grok_mode` field with exact `default`/`plan` values, emitted only for Grok's separately verified first-input `thread-bound` profile. The matching `input-accepted` preserves the original UUID-v4 prompt under its native UUID-v7 session. Other harness observations omit this field and retain their prior JSON shape; they cannot inherit Grok policy or identity authority. Queue accounting and exact receipt retries retain existing semantics. Format support does not enable Grok `ReportWork` completion, other event families, continuation or public dispatch. Existing authenticated Resource reads, CLI and desktop inspection expose the original retained observation without a new RPC or protobuf schema.

### Grok text and response documents

Version-1 `PublishExecution` adds closed `grok-text-observed` / `grok-usage-observed` event kinds with exclusive `grok_text` / `grok_usage` payloads. Text uses a product message UUID-v7, response ordinal, original text and exact native chunk metadata; response usage uses a product observation UUID-v7, bounded ordinal and five decimal uint64 counters. Retained Message resources carry `grok_text.chunks` with every original metadata entry and the first event as their native provenance anchor. Usage resources carry the separate `grok_observation`; execution progress carries bounded `grok_content` ordering state. See the harness/session/usage contracts for original ownership and limits. These additive Resource JSON fields require no protobuf service, generated-binding or database schema change and cannot grant other harnesses Grok publication authority.

### Grok closed first-text terminal document

Version-1 `PublishExecution` additionally permits an exclusive `grok_terminal` payload only on Grok's separately validated successful `turn-finished`. Its closed `closed-first-text` kind, original terminal event/timestamps/model, exact decimal uint64 counts/total/durations, one call/turn, closure UUID-v7 and original history digest follow the harness contract. Retained execution progress carries that original payload. `ReportWork` may then carry the matching version-1 `ExecutionCompletion` after independently owned workspace cleanup; no Grok version-2 checkpoint is accepted; the separate original Stop union below permits its independently validated outcome. Binding/text/counter syntax alone cannot authorize this completion. Protobuf services, generated bindings and the relational schema remain unchanged.

Grok first-text assignments add optional immutable `configuration.grok_context` (`tokens`, `source`) to Resource JSON, with an explicit known/user-declared model limit from 1,024 to 1,000,000,000. Original `observed.grok_context_tokens` must match it before input acceptance. Historical omission preserves bytes and grants no runner default. Public initial Execute/General Chat dispatch and the ordinary Worker now compose the version-1 terminal/workspace report; no protobuf or database migration is needed. Continuation and other native terminal profiles remain separate.

### Grok original Stop Resource documents

Version-1 `turn-finished` permits an exclusive `grok_stop` with original Stop request, product input, input operation, optional product message, native terminal event/timing and selected model. Its closed kind is `interrupted-before-text` (native `MidTurnAbort` and context; no message/content/usage; zero chunks and SHA-256 of empty output), `interrupted-text` (native `MidTurnAbort` and context; no completed usage) or `completed-during-stop` (original successful one-response counts/total/durations; no context replacement). Canonical uint64 decimal strings and original typed HTTP retry metadata remain distinct from response totals and billing. Original output SHA-256, chunk count and delivered/idle/native-cleanup facts are mandatory. No closure/history/checkpoint is synthesized. Retained partial `grok_text.interruption` binds the Stop request and terminal event; message storage finality does not assert `response_completed`.

The server validates existing cancellation, native/content ownership and original counters atomically before retaining this union. A matching version-1 `ReportWork` independently proves workspace cleanup and grants no continuation. Fields are additive Resource JSON; protobuf services, generated bindings and SQLite schema are unchanged.

### Session Git diff observations

The existing workspace-read JSON envelope adds operation `git-diff` and a closed `comparison` selector (`working-tree`, `staged`, `creation`). An exclusive optional `diff` result retains original repository/path, base kind/object, actual optional HEAD, bounded complete patch, separate untracked paths and SHA-256 revision. Worker and server validate scope and exact revision; mixed file/directory/diff payloads are rejected. Current authentication, cancellation, primary-stream ownership and late-report rules remain unchanged. This is additive JSON under existing Connect methods; protobuf, generated bindings and SQLite schema are unchanged. See [files and Git comparisons](cmds-delidev-files-contract.md).


## Local review coordinates

`SessionService.ReadSessionReviewContext` accepts a session UUID and the closed `git-diff` workspace query. It returns at most 1 MiB of exact original diff and parsed ordinary file/line coordinates, derived from the owning Worker through the existing read channel. Owner/paired-client authorization and original observation ownership still apply. Binary/non-line changes are file-only; ambiguous/combined/incomplete patches are unsupported. This read does not persist comments, create receipts or grant submission authority. See the [workspace and review coordinate contract](cmds-delidev-files-contract.md).


## Durable local review mutations

`SessionService.CreateLocalReviewComment`, `EditLocalReviewComment`, `DeleteLocalReviewComment` and `SubmitLocalReview` are dedicated owner/client operations. Creation/submission carry bounded closed JSON, session and request UUIDs; edits/deletion carry exact mutation metadata. Responses expose retained review Resources and, for submission, the ordinary SessionChange/input link with replay status. Review resource documents are exclusive version-1 comment/submission variants. Generic Resource reads/pagination expose them without introducing generic mutation authority. The [local review contract](cmds-delidev-files-contract.md) defines original anchors, freshness, atomic queue acceptance, retained snapshots and reference-only replay. Protobuf changes are additive and generated bindings reproduce from the schema; SQLite schema remains unchanged.

### Grok closed-history user documents

The Grok `input-accepted` JSON event may carry `grok_user_message_id`, a UUID-v7 reserved before assistant publication and retained in execution progress. It creates no Message resource. Original successful `grok_terminal.user` carries the closed source, native user event, canonical millisecond timestamp, zero prompt index, selected model and exact input SHA-256. Only the original closed-history composition can atomically create its complete `grok_user` Message with accepted input text and input reference. Historical omission of both reservation and proof remains readable; mixed pairs, other event kinds/harnesses and unsupported Stop history cannot acquire a user record. Existing Resource RPC/CLI, event identity/revision synchronization and message indexing expose these additive JSON fields without a protobuf or database migration.


## GitHub integration profiles

Owner/client-only `IntegrationService` has strict-definition Save, write-only bounded-PAT Replace, Validate and Delete operations, separate from AccountService. Follow [the integration contract](cmds-delidev-integrations-contract.md). Mutations bind actor/original revision/request identity; accepted results return current metadata and typed optional problems. Pending-operation expected revisions use decimal strings in JSON. A deleted profile retains its tombstone/receipt without token or historical content; generic configuration cannot write connection/identity/pending state. Worker authentication cannot invoke this service.

`InspectRepositoryIntegration(repository_id)` returns a read-only schema-version-1 observation document bound to the local repository UUID/exact decimal revision and selected profile/generation. Fresh identity and remote repository projection accompany eight independent endpoint-access states. It creates no receipt, persists no access state and returns no saved PAT. The integration contract defines bounded lifetime, joined cancellation, scope revalidation and the separation from CI/rules/reviewer decisions.

`QueryRepositoryIntegration` takes local repository UUID plus versioned strict list/search/detail or PR-only diff/checks/statuses/rules/ci/feedback/reviewers query JSON and returns a versioned scoped observation. It shares the integration read lifetime/authorization boundary, carries no receipt or PAT, and preserves exact decimal IDs with issue-versus-PR API provenance, live pagination, search incompleteness/limits and unknown mergeability. PR observations retain exactly one original detail and one exclusive diff/Checks/status family, exact base/head binding, native unknown states and separate totals; they cannot establish evaluated-commit/ruleset eligibility. Follow the integration contract for complete response validation. These version-1 JSON additions do not change protobuf fields.

`SessionService.LinkSessionPullRequest` resolves a stable PR identity through the explicitly configured repository/profile before atomically creating a session/project-owned `pull_request` resource. The request contains only original request/session/repository UUIDs and a canonical decimal PR number; recheck project membership/revision, repository/profile generation and authorization before commit. `UnlinkSessionPullRequest` takes the original association mutation/revision and session UUID. Both are owner/client-only, preserve actor-bound receipts and cannot alter execution or remote GitHub state. ResourceService scoped list/get/snapshot/events retain historical links after Archive or profile reconfiguration. Exact link replay is read-only; a deleted association cannot be recreated by an old receipt. Follow the integration contract for bounds and complete response validation.

`IntegrationService.GetGitHubTokenForm` is a non-mutating owner/client profile/revision-bound read. The closed `GitHubTokenAccess` enum distinguishes fine-grained selected repositories from classic public/private choices. Schema-version-1 response JSON contains canonical non-secret form configuration only; it never reads/returns a token or launches the server browser. Unknown/incompatible selections, pending changes and stale revisions fail.

The PR `rules` observation is an exclusive version-1 JSON family with no caller pagination. It binds exact PR base/head, a bounded complete applicable-active-rule inventory, original source/type/required-context/App provenance and recomputable aggregate digest. Partial inventories, mixed families and foreign scopes fail; no protobuf fields or generated descriptors change. Follow the integration contract for limits and repeated-read consistency.

PR-only `ci` adds one exclusive version-1 JSON observation containing complete active rules, head/test-merge rollups, original required/App/workflow evidence and a domain-recomputed assessment with exact result-node references. Preserve complete totals, known/unknown states and current PR binding; missing permissions/partial errors cannot produce passing CI or automation authority. Unknown required-rule parameters are retained as a non-secret boolean alongside their original policy digest. No protobuf/generated/schema migration is required.

PR-only `feedback` adds an exclusive version-1 JSON observation with complete published entries, original content versions and review/thread references plus excluded-draft counts. No caller pagination, partial data or mixed observation families are accepted. Domain validation recomputes content digests and checks published parent states, exact source URLs and original IDs. Native review dismissal remains separate from local handling; no new protobuf/generated field or SQLite schema is needed. Follow the integration contract for full inventory and bounded cancellation.

PR-only `reviewers` is an exclusive version-1 JSON family containing complete feedback plus exact per-author current identity/permission and per-entry App attribution. Typed access/attribution states distinguish unknown from absent proof, and permission intervals retain custom-role uncertainty. Validate complete coverage, exact references and original versions before publication. No protobuf/generated field or database migration is introduced; persisted policy/controller operations remain separate.

### Retained PR feedback operations

`IntegrationService.RefreshPullRequestProblems` accepts only a request UUID, configured repository UUID and exact PR number. The server resolves the stable remote identity and independently collects complete published feedback; clients cannot submit evidence. `ListPullRequestProblems` reads retained history by exact GitHub repository/PR numeric IDs without requiring current remote access. Pages contain at most 50 original resources and 1 MiB of document bytes, with a signed identity/size/set-revision-bound cursor. `DismissPullRequestProblem` requires the original problem UUID, resource revision and content version. All three operations require a current owner or paired client. Mutations use actor-bound reference-only receipts; replay reads current retained state without network requests or evidence resurrection. Neither collection nor local dismissal starts execution or writes to GitHub.

The collection request's closed kind enum separates published feedback, required CI and merge conflict. An omitted/unspecified kind retains the original feedback behavior for existing callers; unknown numeric enum values fail before remote access. Each explicit kind collects independently and cannot invalidate another kind's current inventory or borrow its prerequisite authority. The normalized collection family is part of the exact mutation identity; unspecified and explicit feedback preserve the original v18 receipt family. CI problems reference immutable shared complete original observations; conflict versions retain a confirmed transition across unknown readings. Shared history and exact version-local dismissal cover all three kinds.


`WorkerService.ReportWork` may carry the exclusive version-1 `pr-startup-rejected` output for an original PR Worktree startup rejection. It binds the immutable assignment, original Worker/server/device/instance and metadata-only workspace phase proof; its uint64 assignment revision is a canonical decimal JSON string. The server accepts it only before any native grant/publication and retains the original output on a failed/canceled job with a typed problem. Session resources add optional `startup_rejection`; the original queue resource uses `rejected-before-start`. These additive JSON shapes require no protobuf/generated or SQLite migration. This output is neither native completion nor authority to Resume, retry Git or send the input again.


`RecoverSessionExecution` retains its existing request/response and explicit original execution ID for pre-native PR recovery. Its internal RecoverExecution job has an exclusive optional `startup` comparison containing original execution/input IDs; common fields bind original server/device/instance/machine, assignment and configuration plus preparation/manifest. Native completion/history/input-mode/prompt/accepted-input fields must be empty for this profile. The result is version-1 original `job_id`/`report_id` plus an assignment-bound `rejection`, never mixed native completion. New claimed jobs retain optional `assigned_device_id`; it is immutable after first claim and checked before result receipt replay. No protobuf or SQLite migration is needed, and missing historical device evidence cannot be synthesized.


IntegrationService adds owner/client-only ListPullRequestRemediationAttempts and ResumePullRequestRemediation. List takes exact stable remote repository/PR numeric strings, page size and opaque cursor; returns the retained problem set, original attempt resources and next cursor. Resume takes a Mutation targeting the original set revision and returns the current set plus request ID/replayed flag. These operations never accept remote evidence or execution commands; follow the integration contract for history fingerprint bounds and explicit baseline-only resumption. Regenerate both Go and TypeScript/Connect Query sources from the canonical schema.

### Managed database backup observation

SystemService exposes owner/client backup creation, metadata pagination and explicit integrity inspection through generated Connect queries and the CLI. Settings > Backups retains exact creation retries and displays precise byte counts. Listing is not integrity or restoration evidence; failed reinspection clears prior success. Follow the [storage contract](cmds-delidev-storage-contract.md) for bounds, identity checks, pagination and the separate permanent-session deletion and remaining restoration work. `DeleteBackup` and `ListBackupDeletions` expose durable irreversible image deletion, original inspected revision/metadata/hash, explicit confirmation, retained exact retries and restart-visible pending/completed jobs. Logical image bytes and unknown interrupted unlink counts never imply physical free-space recovery.


### Durable backup creation

`RequestBackup`, `GetBackupCreation` and `ListBackupCreations` expose original durable jobs through Connect and generated queries. Current CLI and Settings use that path; the synchronous `CreateBackup` remains compatible. Keep pending acceptance separate from image publication, exact retries across navigation, typed failure/stale observations and integer precision. Jobs resume after server restart without client resubmission, and completed history does not assert current image availability. See the [storage contract](cmds-delidev-storage-contract.md).

`SystemService.GetBackupDeletion` is an owner/paired-client read of one original
durable deletion job ID, independent of history pagination. It rechecks current
authority, rejects other job types and preserves exact uint64 revisions and byte
counts without replaying acceptance or filesystem work. Regenerate Go, TypeScript
and Connect Query bindings together.

Provider activation adds an owner/client-only `ProviderService.ListProviderInventory`, closed capability/preset enums, account-list-only `ListResourcesRequest.provider_id`, and additive `SearchModels.enabled_providers_only`. Omitted model filtering preserves existing behavior. Provider enabled and preset provenance remain optional fields in the canonical provider JSON document for legacy compatibility. Inventory capabilities let clients detect older servers that ignore filters; Workers cannot read it. See [provider activation](cmds-delidev-provider-activation-contract.md), and regenerate Go messages/Connect handlers and TypeScript Connect Query bindings together.

## Workspace storage service

`WorkspaceStorageService` is owner/paired-client-only and provides
`RequestWorkspaceStorage`, `GetWorkspaceStorageOperation` and
`CancelWorkspaceStorageOperation`. The closed additive action enum selects
preview/create/cleanup/inspect/restore/delete/recover. Acceptance binds Mutation
to the current session; cancellation binds it to the original current job.
Successful preview, exact snapshot and original recovery job references have
separate fields. Responses expose current original Resource jobs with request
UUID/replay status; polling never resends native side effects. GetStatus adds
`WORKSPACE_STORAGE_V1` at its main-reserved enum value 11; the existing `SESSION_FORWARDING_V1`
retains value 2 and `USER_SERVICES_V1` retains value 3. Capability values are
distinct and never alias or reinterpret main's forwarding or user-service
capabilities. Snapshot metadata stays in the existing Snapshot kind
and generated Go/TypeScript/Connect Query bindings reproduce from the schema.
The [storage contract](cmds-delidev-storage-contract.md) owns exact authorization,
state publication, safe copying, cancellation and explicit recovery semantics.

### Managed database restore

`SystemService.RestoreBackup` and `GetBackupRestore` are owner/paired-client-only
operations. `MANAGED_BACKUP_RESTORE_V1` (wire value 7) advertises availability,
preserving published `SESSION_FORWARDING_V1` value 2 and `USER_SERVICES_V1`
value 3; `InspectBackup`
adds an exact uint64 `restore_revision`. Restoration requires a UUID-v7 request,
original backup revision/metadata/digest, a present exact expected live revision
and explicit confirmation. Generated Go and TypeScript/Connect Query descriptors
must reproduce together. `prepared`, `published`, `restored` and `rolled-back`
remain distinct; publication ends the old server epoch, while startup reconciles
the external journal before opening SQLite. Keep request UUIDs and decimal
revisions exact across uncertain responses; polling never repeats replacement.
Follow the [storage contract](cmds-delidev-storage-contract.md) for authorization,
deletion enforcement, historical quarantine, bounds and recovery evidence limits.

### Current-user services

`SystemService.GetUserService` and `ControlUserService` add owner/paired-client-only, closed typed kind/action/state messages and `USER_SERVICES_V1`. Preserve exact uint64 revisions, UUID-v7 request/installation identity, actor-bound current-state receipts and correlation. The target is the server computer's own server or fixed local Worker scope; no caller path, remote Worker, native PID or credential field exists. Stop acceptance and joined controller cleanup are independent. See the [user-service contract](cmds-delidev-user-services-contract.md).

System capability wire values retain `AUTOMATIC_TITLES_V1 = 1`, `SESSION_FORWARDING_V1 = 2`, `USER_SERVICES_V1 = 3`, `NATIVE_ACCOUNTING_V1 = 4`, `STOPPED_CODEX_ACCOUNT_SWITCH_V1 = 5`, `SERVER_OUTBOUND_PROXY_V1 = 6`, `MANAGED_BACKUP_RESTORE_V1 = 7`, `PERMANENT_SESSION_DELETION_V1 = 9`, `WORKSPACE_STORAGE_V1 = 11` and `CODEX_SESSION_FORK_V1 = 13`. Managed restore, native accounting, workspace storage and same-account Codex Fork use their original reserved numbers. `GetStatus` advertises each implemented capability independently. Preserve these distinct meanings and regenerate both language bindings from the schema.

## Authenticated development-server forwarding

Issue #1089 follows the [session forwarding contract](cmds-delidev-forwarding-contract.md). Additive `ForwardService` start/get/stop, one-shot claim, streaming traffic and original cleanup RPCs plus `WorkerService.WatchForwardRequests` preserve authenticated client/session/Worker ownership and typed `SESSION_FORWARDING_V1` capabilities. Generated Go/TypeScript descriptors and `ForwardQuery` expose the shared API. The CLI owns an explicit loopback listener and returns its exact endpoint. Stop preserves forwards; Archive/deletion/revocation close them, and every Archive completion requires independently confirmed original cleanup. Receipt replay and reconnect cannot recreate a claimed native lifetime. Model API endpoints remain server-relative. Generic schema-24 entities/receipts retain metadata without traffic or a relational migration.


### Permanent session deletion

`SessionService.DeleteSession` uses the existing UUID-v7/revision `Mutation` and
returns an original typed `SessionDeletionJob`, request ID and replay flag.
`GetSessionDeletion` reads by original session ID after content removal. Owner and
paired-client authority applies; Worker credentials cannot invoke these APIs.
`SystemCapability.PERMANENT_SESSION_DELETION_V1` advertises the additive surface.
Pending/succeeded enum state, exact uint64 revision, accepted/finished timestamps,
Worker count and database/backup acknowledgements keep acceptance separate from
confirmed removal; unknown reclaimed bytes must remain unknown.

`WorkerService.ListSessionDeletionWork` and `ReportSessionDeletion` form an
independent current-instance-authenticated cleanup lane, bound to original paired
machine/device and immutable versioned ownership metadata. Work pages contain at
most 20 plans and 1 MiB; UUID continuation is observation only. Reports contain
only original session/deletion IDs, plan digest and retained request UUID. No
paths, prompts, credentials or new execution authority cross this boundary.
Generate Go and TypeScript/Connect Query sources together and follow the
[storage contract](cmds-delidev-storage-contract.md).

`PullRequestFixService` owns typed Codex Git capability and authenticated owner/client manual fix acceptance. Version-1 selection JSON carries explicit project/repository and exact decimal set/problem revisions/content versions; responses return current original attempt/session/set resources and receipt identity. New independent messages/service preserve existing wire allocations and forwarding exports. Workers cannot invoke these business mutations; native Git authority/proof follows the integration contract.

### Grok original tool and response Resource JSON

Native request observations additionally retain `proposal_json`, the exact UTF-8
native JSON parameter bytes as a string alongside their typed payload. The pure
reducer requires the same complete payload, validates the original native request
and computes its byte-based proposal SHA-256 before interaction publication.
Notifications cannot carry this request-only evidence. Both representations fit
the existing 512 KiB public event bound; native request bytes also consume the
existing reducer aggregate bound. Missing historical bytes remain readable
evidence but cannot authorize a new request or reply through re-serialization.

Issue #1091 adds typed `grok-tool-observed`/`grok_tool`, exclusive `Interaction.grok`, question/approval response `input.grok`, native current-mode/order/response aggregate progress and the exclusive `grok_tools_terminal` Resource JSON variants. Original method/request-kind/arrival and exact decimal uint64 counters survive without float conversion. Grok numeric request IDs add `{kind: "decimal", decimal: "<original integer spelling>"}` within Resource JSON, preserving the native 19-digit optional-minus grammar and `-0` without narrowing; historical text/signed-number identities remain unchanged. Each interaction references its immutable earlier observation and original native proposal; Plan additionally binds original entry/Write/content digest/revision. Follow the harness contract for complete independent validation, bounds and evidence limits.

Existing `RespondQuestion`, `RespondApproval`, owning-Worker response claims, metadata-only controls, delivery publications and exact receipt replay carry these variants. Accepted original tool results create the server-derived `native-grok-tool-result` evidence; a Worker cannot assert it through generic acceptance publication. Native Plan uses the original approval request and adds no common Plan gate. No protobuf service, generated binding, relational schema migration or Worker permission expansion is required. CLI `interaction respond` and `interaction approve` accept the same exclusive `{"grok": ...}` document through file/stdin after reading and validating the original interaction; credential stdin remains separate. Historical missing variants preserve existing bytes and do not gain this profile's authority.

Original Grok interaction request IDs must match the complete retained value,
including kind and decimal spelling. Historical numeric and decimal variants
may share a duplicate-detection namespace key; that key cannot substitute one
representation for another or grant a reply to a changed request.

### Explicit stopped Codex API account selection

`SessionService.SwitchSessionAccount` is an additive owner/client-only mutation, capability-gated by `STOPPED_CODEX_ACCOUNT_SWITCH_V1` (wire value 5, allocated on main) in System status. Its `Mutation` binds the exact session revision/request and `account_id` selects an original candidate; actor-bound reference receipts return current `SessionChange`. Admission, historical attribution, full native history and explicit Resume follow the [sessions contract](cmds-delidev-sessions-contract.md). It performs no native side effect or automatic execution. Go, TypeScript and Connect Query bindings are generated from the service-owned schema; existing capabilities retain their wire values.

### Server outbound network configuration

`network.proto` owns NetworkService SaveNetworkProfile, DeleteNetworkProfile, SelectNetworkProfile, GetNetworkRoute and ExportWorkerNetworkMetadata. The pre-reserved `SERVER_OUTBOUND_PROXY_V1 = 6`, `NETWORK_PROFILE = 28` and `NETWORK_ROUTE = 29` values preserve every existing main assignment. Definition JSON and write-only credential bytes are separate. Actor-bound UUID-v7 receipts and exact revisions apply; Worker selections have independent desired generations. Export returns signed non-secret metadata only, never credential bytes or native application evidence. Follow [the network contract](cmds-delidev-network-contract.md).

## Repository inspection metadata allocation prerequisite

Closed, unmerged PR #1193 implemented repository metadata using Worker value 5 and attachment-response field 3. Main now reserves Worker values 3, 4 and 5 for other owners, including native compaction from issue #1203 at value 5. Issue #1142's replacement therefore reserves `WorkerCapability.REPOSITORY_INSPECTION_METADATA_V1 = 6` and `AttachWorkerResponse.supported_worker_capabilities = 3`, retaining original-PR provenance. The original issue's proposed value 3 and the old branch's value 5 cannot replace existing main reservations.

Each reservation uses the kind of its existing declaration (`enum` or `message`); a message-field addition is recorded as `message`, not a separate field declaration kind. The allocation check validates this against the baseline declaration.

PR #1214 established these allocations on main before the dependent implementation. Repository registration now activates only Worker value 6 and response field 3 under the compatibility contract above; the other reserved values retain their owners. No database migration is required.

Issue #1100 adds explicit `UsageAccountingProfile.NATIVE_UNITS_V1` negotiation, advertised by `SystemCapability.NATIVE_ACCOUNTING_V1` and echoed by GetUsageSummary. Its additive UsageTotals.accounting entries have distinct AccountingUnitKind, exact decimal supplied totals and measured/unavailable unit counts. Legacy fields remain response-only. GrokClosedInput cost enums remain unavailable; Codex estimated costs remain in the existing estimate graph. Native source references stay private. Unknown profiles fail; owner/client authorization and both encoded byte bounds remain unchanged. Follow the [usage contract](cmds-delidev-usage-contract.md).

## Managed Codex subscriptions

The additive `subscription.proto` defines owner/client RequestSubscription, CancelSubscription and GetSubscriptionProgress plus separately authorized Worker WatchSubscription, TakeSubscription, PublishSubscriptionProgress and FinishSubscription. Secret bytes exist only in bounded protected Take/Finish fields, never ordinary resources/jobs/events or receipt payloads. The closed action enum, managed-Codex Worker capability and original lease revision/generation fences follow [the subscription contract](cmds-delidev-subscription-contract.md). Subscription publication registrations return no API proxy path or API relay authority. Lifecycle Take preserves its original positive account observation across a definite busy retry, rejects future observations and revalidates the original queued operation and initiating authority; owner requests retain current account revisions and execution Take retains the exact job revision. No schema or generated declarations change. Historical account/snapshot JSON omits the optional subscription extension when absent.
## Browser API

[Protected browser ownership and cleanup](cmds-delidev-browser-contract.md) use the service-owned `BrowserService`, typed profile/state/capability models and generated `BrowserQuery` descriptors. Existing shared enum/field numbers and future migration reservations remain unchanged. Browser messages carry ownership metadata only, never native paths or browsing content.

## Codex session forks (#1092)

`SessionService.ForkSession` accepts owner/paired-client mutation identity, exact
source revision/native turn, child name and closed `ForkWorkspace`. The dedicated
write-only Local proof is never ordinary receipt material. `GetSessionFork` reads
the original job and optional published child; an accepted job is not a child.
`SystemCapability.CODEX_SESSION_FORK_V1` is additive. Ordinary Worker credentials
remain excluded from these product methods and complete their original job via
existing authenticated `WatchWork`/`ReportWork`. See the
[fork contract](cmds-delidev-forks-contract.md). No destructive migration occurs.

Native input accounting activates original capability 8, coverage 2, summary field
13 and the reserved native budget coverage fields. AccountingUnitKind 3 and 4 are
Claude main-loop input and OpenCode step, respectively. Negotiated native-units-v1
returns their independent repeated summaries, retaining all existing response and
Grok meanings. See the usage contract for source-specific counts and pricing.

## Independent subscription service identity

Activate the main-reserved `SUBSCRIPTION_SERVICE_ACCOUNTS_V1 = 17` only with real storage migration 28 after 26/27 and complete service-native configuration authority. The closed `SubscriptionServiceIdentity` enum preserves independent ChatGPT/Claude/Grok values; it is separate from the existing `SubscriptionService` RPC declaration. Reserved additive usage selection/group/model/pricing and request diagnostic fields retain the service without a fabricated provider. API-only documents remain schema 1; subscription Account/native Model and retired metadata projections use schema 2. Older clients receive explicit schema/support guidance. Bindings regenerate from the reconciled service-specific schemas.

Issue #1208 activates the previously reserved `SystemCapability.OPENCODE_FOREGROUND_SUBAGENTS_V1 = 23` and `WorkerCapability.OPENCODE_FOREGROUND_SUBAGENTS_V1 = 12`. The existing ordered publication and session-owned resource/CLI/client boundary carries closed original task/child proof. Generated bindings come from the reconciled schema. The capability grants no child input/control, continuation or cleanup shortcut.

## OpenCode native context profile

The integrated #1203 implementation activates independent System capability 25,
Worker capability 14 and SessionContext capabilities 5/6 using their original
allocation-ledger reservations. Common compaction support does not imply an
OpenCode installation or send authority. Existing CompactSession/GetSessionContext
RPCs, exact mutation revisions and durable receipt identities carry closed
version-3 OpenCode action/result JSON. Legacy Claude and Codex versions remain
unchanged. Generated Go and TypeScript bindings come from the reconciled schema.
The compaction contract owns original native/source/claim/report/cleanup proof.

## OpenCode General Chat fork reservation (#1210)

System capability 26 and Worker capability 15 independently activate the
Unix plain-text General Chat profile through existing authenticated `ForkSession`,
CLI and desktop surfaces. Codex capability 13 cannot imply OpenCode support.
Every operation requires full original source, relocation, history, workspace
and cleanup proof under the fork contract. Copied canonical messages carry
explicit original provenance without input/accounting authority; absent native
agent/model remains private until the first real input independently proves
selection. No migration is allocated.

Worker-only permanent-deletion ownership JSON retains its closed version-1 shape
with an optional original unpublished Sidechat child ID on Fork copies. Keep the
4,096-copy bound, strict 4 MiB envelope/page limit and 20-envelope pagination bound;
native clients allow 8 MiB response transport overhead for JSON/base64. Ordinary
public command JSON retains its 1 MiB bound. These metadata IDs grant no native
replay or filesystem authority.

## Independent server subscription login

Main-established capability 30 and PR #1332's reserved declarations activate the independent server lifecycle. Omitted `RequestSubscription.machine_id` selects this capability; explicit machine requests retain original Worker authority. `GetSubscriptionProgressResponse` adds typed state 4, transient suggested_name 5 and original successful generation 6. The closed SubscriptionLoginState values 0–8 separate all native outcomes; unknown values grant no UI transition. `ForwardSubscriptionCallback` carries account_id 1, operation_id 2 and write-only bounded callback_query bytes 3, returning accepted 1. Only the authenticated original owner/client may claim one original state-bound native callback; no Worker, receipt replay or uncertain delivery grants another send. Callback bytes, authorization URLs and unsaved suggestions never enter persisted records or logs. Follow the subscription contract; regenerate Go, TypeScript and Connect Query outputs together.

### Codex diagnostic activation

The existing schema-v2 account JSON may retain optional server-owned `server_operation.cleanup_phase` with closed `native-confirmed` or `credentials-confirmed` values for failed initial server login cleanup. Omitted legacy metadata remains unchanged. This checkpoint grants no authentication, callback or configuration-deletion authority; the subscription contract defines its original-owner and cleanup requirements. No protobuf field, capability, generated declaration or database migration is added.

PR #1336 established the diagnostic allocations on main at `82d8859e98485458ccf8708225c0c6694d9cfab5`. The optional `GetSubscriptionProgressResponse.diagnostic` field 7 now uses those exact declarations: detected version 1, minimum version 2, closed phase 3, stable code 4, safe message 5, guidance 6 and correlation ID 7. An empty detected version means no verified version; absent diagnostic means the server did not report metadata. Native failure attribution never grants account, callback or retry authority. Preserve original actor/operation ownership and terminal read authorization; older clients can ignore the additive field. Regenerate Go and TypeScript bindings from the reconciled schema. No migration or capability number is added.

## General API OAuth extension

Main-established issue #964 allocations add inventory capability 6, device
connection method 4, closed flow PKCE/DEVICE, Google project options and original
completion state. Preserve capability 5 and historical OpenRouter receipt input.
Declare the reserved additive fields before generation; reservations alone grant
no provider support. The common/Hugging Face implementation returns PKCE flow
only on a live Start, advertises capability 6 only for accepted exact profiles,
and keeps authorization URL/code/state outside cached query variables. Device
user codes remain Start-only; later provider implementations retain their gates.
- For server-owned Baseten Device approval, Start returns flow DEVICE and the
  temporary user code only with the original live Start reply. Status is a read.
  After the sole Go completion claim, its existing response request_id identifies
  that original completion receipt; code-free local recovery retains the original
  expected attempt revision 1. Cancellation keeps its own mutation receipt.
  Public Complete cannot supply a Device callback or initiate polling. These
  semantics reuse the main-reserved fields and grant no unregistered capability.
## Pre-release protocol reset reservation

The [pre-release reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset)
reserves protocol 2 and `AttachWorkerRequest.protocol_version = 10` on main.
The field is absent from active schemas until complete implementation. The
reset removes historical forwarding imports/reflection and obsolete API
surfaces; retained field and enum numbers preserve their original meanings.

## Inline Worker models and endpoint-only completion reservation

Reserve System 42, Worker 22, ModelIdentity, EndpointModel, ListEndpointModels, token-pricing messages and additive usage identity fields on main. Protocol 2 retires independent Model APIs/fields without reusing their numbers. Regenerate reconciled Go/TypeScript/Connect Query outputs. Reservations alone advertise no support.

Follow the complete [catalog amendment](cmds-delidev-catalog-contract.md#inline-worker-models-and-endpoint-only-completion-reservation) and [current-only reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset). This reservation changes no runtime support or native/account acceptance.
