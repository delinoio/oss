# Account switching and ALLGREEN queue merge

## Composed source

This repair merges main `98df29c41` into proxy head
`83dd72c22c1e93bd2bba1a78fd4f30e96fb4b796` on 2026-09-30.
Main adds explicit stopped-session Codex API account selection, stable ALLGREEN
merge-queue observations and allocation-only native model-discovery reservations.

The textual conflicts were in CLI/domain/store/protocol/client instructions,
the protocol contract, System capability schema/status and its generated Go and
TypeScript bindings. All independent source-backed instructions are retained.
System status advertises both stopped account switching (wire value 5) and
server outbound proxies (wire value 6), alongside the existing capabilities.
The incoming status fixture additionally checks the proxy capability, so the
authenticated response must preserve both sides of this composition.

Generated bindings were regenerated from the reconciled schema with
`pnpm proto:generate`; neither generated merge side was selected. Native model
discovery remains a reservation, with no new runtime capability advertisement.
Account switching retains its separate original-history and explicit Resume
authority; queue observations remain read-only and use the existing explicit
server GitHub transport. No migration, retry or credential fallback was added.

## Focused verification

- `GOMAXPROCS=2 go test -race -p 2` across server, CLI, domain, store,
  outbound, providers, inference proxy and GitHub packages, filter
  `Network|CLINetwork|Proxy|Credential|AccountSwitch|SwitchAccount|SwitchSessionAccount|Queue|ALLGREEN`,
  `-count=1 -timeout 5m`, passed all eight packages.
- The final authenticated status capability fixture passed separately with
  `-count=1` after adding the proxy capability to its expected set.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- `GOMAXPROCS=2 go build -o <temporary-output> ./cmds/delidev-cli` passed.
- Protocol formatting/lint, generation and breaking comparison passed.
- Structure/protocol/breaking CI fixture files passed all seven tests.
- API client typecheck and all 45 tests passed.
- The complete frontend `pnpm test` command passed, including typecheck,
  component/integration tests, bundle/desktop-launch/widget checks and build.
  Vitest was temporarily limited to two workers; its source configuration was
  restored unchanged. The existing DeliDev LFS icon is hydrated.

The complete combined-tree Go race command is still running at this merge
commit. Its result, exact frontend counts and post-commit protocol freshness
will be recorded separately. Earlier failed local commands and their unresolved
causes remain preserved in prior issue-1084 evidence; this focused result does
not diagnose them. No real provider/GitHub account, enterprise proxy, native
credential lifecycle, platform release or Worker bootstrap acceptance is claimed.
