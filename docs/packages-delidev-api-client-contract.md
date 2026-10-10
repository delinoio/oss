# DeliDev TypeScript client

## Execution startup bindings

The [direct startup contract](cmds-delidev-execution-startup-contract.md) activates the main-reserved System 43, Worker 23 and closed ReportExecutionStartup declarations. Generate Go, TypeScript and Connect Query outputs from the reconciled service schemas. The original Worker reports exact claimed revision/instance metadata; desktop presentation reads existing authenticated session resources. Additive observations do not grant client Worker authority, inference or credential access.

Generated AccountQuery and AccountService expose StartAccountOAuth, CompleteAccountOAuth, CancelAccountOAuth and GetAccountOAuthStatus, with exact bigint revisions and closed OAuth state/connection-method enums. Authorization URL exists only in original live Start; status carries metadata only. Completion code is a write-only bounded byte array: use a direct authenticated RPC without query/mutation-cache retention, clear transient buffers, and recover only the original completion identity without code. No client-side retry may repeat an exchange. Preserve all four existing account-flow gates independently of capability 5 under the [OAuth contract](cmds-delidev-account-oauth-contract.md).

## Request diagnostic client

Generated `SessionQuery.listRequestDiagnostics` and `SystemCapability.REQUEST_DIAGNOSTICS_V1` expose the previous feature metadata read. Preserve native-input versus proxy-HTTP enum provenance, optional unavailable observations, exact bigint revisions/latency and original session/execution/page selection. The client performs no matching by time/model, usage ingestion, request reconstruction or receipt-driven HTTP retry. Follow the [diagnostics contract](cmds-delidev-diagnostics-contract.md); bindings remain tool-generated.


Buf generates service-specific modules. The normal protocol generation command
also runs `scripts/delidev/proto-compat.mjs` to remove retired historical aggregate
module, Connect Query and Go descriptor facades. Protocol-2 consumers import the
canonical split service modules or package-root exports. Earlier facade paths
are unsupported; original field and capability allocations keep their meanings.

## Scope
`packages/delidev-api-client` owns private `@delinoio/delidev-api-client`, generated messages and service-specific Connect Query namespaces, explicit transport, typed errors, UUID-v7 request identities and bounded resource synchronization. This is the client integration boundary for desktop implementation; it does not itself constitute a desktop app or complete the feature.

## Runtime and Language
TypeScript ES2022 modules run in the desktop renderer. Development uses the repository Node/pnpm versions and TypeScript compiler. Business rules and durable authority remain in Go. There is no browser product deployment or Rust product proxy.

## Users and Operators
The DeliDev desktop client connects to one explicitly selected authenticated local/remote server. Maintainers generate bindings from the canonical protobuf and validate them against the actual Go server.

## Interfaces and Contracts
Generated `EntityKind.SUBAGENT` and `SystemCapability.SUBAGENT_OBSERVATION_V1` support [session child-observation reads](cmds-delidev-subagents-contract.md) through existing Resource/System Connect Query descriptors. Preserve exact native IDs, independent requested/observed models, partial output and nullable string counters. Native usage reports are exact JSON strings. No client mutation or native control is derived from observations.


`NativeModelQuery` exports generated discovery/status/list/cancel descriptors
under the [native model contract](cmds-delidev-native-models-contract.md). Keep
exact bigint revisions, UUID-v7 mutation identities and immutable job/page
selection. Private executable selection and full observations never enter generic
public Resource documents; observation pages remain advisory. Client parsing and
registration preparation grant no authorization or implicit model save.

Generated `ListResourcesRequest` exposes optional account-only provider and account-type selectors. Keep these fields in the list request and its query key/cursor input; do not move them into the shared `Filter` used by snapshots or event streams. The Go server performs filtering before pagination and binds both fields into continuation cursors. Provider inventory reports `ACCOUNT_TYPE_FILTER` separately from its activation/model/provider-filter capabilities; split account views require all of them.
- `buf.gen.yaml` generates messages and Connect Query descriptors exclusively from `delidev.v1`. Root generation/freshness and Turbo input/output tracking include the package. Existing Go/DevHud/ach output remains reproducible.
- `ActivityQuery` exposes typed PR handling metadata and the `PR_HANDLING_V1` capability. Preserve original numeric identity strings, bigint revisions, content-version references and separate dismissal/attempt/verified outcomes. Explicit ResourceQuery source inspection reads original retained resources without mutation or handling inference; see the [activity contract](cmds-delidev-activity-contract.md).
- `UsageQuery` exports generated summary/current-price/historical-price descriptors and the explicit price mutation. Preserve nullable decimal rates, exact amount/counter strings, separate currencies and token-basis versus native coverage. The additive summary request supports explicit day granularity and IANA zone; optional daily/model analytics are server-owned results from the same snapshot as existing totals. Only the server computes historical estimates and Other-model totals; no client-side aggregation or inferred actual cost.
- Automatic session creation uses generated `SessionQuery.createSession` with explicit `name_mode: "automatic"` and no `name`. `SystemQuery.getStatus` exposes the typed automatic-title server capability; UI gating must use that field rather than a version guess. Session title owner/state/reason remain server-owned `Resource.document_json` fields.
- `createDeliDevTransport` takes an explicit server origin and caller-owned fresh token supplier. It uses binary Connect POST, server streaming and no automatic mutation retries. `newRequestId` creates UUID-v7 mutation identities; callers retain the complete original request on uncertain retries.
- `clientFailure` preserves versioned Go problem classifications, guidance and valid correlation IDs; untyped browser/proxy errors never disclose raw exception contents.
- `synchronizeResources` is a read-only async iterator over one resource-kind/session/project scope. It atomically replaces the consumer's scope with a coherent complete server snapshot, then emits indexed current resources or removals. It never substitutes partial lists for snapshots. The existing server snapshot limit is 200 resources and its byte budget; larger histories require a separately paginated presentation and cannot be claimed complete by this helper.
- Consumers apply each update before requesting another. Only afterward does the helper commit its opaque event cursor. Duplicate events, older revisions and unrelated kinds do not reload complete history. A failed indexed read keeps the preceding cursor; NotFound after an update removes the now-deleted resource. Project reassignment removes resources outside the scope.
- Typed expired/gap cursors trigger a fresh coherent snapshot. Transient failures preserve displayed state and reconnect with capped exponential backoff and jitter; authentication/revocation, compatibility, invalid evidence and capacity errors stop with an explicit problem. Disconnection cannot imply completion or dispatch elsewhere. Signals cancel reads, streaming and retry waits, suppressing late publication.
- The helper bounds live resource metadata/document accounting, defaults to 1,000 resources and 4 MiB, permits at most 10,000 resources/16 MiB, and retains at most 512 event IDs. It holds no growing transcript copy or retry queue. Native notification deduplication, paginated large-history presentation and desktop lifecycle remain separate required work.

Additional explicit `watchKinds` may share the primary kind snapshot cursor, loading only changed identities of those kinds. Their initial state must be paginated separately; they are never invented as members of the primary snapshot. All watched kinds share the memory/revision bounds.

`TerminalQuery` exports generated creation/control/output descriptors under the
[terminal contract](cmds-delidev-terminals-contract.md). Terminal observations use
the existing resource filter, while raw output is a cancellable streaming read.
Consumers retain bigint sequence precision, incremental decoding state and an
explicit epoch/gap boundary; mutation retries retain the exact original request.
Canceling a read cannot close or recreate the native terminal.

### packages/delidev-api-client constraints

- Project first-prompt history follows the sessions/storage/desktop/protocol/client contracts for the feature. Preserve immutable project-owned text with empty session IDs, atomic 100-entry acceptance order, actor-bound confirmed clear receipts, scoped byte-bounded reads and text-only boundary recall. System 48 / EntityKind 35 add no Worker capability or migration. Session deletion preserves history; project deletion removes it, managed backups capture it and portable exports exclude it. Never log prompt text.

- OAuth format selection activates only the main-established the originating change ProviderInventory capability 8 and Start/attempt fields 4/7. Follow `cmds-delidev-account-oauth-contract.md#oauth-api-format-selection-reservations`: bind the original explicit Bearer profile without changing the authentication adapter, retain omitted legacy receipt bytes and original recovery/cleanup authority, and keep unaccepted provider registrations disabled. Common manual/OAuth UI explicitly selects multiple formats or displays a sole profile; no migration or native change.

- Key-preserving API format changes follow `cmds-delidev-accounts-contract.md#connected-api-format-changes` after main reservation the originating change. Capability 9 owns the atomic format/preferences RPC; preserve capability 7 and OAuth reservation 8, bounded server-owned generations sharing the original protected key, immutable original execution/continuation profiles, explicit current validation, all-generation cleanup and portable exclusions. Observation-only edits rebase protected fields/revision while retaining drafts; editable conflicts block saves. No migration, native change or format conversion.

- Per-key API formats follow the catalog/protocol contracts and the originating change allocations. Negotiate ProviderInventory capability 7, retain closed API format/authentication enums and schema-3 API family checks independently of ordered-source Agents, and preserve original request bytes. Portable v4 accepts existing v1–3 semantics; no Worker wire change or client-side format conversion is authorized.

- Failed subscription cleanup follows `cmds-delidev-subscription-contract.md#failed-subscription-cleanup-reservations` after main reservation the originating change (System 41). Preserve server-owned complete batches, original actor/login/revision, shared vault/reference deletion checks, atomic result/tombstone receipts, joined shutdown and restore quarantine. Terminal failures require a new explicit batch; status/restart never relaunches login or callbacks. No database migration or Rust/native change.

- Agent Worker wizard saving follows the catalog/protocol contracts: derive one current account source, require at least one account, preserve ordered routing, resolve exact model identity and write model plus Worker in one receipt transaction. Bind source-scoped list cursors before pagination. Preserve canonical metadata/history and legacy accountless APIs; capability 33 is configuration support, never native readiness.

- Historical import, generated-path and aggregate-reflection preservation rules below apply only before complete protocol-2/baseline-32 reset activation under `cmds-delidev-structure-contract.md#pre-release-compatibility-reset`. Reservations alone retain all pre-reset behavior and grant no reset support. At complete activation, use canonical generated service modules and package-root exports and retire only the historical import/reflection facades. The current reset is complete. Preserve numeric allocation ownership, current adapters, capabilities, authentication/refusals and tool-generated outputs; a legacy name alone does not identify removable compatibility code.

- Repository listing/Clone use generated IntegrationQuery/WorkerQuery under the protocol/client contracts, with independent System 31/32 / Worker 18 negotiation. Preserve explicit profile revision/generation/page, transient fresh local proof and identical uncertain request bytes. No PAT read model, client-side Git, persistent draft or frontend registration follow-up is permitted.

- Buf generates service-specific files. Before complete reset activation, `scripts/delidev/proto-compat.mjs` generates historical TypeScript import facades; keep both package-root exports and legacy `./gen/*` paths working and regenerate facades through `pnpm proto:generate`, never by hand. After complete activation, that tool retires historical facades; retain canonical generated service paths and package-root exports under the reset contract.

- Generated ActivityQuery retains the typed PR metadata and PR_HANDLING_V1 capability under the activity contract. Keep exact source revisions and version references; source inspection is an explicit disposable read, and attempt success cannot become verified handling.

- DeliDev generated user-service queries retain exact bigint revisions and original requests; capability support is separate from native manager availability and cleanup. Keep private native identities and credentials off the service wire, and follow `cmds-delidev-user-services-contract.md`.

- Backup job observation uses owner/client `GetBackupCreation` and `GetBackupDeletion` independently of bounded history pages. Keep accepted IDs and exact revisions through navigation, refresh inventory after observed completion, and never replay a mutation to poll status. Follow `cmds-delidev-storage-contract.md`.

- Managed backup deletion uses existing durable jobs/receipts and schema-24 indexing, plus immutable synchronized `backup-deletions/` intents outside SQLite before unlink. Preserve exact inspected revision/metadata/hash, original actor/request identity, startup obligation reconstruction, creation-replay suppression, bounded pending retries and source preservation on mismatch. Never evict deletion obligations or equate logical image bytes with reclaimed disk space; follow `cmds-delidev-storage-contract.md`.

- DeliDev portable configuration uses generated ConfigurationQuery export/preview/apply bindings. Keep original document bytes and exact request identities under `cmds-delidev-configuration-transfer-contract.md`; client parsing never becomes authorization, validation or numeric reserialization authority.

- DeliDev workspace file queries use generated `SessionQuery.readSessionWorkspace`; no client filesystem or duplicated authorization logic. Keep file contents in bounded nonpersistent view caches, preserve exact decimal size strings, and render them as inert text under `cmds-delidev-files-contract.md`.

- `packages/delidev-api-client` owns the private generated TypeScript/Connect Query bindings and bounded read-only synchronization helpers. Follow `packages-delidev-api-client-contract.md`. Generate all descriptors from `delidev.v1`; never duplicate Go product validation or add implicit startup/mutation retry.

- Generated DeliDev UsageQuery exposes the additive explicit granularity/timezone fields and optional same-snapshot daily/model analytics. Preserve exact decimal counter strings and server-supplied Other groups; the client must not recreate aggregates or rank results locally. Follow `packages-delidev-api-client-contract.md`.

- DeliDev `SessionQuery.readSessionReviewContext` carries authoritative Worker diff coordinates with original side/range/text/newline facts. Clients cannot manufacture line anchors for binary, mode-only, symbolic-link or submodule changes. Keep observations bounded and nonpersistent under `cmds-delidev-files-contract.md`.

- DeliDev generated SessionQuery local review mutations preserve exact decimal revisions, original anchor/context and immutable submitted snapshots. Retain exact uncertain wire requests across panel navigation; current Resource refresh cannot silently update a selected revision. Use the dedicated owner/client operations under `cmds-delidev-files-contract.md`.

- DeliDev generated IntegrationQuery follows `cmds-delidev-integrations-contract.md`; write-only PATs never enter query keys/read models and pending revision strings remain exact. Generate all service descriptors from the canonical proto.

- DeliDev IntegrationQuery includes the generated repository access read. Consumers preserve its exact scope and decimal revision and independently validate observations; endpoint availability is not future authorization or semantic CI/rules evidence.

- DeliDev IntegrationQuery includes generated repository content queries. Keep strict query/scope validation, exact decimal IDs with original API identity source, explicit search/page limits and nullable mergeability; no query cache or content response may carry saved PATs.

- DeliDev retained PR feedback uses generated IntegrationQuery refresh/list/dismissal bindings. Preserve exact remote numeric strings, resource revisions and original content-version/request identities. No implicit collection, mutation retry, handling inference or browser persistence is allowed; follow the integration contract.

- DeliDev IntegrationQuery exposes generated retained remediation-history and explicit allowance-resumption bindings. Preserve exact numeric PR IDs and original set/revision/request identity without persistence, implicit mutation retry or execution inference; follow the integration/client contracts.

- DeliDev provider and model settings follow `cmds-delidev-provider-activation-contract.md`: capability-gate against ProviderInventory, derive exact counts from the server, paginate custom providers, filter model reads/selections server-side to enabled API providers, and preserve explicit Off references. Do not add a client-side activation/eligibility source.

- DeliDev session forwards follow `cmds-delidev-forwarding-contract.md`: preserve explicit loopback port selection, original client/Worker/device/instance ownership, negotiated capabilities and bounded ordered opaque traffic. Native claims precede sockets and receipt replay grants no new lifetime. Keep Stop independent from Archive, gate every Archive completion on both original cleanup outcomes, and retain positive private cleanup receipts through offline reporting without redialing or recreating listeners. Worker credentials receive only their original forwarding peer endpoints.

- Before complete reset activation, preserve legacy generated-path reflection exports as well as declaration imports. Generate the aggregate descriptor view in the compatibility pass, retain original declaration order and canonical TypeScript object identity, and cover both direct enumeration and registry construction in compatibility tests. Complete activation retires those historical aggregate views under the reset contract; current service declarations retain their original identity and numeric ownership.

- Generated SessionQuery permanent-deletion acceptance/status and Worker cleanup queries preserve original UUIDs, BigInt revisions, pending removal and unknown reclaimed bytes. Generate through the canonical split schema and compatibility pass; no client-side ownership decisions or automatic mutation replay. Follow `cmds-delidev-storage-contract.md`.

- WorkspaceStorageQuery exposes generated owner/client storage request, inspection and cancellation operations. Preserve exact session/job revisions, original receipts and separate observed native outcomes without implicit mutation retry or cleanup inference.

- TerminalQuery and WorkerQuery expose the generated session terminal operations. Preserve exact bigint cursors/revisions, original bytes, explicit output gaps and request identities without persistence or native side-effect retries; follow the terminal contract.

- Export generated PullRequestFixQuery and its typed profile from the additive PR-fix service. Preserve original request bytes/revisions and facade declaration identity; native Git authority remains Worker-local.

- Native subagent observations follow `cmds-delidev-subagents-contract.md`. Validate original bounded ownership and complete batches before atomic publication; preserve exact receipts, source coverage, requested versus observed models and nullable non-additive usage. Live/unavailable children retain independent cleanup obligations after parent completion. Observation never grants child control or unproved continuation.

- DeliDev API clients export the generated SubscriptionService and closed managed Codex capability/action types. Keep authentication bundles out of Query keys, persistence, synchronization, errors and ordinary resource models. Public clients cannot invoke the protected Worker lane. Follow `cmds-delidev-subscription-contract.md` and retain generation freshness checks.

- Generated NetworkQuery follows `cmds-delidev-network-contract.md`, retaining exact revisions and write-only credential input outside query caches. Preserve service-specific exports; legacy generated paths/reflection are retained only before complete reset activation under the exception above. Signed Worker metadata never proves native installation or encrypted credential transfer.

- Codex Fork uses owner/client-only `SessionService.ForkSession` and `GetSessionFork`, typed `ForkWorkspace`, and allocation-ledger capability `CODEX_SESSION_FORK_V1 = 13`. Local proof is write-only; exact job/child observation cannot replay native creation. Preserve split service ownership and generated compatibility exports under `cmds-delidev-forks-contract.md`.

- Managed database restore follows `cmds-delidev-storage-contract.md`: exact inspected image/live revision and actor-bound external receipts, exclusive settled ownership, immutable current safety/deletion authority, paused/quarantined historical work, and pre-open journal recovery. Preserve typed publication-versus-startup outcomes; uncertain retries never republish or revive native claims.

- Generated `SessionQuery.switchSessionAccount` preserves the exact session revision, UUID-v7 receipt and explicitly selected account. Gate availability with the typed System capability, retain uncertain requests without automatic mutation retries, and leave stopped-session compatibility/authorization in Go.

- Native context and manual compaction use the generated SessionService declarations and closed SessionContextCapability values in `cmds-delidev-claude-compaction-contract.md`. Preserve raw bounded observation JSON, exact uint64 mutation revisions and UUID-v7 retry identity; never infer utilization or action success from an outer envelope.

- Generated UsageQuery consumers verify the NATIVE_UNITS_V1 echo before interpreting native accounting, preserve decimal totals and distinct unit kinds, and keep legacy response fields separate. Grok pricing and budget contribution remain unavailable.

- Generated BrowserQuery bindings expose only the metadata/cleanup contract in `cmds-delidev-browser-contract.md`. Keep browser URLs, tabs and native paths out of product RPCs and React Query server state; local native presentation is independently authorized and generation-bound.

- Generated NativeModelQuery bindings retain exact bigint revisions, original mutation identities and immutable observation cursors under `cmds-delidev-native-models-contract.md`. Keep canonical registration a separate explicit save and advisory metadata outside any client authorization/readiness engine.

- Generated SessionQuery request diagnostics keeps optional metadata, original identity and exact bigint revision/latency under the diagnostics contract. Never infer requests from time proximity or resend a request from a diagnostic receipt.

- `configuration-identity.ts` owns bounded schema-family read negotiation and closed JSON/wire service identity mappings. Preserve API-only v1, recognize only owning v2 families and keep retired original documents inert. It grants no mutation, native support or credential authority; synchronization preserves exact revisions and full snapshot/event atomicity under the client contract.

- Worker network bootstrap/status uses canonical generated NetworkQuery/WorkerQuery declarations. Preserve exact uint64 generations and original request identity. Ciphertext export is a mutation result, never persistent query state; credentials/private keys/decrypted derivatives cannot enter query keys or read projections. Public desired/effective/native states remain independent and grant no execution or observed route use.

- OpenCode Fork independently negotiates System 26 / Worker 15 through the existing SessionQuery fork operations. Preserve exact native turn identities and inherited provenance; preparation grants no child control, effective settings or additive usage. Follow `cmds-delidev-forks-contract.md`.

- OAuth generated bindings retain independent capability 5, field 9 and the closed enums/RPCs under the OAuth contract. Keep code out of cached query/mutation variables and preserve exact original completion identity for code-free local recovery; never add automatic exchange retry.

- Provider presentation identity/order comes from the closed 35-ID mapping in provider-presets.ts; keep the original six, added 26 and local three order consistent with server inventory. This mapping never replaces authenticated availability, account counts or capability negotiation.

- Export independent server-login capability 30, typed progress state/name/generation and write-only original callback messages. URLs, callback bytes and unsaved suggested names stay outside shared caches/persistence/logs. Preserve API, Worker protected-lane and quota authorization; generate outputs from reconciled schemas.

- Subscription diagnostic bindings are generated from main-established protocol allocations. Preserve missing diagnostics separately from an empty detected version; metadata never grants callback/login replay or automatic retry authority.

- Agent Worker ordered source routes follow the catalog, desktop, protocol and sessions contracts. Main reservation the originating change owns System capability 36 and SaveAgentWorkerRequest.route_models field 5; preserve capability 35. Keep schema-3 routes exclusive with legacy fields, all models/accounts referenced and saved atomically, per-source routing state updated only with a successful first claim, confirmed-exhaustion-only fallback and observed-recovery preference for later new sessions. Preserve immutable executions, old-client write protection and portable v1/v2/v3 compatibility; add no SQLite migration.

- Remote repository clients negotiate generated System 37 before saves/imports and preserve Worker 19 independent managed-clone gates. Retain original request bytes and source-kind/URL identity, optional checkout semantics and legacy immutable records under the protocol/workspace contracts.

- Native Claude generated bindings expose System 38 / Worker 20 and closed method/diagnostic/profile metadata. Never place original login URLs or approval-code bytes in query caches, persistent mutation state or logs. Submit is initiating-owner/client-only; protected Take is original-Worker-only. Capability metadata grants no native login or cross-device execution.

- Inline Worker models and endpoint-only completion follow the allocation amendment in `cmds-delidev-catalog-contract.md`. Record System 42 / Worker 22 and the complete endpoint/identity/pricing declarations in the owning feature PR. Compose complete DB 32 / protocol 2 reset with Worker schema 4 and portable bundle 4, removing independent Models/persistent API catalogs while preserving exact inline settings, pricing history and native/account authority. Earlier DBs and backups are unsupported. The owner waives earlier DB retention, permitting an explicit reset of its DB/sidecars without conversion; protected credentials and native ownership retain their original cleanup authority. Reservation-only changes grant no support.

- Standalone GitHub repository access inspection is retired. Preserve the allocated RPC and messages for older clients; authenticated owner/paired-client calls return typed Unsupported/Connect Unimplemented with safe guidance and correlation. The endpoint performs no store, vault, admission or outbound work. Browse and PR operations retain their independent selected-profile/generation/revision checks. Follow `cmds-delidev-integrations-contract.md#retired-standalone-repository-access-inspection`.

- Managed ChatGPT Sidechat follows the feature and `cmds-delidev-sidechat-contract.md#managed-chatgpt-sidechat--issue-1829`. Compose System 47 / Worker 26 with original Sidechat 27/16 and protected Worker 3. Freeze original generation/actor/source/instance; use the exact claimed Fork EXECUTE lease and server-owned Finish receipt before publication. Recheck managed authentication plus read-only enforcement before Fork/input/Steer/compaction. Preserve joined process/plaintext cleanup, original account/history, uncertainty and dependent deletion. Independent subscription Fork stays unsupported; no migration/new login. Fixtures do not establish native/account/platform acceptance.

- The feature remote starting branches uses System 51 / Worker 27 under the workspace, desktop and protocol contracts. Freeze configured source and project/repository/machine revisions; only the original authenticated selected Worker owns read-only native Git discovery. Retain complete 10,000-branch/8 MiB inventory bounds with feature-only job/journal/receipt/transport headroom, protected native Git credentials, safe logs and joined process cleanup. Discovery grants no checkout/preparation/execution authority or migration. Creation preserves saved/manual starting references, independent overrides, comparison base, Local proof and exact uncertain retries; older peers retain manual flows.

- `decodeResourceDocument` in `configuration-identity.ts` owns shared document/schema reads: ordinary resources retain 1 MiB, with only schema-1 `compact-session` Jobs and `workspace-storage` Jobs whose input action is `recover` bounded to 4 MiB. Desktop/receipt and synchronization consumers share this rule without changing aggregate cache limits or original identity checks. Decoding grants no operation or native authority; follow the client, compaction and storage contracts.

- Project behavior settings follow `cmds-delidev-catalog-contract.md#project-behavior-settings-issue-1965`: System 52, schema-2 Project/Settings and portable v5 preserve continuous typed inheritance, explicit project context, source-route/Agent and repository precedence, all three fetch gates, immutable snapshots and first-publication durable plan response decisions. Previously pending plans stay manual; uncertainty never permits resend. Preserve v1–4 imports and reject destructive legacy writes. No SQLite migration or Worker capability is added.

- The feature follow `cmds-delidev-catalog-contract.md#new-session-defaults-and-literal-branch-prefixes--issues-2054-and-2057`: System 56/Worker 38, schema-3 Project/Settings and portable v6 preserve capability52, schema1/2 reads and v1–5 imports. Reject destructive legacy writes. Resolve creation Plan defaults until explicit checkbox edit; freeze pending/uncertain mode and exact requests. Preserve literal `delidev/` default, absent inheritance/empty disable, exact UTF-8 ref validation, original source revisions and legacy execution omission bytes. Compose bounded shared prefix instructions in Execute only through existing four-harness channels, never in Plan/read-only Sidechat or shell interpolation. Preserve all native/account/workspace/history/cleanup authority; no migration or numeric native gate.

- The feature OpenCode Go/Go Plus follows `cmds-delidev-opencode-go-subscription-contract.md`. Preserve identity 4, System 54 and Worker 28 with System 52 Project behavior ownership. Only the exact OpenCode key-backed service may use the fixed Go Chat Completions profile and original accepted native-session header. Reuse protected AccountAPI receipts, joined revocation and confirmed independent cleanup; no native login, quota authority, paid connection validation or migration. Retain category-owned exact uncertain requests and immutable continuation/Fork attribution.

- Sidechat same-question retry follows `cmds-delidev-sidechat-contract.md#same-question-retry--issue-2061`: System 57 / Worker 31 compose original 27/16 and managed 47/26/3. Preserve one direct text-only question, exact actor/revision/turn receipts, immutable child/snapshots, original metadata-only workspace reference, captured Worker/native authority, atomic current-answer publication and all-generation joined cleanup within existing bounds. Never replay uncertain native work, create another child or add a migration.

- Current protocol-2 imports use canonical generated service modules and package-root exports. Retire historical aggregate/query facades. The shared parser supports only schema-4 inline Agent routes and rejects Model resources, ambiguous source identities and older/mixed Agent layouts; preserve all currently activated unrelated Project/Settings/API-profile schemas.

## Storage
No implicit persistence. Tokens remain caller-owned; cursors, resource revision/size accounting and duplicate IDs exist only during the selected connection. No SQLite, workspace, account browser or secure-vault access is implemented here. A client switches server/device only after canceling prior streams and clearing their query/resource caches.

## Security
HTTP is restricted to literal `127.0.0.1`, `[::1]` or `localhost`; remote origins require HTTPS. Raw authority validation precedes WHATWG normalization, rejecting numeric IPv4 aliases, credentials, paths, queries and fragments. Redirects, browser cookies and HTTP caching are disabled. No auth discovery/fallback, secret logging, Web Storage, Tauri traffic proxy or harness/provider endpoint access occurs. Query keys must never contain credentials. RPC authorization and all product mutations remain server-enforced.

## Logging
The library does not log resource documents, credentials, cursors, exception objects or request bodies. It exposes closed connection states and typed safe failures with correlation IDs so the consuming desktop can report actionable status. Actual server-side RPC logs retain their existing sanitized boundary.

## Build and Test
- `pnpm --filter @delinoio/delidev-api-client lint`, `test`, and `build` validate types, synchronization/transport fixtures and generated exports.
- The test command compiles and starts the real Go server in a private temporary scope, exercises binary RPC, coherent snapshots, revision events, idempotent mutation replay, indexed deletion and typed authentication failure, then stops its owned process. It never reads user credentials, invokes inference, or starts a Worker. Go is required; the integration task is uncached.
- Run root protocol formatting/lint/compatibility and repeat generation without drift. The dedicated `delidev-protocol` CI job runs client validation, DeliDev Go binding tests and desktop tests. DeliDev desktop/command/protocol/client and shared configuration inputs select it; DevHud-only source inputs do not. Shared `pnpm proto:check` retains repository-wide schema checks. Generated `dist` is an explicit compilation output and is removed from the final worktree.

## Dependencies and Integrations
Pinned Buf protobuf 2.14.0, Connect/Connect Web 2.1.2 and Connect Query 2.3.1 follow repository versions. React Query integration uses generated service namespaces; no second product transport or client-side routing/eligibility engine is introduced. The Go server and its canonical resource schemas remain authoritative.

## Change Triggers
Update generated bindings, package/domain AGENTS, protocol contract, project index, validation records in pull requests, issues and CI logs/artifacts, generation freshness/Turbo outputs and CI when schemas, origin/auth boundaries, synchronization behavior or validation commands change.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References
- [Project](project-delidev.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Connect Web](https://connectrpc.com/docs/web/getting-started/)
- [Connect interceptors](https://connectrpc.com/docs/web/interceptors/)

SessionQuery exposes generated budget read/write descriptors and the client recognizes the typed `budget_reached` failure. Budget amounts remain exact decimal strings and counts retain uint64 precision. Clients never recompute price history, decide execution eligibility or reinterpret incomplete evidence as verified compliance; the owner/client session RPC remains authoritative.

The existing resource/event envelope also carries optional typed Claude message documents from Go. One provider message remains one indexed message resource containing ordered block indices and independent block/message lifecycle. Clients must preserve the complete document, fetch revisions by original resource identity, and never flatten blocks or infer a terminal execution from provider message closure. No new transport or native API path is introduced.

The existing `Resource.document_json` usage shape may contain a typed `claude_observation`. Preserve nullable decimal-string counters and exact native USD estimate strings, independent provider reports and input-result main-loop/cumulative scopes. It does not change generated schemas or grant billing/response-ledger authority.

### Session files

Generated `SessionQuery.readSessionWorkspace` exposes the [file explorer contract](cmds-delidev-files-contract.md). Presentation keeps decimal size strings exact and uses bounded, nonpersistent caches without mutation intents, background polling or automatic retries. File text stays inert; clients cannot supply absolute roots or turn native transcript paths into access authority.

## Portable configuration
Generated `ConfigurationQuery` exposes export, preview and apply. Clients preserve the original document bytes, explicit target mappings, exact reviewed plan and retained request identity; JavaScript numeric parsing is never the serialization authority. Preview editing invalidates apply, and accepted jobs are observed independently of mutation retries. Follow [portable configuration](cmds-delidev-configuration-transfer-contract.md).

### Session Git comparisons

`SessionQuery.readSessionWorkspace` also carries the closed `git-diff` query/result described in the [workspace read contract](cmds-delidev-files-contract.md). Clients validate repository/comparison/path ownership, exact Git object and revision strings, bounded inert patch text and separate untracked paths; they retain no persistent file cache. The existing generated Connect service surface is unchanged.


## Local review coordinates

Generated `SessionQuery.readSessionReviewContext` exposes the exact original Worker diff and validated file/line coordinates. Preserve original side numbers, hunk identity, final-newline absence and non-line file-only states. The Go domain remains coordinate authority; clients cannot turn these read-only observations into implicit mutation or execution. Follow the [workspace contract](cmds-delidev-files-contract.md).


## Durable local review client

Generated `SessionQuery` includes comment creation/edit/deletion and grouped submission. Use `ResourceQuery` for scoped review reads. Preserve bigint entity revisions and decimal-string content revisions without numeric rounding, strict comment/submission document variants, original anchor/context and immutable submitted copies. Current data cannot replace selected request revisions or uncertain wire requests. The desktop uses connection-scoped mutation retention and does not infer agent execution from queue acceptance. Follow [local reviews](cmds-delidev-files-contract.md).


`IntegrationQuery` exports generated SaveIntegrationProfile, ReplaceIntegrationToken, ValidateIntegrationProfile and DeleteIntegrationProfile descriptors. PATs are bounded write-only fields; no token belongs in query keys, resource documents or read responses. Preserve pending-operation decimal revisions and actor-bound exact request identities under the [integration contract](cmds-delidev-integrations-contract.md).

Standalone GitHub repository access inspection is retired. Preserve the allocated RPC and messages for older clients; authenticated owner/paired-client calls return typed Unsupported/Connect Unimplemented with safe guidance and correlation. The endpoint performs no store, vault, admission or outbound work. Browse and PR operations retain their independent selected-profile/generation/revision checks.

`IntegrationQuery.queryRepositoryIntegration` supplies PR/issue list/search/detail and PR-only immutable diff, latest head Checks and combined commit-status reads. Consumers validate repository/revision/profile, exact requested query, original API identity namespace and complete bounded response. Keep nullable mergeability, independent search incompleteness/count/cap and original pagination semantics. PR observations require exactly one original detail and its exclusive selected family, matching heads, exact IDs/counts and original known/unknown states. Checks/statuses remain separate and cannot imply required CI or evaluated-commit evidence. Inactive content queries are canceled/discarded; no browser-side GitHub fetch or stored PAT is added.

`SessionQuery.linkSessionPullRequest` and `unlinkSessionPullRequest` manage durable session PR metadata. ResourceQuery reads the session-scoped `PULL_REQUEST` records. Preserve exact decimal remote identities and uint64 association revisions, session/project ownership, original uncertain request bytes and current-state receipt semantics; links never grant execution or current GitHub access. The generated messages/descriptors reproduce from the canonical schema without handwritten transport code.

Generated IntegrationService descriptors expose `GetGitHubTokenForm` and `GitHubTokenAccess`. Clients retain exact bigint profile revisions, validate the closed returned form against the selected profile and explicit scope, and separate the read from optional local OS presentation. No URL is a token-access grant.

The same repository query adds PR-only `rules`: consumers validate a complete exclusive base/head-bound active-rule inventory, exact ruleset/App IDs, unknown source states and bounded required-check entries. The desktop offers an explicit detail read and disposes inactive queries. No new transport or generated fields are required; rules do not establish evaluated-commit or CI outcomes.

PR-only `ci` uses the same generated repository query with no caller paging. Validate complete head/test-merge inventories, exclusive family, PR binding, exact App/ruleset/result identities and assessment references before display. Unknown/pending/missing and non-failing are distinct from terminal failure. The read creates no remediation execution or stored PAT and uses no browser GitHub API.

PR-only `feedback` uses the existing generated repository query without caller paging. Validate the exclusive current-PR-bound inventory, exact IDs, published states, complete review/thread membership, structural content versions and bounded inert author/body/code context. Discard inactive observations; do not treat approved/dismissed states or Bot names as local handling, GitHub App or reviewer-permission proof. No client-side GitHub request or generated transport change is introduced.

PR-only `reviewers` uses the same generated query and current selected repository/profile binding. Validate complete feedback/author/App coverage, current numeric/node identity correspondence, native actor types, exact standard/custom permission intervals and content-version-specific attribution. Keep unavailable capabilities independent; Bot names, public visibility and historical observations cannot create execution authority. No new transport or generated fields are needed.


Generated IntegrationQuery now includes RefreshPullRequestProblems, ListPullRequestProblems and DismissPullRequestProblem. Retain exact request UUIDs, big integer resource revisions, stable remote numeric strings and content versions. Collection inputs contain only local repository selection/PR number; callers cannot submit evidence. Read history without implicit collection and preserve signed page tokens and explicit stale-cursor recovery. Local UI mutations do not authorize execution.


The generated PR collection kind enum adds independent CI/conflict reads while omitted/explicit feedback preserve the original receipt family. Unknown numeric kinds are rejected on the server. Shared Resource envelopes retain typed original CI proof references and conflict transitions; callers must keep historical evidence separate from current prerequisites and cannot infer handling or execution from a response.


IntegrationQuery also exports generated ListPullRequestRemediationAttempts and ResumePullRequestRemediation bindings. Callers preserve exact stable numeric strings and original set/request/revision identity; paginated history and explicit allowance resumption cannot infer execution authority or perform implicit mutation retries. These methods share the existing authenticated direct Connect transport and require no new client persistence or environment configuration.

### Managed database backup observation

SystemService exposes owner/client backup creation, metadata pagination and explicit integrity inspection through generated Connect queries and the CLI. Settings > Backups retains exact creation retries and displays precise byte counts. Listing is not integrity or restoration evidence; failed reinspection clears prior success. Follow the [storage contract](cmds-delidev-storage-contract.md) for bounds, identity checks, pagination and the separate permanent-session deletion and managed restoration boundary. `DeleteBackup` and `ListBackupDeletions` expose durable irreversible image deletion, original inspected revision/metadata/hash, explicit confirmation, retained exact retries and restart-visible pending/completed jobs. Logical image bytes and unknown interrupted unlink counts never imply physical free-space recovery.


### Durable backup creation

`RequestBackup`, `GetBackupCreation` and `ListBackupCreations` expose original durable jobs through Connect and generated queries. Current CLI and Settings use that path. The synchronous `CreateBackup` remains compatible only before complete protocol-2/baseline-32 reset activation; reservations alone do not retire it. At the current complete activation, the [reset contract](cmds-delidev-structure-contract.md#pre-release-compatibility-reset) retains only its historical declaration and allocation provenance: authorized calls return Unsupported without a receipt, backup file or current job. Keep pending acceptance separate from image publication, exact retries across navigation, typed failure/stale observations and integer precision. Jobs resume after server restart without client resubmission, and completed history does not assert current image availability. See the [storage contract](cmds-delidev-storage-contract.md).

`SystemService.GetBackupDeletion` is an owner/paired-client read of one original
durable deletion job ID, independent of history pagination. It rechecks current
authority, rejects other job types and preserves exact uint64 revisions and byte
counts without replaying acceptance or filesystem work. Regenerate Go, TypeScript
and Connect Query bindings together.

Generated `ProviderQuery` exposes bounded provider inventory and the client maps typed `provider_disabled` failures. Desktop provider/model consumers require provider activation, active-provider model filtering and account-provider filtering capabilities; split account views and their wizard additionally require `ACCOUNT_TYPE_FILTER`. Use generated inventory/model queries; do not infer availability from generic resource pages or query unfiltered providers as fallback. Preserve exact mutation requests across uncertain outcomes. See [provider activation](cmds-delidev-provider-activation-contract.md).

## Worker workspace snapshots

`WorkspaceStorageQuery` exports generated owner/client acceptance, operation-read
and cancellation descriptors. Preserve action/session/snapshot/preview/recovery
identities, exact expected revisions and original request UUIDs. Acceptance is a
job, not verified snapshot/cleanup success; reconnect can only query it or retry
its exact receipt. Explicit recovery is separate from native replay. Generated
`WORKSPACE_STORAGE_V1` permits clients to discover this boundary; no desktop
storage workflow or automatic retention is implied. Decimal byte/revision values
remain precise. See the [storage contract](cmds-delidev-storage-contract.md).

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

### Stopped-session account switching

Generated `SessionQuery.switchSessionAccount` and the typed System stopped-Codex-account-switch capability expose explicit future selection. Preserve exact request/account/session revision after acknowledgement loss; replay is explicit and current-state-only. The server validates stopped ownership, portable history and compatibility. No client-side eligibility/routing, implicit Resume or credential access is introduced.

## Current-user service metadata

Generated SystemQuery exports `getUserService` and `controlUserService` with the additive `USER_SERVICES_V1` capability and closed service enums. Keep exact bigint revisions and original request identity; pending/uncertain control cannot be retried as a fresh native write. Returned metadata excludes private paths, executable/process identity and authentication. Read capability and absent state through the existing real Go Connect fixture, without native registrations. See the [user-service contract](cmds-delidev-user-services-contract.md).

## Authenticated development-server forwarding

The feature follows the [session forwarding contract](cmds-delidev-forwarding-contract.md). Additive `ForwardService` start/get/stop, one-shot claim, streaming traffic and original cleanup RPCs plus `WorkerService.WatchForwardRequests` preserve authenticated client/session/Worker ownership and typed `SESSION_FORWARDING_V1` capabilities. Generated Go/TypeScript descriptors and `ForwardQuery` expose the shared API. The CLI owns an explicit loopback listener and returns its exact endpoint. Stop preserves forwards; Archive/deletion/revocation close them, and every Archive completion requires independently confirmed original cleanup. Receipt replay and reconnect cannot recreate a claimed native lifetime. Model API endpoints remain server-relative. Generic schema-24 entities/receipts retain metadata without traffic or a relational migration.


Generated `SessionQuery.deleteSession`/`getSessionDeletion` expose confirmed
irreversible acceptance and independent original-job observation. Generated
Worker cleanup queries and `PERMANENT_SESSION_DELETION_V1` retain the additive
protocol without duplicating Go ownership logic. Preserve original request IDs,
BigInt revisions and pending/unknown removal state; no automatic mutation replay.
See the [storage contract](cmds-delidev-storage-contract.md).

Generated `PullRequestFixQuery` provides typed manual-fix capability and acceptance operations. Callers preserve exact original version-1 selection/request bytes and validate original attempt/session/set acknowledgments. Uncertain responses permit only same-request receipt replay; server lookup credentials never become native Git authority. See the integration contract.

### Explicit outbound networking

Generated `NetworkQuery` exposes authenticated configuration operations and typed capability/resources. Consumers retain exact decimal revisions and treat credentials as write-only request input, never query-cache data. Signed Worker metadata proves only the server export scope, not native installation or encrypted credential transfer. Follow [the network contract](cmds-delidev-network-contract.md).

The feature generates the native accounting profile and unit enums with the existing UsageQuery descriptor. Consumers must require the echoed NATIVE_UNITS_V1 profile before interpreting UsageTotals.accounting, preserve exact decimal totals and distinct CodexResponse/GrokClosedInput unit kinds, and retain response-only legacy fields. Grok cost and budget contribution remain unavailable; clients cannot normalize or price the separate response dimensions.

## Managed Codex subscription clients

The generated SubscriptionService/action types and closed managed-Codex Worker capability are exported with a SubscriptionQuery namespace. Owner/client lifecycle and login-presentation requests remain separate from the protected Go Worker bundle channel. Bundle bytes may never enter query keys, saved state, synchronized resources, errors or ordinary outputs. See [managed subscriptions](cmds-delidev-subscription-contract.md); generated clients alone do not enable desktop login or prove account/platform acceptance.
## Browser client

[Protected browser ownership and cleanup](cmds-delidev-browser-contract.md) use the service-owned `BrowserService`, typed profile/state/capability models and generated `BrowserQuery` descriptors. Existing shared enum/field numbers and future migration reservations remain unchanged. Browser messages carry ownership metadata only, never native paths or browsing content.

## Codex fork clients

Generated messages, the `ForkWorkspace`/server capability enums and the existing
`SessionQuery` namespace now expose `forkSession` and `getSessionFork`. The
[fork contract](cmds-delidev-forks-contract.md) keeps native paths and creation
logic in Go. Preserve exact uncertain requests; observe accepted operations by
job ID instead of issuing another mutation. Desktop connection memory retains
its controller through navigation and separates acceptance from child publication.

## Service-native schema families

`configuration-identity.ts` owns bounded schema-v2 read negotiation for closed service-native Account/native Model, reconfiguration-required Agent and inert retired wrappers. It preserves API-only v1 reads, refuses mixed Provider/service identity and maps generated closed service enums independently from JSON service strings. The helper grants no mutation/native authority and never unwraps a retired document into a live configuration. Synchronization accepts those owning families without changing exact revisions or snapshot/event atomicity. Portable UI preserves original v2 or API-only v1 document/preview bytes; service-native v1 graphs are unsupported.

Worker bootstrap export uses generated mutation results with bounded ciphertext and its separately displayed authenticated digest. Status retains exact desired/effective/native generation strings and closed route states; current control readiness cannot manufacture native route use or provider success. Ciphertext is not persistent query state, and private recipient keys/decrypted derivatives never cross the client boundary.

OpenCode General Chat Fork uses existing generated `SessionQuery` ForkSession and GetSessionFork with independently negotiated System 26 / Worker 15. The Unix pinned profile retains exact source revision/native turn, paused independent child identity, and explicit inherited-message provenance with no input or accounting authority. Native agent/model absence remains preparation state until the first real child input; clients cannot infer selection or broaden the Go-owned plain-text eligibility boundary.

Independent server subscription login exports capability 30, the closed SubscriptionLoginState and ForwardSubscriptionCallback from reconciled schemas. Keep progress URLs, transient suggested names and write-only callback bytes outside shared query caches. Original operation/generation checks precede name entry; exact request retries cannot grant callback replay. Explicit-machine Worker methods, API accounts and quota ownership retain their contracts.

## Codex login diagnostic client

Generated subscription progress exposes an optional `CodexDiagnostic` and closed `CodexDiagnosticPhase` enum using main-established allocations. Preserve absent metadata independently from a reported empty detected version. Keep the original operation's progress in its owning Settings lifetime rather than shared query caches. Metadata never permits native replay, callback forwarding or login retries; renderer presentation reconstructs safe text from validated version/phase/code fields.

## Repository addition

System capability 37 permits repository saves/imports with a required credential-free `remote_url` and empty `checkouts`. Clients must verify this gate before sending URL registration or importing the new URL-only repository shape. Legacy checkout-backed repository saves/imports omit `remote_url` and retain the pre-capability contract. The existing save RPC and durable job also cover registration without Worker proof or inspection children. Repository and Project export/import need no machine or path binding when checkouts are empty. Worker capability 19 separately permits managed workspace and independent Fork clones; 31/32 and Worker 18 keep their existing immediate Local Clone and metadata contracts.

Generated `IntegrationQuery.listGitHubRepositories` and
`WorkerQuery.cloneRepository` retain the main-established declarations and
independent System 31/32 / Worker 18 capabilities. Listing is an explicit
revision-bound profile/page read; preserve exact remote IDs, current generation,
constructed URLs and page-local filtering. The client never receives a saved PAT.
Clone sends fresh transient local Worker proof and optional selected GitHub
metadata; its original durable job completes registration server-side. Keep proof
outside read keys, drafts and persistence. Retain identical uncertain mutation
bytes only in the disposable dialog registry; close/departure drops that registry
and guards all late callbacks without canceling accepted business work.

## Agent Worker wizard bindings

Generate ConfigurationQuery.saveAgentWorker, typed model-selection oneof and
System capability 33 from their canonical schemas. Source-scoped account/model
queries use the closed service enum and server pagination; keys retain each exact
source/cursor. Keep original uint64 model/Worker revisions and exact uncertain
wire requests. The canonical model resource remains an internal identity used by
existing APIs, Usage and historical snapshots. Configured compatibility and
catalog results grant no execution readiness. Follow the desktop/catalog contracts.

## Failed subscription cleanup client

The existing cleanup RPC also includes fully disconnected subscription configurations across services. Individual failed initial ChatGPT deletion uses the existing configuration deletion RPC; generated request/response declarations and capability numbers remain unchanged.

Generated SubscriptionQuery exports cleanupFailedSubscriptions and getFailedSubscriptionCleanup plus the closed batch state, outcome and reason enums. Capability 41 was reserved on main in the originating change; preserve Claude 38/Grok 39/40 and their independent support. Exact request UUIDs bind uncertain admission retries; accepted work is read by original job ID, with bigint revisions and fixed 50-result pages. Validate identity, monotonic counts/revision, closed state/outcome/reasons and complete page bounds before accepting status. Metadata-only status may use category-scoped Connect Query; neither generated bindings nor a read grant native/login/callback/deletion authority. See the subscription/Settings contracts.

## Project prompt history client

Generated `ConfigurationQuery` exposes `listProjectPromptHistory` and `clearProjectPromptHistory`, the closed entry type, System capability 48 and entity kind 35. Preserve exact text, bigint acceptance order, actor/project-bound cursors and original confirmed-clear request identity. Reads never restore skill, image or execution authority. Capability absence preserves ordinary composer behavior.

## Repository branch discovery
Generated System 51 and Worker 27 plus `DiscoverRepositoryBranches` independently
negotiate remote starting-branch metadata. Reuse existing Job resources and
original revision/request identities. The desktop validates up to 10,000 sorted
unique branch names in an 8 MiB result using a dedicated bounded 9 MiB Job reader;
the general 1 MiB document parser remains unchanged. Result identity must match
original project/repository/selected Worker revisions. Discovery remains advisory;
session creation uses existing remote starting overrides and ordinary preparation
validation. Older peers retain saved/manual reference flows.

## Typed large Job document reads

`decodeResourceDocument` owns the shared UTF-8 JSON/schema read boundary. Ordinary
resources remain bounded to 1 MiB. Only schema-1 Job documents with type
`compact-session`, or `workspace-storage` with input action `recover`, may use
their existing 4 MiB Job bound. Unsupported families, invalid UTF-8/JSON and
oversized documents remain unavailable. This read exception grants no operation
or native authority. Desktop document/receipt readers and synchronization use
the same decoder; synchronization retains its existing aggregate memory,
resource-count, identity/revision and snapshot/event publication bounds.

## Project behavior settings

Follow [project behavior settings](cmds-delidev-catalog-contract.md#project-behavior-settings) for schema-2 documents, continuous inheritance, explicit original project context, policy precedence, first-publication durable plan decisions and portable version 5. Preserve the original domain authority and uncertainty rules; no SQLite migration is introduced.

## New-session defaults and branch prefix declarations

Follow [the feature](cmds-delidev-catalog-contract.md#new-session-defaults-and-literal-branch-prefixes) for System 56, Worker 38, schema-3 Project/Settings and portable version 6. Preserve capability 52, schema-1/2 reads and v1–5 imports; reject destructive old-client writes. Automatic creation mode yields only to an explicit checkbox edit. New immutable prefix declarations preserve original source revisions and legacy omission bytes. Shared Execute instructions remain literal, bounded, and absent in Plan/read-only Sidechat; existing native/account/Worker/workspace/recovery/cleanup ownership remains authoritative. No migration, native version gate, branch interception or automatic branch creation is introduced.

## Mobile consumer ownership

`apps/delidev-mobile` consumes the generated client directly over its selected
HTTPS profile. Mobile owns device-only credentials, immutable pending mutation
requests, profile/cache isolation and foreground lifetime. Shared synchronization
remains read-only; returning to the foreground cannot retry a mutation. A bounded
Settings snapshot supplies a coherent event cursor while session/history pages
remain independently paginated. No new business RPC or transport authority is
added. Follow the [mobile contract](apps-delidev-mobile-contract.md).

Current schema-4 Agent routes embed exact source/native identity and non-secret metadata. The shared parser rejects Model resources, mixed UUID routes and older Agent schemas. Other currently activated Project, Settings and API-profile schemas remain supported. Protocol-2 connection verification rejects protocol 1 without adopting its state.

## Codex asynchronous message decoding

The `codexAsyncMessage` helper validates the closed version-1 optional message
metadata profile. Preserve explicit field presence, native nullable delivery,
ordered question titles and null versus empty options. Apply the owning harness
bounds and reject unknown fields and malformed types. This decoder returns
inert text provenance only, with no request, reply, media fetch or dispatch
authority. Generated capability bindings advertise System 92 and Worker 61.
