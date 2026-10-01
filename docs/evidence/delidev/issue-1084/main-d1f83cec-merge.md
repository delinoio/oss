# OpenCode reconciliation and Windows root merge

## Composed source

This repair merges main `d1f83cecee4e0c50ea094335392cf68845f85739`
into proxy head `cbbe40839df3be71e08df7826904a1f575e2b94d` on
2026-09-30. Main adds original-process OpenCode event reconciliation and the
separate Windows General Chat native/filesystem root profile.

The sole textual conflict was in `cmds/delidev-cli/internal/server/AGENTS.md`.
The resolution retains both independent rules: NetworkService's explicit
server routing and non-secret Worker export boundary, and Windows OpenCode's
independent root/checkpoint proof and existing dispatch gates. The incoming
harness contract limits reconciliation to the owned native loopback transport;
it does not authorize retries by server business proxy clients.

No frontend, protocol or API client source differs between main `65eca3341`
and this incoming main commit. The preceding combined frontend/client evidence
remains historical evidence for those unchanged inputs, not a new execution
against this merge. No tool-owned protocol output required conflict resolution.

## Focused verification

- `GOMAXPROCS=2 go test -race -p 2` across server, CLI, domain, outbound,
  providers, inference proxy, GitHub, OpenCode and Worker packages, with filter
  `Network|CLINetwork|Proxy|Credential|EventReconciliation|Reconciliation|WorkspaceRoot|WindowsGlobal|CheckpointRoot`,
  `-count=1 -timeout 5m`, passed all nine selected packages. This filter covers
  proxy fixtures and event reconciliation fixtures; it does not claim every
  new root test or real native acceptance.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- Structure/protocol/breaking CI fixture files passed all seven tests.

The complete combined-tree Go race command is still running at this merge
commit. Its results and post-commit protocol freshness will be recorded in a
separate follow-up file. Earlier complete-Go failures remain unresolved; this
focused result does not turn those commands green. No real native account,
enterprise proxy, GitHub or Worker bootstrap acceptance is claimed.
