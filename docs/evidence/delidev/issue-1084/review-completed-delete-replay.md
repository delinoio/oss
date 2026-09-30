# Completed deletion receipt replay

The 2026-09-30 19:44 UTC maintenance pass inspected PR #1216 at
`f6aed111aa71c22a29bf234191c5d87f20a5ccbb`. Codex thread
`PRRT_kwDORRAKg86nr2fp` identified cleanup intent recreation on an exact accepted
deletion retry after the original obligation had been retired.

The new authenticated Connect regression completes a credential-bearing deletion,
restarts the service, and retries the original owner receipt twice. It separately
injects unavailable vault enumeration and makes the private state directory
read-only. Both cases failed against the original implementation: the retry tried
protected cleanup or intent publication despite already completed deletion.

The handler now inspects and reconciles existing pending metadata but writes and
finishes a new cleanup obligation only for a fresh, uncompleted deletion. Replays
retain the original request ID, `replayed` and `deleted`, no resource and no new
intent. Changed revision input remains rejected. Existing pending cleanup,
revoked-actor recovery and failed-publication behavior are preserved.

Validation used private temporary SQLite state and an injected fixture vault:

- Before the fix: `GOMAXPROCS=2 go test -race -p 2
  ./cmds/delidev-cli/internal/server -run
  '^TestNetworkCompletedDeleteReplayDoesNotRecreateCleanup$' -count=1
  -timeout=3m` failed both subcases, package duration 4.521 seconds.
- After the fix: `GOMAXPROCS=2 go test -race -p 2
  ./cmds/delidev-cli/internal/server -run
  'TestNetwork(CompletedDeleteReplay|DeleteIntent|SaveReceipt)' -count=1
  -timeout=4m` passed, package duration 13.499 seconds.

This fixture evidence does not establish native OS vault or enterprise-network
acceptance. Combined mandatory validation is recorded independently for this pass.
