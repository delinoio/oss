# Latest Grok review maintenance validation

Starting pushed head: `642f0161da82af077def33b526414342167733f5`.
Proposal display fix: `1b6b127225571ef6d5dc69aeda465e85a305b9aa`.
Final Plan regression: `1790402c15feb442abede44c0702518b9cc9530a`.

## Review outcomes

The production request event's original `proposal_json` is accepted as bounded
inert display evidence in Session/Inbox controls and request-type transcript
observations. Historical omission remains readable; notification placement,
invalid text and unrelated keys are rejected. Go still owns original payload,
proposal-digest and response authority. See the independent retained-proposal
review record for the positive and negative fixtures.

The text-only initial Plan finding is objectively incorrect: the original
queue-running branch initializes planning/file/question observers before tools.
The final public-profile regression verifies original mode claim/binding,
pre-selection blocking, zero tool/in-prompt mode events and independent terminal
cleanup. The runtime `input.go` and reducer code remain identical to the starting
pushed head. See the independent initial-Plan review record, including failed
setup runs and the new test's separately bounded setup/input phases.

## Completed checks

- Client build and frontend typecheck: passed.
- Focused native-Grok frontend selection: two files, 119 tests passed.
- Final isolated initial-Plan race regression: passed, 30.412 seconds.
- DeliDev Go vet, `GOCACHE=/tmp/oss-1091-go-cache GOMAXPROCS=2 go vet -p 1
  ./cmds/delidev-cli/...`: passed.
- Separate bundle dry-run: eight checks passed.
- Separate desktop launch/asset fixtures: 16 checks passed.
- Widget fixture and production frontend build: passed.
- Required generated Go embeds: the first administrator build failed because
  its ignored typed-client output had been removed; building that client first
  then the administrator succeeded. The initial ach filter matched no project;
  the actual `async-commit-hook build:embedded` command then generated and
  verified the assets. No source/dependency change was needed for preparation.
- Normal commit hooks passed; no hook bypass or force push is used.

## Required complete frontend script

`pnpm test` failed in the unit phase: 20 failed files / 67 failed tests,
81 passed files / 1,240 passed tests; 101 files / 1,307 tests total, 187.85 seconds.
Its client build/typecheck succeeded, but later script phases were not reached.
The packaging/build results above came from explicit separate commands and do
not turn this script into a pass. The log contained 50 test-timeout messages and
no missing-embed diagnostics; remaining assertion/setup failures and their
causes are not explained by that count. No existing test deadlines/assertions
or production timeouts were changed, and the complete script was not retried.

Failure inventory from the completed script:

| File | Failed tests |
| --- | ---: |
| `src/App.test.tsx` | 10 |
| `src/agent-configuration.test.tsx` | 4 |
| `src/backups.test.tsx` | 1 |
| `src/desktop.test.tsx` | 3 |
| `src/device-settings.test.tsx` | 1 |
| `src/doctor.test.tsx` | 1 |
| `src/notification-presentation.test.tsx` | 1 |
| `src/pull-requests.test.tsx` | 3 |
| `src/schedules-sidebar.test.tsx` | 1 |
| `src/settings-claude.integration.test.tsx` | 1 |
| `src/settings-configuration.integration.test.tsx` | 1 |
| `src/settings-github.integration.test.tsx` | 1 |
| `src/settings-lifetime.test.tsx` | 7 |
| `src/settings-models.test.tsx` | 8 |
| `src/settings-preferences.integration.test.tsx` | 1 |
| `src/settings-projects.test.tsx` | 7 |
| `src/settings-workspace.integration.test.tsx` | 1 |
| `src/settings.test.tsx` | 11 |
| `src/subscription-controller.test.tsx` | 3 |
| `src/tray-presentation.test.tsx` | 1 |

The public/proposal Go selection also failed during native initialization
before prompt settlement, 129.123 seconds. The subsequent isolated regression
and final phase correction are recorded separately; neither erases those
failures. System load was observed above 200 during validation, but it does not
prove the cause of any failure.

## Complete Grok race run

`GOCACHE=/tmp/oss-1091-go-cache GOMAXPROCS=2 go test -race -p 1
-timeout=20m ./cmds/delidev-cli/internal/harness/grok`: failed, exit 1,
1,201.289 seconds. The package watchdog fired during
`TestInitialPlanRequiresOriginalClaimAckAndMode/mode-exit`; later cases are
incomplete. There were 29 failure headers, including parent/subtest summaries,
across these six completed top-level failures:

- `TestAPIInitializationOwnsConfigurationAndNativeAuthority`
- `TestSessionBindingRequiresOriginalReadyModeAndConfiguration`
- `TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval`
- `TestTextClosureClaimCancelsWithNativeLifetime`
- `TestOriginalFileReplyClaimsAndTerminalFaults`
- `TestOwnedInputRetainsClaimsAndRejectsUncertainReplay`

This run compiled the initial explicit-pre-mode-guard test variant before its
setup/input watchdog correction. Production Go sources remained unchanged,
and the package did not reach the new public regression before the watchdog.
The final regression passed separately at `1790402c1`; no passing complete
final-source package or repository run is claimed. The failure output includes
native initialization, delivery/cleanup uncertainty and original closure/input
checks; no unproved root cause is assigned and no retry was performed.

After the package exited, an exact executable-prefix inspection found no
remaining owned test processes. Other worktrees' processes were not touched.


## GitHub and remaining limits

The starting state query found the head OPEN and MERGEABLE but BLOCKED. The
one-shot final helper inventory still reported BLOCKED with 38 non-failing checks. The CI-stage query completed with 12 passed and 26
skipped checks. This evidence belongs to the starting head, and a new push
invalidates it. Fresh CI/review results are checked on the next heartbeat.

The original P1 automatic outside-workspace Read finding is unchanged and
unresolved pending the existing human policy decision. No filesystem
confinement, explicit risk acceptance, real hosted-account/native-platform
acceptance, complete passing repository suite or merge readiness is claimed.
Protocol definitions, generated tracked sources and Rust code were unchanged.
Repository-owned ignored generated dist directories are removed before push;
the final worktree must be clean. Preserve the historical ledger and standalone
`Closes #1091` reference.
