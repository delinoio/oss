# Restored worktree validation and unresolved local race failures

## Revision and environment — 2026-09-30

Production/test source revision: `fcf51787b451003d1b6b87f4666973f232cd8682`,
including review repairs `1fabab7d1` and `3fdd55a39`. The original checkout was
removed while its earlier full race run was active. A new managed checkout of
the same PR branch was restored without changing the primary checkout's edits.

The earlier run recorded in `full-race-and-main-baseline.md` ended with exit 1:
CLI and Claude/Codex fixture failures were followed by compilation failures after
the original directory disappeared. That run never established a full pass.
New attempts also encountered disappearing shared compiler/vet and dependency
files. The following checks therefore use task-private temporary `GOMODCACHE`
and `GOCACHE`, with `GOFLAGS=-p=1 GOMAXPROCS=2`.

## Completed checks

- `go vet ./cmds/delidev-cli/...` passed.
- Background-requested domain/Worker/server race regressions passed; see
  `background-requested-review-repair.md`.
- Failed-job diagnostic reproduction and repaired recovery race regressions
  passed; see `failed-job-diagnostic-review-repair.md`.
- From `apps/delidev`, `pnpm test` passed: 84 files, 960 frontend tests,
  bundle dry-run, desktop-launch, widget and production-build checks. The exact
  DeliDev icon LFS object was hydrated before asset checks; no source asset was
  changed.
- Both repair Go formatting hooks and `git diff --check` passed.

## Full race command and focused comparison

Started `go test -race ./cmds/delidev-cli/... -timeout=30m`. This complete run
cannot be reported as passing. Its remaining packages are still running at this
record's creation. Completed failures include:

- CLI `TestCLISessionAcceptanceQueueAndArchive`, `sessions_test.go:239`: creation
  comparison through `session diff` returned typed `unavailable`, with the
  workspace file reader unavailable. The CLI package took 178.171 seconds.
- Claude `TestStreamPreservesLateAcknowledgmentsAndCanceledReads`,
  `TestStreamRejectsProtocolFailuresAndStopsOwnedScope/duplicate-key`, and
  `TestStreamMatchesOnlyOneExactClaimedReplyEcho/mismatched-echo` failed their
  acknowledgment/owned-termination assertions. The package took 228.847 seconds.

A focused retry of the CLI test also failed at the same creation-diff read
(44.501 seconds). The same test on the source-only unchanged-main snapshot
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` failed with the same typed workspace
reader classification at `sessions_test.go:244`, in the subsequent creation
review-context read (63.325 seconds). This reproduces the failure family on the
unchanged snapshot, with a different read operation; it does not establish the
underlying cause or a fix.

Executed the three failed Claude stream test families together with
`go test -race ./cmds/delidev-cli/internal/harness/claude -run '^(TestStreamPreservesLateAcknowledgmentsAndCanceledReads|TestStreamRejectsProtocolFailuresAndStopsOwnedScope|TestStreamMatchesOnlyOneExactClaimedReplyEcho)$' -count=1`.
That retry passed in 36.862 seconds. The stream implementation and those tests
are unchanged from the inspected main snapshot. A successful retry does not
erase the failed complete run or prove its cause.

The full run's terminal result and remaining package results require a later
observation. These temporary fixtures establish no new installed-native,
hosted-account, desktop-platform or release acceptance.
