# PR #1224: bind the first child publication to native fork ownership

The Codex review at
<https://github.com/delinoio/oss/pull/1224#discussion_r4145335955>
identified missing first-child native identity comparisons. Ordinary continuation
checks did not cover version-3 fork assignments, and the child's turn index did
not contain its inherited source turn.

The server now rejects a first-child `ThreadBound` event for another native
thread and rejects `InputAccepted` reuse of the inherited last source turn.
Both checks precede progress or input-accounting mutation. Existing configuration,
assignment, publication sequence and native-identity validation remains required.

Validation on macOS arm64:

- Before the guards, `GOMAXPROCS=2 go test -p=1 ./cmds/delidev-cli/internal/server -run '^TestSessionForkExecutionRejectsForeignThreadAndInheritedTurnAtomically$' -count=1 -timeout=3m`
  failed both subtests because each invalid event was accepted.
- After the guards, `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/server -run '^TestSessionFork(ExecutionRejectsForeignThreadAndInheritedTurnAtomically|FirstExecutionRecoversLostReportFromForkRuntime)$' -count=1 -timeout=3m`
  passed (9.858s).
- Each invalid publication is sent through the authenticated public Worker RPC.
  The regression compares exact bytes and revisions of the child, its input/job,
  and source after rejection, then completes a valid fresh turn on the assigned
  native thread. The lost-report recovery regression also passes with these
  guards enabled.

Native IDs and checkpoint digests are controlled Worker fixture observations;
this evidence does not claim a new installed Windows/Linux or packaged desktop
run. The historical evidence ledger remains unchanged.

## Final verification with all three review repairs

- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker -run 'Fork|Continuation|ExecutionRecovery|ExecutionPublication|ExecutionCheckpoint|RecoveryJob' -count=1 -timeout=15m`
  passed: server 194.489s and Worker 19.535s.
- `DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex GOMAXPROCS=2 go test -p=1 ./cmds/delidev-cli/internal/cli -run '^TestManualNativeCLI(SessionFork|AccountSwitch)$' -count=1 -timeout=15m`
  passed (147.331s) with installed Codex 0.151.0 and temporary keyless loopback
  fixtures, including General Chat/two-repository forks and child continuation
  across process replacement. This is separate from the controlled server
  lost-report regression above.
- Root `GOMAXPROCS=2 go vet -p=1 ./cmds/delidev-cli/...` passed again.
- Windows amd64 Worker tests compiled again after the runtime-retention fix:
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOMAXPROCS=2 go test -p=1 -c -o /tmp/delidev-1224-1338-worker-windows-final.exe ./cmds/delidev-cli/internal/worker`.
  Cross-compilation does not execute Windows tests; fresh CI remains required.
- The merge's 1,279-test frontend invocation, 44 client tests, protocol freshness
  and contract checks remain recorded in the independent
  [main reconciliation](pr-1224-account-switch-main-merge-2026-09-30.md) and
  [Windows fixture](pr-1224-windows-cleanup-fixture-2026-09-30.md) evidence.
  These review repairs change Go ownership guards without changing schema or
  frontend source. No Rust source changed.
- `git diff --check` passed and no repository-owned generated `dist` directories
  remain. This does not claim an unfiltered Go-suite pass or pushed-head CI/review
  acceptance. Maintenance assesses those on the next scheduled heartbeat.
