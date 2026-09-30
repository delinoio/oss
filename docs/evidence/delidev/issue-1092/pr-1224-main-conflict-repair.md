# PR #1224: main conflict repair

## Sources and scope

The five-minute maintenance pass observed PR #1224 as conflicting on head
`fdfb3ef10969cb25d952e97d55e8abb47651c866`. The clean issue checkout merges main
`65eca3341e2180f676fc81c24ebccbc82b67f344` without rebasing.

The only textual conflicts are independent entries in
`apps/delidev/src/AGENTS.md` and `protos/delidev/AGENTS.md`. Both fork rules and
main's Activity presentation/repository-inspection reservation rules are
preserved. Existing wire allocations and generated sources are unchanged by the
conflict resolution. Main's Activity, execution-device presentation and pinned
required-workflow changes remain intact. The fork's generic technical Worker
proof wording remains consistent with the Runner Device presentation contract,
which expressly preserves generic technical Worker terminology.

No new fork native, persistence, account selection, queue, deletion or workspace
behavior is introduced by the resolution. There were no unresolved Codex review
threads and no failing CI checks in the repair's initial inventory. The previous
head's Ubuntu/macOS Go CI passed; that evidence does not approve the merged head.

## Validation

- Focused frontend: all 21 tests passed across `session-fork`, `activity-sidebar`
  and `session-tools`, with one Vitest worker and `GOMAXPROCS=2`.
- Protocol/structure CI-contract tests: all 6 passed.
- API-client output was explicitly built before frontend consumption.
- Protocol lint and `git diff --check` passed.
- Race-enabled server fork tests passed in 65.022 seconds. They retain the same
  independent child lifetime and native-ownership checks after the merge.
- Root `go vet -p 1 ./cmds/delidev-cli/...` and `pnpm proto:check` passed.
- Full root DeliDev Go tests and desktop `pnpm test` were launched against the
  reconciled checkout and are tracked separately. No whole-suite pass is inferred
  from the focused checks. The frontend's workspace integration setup reached its
  explicit 120-second hook bound on the shared host; other tests were still running
  when this conflict repair was committed. Native acceptance from the prior
  replacement evidence remains revision-qualified; no installed native run is
  inferred from this documentation conflict repair.
