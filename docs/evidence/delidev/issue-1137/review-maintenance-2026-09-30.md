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

## Legacy listener/configuration review

Desktop launch/Retry now validate the authenticated status listener against the
requested listener and stored endpoint before reuse. They leave absent legacy
lifecycle configuration absent even for a compatible listener, because status
cannot prove original TLS paths or allowed origins. Ordinary explicit startup
retains its original legacy adoption behavior.

The race-enabled stopping, legacy-listener and initialization/Stop regressions
passed together (three tests). The legacy fixture uses an actual foreground
server with its lifecycle record removed to represent a pre-lifecycle process;
it proves an alternate listener stays untouched, compatible live reuse does not
publish invented origins, and ensure does not restart it after exit.

## Relative launch-admission scope review

Launch admission now applies `filepath.Abs` before `filepath.EvalSymlinks`,
matching existing service-scope ownership. Ordinary relative `--data-dir`
ensure remains observational for an unconfigured scope and does not create an
owner or lifecycle record. Relative and absolute spellings of an installed
scope retain the same stopped registration and contend for the same admission
lock, without changing process cwd or user configuration.

`go test -race ./cmds/delidev-cli/internal/cli
./cmds/delidev-cli/internal/userservice -run
'TestLaunchAdmission|TestDesktopLaunchAdmissionPreservesRelativeEnsureScope'
-count=1` passed both packages, including the existing concurrent native-control
admission regression.

## Aggregate startup deadline review

Automatic/desktop startup now shares one 35-second context deadline across
admission, startup-controller joining, original store ownership and readiness.
Each nested phase can only shorten that deadline. Admission/controller locks
check cancellation before and after acquisition; intent publication and native
spawn also check it. An interruption after retaining running intent returns a
truthful recovery-required outcome without falsely claiming process startup or
cleanup. Ordinary explicit Start keeps its original per-phase bounds.

`go test -race ./cmds/delidev-cli/internal/cli
./cmds/delidev-cli/internal/userservice -run
'TestDesktopLaunch|TestLaunchAdmission' -count=1` passed both packages. The
aggregate test deliberately supplies no caller deadline, holds service admission
for 18 seconds and then continues to hold the startup controller. It returns
within the shared 35-second budget instead of adding a fresh 20-second wait,
with neither lifecycle publication nor a detached process log. Separate canceled
uncontended-lock checks prove cancellation cannot acquire fresh admission.
