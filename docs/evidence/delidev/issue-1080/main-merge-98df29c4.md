# Restore maintenance reconciliation with stopped account switching and ALLGREEN

On 2026-09-30 PR #1222 became conflicting after main advanced to
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`. This repair merges that exact main into
restore parent `860b5f91ed17b02c38ba2f420c327fa12e24dd3f` without rebasing.
The main snapshot includes #1212 native Codex model-discovery reservations,
#1213 stable ALLGREEN merge-queue CI, and #1218 explicit stopped-session API
account switching. Preserve all their implementation and source-backed evidence.

Compose additive CLI/store/protocol/client instructions and client contracts.
Retain both stopped-account-switch capability 5 and managed-restore capability 7
alongside all previous advertised capabilities. Generate conflicting Go and
TypeScript descriptors from the reconciled schema, never from a selected merge
side. The newly reserved model-discovery capabilities remain unadvertised.

## Composition regression

`TestBackupRestoreRetainsAccountSwitchHistoryWithoutSelectionAuthority` accepts
an account switch through the authenticated Connect fixture, creates and inspects
a real managed SQLite backup, retires native authority and the Worker stream,
restores and reopens the database. It verifies the original account-change
history and execution attribution survive, the session stays paused and requires
recovery, both accounts lose connection/validation authority, and a fresh account
switch is rejected without changing the restored session. Combined status tests
also verify capabilities 5 and 7 remain independently advertised.

The first local test build referenced a nonexistent HTTP field on this fixture;
it was corrected to close its actual Worker stream and retire service/native
authority. The next isolated run correctly rejected the fresh switch with the
existing Aborted classification; its assertion had incorrectly expected
FailedPrecondition. Correcting these two fixture assertions required no product
behavior change. The final race command below passed the composed regression.
These fixtures do not invoke an installed Codex binary or hosted account.

## Completed verification

- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/store
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli
  ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github
  ./cmds/delidev-cli/internal/apiproxy -run
  'Restore|BackupRestore|AccountSwitch|SwitchAccount|Switched|ALLGREEN|QueueCI|CIQuery|RequiredCI|HistoricalQueue|CIProblem|PRCI|Remediation|HistoryObservation'
  -count=1 -timeout 15m`: passed all six packages, respectively 66.545 s,
  46.598 s, 5.434 s, 2.353 s, 2.404 s and 2.373 s.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...`: passed.
- `pnpm proto:lint`, `DEVHUD_PROTO_BASELINE=98df29c41 pnpm proto:breaking`,
  and `node --test scripts/ci/delidev-proto.test.mjs`: passed format/lint,
  compatibility against the merged main snapshot, and all three allocation/
  relocation checks. Repeated `pnpm proto:generate` reproduced all 115 generated
  files exactly, verified by complete path/SHA-256 inventories before and after.
- `GOMAXPROCS=2 GOFLAGS='-p=2' pnpm test` and `pnpm typecheck` in
  `packages/delidev-api-client`: passed all 46 tests in five files and typecheck.
- `VITEST_MAX_WORKERS=2 GOMAXPROCS=2 GOFLAGS='-p=2' pnpm test` in
  `apps/delidev`: passed typecheck, all **1,275 tests in 99 files**, eight packaging
  fixtures, sixteen desktop-launch/asset fixtures, widget fixtures/build and
  production frontend build. Original deadlines were unchanged. Earlier required
  default-concurrency failures remain preserved in historical evidence.
- `GOMAXPROCS=2 CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build` and the
  corresponding `GOOS=linux GOARCH=arm64` build of `./cmds/delidev-cli`: passed,
  with binaries in owned temporary paths outside the checkout. Compilation does
  not establish other-platform runtime acceptance.
- `git lfs fsck`: passed. Both required root embedded frontend builds passed for
  commit-hook prerequisites. No Rust source changed. Generated ignored `dist`
  directories are removed after the commit hook finishes.

The historical full Go race failure at `70b7de3c` remains documented in
`full-race-terminal-70b7de3c.md`; no complete local race pass is claimed for this
head and no blind full-suite repetition was started. This evidence does not
establish installed native, hosted-account or release/distribution acceptance.
New-head CI and Codex reviews must be checked after the push on the next heartbeat;
the previous head's successful checks and thumbs-up do not approve this merge.
