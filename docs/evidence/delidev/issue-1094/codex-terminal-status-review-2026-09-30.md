# Issue #1094 Codex terminal-status review repair

Review thread `PRRT_kwDORRAKg86ng_M_` identified that a root status notification
aborted publication while the completed root still owned live children. The
publisher now accepts correlated, non-late idle/active root statuses after
terminal publication as observations only. Unknown/duplicate flags, foreign or
late events and attempts to reopen root waiting controls still fail. Root status
does not publish another event, finish children or establish native cleanup.

The regression follows a live child through root completion, idle and active
root notifications, then the child's independent completion. It also checks
the rejected status/ownership cases. This focused command passed:

```sh
GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/worker \
  -run '^TestSubagentCodexTerminalStatus' -count=1 -timeout=5m
```

The existing ownership, capability and cleanup contracts are unchanged. The
pre-repair aggregate failures remain recorded separately; this focused pass
does not establish a full backend pass. The thread is resolved only after the
single final repair push succeeds.
