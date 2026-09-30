# PR #1224: reject invalid preparation before child process creation

The Codex review at
<https://github.com/delinoio/oss/pull/1224#discussion_r4145945780>
identified that fork request derivation created the child's process index before
checking later repositories. A definite rejection could leave an unpublished
child process directory with no workspace or deletion inventory.

Derivation now assembles and validates the complete repository/request shape,
including unborn-repository eligibility, before creating a child process index
or launching native Git. A second phase reads actual source HEADs and fills
canonical commit references. Native read/ownership failures retain the existing
`RecoveryRequired` classification and process evidence; no uncertain scope is
removed by this change.

Validation on macOS arm64:

- Before the fix, `GOMAXPROCS=2 go test -p=1 ./cmds/delidev-cli/internal/workspace -run '^TestForkPreparationRejectsAllRepositoriesBeforeCreatingChildScope$' -count=1 -timeout=5m`
  failed all six subtests (129.787s): Worktree and Local requests with an unborn
  second repository, duplicate repository IDs, or a missing primary each left
  the child process directory behind.
- After the fix, `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/workspace -run 'Fork' -count=1 -timeout=10m`
  passed (435.047s). The regression repeats each rejection with fresh child IDs,
  requires process/workspace absence, and checks the stored source manifest,
  staged/unstaged Git state and file bytes remain unchanged. A separate native
  second-repository HEAD failure retains process evidence and recovery status.
  Existing dirty-copy, snapshot cleanup, Local lifetime and parent-deletion
  fork regressions also pass.
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOMAXPROCS=2 go test -p=1 -c -o /tmp/delidev-1224-1452-workspace-windows.exe ./cmds/delidev-cli/internal/workspace`
  passed. This compiles the Windows tests; it does not execute them.

## Combined authorization and preparation verification

- `DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex GOMAXPROCS=2 go test -p=1 ./cmds/delidev-cli/internal/cli -run '^TestManualNativeCLISessionFork$' -count=1 -timeout=15m`
  passed (94.699s) with installed Codex `0.151.0`, temporary private state and a
  scripted keyless loopback provider. This covers General Chat/two-repository
  forks and child continuation across process replacement without existing user
  credentials or hosted-account inference.
- Root `GOMAXPROCS=2 go vet -p=1 ./cmds/delidev-cli/...` passed.
- The server fork race suite separately passed (33.777s), as recorded in
  [authorization evidence](pr-1224-fork-observation-authorization-2026-10-01.md).
- `git diff --check` passed. No frontend, protocol or Rust source changed, and
  no repository-owned generated `dist` directories remain.

This evidence does not claim an unfiltered local Go-suite pass, a new packaged
desktop run or fresh pushed-head CI/review acceptance. Maintenance checks those
remote outcomes on a later heartbeat. The historical evidence ledger is unchanged.
