# Issue #1083: retained PR activity replacement

## Revision and scope

The replacement starts from freshly fetched main
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` (2026-09-30). Issue #1083 is open.
Its earlier PR #1118 was closed without merging after the structural reset; its
retained head `e8ca2ab10a5f179ff4f8912d6e70681c0f5b95a4` supplied the source-backed
implementation. This change adapts that implementation to main's scoped owners,
service-specific schemas and independent evidence policy. It does not copy or
modify the frozen historical evidence ledger or the conflict-reset inventories.

The wire additions activate #1118's pre-established allocations: ActivityKind
7–10, ActivityEntry.pull_request 15 and ListActivityResponse.capabilities 3.
Buf and the normal compatibility pass regenerate service-owned Go/TypeScript
bindings. SQLite remains at schema 24; no migration or inferred historical
backfill is introduced.

## Behavior and acceptance coverage

Problem observations, exact-version local dismissals and semantic attempt-state
transitions retain independent immutable UUID-v7 identities, original source
revisions, actor/request/time and navigation references in their source transaction.
Receipt replay, aliases and repeated unchanged uncertainty do not append another
transition. Source publication failure rolls back activity too.

Failed, uncertain, successful and positive pre-native not-started attempts have
separate typed outcomes. Only a dedicated immutable verification record can project
verified handling; a successful attempt or provider state cannot fabricate it.
The private proof-retention boundary is independently bounded and deduplicates
repeated proof publication. No production verifier or new execution control calls
that boundary in this change.

Activity reads retain current owner/client authorization and bounded signed
pagination, include Archive, preserve original navigation across repository
renames, and remove session-owned attempt activity including pre-binding
reservations when the session is deleted. Shared PR evidence remains independently
owned. Complete metadata pages obey both protobuf and JSON byte bounds. No comment
body, PR title, credential, native content or private proof commitment enters the
activity response, and reading changes neither Inbox read state nor PR handling.

Desktop inspection reads the retained original source and set only on explicit
request. It validates original scope/version references, labels recorded versus
current revisions, and disposes on close or inactivity. Returning to Activity
requires another explicit inspection. Business authority remains in Go, with the
same typed projection exposed through Connect, generated clients and CLI.

## Executed verification

- `git lfs pull`: passed; required assets were hydrated before consumption.
- `git lfs fsck`: passed.
- `pnpm install --frozen-lockfile`: passed, including linked-worktree hook setup.
- `pnpm proto:generate`: passed after retrying outside the restricted network
  sandbox; the sandboxed attempt could not reach the Go module proxy.
- `pnpm proto:lint` and `pnpm proto:breaking`: passed against current main.
- Generated-client build and desktop typecheck: passed.
- Focused desktop activity tests: all four passed, including inspection disposal,
  distinct outcomes, dedicated proof disclosure and foreign-source rejection.
- `pnpm ci:contracts`: all 102 tests passed.
- `pnpm ci:workflows`: passed.
- `go test ./protos/delidev/...`: allocation/reflection compatibility passed.
- The initial race-enabled `PRActivity|Activity|PRRemediation` selection passed
  store and CLI packages but failed the existing
  `TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof` server
  fixture: its matches/different/pause/unlink cases timed out during workspace
  preparation. This is a recorded failure, not a successful broad test result.
- The required default `pnpm test` invocation reached typecheck and Vitest, then
  reported timeouts in unchanged tray, Settings and App tests and 120-second
  Settings integration hooks. That overloaded default-concurrency run was
  interrupted with exit 130. A full one-worker validation rerun preserves all
  assertions, hooks and timeouts; its result is recorded after completion.

Additional required validation is recorded below after its completion.

## Limits

Temporary SQLite, loopback RPC/CLI and renderer fixtures use only test-owned
state and controlled provider/native observations. They do not establish real
provider-account remediation, independent production verification, actual native
side effects or multi-platform release acceptance. Permanent managed-backup and
native-session erasure remains part of the separate coordinated deletion work.
