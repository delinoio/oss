# Full local race run and unchanged-main CLI comparison

## Revisions and commands — 2026-09-30

The issue implementation was committed as
`752ec8d45f8b54da41c3fae3f83fef8d150cc8d3`, with the separately executed
failed-settings regression committed as
`04627225e30e5af82b31f38694bb8109f69ed7b6`.

The full local command is
`GOFLAGS=-p=1 GOMAXPROCS=2 go test -race ./cmds/delidev-cli/... -timeout=30m`.
Limited package concurrency does not isolate the machine from other concurrent
Go/frontend suites. Its CLI package finished with failure after 581.464 seconds:

- `TestCLIPairWorkerAndInspectRealRepository`: repository inspection returned
  typed `unavailable` / operation timed out; the fixture also reported that its
  Worker did not stop within its cleanup wait.
- `TestCLISessionAcceptanceQueueAndArchive`: `session create --wait` returned
  typed `unavailable` / operation timed out while workspace preparation was
  claimed. This occurred before native execution.

## Unchanged-main comparison

A source-only archive of freshly fetched main
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` was extracted into an independent
temporary directory with Go module files, DeliDev source and its generated Go
protocol bindings. It did not consume repository LFS assets, generated UI assets,
user state or provider credentials.

Executed:
`GOFLAGS=-p=1 GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/cli -run '^(TestCLIPairWorkerAndInspectRealRepository|TestCLISessionAcceptanceQueueAndArchive)$' -count=1 -timeout=10m`.

Both tests failed on unchanged main with the same typed repository-inspection and
workspace-preparation timeout classifications, at `devices_test.go:138` and
`sessions_test.go:202`. The package completed with exit 1 after 105.061 seconds.
This independently reproduces those failures without the issue implementation;
it does not establish their underlying cause or eliminate their unresolved status.

## Remaining validation

The complete local race command is still running in the remaining packages and
already cannot be reported as passed. The separately completed focused issue race
regressions and Go vet passed as recorded in
[verified failed Resume](verified-failed-resume.md). Remaining full-suite results
are not established by those focused passes or by the CLI baseline comparison.

Real required administrator/async-commit-hook embeds were generated before both
Go formatting hooks passed, then all five generated repository-owned dist
outputs were removed. No frontend, Rust or schema sources changed.

This evidence uses temporary SQLite, Git, process, native and provider fixtures.
It establishes no new installed-Claude, hosted-account, desktop-platform or
release acceptance.
