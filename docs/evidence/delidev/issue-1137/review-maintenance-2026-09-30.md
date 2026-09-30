# Issue #1137 review maintenance — 2026-09-30

This maintenance pass starts from PR head `1bed761e1801cd4e3b020521d977d007bdda2ea0`
and merges main revision `36736923edfd9fcaa177a2c9594acea223e10bdf`.

The Home sidebar navigation and consumed wide/compact focus handoff are retained
alongside the issue #1137 persistent advanced Connection & diagnostics controls.
The app and frontend ownership rules preserve both boundaries. The merge also
retains main's independent provider-picker and schedule-creation changes and
contracts without reducing their source-backed scope.

Merge verification: API client generation succeeded. The focused `App`,
`sidebar`, `settings` and `desktop` Vitest run with one worker and a 15-second
per-test bound passed all 101 tests across four files. Full frontend validation
and the four newly reported startup review findings are evaluated separately
below when their results are available.

Earlier broad-test failures and native/platform acceptance gaps remain recorded
in `launch-default.md` and `main-merge-maintenance-2026-09-30.md`; these component
checks do not establish native UI acceptance or erase those qualifications.
