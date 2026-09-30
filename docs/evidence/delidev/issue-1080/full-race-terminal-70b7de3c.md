# Original complete race command: terminal failure

The original immutable restore implementation checkout at
`70b7de3c26e92baee6d716a6fed8139feb1cf6c9` finished its complete command on
2026-09-30 with **exit 1**:

`DELIDEV_NATIVE_USER_SERVICE_TEST=0 go test -race -p 2 -timeout 20m
./cmds/delidev-cli/...`

Its source remained unchanged throughout execution. Session 94932 is now closed;
log `/tmp/delidev-1080-race-v3-final.log` contains the complete output. Six package
results failed; sixteen passed. No race report appeared in the output, which does
not turn this failed run into race acceptance or prove unexecuted paths.

| Package | Observed failure | Package duration |
| --- | --- | --- |
| CLI | Session acceptance preparation deadline; isolated workspace-reader Unavailable also reproduced on original main | 468.084 s |
| Codex harness | Three Steer fixtures: delayed-history accept deadline, original-cwd native handshake and bounded continuation broken pipe | 676.831 s |
| Grok harness | Closure/mode fixture deadlines, instruction-change case and 20-minute package timeout during owned initialization | 1200.664 s |
| Server | Claude continuation/denial safe-storage failures and 20-minute timeout during lost failed-report recovery | 1200.825 s |
| Store | 20-minute timeout during frozen historical-schema convergence, at the v4 fixture | 1200.776 s |
| Workspace | Unborn diff returned recovery-required; current native worktree head match timed out | 1143.422 s |

The CLI line-215 failure was independently reproduced on the exact original main
baseline, as recorded in `validation-14330c62.md`. The isolated Claude denial
case passed on both original main (18.313 s) and the immutable restore checkout
(12.663 s). The other broad failures were not independently reproduced on main;
their precise cause is unresolved. Do not attribute every failure to host load,
claim all failures predate restore, increase deadlines to hide them, or infer
other-platform/native acceptance.

The subsequent schema-25 composition and two Codex repairs have independent
passing focused evidence. This terminal result supersedes only the earlier
records' pending-command status; preserve those historical records. There is no
long-running validation owner remaining from session 94932 and no complete local
race pass is claimed for the final repaired head.
