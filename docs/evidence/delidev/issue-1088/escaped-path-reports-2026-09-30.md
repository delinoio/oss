# Terminal report and journal bounds for escaped native paths

Inspected PR #1173 after `5e12cc13`. Codex thread `PRRT_kwDORRAKg86ncVgV`
identified accepted 4,096-byte paths whose JSON reports exceeded the old 16 KiB
report limit. The full paths also exceeded the previous 32 KiB journal limit.

Worker and server now share a 64 KiB result JSON limit. Two 4,096-byte paths
require at most 49,152 escaped bytes, leaving room for state and safe problems.
Every journal reader/writer uses a 68 KiB envelope, reserving 4 KiB for original
UUID/digest/phase metadata around a maximum-sized accepted report.

The new Connect running/close/Archive fixture and extended Worker escaped-path
journal fixture both failed before the fix. The same checks pass afterward,
including exact acknowledgment-loss retry without native replay. A second
journal case fills the entire 64 KiB report envelope, while oversized reports
and oversized journal replacement remain rejected.

Executed from the repository root with an isolated cache:

```sh
GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go test -race -p 2 -timeout 3m ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker -run '^TestTerminal(ReportEscapedPathBoundsRemainRetryableThroughArchive|Journal.*|NativeCreateAndInputReceiptLossNeverReplay|CloseBeforeNativeStartUsesOriginalJournal)$' -count=1
GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go test -race -p 2 -timeout 2m ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker -run '^TestTerminal(ReportEscapedPathBoundsRemainRetryableThroughArchive|Journal.*)$' -count=1
```

Both passed. The paths are controlled metadata fixtures, not physically created
maximum-length executable paths. Native no-replay evidence is Unix/macOS only;
prior broad-suite, other native platform, remote Worker, desktop visual and
release gaps remain in the earlier evidence records.
