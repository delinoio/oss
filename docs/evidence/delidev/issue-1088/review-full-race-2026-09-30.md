# Review-repair full Go race validation

The four review repairs are committed through `46268cead`; the documentation-only
allocation correction is `a61fcd567d095cff90a0aa0c5ee3ad0c14db3fe8`.
This record describes the complete local command before the later merge of main
`6d7004d2e37c42da5febd9dd3db06fb1fade019e`.

```sh
GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go test -race -p 2 -timeout 5m ./cmds/delidev-cli/...
```

The command finished with exit status 1. The 5-minute watchdog applies to the
whole package, not each test. It is shorter than the repository's 20-minute CI
watchdog; package timeouts are incomplete validation, not proof that the named
last-running test itself has a defect.

| Package | Observed outcome |
| --- | --- |
| `internal/cli` | Failed `TestCLISessionAcceptanceQueueAndArchive` at its creation-comparison review-context read because the owning workspace reader was unavailable. Package finished in 240.461 seconds. |
| `internal/harness/grok` | Failed `TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval/closure-wrong-outcome` during native initialization, then hit the package timer while `TestOriginalFileReplyClaimsAndTerminalFaults/write-missing-rpc` was running. |
| `internal/server` | Package timer expired while `TestDiscoveryRevisionReceiptsAuthorizationAndAtomicPublication` was running. |
| `internal/store` | Package timer expired while `TestPRRemediationCompletionCannotReleaseChangedOriginalAuthority/connection` was running. |
| `internal/worker` | Package timer expired while `TestTodoToolsRetainClearingAndRejectChangedAppliedLists/input` was running. |
| `internal/workspace` | Package timer expired while `TestLocalPartialRecoveryDeletesOnlyMetadataAndRetainsProof/2` was running. |

All other reported test packages passed: apiproxy, connections, credentials,
domain, forwarding, harness, Claude, Codex, nativewire, OpenCode, GitHub integration,
presentation, process, providers, security, terminal and user services. The
command root and RPC package have no tests. There were no missing-cache build
failures in this run; every reported test package executed.

The historical original-main `74701b894` baseline reproduction remains limited
to its recorded CLI/discovery cases. This pass did not separately rerun the current
main baseline, and does not classify the new Grok outcome or package timeouts as
proven baseline failures. Preserve all older runs and native evidence limits.

Because the full timers left terminal coverage incomplete, the final composed
review revision also ran:

```sh
GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go test -race -p 2 -timeout 3m ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/terminal ./cmds/delidev-cli/internal/worker -run '^(TestTerminal|TestSessionArchiveWaitsForTerminal|TestWorkerPairingOwnership)' -count=1
```

The selected store, server and Worker suites passed. The terminal package compiled
but has no matching test names under that selection; its full suite passed in the
broad command. Root Go vet and all 102 pre-merge repository contracts passed.
The later main composition and its validation are recorded separately.
