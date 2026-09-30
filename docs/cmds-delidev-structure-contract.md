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
