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

The complete root DeliDev race suite and frontend timeout investigation remain
to be recorded after the merge commit. No Windows-native, installed Grok or
real-account acceptance is inferred from these local fixture results.

## Review boundary

The existing [automatic outside-workspace Read finding](https://github.com/delinoio/oss/pull/1230#discussion_r4144405319)
remains unresolved pending the already requested maintainer policy decision.
The merge does not introduce filesystem confinement, disable public Grok input,
change the native trust model, accept the risk or manufacture review approval.
The previous head's 12 passing checks and completed Codex reviews do not verify
this new merged head. Maintenance continues; no merge or auto-merge is authorized.
