# cmds/delidev-cli/internal/workspace working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/workspace/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Owning contracts

- [DeliDev native session compaction](../../../../docs/cmds-delidev-compaction-contract.md)
- [DeliDev protected credential storage](../../../../docs/cmds-delidev-credentials-contract.md)
- [DeliDev direct execution startup](../../../../docs/cmds-delidev-execution-startup-contract.md)
- [DeliDev same-account native session forks](../../../../docs/cmds-delidev-forks-contract.md)
- [DeliDev native harness adapter contract](../../../../docs/cmds-delidev-harness-contract.md)
- [DeliDev GitHub Integration Profiles](../../../../docs/cmds-delidev-integrations-contract.md)
- [DeliDev session acceptance and input queue contract](../../../../docs/cmds-delidev-sessions-contract.md)
- [DeliDev native read-only Sidechat](../../../../docs/cmds-delidev-sidechat-contract.md)
- [DeliDev storage operations](../../../../docs/cmds-delidev-storage-contract.md)
- [DeliDev Worker workspace contract](../../../../docs/cmds-delidev-workspace-contract.md)
