# Second review round validation — 2026-09-30

This record belongs to issue #1088 and PR #1226. Tested implementation revision:
`2e9d0c2f5600582b5100fbb53ca77f007b57495b`, following the independent
`b4dc31c0` output-loss repair. The preceding Settings test repair remains
`5f6800f2ee39409923379657bd946629ba23a326`.

## Review disposition

- [Output-loss thread 4144945200](https://github.com/delinoio/oss/pull/1226#discussion_r4144945200):
  repaired independently; see `output-loss-gap-review-repair-2026-09-30.md`.
- [Pre-native creation thread 4144945209](https://github.com/delinoio/oss/pull/1226#discussion_r4144945209):
  repaired independently; see `pre-native-owner-index-review-repair-2026-09-30.md`.
- [Process-wait thread 4144945192](https://github.com/delinoio/oss/pull/1226#discussion_r4144945192):
  its mutex-deadlock premise does not match the current callers. `Handle.Wait`
  and `Handle.Done` share one channel closed after native wait/pump completion.
  Ordinary exit handling enters `finishNative` only after that channel closes;
  blocked pumps are skipped by exit observation, which releases the manager
  mutex. Failed/timed-out/canceled controls and explicit close cancel output
  before closing/joining the handle. The existing failed-control race fixtures
  passed in focused verification (3.461 s). This caller assessment is code
  inspection; no new disconnected-publisher fixture was executed. The English
  explanation is prepared for the original inline thread after the final push.

## Completed checks

- Root `go vet ./cmds/delidev-cli/...`: passed.
- Root `pnpm ci:contracts`: all 113 checks passed (5.869 s).
- Root `pnpm --filter @delinoio/delidev-api-client test`: all four files and
  44 tests passed (11.38 s).
- From `apps/delidev`, `GOMAXPROCS=2 VITEST_MAX_WORKERS=2 pnpm test`: passed.
  Client build and frontend typecheck passed, all 99 Vitest files and 1,270
  tests passed (106.51 s), all eight packaging and sixteen launcher/asset
  fixtures passed, and widget fixtures plus production frontend build passed.
- `GOOS=windows GOARCH=amd64 go test -c` for both Worker and server, writing
  binaries only under `/tmp`: passed. These are test-binary cross-compilation
  checks, not native Windows execution.
- The required DeliDev icon was LFS-hydrated. After frontend validation, both
  ignored generated `dist` directories were removed with ownership checks;
  a repository directory scan found no remaining repository-owned `dist`.

## Complete Go race-suite attempt

Command from the repository root:
`GOMAXPROCS=2 go test -race -timeout=20m ./cmds/delidev-cli/...`.
The package runner limit matches the existing CI watchdog; no native protocol,
observation or cleanup deadline changed.

The command completed with exit status 1. Every package except Grok passed,
including CLI (229.577 s), process (16.540 s), server (679.541 s), store
(415.622 s), Worker (328.340 s) and workspace (456.893 s).
Grok failed (1,025.901 s): `TestOriginalPlanReplyClaimJoinsNativeLifetimeLoss`
reported a timeout, and `TestProbeOwnsBoundedInspectedInitialization` cases
`foreign`, `trailing` and `request` timed out with unavailable rather than their
expected unsupported classification. The cause has not been established.
Grok and process sources match the pre-pass revision; no separate passing
baseline comparison was performed, and these failures are not labeled baseline.
The complete command is therefore not a passing full Go validation. No isolated
Grok rerun or further deadline change was made in this repair pass.

## Limits

Local fixture/build passes do not establish complete successful hosted CI or
native Windows/Linux, remote Worker, visual desktop or release acceptance.
Earlier evidence files and the frozen ledger remain unchanged. Fresh CI and
Codex review evidence are required after the final push.
