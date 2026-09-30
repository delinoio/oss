# Main reconciliation for public Grok sessions, 2026-09-30

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). This independent maintenance
record preserves the earlier implementation and validation records.

The repair started from `3603071b3fad72501aa15e1f99a252a8af2dcf68` and merges
main `98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`. Main includes stopped Codex API
account switching, ALLGREEN merge-queue CI, and the allocation-only native Codex
model-discovery prerequisite. No new protocol number or migration is allocated
by this reconciliation.

Six textual conflicts retained both sets of independent additions. The sole
source conflict in `ExecutionProgress` preserves Grok's current mode, original
native event and tool count together with Codex's `NativeHistory` field. The
frontend, domain and storage instructions retain both Grok and ALLGREEN rules.
The harness contract retains both original Grok tools/Plan and portable Codex
history boundaries. The protocol contract retains both Resource JSON and the
stopped-account-switch RPC, with separate headings. No generated source was
resolved by selecting a side or edited manually.

## Focused verification before the merge commit

Checks ran on the reconciled working tree with private test resources, without
user credentials or hosted inference. Go commands use the task-private
`GOCACHE=/tmp/oss-1091-go-cache` and `GOMAXPROCS=4`.

- `pnpm proto:generate` reproduced the already merged generated Go and TypeScript
  files without worktree drift against the index. `pnpm proto:lint` and
  `pnpm proto:breaking` passed.
- `pnpm --filter devhud-admin build:embedded` and
  `pnpm --filter async-commit-hook build:embedded` passed before Go compilation.
- Focused `go test -race -p 2 -timeout=20m` passed across domain, server, API proxy,
  Worker, CLI, Grok harness and GitHub integration packages. The same invocation
  compiled storage, which had no matching tests. The selection covered public
  Grok tool/Plan/response identities, retained foreign Plan locators, stopped
  account switching, original native-history authority and ALLGREEN queue proof.
  The exact test selector was
  `Test(GrokPublic|GrokInitialPlanTerminal|GrokResponses|GrokExactNative|GrokDecimal|PublicToolJournal|PublicNumericRequest|StoppedAccountSwitch|AccountSwitch|HistoryObservation|SwitchedHistory|FullNativeHistory|ALLGREEN|HistoricalQueue|CLISwitchAccount|GrokExtraReceipt)`.
- `go vet -p 2 ./cmds/delidev-cli/...` and
  `go build -p 2 -o /tmp/oss-1091-main-98df-delidev ./cmds/delidev-cli` passed.
- `pnpm --filter @delinoio/delidev-api-client test` passed all 44 tests in four files.
- The first required `pnpm test` in `apps/delidev` passed client build and
  typechecking, then failed one of 1,292 unit tests at its original five-second
  deadline: `App.test.tsx` / `defers a targeted entry within an opening and clears
  it when that opening closes`. The other 1,291 tests passed. The aggregate did
  not reach bundle, launch, widget or frontend build stages. This is a recorded
  failure, not a complete frontend pass or proof that the timeout is unrelated.

## Post-merge frontend verification

The merge commit is `44b68c0b690024af6a6498577694ff0ec28e430f`. The unchanged
`App.test.tsx` file passed all 45 tests in isolation with its original deadlines;
the initially failing targeted-entry test was not edited by this merge. Main's
App fixture change separates different notification/import draft checks.

A second unmodified `pnpm test` in `apps/delidev` passed the complete command:
client build, typecheck, all 1,292 unit tests in 100 files, eight bundle-dry-run
fixtures, 16 desktop-launch/asset fixtures, widget fixtures and the production
frontend build. It used the original default runner settings and deadlines.
The first failure remains recorded above; the successful retry does not prove
its cause or imply any native platform/account acceptance.

`node scripts/delidev/verify-independent-changes.mjs` also passed its disposable
provider/schedule schema merge experiment, with reproducible generated bindings
and no shared changed files. It did not change this checkout or publish anything.

## Complete root race run and closest checks

At merge commit `44b68c0b690024af6a6498577694ff0ec28e430f`, the required root
command `go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...` exited 1. Twenty
packages passed, two packages had no test files, and two packages failed:

- Worker failed `TestStreamTerminationCancelsRunningOwnedWork/permission_denied`
  with `stream termination left native work running`. The complete group took
  60.04 seconds; the package took 356.991 seconds. The test and its watch
  controller have no diff against pinned main `98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`.
  The unchanged whole group passed in isolation in 16.820 seconds using
  `go test -race -p 2 -timeout=20m ./cmds/delidev-cli/internal/worker -run '^TestStreamTerminationCancelsRunningOwnedWork$'`.
- Workspace hit the package-wide 20-minute watchdog, exiting after 1,200.439
  seconds. `TestRemoteFetchUsesUpdatedCommitAndNeverStaleFallback` had been
  running for 18 seconds at that point; the captured stack was in owned Git
  inspection. The test, Git command implementation and Unix process controller
  have no diff against the same pinned main. The unchanged selected test passed
  in isolation in 35.857 seconds using
  `go test -race -p 2 -timeout=20m ./cmds/delidev-cli/internal/workspace -run '^TestRemoteFetchUsesUpdatedCommitAndNeverStaleFallback$'`.

Both isolated commands used the same task-private Go cache, `GOMAXPROCS=4`, race
instrumentation and original deadlines. They do not establish either broad-run
failure's cause, prove that the failures are unrelated, or turn the complete
race suite into a pass. No cancellation behavior, test deadline or runner
settings were changed to obtain these results.

No Windows-native, installed Grok or real-account acceptance is inferred from
these local fixtures. Generated repository-owned `dist` outputs are removed
after validation and commit hooks finish.

## Review boundary

The existing [automatic outside-workspace Read finding](https://github.com/delinoio/oss/pull/1230#discussion_r4144405319)
remains unresolved pending the already requested maintainer policy decision.
The merge does not introduce filesystem confinement, disable public Grok input,
change the native trust model, accept the risk or manufacture review approval.
The previous head's 12 passing checks and completed Codex reviews do not verify
this new merged head. Maintenance continues; no merge or auto-merge is authorized.
