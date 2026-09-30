# PR #1231 long-path fixture preparation

The 2026-09-30 maintenance pass inspected Windows Actions run `36722791421`,
Worker job `109912018425`, for prior head `461cb6aba`. The Worker package passed
(182.426 seconds), including restored-session deletion. Workspace failed
`TestSnapshotRestoredGitIdentityAtLongPrivatePath` at its initial `Prepare`
(`snapshot_restored_git_test.go:39`), before snapshot creation or restoration.
The reported Git operation was unavailable after 0.84 seconds. This differs
from the earlier restored-session deletion failure and does not establish a
restoration identity regression. The retained job log is
`/tmp/delidev-1079-461cb6-windows-worker.log`.

## Fixture change

Starting from `2c8adc716`, the temporary source explicitly enables
`core.longpaths` for initial preparation and then disables it before storage,
as before. All restored-store checks, source-offline behavior, subsequent
recovery/deletion checks and the final source-configuration assertion remain.
The fixture also retains structured preparation diagnostics in a private file,
emitting them only on failure. No production deadline or Git profile changes.

Git for Windows' `are_long_paths_enabled` implementation defaults to false at
[inspected source revision 49d759b6](https://github.com/git-for-windows/git/blob/49d759b698127791a5f3f2759c69b983846711dd/compat/mingw.c).
The explicit temporary setup removes reliance on ambient configuration. This
source inspection does not prove the cause of the original Windows failure.

## Executed checks and limits

The exact isolated command was:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout=10m \
  ./cmds/delidev-cli/internal/workspace \
  -run '^TestSnapshotRestoredGitIdentityAtLongPrivatePath$' -count=1
```

The first attempt failed (38.783 seconds package time), at initial preparation
on line 59, with `RecoveryRequired`: owned process descendants could not be
confirmed stopped. Structured diagnostics report a native child exiting with
that code after approximately 14.96 seconds. This is a different result from
the Windows job's unavailable Git operation. Its cause remains unproven.
The unchanged exact isolated rerun passed (41.166 seconds). Both outcomes are
retained in `/tmp/delidev-1079-third-longpath-setup.log` and
`/tmp/delidev-1079-third-longpath-setup-rerun.log`; the rerun does not erase the
initial failure or establish native Windows success.

Windows amd64 workspace test cross-compilation passed, including this fixture
and the four preceding ownership repairs
(`/tmp/delidev-1079-third-windows-final.log`). Compilation is not native runtime
acceptance. New CI on the final pushed head is still required.
