# PR #1184: exclude Grok success racing accepted cancellation

The final snapshot of the 08:37 UTC repair pass surfaced Codex thread
[`PRRT_kwDORRAKg86ndDqV`](https://github.com/delinoio/oss/pull/1184#discussion_r4142635655).
It identified ordinary Grok success entering verified accounting after a
committed Stop or Archive request, even when no native GrokStop proof existed.
The review was evaluated against the completion transaction, cancellation
marker, product/native outcome separation and the existing usage contract.

## Reproduction and fix

On merged baseline `36cf47a52c921e43de60e7b1145c404be21b7acd`, the new
real temporary-server/SQLite/RPC regression failed all four affected cases:
Stop and Archive accepted before the ordinary terminal, and after the native
terminal but before verified cleanup retention. The two positive cases with
controls after already committed cleanup/accounting passed.

The original verified completion transaction now requires both successful
product outcome and absence of the original job cancellation marker before
inserting a Grok unit. Reading the marker at commit covers cancellation after
native success, when the retained product outcome can still be successful.
Native success, original response usage, cleanup, receipt replay, paused
dispatch and completed Archive remain independently retained. Later controls
preserve the already committed immutable unit. Updated the owning server rule
and usage contract with this acceptance-order boundary.

## Executed verification

On macOS arm64 with Go 1.26.8:

- `GOMAXPROCS=2 go test -race -p 1 -timeout=5m ./cmds/delidev-cli/internal/server -run '^TestGrokAccountingExcludesSuccessRacingStopAndArchive$' -count=1`
  failed the four affected cases before the fix (5.629s package result), then
  passed all six cases after the fix (7.565s).
- `GOMAXPROCS=2 go test -race -p 1 -timeout=10m ./cmds/delidev-cli/internal/server -run 'GrokAccounting|GrokClosedTextTerminal|NativeCompletion|NativeSessionControls' -count=1`
  passed in 38.581s. Includes existing normal/zero/maximum-uint64 accounting,
  exclusions, mixed units, replay, restart, rollback, capability, terminal,
  completion and session-control regressions.
- `go vet ./cmds/delidev-cli/...` passed after the guard change.

The [main-reconciliation record](main-reconciliation-2026-09-30-0837.md)
retains the five-package composition race pass and full 1,129-test frontend
pass. The guard does not change frontend or protocol source. The final helper
snapshot was not repeated or used to start a check-monitoring loop. Both the
merge and this distinct review repair are pushed once at the end; only then
is the handled thread resolved.

No complete broad local Go race pass or fresh pushed-head CI/review approval
is claimed. Earlier broad limits remain preserved. Temporary fixtures do not
establish installed Grok, hosted-account, native visual, Windows/Linux product
or release acceptance.
