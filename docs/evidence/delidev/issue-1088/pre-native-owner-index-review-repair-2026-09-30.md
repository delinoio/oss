# Pre-native terminal owner-index review repair — 2026-09-30

This record belongs to issue #1088 and PR #1226. The source baseline is
`b4dc31c0` (the preceding independent output-gap repair); this commit contains
the tested creation-order change for
[review thread 4144945209](https://github.com/delinoio/oss/pull/1226#discussion_r4144945209).

Creation now prepares the private terminal-process root and its original
terminal owner index, synchronizing their parent directories through the
existing platform security abstraction, before writing the started journal.
Index preparation failure preserves the claimed journal and performs no
native action. Input/resize start ordering and original uncertain-operation
replay refusal remain unchanged.

`TestTerminalStartedCreationRetainsPreNativeCleanupIndex` stops at that actual
start-intent helper before execute or shell discovery. A replacement manager
simulates restart and closes against the original started creation. Moving
the index produces recovery-required uncertainty; restoring the unchanged
empty index permits independent close reconciliation without any process or
PID inference. This is a controlled boundary simulation, not a physical
power-loss test. `TestTerminalCreationOwnerFailurePreservesClaimedJournal`
rejects a foreign file in place of the index, preserving both its bytes and
the original pre-native phase.

Executed from the repository root:

- `go test -race ./cmds/delidev-cli/internal/worker -run 'TestTerminal(StartedCreation|CreationOwnerFailure|CloseBeforeNativeStart|NativeCreateAndInputReceiptLoss|FailedControl)' -count=1`:
  passed (3.461 s). The native Unix receipt-loss/control fixtures also retain
  exact no-replay and confirmed/unconfirmed cleanup behavior.
- `gofmt` on the changed Go sources and `git diff --check`: passed.

Worker instructions, the terminal/process contracts and the project cleanup
invariant describe this ordering. No protocol or database migration changes.
The platform-neutral index fixtures do not establish native Windows execution
or durable recovery after real power loss. Broader validation is recorded
separately, and fresh hosted checks/reviews remain required after the push.
