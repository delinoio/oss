# Issue #1095: complete validation after review repairs

This independent record covers [PR #1233](https://github.com/delinoio/oss/pull/1233)
after all review repairs and reconciliation with main
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`. Tested implementation and generated
bindings are at `20e9a04f2b2cde1b33cad880aaf3c2a5e5aa1822`; subsequent local
commits change evidence only. Earlier failures and the complete pre-review pass
at `a91ff7f266929eb258c51065df8f5d3ac83a1db5` remain preserved.

The separate repair records cover late-Finish ownership fencing, accepted
native-provider comparisons, terminal bundle capture before native Close, and
preserving the unused original generation after confirmed pre-native failure.
The incoming API account-switch continuation retains its original complete
predecessor account/connection pair and the accepted checkpoint provider profile.

## Required full Go race run

On macOS arm64 with Go 1.26.8:

```sh
GOMAXPROCS=4 go test -race -p 2 -timeout 30m -count 1 ./cmds/delidev-cli/...
```

The command completed with exit status 1. It was not terminated and did not
reach the package deadline. All packages ran; four failed:

| Package | Executed failure | Package duration |
| --- | --- | --- |
| CLI | `TestCLISessionAcceptanceQueueAndArchive`: unavailable tracked-file reader at `sessions_test.go:215` | 188.422s |
| Codex | Two Steer inspection functions: missing retained attempt, uncertain delivery and operation timeout | 373.985s |
| Grok | `TestOriginalTextStopSeparatesSubmissionTerminalAndCleanup/stop-active-drift`: native initialization unavailable | 1170.622s |
| Workspace | Three diff functions: unproved preparation, binary/literal-path timeout and unborn-diff timeout | 921.977s |

Every other tested package passed, including complete server (697.427s), storage
(912.773s), Worker (394.889s), subscription (1.646s), Claude (344.289s), OpenCode
(59.736s), GitHub (10.732s), API proxy, domain, credentials, process, native wire,
providers, forwarding, connections, presentation, security and user-service
packages. The complete Worker pass includes the new rotated terminal-bundle
fixture and confirmed pre-native cleanup controls. No complete-suite pass is
claimed for this composed revision.

## Failure controls

[`review-cli-codex-controls-2026-10-01.md`](review-cli-codex-controls-2026-10-01.md)
records the CLI review-file failure reproduced on unchanged main and both Codex
Steer functions passing on isolated feature and baseline runs. It preserves the
different CLI operation reached in the full run and the limits of that proof.

The entire isolated Grok stop function still failed (271.516s), with nine cases
reporting initialization or operation timeouts. The two relevant baseline cases
also failed native initialization (54.243s):

```sh
GOMAXPROCS=4 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/harness/grok \
  -run '^TestOriginalTextStopSeparatesSubmissionTerminalAndCleanup$/(stop-active-drift|stop-missing-idle)$' \
  -count 1
```

The baseline command ran in the independent archive of the exact incorporated
main. Grok source has an empty feature diff against that revision. This confirms
those baseline initialization failures, not every diagnostic or a single cause
for the broader failures.

All three complete workspace diff functions passed on isolated feature retry
(53.945s) and unchanged-main control (54.609s):

```sh
GOMAXPROCS=4 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/workspace \
  -run '^TestWorkspaceDiff(KeepsOriginalCreationCommitAndLiveExecution|LiteralPathBinaryAndNoExternalHelpers|UnbornAndBoundedResults)$' \
  -count 1
```

Workspace source also has an empty feature diff against incorporated main.
These isolated passes do not establish a complete workspace-suite pass or erase
the full-run failures. Production and fixture deadlines/assertions were unchanged.

## Other executed checks

- `GOMAXPROCS=4 go vet -p 1 ./cmds/delidev-cli/...`: passed.
- `DEVHUD_PROTO_BASELINE=98df29c41ddf3c8b1274c51fe8f406b6dae6ca74 pnpm proto:check`:
  lint/format, breaking comparison and forced generation freshness passed. The
  baseline is the freshly fetched and incorporated main, pinned to avoid another
  checkout's shared-ref movement; no schema compatibility check was omitted.
- `GOMAXPROCS=4 go test -p 1 ./protos/...`: contract/reflection tests passed and
  generated packages compiled. An initial `./protos` invocation failed setup
  because that directory contains no Go files; the corrected recursive command
  above completed successfully.
- API-client `pnpm typecheck` and `pnpm test`: passed; all 44 tests across four
  files passed, including its Go-built Connect fixture. The final Go fixture
  build was warmed without changing its existing 120-second build deadline.
- Six focused race-tested packages and all six allocation/structure tests passed
  as recorded in [`review-main-98df-2026-10-01.md`](review-main-98df-2026-10-01.md).
- Frontend full and bounded-unit runs failed; all four remaining timeout cases
  passed isolated. Typechecking, packaging/launch/widget fixtures and the build
  passed. Exact counts, commands and limits remain in
  [`review-frontend-2026-10-01.md`](review-frontend-2026-10-01.md).
- `git diff --check` passed. Required ignored embedding/client/frontend build
  outputs were generated explicitly, then removed from the final worktree.
  No generated distribution, credential or real-account state is included.

## Limits

Controlled synthetic native/provider fixtures remain distinct from real-account
OAuth/inference, installed-native managed execution, Windows/Linux platform and
release acceptance, desktop lifecycle controls and complete uncertain-lease
recovery. No such acceptance is claimed. Complete issue #964 remains separate.
The pushed head requires its own GitHub CI/review observations; local validation
does not establish approval or merge readiness.
