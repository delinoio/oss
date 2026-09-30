# Issue #1095: keep native Fork inside its authentication profile

Incoming main adds the native Fork coordinator, which opens an API-authenticated
child without a managed credential lease. A managed source could otherwise be
accepted and reach that unsupported native profile before rejection. The shared
Fork assignment validation now rejects managed subscription sources with a typed
Unsupported outcome, before server job acceptance and Worker journaling/native
work. It grants no credentials, relay fallback or replacement execution.

Fork/domain/server/Worker instructions, the subscription and fork contracts and
the project cross-domain invariant preserve this current implementation limit.
Managed Fork remains unavailable until it owns a separately verified protected
lease, joined cleanup and credential write-back. Existing API forks remain
covered by the incoming complete fork suites.

```sh
GOMAXPROCS=4 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/server \
  -run '^TestSessionForkRefusesManagedAuthenticationAssignmentBeforeNativeWork$' \
  -count 1
```

Passed on macOS arm64 in 6.440s. The controlled fixture starts from an eligible
API assignment, preserves its complete immutable configuration/snapshot digest
bindings when selecting managed authentication, proves the source assignment
still validates and proves Fork specifically rejects that profile. No native
credentials or real-account acceptance are claimed.
