# Short proxy credential reflection review repair

## Source and finding

This repair is based on PR #1170 head `3172bd48c7c83f08798956e862988a6194f5f5fd`. Codex thread [PRRT_kwDORRAKg86nbNW6](https://github.com/delinoio/oss/pull/1170#discussion_r4141866282) correctly identified that the existing guard omitted unquoted literal forms shorter than eight bytes, despite the credential contract permitting them. The new tests cover the reported plaintext SSE example.

## Resulting behavior

Short literal, JSON-escaped and Base64 forms match complete byte tokens; ASCII letters/digits/underscore/hyphen and non-ASCII bytes count as token continuations. A complete short candidate remains withheld until another byte or EOF proves its right boundary. The preceding emitted byte preserves left-boundary context across source reads and one-byte caller buffers. Header values use independent complete-value boundaries. Existing exact quoted JSON protection and longer-form substring checks remain intact. Closing a body releases an owned read waiting for a short candidate's right boundary. This finite guard does not claim arbitrary transformation detection or short-form substring matching within unrelated words.

## Executed verification on macOS arm64, 2026-09-30

The following commands use `GOMODCACHE=/private/tmp/delidev-1084-review-modcache`, `GOCACHE=/private/tmp/delidev-1084-review-go-cache` and `GOMAXPROCS=4`, with Go 1.26.8 selected by the repository from the installed Go launcher:

- `go test -race -p 1 ./cmds/delidev-cli/... -run '^(TestNetwork|TestCLINetwork|TestProxyCredential)' -count=1` passed. This filtered run compiles every DeliDev package and executes the selected routing, credential, client, CLI and server regressions; unrelated suites report no tests selected.
- `go test -race -p 1 ./cmds/delidev-cli/internal/outbound -count=1` passed the complete outbound suite after adding independent-header state and pending-boundary cancellation cases. Literal/encoded reflection at response start, punctuation/whitespace boundaries and EOF is rejected with full and one-byte source reads. Unrelated JSON/words and one-byte caller reads remain unchanged.
- `go vet -p 1 ./cmds/delidev-cli/...` passed.
- Initial attempts using the shared cache, and then only a private cache, could not compile after shared cache artifacts and toolchain executables became unavailable. The successful commands isolate both module/toolchain material and cache; they do not modify the shared directories.
- Administrator and ach embed outputs were regenerated explicitly before the required root Go-format commit hook. They are generated output and are removed after verification.

## Completed isolated full race retry

The existing `go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...` retry in session 92171 completed with **exit 1**, confirmed on 2026-09-30. It used the isolated module/toolchain and cache paths listed above and did not repeat the original run. The retry began during the short-credential repair before merge `53d587adb5cf7f22810833dcbbc9f92087b3b8aa`; because that worktree later merged main while the command remained active, this run is not immutable combined-tree full-suite evidence.

| Package | Actual outcome |
| --- | --- |
| `internal/cli` | Failed after 206.392 seconds. `TestCLISessionAcceptanceQueueAndArchive` failed at `sessions_test.go:215`: the selected workspace file read returned typed `unavailable`. |
| `internal/harness/grok` | Failed after 1,201.226 seconds, including the 20-minute package timeout. `TestSessionBindingRequiresOriginalReadyModeAndConfiguration` returned uncertain native delivery for Execute and Plan; `TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval/closure-ack-only` could not complete native initialization; `TestProbeOwnsBoundedInspectedInitialization/inventory-first` returned a timeout instead of unsupported. At the package timeout, `TestOriginalTextStopSeparatesSubmissionTerminalAndCleanup/stop-claim-failure` was active. |
| `internal/server` | Failed at the 20-minute package timeout, after 1,200.821 seconds. `TestStoppingRecoveryCancelsOnlyItsJobAndKeepsOriginalUncertain` was active at termination. |
| `internal/workspace` | Failed after 1,004.482 seconds. `TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership/local` and `TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess` returned `recovery_required`: the Worker result did not prove accepted preparation. |

The other test-bearing packages passed: apiproxy, connections, credentials, domain, forwarding, harness/discovery, Claude, Codex, nativewire, OpenCode, GitHub integrations, outbound, presentation, process, providers, security, store, user services and Worker. Packages without tests reported that explicitly. Unlike the original shared-cache attempt, this isolated retry completed without unavailable cache/toolchain build artifacts.

The earlier unchanged-main comparison is bounded to its selected tests and remains recorded in [implementation evidence](implementation.md). Its session failure occurred at an earlier workspace preparation step; it does not reproduce the file-read failure above or the Grok/server/workspace failures. Its two discovery verification failures did not recur in this retry, and its bounds/update-suppression test had passed. These are observed failures with unresolved causes, not proof of either a proxy regression or baseline equivalence. Targeted passes do not turn this full-suite outcome into success. No test assertion, timeout or product behavior was changed to suppress these outcomes.

## Evidence limits

These are controlled local fixtures with temporary state and fixture-owned credentials. No real accounts, enterprise proxies, native proxy-credential lifecycle, Windows/Linux runtime, Worker bootstrap or release acceptance was performed. No frontend, protocol or Rust source changed in this repair.
