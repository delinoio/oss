# PR #1232 service completion CI repair

## Revision and failure

Repair: `d812daa1cbe2cebbcf0c0dcc1f26314e25b30fcf`. Parent: `ec025a7fa3526ba1d26f57f453ebbddb1d4000b8`, branch
`kdy1/delidev-browser-1087-replacement`, 2026-09-30.

The initial [Windows worker shard](https://github.com/delinoio/oss/actions/runs/36717929292/job/109895494511)
failed `TestRepeatedLifecycleAndReceiptReplayPreserveNewStart` at its second
`WaitStopped`, after `user_service_controller_unconfirmed` reported
`stage=cleanup_publication` and `code=recovery_required`. All other packages in
that shard passed. The service implementation was unchanged by the browser PR
at that revision.

Inspection found that status readers hold the state gate while validating and
reading the runtime record, but final positive publication held only the runtime
lock. These operations could overlap. Windows replacement must respect open
handle sharing modes; see [Microsoft CreateFile documentation](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew).
The CI diagnostic does not retain the underlying filesystem error, so the exact
Windows sharing error remains an inference from the publication stage and this
ordering gap, rather than a directly observed errno.

## Repair

After the product controller and intent observer join, acquire the existing
bounded state gate independently of the canceled controller context. Retain the
runtime lock through atomic positive-completion publication. A gate or write
failure still leaves completion uncertain. No retry, PID-only completion claim,
native service-manager write or cleanup-authority shortcut is added.

The regression fixture holds the state gate and an open runtime reader while
canceling and joining the product controller. It checks that the runtime claim
remains owned and incomplete, then releases the reader/gate and confirms durable
completion and stopped status. The unchanged implementation fails this test on
macOS because it exits during observation; the same held reader also exercises
Windows replacement sharing restrictions when run there.

## Executed validation

- Negative regression: temporarily run the new test with the parent's original
  `manager.go`, restoring the repaired source in a `finally` block. Command:
  `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/userservice -run '^TestCompletionPublicationWaitsForStateReaders$' -count=1`.
  Exit 1, expected `controller exited during state observation: <nil>`.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/userservice -count=10`:
  passed ten complete package runs in 38.704 seconds on macOS.
- `GOMAXPROCS=2 GOOS=windows GOARCH=amd64 go test -c -o /tmp/delidev-1232-userservice-windows.test.exe ./cmds/delidev-cli/internal/userservice`:
  passed. This is cross-compilation, not Windows execution.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...`: passed.
- `pnpm proto:check`: lint, breaking checks and generated freshness passed.
- DeliDev API client `pnpm build`, `pnpm lint`, and
  `GOMAXPROCS=2 GOFLAGS=-p=1 pnpm test`: passed, all 44 tests in four files.
- Explicit DevHud administrator and async-commit-hook embedded builds passed
  before the broad Go check. Git LFS integrity and `git diff --check` passed.
- Six protocol/structure tests passed with
  `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`.
- Full `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/...`: completed
  with exit 1. Service (4.786 seconds), storage (177.555 seconds), Worker
  (161.047 seconds), API proxy, connections, credentials, domain, forwarding,
  harness core, Claude, native wire, OpenCode, GitHub integrations, presentation,
  process, providers and security passed. Failures remained in unchanged areas:
  - CLI `TestCLISessionAcceptanceQueueAndArchive`: unavailable creation
    comparison workspace read; package failed after 188.548 seconds.
  - Codex `TestSteerInspectionDefiniteRejectionNeverNeedsHistoryOrResends/steer-reject`:
    expected conflict, received recovery-required; package failed after 117.651 seconds.
  - Grok reached the ten-minute package deadline while running
    `TestQuestionControllerOriginalClaimsAndUncertainty/question-missing-automatic-resolution`.
  - Server reached the ten-minute package deadline while running
    `TestScheduleRPCCursorScopeEpochAndRetainedOrder`.
  - Workspace reached the ten-minute package deadline while running
    `TestPRPreparationRejectsChangedRemoteWithoutAlteringOriginalCheckout/fail`.
  The entire run completed; no passing full Go suite is claimed. The recorded
  active cases identify the package-deadline snapshots, not a proven individual
  test hang or root cause. Preserve the preceding full-suite qualifications.
- Removed generated repository-owned `dist` output after checks.

Native Windows execution awaits the repaired PR's CI. Browser native/provider,
platform and release acceptance gaps remain as recorded in the preceding browser
records. This CI repair changes Go service completion and its documentation;
frontend and Rust sources are unchanged.
