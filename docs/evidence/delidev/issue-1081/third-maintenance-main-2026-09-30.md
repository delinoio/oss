# Manual PR fixes: third-pass main reconciliation

Date: 2026-09-30. PR: [#1227](https://github.com/delinoio/oss/pull/1227).
Revision: the enclosing merge commit, with parents
`b716060c569339887ac253bea044b561e8f53376` and main
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`.

Main advanced during the three-review-fix validation. This merge retains native
model-discovery reservations (#1212), stable ALLGREEN CI (#1213), and explicit
stopped-session Codex API account switching (#1218), together with manual-fix
execution and the three preceding review repairs. Instruction/contract conflicts
retain both sets of source-backed requirements. The integration scope now
distinguishes supported ALLGREEN from unsupported HEADGREEN while retaining the
manual-fix and automatic/provider-resolution boundaries.

The continuation conflict required semantic composition: main moved successor
construction into a helper with original predecessor account/connection binding.
That helper clears the predecessor remediation selection before current-authority
validation. Fresh exact input/attempt binding remains the only source of successor
PR Git authority. Original stopped-account history and checkpoint ownership remain
intact. No new shared allocation or migration was invented for this merge.

Executed validation on the reconciled tree:

- `pnpm proto:generate`, `pnpm proto:lint`, `pnpm proto:fresh` and
  `pnpm proto:breaking` passed. Generated sources reproduce from both composed
  service schemas; the baseline was the freshly fetched main above.
- `pnpm test` in `packages/delidev-api-client` passed all 44 tests in four files.
- Required `pnpm test` in `apps/delidev` passed all 1,281 cases in 100 files,
  type checking, eight bundle fixtures, sixteen desktop-launch fixtures, widget
  checks and the production build. This is a complete frontend-command pass on
  this tree; earlier failed runs remain preserved in their independent records.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/integrations/github ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/apiproxy ./cmds/delidev-cli/internal/cli -run 'ManualPRFixRPC|PRFix|Continuation|AccountSwitch|SwitchedHistory|FullNativeHistory|ALLGREEN|QueueCI|MergeQueue|HistoricalQueueFailure|ExecutionCheckpointRequiresExactAssignmentAndConfirmedCleanup' -count=1 -timeout=15m`
  passed server (172.523 seconds), domain (1.580), Worker (9.280), GitHub adapter
  (1.916), API proxy (1.471), and CLI (2.379). Store failed three positive
  `TestPRFixOnlyVerifiedOriginalPushHandlesEvidence` cases: verified and both
  Worker-clock cases omitted handling proof. Inspection identified their synthetic
  Agent's default permission, which the new immutable manual-fix guard correctly
  rejects. This combined command did not pass; the fixture correction and its
  subsequent verification belong to a separate record/commit.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed after that run.
- `GOMAXPROCS=2 go build -p 1 -o /tmp/delidev-1227-third-merge-cli ./cmds/delidev-cli`
  passed. Required embed outputs were explicitly generated before validation.
- `git diff --check` passed after conflict composition.

These checks do not establish a complete Go race-suite pass, live-account native
Git execution, all-platform installed behavior or release acceptance. No Rust
source changed. Generated repository-owned `dist` output is removed before the
repair finishes; dependency caches are preserved. The original executable/
configuration verification-to-execution P1 remains unresolved pending the
previous profile/execution-boundary decision. Prior-head CI success is historical;
the final repair push requires fresh CI/review observation on the next heartbeat.
