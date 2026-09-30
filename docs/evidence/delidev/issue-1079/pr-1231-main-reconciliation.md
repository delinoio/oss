# Issue #1079: PR #1231 initial maintenance

## Source reconciliation

PR #1231 was created as a non-draft PR against main from
`ced82fa2c09f2d815a7c43686f7c70ca81ee87c7`, with a standalone `Closes #1079`.
The initial maintenance inventory found a main conflict and no unresolved bot
threads or failing checks. Main `d1f83cecee4e0c50ea094335392cf68845f85739` was
fetched and merged without rebasing, producing
`36d7dcd75583ba94a7e63287db3cd08d292c5a6f`.

The only content conflict was an appended Worker AGENTS.md region. Preserve
both the snapshot/permanent-deletion rule and main's Windows OpenCode root and
checkpoint rules. Main's required-workflow proof, execution-device terminology,
Activity presentation, OpenCode root validation and event reconciliation changes
are retained. The automatically merged dispatch still requires present storage
before execution. No generated file is resolved by choosing a side. Main's
additional protocol reservations remain reservations, without empty migrations
or invented implementations.

## Executed checks of the combined source

- `GOMAXPROCS=2 go test -race -p 2 -timeout=10m
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker
  -run 'WorkspaceStorage|StorageRemoval|StorageRetirement|StorageJournal|SessionDeletionIncludesStored|Opencode.*Storage'
  -count=1` passed (server 17.119s; Worker 74.925s).
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- Root `pnpm proto:check` passed, including regenerated-source freshness and
  compatibility against reconciled main.
- Root `pnpm ci:contracts` passed all 113 tests.
- The complete desktop test pipeline passed: client build, frontend typecheck,
  all 98 Vitest files and 1,264 tests with two workers and `GOMAXPROCS=2`
  (101.69s), all eight packaging and sixteen launch/asset tests, widget fixtures
  and production build. Test/fixture timeout bounds remain unchanged.
- Generated app/client `dist` output was removed after validation; dependency
  and toolchain source directories are excluded from repository-output cleanup.

## Separate fixed-source full race run

The earlier required full race command continues against a source-only archive
of `46452512fcf4cfa832086419dfda45fba7b9f98f`. It does not validate this later
merge or the subsequent forwarding exclusion. Its retained partial log reports:

- The CLI's line-239 creation-diff reader failure (502.193s package time), also
  reproduced independently on main and the branch as recorded in
  [the implementation evidence](current-main-workspace-storage.md).
- `TestQuestionControllerOriginalClaimsAndUncertainty/question-claim-failure`:
  `question_reply_test.go:105` reports that Grok did not complete native
  initialization (15.94s subtest; Grok package 1,242.225s). The Grok implementation
  and tests have no diff from the inspected initial main; this particular
  failure has not been independently reproduced on main.
- Successful server (1,480.257s), store (415.990s), Worker (289.410s), user-service,
  Claude, Codex, OpenCode, native-wire, process and other package results.

The overall command and workspace package remain in progress at this record.
No complete Go race-suite success or final exit status is claimed. Its final
result will be retained independently through PR maintenance. Earlier failures,
timeouts and passing isolated reruns remain visible in the implementation and
historical evidence.

## Limits

All checks use private temporary fixtures and controlled local children. Native
Windows/Linux runtime, real-provider accounts, release acceptance and broader
issue #964 completion remain unperformed. New CI/review must evaluate the newly
published head; old check or review results cannot establish its readiness.
