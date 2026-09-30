# Issue #1094 Claude idle review repair

Review thread `PRRT_kwDORRAKg86ng_NH` identified that otherwise valid root idle
blocked the publisher while an owned child remained live. The publisher now
retains the first validated result/command/idle terminal proof and continues
accepting owned child observations. The runner performs final child-history
inspection and publishes that same terminal proof only after every child is
terminal. Completion still requires the original API EOF and native/workspace
cleanup; idle does not prove them. A structured deferred-terminal diagnostic
contains only the job identity and bounded child count.

The two-child regression checks sibling independence, post-idle task completion,
the original result/command/idle identities, lost terminal acknowledgment and
byte-identical receipt replay. Existing terminal, continuation and denial
regressions passed alongside it:

```sh
GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/worker \
  -run 'TestSubagentClaudeIdle|TestClaudeTerminal|TestClaudeContinuation|TestClaudeDenial' \
  -count=1 -timeout=8m
```

Ownership, capability, continuation and cleanup requirements remain unchanged.
The earlier full-suite failures remain recorded independently. The review
thread is resolved only after the single final repair push succeeds.
