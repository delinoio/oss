# Schedules pane and creation merge validation

Recorded on 2026-09-30 during maintenance of
[PR #1189](https://github.com/delinoio/oss/pull/1189) for
[issue #1153](https://github.com/delinoio/oss/issues/1153).

## Revision and preservation

Tested merge revision: `9ac91d105afb958afb3a1990059aeee7e613b811`.
Main parent: `36736923edfd9fcaa177a2c9594acea223e10bdf`.
Previous issue-branch head: `4137008f1b4248204d2a5eba6827addb6bdf3e84`.

The conflicts were adjacent ownership notes in the two desktop/frontend AGENTS
files and adjacent imports in `schedules.tsx`. Both Schedules-pane ownership and
the home-only header-action/focus contract are retained. The Schedules icon import
and newly landed ScheduleCreation import are both present. Main's creation-only
frequency presets, provider picker, navigation behavior, contracts, tests and CI
sharding changes remain intact. The sidebar disclosure/draft, distinct accessible
action names, filters, rows, bounded requests and protected workflow ownership
remain unchanged by the import resolution.

## Executed verification

- Generated client build, then focused Vitest checks of
  `schedules-sidebar.test.tsx`, `schedules.test.tsx`, `App.test.tsx` and
  `sidebar.test.tsx`: 4 files / 108 tests passed.
- `pnpm test` from `apps/delidev` passed on the merge revision: generated client,
  TypeScript checking, 89 files / 1,123 Vitest tests, 8 packaging dry-run tests,
  16 launcher/asset tests, Swift widget fixtures and production Rsbuild build.
- The full run used temporary `maxWorkers: 1`, `testTimeout: 60000` and Testing
  Library `asyncUtilTimeout: 15000`. The runner restored both configuration files
  in `finally`; their diffs were empty afterward. This is a serial,
  extended-timing pass rather than a default-timing acceptance claim.
- The final repair inventory found no unresolved non-outdated Codex threads and
  no failing checks on the previously published head. New-head CI/review evidence
  must be obtained after the push; the earlier head's result is not its approval.

## Cleanup and limits

Generated API-client and app `dist` were removed after verification. A scan
excluding dependency `node_modules` and ignored native `target` found no remaining
repository-owned generated `dist`. No Rust source was changed.

Earlier [browser/native evidence](schedules-context-validation.md),
[ownership-merge evidence](main-merge-validation.md) and
[selector/accessibility evidence](selector-and-history-review-validation.md)
remain preserved. Browser/native checks were not repeated for this import-only
conflict resolution. Windows/X11, actual native 200% zoom, the full native
viewport/state matrix, native assistive-technology acceptance and signed release
acceptance remain unverified. The previously reported repository review-quota
limit is an external review constraint, not a validation pass.
