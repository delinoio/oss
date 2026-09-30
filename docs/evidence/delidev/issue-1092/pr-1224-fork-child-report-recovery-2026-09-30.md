# PR #1224: recover the first child report from its fork runtime

The Codex review at
<https://github.com/delinoio/oss/pull/1224#discussion_r4145335943>
identified different history IDs in the Worker checkpoint and the server's
first-child recovery request. Version-3 execution owns a fresh report identity
but resumes the independently created fork runtime.

The server now derives the recovery history ID from the original assignment's
`Fork.RuntimeID`, matching Worker checkpoint retention. Ordinary initial execution
and version-2 continuation retain their existing history selection.

Validation on macOS arm64:

- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/server -run '^TestSessionForkFirstExecutionRecoversLostReportFromForkRuntime$' -count=1 -timeout=3m` passed (2.924s).
- The regression uses public session/fork/queue/Resume and authenticated Worker
  APIs. It publishes the first child's successful terminal turn without its
  final report, replaces the disconnected Worker, requests recovery, verifies
  the original history/thread/turn/report identities, claims the recovery job
  and settles it. Success, cleanup, accepted-input accounting and paused dispatch
  remain independent and are verified after recovery.

Worker native observations/checkpoint digests are controlled fixture evidence,
not new installed-harness, hosted-inference or Windows execution evidence. The
historical evidence ledger remains unchanged.
