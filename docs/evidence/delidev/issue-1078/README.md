# Issue #1078: permanent session deletion

## Implementation

Replacement based on main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` on
2026-09-30. The preserved implementation from closed PR #1116 was adapted to the
service-specific schemas and scoped source ownership, with capability value 9
from the main allocation ledger. Generated bindings were regenerated rather
than transplanted. SQLite remains schema 24; no reserved migration is activated.

Current forwarding is composed into deletion: Stop is requested atomically,
both original peer cleanup receipts gate purge, and cleanup reports remain valid
after session admission closes. Both primary and auxiliary Worker lanes retain
the outer job lock through final journal and report publication.

## Validation

Fresh checks were executed on 2026-09-30. Implementation revision
`ed9ed80d2` and the focused replay correction `f34ac670d` are the source revisions;
historical PR results are not counted as replacement validation.

Passed:

- Focused Go deletion fixtures across store, server, Worker, workspace and CLI.
- `go test -race ./cmds/delidev-cli/internal/server -run SessionDeletion -count=1`,
  including both forwarding cleanup receipts and actual socket closure.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/store -run SessionDeletion -count=1`,
  including idempotent pause revision/event replay and restored-database erasure.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/cli -run SessionDeletion -count=1`, including the production Worker final-report barrier regression.
- Root `go vet ./cmds/delidev-cli/...` and the serial final-source vet check.
- `pnpm proto:check`: lint, FILE breaking comparison and drift-free regeneration.
- DeliDev source-structure and protocol allocation/relocation tests: six passed.
- API client lint, test (44 tests) and build. Generated package output is removed
  before the final worktree is published.

The required root `go test -race ./cmds/delidev-cli/...` completed with failures:
CLI configuration import/workspace diff and native harness/process fixtures hit
bounds; several large packages reached the default ten-minute timeout. The native
wire diagnostic fixture also raced a `bytes.Buffer.String` read against the
process-exit structured logger write. This exact race was independently reproduced
on an isolated archive of unchanged base `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`
with `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/harness/nativewire -run TestExplicitJSONRPCRejectsForeignEnvelopesAndBoundedDiagnostics -count=5`.
Those native-wire/process sources are unchanged by this PR. The full race suite
is not reported as passing. A serial retry (`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/...`)
also encountered the CLI creation-diff `unavailable` failure and was stopped after
that confirmed failure; it is not a completed second full-suite run.

## Limits

Tests use isolated temporary SQLite, Git, process and socket fixtures without
credentials or hosted inference. Native Windows/Linux, real-account and package
distribution acceptance have not been performed. Workspace snapshots, dependent
Sidechats and terminal product families remain unimplemented on this baseline;
their future resource owners must join the deletion graph before activation.
Reclaimed filesystem allocation remains unknown even after confirmed cleanup.
