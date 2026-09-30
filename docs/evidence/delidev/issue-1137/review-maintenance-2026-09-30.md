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

## Still-answering Stop review

The fresh-launch probe previously returned a stopping conflict before joining
original store ownership. Fresh launch now treats only that conflict as pending
cleanup. It retains the original intent until the store lock releases; service
registration admission still prevents a competing process. Ordinary Start,
Retry, ensure and observation retain their existing Stop behavior.

`go test -race ./cmds/delidev-cli/internal/cli -run
'^TestDesktopLaunchJoinsStillAnsweringStoppedServer$' -count=1` passed. The fixture
uses an authenticated Connect stopping-status endpoint and a separately held
original store lock, proves no replacement intent/log while held, then observes
the same pending launch start a real detached replacement after release. This
bounded synthetic stopping interval is not a native UI acceptance result.
