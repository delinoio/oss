# Review-repair base reconciliation

PR #1222 head `79bbc1109b4b4ade4a566b0525885f45dc29a2d4` had two newly
inventoried Codex findings after the first maintenance push. A separate repair
pass first merged main `d1f83cecee4e0c50ea094335392cf68845f85739`, retaining
its OpenCode event-reconciliation and Windows separate-root implementations.
Compose the additive server-owner conflict without changing restore, migrations,
wire numbers or the imported native ownership rules.

`GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/harness/opencode
./cmds/delidev-cli/internal/server -run
'EventReconciliation|WorkspaceRoot|CheckpointRoots|StatusPreservesForwardingAndRestore'
-count=1 -timeout 10m` passed OpenCode (7.366 s) and server (2.488 s).
This is focused composition evidence, not installed Windows/native acceptance.
The two restore/deletion review repairs receive separate commits and evidence.
