# Issue #1105 merged-tree validation and CI limits

## Source

Validated the resolved merge at
`260b9a8ea7c404040ebd60a09fe39960fb44e71c`, combining the prior PR head
`193f9c0f82247efec970830ff189af1d71efa701` and main
`7090de04621ece95b3c2cce8d88fcfcdeadad7cc`. The conflict resolution and
complete preservation check are retained in
[the separate merge record](main-merge-2026-09-30.md). This record does not
replace the initial validation or frozen historical ledger.

## Go and protocol results

- The merged queue/domain/GitHub/store accounting/migration race command in the
  merge record passed all three packages.
- `go vet ./cmds/delidev-cli/...`: passed.
- `pnpm proto:check`: passed lint/format, main-baseline breaking comparison and
  forced regeneration/freshness.
- `node scripts/delidev/verify-independent-changes.mjs`: passed; its structural
  fixture reproduced bindings without shared changed files.
- `GOMAXPROCS=2 go test -race -p=1 -timeout=3m ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run 'GrokAccounting|MixedCodexGrokAccounting|NativeAccountingCapability|Usage.*Accounting|RepositoryQuery|PRProblemsRPC|PRProblemCollectionFamilies|CLIPRProblem|CLIRemediation'`:
  reported passing server (141.226s) and CLI (15.297s) packages. The command
  took substantially longer than those package durations; its runner was
  observed waiting for an OS child. An attempted cancellation did not send a
  signal because the output-file ownership check did not match. These package
  results are not a complete native-suite pass.

## Frontend results

`pnpm test` in `apps/delidev` failed in Vitest: 90 files / 1,240 tests passed,
six files / seven tests failed, over 343.90s. Six failures were test deadlines
(five at 5,000ms and the workspace integration at 60,000ms). The remaining
pull-request pagination assertion saw an additional empty-cursor request.
Failures covered App New Project focus, Agent disclosure closure, two PR
pagination/filter cases, Settings write lifetime, native workspace inspection
and Agent-row configuration presentation. No assertion or deadline was changed.
The API-client build and type checking had passed before Vitest stopped the
standard command.

Executed the same build/typecheck/Vitest stage sequence with
`pnpm exec vitest run --maxWorkers=2`. It failed over 252.02s with 95 files /
1,246 tests passed and one file / one test failed. All seven earlier failing
cases passed in this attempt; the remaining failure was the 5,000ms deadline
for `defers a targeted entry within an opening and clears it when that opening
closes` in `App.test.tsx`.

`pnpm exec vitest run src/App.test.tsx -t 'defers a targeted entry within an opening and clears it when that opening closes' --maxWorkers=1`
passed that case in 1.12s (43 unrelated cases were intentionally not selected).
This focused pass does not turn either full attempt into a passing full suite.
Concurrent tests/builds in other checkouts and the original Go run were observed;
that is a qualification, not proof of each failure's cause.

Executed the remaining standard stages separately after the failed Vitest
attempts:

- `pnpm test:bundle-dry-run`: eight fixtures passed.
- `pnpm test:desktop-launch`: sixteen fixtures passed, including exact-path LFS
  asset preparation and local-edit preservation.
- `pnpm test:widget`: passed the Swift widget fixture checks.
- `pnpm build`: passed the production frontend build.

Generated repository-owned frontend and API-client `dist` directories were
removed after these checks. The DeliDev LFS icon was hydrated before consuming
asset/packaging output. These are fixture/build results, not native packaged-app
or supported-platform acceptance.

## GitHub CI and isolated reproduction

On the pre-push head, ten GitHub checks passed, twenty-six were skipped and two
failed. The macOS Go job failed during workspace preparation for
`TestLocalReviewSubmissionRejectsConcurrentCommentEdit` with
`unavailable: Git could not be launched on this Worker`. The aggregate CI Result
job reported `go-test: expected success, got failure`, so it is the same upstream
failure rather than a separate root cause. Linux Go and all Windows Go shards
passed. See the [macOS job](https://github.com/delinoio/oss/actions/runs/36700067003/job/109837825931).

Executed:

`GOMAXPROCS=2 go test -race -p=1 -timeout=2m ./cmds/delidev-cli/internal/server -run '^TestLocalReviewSubmissionRejectsConcurrentCommentEdit$' -count=1`

It completed with exit 1; the fixture failed during setup at
`local_reviews_test.go:163` with `internal: State storage failed`
(test 0.77s, package 2.150s). This is a different stage/error from GitHub's
Git-launch failure and does not reproduce or explain that CI root cause.
Neither native launch nor storage diagnostics in these observations establish
an actionable implementation correction. No speculative workaround, timeout
increase, test omission or CI-success claim was introduced.

The original full Go race run remains separate and has recorded CLI, Codex,
Grok, OpenCode, server and Worker failures. Its complete result must be retained
independently when the runner exits, including the source-provenance limit
already documented in the merge record. No second full native suite was run.
A new pushed head requires fresh CI and Codex review; an earlier completed
review with no unresolved threads is not acceptance of the merged head.
