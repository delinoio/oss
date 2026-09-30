# PR #1224: future Worker observation gate

Codex review thread `PRRT_kwDORRAKg86ng0ZE` identified that first fork-child
dispatch lacked ordinary dispatch's future-heartbeat rejection. A backward host
clock move could therefore make a stale Worker observation look current.

The first-child gate now rejects observations more than one second in the
future, matching ordinary initial and continuation dispatch. Rejection occurs
before current account selection, input claiming, execution publication or an
empty Resume becoming ready. Existing stale-observation and exact instance
validation remain intact.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server -run
'^TestSessionForkRejectsFutureWorkerBeforeChildDispatch$' -count=1 -timeout=3m`
passed in 4.988 seconds. The test publishes a child through the original fork
job, then simulates a future observation in the same transaction as dispatch
so fixture heartbeat timing cannot erase it. Both empty and queued Resume
preserve the original child bytes/revision and execution-job inventory; queued
input retains its delivery state and absent native/execution claim.
