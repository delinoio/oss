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

- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /private/tmp/delidev-1080-windows.exe ./cmds/delidev-cli`
  and the equivalent `GOOS=linux GOARCH=arm64` command: both passed.
- Generated repository-owned `dist` directories were removed after validation.

## Initial broader race validation and base comparison

`go test -race -p 2 -timeout 20m ./cmds/delidev-cli/...` on the original source
completed with exit 1. The complete store package passed (559.392s), including its
restore tests. This is not a passing complete-suite result. The first publication
recorded an in-progress run; the outcomes below preserve that completed run.

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

Additional initial-run outcomes:

- Grok: twelve initialization/session/text/file/input fixtures failed and the
  package reached its 20-minute deadline (1201.997s).
- Native wire: four JSON-RPC/protocol/input fixtures failed (229.954s). A race
  joined `process.Start` logging through `slog` into a test-owned `bytes.Buffer`
  with `wire_test.go:186` reading that buffer. The unchanged-base command
  `go test -race -p 1 -timeout 5m ./cmds/delidev-cli/internal/harness/nativewire -run '^TestExplicitJSONRPCRejectsForeignEnvelopesAndBoundedDiagnostics$' -count=2`
  reproduced the race in both iterations (62.284s).
- OpenCode: `TestProbeOwnsAuthenticatedServerAndCleanup` failed (307.087s).
- Process: `TestOwnedDaemonizedDescendants` failed (95.890s).
- Server: five local-review submission/capacity fixtures failed and the package
  reached its 20-minute deadline (1202.226s).
- Worker: `TestStreamTerminationCancelsRunningOwnedWork` failed cleanup assertions
  (646.010s).
- Workspace did not build: standard-library/module build-cache files disappeared
  from the shared Go cache during compilation. No workspace test executed in that
  package. The task did not clean the user's cache.

The original seven CLI/discovery failures were rerun with
`go test -race -p 1 -timeout 15m` and an exact anchored test-name filter, both on
this source and in a separate `git archive` of the exact base revision above.
The filter was
`^(TestCLIPairWorkerAndInspectRealRepository|TestCLISessionAcceptanceQueueAndArchive|TestDiscoveryVerifiesClaudeWithoutGrantingExecution|TestDiscoveryVerifiesGrokWithoutExecution|TestDiscoveryVerifiesOpenCodeWithoutExecution|TestDiscoveryUsesIsolatedEnvironmentAndOwnedProcesses|TestExplicitPathFailuresNeverFallBack)$`
with `-count=1` for both package selections.
Discovery and pairing passed in both runs. The session acceptance fixture failed
on both source and unchanged base: source reached an unavailable workspace file
read (CLI package 84.990s); base timed out preparing a workspace (119.564s).
The complete initial failures remain part of this record despite successful
isolated reruns.

The five named Claude failures were also selected on unchanged base using
`go test -race -p 1 -timeout 10m ./cmds/delidev-cli/internal/harness/claude -run '^(TestProbeOwnsOnlyBoundedPrivateInitialization|TestStreamCorrelatesControlsAndNativeInputWithoutReplay|TestStreamPreservesLateAcknowledgmentsAndCanceledReads|TestStreamClaimsOneOriginalCallbackAndRetainsCancellation|TestStreamRejectsProtocolFailuresAndStopsOwnedScope)$' -count=1`.
That comparison failed `TestProbeOwnsOnlyBoundedPrivateInitialization`,
`TestStreamPreservesLateAcknowledgmentsAndCanceledReads` and
`TestStreamRejectsProtocolFailuresAndStopsOwnedScope` (package 494.677s).
This establishes pre-existing fixture failures for those checks, not a passing
broader suite or an attribution of every remaining failure. Native fixture
failure causes remain unresolved; no unrelated timeout or harness code was
changed to make this feature's checks appear green.

## Durability repairs and final source

- `6c390a5d267ece4445ea8aa49d54e46685bd9ac7` commits stopped lifecycle intent
  through a server-owned prepared pre-publication barrier. This addresses
  [the Codex finding](https://github.com/delinoio/oss/pull/1180#discussion_r4142010258).
  `TestRestoreStoppedIntentFailurePreservesOriginalDatabase` failed before the fix:
  startup observed `restored` despite stop-intent failure. After the fix it passes
  with `rolled-back`, unchanged live revision and unchanged source SHA-256.
  Seven actual process exits additionally retain the external epoch marker at all
  published boundaries. Exact replay preserves the new explicit running intent.
- `13f39e566d1b44a7f51a95e8730144b621181184` synchronizes the restore-root directory
  in its enclosing server scope before staging or SQLite closure. The unprivileged
  POSIX permission regression failed before the fix because directory-sync failure
  crossed the live publication boundary. After the fix it preserves current state,
  live revision and source bytes without accepting a journal/attempt. Windows and
  privileged execution skip only this POSIX permission fixture.
- Final focused race command:
  `GOCACHE=/private/tmp/delidev-1080-go-cache go test -race -p 2 -timeout 10m ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run 'Test.*(BackupRestore|Restore|ConcurrentBackup)' -count=1`:
  all packages passed (73.615s, 10.357s, 9.007s).
- Final `go vet ./cmds/delidev-cli/...`, Windows amd64/Linux arm64 cross-builds and
  all 46 client tests passed again with the task-specific Go cache after the
  shared-cache compilation failures. No cache belonging to another task was reset.

`GOCACHE=/private/tmp/delidev-1080-go-cache go test -race -p 4 -timeout 20m ./cmds/delidev-cli/...`
completed with exit 1 on final source
`13f39e566d1b44a7f51a95e8730144b621181184`. The complete store package passed
(363.665s), including every restore regression; the complete Worker package passed
(351.514s). Discovery, Claude, Codex, OpenCode, process, user-service and the other
completed core packages passed. The shared-cache workspace build failure did not
recur. These passes do not erase the initial failed-run evidence above.

Final complete-suite failures:

- CLI: `TestCLISessionAcceptanceQueueAndArchive` reported an unavailable workspace
  file reader during the creation comparison (package 159.015s), matching the
  earlier isolated source failure; the same test also failed on unchanged base.
- Grok: `TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval` and
  `TestOriginalFileReplyClaimsAndTerminalFaults` failed; the package reached its
  20-minute deadline in `TestOriginalTextStopSeparatesSubmissionTerminalAndCleanup`
  (1200.640s).
- Native wire: `TestExplicitJSONRPCProfileRequestsRepliesAndNotifications` and
  `TestExplicitJSONRPCRejectsForeignEnvelopesAndBoundedDiagnostics` failed, with the
  same test logging-buffer race independently reproduced twice on unchanged base
  (43.718s).
- Server: the package reached its 20-minute deadline in
  `TestOpenCodeFirstDispatchRefusalDoesNotConsumeRoutingOrInput/windows-global`
  (1203.873s). The focused restore server tests passed independently on this source.
- Workspace: `TestWorkspaceDiffUnbornAndBoundedResults` rejected an unborn
  working-tree comparison as not proving its accepted preparation (884.596s).

No restore test failed. The remaining fixture causes are unresolved and are not
claimed as repaired or all reproduced on base. No unrelated harness/process/
workspace source or timeout was changed. Final complete-suite validation remains
non-passing despite passing feature, client, protocol, vet and cross-build checks.

## Evidence limits

Fixture execution and cross-compilation do not establish actual supported-platform,
provider/account, installed-harness, credential-recovery, Worker workspace or
release/distribution acceptance. No real user credentials or production services
were accessed. No frontend or Rust implementation changed, so frontend and root
Rust suites were not selected. The remaining complete issue #964 requirements and
permanent-session-deletion/Worker-snapshot workflows remain independently tracked.
