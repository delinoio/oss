# Issue #1095: managed Codex subscription validation

The replacement implementation starts from main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and adapts the unmerged implementation from [closed PR #1124](https://github.com/delinoio/oss/pull/1124) to the current service-specific schemas, CLI/server file ownership, Worker capability allocation and independent evidence structure. The previous branch observations are preserved separately in [historical evidence](previous-pr-1124.md).

All authentication fixtures use synthetic JWT/token material, temporary private runtime state and controlled native subprocesses. No user login or real provider account is imported.

## Checks executed on the replacement

Original implementation revision: `39ce1b5014a5f219a1c7904340ffc1d8a4e777f9`. The later protocol compatibility repair is recorded separately below.

- `go test ./cmds/delidev-cli/... -run 'Subscription|Managed|Bundle' -count=1`: passed, including protected Connect/SQLite lifecycle, concurrent leases, cancellation/revocation, bundle rotation, lost write-back, native process fixtures and execution authentication cleanup.
- `go vet ./cmds/delidev-cli/...`: passed; the final implementation including capability coexistence was also checked with `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` and passed.
- API client `pnpm test`: 44 tests passed. The first run hit the integration fixture's two-minute Go build deadline during concurrent machine compilation; the warmed-cache retry passed all four test files.
- API client `pnpm typecheck`: passed.
- `pnpm proto:check`: passed, including Buf formatting/lint, FILE/package breaking compatibility against the fetched baseline, allocation-compatible capability value 3, and generated-source freshness.
- `go fmt ./...`: passed after explicitly generating both Go embedded app asset prerequisites.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/subscription ./cmds/delidev-cli/internal/cli -run 'Subscription|Managed|Bundle' -count=1`: all five packages passed, including capability coexistence and duplicate rejection.
- Required full `go test -race ./cmds/delidev-cli/...`: failed during concurrent machine load. CLI/workspace/process/native discovery failures and ten-minute package timeouts were reported across Claude, Codex, Grok, server, Worker and workspace tests. No full-suite pass is claimed.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/harness -run '^(TestCLISessionAcceptanceQueueAndArchive|TestDiscoveryVerifiesGrokWithoutExecution|TestDiscoveryVerifiesOpenCodeWithoutExecution)$' -count=1`: both discovery tests passed in the isolated package rerun; the CLI scenario still timed out at `sessions_test.go:202` (`session create --wait`, workspace preparation remained claimed).
- The same isolated CLI scenario also failed at `sessions_test.go:202` on a disposable archive of unchanged main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` (73.26-second test, 76.081-second package), reproducing the existing preparation deadline without the subscription implementation. This establishes only that specific baseline failure, not that every full-run failure is unrelated.
- The complete adapter/process rerun, `GOMAXPROCS=2 go test -race -p 1 -timeout=20m ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/process -count=1`, failed. Codex reported native handshake/delivery/approval failures and timed out after twenty minutes (1201.269-second package), while `TestContinuationInvalidHistoryNeverAuthorizesResend/duplicate-item` was active. The process package failed `TestCancellationAndForeignOwnerRecovery` with "canceled child did not stop" (76.555-second package). These complete-package checks are not reported as passing; no unchanged-main comparison was executed for these additional failures.

- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`: all six allocation, descriptor relocation and structural compatibility checks passed.

## Acceptance limits

These fixtures establish deterministic protocol and ownership behavior, not real subscription OAuth, hosted inference, installed-Codex subscription execution, desktop login controls, full uncertain-lease recovery, native Windows/Linux runtime acceptance or release readiness. Complete issue #964 remains independent and unfinished.

## First PR maintenance repair

Main `7f356266f` was merged in `959c4070`, retaining its AI API Keys terminology and Diagnostics presentation together with the managed subscription contract. No Go implementation or schema change came from that base merge.

PR #1174's protocol job and macOS/Ubuntu Go jobs all reported the same aggregate-reflection omission: the compatibility generator took its service inventory from the historical declaration relocation map, excluding the new subscription schema. The repair derives both Go and TypeScript aggregate files from compiled `delidev.proto` public imports, preserves historical declaration order, and leaves the relocation map unchanged. TypeScript coverage now verifies canonical subscription declaration identity and reconstructed service reflection.

Executed against the repaired generated output:

- `GOMAXPROCS=2 go test -p 2 ./protos/...`: passed, including the previously failing aggregate reflection test.
- API client `pnpm test` and `pnpm typecheck`: all 44 tests and typecheck passed.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`: all six checks passed.
- `pnpm proto:lint` and `pnpm proto:breaking`: passed.

These focused repair checks do not supersede the complete native-suite failures or real-account acceptance limits above. New-head CI and review remain separate evidence after publication.
