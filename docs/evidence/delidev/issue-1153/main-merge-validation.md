# PR #1189 main-merge validation

Recorded on 2026-09-30 during the first maintenance pass for
[PR #1189](https://github.com/delinoio/oss/pull/1189), which addresses
[issue #1153](https://github.com/delinoio/oss/issues/1153).

## Source and resolution

Tested merge revision: `1efc3dbf2ed3c50d1c1636a63fab5f53484d575a`.
The merged main parent is `7f356266fc195b1880ffac66a93dadab5c5a2df7`;
the issue branch parent is `89e8444c8ae551a62d3f4bb155365f582548be69`.

GitHub reported a conflict after the PR was opened. The only textual conflict
was between adjacent ownership additions in `apps/delidev/AGENTS.md`. The
resolution retains both the Schedules ownership pointer from #1153 and the
independently landed Diagnostics ownership pointer. Main's API-key terminology,
Diagnostics implementation/contracts/tests and clibox release metadata were
preserved. No Schedules implementation behavior changed during this repair.

## Executed checks

- `pnpm --filter @delinoio/delidev-api-client build` followed by
  `pnpm --filter delidev-desktop exec vitest run src/schedules-sidebar.test.tsx src/schedules.test.tsx`
  passed: 2 files / 23 tests.
- `pnpm test` from `apps/delidev` passed on the merge revision: generated API
  client, TypeScript checking, 86 files / 1,014 Vitest tests, 8 packaging dry-run
  tests, 16 launcher/asset tests, Swift widget fixtures and production Rsbuild
  build.
- The full run used temporary `maxWorkers: 1`, `testTimeout: 60000` and Testing
  Library `asyncUtilTimeout: 15000`. Both configuration files were restored by
  the runner's `finally` block and verified to have no diff. This is a serial,
  extended-timing pass rather than evidence of default-timing acceptance.
- Generated client/app `dist` directories were explicitly removed after the
  run. A scan excluding dependency `node_modules` and ignored native `target`
  found no remaining repository-owned generated `dist`.

The browser matrix and macOS ad hoc native smoke from the implementation remain
recorded in [the original validation record](schedules-context-validation.md).
They were not repeated after this ownership-only conflict resolution. Windows/X11,
actual native 200% zoom, the complete native viewport/state matrix and signed
release acceptance remain unverified. The original record is preserved rather
than restated as broader acceptance of the merge.
