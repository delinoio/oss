# Combined validation after OpenCode reconciliation merge

## Tested source

The combined tree is merge commit
`69d72d8c29702fc40a092cf362ef63559b730fce`, incorporating main
`d1f83cecee4e0c50ea094335392cf68845f85739`. This evidence was collected
on macOS on 2026-09-30. The follow-up evidence commit changes documentation
only; earlier records remain independent historical observations.

## Passing commands

- The nine-package focused race command, its exact filter and scope limits are
  recorded in `main-d1f83cec-merge.md`; all selected packages passed.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- `pnpm proto:check` passed lint, breaking comparison and freshness. Freshness
  forced `buf generate` and the compatibility generator; the worktree remained
  clean after regeneration.
- `node --test scripts/ci/delidev-structure.test.mjs scripts/ci/delidev-proto.test.mjs scripts/ci/proto-breaking.test.mjs`
  passed all seven tests.

## Complete Go command

`GOMAXPROCS=2 go test -race -p 8 ./cmds/delidev-cli/... -timeout 20m`
completed with exit 1: 19 packages passed and four failed. Some unchanged
passing packages reused Go's test cache; the filtered checks above used
`-count=1`.

- CLI: `TestCLISessionAcceptanceQueueAndArchive` failed at
  `sessions_test.go:239` during the creation-comparison diff with a workspace
  reader `unavailable` result. The retained Worker log classifies this
  `git-diff` read as `recovery_required`. The test took 47.51 seconds and the
  package took 301.552 seconds.
- Grok: the package's 20-minute timeout expired while
  `TestStoppedTextObservationRetainsIndependentTerminalAndContent/stop-completion-race`
  was running. The package took 1,202.433 seconds; this is not proof that all
  Grok cases completed, nor the same failure as the preceding native-input case.
- Server: the package's 20-minute timeout expired while
  `TestQuestionResponseRejectsChangedExecutionAuthority/heartbeat` was running.
  The package took 1,200.831 seconds; remaining server cases are unconfirmed.
- Workspace: `TestWorkspaceDiffUnbornAndBoundedResults` at `diff_test.go:163`
  and `TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership/worktree`
  at `pr_match_test.go:62` rejected preparation proof with `recovery_required`.
  The package took 977.010 seconds.

OpenCode passed in 118.278 seconds and Worker passed in 757.212 seconds.
Native-wire and store passed using cached results; this does not fix the
historical native-wire test/log-buffer race. No new data-race warning appears
in this complete command's log. No failure cause was established or repaired
by the merge, and complete local Go validation remains failed.

## Unchanged-input and acceptance limits

This main merge changes no `apps/delidev`, `protos` or
`packages/delidev-api-client` source relative to the preceding main merge.
The preceding complete frontend command passed 98 files/1,264 tests, bundle,
desktop-launch/assets, widget and build checks, and the client command passed
typecheck/45 tests. Those commands were not rerun here; their exact tested head
and temporary runner-cap restoration are retained in
`main-65eca334-local-validation.md`. Generated `apps/delidev/dist` and
`packages/delidev-api-client/dist` are absent and untracked.

The preceding isolated CLI comparison passed on an untouched archive of main
`65eca3341` and failed at the workspace diff assertion on the PR branch. That
comparison's cause remains unresolved; it does not explain this run by itself.
The still earlier session-create timeout comparison is a different failure.
No observation deadlines or assertions were relaxed.

At 12:37 UTC, one host observation reported load averages 7.97/7.84/11.07 and
14 GiB available on a 99%-full data volume. Neither observation establishes a
test failure's cause, and the preceding high-load observation cannot explain
this run without additional evidence.

No real provider/GitHub accounts, enterprise network, native credential
lifecycle, Windows/Linux runtime, release or Worker bootstrap acceptance was
performed. Incoming main's native acceptance records retain their own scope;
they are not executions performed by this proxy repair. Fresh CI and Codex
review evidence are required after the repair push.
