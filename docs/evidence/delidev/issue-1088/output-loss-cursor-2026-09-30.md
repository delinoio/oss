# Terminal output-loss cursor repair

Inspected PR #1173 at `6369089cc2d9069141370bf3c169de7a3444a996`.
Codex thread `PRRT_kwDORRAKg86ncVgG` identified loss-only notifications
replaying output already acknowledged by a client.

The new Connect regression covers an active watcher and a reattachment after
cleanup reports abandoned output. Both failed before the repair because the
notification changed the acknowledged cursor. The repair preserves that cursor
for loss alone while retaining epoch/prefix/forward-cursor gap recovery.

Executed from the repository root with an isolated Go build cache:

```sh
GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go test -race -p 2 -timeout 2m ./cmds/delidev-cli/internal/server -run '^TestTerminal(OutputLossPreservesAcknowledgedCursor|ReceiptsOutputReattachAndArchiveBarrier)$' -count=1
```

Passed, including the existing split UTF-8, retry, retained-prefix and Archive
barrier fixture. This verifies controlled Connect behavior; it does not replace
native platform, remote Worker, desktop visual or release acceptance. Earlier
broad-suite qualifications remain in `validation.md` and `main-merge-2026-09-30.md`.
