# Confirmed terminal ownership retirement

Starting revision: `2c0238a3b`, PR #1226. Addresses `PRRT_kwDORRAKg86nk49B`.

A confirmed report now synchronizes a separate reported phase before local retirement. Verified cleanup prunes completed native scopes, removes only the empty private owner index and released recovery lock, retires its shutdown observation, and finally removes the operation journal. The manager mutex and existing exclusive Worker root lifecycle ownership prevent another terminal lifecycle from reusing that UUID. Uncertainty and unacknowledged reports retain process evidence. A failed retirement retains the acknowledgement for local retry after process replacement or server-side terminal/session purge; that retry does not require another report or grant native replay.

Executed `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/worker -run '^TestTerminal' -count=3`: passed in 11.418 seconds. Fixtures cover lost report responses retaining every ownership artifact, acknowledged cleanup removing them, a held maintenance lock retaining synchronized acknowledgement, replacement finishing retirement with no RPC client, and uncertain results preserving the original process index and shutdown loss. The existing native macOS creation/input/close and replacement-close tests also pass.

This is fixture evidence, not native Windows/Linux or remote/release acceptance. Nonempty or invalid ownership cannot be erased recursively. The generic process reconciler retains its existing owner-index behavior.
