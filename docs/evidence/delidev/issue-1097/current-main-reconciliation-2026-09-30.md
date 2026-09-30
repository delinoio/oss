# Stopped Codex account selection on current main

## Revision and scope

Reconciled implementation: `471b05d8489c7f0d992a2b582022be33a27833f8`.
Inspected base: freshly fetched main
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250` on 2026-09-30.
The implementation retains the source-backed work and historical evidence from
closed, unmerged PR #1177 at `f2826f58244566d53b6a4543f5284b01378b4c27`.
The previous record remains intact; its executed checks are historical, not new
validation of this revision.

The current change composes selection with main's permanent-session deletion:
both SessionService RPC families remain available, permanent deletion retains
capability 9, and stopped-account selection activates main's reserved capability
5. Regenerated Go and TypeScript bindings retain both operations and the legacy
reflection/query exports. Scoped instructions and domain contracts retain both
ownership boundaries. No migration or numeric reservation was added.

The added deletion regression checks the real authenticated Connect operations.
Pending original Worker cleanup rejects a fresh account-selection request.
An already accepted selection receipt returns current state without another
history append, receipt, execution grant or deletion-state mutation. The native
fixture additionally checks retained assistant history and rejects item references
alongside response/conversation references.

## Executed validation

- The first scoped race invocation passed the stopped-account/selection,
  usage-attribution, principal/revocation, switch-back connection, CLI capability,
  full-history relay and remote-reference refusal tests in server, apiproxy and CLI.
- `GOMAXPROCS=2 go test -race -p 1 -timeout 5m
  ./cmds/delidev-cli/internal/server
  -run '^TestAccountSwitchCannotAlterSessionPendingPermanentDeletion$' -count=1`
  passed after correcting the fixture to preserve accepted receipt observations.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed.
- `pnpm proto:check` passed protocol lint, breaking comparison and forced
  generated-source freshness against the committed implementation. The
  compatibility pass retained both service families with no generated drift.
- The delidev-api-client test, typecheck and build commands passed: 44 tests
  across four files, including both capabilities, both SessionService query
  families and historical descriptor identity.
- `node --test scripts/ci/delidev-structure.test.mjs
  scripts/ci/delidev-proto.test.mjs scripts/ci/proto-breaking.test.mjs` passed
  all seven tests, including immutable numeric allocations and LFS-free schema
  baseline comparison.
- Root Lefthook Go formatting passed for the implementation commit. The real
  administrator bundle was generated and validated for Go's embedded-asset
  preparation; this does not establish DevHud runtime acceptance.

## Failed attempts and limits

A concurrent scoped repeat reported a missing continuation assignment deadline
in the usage fixture. An initial deletion regression incorrectly required an
accepted receipt to fail during deletion; it was corrected to assert current-state
observation and independent denial of fresh selection, and the isolated rerun
passed. Neither failed invocation is counted as a successful suite.

The installed-native command selected Codex `0.151.0` explicitly:
`DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex
GOMAXPROCS=2 go test -race -p 1 -parallel 1 -timeout 5m
./cmds/delidev-cli/internal/cli -run '^TestManualNativeCLIAccountSwitch$'
-count=1 -v`. It failed during the discovery initialization handshake before
account selection, with typed `unavailable` diagnostics. It proves no A-to-B
native acceptance. Product probe/operation deadlines were not relaxed.

The full `GOMAXPROCS=4 go test -race -p 2 -timeout 20m
./cmds/delidev-cli/...` invocation reported CLI and harness timeout failures.
The host's observed load rose from about 114 to 589 on 16 CPUs during these
attempts. This is operating context, not proof of the cause of every failure.
The invocation is not a successful full-suite gate.

No hosted-account inference/billing, subscription switching, desktop UI,
Windows/Linux native execution or release acceptance is claimed. The scoped
provider/SQLite fixtures use isolated temporary state and synthetic/keyless
loopback authority. Generated repository-owned `dist` output is removed before
delivery; installed dependency distributions are not repository-owned output.
