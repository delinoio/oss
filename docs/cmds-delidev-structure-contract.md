# DeliDev source ownership and compatibility

## Allocation workflow

A complete DeliDev feature PR may update protocol-number and database-version
allocation records, active declarations, generated bindings and implementation
together. A separate reservation PR, prior merge to main or reservation closure
before branching is not required. This workflow supersedes earlier main-first
prerequisites; historical reservation PRs remain provenance, not delivery gates.

Preserve existing numbers, original declaration/member and migration owners,
shared consumers, collision checks and compatibility checks. Reconcile concurrent
allocations against the current target branch before merging, then regenerate
outputs from the reconciled sources. Keep real migration dependencies and order;
never insert an empty migration to skip unfinished work. Planned records remain
separate from executable migrations and do not advertise runtime, native or
account support. Complete feature delivery and its acceptance requirements remain
unchanged.

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
GrokDiagnostic fields 1–7 and closed GrokDiagnosticPhase values 0–11. Record
the complete closure in the owning feature PR. Codex field 7 and existing
System 30/35/36/37 and Worker 19 ownership
remain unchanged. Reservations activate nothing and add no migration.

## Agent Worker source-route ownership

PR #1371 established System capability `AGENT_WORKER_SOURCE_ROUTES_V1 = 36`
and `SaveAgentWorkerRequest.route_models = 5` on main before implementation.
Capability 35 remains owned by known subscription models. The catalog contract
owns ordered source/model/account references, atomic multi-model saving and the
confirmed-exhaustion first-execution boundary under one Harness. Later new
sessions prefer an earlier source after observed quota recovery. Existing
sessions retain their selected account/model and immutable attribution.
Domain and store own the shared
pure source selector and per-source routing state. Server owns immutable complete
decisions and selected-source dispatch; desktop owns group editing and source-scoped
catalog/account pages. Portable configuration version 3 maps the complete graph.
Generated bindings are regenerated from reconciled schemas. No SQLite migration
or historical snapshot rewrite is introduced; capability advertisement cannot
grant native/account acceptance.

The reservation alone changed no active schemas, generated bindings, resource
documents, SQLite migrations or runtime support. The implementation reuses the
existing typed model selection; reserved numbers alone grant no feature support.

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

## Scope

The 2026-09-30 structural change preserves the main implementation while preparing
independent replacements for the owner-approved set of 22 PRs.
Those PRs' unmerged features are not activated by this refactor.

## Documentation

Project indexes contain ownership, domain links and cross-domain invariants.
Implementation details belong to domain contracts. Record implementation status
and validation results in pull requests, issues and CI logs/artifacts under the
root DeliDev validation policy; do not add repository evidence documents. Include
the source revision, commands, results and unresolved limits. Distinguish fixtures,
builds and packaging from actual native/account/platform acceptance, and exclude
secrets, user state and raw native content from validation records.
Parent AGENTS files route work to scoped owners. Cross-domain changes must read
all affected owners, even when their rules live outside the edited directory.

## Protocol

GitHub token-first onboarding under issue #964 reserves System capability 34,
two closed enums and five new message declarations in the protocol allocation
ledger. Record this allocation closure in the owning feature PR. Records alone
grant no token inspection, pre-profile form, credential
retention or browser capability and adds no migration. Follow the protocol and
integration contracts.

The Agent Worker wizard amendment under issue #964 reserves System capability
`AGENT_WORKER_WIZARD_V1 = 33`, list-only subscription-service field 4 on
`ListResourcesRequest`, field 7 on `SearchModelsRequest`, and the wholly new
`SaveAgentWorkerRequest` and `AgentWorkerModelSelection` declarations. The request
reserves mutation/document/model/schema-version fields 1–4; model selection
reserves canonical model ID/native ID/expected model revision fields 1–3.
PR #1351 established this closure on main before the source-scoped wizard and
atomic model/Agent save. The implementation reuses the existing acknowledgement
and storage schemas without a database migration. Reservations alone still grant
no runtime support, native execution or account authority.

Keep package `delidev.v1`, Go import paths, RPC procedure names, existing field and
enum numbers, JSON meanings and TypeScript exports stable. Service-specific schema
files own their exclusive request/response types. Shared types and their dependency
closure have one common owner. Generated compatibility exports preserve historical
TypeScript import paths; generated code is never resolved by choosing a merge side.
Regenerate from the reconciled source schema.

The numeric allocation ledger binds each new enum member or existing-message field
to its original PR, or its owning issue when no implementation PR exists yet, and
a unique number. Shared consumers are recorded explicitly. Existing main
assignments are immutable.
Reservations do not advertise capability support or activate implementation.
Feature PRs may add allocation records, active declarations and implementation
together; no separate reservation PR or prior merge to main is required. Existing
shared message semantics still require explicit composition.

Issue #1203's shared compaction RPC closure reserves
`CompactSessionRequest.expected_execution_id = 2` and
`CompactSessionResponse.session = 4`, with #1093/#1202 as shared consumers.
Record PR #1221's already-active request/response assignments in the immutable
baseline without renumbering them. Record the additive reservations with exact
predecessor admission and joined current-session/job responses in the owning
feature PR. The existing response request ID remains the original action ID.
Reservations do not add active schema fields, generated bindings, capabilities
or migrations and do not establish complete native acceptance.

Wholly new messages and closed enums use explicit `newDeclaration: true` member
reservations under one original owner, including the zero UNSPECIFIED member of
each enum. Later additions retain their own original issue/PR owner; they do not
redefine declaration ownership. Keep planned declarations out of the immutable
active baseline. The allocation check validates message fields and enum values,
their unique numbers and later active schema declarations without requiring
premature runtime support.

## Runtime and storage

Server lifecycle, authorization, route registration and status have separate owners.
CLI framing/authentication and command groups have separate files. They retain
existing order, authorization, cleanup and output behavior.

The original structural change retained schema 24. The current executable registry
implements real migrations through 31; later reserved versions remain inactive. Versioned migration
definitions share creation and upgrade paths while retaining backup-first atomic
upgrade and all recognized historical layout repairs. Historical migration tests
start from fixed historical SQL, not a newer schema with an expanding drop list.
Future reserved versions are not executable migrations. Activation requires the
complete preceding sequence; no empty migrations may skip an unimplemented change.
Unknown or newer databases, including unmerged variant schema-25 databases, remain
preserved and require recovery rather than being inferred from their version alone.

## Validation and rollout

The general API browser OAuth extension reserves migration 31 under issue #964
after real 26–30 and retains the original 29/30 owners. Inventory capability 6,
device connection method 4 and the additive flow/options/state fields belong to
the same allocation closure in the allocation ledger. Reservations
grant no provider registration, exchange or native capability. Deliver complete
common/Hugging Face, Gemini and Baseten feature PRs in that dependency order under
the [account OAuth contract](cmds-delidev-account-oauth-contract.md).

### Repository addition prerequisites

The approved remote-first extension reserves System 37
(`REMOTE_REPOSITORIES_V1`) and Worker 19 (`REMOTE_WORKSPACE_CLONE_V1`) in the
owning feature PR. It follows the Add repository implementation
in PR #1355 and preserves its immediate-clone/listing allocations. Reservations
alone grant no URL registration or managed workspace clone and add no migration.
The complete feature must compose repository saving, session/schedule admission,
Worker preparation, native ownership, recovery, snapshots, Fork, Sidechat and
deletion; a URL-only form without remote Worker preparation is incomplete.

The Add repository extension under issue #964 reserves System capabilities 31
(`REPOSITORY_CLONE_V1`) and 32 (`GITHUB_REPOSITORY_PICKER_V1`), Worker capability
18 (`REPOSITORY_CLONE_V1`), and the five new repository-list/clone message
declarations in the allocation ledger. Record these allocations in the owning
feature PR. Records alone grant no GitHub listing, Git authentication,
filesystem write, clone or registration capability. No migration is reserved:
the implementation uses the existing repository schema and durable job/receipt
boundary. PATs remain server-only GitHub API credentials; the original Worker
uses only its own Git/SSH authentication. Existing Local checkout deletion
ownership is unchanged.

Use the existing protocol, Go, frontend and CI-contract suites. No new GitHub
ruleset, required check or merge-queue policy is introduced. Record validation and
remaining acceptance limits in the owning pull request, issue and CI runs.

## Concrete source boundaries

The server's HTTP/authentication wrapper, Connect registration, system status, and
startup live in `server_http.go`, `server_routes.go`, `server_status.go`, and
`server_startup.go` under its owner directory. CLI dispatch uses one file per command
family; shared output framing still observes each command's generated request ID.
Settings integration files call the common `settings-test-fixture.ts` factory,
which owns independent temporary directories and child lifetimes per file.

## Migration sequence and replacement dependencies

`cmds/delidev-cli/internal/store/migration-reservations.json` reserves 25 for the
replacement of #1108, 26 for #1115, and 27 for #1117. Each originally used 25.
The Grok replacement for issue #1100 implements reserved version 25 with the
independent `grok-closed-input-v1` layout marker. Complete feature PRs implement real version 26 for original Claude/OpenCode
accounting and version 27 for metadata-only request diagnostics, followed by real
version 28 for service-native subscription identity and its account-scoped recovery notification constraint, preserving all original delivery claims with an independent layout marker. The executable registry ends
at 30 in the coordinated implementation: real OAuth 29 follows 28, and the
26 added hosted Provider defaults use real migration 30. Each has its own exact
private layout marker; no reservation itself activates support. Unmarked historical
version-25 files still require recovery without modification.
Claude accounting must compose with the Grok accounting schema and shared usage
meaning established by the preceding change. Request diagnostics follows both
implemented versions. If that product order changes, reconcile the ledger in the
owning feature PR; do not insert empty migrations to skip unfinished work.

Issue #1235 reserves migration 28 for service-native subscription identity and
legacy configuration retirement, after the real implementations of 26 and 27.
Its independent `SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1` allocation is
17. Both reservations reached main before dependent implementation. The real
retirement migration follows accounting and diagnostics; reservations alone never
activate runtime support. Migration provenance uses one original PR or owning
issue when no implementation PR exists yet, preserving that identity thereafter.
The [storage contract](cmds-delidev-storage-contract.md#subscription-retirement-issue-1235)
owns the reset boundary. Compose later account/native ownership and restore
changes with the subscription lifecycle work for issue #1095 rather than
replacing its unsettled-ownership and cleanup gates.

Issue #1146 reserves migration 29 for private OpenRouter OAuth attempt metadata,
after real migrations 26–28, together with inventory capability 5, inventory-entry
field 9 and the exclusively owned connection-method/attempt-state enums. The
[OAuth contract](cmds-delidev-account-oauth-contract.md) owns the complete
implemented lifecycle and outstanding real-provider/platform acceptance. Record
these allocations in the owning feature PR; allocation records alone introduce
no placeholder migration, active protobuf declaration, generated binding or
OAuth capability. Keep
the existing sequence and issue open until full implementation is accepted.

The storage suite covers every fixed historical schema and the real 29-to-30
Provider upgrade, along with the recognized
21/22 backup and 23 title variants. It compares upgraded DDL with a fresh database,
retains existing seeded record/backfill/rollback tests, and verifies that three
unidentified version-25 layouts return recovery-required without modifying bytes.

Independent files prevent incidental textual conflicts, not semantic dependencies.
Shared authentication, Worker permissions, account selection, title/usage attribution,
resource deletion and subagent/session lifecycle changes require explicit review
against the latest main contract. Regenerate bindings after composing schema changes;
never accept one PR's generated file or migration number merely to resolve a conflict.

The planned shared native-compaction boundary for #1093, #1202 and #1203 reserves
EntityKind 32, SystemCapability 15, WorkerCapability 5 and SessionChange field 9.
It uses the generic durable entity/job/receipt boundary and allocates no migration
in this prerequisite. No native profile or RPC is activated; see the
[compaction contract](cmds-delidev-compaction-contract.md).

## Legacy reflection compatibility

The legacy TypeScript `file_delidev_v1_delidev` export aggregates canonical split-file
messages, enums and services in the original order. Both direct enumeration and
registry construction from its descriptor proto retain the complete schema. The Go
`File_delidev_v1_delidev_proto` export likewise retains an aggregate reflection view
without registering duplicate global symbols. Physical descriptor ownership follows
the explicit split-file layout; canonical runtime type registration remains unique.
These views are generated from service descriptors at runtime so an independent
service addition does not rewrite a shared serialized descriptor blob.

The compatibility generator derives owned files from the compiled public imports
of `delidev.proto`, including newly added services. The relocation manifest remains
a historical order and breaking-check map; it is not the current service inventory.
Both aggregate views therefore include `NetworkService` and `SubscriptionService`
without adding their declarations to the relocation map.
The historical prefix contains 358 declarations: 303 messages, 36 enums and
19 services, including PullRequestFix, Terminal and Browser. Preserve their
per-kind positions from the intact map at
`54187b780d48e94a49763e87fc140f449869d9f4`. Go and TypeScript regression tests
share the fixed `protos/delidev/v1/contracttest/testdata/legacy-declaration-order.json`
snapshot; expected order must not come from the editable relocation map.
The fixed snapshot also pins the relocation names, kinds, files and complete
map order, including moves across declaration kinds.
WorkspaceStorage and other additive declarations follow this prefix and remain
discoverable through public imports, direct enumeration and reconstructed registries.
Issue #1084 activates its already reserved wire allocations without changing
that historical map. Generated service/query facades retain both services.

## Remaining-feature prerequisite reservations

The allocation ledger records the complete remaining-feature dependency closure
with independent implementation PRs. It retains every existing main assignment
and original owner; reservations grant no capability or native authority.
The closed WorkspaceStorageAction enum retains original PR #1121 ownership
alongside its existing System capability 11. Its eight values remain reserved
until the complete workspace snapshot/restore/cleanup implementation.
Accounting kinds/summary fields and metadata-only diagnostics precede independent
subscription-service attribution. Quota/reset-credit observations, encrypted Worker
bootstrap and native routes, Codex child settings, OpenCode foreground children,
independent Codex/OpenCode compaction and OpenCode General Chat Fork each retain
their separately negotiated System/Worker capabilities. Sidechat purpose and exact
original deletion observation, SSH setup and signed-update operations are reserved
under issue #964 without activating product endpoints or cleanup authority.

The immutable baseline includes the already active AccountingUnitKind,
SubscriptionAction, SessionContextCapability, ForkSessionRequest and
ListSessionDeletionWorkRequest declarations verified from main. Planned
declarations remain outside that baseline. Shared consumers are explicit and
new declaration provenance is independent of later field/member provenance.

Migration 30 belongs to issue #1148's 26 additional fixed hosted provider presets
and follows the real private OAuth migration 29. Versions 26–29 keep their original
accounting, request-diagnostics, subscription-retirement and OAuth ownership and
order. No executable migration, schema declaration, generated binding or runtime
advertisement is added by this prerequisite.

Delivery uses independent PRs merged in dependency order. A coherent feature PR
may compose related complete contracts and consecutive real migrations with their
shared allocation records; the delivery boundary is not one PR per
issue or migration. Retain every original migration owner and execution order.
Record these shared allocations in the owning feature PR; each feature PR
must include its complete business, authenticated RPC, CLI, client and desktop
boundary plus applicable validation. Preserve implementation branches and record
source-bound validation and unresolved native/account/platform acceptance in PRs
and CI. Reservation completion cannot close a feature issue.

Sidechat reference preparation privately owns sidechat-preparations/ under the workspace Manager. Original Fork job/parent/child and native metadata identities bind each bounded claim before manifest publication. Permanent deletion composes that ownership only after original process cleanup; the server receives IDs and digests, never filesystem authority.
## Pre-release compatibility reset

The owner-approved pre-release cleanup establishes database baseline 32 and
DeliDev protocol 2 in the complete feature PR. Record
`AttachWorkerRequest.protocol_version = 10` in the owning feature PR.
Reservations do not activate a database layout, RPC, Worker admission or reset.

The complete reset replaces migrations 1–31 with one current initialization
schema. Earlier databases and backups are unsupported and must be rejected
without automatic conversion, deletion or replacement. Preserve their files and
sidecars. Current backup recovery, deletion obligations, authorization, receipts
and native cleanup remain required. Existing allocation numbers and original
owners remain historical reservations and must never acquire another meaning.

Protocol 2 removes DeliDev historical imports, aggregate reflection facades,
synchronous CreateBackup, retired subscription configuration and older-client,
older-server and older-Worker fallback paths. Current configuration bundle 2 is
the only portable format. Agent Worker writes require at least one same-source
account and use atomic model/Agent saving. Saved Providers explicitly retain
enabled state; saved Repositories retain their credential-free remote URL.
Current API response accounting and independently typed native units remain
separate under NATIVE_UNITS_V1.

Keep current external harness/provider adapters, capability and authorization
checks, and current writer-produced execution/OAuth profiles. A lower version
number or a legacy name alone does not identify compatibility code.

This approved reset supersedes historical compatibility-preservation and
executable migration-retention requirements only when its complete feature is
implemented. Retain original allocation ownership and tool-generated outputs.
DeliDev pre-release breaking changes do not suppress other projects' Buf
breaking checks, numeric allocation validation, lint or freshness. Record source
revision, commands, results and unresolved limits in PRs and CI artifacts.

## Native Claude subscription allocation closure

PR #1612 established System capability 38, Worker capability 20 and the complete
Claude login-code/progress/native-identity closure on main at
`8a698d04d51f9db3a041b909edf85a7811f09ff3` before the dependent feature branch.
Existing Codex bundles, independent capabilities and real migrations through 31
retain their meanings. Main-first reservation history remains in the allocation
ledger; a reservation alone grants no native or browser authority. The feature
uses optional ownership JSON with no migration and leaves the inactive reset
reservation for baseline 32/protocol 2 unchanged. Native account/platform
acceptance must be recorded independently from fixture/build validation under
the subscription and desktop contracts.
Issue #964 reserves System capability 38, Worker capability 20 and the complete
Claude login-code/progress/native-identity closure in the protocol allocation
ledger. Record these allocations in the owning feature PR. Existing Codex
bundles, independent capabilities and
real migrations through 31 retain their meanings. Reservations alone grant no
native login, execution, browser dispatch or cleanup authority and add no
migration. The complete feature owns selected-Runner native authentication,
metadata-only server ownership, single-use original login input and joined
native lifecycle/execution cleanup under the subscription and desktop contracts.

## Desktop transport owner

`internal/desktopruntime` owns the private same-server execution locator, challenge proof, private bearer transport protection and fixed Local Worker relocation marker. This transport layer restores the original bearer before existing server authentication and changes no public Connect schema or stable credential format. CLI owns resident control framing/admission; server owns the directly bound listener and authenticated business shutdown; Rust owns the single original child and platform lifetime. Device/pairing/recovery files, Saved addresses, public Connect schemas and SQLite migration ownership remain unchanged.

## Inline Worker models and endpoint-only completion reservation

The feature PR records System 42, Worker 22 and the complete endpoint/model-identity/pricing closure in the allocation ledger. Compose full DB baseline 32 / protocol 2 reset with inline Worker schema 4 and current portable bundle 4. The prior atomic Model/Agent save and portable bundle 2 proposal are superseded only on complete activation. The 2026-10-07 owner amendment waives earlier DB retention and permits explicit DB/sidecar reset without conversion, retaining original native and protected-credential cleanup authority.

Follow the complete [catalog amendment](cmds-delidev-catalog-contract.md#inline-worker-models-and-endpoint-only-completion-reservation) and [current-only reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset). This reservation changes no runtime support or native/account acceptance.

## Owner-authorized #1748 allocation exception

For the fixed 2026-10-08 QA batch, the owner explicitly authorized #1748's complete
skill declarations and activation in one feature PR instead of a prior main
reservation PR. System 44, Worker 24 and session request field 4 retain exclusive
ownership. Regenerate shared outputs from reconciled sources. This exception does
not waive other domain contracts or authorize merging, native acceptance claims,
or unrelated allocation changes.
## Issue #1728 batch allocation exception

The owner explicitly permits System `SERVER_SUBSCRIPTION_QUOTA_V1 = 46` allocation, declaration, generated bindings and activation in the same complete feature PR for issue #1728. This narrow exception waives only the prior main reservation merge. Existing QUOTA and Refresh all RPCs retain their allocations; no new RPC, Worker capability, migration or native change is introduced. Follow the [server quota ownership contract](cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota--issue-1728). Capabilities 18, 19 and 30 retain independent ownership.

## Managed ChatGPT Sidechat allocation closure

Issue #1829 records System 47 and Worker 26 in its complete owning feature PR.
Existing Sidechat 27/16 and managed authentication Worker 3 retain their original
ownership. The additional declarations grant only the closed managed Sidechat
profile after activation and original native verification. Independent managed
Fork stays unsupported; no database migration or separate reservation PR is
required. Follow the [Sidechat contract](cmds-delidev-sidechat-contract.md#managed-chatgpt-sidechat--issue-1829).

## Project prompt history allocation closure

Issue #1828 records System `PROJECT_PROMPT_HISTORY_V1 = 48`, EntityKind `PROJECT_PROMPT_HISTORY = 35` and complete closed list/clear declarations in its owning feature PR. This follows the allocation workflow; no separate reservation merge is required. Preserve every original allocation. No Worker capability or SQLite migration is added. Allocation records alone activate no support.

## Server-owned reset-credit allocation closure

Issue #1809 records System `SERVER_SUBSCRIPTION_RESET_CREDITS_V1 = 49` with complete implementation in the owning feature PR. Reuse the owner/client observation and reconciliation RPCs. Preserve System 19/30/46, existing allocations and separate Worker ownership; add no Worker/entity allocation or database migration. Follow the [server-credit contract](cmds-delidev-subscription-contract.md#server-owned-chatgpt-reset-credits--issue-1809).

### Server quota V2 allocation closure

Issue #1854 owns System 50 `SERVER_SUBSCRIPTION_QUOTA_V2`, recorded with its complete feature implementation under the [allocation workflow](#allocation-workflow). It expands omitted-machine server quota only; retain original System 18/46/49 and all Worker allocations. No migration or new RPC is required. Real native/account/remote/platform acceptance remains owner-assigned and nonblocking for the authorized batch; fixtures/builds never substitute for that evidence.
