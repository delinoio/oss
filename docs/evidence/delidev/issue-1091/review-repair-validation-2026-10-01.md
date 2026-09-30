# Review repair integrated validation, 2026-10-01

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). The date uses Asia/Seoul;
this pass began with the `2026-09-30T14:55:40.466Z` heartbeat.

Distinct fixes retain original native proposal bytes and independently verify
proposal SHA-256 (`73d20a770`), then require the complete original request-ID
representation (`78affe3e5`). Their focused evidence is retained in
[proposal digest](proposal-digest-review-2026-10-01.md) and
[exact request identity](exact-request-identity-review-2026-10-01.md).

[Main reconciliation](main-9ef-reconciliation-2026-10-01.md) records the initial
breaking failure, interrupted earlier race run and merge of
`9efb1917e0127a9223cee0969877238ab37c0e1d`. The merge commit is
`3e73a4edc4a8b66d37c66d5420965bbdae2ecf1c`. The later documentation-only
`784130d6d` retains stopped account switch value 5 in the merged capability
paragraph and corrects the independent status capability count to seven, as
proved by `system.proto` and `server_status.go`. No executable source changed
after the merge's validation began.

All commands use private test resources without user credentials or inference.
Go uses `GOCACHE=/tmp/oss-1091-go-cache`. The reconciled source passed:

- Protocol generation, formatting/lint, breaking against current main and
  generated-source freshness. Regenerated bindings matched the merge index.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` and
  `GOMAXPROCS=2 go build -p 1 -o /tmp/oss-1091-review-1455-main9ef-delidev ./cmds/delidev-cli`.
- Focused race checks for the two reviews plus managed restore, account-switch
  quarantine, current deletion/actor authority and CLI restore: five packages
  passed with the exact command retained in the reconciliation record.
- `pnpm --filter @delinoio/delidev-api-client test`: five files, 46 tests.
- Complete `pnpm test` from `apps/delidev`: client build/typecheck, 100 unit
  files and 1,292 tests, eight bundle fixtures, 16 launch/asset fixtures,
  widget checks and production build. Original settings/deadlines were retained.
  The repository icon was hydrated before testing.

The complete required root race command on the reconciled executable source was:

```sh
GOMAXPROCS=4 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...
```

It exited 1: 21 packages passed, two had no test files and CLI failed.
`TestCLISessionAcceptanceQueueAndArchive` failed after 80.30 seconds at
`sessions_test.go:281`: stale review submission returned `unavailable` with
workspace-reader guidance instead of the expected conflict. Its private logs
retained native `internal` and workspace `recovery_required` outcomes during
Git-diff observation. The CLI package took 209.853 seconds.

The unchanged exact fixture passed in isolation in 85.563 seconds:

```sh
GOMAXPROCS=4 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/internal/cli -run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1
```

The fixture and workspace reader were not changed by this repair. The initial
run coincided with high machine load, but neither that observation nor the
isolated pass establishes the failure's cause or turns the broad run into a
pass. No test/production deadline or assertion was weakened, and the full suite
was not repeatedly retried. Complete Grok (998.887 seconds), server (659.243),
store (278.053), Worker (175.795) and workspace (425.393) packages passed.
Prior failures remain preserved in their original independent records.

Required administrator and async-commit-hook embedded builds passed before
CLI compilation and normal Go commit hooks. Repository-owned generated `dist`
output is removed after final validation/hooks; none is tracked. The source
repairs require no Rust, frontend implementation, dependency, native permission,
protobuf allocation or migration change.

The automatic outside-workspace Read review remains unresolved pending the
previously requested policy decision. These repairs do not establish filesystem
confinement, real-account/platform acceptance, risk acceptance or merge approval.
A push invalidates old-head CI/review evidence; current-head checks and reviews
are observed on the next registered heartbeat, without merging or auto-merge.
