# Schedules pane and Projects ownership merge validation

Recorded on 2026-09-30 during maintenance of
[PR #1189](https://github.com/delinoio/oss/pull/1189) for
[issue #1153](https://github.com/delinoio/oss/issues/1153).

## Revision and preservation

Tested merge revision: `5acc28469ac704a69274ed1249dfc12f18856a39`.
Main parent: `b1b3e9e7c55511086a284021850426d48484b127`.
Previous issue-branch head: `1fe941b8848cc19655cc4b791729b339b1776b57`.

The conflicts were adjacent ownership notes in the desktop and frontend AGENTS
files. Both Schedules-pane ownership and the new Projects-only Settings contract
are preserved. The shared ResourceChoice and stylesheet merged without source
conflicts. Main's Projects form/list presentation, Settings lifecycle safeguards,
Claude continuation repairs and CI test-compilation changes remain intact. The
merged Go and CI implementation trees match the fetched main exactly. The PR
continues to contain only the Schedules presentation, regression tests, contracts
and issue-owned evidence relative to that main revision.

## Executed verification

- Generated client build, then focused Vitest checks of
  `schedules-sidebar.test.tsx`, `schedules.test.tsx`, `App.test.tsx`,
  `sidebar.test.tsx`, `settings-projects.test.tsx` and
  `agent-configuration.test.tsx`: 6 files / 143 tests passed with unchanged
  checked-in timing settings.
- `pnpm test` from `apps/delidev` passed on the merge revision: generated client,
  TypeScript checking, 90 files / 1,139 Vitest tests, packaging dry-run tests,
  launcher/asset tests, Swift widget fixtures and production Rsbuild build.
- The full run used temporary `maxWorkers: 1`, `testTimeout: 60000` and Testing
  Library `asyncUtilTimeout: 15000`. The runner restored both configuration files
  in `finally`; their diffs were empty afterward. This is a serial,
  extended-timing pass rather than a default-timing acceptance claim.
- The final repair inventory found no unresolved non-outdated Codex threads and
  no failing checks on the previously published head. Codex's code/security
  summary completed for that previous head; new-head CI/review evidence must be
  obtained after the push. These earlier results do not approve the new head.

## Cleanup and limits

Generated API-client and app `dist` were removed after verification. A scan
excluding dependency `node_modules` and ignored native `target` found no remaining
repository-owned generated `dist`. No Rust source was changed.

Earlier [browser/native evidence](schedules-context-validation.md),
[ownership-merge evidence](main-merge-validation.md),
[selector/accessibility evidence](selector-and-history-review-validation.md) and
[creation/navigation merge evidence](schedule-creation-merge-validation.md)
remain preserved. Browser/native checks were not repeated for this ownership-note
conflict resolution. Windows/X11, actual native 200% zoom, the full native
viewport/state matrix, native assistive-technology acceptance and signed release
acceptance remain unverified. Pending remote checks and review approval remain
separate from executed local validation.
