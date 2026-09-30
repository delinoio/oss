# Issue #1204: aggregate native-read budget

Baseline: `caf0b8bb3ad58f40fd83f801a712ae6611635d8f`. Implementation revision: the enclosing follow-up commit of this record, on `kdy1/issue-1204-opencode-stream-reconciliation`. Validation used macOS arm64, Go `1.26.8` and pinned OpenCode `1.18.32`.

Final review found that counting canonical snapshot sizes could undercount repeated predecessor reads and whitespace removed from otherwise valid JSON. The follow-up counts every actual response body read through the session HTTP boundary during reconciliation, including predecessor pages, and the original authentication/health/global-configuration checks. The same 32 MiB cycle budget and 1 MiB response bound remain in force; no new protocol or migration allocation is introduced. Stored-input read failures retain their original bound classification.

`TestEventReconciliationCountsRepeatedWireBytesBeforeCanonicalization` supplies changing history with individually valid, whitespace-padded responses. It verifies aggregate exhaustion before the observation deadline, no partial facts, an unchanged original prefix and no second subscription. The fixture deliberately distinguishes wire size from the much smaller canonical content.

Executed checks:

```sh
go test -race ./cmds/delidev-cli/internal/harness/opencode -run '^TestEventReconciliation' -count=1
go vet ./cmds/delidev-cli/...
DELIDEV_NATIVE_OPENCODE_EXECUTABLE="$native_executable" go test ./cmds/delidev-cli/internal/harness/opencode -run '^TestManualNativeOpenCodeEventReconciliation$' -count=1
git diff --check
```

Race, vet and whitespace checks passed. The first follow-up native invocation failed during `configuration-before-provider` initialization, before creating a session or exercising reconciliation; its startup failure cause was not independently established. A repeat of the exact native command passed in 9.648 seconds. The native executable was the same isolated pinned temporary binary used for the original record. The passing check again severed only the real original event response, retained one original prompt/provider request, recovered complete text and independently verified history and cleanup. It was not race-instrumented. The [original validation record](event-reconciliation.md) retains the broader checks, startup failures and native/public/platform qualifications; they are not superseded by this focused follow-up.
