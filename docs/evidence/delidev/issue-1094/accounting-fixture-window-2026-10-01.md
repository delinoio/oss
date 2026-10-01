# Accounting fixture query window

GitHub Actions run `36796115658` evaluated PR #1225 head
`490874cd554a6aa0130c00fc8c4726b5bc200e35`. The macOS Go job
`110159897786` failed only `TestMixedCodexGrokAccountingKeepsUnitKindsAndLegacyCounts`
(at `grok_accounting_test.go:236`, 0.03 s; server package 107.034 s). Its summary
contained the Grok unit but no newly inserted Codex response. `CI Result`
`110162961996` failed because that Go job failed. All other selected CI jobs passed:
15 passed, two failed, 23 skipped. The Windows Worker job passed, supplying actual
Windows CI execution of the prior private-ACL history fixture; the earlier
cross-compilation alone had not proved that execution.

The fixture used a default query endpoint sampled immediately after its writes.
Response retention truncates timestamps to milliseconds; the default summary end
is the current millisecond, and response selection correctly uses `created_at <
until`. A fresh retained row can therefore coincide with the exclusive endpoint.
The accounting fixture now supplies a bounded endpoint one hour after query
construction. It still checks exact Codex/Grok separation and all original totals,
coverage, replay, control-race, transaction, deletion and restart assertions.
Production selection, aggregation, retention and timeouts are unchanged.

Natural recurrence was not reproduced locally in 50 unchanged repetitions:

```sh
GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/server -run '^TestMixedCodexGrokAccountingKeepsUnitKindsAndLegacyCounts$' -count=50
```

That command passed (23.250 s). A temporary uncommitted Go overlay instead set the
mixed fixture's endpoint to the exact retained Codex response timestamp. The
original assertion then failed with the same missing Codex response and retained
Grok unit as CI (server 1.170 s). The overlay logged equal retained and endpoint
milliseconds and was used only for this controlled boundary demonstration; it is
not part of the committed test or production code. This distinguishes deterministic
endpoint reproduction from an unobserved repeat of the original timing race.

After the fixture endpoint change:

```sh
GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/server -run '^(TestGrokAccounting|TestMixedCodexGrokAccounting)' -count=3
GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/store -run '^TestUsageSummaryHalfOpenTimeZeroAndGroupBounds$' -count=1
```

Both passed (server 23.419 s; store 3.409 s). The unchanged store regression
independently confirms exclusive-end behavior. `git diff --check` passed.
Source for the focused commands is review-fix commit `ead4c3ec77a15c7f8dedbf5477aced28487e812d`
plus only this fixture endpoint change. No runtime, protocol, policy, dependencies
or public behavior changed. Full validation and fresh pushed-head CI remain
separate evidence; fixtures do not establish real-account or release acceptance.
