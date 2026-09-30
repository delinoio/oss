# DeliDev CLI

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `cmds/delidev-cli/internal/worker/AGENTS.md`
- `cmds/delidev-cli/internal/domain/AGENTS.md`
- `cmds/delidev-cli/internal/store/AGENTS.md`
- `cmds/delidev-cli/internal/cli/AGENTS.md`
- `cmds/delidev-cli/internal/server/AGENTS.md`

Keep implementation evidence in independent files under `docs/evidence/delidev/issue-<number>/`. Update instructions only when their rules or ownership change, not merely to record another validation run.

- Session terminals and native PTY/ConPTY ownership follow `docs/cmds-delidev-terminals-contract.md` and the process contract. Keep the shared terminal result JSON bound at 64 KiB and reserve 4 KiB more for operation journal ownership metadata. Keep shell selection Worker-owned with no fallback after invalid discovery or override; input/output/path/environment contents never enter logs or native ownership journals.
