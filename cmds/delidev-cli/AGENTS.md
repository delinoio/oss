# DeliDev CLI

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `cmds/delidev-cli/internal/worker/AGENTS.md`
- `cmds/delidev-cli/internal/domain/AGENTS.md`
- `cmds/delidev-cli/internal/store/AGENTS.md`
- `cmds/delidev-cli/internal/cli/AGENTS.md`
- `cmds/delidev-cli/internal/server/AGENTS.md`

Keep implementation evidence in independent files under `docs/evidence/delidev/issue-<number>/`. Update instructions only when their rules or ownership change, not merely to record another validation run.

- Desktop-launch infrastructure follows issue #1137 and the desktop/CLI/user-service contracts. Ordinary product commands never implicitly start a server. Pin registered native-service admission against concurrent control through running-intent publication and detached spawn; preserve original explicit Start and running-intent-only ensure semantics.

Native session compaction for issues #1093, #1202 and #1203 follows the planned shared boundary in `docs/cmds-delidev-compaction-contract.md`. Its reservations must land on main before dependent implementation. Preserve original transcript/outcome, once-only native claims and independent history/cleanup verification; native acknowledgment never grants a successor checkpoint.
