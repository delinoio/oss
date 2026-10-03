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

### Coordinated single-PR exception

An explicitly owner-approved single integration PR may establish missing numeric
reservations and implement their complete dependency closure in that same PR.
Record reservations before dependent source edits, retain the original allocation
owners, and regenerate bindings from the composed schema. This exception does
not authorize independent dependent branches before the reservations reach main.
Executable migrations must still implement every real predecessor in order;
the integration of 26 through 29 cannot use placeholders, reorder reservations,
or reinterpret existing layouts. Reservation-only commits are intermediate work,
not feature completion or capability evidence. Record final implementation and
validation coverage in the integration PR and CI, not repository evidence files.

Wholly new closed enums use explicit `newDeclaration: true` member reservations
under one original owner, including their zero UNSPECIFIED member. Keep these
planned declarations out of the immutable active baseline; the allocation check
validates their unique numbers and later active schema declarations without
requiring premature runtime support.

## Runtime and storage

Server lifecycle, authorization, route registration and status have separate owners.
CLI framing/authentication and command groups have separate files. They retain
existing order, authorization, cleanup and output behavior.

SQLite remains at schema 24 for this structural change. Versioned migration
definitions share creation and upgrade paths while retaining backup-first atomic
upgrade and all recognized historical layout repairs. Historical migration tests
start from fixed historical SQL, not a newer schema with an expanding drop list.
Future reserved versions are not executable migrations. Activation requires the
complete preceding sequence; no empty migrations may skip an unimplemented change.
Unknown or newer databases, including unmerged variant schema-25 databases, remain
preserved and require recovery rather than being inferred from their version alone.

## Validation and rollout

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
independent `grok-closed-input-v1` layout marker. The integrated implementation activates real version 26 for original Claude/OpenCode
accounting and version 27 for metadata-only request diagnostics, followed by real
version 28 for service-native subscription identity and its account-scoped recovery notification constraint, preserving all original delivery claims with an independent layout marker. The executable registry ends
at 28; reserved version 29 remains inactive until the OAuth implementation. Unmarked historical
version-25 files still require recovery without modification.
Claude accounting must compose with the Grok accounting schema and shared usage
meaning established by the preceding change. Request diagnostics follows both
implemented versions. If that product order changes, revise the ledger on main
before branching; do not insert empty migrations to skip unfinished work.

Issue #1235 reserves migration 28 for service-native subscription identity and
legacy configuration retirement, after the real implementations of 26 and 27.
Its independent `SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1` allocation is
17. Independent changes must establish both reservations on main before dependent
implementation. The approved single-PR composition exception above permits their
reservation and complete implementation together; reservations alone never
activate runtime support. Migration provenance uses one original PR or owning
issue when no implementation PR exists yet, preserving that identity thereafter.
The [storage contract](cmds-delidev-storage-contract.md#planned-subscription-retirement-issue-1235)
owns the reset boundary. Compose later account/native ownership and restore
changes with the subscription lifecycle work for issue #1095 rather than
replacing its unsettled-ownership and cleanup gates.

Issue #1146 reserves migration 29 for private OpenRouter OAuth attempt metadata,
after real migrations 26–28, together with inventory capability 5, inventory-entry
field 9 and the exclusively owned connection-method/attempt-state enums. The
[planned OAuth contract](cmds-delidev-account-oauth-contract.md) owns the complete
future lifecycle and acceptance. Establish these allocations on main before
dependent implementation; no placeholder migration, active protobuf declaration,
generated binding or OAuth capability is introduced by this prerequisite. Keep
the existing sequence and issue open until full implementation is accepted.

The storage suite covers every fixed schema from 1 through 28 and the recognized
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

Quota/reset-credit composition reserves System capabilities 18/19, Worker
capability 8, native lease actions 5/6, and their independently owned observation
messages before activation. New declaration reservations identify the original
declaration owner once; later explicitly numbered members retain their own
issue/PR provenance. The ledger baseline contains only declarations verified
from the fixed main source, including its original accounting and subscription
enums. New message fields are checked as strictly as new enum values.

Issue #1208 reserves independent System capability 23 and Worker capability 12 for OpenCode foreground child observation before activation. The existing resource/ordered-publication boundary carries the closed native task/child proof under its separate supported profile; these reservations alone grant no child publication, control, continuation or cleanup authority.

Issues #1202/#1203 reserve independent Codex/OpenCode compaction System capabilities 24/25, Worker capabilities 13/14 and SessionContext capabilities 3–6 before dependent activation. The shared existing capability 15/5 remains the common boundary, with each native profile independently negotiated. `SessionContextCapability` baseline values 0–2 are copied from verified main source `2fd96133`; their existing Claude meanings remain unchanged. These reservations grant no compaction send, checkpoint or cleanup authority and allocate no migration. The owner-approved single integrated PR exception applies; independent changes still establish reservations on main first.

Issue #1202 additionally reserves `GetUsageSummaryResponse.accepted_compactions_without_response = 14`, shared with #1203. It counts accepted native context actions independently of ordinary executions, preserving unavailable response coverage without a fabricated charge or measured zero. The integrated prerequisite is recorded before generation; independent PRs retain the main-first rule.
