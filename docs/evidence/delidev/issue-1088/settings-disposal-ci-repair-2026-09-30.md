# Settings draft-disposal CI repair — 2026-09-30

This record belongs to issue #1088 and PR #1226. The source baseline is
`b1a415ea9555b5da25f7977613457b28d54afcc1`; the tested
`apps/delidev/src/App.test.tsx` Git blob is
`c0363eac97575856721f6e11a0b4ebdb86ac41ec`. This pass changes tests and evidence
only, with no runtime, protocol, policy or ownership change.

## Observed failure and repair

The [DevHud Protocol and Client job](https://github.com/delinoio/oss/actions/runs/36717807097/job/109895105272)
passed its earlier protocol/client steps and failed in the DeliDev desktop
Vitest suite. Its combined notification/import draft-disposal test exceeded
the 5,000 ms test watchdog (reported duration 5,244 ms). The suite reported
1,268 passing tests and that one timeout. The log reported no failing disposal
assertion; the exact hosted scheduling cost was not established.

The original combined test passed in isolation locally in 1,731 ms. The repair
gives each independent draft workflow its own fresh App/Settings fixture and
test watchdog. Both still close Settings, enter Pull requests, reopen Settings
through the targeted repository entry and verify the discarded draft. The
notification case now also asserts that no notification-preference write was
sent. Neither test nor product deadlines were increased.

## Executed validation

- `pnpm --filter @delinoio/delidev-api-client build`: passed before focused tests.
- From `apps/delidev`,
  `pnpm exec vitest run src/App.test.tsx -t 'discards (notification|import) drafts on close without saving' --reporter=verbose`:
  both repaired cases passed (notification 1,097 ms; import 589 ms).
- From `apps/delidev`, `GOMAXPROCS=2 VITEST_MAX_WORKERS=2 pnpm test`: passed.
  Client build and frontend typecheck passed; all 99 Vitest files and 1,270
  tests passed (81.37 s). All eight packaging and sixteen launcher/asset
  fixtures, the macOS widget fixtures and production frontend build passed.
- `git diff --check`: passed. The two ignored generated `dist` directories
  created by validation were removed after verifying their ignored/untracked
  ownership.

## Limits

Local passes do not establish a successful fresh hosted run or native desktop
acceptance. The earlier complete local Go race-suite failures and their
qualifications remain in `review-repairs-2026-09-30.md`; this test-only pass
does not claim to repair or revalidate them. Pending CI and Codex reviews are
left for the next maintenance heartbeat after the final push.
