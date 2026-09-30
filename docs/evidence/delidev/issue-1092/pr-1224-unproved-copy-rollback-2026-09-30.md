# PR #1224: retain runtime after unproved workspace rollback

The Codex review at
<https://github.com/delinoio/oss/pull/1224#discussion_r4145335925>
identified that `PrepareFork` may return `RecoveryRequired` without rolling back
earlier owned copies, while the Worker still classifies the native runtime as
unused. Native phase alone is insufficient removal authority.

The common pre-native failure guard now preserves `RecoveryRequired` and its
runtime evidence. Definite workspace rejection still removes the unused runtime
after the workspace owner has confirmed rollback. Unjoined inspection and possible
native child phases retain their existing uncertainty behavior.

Validation on macOS arm64:

- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/worker -run 'SessionFork' -count=1 -timeout=5m` passed (1.787s).
- The temporary-filesystem regression verifies retained runtime bytes for an
  uncertain workspace result in the unused native phase, definite rejection
  removal, and retention for unjoined inspection and possible native creation.

This is guard-level filesystem evidence; it does not claim a fresh installed
Windows run or new-head GitHub CI completion. The historical evidence ledger is
unchanged.
