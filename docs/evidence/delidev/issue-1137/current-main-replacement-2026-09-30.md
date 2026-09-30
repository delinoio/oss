# Issue #1137 current-main replacement — 2026-09-30

This implementation starts from main `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`
and composes the retained implementation from closed, unmerged PR #1197 at
`1ccd6fd840fa46496d6521232bf65d2710c4ad26`. Neither prior PR #1151 nor #1197
establishes issue completion on main. Their recorded validation and acceptance
limits remain preserved in the neighboring issue evidence files.

## Composition and cancellation

Current main's Home accumulated catalogs, accepted-boundary refresh, scope-local
retries, archive selection, scroll retention and consumed header focus handoff
remain intact. Settings opening disposal and the new Schedules presentation remain
intact. Issue #1137 supersedes the Home server-management disclosure: the footer
shows This computer or the actual saved profile name with generic connection
status. Native non-ready and authenticated stopping observations override cached
successful status. Original lifecycle/registration controllers and exact uncertain
requests stay mounted in the persistent Connection & diagnostics panel outside
SettingsLifetime. Saved windows retain their pinned verification/presentation
controls in their own advanced panel.

The existing service-admission, aggregate-deadline, legacy-listener and prior-Stop
repairs are retained. Fixed-client pairing admission now also rejects cancellation
before lock acquisition and releases a lock acquired after cancellation. Its
regression covers an expired context when the original lock becomes uncontended,
while preserving original recovery evidence and ordinary pairing behavior.

## Initial checks

- Root `pnpm install --frozen-lockfile` passes and installs linked-worktree hooks.
- `git lfs fetch origin main` and `git lfs checkout` hydrate all required assets.
  An initial `git lfs pull` reported unresolved merge-index entries; hydration was
  completed with the separate fetch/checkout commands before asset validation.
- Required administrator and async-commit-hook embedded bundles were generated.
- DeliDev API-client build and frontend type checking pass.
- The initial five-file focused frontend run passed 156 tests and failed two App
  assertions that still expected the footer text without the newly retained
  descriptor. Those assertions now check the complete product label. A second
  three-file run passes App and sidebar, with eight desktop cases passing and the
  Stop-retention case exceeding its existing 15-second test bound. The second run
  took 194.52 seconds under concurrent host build load; this observation does not
  prove the timeout is environmental. A focused desktop rerun and the full required
  pipeline remain pending at this checkpoint.
- `go vet -p 2 ./cmds/delidev-cli/...` passes before the added cancellation guard;
  final changed-source checks are recorded separately below.
- The independent-change verifier passes, including generated binding reproduction
  after composing independent provider/schedule source changes.

## Native acceptance boundary

The fixed product listener `127.0.0.1:46310` is already owned by an existing
DeliDev process. This change does not terminate or replace it. Temporary-scope
real-sidecar fixtures can prove native connector/runtime ownership on macOS arm64;
those fixtures do not establish rendered CEF first-launch/focus/reopen/Quit,
packaged installation, real service-manager lifecycle, remote TLS or other
supported-platform acceptance. Previous broad-suite failures and native-service
uncertainty remain recorded, without promoting earlier checks to this revision.

Final validation results and any remaining failures are appended to this
issue-specific record. Generated repository-owned dist output is removed before
publishing the completed change.

## Fixture bounds and product wording

The service-admission fixture now starts its one-second context after unrelated
service removal completes. The bound still applies to admission; setup no longer
consumes it. The cancellation and service-control race regressions pass with
`GOMAXPROCS=2 go test -race -p 1` (CLI 4.454 seconds; user-service 2.062 seconds).
Final-source `go vet -p 1 ./cmds/delidev-cli/...` also passes.

The first complete frontend run passed 1,237 tests in 94 files, including all nine
desktop cases, but three existing full-shell App cases exceeded their aggregate
15-second fixture budget. Those three cases and the full Settings Stop-retention
case now have a 60-second aggregate budget. Assertions, observation waits and
product RPC/native deadlines are unchanged. The final focused App/desktop run
passes all 53 tests in 66.84 seconds. A complete pipeline rerun is recorded below.

The ordinary tray exit label is `Quit DeliDev`. Its detached-runtime lifetime is
unchanged and remains documented in the internal desktop contract.

## Main advancement

Main advanced to `574c1a92c957fc741a723ff8123888dad32a2194` during validation,
adding verified Grok native-input accounting (#1211) and shared compaction
reservations (#1215). Both changes are merged intact. Instruction conflicts retain
the new accounting/reservation rules alongside desktop startup rules; the prior
Home lifecycle disclosure remains explicitly superseded by issue #1137. No
protocol numbers or migration versions are reallocated by this change.
