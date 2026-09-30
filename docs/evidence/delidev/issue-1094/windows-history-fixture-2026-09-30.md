# Windows child-history fixture permissions, PR #1225

Inspected base: `b5c14557d0652f42e63f709bbfaae78c5f1dc591`. Windows Worker job `110113960687` in run `36781884423` failed `TestSubagentClaudeHistoryPublishesEachOriginalLeafOnceAfterAcknowledgment` at the lost-acknowledgment assertion (0.34s). The Worker package failed after 125.819s; the workspace package passed. The aggregate CI failure shares that root cause.

The fixture created Claude's home indirectly with `os.MkdirAll` and Unix mode bits. Windows ignores those mode bits for DACL ownership; the production reader requires the existing exact owner-only ACL on the home and every descendant. An unavailable history read cannot exercise the intended lost-publication acknowledgment path.

The fixture now creates the native home through `security.PrivateDir`, whose Windows descriptor restricts access to the current user and passes inheritable owner-only permissions to descendants. It explicitly checks both private files and verifies the original child transcript before testing receipt loss. All original lost-ack replay, sibling, repeated leaf and finalized-usage assertions remain. No reader, runtime ownership policy, permission check, skip or timing bound changes.

Focused verification:

```sh
GOMAXPROCS=4 go test -race -p 2 -parallel 2 \
  ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/harness/claude \
  -run '^(TestSubagentClaudeHistoryPublishesEachOriginalLeafOnceAfterAcknowledgment|TestRetainedHistoryReaderBindsOriginalMainAndChildFiles|TestRetainedHistoryReaderRejectsUnsafeScopesWithoutRepair)$' \
  -count=1 -timeout=20m
```

Both packages passed (Worker 3.625s; Claude 3.777s). Windows compilation and complete validation results are recorded in the maintenance validation file. Windows cross-compilation proves buildability only; the ACL behavior and receipt assertions require the next Windows CI run. Fixtures do not establish real native/account/platform or release acceptance. Historical evidence remains unchanged.
