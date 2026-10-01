# Reconcile newly merged outbound network support

The repair began at PR head `490874cd554a6aa0130c00fc8c4726b5bc200e35`,
then committed task-backed Claude child creation as `ead4c3ec7` and the independent
accounting fixture window fix as `4b0e277cf601907bf38dfe354b243507bb6c6c00`.
During validation, refreshed main `ef5dbf8974ee69fb262cf66fc174f91e97a0e86f`
introduced issue #1084's outbound proxy feature through PR #1216. Protocol breaking
validation detected missing network.proto, entity kinds 28/29 and capability 6.
GitHub then reported CONFLICTING/DIRTY. This merge reconciles that exact base
without rebasing or replacing either feature.

Instruction/project conflicts retain both sides' policies. All nonblank lines
from both versions of each conflicted Markdown file were checked for preservation.
Domain kind declarations and validation contain network profiles/routes plus
subagents; status advertises independent outbound and subagent capabilities.
Schemas retain main's already-established network values 28/29/6 and child values
30/12. No new reservation or migration was introduced. Buf and the compatibility
pass regenerate all Go/TypeScript sources from reconciled schema; no generated
conflict side was selected manually. NetworkService and legacy reflection/query
facades remain present. Main's implementation and independent issue #1084 evidence
are retained without editing their content.

Focused checks against the merged sources:

```sh
GOMAXPROCS=4 pnpm proto:generate
GOMAXPROCS=4 pnpm proto:lint
GOMAXPROCS=4 pnpm proto:breaking
GOMAXPROCS=4 pnpm --filter delidev-api-client build
GOMAXPROCS=4 pnpm --filter delidev-api-client test
GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/server -run '^(TestSubagent|TestNetwork|TestGrokAccounting|TestMixedCodexGrokAccounting)' -count=1
```

All passed: client six files/47 tests; domain 1.420 s, server 29.410 s.
`git diff --check` passed and source conflict markers are absent.

Before this merge, default frontend validation passed all 103 files/1,323 tests,
plus its bundle/launcher/widget/production build stages. Vet and 113 repository
contract checks passed. Those results identify the earlier `4b0e277cf` tree,
not this combined source. Protocol verification stopped at breaking and did not
run freshness then. The earlier full Go race command was explicitly interrupted
before merging (exit 143) and is incomplete, not a successful suite. Its original
Go/test processes were confirmed gone. Full combined validation is recorded
separately; fixtures and generated-source checks do not prove native/account,
enterprise proxy, credential-store, packaging or release acceptance.
