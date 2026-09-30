# DeliDev source ownership and compatibility

## Scope

The 2026-09-30 structural change preserves the main implementation while preparing
independent replacements for the 22 PRs in the immutable conflict-audit snapshot.
Those PRs' unmerged features are not activated by this refactor.

## Documentation

Project indexes contain ownership, domain links and cross-domain invariants.
Implementation details belong to domain contracts. Validation records belong to
independent files under `docs/evidence/delidev/issue-<number>/`; the historical ledger
is frozen with its original contents and anchors. Source-backed paragraphs and
rules moved in this change have SHA-256 entries in the relocation inventory.
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
to its original PR and a unique number. Existing main assignments are immutable.
Reservations do not advertise capability support or activate implementation.
New allocations must be established on main before dependent feature branches use
them. Existing shared message semantics still require explicit composition.

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
ruleset, required check or merge-queue policy is introduced. Merge the validated
structural PR first, confirm main, then close only still-open PRs in the snapshot.
Preserve their branches and linked issues. New PRs are not added to that set.

## Concrete source boundaries

The server's HTTP/authentication wrapper, Connect registration, system status, and
startup live in `server_http.go`, `server_routes.go`, `server_status.go`, and
`server_startup.go` under its owner directory. CLI dispatch uses one file per command
family; shared output framing still observes each command's generated request ID.
Settings integration files call the common `settings-test-fixture.ts` factory,
which owns independent temporary directories and child lifetimes per file.

The relocation inventory is an audit of this change, not a permanent prohibition
on editing live contracts. Set `DELIDEV_VERIFY_RELOCATION=1` to repeat the verbatim
migration audit; ordinary CI checks the destinations and inventory without freezing
future authorized policy edits.

## Migration sequence and replacement dependencies

`cmds/delidev-cli/internal/store/migration-reservations.json` reserves 25 for the
replacement of #1108, 26 for #1115, and 27 for #1117. Each originally used 25.
These are plans, not runtime support: the executable registry ends at 24.
Claude accounting must compose with the Grok accounting schema and shared usage
meaning established by the preceding change. Request diagnostics follows both
implemented versions. If that product order changes, revise the ledger on main
before branching; do not insert empty migrations to skip unfinished work.

The storage suite covers every fixed schema from 1 through 24 and the recognized
21/22 backup and 23 title variants. It compares upgraded DDL with a fresh database,
retains existing seeded record/backfill/rollback tests, and verifies that three
unidentified version-25 layouts return recovery-required without modifying bytes.

Independent files prevent incidental textual conflicts, not semantic dependencies.
Shared authentication, Worker permissions, account selection, title/usage attribution,
resource deletion and subagent/session lifecycle changes require explicit review
against the latest main contract. Regenerate bindings after composing schema changes;
never accept one PR's generated file or migration number merely to resolve a conflict.

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
Both aggregate views therefore include `SubscriptionService` without adding its
declarations to the relocation map.
