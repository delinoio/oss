# DeliDev CLI

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `cmds/delidev-cli/internal/worker/AGENTS.md`
- `cmds/delidev-cli/internal/domain/AGENTS.md`
- `cmds/delidev-cli/internal/store/AGENTS.md`
- `cmds/delidev-cli/internal/cli/AGENTS.md`
- `cmds/delidev-cli/internal/server/AGENTS.md`

Keep implementation evidence in independent files under `docs/evidence/delidev/issue-<number>/`. Update instructions only when their rules or ownership change, not merely to record another validation run.

- Browser ownership and cleanup follow `docs/cmds-delidev-browser-contract.md`. Keep bounded non-secret profile metadata on the original paired-client Device document with independent UUID-v7 identity/revision, exact authorization and receipts. Account deletion atomically marks every device obligation; session deletion and Archive retain profiles. Service-owned browser messages avoid reserved shared enum numbers and SQLite migrations; never persist browsing content or paths.
