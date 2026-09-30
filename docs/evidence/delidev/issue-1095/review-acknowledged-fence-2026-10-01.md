# Issue #1095: acknowledged fenced execution remains uncertain

Review thread `PRRT_kwDORRAKg86nouCi` identifies an acknowledged protected
Finish that can retain recovery-required ownership. The RPC acknowledgment
alone previously allowed ordinary job reporting and replaced the started claim.

Execution now permits ordinary reporting only after final bundle capture and
independent credential cleanup, or the verified unused-original pre-native
outcome. Other acknowledged finishes return the existing typed uncertainty,
retain the original job claim and stop the primary lane. Workspace cleanup
continues to preserve that typed uncertainty. The scoped Worker instructions
and subscription contract explicitly describe this acknowledgment boundary.

## Executed verification

With regression tests present and execution.go restored temporarily to merge
`46377ac1b`, the new race test failed in all three cases: final native identity
read failure, retained credential scan failure and failed startup cleanup. Each
incorrectly authorized ReportWork. The fixed source then passed:

```sh
GOMAXPROCS=4 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/worker \
  -run 'ManagedExecution|ManagedSubscription.*Lane' -count 1
```

Worker passed in 9.024s. Assertions prove the protected Finish is acknowledged,
ordinary ReportWork is absent and the original journal remains started without
output/problem. Existing successful rotated-bundle and confirmed pre-native
failures remain covered. Two initial fixture runs failed an incorrect expectation
that capture failure could still prove cleanup without captured bytes; the final
test preserves that cleanup limitation. All native processes use the controlled
test binary, private temporary state and synthetic credentials. This is not
real-account or installed-native acceptance.
