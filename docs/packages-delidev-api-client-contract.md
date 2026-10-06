# DeliDev TypeScript client

Generated AccountQuery and AccountService expose StartAccountOAuth, CompleteAccountOAuth, CancelAccountOAuth and GetAccountOAuthStatus, with exact bigint revisions and closed OAuth state/connection-method enums. Authorization URL exists only in original live Start; status carries metadata only. Completion code is a write-only bounded byte array: use a direct authenticated RPC without query/mutation-cache retention, clear transient buffers, and recover only the original completion identity without code. No client-side retry may repeat an exchange. Preserve all four existing account-flow gates independently of capability 5 under the [OAuth contract](cmds-delidev-account-oauth-contract.md).

## Request diagnostic client

Generated `SessionQuery.listRequestDiagnostics` and `SystemCapability.REQUEST_DIAGNOSTICS_V1` expose the issue #1103 metadata read. Preserve native-input versus proxy-HTTP enum provenance, optional unavailable observations, exact bigint revisions/latency and original session/execution/page selection. The client performs no matching by time/model, usage ingestion, request reconstruction or receipt-driven HTTP retry. Follow the [diagnostics contract](cmds-delidev-diagnostics-contract.md); bindings remain tool-generated.


Buf generates service-specific modules. The normal protocol generation command
also runs `scripts/delidev/proto-compat.mjs` to reproduce historical module and
Connect Query import paths. Package-root exports and existing `./gen/*` consumers
remain compatible; facades contain re-exports, never handwritten descriptors.

## Scope
`packages/delidev-api-client` owns private `@delinoio/delidev-api-client`, generated messages and service-specific Connect Query namespaces, explicit transport, typed errors, UUID-v7 request identities and bounded resource synchronization. This is the client integration boundary for desktop implementation; it does not itself constitute a desktop app or complete issue #964.

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

## Storage
No implicit persistence. Tokens remain caller-owned; cursors, resource revision/size accounting and duplicate IDs exist only during the selected connection. No SQLite, workspace, account browser or secure-vault access is implemented here. A client switches server/device only after canceling prior streams and clearing their query/resource caches.

## Security
HTTP is restricted to literal `127.0.0.1`, `[::1]` or `localhost`; remote origins require HTTPS. Raw authority validation precedes WHATWG normalization, rejecting numeric IPv4 aliases, credentials, paths, queries and fragments. Redirects, browser cookies and HTTP caching are disabled. No auth discovery/fallback, secret logging, Web Storage, Tauri traffic proxy or harness/provider endpoint access occurs. Query keys must never contain credentials. RPC authorization and all product mutations remain server-enforced.

## Logging
The library does not log resource documents, credentials, cursors, exception objects or request bodies. It exposes closed connection states and typed safe failures with correlation IDs so the consuming desktop can report actionable status. Actual server-side RPC logs retain their existing sanitized boundary.

## Build and Test
- `pnpm --filter @delinoio/delidev-api-client lint`, `test`, and `build` validate types, synchronization/transport fixtures and generated exports.
- The test command compiles and starts the real Go server in a private temporary scope, exercises binary RPC, coherent snapshots, revision events, idempotent mutation replay, indexed deletion and typed authentication failure, then stops its owned process. It never reads user credentials, invokes inference, or starts a Worker. Go is required; the integration task is uncached.
- Run root protocol formatting/lint/compatibility and repeat generation without drift. The existing shared protocol CI job runs client validation and is selected by DeliDev command/client inputs. Generated `dist` is an explicit compilation output and is removed from the final worktree.

## Dependencies and Integrations
Pinned Buf protobuf 2.14.0, Connect/Connect Web 2.1.2 and Connect Query 2.3.1 follow repository versions. React Query integration uses generated service namespaces; no second product transport or client-side routing/eligibility engine is introduced. The Go server and its canonical resource schemas remain authoritative.

## Change Triggers
Update generated bindings, package/domain AGENTS, protocol contract, project index, validation records in pull requests, issues and CI logs/artifacts, generation freshness/Turbo outputs and CI when schemas, origin/auth boundaries, synchronization behavior or validation commands change.

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

`IntegrationQuery.inspectRepositoryIntegration` exposes the repository-specific read. Consumers must validate the complete schema-version-1 document, exact repository/revision/profile and canonical feature states before display. Preserve unknown access and previous observations distinctly; no endpoint success constitutes CI/rules/reviewer evidence. Desktop cancels/discards observations on close/inactivation and uses no automatic focus/reconnect retries.

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

`RequestBackup`, `GetBackupCreation` and `ListBackupCreations` expose original durable jobs through Connect and generated queries. Current CLI and Settings use that path; the synchronous `CreateBackup` remains compatible. Keep pending acceptance separate from image publication, exact retries across navigation, typed failure/stale observations and integer precision. Jobs resume after server restart without client resubmission, and completed history does not assert current image availability. See the [storage contract](cmds-delidev-storage-contract.md).

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

Issue #1089 follows the [session forwarding contract](cmds-delidev-forwarding-contract.md). Additive `ForwardService` start/get/stop, one-shot claim, streaming traffic and original cleanup RPCs plus `WorkerService.WatchForwardRequests` preserve authenticated client/session/Worker ownership and typed `SESSION_FORWARDING_V1` capabilities. Generated Go/TypeScript descriptors and `ForwardQuery` expose the shared API. The CLI owns an explicit loopback listener and returns its exact endpoint. Stop preserves forwards; Archive/deletion/revocation close them, and every Archive completion requires independently confirmed original cleanup. Receipt replay and reconnect cannot recreate a claimed native lifetime. Model API endpoints remain server-relative. Generic schema-24 entities/receipts retain metadata without traffic or a relational migration.


Generated `SessionQuery.deleteSession`/`getSessionDeletion` expose confirmed
irreversible acceptance and independent original-job observation. Generated
Worker cleanup queries and `PERMANENT_SESSION_DELETION_V1` retain the additive
protocol without duplicating Go ownership logic. Preserve original request IDs,
BigInt revisions and pending/unknown removal state; no automatic mutation replay.
See the [storage contract](cmds-delidev-storage-contract.md).

Generated `PullRequestFixQuery` provides typed manual-fix capability and acceptance operations. Callers preserve exact original version-1 selection/request bytes and validate original attempt/session/set acknowledgments. Uncertain responses permit only same-request receipt replay; server lookup credentials never become native Git authority. See the integration contract.

### Explicit outbound networking

Generated `NetworkQuery` exposes authenticated configuration operations and typed capability/resources. Consumers retain exact decimal revisions and treat credentials as write-only request input, never query-cache data. Signed Worker metadata proves only the server export scope, not native installation or encrypted credential transfer. Follow [the network contract](cmds-delidev-network-contract.md).

Issue #1100 generates the native accounting profile and unit enums with the existing UsageQuery descriptor. Consumers must require the echoed NATIVE_UNITS_V1 profile before interpreting UsageTotals.accounting, preserve exact decimal totals and distinct CodexResponse/GrokClosedInput unit kinds, and retain response-only legacy fields. Grok cost and budget contribution remain unavailable; clients cannot normalize or price the separate response dimensions.

## Managed Codex subscription clients

The generated SubscriptionService/action types and closed managed-Codex Worker capability are exported with a SubscriptionQuery namespace. Owner/client lifecycle and login-presentation requests remain separate from the protected Go Worker bundle channel. Bundle bytes may never enter query keys, saved state, synchronized resources, errors or ordinary outputs. See [managed subscriptions](cmds-delidev-subscription-contract.md); generated clients alone do not enable desktop login or prove account/platform acceptance.
## Browser client

[Protected browser ownership and cleanup](cmds-delidev-browser-contract.md) use the service-owned `BrowserService`, typed profile/state/capability models and generated `BrowserQuery` descriptors. Existing shared enum/field numbers and future migration reservations remain unchanged. Browser messages carry ownership metadata only, never native paths or browsing content.

## Codex fork clients (#1092)

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
