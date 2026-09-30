# Third terminal repair validation

This maintenance pass started on 2026-09-30 UTC and validates implementation
`187bde31dfd8042427e5884b20e886789f0c7fe8`, after merging main
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`. It fixes replacement-instance close
journal recovery; the independent merge and review records retain focused
commands and the failing pre-fix regression.

## Passing checks

- Complete focused terminal Worker race suite, three repetitions: 22.652 s.
- `pnpm proto:check`: lint, breaking comparison and generated freshness passed.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`: passed.
- `pnpm ci:contracts`: all 113 tests passed (33.384 s).
- Generated API client `pnpm test`: four files / 44 tests passed (113.58 s).
- Desktop type checking and API client build passed before the full desktop
  test invocation.
- Desktop `pnpm prepare:assets`, `pnpm test:bundle-dry-run`,
  `pnpm test:desktop-launch`, `pnpm test:widget` and `pnpm build`: passed
  separately. This includes eight packaging tests, 16 asset/launcher tests,
  Swift widget fixtures and the production frontend build.
- Windows amd64 Worker and server test binaries cross-compiled with
  `GOMAXPROCS=2 GOOS=windows GOARCH=amd64 go test -p 2 -c -o <temporary-binary>`
  for their respective packages. No Windows binary was executed.
- `git diff --check`: passed. Required LFS icon content was hydrated. Generated
  app/client `dist` output was removed; the final repository scan found none.

## Desktop failures

Required `GOMAXPROCS=2 pnpm test` in `apps/delidev` exited nonzero: 27 failed /
73 passed files; 132 failed / 1,138 passed / 10 skipped tests (1,280 total,
336.19 s). Failures included five-second test watchdogs and failed temporary
Settings fixture Go builds. Concurrent other-worktree test processes were
observed, but that observation does not establish the cause of every failure.

The controlled follow-up `GOMAXPROCS=2 pnpm exec vitest run --maxWorkers=2`
kept the original test deadlines and completed with 98 passed / two failed files,
1,277 passed / three failed tests (302.44 s). Remaining five-second timeouts were
the App uncertain New Project close/reopen case and Settings draft-disposal and
unfiltered-provider-picker cases. This is not a passing complete desktop suite.
No fixture or product deadlines were changed.

## Complete Go failures

Root `GOMAXPROCS=2 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...`
completed with exit status 1. The package watchdog matches CI; native protocol,
observation and cleanup deadlines remain unchanged. Seven packages failed:

- API proxy: revocation refusal assertion, 2.845 s. A temporary Go overlay that
  changed only its failure diagnostic to show status and credential counters
  passed 30 isolated repetitions (1.797 s); it did not reproduce or explain the
  full-run failure. No repository source or assertion was changed for that probe.
- CLI: session acceptance workspace file reader unavailable, 405.019 s.
- Harness discovery: Claude cleanup uncertainty and Grok/OpenCode native
  validation timeout/unavailable outcomes, 116.383 s.
- Claude: valid API stream delivery uncertainty, 373.282 s.
- Grok: initialization/creation/Stop fixture failures, followed by the unchanged
  20-minute package watchdog (1,201.073 s).
- Server: PR remediation workspace preparation failures and the 20-minute
  package watchdog (1,200.914 s).
- Workspace: Local PR match did not prove the accepted preparation, 446.150 s.

Other packages passed, including the complete Worker (270.092 s), store
(660.071 s), terminal (1.712 s), process (14.098 s), Codex (299.428 s), OpenCode
(57.904 s), native-wire (10.076 s), GitHub integration (11.967 s) and user-service
(13.585 s). No separate passing baseline comparison was performed. These
unresolved failures are not classified as baseline or dismissed as host load.

## Hosted evidence and limits

The initial PR head `7e450cad583e00b06e7f876dde98d79f41fad043` exposed only a
failed Cloudflare Pages check. GitHub provided its build-failed summary and
dashboard link, but no build logs; the external check remains report-only.
Older run `36720147844` on the previous head failed Ubuntu forwarding deletion
cleanup and Windows detached Worker offline Stop with a scope-lock conflict.
Those logs are historical evidence, not current-head validation.

Fresh CI and Codex review evidence are required after push. Fixture/build passes
do not establish physical remote Worker, installed Windows/Linux, native desktop
visual, real-account or release acceptance. Historical records and the frozen
ledger remain intact.
