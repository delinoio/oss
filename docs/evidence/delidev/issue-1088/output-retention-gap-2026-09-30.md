# Fresh attachment after terminal output retention loss

Inspected PR #1173 after `ffee058a` (the independent acknowledged-cursor repair).
Codex thread `PRRT_kwDORRAKg86ncVgQ` identified cursorless clients receiving an
apparently complete empty transcript after ephemeral output was discarded.

A fresh cursorless observation of an empty ring now reports unknown prior
retention as a gap. This conservative fact also applies before the first native
frame. A retained transcript starting at sequence one remains complete; an
observer with a known cursor keeps the ordinary cursor/loss rules.

The Connect fixture keeps terminal metadata in SQLite while evicting its ring
through the real 128-ring LRU, or constructing a new service without ephemeral
rings. Both discarded cases failed before the fix; the retained case passed.
All three pass after the fix, without reconstructing discarded bytes.

Executed from the repository root:

```sh
GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go test -race -p 2 -timeout 2m ./cmds/delidev-cli/internal/server -run '^TestTerminal(Output.*|ReceiptsOutputReattachAndArchiveBarrier)$' -count=1
```

Passed. This is controlled Connect/service recreation evidence, not a physical
remote Worker or native desktop test. Prior broad-suite and real-platform gaps
remain recorded in the earlier evidence files.
