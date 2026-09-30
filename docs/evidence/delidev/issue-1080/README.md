# Issue #1080: managed database restore

## Revision and provenance

Replacement source: `2c409fa424e437e842827c4955f4aa59cb6c5659`, based on
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` from freshly fetched main.
The closed, unmerged [PR #1114](https://github.com/delinoio/oss/pull/1114),
head `df70602811045b6b1a46327504f96223408bac59`, supplied the preserved implementation
and regression fixtures. Its historical validation remains on that PR and in Git;
it is not substituted for the checks below.

The replacement follows the structural reset contract: use service-owned
`system.proto`, reserved `MANAGED_BACKUP_RESTORE_V1 = 7`, existing schema 24 without
an executable migration, regenerated Go/TypeScript/Connect Query bindings and
issue-local evidence. It does not merge the old branch or modify the frozen shared
implementation ledger.

## Implemented boundary

- Owner/client RPC and CLI restore bind the original image metadata, SHA-256,
  present live revision and UUID-v7 request. Workers and revoked clients gain no
  restore authority. Exact retries observe their original external receipt.
- Exclusive settled ownership excludes current claimed/uncertain execution,
  unresolved forwarding cleanup, credential mutations and native user-service
  control. Restore never terminates work to manufacture eligibility.
- Source-preserving private staging validates SQLite integrity, schema and server
  identity, captures committed WAL data in a synchronized current safety image,
  and preserves live client authorization, tombstones and external backup-removal
  obligations before publication.
- Restored sessions remain paused/recovery-required, historical nonterminal jobs
  are canceled, schedules are disabled, accounts/integrations are disconnected,
  Worker credentials/assignments/grants are quarantined, and old receipts cannot
  replay native effects. Protected credential stores and Worker files are not
  restored.
- Fingerprint-bound external journals precede atomic same-volume publication.
  Publication ends the original server epoch. Startup reconciles the original or
  replacement outcome before SQLite opens; uncertain evidence is preserved.

## Executed validation

Executed on macOS arm64 with Go 1.26.8, Node.js 24.11.0 and pnpm 10.26.2 on
2026-09-30. All fixture state, credentials and subprocesses are test-owned.

- `go test -race -p 2 -timeout 20m ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run 'Test.*(BackupRestore|Restore|ConcurrentBackup)' -count=1`: passed all three packages (290.970s, 43.876s and 50.465s respectively).
  Includes seven actual process-exit checkpoints plus fault injection; committed
  WAL restoration; corrupt/newer/foreign/replaced images; concurrent requests;
  revoked/current clients; deleted sessions and children; external backup deletion;
  paused sessions/schedules; untouched protected bytes; private older-schema
  migration; changed recovery evidence; manual rollback refusal and exact receipts.
- `go vet ./cmds/delidev-cli/...`: passed.
- `pnpm --filter @delinoio/delidev-api-client test`: all 46 tests in five files
  passed on the final source, including the real temporary Go server and legacy
  import/reflection checks. The two new restore tests verify canonical/legacy
  generated-query identity, capability 7, distinct publication/startup states,
  omitted versus explicit-zero live revisions and exact bigint round trips.
  Client lint/typecheck and build passed; lint was run again after adding the
  feature tests.
- `pnpm proto:check`: passed formatting/lint, breaking compatibility and exact
  forced regeneration/freshness against committed generated sources.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`:
  all six checks passed, including main-established numeric reservations and FILE
  semantic compatibility.
- Locked root installation and repository pre-commit hooks passed. Required admin
  and ach embedded assets were generated explicitly; the Go-embedded LFS asset was
  hydrated before the repository-wide Go formatting hook.

- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o <temporary-output> ./cmds/delidev-cli`
  and the equivalent `GOOS=linux GOARCH=arm64` command: both passed.
- Generated repository-owned `dist` directories were removed after validation.

## Broader race validation and base comparison

`go test -race -p 2 -timeout 20m ./cmds/delidev-cli/...` was executed and has
already reported failures; it is not a passing complete-suite result. At initial
publication, later packages remain running. Their final outcomes will be recorded
in this same issue-local record. No data race has been reported so far.

Reported package failures:

- CLI: `TestCLIPairWorkerAndInspectRealRepository` and
  `TestCLISessionAcceptanceQueueAndArchive` timed out in Worker repository/workspace
  operations (package 543.311s).
- Discovery: the Claude, Grok and OpenCode version-verification fixtures,
  `TestDiscoveryUsesIsolatedEnvironmentAndOwnedProcesses` and
  `TestExplicitPathFailuresNeverFallBack` failed their bounded native probes
  (package 368.536s).
- Claude: `TestProbeOwnsOnlyBoundedPrivateInitialization`,
  `TestStreamCorrelatesControlsAndNativeInputWithoutReplay`,
  `TestStreamPreservesLateAcknowledgmentsAndCanceledReads`,
  `TestStreamClaimsOneOriginalCallbackAndRetainsCancellation` and
  `TestStreamRejectsProtocolFailuresAndStopsOwnedScope` failed private
  initialization/stream fixtures (package 837.245s).
- Codex: thirteen steer/thread/turn/workspace-root fixtures failed native handshake
  or control assertions, and the package reached its 20-minute deadline during
  `TestMultipleWorkspaceRootsRejectIncompleteOrBroadenedNativeAuthority`
  (package 1202.850s). The harness directory is unchanged from the base.

The original seven CLI/discovery failures were rerun with
`go test -race -p 1 -timeout 15m` and an exact anchored test-name filter, both on
this source and in a separate `git archive` of the exact base revision above.
Discovery and pairing passed in both runs. The session acceptance fixture failed
on both source and unchanged base: source reached an unavailable workspace file
read (CLI package 84.990s); base timed out preparing a workspace (119.564s).
The complete initial failures remain part of this record despite successful
isolated reruns.

The five named Claude failures were also selected with an exact anchored filter
on unchanged base using `go test -race -p 1 -timeout 10m
./cmds/delidev-cli/internal/harness/claude -run '<five exact names>' -count=1`.
That comparison failed `TestProbeOwnsOnlyBoundedPrivateInitialization`,
`TestStreamPreservesLateAcknowledgmentsAndCanceledReads` and
`TestStreamRejectsProtocolFailuresAndStopsOwnedScope` (package 494.677s).
This establishes pre-existing fixture failures for those checks, not a passing
broader suite or an attribution of every remaining failure. Native fixture
failure causes remain unresolved; no unrelated timeout or harness code was
changed to make this feature's checks appear green.

## Evidence limits

Fixture execution and cross-compilation do not establish actual supported-platform,
provider/account, installed-harness, credential-recovery, Worker workspace or
release/distribution acceptance. No real user credentials or production services
were accessed. No frontend or Rust implementation changed, so frontend and root
Rust suites were not selected. The remaining complete issue #964 requirements and
permanent-session-deletion/Worker-snapshot workflows remain independently tracked.
