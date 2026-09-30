# Issue #1095: complete review maintenance after main 6c7496

The implementation source is `54403048beada6db90f1ff41ff508962ace487bb`,
incorporating main `6c749670727b30679e722821846bc8dc00f5ac32`. The later
`de44e056fdf75f363979d3700830693a952861a7` adds frontend evidence only.
This record preserves every earlier validation result and qualification,
including the [prior complete review record](review-complete-validation-2026-10-01.md).

## Repairs and focused evidence

- [Main reconciliation](review-main-6c7496-2026-10-01.md) preserves fork/restore ownership and regenerates bindings without reallocating protocol or migration reservations.
- [Acknowledged fenced execution](review-acknowledged-fence-2026-10-01.md) retains the started claim and prevents ordinary reporting when credential capture/cleanup is uncertain. All three regression cases failed on unfixed source and passed after repair; confirmed unused-original failures remain ordinary.
- [Runner Device guidance](review-runner-device-2026-10-01.md) corrects new subscription-facing errors while preserving technical identifiers and behavior.
- [Restore ownership](review-restore-subscriptions-2026-10-01.md) refuses unsettled subscription state and fences disconnected historical references because the external vault is not restored.
- [Fork authentication](review-fork-authentication-2026-10-01.md) rejects managed sources before acceptance/native work until Fork owns a protected lease and joined write-back.
- [Frontend/client evidence](review-frontend-6c7496-2026-10-01.md) retains the full failures and isolated controls; no complete frontend pass is claimed.

## Required complete Go run

On macOS arm64, using the current source and temporary generated state:

```sh
GOMAXPROCS=4 go test -race -p 2 -timeout 40m -count 1 ./cmds/delidev-cli/...
```

The command completed with exit status 1. It was not manually terminated.
Seventeen packages passed, six failed and two had no test files. Grok exhausted
its configured 40-minute package deadline; the other packages finished.

| Package | Result | Reported duration |
| --- | --- | --- |
| `root` | no test files | — |
| `internal/apiproxy` | passed | 4.612s |
| `internal/cli` | failed | 277.272s |
| `internal/connections` | passed | 22.402s |
| `internal/credentials` | passed | 5.553s |
| `internal/domain` | passed | 5.749s |
| `internal/forwarding` | passed | 2.426s |
| `internal/harness` | failed | 145.834s |
| `internal/harness/claude` | passed | 295.609s |
| `internal/harness/codex` | failed | 1287.643s |
| `internal/harness/grok` | failed | 2401.189s |
| `internal/harness/nativewire` | passed | 20.394s |
| `internal/harness/opencode` | passed | 83.783s |
| `internal/integrations/github` | passed | 15.229s |
| `internal/presentation` | passed | 2.765s |
| `internal/process` | passed | 18.025s |
| `internal/providers` | passed | 10.589s |
| `internal/rpc` | no test files | — |
| `internal/security` | passed | 1.662s |
| `internal/server` | failed | 2310.413s |
| `internal/store` | passed | 989.531s |
| `internal/subscription` | passed | 1.852s |
| `internal/userservice` | passed | 8.843s |
| `internal/worker` | passed | 432.134s |
| `internal/workspace` | failed | 1295.947s |

The CLI failed `TestCLISessionAcceptanceQueueAndArchive` at line 202 during
`session create --wait` with unavailable/timeout. Discovery failed its four
Claude/Grok/OpenCode/Codex bounded initialization or process-closure cases.
Codex failed question acceptance, both Steer-inspection functions, both managed
profile functions and changed-settings/malformed-reply inspection; reports
include uncertain delivery, missing retained Steer evidence and native handshake
failure. Grok reported multiple closure/file/input/probe failures before its
40-minute package timeout. Server failed five Local Review observations and
`TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof`.
No managed subscription server failures were reported; this does not establish
a whole-server pass.

Workspace failed `TestWorkspaceDiffUnbornAndBoundedResults` in 24.60s at
`diff_test.go:163`: the unborn result returned `recovery_required` because it did
not prove accepted preparation. The package finished in 1,295.947s. Workspace
source and tests have no changes relative to incorporated main.

The same function passed in a five-minute-bounded isolated race run on the
branch (28.063s) and unchanged main (38.205s), without widening operation bounds:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/workspace \
  -run '^TestWorkspaceDiffUnbornAndBoundedResults$' -count 1
```

These controls do not erase the complete-package failure or prove its cause.

The machine concurrently ran full Go/native suites in three other worktrees.
That observation does not prove the cause of these failures. No unrelated
process was terminated. A cleanup inventory of the completed Codex test binary
found no remaining processes and sent no signals.

## Bounded failure controls

All controls retain the original per-operation/test bounds and use temporary,
synthetic fixtures. Main controls use an unmodified archive of the exact
incorporated revision; source assets remain pointers because these Go packages
do not compile or package them.

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/harness \
  -run '^(TestDiscoveryVerifiesClaudeWithoutGrantingExecution|TestDiscoveryVerifiesGrokWithoutExecution|TestDiscoveryVerifiesOpenCodeWithoutExecution|TestDiscoveryUsesIsolatedEnvironmentAndOwnedProcesses)$' -count 1
```

All four discovery functions passed on main (18.872s) and this branch (24.049s).
This does not erase the broader failure or establish its cause.

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/cli \
  -run '^TestCLISessionAcceptanceQueueAndArchive$' -count 1
```

Main failed in 64.692s at line 215 during unavailable `session files read`;
the branch failed in 61.347s at line 202 during creation timeout. The different
phases cannot establish that the branch's earlier failure is reproduced on main.
No passing CLI-control result is claimed.

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/harness/codex \
  -run '^(TestManagedCodexThreadRechecksWorkspaceProviderAuthority|TestManagedCodexDeviceLoginRefreshAndLocalLogout)$' -count 1
```

Both managed profile functions passed in 42.343s. The broader failures remain
recorded above. A separate 10-minute-bounded control selected question
acceptance, both Steer-inspection functions and changed-settings/malformed
inspection on main and branch. Main failed in 131.645s on `steer-unsupported`
with uncertain delivery instead of Unsupported; branch failed in 154.874s on
`ready` with uncertain delivery. Different subcases do not prove equal causes.
No complete Codex-control pass is claimed.

## Other required checks

All passed:

```sh
GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...
DEVHUD_PROTO_BASELINE=6c749670727b30679e722821846bc8dc00f5ac32 pnpm proto:check
node --test scripts/ci/delidev-structure.test.mjs scripts/ci/delidev-proto.test.mjs
GOMAXPROCS=2 go test -p 1 ./protos/...
```

Protocol generation reproduced without drift and all six structural/allocation
checks passed. API-client typecheck and all 46 tests passed. Frontend typecheck,
8 packaging tests, 16 launch/asset tests, widget fixtures and production build
passed. Its required full test failed with 182 failed / 1,090 passed / 7 skipped
tests; the focused 55-test run still failed seven App timeouts. Those exact seven
cases passed in isolation without widening their bounds. See the separate record
for commands and limitations.

Required LFS assets were hydrated before packaging/build checks. Generated
repository distributions are removed from the final worktree. No historical
ledger, existing evidence or selected backup source was rewritten.

## Remaining limits

Real-account OAuth/inference, installed-native/platform/release acceptance,
desktop lifecycle controls and complete uncertain-lease recovery remain
unverified. Managed Fork is explicitly unavailable under the current API-only
coordinator. Database restore preserves quarantined references without restoring
vault authority. This PR does not complete issue #964. A push requires fresh
CI/review evidence; earlier checks cannot establish the published head's readiness.
