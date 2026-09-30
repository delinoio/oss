# Issue #1095: lifecycle lease contention

Review thread `PRRT_kwDORRAKg86nt1O6` identified a lifecycle Take racing an
execution grant. The definite busy refusal previously escaped the lifecycle
goroutine and closed the shared Worker connection, fencing unrelated ownership.

Lifecycle actions now use the same bounded, cancellable wait as execution for
the server's typed `ResourceExhausted` refusal. Every retry preserves the exact
protected request, original durable claim and selected account. Unknown delivery
still closes the lane without another Take or an invented Finish. A structured
waiting event records only opaque identities and the closed action.

Real loopback Connect fixtures select login, refresh and logout. Each receives
one typed busy response followed by protected delivery, retains one journal,
compares both Take requests byte-for-byte through protobuf equality and proves
the lane remains open after an acknowledged operation failure. Caller
cancellation then joins it. Native installation is deliberately invalid, so no
native process or account authentication is performed.

Executed on macOS arm64 with synthetic temporary state:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/worker -run '^TestManagedLifecycle' -count 1
# Original execution-only busy guard: all three lifecycle cases failed, exit 1.
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/worker \
  -run 'ManagedLifecycle|ManagedSubscription.*Lane' -count 1
# Fixed source: passed, 5.390s, including unknown-delivery and lane-loss controls.
```

Initial fixture attempts lacked an import and then used an untyped Connect
error, which correctly classified as unknown server delivery rather than a
definite busy refusal. Those attempts failed. The final fixture uses the actual
server ErrorDetail envelope; no untyped transport refusal gained retry authority.
The original busy predicate was restored for the final failing regression run
before the repair was reapplied.

These fixtures do not establish real-account, installed-native, Windows or
complete-suite acceptance. Required complete validation and the Windows CI
Workspace failure are recorded separately; earlier failures remain preserved.
