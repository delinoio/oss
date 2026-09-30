# Native child source-field validation, PR #1225

Inspected base: `b5c14557d0652f42e63f709bbfaae78c5f1dc591`. Codex thread `PRRT_kwDORRAKg86nuax_` identified task/activity observations claiming output or observed models unavailable from those native sources.

The shared incoming-observation validator now rejects output and observed models from Claude task events, output and both model fields from Codex activity events, and observed models or full output blocks from Codex collaboration events. Validation runs before merging retained last-available fields. History/content sources retain their supported fields and the existing closed native usage-family/counter parity checks.

Seven real server/SQLite regression cases failed before the repair because malformed batches were accepted. They now reject the complete batch without publishing the valid preceding child or advancing the session revision. A positive case publishes original Claude content followed by a task update with omitted output/model; the retained record preserves the earlier content and both source entries without attributing that content to the task event.

Executed verification:

```sh
GOMAXPROCS=4 go test -race -p 2 -parallel 2 \
  ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/server \
  ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/harness/codex \
  -run 'SubagentSourceField|SubagentTaskOmission|SubagentClaudeTaskReceipts|SubagentClaudeOriginal|SubagentUsage|SubagentChildMetadata|SubagentCollaboration|SubagentNotifications' \
  -count=1 -timeout=20m
GOMAXPROCS=4 go test -race -p 2 -parallel 2 \
  ./cmds/delidev-cli/internal/harness/codex \
  -run '^(TestSubagentDescendantInspectionReadsHistoryWithoutChildControls|TestSubagentCanonicalCollaborationKeepsRequestedModelAndChildScope|TestSubagentActivityUsesCanonicalStringKind|TestSubagentThreadStartedRequiresPriorOwnershipEvidence)$' \
  -v -count=1 -timeout=20m
```

All four package selections passed. The explicit verbose native-adapter selection confirms all four named tests executed and passed; the broader selection alone is not evidence that those named adapter tests ran. Complete backend/protocol/desktop results are recorded separately at the end of this maintenance pass. These fixtures do not establish native-account, Windows, packaging, signing or release acceptance. Historical evidence remains unchanged.
