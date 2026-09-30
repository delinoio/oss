# Stable repaired aggregate race result

On 2026-09-30, ran `GOMAXPROCS=2 go test -race -p 2 -timeout=20m
./cmds/delidev-cli/...` against Go/protocol code revision
`957bc7bfffbe44ca8e18639f9743747147c20123`, including main `574c1a92c`.
Only independent evidence documents changed while the command was running; no
source merge or runtime/protocol edit crossed this run. The command finished with
exit status 1. This is a completed aggregate result, not a full-suite pass.

Four packages failed:

- CLI: `TestCLISessionAcceptanceQueueAndArchive` received workspace-reader
  `unavailable` during stale-review submission (78.96 s test; 220.718 s package).
  The isolated current and untouched-main results are retained separately in
  [the CLI baseline record](cli-main-baseline.md).
- Grok: `TestOriginalTextStopSeparatesSubmissionTerminalAndCleanup` failed its
  `stop-completion-race` case with an operation timeout (110.18 s parent test;
  1094.656 s package). This change does not modify the Grok implementation; that
  observation does not establish the timeout's cause.
- Server: `TestScheduleRPCLifecycleRetriesAndIndependentHistory` rejected the
  schedule defaults/timer (0.74 s test; 1116.512 s package). The unchanged test
  passed in isolation with `GOMAXPROCS=2 go test -race -p 1
  ./cmds/delidev-cli/internal/server -run
  '^TestScheduleRPCLifecycleRetriesAndIndependentHistory$' -count=1` (4.158 s).
  The isolated pass does not establish the aggregate failure's cause.
- Workspace: `TestWorkspaceDiffUnbornAndBoundedResults` reported
  `recovery_required` because the Worker workspace result did not prove the
  accepted preparation (24.86 s test; 866.195 s package). This result has not been
  isolated or compared against untouched main.

All other tested packages passed, including Claude (128.737 s), Codex (120.099 s),
OpenCode (56.630 s), store (637.576 s) and Worker (779.457 s). The store result
includes historical schema compatibility and current schema 25; it supersedes
the earlier default-ten-minute attempt's inability to finish without changing
that historical observation. Focused compaction regressions and the four pinned
native public scenarios passed as recorded in their independent evidence files.

No assertion, production deadline, fixture deadline or recovery requirement was
weakened to obtain a pass. Broader aggregate failures remain visible; native
compaction acceptance and passing focused checks do not make them a passing full
suite.
