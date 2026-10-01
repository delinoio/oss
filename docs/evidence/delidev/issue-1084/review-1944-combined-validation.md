# Combined validation of the 19:44 maintenance repairs

The maintenance pass began on 2026-09-30 at 19:44 UTC against PR #1216 head
`f6aed111aa71c22a29bf234191c5d87f20a5ccbb`, already containing main
`6c749670727b30679e722821846bc8dc00f5ac32`. GitHub reported it open and mergeable.
Ubuntu Go CI failed in the relay revocation test, with the dependent CI Result
failure sharing that cause. Codex reported two current unresolved findings.
Complete thread, review, comment and reaction inventory had no additional pages.
The earlier Cloudflare deployment failure on `49feceaa8` was historical: deployment
for `f6aed111a` succeeded.

Each independent repair has its own commit and focused evidence:

- `0919b971b`: completed profile-deletion retries do not recreate cleanup intents
  or reopen a retired obligation's vault. See
  [completed deletion replay](review-completed-delete-replay.md).
- `834c1f21f`: restore retains current machine descriptors required by current
  Worker routes, preserving owner administration without Worker authorization.
  See [restored Worker routes](review-restored-worker-route-machines.md).
- `0c52890b5`: the native relay joins started cancellation callbacks before HTTP
  handler return, preventing a late deadline from affecting another downstream
  request. See [CI cancellation ownership](ci-relay-cancellation-join.md).

Combined validation ran against `0c52890b5` with all three repairs. This final
record changes documentation only. All state, accounts, vaults, repositories and
native processes used by tests were private fixtures; no real account inference,
GitHub publication or production credentials were used.

## Passing checks

- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- `GOMAXPROCS=2 go build -p 2 -o <temporary-output>
  ./cmds/delidev-cli` passed.
- Root `pnpm proto:check` passed formatting, lint, breaking compatibility and
  forced regeneration freshness, without tracked generated-source drift.
- API-client `pnpm typecheck` and `pnpm test` passed: six files, 47 tests.
- Complete desktop `pnpm test` passed: 100 Vitest files and 1,279 tests, eight
  bundle fixtures, 16 launch/assets fixtures, widget checks and production build.
  Vitest duration was 77.94 seconds. Its temporary two-worker cap was restored
  byte-for-byte; no assertion or test deadline changed. The required icon LFS
  object was hydrated. Both generated DeliDev `dist` directories were removed
  after their consumers finished.

## Full Go race result and remaining limits

`GOMAXPROCS=2 go test -race -p 4 ./cmds/delidev-cli/... -timeout=20m` completed
with **22 passing packages and one failing package**. Ten passing results were
cached; three additional packages have no tests. No package hit its timeout and
no data-race warning appeared. The server passed in 783.120 seconds, store in
345.804 seconds, relay in 3.415 seconds, and native-wire in 7.896 seconds.
Grok completed successfully in 1,015.441 seconds.

The sole failure was `TestCLISessionAcceptanceQueueAndArchive`, in
`internal/cli`, at `sessions_test.go:239`. Its creation-comparison `session diff`
returned `unavailable` with the workspace-file-reader guidance; the owning Worker
log retained the independent `recovery_required` workspace comparison. The CLI
package failed in 179.821 seconds. This matches the previously recorded
creation-diff boundary, rather than the repository-create timeout at line 195 in
the 18:16 run. Its cause and branch/baseline comparison remain unresolved. This
pass did not relax assertions, repeat that blocked repair or claim a complete
Go-suite pass.

Earlier failed Go/frontend runs and the historical native-wire test log-buffer
race remain preserved in their original records. Passing native-wire in this run
does not diagnose or repair that historical race. Desktop validation began after
the other Go test processes had completed, while the last Grok fixture was idle;
the complete Go command then finished before desktop validation completed.

Fresh CI and Codex review must assess the newly pushed head. Local fixture
success does not prove enterprise proxy, real provider/GitHub account, native OS
credential lifecycle, other-platform runtime, release or Worker bootstrap
acceptance. The PR remains maintained without merging or enabling auto-merge.
