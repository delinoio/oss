# Repository-inspection reservation merge

## Composed source

This repair merges main `65eca3341` into proxy head `4d76e25fe` on 2026-09-30.
Main includes repository-inspection allocation prerequisites, pinned required
workflow observation, Activity filtering and Runner Device presentation changes.

The two textual conflicts were in `protos/delidev/AGENTS.md` and
`docs/protos-delidev-v1-contract.md`. Both independent sections are retained:
NetworkService activates its existing SystemCapability 6 and EntityKind 28/29,
while repository inspection reserves WorkerCapability 6 and attachment field 3
without advertising implementation. These values belong to distinct enum/message
owners. No schema or generated binding changes are implied by the reservation.

Bindings were regenerated from the composed sources and have no additional
Go/TypeScript diff. Inspection of incoming workflow reads shows they use the
existing GitHub client's JSON/GraphQL helpers and HTTP client; they do not create
an independent direct or ambient-proxy client. This is source inspection, not
enterprise-proxy or real GitHub acceptance.

## Focused verification

- `GOMAXPROCS=2 go test -race -p 2` across server, CLI, domain, outbound,
  provider, inference-proxy and GitHub packages with filter
  `Network|CLINetwork|Proxy|Credential|Workflow`, `-count=1 -timeout 5m` passed
  all selected packages. This includes incoming pinned-workflow fixtures and the
  existing network authorization/routing/credential/cancellation fixtures.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- `pnpm proto:generate`, `pnpm proto:lint` and `pnpm proto:breaking` passed.
- Structure/protocol/breaking CI fixture files passed all seven tests.
- DeliDev API client `pnpm typecheck && pnpm test` passed all 45 tests.

Complete combined-tree Go/frontend commands and post-commit protocol freshness
remain in progress at this merge commit. Their results will be recorded in an
independent follow-up file. Earlier validation records retain their failed broad
commands and passing focused retries; they do not certify this new merged head.
No real native/account/network/Worker bootstrap acceptance is claimed.
