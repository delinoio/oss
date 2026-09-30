# DeliDev CLI

- Execution-device user-facing messages follow issue #1136: use `Runner Device` / `Runner Devices` for former Execution Worker presentation nouns, including PR planning failures and schedule reconfiguration guidance. Preserve generic technical Worker terminology, Agent Worker, user-assigned names, CLI commands, structured logs, stable error codes/classifications, authorization, protocol/storage identifiers and all execution conditions. The desktop New session selector alone uses `Runs on`; follow `docs/apps-delidev-desktop-contract.md`.

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `cmds/delidev-cli/internal/worker/AGENTS.md`
- `cmds/delidev-cli/internal/domain/AGENTS.md`
- `cmds/delidev-cli/internal/store/AGENTS.md`
- `cmds/delidev-cli/internal/cli/AGENTS.md`
- `cmds/delidev-cli/internal/server/AGENTS.md`

Keep implementation evidence in independent files under `docs/evidence/delidev/issue-<number>/`. Update instructions only when their rules or ownership change, not merely to record another validation run.
