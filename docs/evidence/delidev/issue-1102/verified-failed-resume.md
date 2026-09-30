# Verified settled failed Claude Resume — issue #1102

## Source and boundary

The implementation starts from freshly fetched main
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and adapts the unmerged
implementation from PR #1109 (`0c7d6d30fd55aaa77ec33abc2911c7a816d5463a`)
to the current domain/server/Worker ownership and independent evidence layout.
The historical ledger remains unchanged. Issue #964 remains the complete product
contract; this record covers only issue #1102.

Eligible correlated, settled non-aborted Claude failures can retain a public v2
checkpoint after original clean EOF, exact eligible inline history, settled
callback publication, unchanged permissions and independent workspace cleanup.
They retain their failed outcome and paused dispatch. Existing authenticated
Resume/CLI semantics authorize the oldest new queued input under fresh execution
ownership; automatic FIFO and failed-input/tool/answer replay remain prohibited.
Lost-report reconciliation compares original metadata and native history without
native launch and preserves the original failure, problem and pause.

No schema, migration, RPC, frontend or Rust behavior changes are introduced.
Accepted historical v1 reports are not upgraded. Stop/aborted, changed-permission,
unsettled callback and unproved child/background histories remain excluded.

## Executed validation — 2026-09-30

- Passed:
  `go test -race ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/claude ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/server -run 'Claude.*(Continuation|Recovery|Failed|Resume)|OriginalFailedEOF|ClosedContinuation' -count=1`.
  This exercises failed eligibility, independent original native EOF proof,
  root/Read/Bash/answered-question history, explicit Resume in Execute and Plan,
  one claim on Resume receipt replay, unchanged failed predecessor ownership,
  successful/failed recovery rejection at acceptance and result commit, and
  paused comparison-only recovery preserving the original failed problem.
- Passed `git diff --check`.
- Full `go test -race ./cmds/delidev-cli/... -timeout=30m` is in progress with
  `GOFLAGS=-p=1 GOMAXPROCS=2` to limit this run's package concurrency on a machine
  concurrently running other suites. Its result is not yet established.
- Passed `go vet ./cmds/delidev-cli/...`.
- Root `pnpm install --frozen-lockfile` succeeded and installed linked-worktree
  hooks. Real administrator and async-commit-hook embed assets were generated
  before repository-wide Go formatting; generated dist output will be removed
  after verification.

## Evidence limits

These runs use isolated temporary state and controlled native/provider fixtures.
They do not invoke an installed Claude CLI, authenticate user/provider accounts or
claim new hosted-account, native desktop, Windows/Linux, release or perceptual
acceptance. Existing native evidence remains historical and was not rerun here.
