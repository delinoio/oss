# Cleanup problem precedence after failed terminal controls

Inspected PR #1173 after `10c204d8`. Codex thread `PRRT_kwDORRAKg86ncVgh`
identified the input/resize error overwriting `finishNative` reconciliation
failure while process-tree cleanup remained unconfirmed.

The repair retains the reconciliation problem for failed and timed-out controls
unless cleanup is independently verified. Confirmed cleanup still reports the
original input/resize problem.

Controlled macOS native process fixtures use the existing start barrier for an
unsent input failure and invalid dimensions for a resize failure. An unexpected
private owner-index entry independently causes reconciliation to fail. Both
unconfirmed cases failed before the repair, reporting the control problem rather
than `RecoveryRequired`; both confirmed-cleanup controls passed. All four cases
pass afterward. The fixture restores the owner index and independently reconciles
it before ending; it never manufactures PID-based termination authority.

Executed from the repository root:

```sh
GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go test -race -p 2 -timeout 3m ./cmds/delidev-cli/internal/worker -run '^TestTerminal(FailedControlPreservesCleanupRecoveryProblem|NativeCreateAndInputReceiptLossNeverReplay|CloseBeforeNativeStartUsesOriginalJournal|Journal.*)$' -count=1
```

Passed. The timeout branch shares the same cleanup guard and was inspected;
this run did not inject a blocked five-second native control. Other native
platform, remote Worker, visual and release acceptance remain unperformed.
Earlier full-suite outcomes are preserved in their original evidence files.
