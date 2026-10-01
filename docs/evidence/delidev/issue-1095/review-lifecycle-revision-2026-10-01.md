# Issue #1095: original lifecycle observation across lease cleanup

This follows the Worker busy-retry repair for review thread
`PRRT_kwDORRAKg86nt1O6`. The original server at
`63fecacdd12d07dacd13c24dec2b00331af41298` compared lifecycle Take with the
current account revision before checking exclusive ownership. Its execution
Finish necessarily advances that revision. Retrying the identical lifecycle
request after a definite busy response therefore failed with `Conflict` even
though its original queued operation remained authorized. The earlier fake
Worker RPC tests did not model this server revision transition; their evidence
is preserved without claiming otherwise.

Take now reads current account state without treating the original lifecycle
observation as an exact current-record requirement. The observation remains
positive and cannot be in the future. Authority still requires the exact
uncanceled queued operation, action, selected machine and current initiating
actor, with the existing Worker/device/instance, installation and recovery
checks. Owner Request/Cancel mutations retain their current account revision;
execution Take retains its exact claimed job revision. The original Take
receipt cannot redistribute its bundle. No schema, migration or generated
bindings change.

Real loopback Connect/SQLite fixtures hold an execution lease, queue refresh or
logout, observe a typed busy response, release the execution with a verified
unused-original completion and retry the identical request. Both retries must
acquire the same queued operation and original generation. Further fixtures
cover login/refresh/logout after metadata changes and reject canceled,
replaced, recovery-required or future-observation claims without a mutation.

Executed on macOS arm64, with only temporary state and synthetic credentials:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m -overlay "$OVERLAY" \
  ./cmds/delidev-cli/internal/server \
  -run '^TestSubscriptionLifecycleTake(RetainsOriginalRevisionAfterBusy|StaleObservationPreservesPendingAuthority)$' -count 1
```

The baseline overlay added only the new regression file to unchanged
`63fecacdd`; it failed the two busy/release retries and all three metadata
cases, exit 1 (18.024s). The twelve negative controls passed. The fixed overlay
also replaced only `subscriptions.go` with the proposed source; all seventeen
cases passed, exit 0 (17.752s). External overlays kept the running complete Go
validation's repository source unchanged. The exact validated files are copied
into the final repair without substituting implementation or test behavior.

The fixed overlay also passed the complete existing subscription regression
selection (`-run 'Subscription'`, 57.439s), server `go vet -p 1` and Windows
amd64 test-package cross-compilation. Compilation does not run Windows tests.
The command-specific logs remain in this run's external temporary directory.

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m -overlay "$FIXED_OVERLAY" \
  ./cmds/delidev-cli/internal/server -run 'Subscription' -count 1
GOMAXPROCS=2 go vet -p 1 -overlay "$FIXED_OVERLAY" \
  ./cmds/delidev-cli/internal/server
GOMAXPROCS=2 GOOS=windows GOARCH=amd64 go test -p 1 -c \
  -overlay "$FIXED_OVERLAY" -o "$EXTERNAL_TEST_BINARY" \
  ./cmds/delidev-cli/internal/server
```

The earlier complete-run source and this subsequent focused source are recorded
separately in the composed validation record. No complete suite pass, real
OAuth/inference, installed-native acceptance or Windows execution is claimed.
The published-head Windows Workspace timeout remains unresolved; this repair
addresses the independently reproduced lifecycle revision problem.
