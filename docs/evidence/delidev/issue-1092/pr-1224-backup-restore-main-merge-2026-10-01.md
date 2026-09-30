# PR #1224: reconcile managed backup restore from main

During the two-review-finding repair, the final remote status changed from
mergeable to conflicting after main merged managed backup restore (#1222).
The branch merges `9efb1917e` without rebasing. Both Fork and restore ownership
instructions are preserved. `GetStatus` retains all implemented capabilities,
including Fork (13), stopped-account switching (5) and managed restore (7).
Generated Go and TypeScript outputs are regenerated from the combined split
schema and compatibility pipeline, not chosen from either merge side. No numeric
allocation or migration reservation changes are introduced.

The composed storage boundary retains published child fork metadata and original
execution selection while restoring paused/recovery-required state. Claimed fork
ownership prevents database replacement; a refused restore cannot freeze the
server epoch or change the original source/job. Database publication never proves
native cleanup or grants continuation. These are controlled fixture observations,
separate from installed native execution.

Completed validation on macOS arm64:

- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/server -run 'BackupRestore.*Fork' -count=1 -timeout=5m`
  passed (7.868s), including both new cross-feature regressions.
- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/cli -run 'Backup|Restore|Fork|SessionDeletion|AccountSwitch|UserService' -count=1 -timeout=15m`
  passed: server 356.254s, store 413.525s, Worker 18.003s and CLI 11.264s.
- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/security -count=1 -timeout=3m`
  passed (1.742s). Root `GOMAXPROCS=2 go vet -p=1 ./cmds/delidev-cli/...`
  also passed after the merge.
- Root `pnpm proto:generate` and `pnpm proto:check` passed, including lint,
  breaking compatibility and deterministic generated-source freshness.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/proto-breaking.test.mjs scripts/ci/delidev-structure.test.mjs`
  passed all seven contract tests, including numeric reservations and the
  pointer-only LFS baseline check.
- `VITEST_MAX_WORKERS=1 pnpm test` from `packages/delidev-api-client` passed
  all 46 tests in five files. The desktop pipeline's client build and TypeScript
  typecheck also passed.

## Timing diagnostics

The first merged desktop pipeline reported 20 UI failures in three files and was
interrupted to investigate. A single failing New Project test, rerun alone at the
default limit, failed with `Test timed out in 5000ms`; it then passed in 2.09s with
an invocation-only 30-second diagnostic limit. No source test timeout was changed,
and that diagnostic is not a standard full-suite pass.

The first merged installed-Codex fork invocation failed in General Chat's
discovery wait, before reaching Fork. The Worktree subtest completed the
two-repository fork and both child continuations. Logs show the first Codex
initialization was unavailable, then subsequent Worktree native handshakes,
fork publication/replay and child continuation completed. The overall invocation
remained failed (241.700s).

The host load average was 231.78 during diagnosis. This supports resource
contention as an explanation but does not convert failures into passes. The
fresh standard desktop invocation after the selected Go checks completed passed:
`VITEST_MAX_WORKERS=1 GOMAXPROCS=2 pnpm test` from `apps/delidev` completed
all 1,279 UI tests in 100 files (192.52s), eight bundle dry-run checks, 16 asset/
desktop-launch checks, widgets and the production build. The earlier pre-merge installed
acceptance remains separately recorded in the
[preparation evidence](pr-1224-fork-preparation-scope-2026-10-01.md).

The previously failed General Chat native case was then rerun after our other
checks finished:
`DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex GOMAXPROCS=2 go test -p=1 ./cmds/delidev-cli/internal/cli -run '^TestManualNativeCLISessionFork$/^general-chat$/^execute$' -count=1 -timeout=10m`
passed (20.685s), including the fork and child continuation. Together with the
successful Worktree subtest above, both merged native scenarios have positive
results; the initial combined invocation remains recorded as failed.
These fixtures use installed Codex `0.151.0`, fresh private state and a scripted
keyless loopback provider, without existing user credentials or hosted inference.

`git diff --check` and the staged equivalent passed. Both generated client/desktop
`dist` directories were removed after validation. No Rust source changed.

The historical evidence ledger remains unchanged. These checks do not establish
unfiltered Go-suite, hosted-account, packaged desktop or fresh pushed-head
CI/review acceptance.
