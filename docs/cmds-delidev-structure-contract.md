# DeliDev source ownership and compatibility

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

The Agent Worker wizard amendment under issue #964 reserves System capability
`AGENT_WORKER_WIZARD_V1 = 33`, list-only subscription-service field 4 on
`ListResourcesRequest`, field 7 on `SearchModelsRequest`, and the wholly new
`SaveAgentWorkerRequest` and `AgentWorkerModelSelection` declarations. The request
reserves mutation/document/model/schema-version fields 1–4; model selection
reserves canonical model ID/native ID/expected model revision fields 1–3.
Establish this closure on main before implementing the source-scoped wizard and
atomic model/Agent save. It changes no active schema, runtime capability or
database migration, and grants no native execution or account authority.

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
New allocations must be established on main before dependent feature branches use
them. Existing shared message semantics still require explicit composition.

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
implements real migrations through 28; later reserved versions remain inactive. Versioned migration
definitions share creation and upgrade paths while retaining backup-first atomic
upgrade and all recognized historical layout repairs. Historical migration tests
start from fixed historical SQL, not a newer schema with an expanding drop list.
Future reserved versions are not executable migrations. Activation requires the
complete preceding sequence; no empty migrations may skip an unimplemented change.
Unknown or newer databases, including unmerged variant schema-25 databases, remain
preserved and require recovery rather than being inferred from their version alone.

## Validation and rollout

### Repository addition prerequisites

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
implemented versions. If that product order changes, revise the ledger on main
before branching; do not insert empty migrations to skip unfinished work.

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
implemented lifecycle and outstanding real-provider/platform acceptance. Establish these allocations on main before
dependent implementation; no placeholder migration, active protobuf declaration,
generated binding or OAuth capability is introduced by this prerequisite. Keep
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
Issue #1084 activates its already reserved wire allocations without changing
that historical map. Generated service/query facades retain both services.

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

Migration 30 belongs to issue #1148's 26 additional fixed hosted provider presets
and follows the real private OAuth migration 29. Versions 26–29 keep their original
accounting, request-diagnostics, subscription-retirement and OAuth ownership and
order. No executable migration, schema declaration, generated binding or runtime
advertisement is added by this prerequisite.

Delivery uses independent PRs merged in dependency order. A coherent feature PR
may compose related complete contracts and consecutive real migrations after all
shared reservations have reached main; the delivery boundary is not one PR per
issue or migration. Retain every original migration owner and execution order. Establish these shared
reservations on main before composing their dependent branches; each feature PR
must include its complete business, authenticated RPC, CLI, client and desktop
boundary plus applicable validation. Preserve implementation branches and record
source-bound validation and unresolved native/account/platform acceptance in PRs
and CI. Reservation completion cannot close a feature issue.

Sidechat reference preparation privately owns sidechat-preparations/ under the workspace Manager. Original Fork job/parent/child and native metadata identities bind each bounded claim before manifest publication. Permanent deletion composes that ownership only after original process cleanup; the server receives IDs and digests, never filesystem authority.
