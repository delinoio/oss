# Relay cancellation callback ownership

PR #1216's Ubuntu Go check failed on
`f6aed111aa71c22a29bf234191c5d87f20a5ccbb` in Actions run `36766909973`, job
`110063388228`. The only failed package was `internal/apiproxy`, in
`TestProxyRevocationCancelsUpstreamAndRefusesLaterRequests`. The dependent
`CI Result` failed for the same root cause. Logs were retrieved through `gh`.

The old assertion conflated unauthorized downstream delivery with credential
reuse. Added bounded fixture diagnostics exposed three failures in 1,000
non-race repetitions: the later revoked request received EOF, while upstream
calls and key reads both stayed at one and the two leases were released.
One hundred race-enabled repetitions had passed before that diagnostic run;
that earlier sample did not disprove the CI failure.

The handler stopped its `context.AfterFunc` association without joining a callback
already running. That callback still owned the original response writer and
could apply an expired write deadline after `net/http` reset the connection for
another request. Installed Go's `context.AfterFunc` documentation explicitly
states that the stop function does not wait for completion; inspected
`net/http/server.go` resets the write deadline after `ServeHTTP` finishes.

A new controlled regression holds the original cancellation deadline callback
and observes whether its handler returns. Before the production fix, it failed:
the handler returned while the callback still owned the writer. The handler now
joins a started callback before returning, preserving upstream cancellation,
once-only key reads and lease release. The original downstream response may
still be aborted; later revoked requests must reject without new protected work.

Validation used loopback fixtures without real accounts or inference:

- Before: `GOMAXPROCS=2 go test -p 2
  ./cmds/delidev-cli/internal/apiproxy -run
  '^TestProxyRevocationCancelsUpstreamAndRefusesLaterRequests$' -count=1000
  -timeout=4m` failed three times, package duration 3.124 seconds.
- Before: `GOMAXPROCS=2 go test -race -p 2
  ./cmds/delidev-cli/internal/apiproxy -run
  '^TestProxyJoinsCancellationDeadlineBeforeReturning$' -count=1 -timeout=2m`
  failed, package duration 0.767 seconds.
- After: `GOMAXPROCS=2 go test -race -p 2
  ./cmds/delidev-cli/internal/apiproxy -run
  'TestProxy(JoinsCancellationDeadlineBeforeReturning|RevocationCancelsUpstreamAndRefusesLaterRequests)'
  -count=100 -timeout=3m` passed, package duration 8.078 seconds.
- After: the original revocation test repeated 1,000 times without race, with
  `-timeout=3m`, passed, package duration 3.142 seconds.

Fresh GitHub CI after publication remains independent of this local evidence.
