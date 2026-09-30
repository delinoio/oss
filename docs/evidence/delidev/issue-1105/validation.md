# Stable ALLGREEN merge queue CI validation

Date: 2026-09-30. Base: freshly fetched `origin/main` at
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. The implementation reuses the
issue-specific source change from closed, unmerged PR #1110 (commit
`9cc7e529e87d9dafb3945997022551f22be52e4a`) and adapts ownership instructions
and evidence to the current split layout. Earlier PR validation is not counted
as validation of this replacement.

## Implemented boundary

The existing authenticated CI query, equivalent CLI and desktop select an exact
ALLGREEN entry commit only after stable complete PR, queue, ordered entry,
configuration, active-rule and check inventories. Actions checks additionally
bind the original `merge_group` workflow to the check suite and entry commit.
HEADGREEN, incomplete or changed observations, missing operands and queue state
alone cannot establish failure. Retained proofs preserve original queue/entry
identity; removal clears current membership and fresh remediation cannot inherit
another entry's historical failure authority. No protobuf, database migration,
Rust source or Settings implementation changes are included.

## Executed checks

- `go test -race ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github ./cmds/delidev-cli/internal/store -run 'ALLGREEN|QueueCI|QueueFailure|MergeQueue' -count=1`
  passed: domain 2.139s, GitHub 3.251s, store 2.729s. Controlled fixtures cover
  entry-commit attribution, wrong App/event/workflow/commit, pending/success,
  UNMERGEABLE without a failed check, HEADGREEN, missing configuration/permissions/
  pages, independent cursors, changed/reordered/removed/replaced entries, original
  proof retention across restart and loss of current remediation authority.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run 'TestPRProblem|TestCLIPRProblem|TestPRRemediationHistory' -count=1`
  passed: server 50.194s, CLI 2.970s. This uses temporary SQLite and authenticated
  loopback fixtures for collection, request/revision receipts, history and CLI
  identity parity; it does not execute a real provider remediation.
- `GOMAXPROCS=2 go test -race -p 1 -timeout=20m ./cmds/delidev-cli/internal/store`
  passed the complete store package in 1,110.518s, using the Go source at
  `ea7683f64daec5e19e98962496c122b45dff5551`. The log is retained at
  `/private/tmp/issue-1105-store-race.log`. This bounded package rerun does not
  replace the unsuccessful default aggregate command below.
- `go vet ./cmds/delidev-cli/...` passed.
- Type checking and `pnpm exec vitest run src/github-ci.test.tsx src/pr-problems.test.tsx`
  passed: two files / 17 tests. Queue evidence, inert display, historical proof,
  foreign scope/commit, HEADGREEN and original Actions provenance are covered.
- `pnpm exec vitest run src/backups.test.tsx src/usage.test.tsx --maxWorkers=1`
  passed: two files / 14 tests. These unchanged tests had failed in the aggregate
  frontend run below; the isolated rerun does not erase that result.
- `GOMAXPROCS=2 pnpm exec vitest run --maxWorkers=2` completed with 82 passed /
  two failed files and 962 passed / two failed tests. The remaining failures were
  `settings-devices.integration.test.tsx` (missing asynchronous revoke button)
  and `tray-presentation.test.tsx` (five-second deadline). This is an improvement
  in the observed result, not a passing complete suite.
- Separate `pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`,
  `pnpm test:widget` and `pnpm build` passed: eight package fixtures, 16 launcher/
  LFS preparation fixtures, native Swift widget fixtures and the production build.
- `node --test scripts/ci/delidev-structure.test.mjs` passed: three tests.
- The complete fixed GraphQL document executed against public PR #1110 without
  schema errors. It returned no queue entry, so this proves query compatibility,
  not live ALLGREEN acceptance. GitHub schema introspection also confirmed the
  entry/queue fields and ALLGREEN/HEADGREEN strategy enum.
- Required DeliDev icon LFS content was hydrated and `git lfs fsck` passed.
  Required administrator and ach Go embed output was generated explicitly for
  the repository formatting hook. All seven repository-owned generated `dist`
  directories were removed from the final worktree after validation; dependency
  output inside `node_modules` was retained.

## Aggregate failures and limits

The required `go test -race ./cmds/delidev-cli/...` command failed. The complete
domain and GitHub adapter packages passed (7.160s / 12.475s). CLI workspace reads
and session acceptance timed out; harness discovery and OpenCode native fixtures
failed; Claude, Codex, Grok, server, store, Worker and workspace packages reached
the default ten-minute package budget. The failures are retained in
`/private/tmp/issue-1105-go-race.log`. Other independent native test runs were
observed on this host. Contention is a possible contributor, not a proven
explanation for every failure; the complete command is not reported as passing.

The required `pnpm test` command failed at Vitest: 17 failed / 67 passed files,
38 failed / 926 passed tests (84 files / 964 tests). Failures were in unchanged
app, backup, usage, desktop, notification and Settings coverage, including
five-second test deadlines and missing asynchronous UI results. The changed CI
and PR problem files passed. Logs are retained in
`/private/tmp/issue-1105-frontend.log`. Subsequent isolated or bounded checks are
reported separately and do not replace this unsuccessful default invocation.

The separate native CLI rerun with `GOMAXPROCS=2 go test -race -p 1` still failed
`TestCLISessionAcceptanceQueueAndArchive` on its workspace-preparation wait
(117.134s for the two-case selection). `TestCLIPairWorkerAndInspectRealRepository`
did not fail in that selection. This persistent failure is not dismissed as a
confirmed concurrency-only result.

The untouched-base control used a temporary source archive of
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and ran
`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/cli -run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1`.
It failed the same workspace-preparation wait (93.294s). The log is retained at
`/private/tmp/issue-1105-cli-baseline.log`. This proves that case also fails
without the ALLGREEN source change on this host; it does not prove the cause of
other aggregate failures.

The final one-worker frontend selection of `settings-devices.integration.test.tsx`
and `tray-presentation.test.tsx` passed the tray case but still failed the Settings
revoke-button observation: one passed / one failed test. That persistent result
remains unresolved; no unrelated Settings source was modified.

Fresh live queued-account/PAT/SSO, native product/platform and release acceptance
were not performed. Fixtures use temporary state and synthetic credentials,
never user accounts or configuration. No full issue #964 completion is claimed.
