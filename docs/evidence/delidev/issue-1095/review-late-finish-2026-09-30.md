# Issue #1095: late subscription Finish ownership fence

[PR #1233 feedback](https://github.com/delinoio/oss/pull/1233#discussion_r4144990756)
correctly identified that a new Finish could mutate the vault and clear ownership
after stream loss had already published recovery-required state.

On head `b8ad137dfd6efa34b61944ac54cea13d9783e1bb`, new temporary Connect/SQLite
fixtures reproduced accepted late finishes for login, refresh, logout and
execution. All four cases failed with an unexpectedly successful RPC. A separate
positive control confirms an already accepted receipt can still replay after a
later operation loses ownership.

The handler now checks recovery-required state before vault work and at its
final transaction, without changing the accepted receipt's read-only replay
path. Tests compare the complete account bytes/revision and vault put/delete
counters to prove denied late finishes preserve the original lease, pending
operation and generations.

`GOMAXPROCS=4 go test -race -p 1 -timeout=10m
./cmds/delidev-cli/internal/server -run 'Subscription' -count=1` passed after the
fix (24.100-second package). The earlier complete-suite result remains scoped
to its recorded revision; this focused repair result is not a new full-suite,
real-account or platform acceptance claim.
