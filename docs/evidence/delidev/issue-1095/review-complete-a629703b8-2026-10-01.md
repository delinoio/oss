# Issue #1095: review and CI maintenance after 1d15a4

The complete-run source is `63fecacdd12d07dacd13c24dec2b00331af41298`.
The subsequent lifecycle revision repair is `a629703b8fadf6e4d5c6138fbde54f590048daf6`;
its exact source was validated separately with Go overlays before copying and
committing. Both follow published head `1d15a46908f17fa882826bd73a47b324c1fea72b` and
incorporated main `6c749670727b30679e722821846bc8dc00f5ac32`.
Historical evidence, including the [previous complete run](review-complete-6c7496-2026-10-01.md), remains unchanged.

## Review repairs

- [Unpublished authentication](review-unpublished-auth-2026-10-01.md): confirmed pre-native destination absence permits unused-original completion only with private-home, synchronization and remnant proof; native-owned missing authentication remains uncertain.
- [Lifecycle contention](review-lifecycle-busy-2026-10-01.md): only the server's typed busy refusal waits with the exact original claim; shared ownership stays connected and unknown delivery is never retried.
- [Lifecycle revision](review-lifecycle-revision-2026-10-01.md): the real server accepts the original positive observation only while its exact queued operation remains authorized, so lease cleanup and metadata edits do not break an unchanged busy retry. Canceled, replaced, recovery-required and future-observation requests remain blocked.
- [Committed Finish](review-committed-finish-2026-10-01.md): presentation cancellation after mutation/final cleanup does not add a fence; failed final vault cleanup still does, and receipt replay preserves both states.

Each repair was committed separately after a failing regression on its original
behavior and a passing focused race run. All fixtures use synthetic credentials,
temporary state and repositories; they grant no real native/account acceptance.

## Published-head CI failure

[PR Windows worker job](https://github.com/delinoio/oss/actions/runs/36778031708/job/110100874426) at `1d15a4` failed:

- `TestWorkspaceDiffLiteralPathBinaryAndNoExternalHelpers`: unavailable/timeout at `diff_test.go:101`, 18.36s.
- `TestWorkspaceDiffUnbornAndBoundedResults`: unavailable/timeout at `diff_test.go:163`, 38.77s.

The Worker package passed in 267.721s; Workspace failed in 232.106s. The aggregate
CI Result failure follows that job. The remaining scheduled Go jobs on Ubuntu,
macOS and Windows core/server/harness passed; skipped jobs are not validation.

Workspace source/tests have no difference from exact incorporated main.
[Main's Windows worker job](https://github.com/delinoio/oss/actions/runs/36748535717/job/110000939456) passed Worker in 163.480s and Workspace in 158.902s.
Different runs on different hosts do not prove equal causes or repair this PR's
timeouts. No production/test operation deadline, cleanup rule, CI inventory or
native ownership check was widened or removed.

A macOS arm64 race control selected exactly those two functions at their
unchanged operation bounds:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/workspace \
  -run '^TestWorkspaceDiff(LiteralPathBinaryAndNoExternalHelpers|UnbornAndBoundedResults)$' -count 1
```

It failed both functions with `recovery_required` original-workspace proof
errors at the same assertion sites (51.35s and 66.94s; package 119.059s).
These are different classifications from the Windows unavailable/timeouts and
do not establish a shared cause. Other Go/native runs were active on this
machine; that observation alone does not prove the cause, and no unrelated
process was terminated. No passing control result is claimed.

The Windows timeout cause remains unresolved. The review fixes address
different ownership problems and require fresh CI on their published head;
cross-compilation and local controls cannot establish Windows execution success.

## Required complete Go validation

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 20m -count 1 ./cmds/delidev-cli/...
```

At exact source `63fecacdd`, the required run completed with exit 1: eighteen
packages passed, five failed and two contain no tests. Workspace passed in
567.984s. The complete final-source suite was not rerun after the independently
validated lifecycle revision repair.

Failed packages and observed phases:

| Package | Result | Observation |
| --- | --- | --- |
| CLI | failed, 424.935s | `TestCLISessionAcceptanceQueueAndArchive` (95.22s), `sessions_test.go:202`: session create `--wait` returned unavailable/timeout while workspace preparation remained claimed. |
| harness | failed, 179.109s | Claude discovery failed without verified version/protocol (12.90s); Grok discovery retained recovery because owned descendants could not be confirmed stopped (46.71s); OpenCode discovery returned unavailable/timeout (26.10s). |
| harness/claude | failed, 490.376s | `TestAPIStreamRebuildsPrivateRuntimeAndValidatesNativeAuthority/valid` (6.81s; parent 123.72s), `api_test.go:151`: Unix control write reported broken pipe. |
| harness/grok | 20-minute package timeout, 1201.116s | Active at the package deadline: `TestReadInputOwnsToolsResponsesAndNoPlainTextHistory` (1m2s), `read-write-tool` (4s). |
| server | 20-minute package timeout, 1201.769s | Active at the package deadline: `TestOpenCodeReplyAcceptanceRejectsChangedEvidenceAtomically` (13s), `user-question/claim` (1s). |

Package deadlines are cumulative. The short active subtest durations do not
establish that those subtests stalled, and timed-out packages do not provide a
complete test inventory. These results do not justify changing operation bounds,
native ownership proof or cleanup requirements.

Passing complete packages, in seconds:

| Package | Seconds | Package | Seconds |
| --- | ---: | --- | ---: |
| apiproxy | 2.320 | connections | 36.205 |
| credentials | 5.639 | domain | 7.698 |
| forwarding | 2.379 | harness/codex | 323.106 |
| harness/nativewire | 14.400 | harness/opencode | 86.590 |
| integrations/github | 14.593 | presentation | 2.779 |
| process | 14.917 | providers | 10.633 |
| security | 1.716 | store | 559.875 |
| subscription | 1.476 | userservice | 5.190 |
| worker | 198.672 | workspace | 567.984 |

The command root and `internal/rpc` contain no tests. Full output was retained
outside the repository in `go-race-complete.log` for this maintenance run; it is
not bundled into the product or a public documentation surface.

The later lifecycle revision source at `a629703b8` has separate focused evidence:
five positive regressions failed on the unchanged server and twelve negative
controls passed; all seventeen passed with the fix (17.752s). The entire existing
server subscription selection passed with the fix (57.439s), as did server vet
and Windows amd64 test-package cross-compilation. The exact overlay source/test
bytes match the committed files after formatting hooks. See the independent
[lifecycle revision record](review-lifecycle-revision-2026-10-01.md) for commands,
revision binding and the original Worker fixture's limits.

## Other executed checks

Passed at complete-run source `63fecacdd`:

```sh
GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...
DEVHUD_PROTO_BASELINE=6c749670727b30679e722821846bc8dc00f5ac32 pnpm proto:check
node --test scripts/ci/delidev-structure.test.mjs scripts/ci/delidev-proto.test.mjs
GOMAXPROCS=2 go test -p 1 ./protos/...
```

All six structural/allocation tests passed; bindings reproduced without drift.
The same six structural/allocation tests also passed after the lifecycle
revision contracts and scoped instructions were committed at `a629703b8`.
From `packages/delidev-api-client`, `pnpm typecheck` and `pnpm test` passed
(46 tests across five files, 7.14s). Both changed test packages also compiled
for Windows amd64:

```sh
GOMAXPROCS=2 GOOS=windows GOARCH=amd64 go test -p 1 -c \
  -o /tmp/delidev-1095-maintenance-225SLsEq/worker-windows-amd64.test.exe ./cmds/delidev-cli/internal/worker
GOMAXPROCS=2 GOOS=windows GOARCH=amd64 go test -p 1 -c \
  -o /tmp/delidev-1095-maintenance-225SLsEq/server-windows-amd64.test.exe ./cmds/delidev-cli/internal/server
```

The actual output files were under this run's external temporary directory.
Compilation is not native Windows execution or resolution of the CI timeout.

The required embedded administrator and async-commit-hook distributions were
generated before root Go formatting hooks. Existing LFS assets were hydrated;
all repository-owned generated distributions are removed from the final tree.
Generated binaries remain outside the repository. No Rust or frontend source
changed, so their complete suites were not rerun in this Go-only repair.
The previously recorded frontend failures and acceptance limits remain visible.

## Remaining limits

Real-account OAuth/inference, installed-native/platform/release acceptance,
desktop lifecycle controls and complete uncertain-lease recovery remain
unverified. Managed Fork remains explicitly unavailable under the API-only
coordinator, and database restore does not restore external vault authority.
This PR does not complete issue #964. CI/review on a previous published head
cannot establish approval or readiness of the new head.
