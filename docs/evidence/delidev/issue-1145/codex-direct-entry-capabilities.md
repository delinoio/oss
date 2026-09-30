# Issue #1145 direct-entry capability review repair

Validated the repair working tree based on `218b6a2c` on 2026-09-30 for
[the Codex finding](https://github.com/delinoio/oss/pull/1183#discussion_r4142192400).

An API Providers row can expose its Add action with the original three inventory
capabilities. Its direct entry previously enabled name/key fields while account
or independent picker inventory lacked the required four gates. The existing
details fieldset now remains natively disabled until both inventories report
those gates, and disables again if either loses them. Existing explicit submit,
retry and late-continuation capability guards remain authoritative.

Two Strict Mode component cases cover independently unavailable account/picker
inventory, readiness recovery, later capability loss and no writes. A Settings
router regression follows the actual API Providers Add action on a server
without account-type filtering and verifies disabled details and no save/connect.

## Validation

- `pnpm typecheck`: passed.
- `pnpm exec vitest run src/account-settings.test.tsx src/settings.test.tsx
  --maxWorkers 1 --no-file-parallelism --testTimeout 30000`: **all 62 tests passed**.
  This includes both review repairs and the existing account/Settings regressions.
- Final required `GOCACHE=/tmp/issue-1145-go-cache pnpm test`: **passed completely**.
  API-client build, frontend typecheck and all **1,069 tests across 88 files**
  passed, followed by all eight package checks, all sixteen desktop-launch checks,
  widget fixtures and the frontend build. No repository timing or concurrency
  setting was changed; only the already-used private temporary Go cache was set.
- `git diff --check` passed; generated app/API-client `dist` directories were
  removed afterward.

The earlier failing full-suite results are preserved in
[the main-merge record](main-merge-maintenance-2026-09-30.md) and
[the original picker evidence](provider-picker.md). Their timing/lookup failures
are not retroactively reclassified. The final passing run is automated component,
temporary-server and build evidence; native CEF, supported-platform packaging
and real hosted-provider acceptance remain outside this result.
