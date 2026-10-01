# PR #1232 Windows recovery-fixture readiness repair

GitHub Actions run `36796466169`, Windows server job `110161005582`, failed at
`TestWorkspaceRecoveryAfterWorkerRestartUsesOriginalJournal/ready`:
`replacement Worker did not attach`. The failing subtest ran for 5.98 seconds;
the fixture imposed a five-second readiness wait. The server shard otherwise
completed its package inventory, and its aggregate CI Result consequently failed.

`worker.Run` publishes private lifecycle state before readiness. Its connected
startup performs an attachment bounded at 30 seconds, an optional native title
probe bounded at 30 seconds, and a separate negotiation attachment bounded at
30 seconds. A five-second test wait does not cover those existing valid phases.
The fixture now allows two minutes for its original Ready callback and fails
immediately if the controller exits first. Existing structured Worker logging
and explicit stage/elapsed-time diagnostics make future startup failures visible
without credential, browsing-content or local-path logging.

The change is limited to test synchronization and diagnostics. Product startup
deadlines, original journal comparisons, the recovery settlement waiter and
joined-cleanup assertions are unchanged. The CI log alone does not identify
which startup phase exceeded five seconds; it is not proof of a production
attachment defect or of successful Windows acceptance after this repair.

Executed on macOS on 2026-10-01:

- The unchanged six-case test passed before the repair in 16.360 seconds.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server -run
  '^TestWorkspaceRecoveryAfterWorkerRestartUsesOriginalJournal$' -count=1 -v`
  passed after the repair in 15.609 seconds. All six ready, partial-cleanup,
  mismatched-journal and Local variants ran, including the explicit repaired
  journal retry and original evidence preservation.

Fresh Windows CI remains required. Complete-suite results are recorded separately
after the final local validation; this focused run does not establish a full pass.
