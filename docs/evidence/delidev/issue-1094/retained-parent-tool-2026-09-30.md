# Retained Claude parent-tool ownership, PR #1225

Inspected base: `b5c14557d0652f42e63f709bbfaae78c5f1dc591`. Codex thread `PRRT_kwDORRAKg86nuax7` identified a fresh child/native identity reusing a completed child's original Agent/Task tool in a later execution of the same session. Current-execution validation could not see that historical claim.

The single bounded SQLite ownership query now includes both proposed native identities and nonempty original parent-tool identities within the same session. Any matched record must retain the exact proposed child/product/execution identity. No migration or per-child large-document scan is introduced. Existing complete-batch validation and transactional publication remain authoritative.

A real temporary server/SQLite fixture publishes two original Agent proposals and retains a terminal child from an earlier execution. A fresh distinct sibling precedes the conflicting new child in the submitted batch. The unmodified query accepted this batch: the regression failed (server package 1.242s). After the query repair, the whole batch rejects without advancing session revision or publishing either child. The distinct-tool sibling then publishes and updates successfully, while the historical record stays unchanged.

Executed verification:

```sh
GOMAXPROCS=4 go test -race -p 2 -parallel 2 \
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/domain \
  -run 'TestSubagentRetainedParentTool|TestSubagentBatchReusedHistoricalIdentity|TestSubagentPublicationReplay|ParentTool' \
  -count=1 -timeout=20m
```

Both packages passed. Complete backend, protocol and desktop validation is recorded separately at the end of this maintenance pass. Fixtures use private temporary authority, original Connect publication and SQLite resources; they do not establish real-account/native-platform/release acceptance. Historical evidence remains unchanged.
