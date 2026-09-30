# Earlier aggregate race and merge validation results

The earlier `GOMAXPROCS=2 go test -race -p 2 -timeout=20m
./cmds/delidev-cli/...` run completed with exit 1. It reported failures in CLI
acceptance, discovery and Claude, Codex, Grok and OpenCode fixture packages, plus
server, Worker and workspace package timeouts. The initially passing API proxy,
connections, credentials, domain and forwarding packages were followed by passing
nativewire, GitHub integration, presentation, process, providers, security and
user-service packages.

This run started before the actor repair and remained active during later main
composition. Its store build eventually used incompatible dependency snapshots
and failed on newly merged accounting declarations. It is neither a stable-head
validation nor a full-suite pass. Those late build errors cannot be used as proof
that the reconciled source fails to compile. Concurrent local runs were observed;
that observation does not establish a cause for earlier fixture failures.

The merge-focused storage/domain run also returned exit 1: domain checks passed
(3.018 s), while storage timed out in the historical-schema convergence test at
the command's default ten-minute package limit (600.792 s). No historical-layout
test, migration requirement or production bound was removed or weakened.

A fresh aggregate run was started only after the code stabilized at `957bc7bf`,
with a twenty-minute package bound and no further Go/protocol source edits. Its
final result belongs in a new independent evidence record. Pinned-native public
acceptance and the unchanged private baseline now pass as recorded separately;
those successes do not convert this aggregate result into a passing check.
