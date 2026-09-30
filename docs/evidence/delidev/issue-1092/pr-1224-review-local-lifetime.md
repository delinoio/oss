# PR #1224: Local child workspace lifetime

Codex review thread `PRRT_kwDORRAKg86ng0Y9` identified that a Local fork of a
managed Worktree would borrow paths still owned by the parent's deletion plan.

Local sharing now requires an original Local source with only user-owned
checkouts. The server rejects managed sources before accepting work, rechecks
the immutable manifest at acceptance/claim/publication, and the Worker rejects
unsupported sharing before native inspection or runtime creation. Workspace
preparation independently applies the same guard. Managed sources retain
independent Worktree copies. The desktop offers sharing only for Local sources.
Same-machine authentication remains required; no checkout ownership is transferred.

Focused race tests passed: workspace 8.186 seconds and server 2.476 seconds via
`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/workspace
./cmds/delidev-cli/internal/server -run '^TestForkLocal|^TestSessionForkRejectsLocalManagedSource'
-count=1 -timeout=3m`. They verify rejection without child preparation, API rejection
without accepted work or source changes, and a real Local checkout plus child
metadata surviving parent deletion. A Local manifest cannot hide an owned copy.
The initial new filesystem assertion compared a canonical prepared path with the
temporary directory's unresolved spelling; it was corrected to compare the
original manifest's canonical path before this successful run.

All four `session-fork` frontend tests passed with one Vitest worker in 2.14
seconds, including both Local and managed Worktree source choices and retained
uncertain requests. The required full desktop `pnpm test` was launched after the
presentation change; its result is recorded separately. No installed native or
other-platform acceptance is inferred from these fixtures.
