# PR #1232 outbound-proxy merge validation

The browser branch at `888dfb03c1bedb7f9ec3fee4195c2a56259905fc`
merges main's `ef5dbf897` outbound-proxy implementation from PR #1216.
Both browser and outbound-network instruction paragraphs are retained. The
combined canonical schema imports both services; Buf and the compatibility pass
regenerate the Go and TypeScript reflection facades instead of selecting either
generated conflict side. Current Device/browser removal retention and current
network profile/route retention compose in the same managed-restore safety image.

Executed on macOS on 2026-10-01, using hydrated LFS assets and generated
administrator/async-commit-hook embedded assets:

- `pnpm proto:generate`, `pnpm proto:lint`, and `pnpm proto:breaking`: passed.
- Protocol and structure Node fixtures: all 6 passed.
- DeliDev API-client tests: all 47 passed across 6 files.
- Focused Go race tests for `TestBrowser`, `TestNetwork`, and the current-network
  restore cases: server passed in 14.155 seconds; store passed in 3.305 seconds.
  The generated Go binding package compiled and contains no tests.
- The unchanged six-case workspace-recovery test passed locally in 16.360 seconds.
  This does not disprove its recorded Windows replacement-Worker attachment
  failure in run `36796466169`; that CI problem is handled separately.

This evidence does not establish live proxy/provider login, native browser
acceptance, release acceptance, or a successful complete Go suite after the merge.
Historical validation records remain unchanged.
