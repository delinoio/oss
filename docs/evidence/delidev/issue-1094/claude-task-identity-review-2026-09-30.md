# Issue #1094 Claude child task identity repair

Review thread `PRRT_kwDORRAKg86ng_NQ` identified that child-task receipts did not
reserve their original native event identities. The acknowledged child commit
now records Claude task source IDs in the binding's existing progress identity
set. An unacknowledged receipt reserves no identity; its exact replay records
that same ID only after acknowledgment. Existing duplicate validation then
rejects exact or altered native repeats before source coverage or metadata can
advance. Fresh notification IDs remain supported.

The regression checks lost acknowledgment, identical receipt replay, a fresh
notification, and both exact and altered repeats with unchanged retained
description and no new publication. These tests passed with existing task and
child replay/receipt regressions:

```sh
GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/worker \
  -run 'TestSubagentClaudeTaskIdentity|TestSubagentClaudeOriginalTask|TestSubagentClaudeTaskReceipts|TestClaudeTask' \
  -count=1 -timeout=8m
```

The receipt and ownership contracts remain unchanged. Earlier broad failures
remain recorded independently. The thread is resolved only after the single
final repair push succeeds.
