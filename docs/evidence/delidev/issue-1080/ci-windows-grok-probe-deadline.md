# Windows Grok probe fixture uses the existing operation deadline

Inspected GitHub Actions run `36725376220` for PR #1222 head
`04a9d22c2791ce8679502e618c27764061461188` on 2026-09-30. The independent
failure is [Go Test (windows-latest, harness)](https://github.com/delinoio/oss/actions/runs/36725376220/job/109920896513).
Its only failing test is
`TestProbeOwnsBoundedInspectedInitialization/inventory-first`: it returned
`Unavailable` with a timeout instead of the expected `Unsupported` for an
early native inventory notification. The aggregate CI Result consequently
failed. The original log is retained at
`/tmp/delidev-1222-04a9-windows-harness-ci.log`.

The fixture imposed a five-second caller deadline on a production operation
whose configuration inspection and ACP initialization already share a
ten-second bound. Structured Windows logs show the first subprocess prepared
at 14:04:55.458 UTC, resumed at 14:04:56.051 and exited at 14:04:57.076.
The ACP subprocess was prepared at 14:04:58.450 and resumed at 14:04:59.448,
then exited with initialization timeout at 14:05:00.317. The shorter caller
budget expired during this two-process sequence before the fixture's malformed
frame was observed. This test does not exercise managed backup restore.

The test now uses the existing ten-second whole-operation budget for protocol
classification fixtures. Production deadlines and behavior are unchanged.
The explicit `timeout` and `inventory-missing` cases retain their 250 ms
caller cancellation. All expected classifications, no-input/no-authentication
checks, private-content redaction and owned-process reconciliation assertions
remain intact. The comment records the Windows startup reason and its scope.
The harness contract already specifies the ten-second combined bound; no
ownership or policy change requires an AGENTS or project-index edit.

The complete local Grok race attempt
`GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/harness/grok
-count=1 -timeout 10m` failed in 601.231 s. It reported initialization failure
for `TestAPIInitializationOwnsConfigurationAndNativeAuthority/valid`, caller
timeout for `TestSessionBindingRequiresOriginalReadyModeAndConfiguration/execute`,
and delivery/initialization failures for four
`TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval` cases
(`closure-valid`, `closure-removal-first`, `closure-foreign-removal`,
`closure-wrong-outcome`). It then hit the package deadline during
`TestOwnedInputRetainsClaimsAndRejectsUncertainReplay/input-text-method`, before
executing the focused probe tests. Its terminal log is
`/tmp/delidev-1222-grok-ci-race.log`. These failures are outside the edited
probe fixture; their exact cause remains unresolved. No baseline attribution
or complete local Grok race pass is claimed. The owned subprocesses from this
attempt exited after the terminal timeout; unrelated processes were untouched.

Focused verification after that failure:

- `GOMAXPROCS=2 go test -p 1 ./cmds/delidev-cli/internal/harness/grok
  -count=1 -timeout 20m` passed in 157.885 s. This checks the complete package
  using CI's ordinary test mode and package deadline, without repeating the
  failed complete race run. Log: `/tmp/delidev-1222-grok-ci-tests.log`.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/harness/grok
  -run '^TestProbe' -count=1 -timeout 10m` passed in 34.361 s, including all
  probe classification, cancellation, redaction and cleanup assertions. Log:
  `/tmp/delidev-1222-grok-ci-probe-race.log`.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/internal/harness/grok` passed.
- `GOMAXPROCS=2 GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -p 2 -c
  -o /tmp/delidev-1222-grok-ci-windows.test.exe
  ./cmds/delidev-cli/internal/harness/grok` passed.
- The final combined source passed the Windows amd64 CLI cross-build:
  `GOMAXPROCS=2 GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -p 2
  -o /tmp/delidev-1222-cleanup-windows.exe ./cmds/delidev-cli`.
- `git diff --check` passed. No Rust, frontend or generated protocol source
  changed in either repair.

Windows runtime validation must come from new-head CI. The failing Windows
runtime was not reproduced on this macOS host, and a cross-compile does not
prove the repaired Windows test passes. This change neither reruns the old
failed job nor suppresses a failure, skips a platform or alters the CI shard.
Historical full local Go race failures remain recorded in
`full-race-terminal-70b7de3c.md`.
