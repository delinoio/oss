# Schedules pane, Agent Workers and Subscription merge validation

Recorded on 2026-09-30 during maintenance of
[PR #1189](https://github.com/delinoio/oss/pull/1189) for
[issue #1153](https://github.com/delinoio/oss/issues/1153).

## Revision and preservation

Tested merge revision: `8262f62152a58a0a88c8e3dc50b622b66a39cd3e`.
Main parent: `82020a7ef2916534f3342aa5208f43d2a051b052`.
Previous issue-branch head: `b23ebab7d4f43fb5109a61376cc79c88503ab2ff`.

The conflicts were adjacent ownership notes in the desktop and frontend AGENTS
files. Schedules ownership, Agent Workers presentation and the bounded
unsupported-schema Agent-name projection rules are all retained. The shared
stylesheet and desktop contract merged without source conflicts. Main's
Subscription/Agent Workers implementations, Settings lifecycle behavior,
subscription contract and original bundled mark notices remain intact. The
subscription/account controllers, Settings implementation and mark directory
match the fetched main exactly. The Schedules root scope, filters, selected rows,
retained-history disclosure/draft and distinct accessible action names remain
unchanged by the ownership-note resolution.

## Executed verification

- Generated client build, then focused Vitest checks of
  `schedules-sidebar.test.tsx`, `schedules.test.tsx`, `App.test.tsx`,
  `sidebar.test.tsx`, `settings.test.tsx`, `settings-projects.test.tsx`,
  `subscription-controller.test.tsx`, `subscription-settings.test.tsx` and
  `settings-lifetime.test.tsx`: 9 files / 215 tests passed with unchanged
  checked-in timing settings.
- `pnpm test` from `apps/delidev` passed on the merge revision: generated client,
  TypeScript checking, 93 files / 1,189 Vitest tests, 8 packaging dry-run tests,
  16 launcher/asset tests, Swift widget fixtures and production Rsbuild build.
- The full run used temporary `maxWorkers: 1`, `testTimeout: 60000` and Testing
  Library `asyncUtilTimeout: 15000`. The runner restored both configuration files
  in `finally`; their diffs were empty afterward. This is a serial,
  extended-timing pass rather than a default-timing acceptance claim.
- The final repair inventory found no unresolved non-outdated Codex threads and
  no failing checks on the previously published head. New-head CI/review evidence
  must be obtained after the push; the previous head's results are not approval
  of the new head.

## Cleanup and limits

Generated API-client and app `dist` were removed after verification. A scan
excluding dependency `node_modules` and ignored native `target` found no remaining
repository-owned generated `dist`. No Rust source was changed.

Earlier browser/native evidence and all issue-owned repair records remain
preserved. Browser/native checks were not repeated for this ownership-note
resolution. Windows/X11, actual native 200% zoom, the full native viewport/state
matrix, native assistive-technology acceptance and signed release acceptance
remain unverified. The merged Subscription fixtures do not establish real-provider
login, quota collection or native acceptance. Remote checks and review approval
remain separate from executed local validation.
