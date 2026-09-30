# Once-per-attachment output-loss review repair — 2026-09-30

This record belongs to issue #1088 and PR #1226. The source baseline is
`5f6800f2ee39409923379657bd946629ba23a326`; this commit contains the tested
stream change and regression coverage for
[review thread 4144945200](https://github.com/delinoio/oss/pull/1226#discussion_r4144945200).

The attachment now tracks its observation of the monotonic persisted
`OutputLost` fact independently of the resource revision. After its first
loss notification, metadata updates and confirmed cleanup remain ordinary
heartbeats. Fresh attachment still reports the retained fact, and cursor,
epoch and unknown-retention gaps retain their separate behavior. Loss-only
notifications never replay the acknowledged bytes or advance their cursor.

The expanded `TestTerminalOutputLossPreservesAcknowledgedCursor` exercises
both an existing stream and reattachment after loss, then observes two
metadata reports and final cleanup. Both cases failed against the unchanged
production source with repeated-loss assertions (2.198 s), establishing the
regression before the fix.

Executed from the repository root:

- `go test -race ./cmds/delidev-cli/internal/server -run '^TestTerminalOutput' -count=3`:
  passed (8.995 s), including loss/cursor behavior, retained/evicted/restarted
  fresh-attachment behavior and exact serialized LRU eviction.
- `gofmt` on the changed Go sources and `git diff --check`: passed.

The scoped server instructions and terminal contract describe the refined
observation rule. No protocol, storage migration or native process authority
changes. This focused fixture result does not establish a complete fresh CI
run or native platform acceptance; broader validation is recorded separately.
