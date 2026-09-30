# Shutdown output loss review repair

Starting revision: `fb6aa4d4c`, PR #1226. Addresses `PRRT_kwDORRAKg86nk48w`.

Shutdown previously discarded publication cancellation loss. It now synchronizes a conservative loss observation before cancellation, joins the native process and output machinery, and retains that final result. The record binds the original terminal and Worker instance and contains no input/output bytes. A freshly claimed replacement close carries the loss observation without treating it as independent cleanup proof. Lost report responses preserve the exact result and receipt identity.

Executed `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/worker -run 'TestTerminalShutdown|TestTerminalReplacement|TestTerminalCloseRecovery|TestTerminalNativeCreate' -count=3`: passed. The native macOS fixture acknowledges output sequence 1, blocks the suffix, shuts down, replaces the Worker and loses the first close response. Independent tests preserve uncertainty without the original process index and reject a foreign shutdown owner. Existing close-replacement phases and once-only native creation/input regressions also pass.

This proves controlled local fixtures, not native Windows/Linux, remote Worker or release acceptance. Storage failures retain structured error logging and original process ownership; they do not establish cleanup.
