# DeliDev source ownership and compatibility

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

## Grok Build subscription reservations

The [Grok subscription contract](cmds-delidev-grok-subscription-contract.md)
reserves System login 39, System execution 40, Worker managed execution 21 and
progress diagnostic field 10 under issue #964. The ledger also owns the new
GrokDiagnostic fields 1–7 and closed GrokDiagnosticPhase values 0–11. Establish
the complete closure on main before active schemas, generated bindings or runtime
support. Codex field 7 and existing System 30/35/36/37 and Worker 19 ownership
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
catalog/account pages. Portable configuration version 2 maps the complete graph.
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
provider.proto; generated bindings use the normal Buf pipeline.
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
ledger. Establish this reservation-only closure on main before dependent
implementation. It grants no token inspection, pre-profile form, credential
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

Keep package `delidev.v1`, canonical Go import paths and allocation history.
Service-specific schemas own their exclusive request/response types. Shared types
and their dependency closure have one common owner. Pre-release obsolete APIs and
historical import paths may be removed under the approved reset. Generated code
is never resolved by choosing a merge side; regenerate from reconciled schemas.

The numeric allocation ledger binds each new enum member or existing-message field
to its original PR, or its owning issue when no implementation PR exists yet, and
a unique number. Shared consumers are recorded explicitly. Existing main
assignments are immutable.
Reservations do not advertise capability support or activate implementation.
New allocations must be established on main before dependent feature branches use
them. Existing shared message semantics still require explicit composition.

Issue #1203's shared compaction RPC closure reserves
`CompactSessionRequest.expected_execution_id = 2` and
`CompactSessionResponse.session = 4`, with #1093/#1202 as shared consumers.
Record PR #1221's already-active request/response assignments in the immutable
baseline without renumbering them. Establish the additive reservations on main
before implementing exact predecessor admission and joined current-session/job
responses. The existing response request ID remains the original action ID.
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

Schema 32 directly initializes the complete current functional layout. Startup,
backup inspection and restore accept only that schema, preserving unsupported
original files and sidecars. Executable migrations, recognized old-layout repairs
and backfills are removed. `migration-reservations.json` retains historical
numbers and provenance, including main-established baseline 32; no number is
reused and future reservations do not become executable migrations.

## Validation and rollout

The general API browser OAuth extension reserves migration 31 under issue #964
after real 26–30 and retains the original 29/30 owners. Inventory capability 6,
device connection method 4 and the additive flow/options/state fields belong to
the same main-first reservation closure in the allocation ledger. Reservations
grant no provider registration, exchange or native capability. Deliver complete
common/Hugging Face, Gemini and Baseten feature PRs in that dependency order under
the [account OAuth contract](cmds-delidev-account-oauth-contract.md).

### Repository addition prerequisites

The approved remote-first extension reserves System 37
(`REMOTE_REPOSITORIES_V1`) and Worker 19 (`REMOTE_WORKSPACE_CLONE_V1`) on main
before dependent implementation. It follows the Add repository implementation
in PR #1355 and preserves its immediate-clone/listing allocations. Reservations
alone grant no URL registration or managed workspace clone and add no migration.
The complete feature must compose repository saving, session/schedule admission,
Worker preparation, native ownership, recovery, snapshots, Fork, Sidechat and
deletion; a URL-only form without remote Worker preparation is incomplete.

The Add repository extension under issue #964 reserves System capabilities 31
(`REPOSITORY_CLONE_V1`) and 32 (`GITHUB_REPOSITORY_PICKER_V1`), Worker capability
18 (`REPOSITORY_CLONE_V1`), and the five new repository-list/clone message
declarations in the allocation ledger. Establish these reservations on main
before implementation. Reservations grant no GitHub listing, Git authentication,
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

## Historical migration sequence and replacement dependencies

`cmds/delidev-cli/internal/store/migration-reservations.json` retains the immutable
allocation history through 31. Versions 25, 26 and 27 originally collided;
the main-established ledger separated Grok accounting (#1100, replacing #1108),
Claude/OpenCode accounting (#1115) and request diagnostics (#1117). Version 28
belonged to service-native subscription identity and configuration retirement
(#1235), 29 to private OpenRouter OAuth attempts (#1146), 30 to additional hosted
Provider defaults (#1148), and 31 to general API OAuth (#964).

Those implementations originally followed the consecutive migration order.
Schema 32 now creates their current functional structures directly, including
account-scoped recovery notifications and all current hosted Provider defaults.
Retirement-only storage and executable migrations are removed. Earlier and
unmarked historical layouts are preserved and rejected without upgrade.
Existing capability/field numbers, layout markers and original allocation owners
remain reserved; no number can be reused.

Main-first reservation still precedes dependent implementation. A reservation
alone grants no capability or runtime authority. Preserve the complete current
OAuth, subscription, accounting and native cleanup boundaries, and keep outstanding
real-provider/platform acceptance visible under their owning contracts.

The storage suite creates and restarts current databases, preserves current
backup/recovery and native cleanup regressions, and rejects frozen historical
layouts and every earlier schema without modifying original bytes or sidecars.

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

## Service-owned generated bindings

Buf generates Go and TypeScript bindings directly from service-specific schemas.
Internal consumers import their owning generated file. The client package common
entry point exports those files and service-specific Connect Query namespaces.
The historical forwarding file, relocation map, facade generator and aggregate
reflection shim are removed. Canonical descriptors remain registered once by
Buf-generated bindings.

DeliDev pre-release breaking comparisons are excluded. All other projects retain
FILE comparisons. DeliDev formatting, lint, allocation validation and generated
freshness remain required; generation and Turbo have no compatibility pass.

## Remaining-feature prerequisite reservations

The allocation ledger reserves the complete remaining-feature dependency closure
before independent implementation PRs. It retains every existing main assignment
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

Historical migration allocations 26–31 retain their original accounting,
diagnostic, subscription, Provider and OAuth owners under the schema-32 reset.
They provide no executable upgrade path.

Delivery uses independent complete PRs merged in dependency order after shared
reservations reach main. A feature PR includes its complete business,
authenticated RPC, CLI, client and desktop boundary plus applicable validation.
Record source-bound validation and unresolved native/account/platform acceptance
in PRs and CI. Reservation completion cannot close a feature issue.

Sidechat reference preparation privately owns sidechat-preparations/ under the workspace Manager. Original Fork job/parent/child and native metadata identities bind each bounded claim before manifest publication. Permanent deletion composes that ownership only after original process cleanup; the server receives IDs and digests, never filesystem authority.
## Pre-release compatibility reset

The owner-approved pre-release cleanup establishes database baseline 32 and
DeliDev protocol 2 before implementation. PR #1609 established
`AttachWorkerRequest.protocol_version = 10` on main before implementation.
The field now requires protocol 2 before Worker connection mutation. Reservations
alone never grant a database layout, RPC, Worker admission or reset.

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
implemented. Retain main-first allocation ownership and tool-generated outputs.
DeliDev pre-release breaking changes do not suppress other projects' Buf
breaking checks, numeric allocation validation, lint or freshness. Record source
revision, commands, results and unresolved limits in PRs and CI artifacts.

## Native Claude subscription reservations

Issue #964 reserves System capability 38, Worker capability 20 and the complete
Claude login-code/progress/native-identity closure in the protocol allocation
ledger. Establish these reservations on main before dependent feature branches
activate the declarations. Existing Codex bundles, independent capabilities and
real migrations through 31 retain their meanings. Reservations alone grant no
native login, execution, browser dispatch or cleanup authority and add no
migration. The complete feature owns selected-Runner native authentication,
metadata-only server ownership, single-use original login input and joined
native lifecycle/execution cleanup under the subscription and desktop contracts.
