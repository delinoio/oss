# cmds/delidev-cli/internal/domain working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/domain/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Owning contracts

- [API account browser OAuth](../../../../docs/cmds-delidev-account-oauth-contract.md)
- [DeliDev account lifecycle](../../../../docs/cmds-delidev-accounts-contract.md)
- [DeliDev protected account browser](../../../../docs/cmds-delidev-browser-contract.md)
- [DeliDev provider and model catalog](../../../../docs/cmds-delidev-catalog-contract.md)
- [DeliDev Claude native context and manual compaction contract](../../../../docs/cmds-delidev-claude-compaction-contract.md)
- [DeliDev native session compaction](../../../../docs/cmds-delidev-compaction-contract.md)
- [DeliDev command, server, and Worker contract](../../../../docs/cmds-delidev-contract.md)
- [DeliDev protected credential storage](../../../../docs/cmds-delidev-credentials-contract.md)
- [DeliDev direct execution startup](../../../../docs/cmds-delidev-execution-startup-contract.md)
- [DeliDev same-account native session forks](../../../../docs/cmds-delidev-forks-contract.md)
- [DeliDev native harness adapter contract](../../../../docs/cmds-delidev-harness-contract.md)
- [DeliDev image input contract](../../../../docs/cmds-delidev-image-input-contract.md)
- [DeliDev GitHub Integration Profiles](../../../../docs/cmds-delidev-integrations-contract.md)
- [DeliDev native Codex model observations](../../../../docs/cmds-delidev-native-models-contract.md)
- [DeliDev explicit outbound network contract](../../../../docs/cmds-delidev-network-contract.md)
- [OpenCode Go subscription contract](../../../../docs/cmds-delidev-opencode-go-subscription-contract.md)
- [DeliDev provider inspection](../../../../docs/cmds-delidev-providers-contract.md)
- [DeliDev session acceptance and input queue contract](../../../../docs/cmds-delidev-sessions-contract.md)
- [DeliDev native read-only Sidechat](../../../../docs/cmds-delidev-sidechat-contract.md)
- [DeliDev storage operations](../../../../docs/cmds-delidev-storage-contract.md)
- [DeliDev native subagent observations](../../../../docs/cmds-delidev-subagents-contract.md)
- [DeliDev native subscriptions](../../../../docs/cmds-delidev-subscription-contract.md)
- [DeliDev Session Terminals Contract](../../../../docs/cmds-delidev-terminals-contract.md)
- [DeliDev native usage ledger](../../../../docs/cmds-delidev-usage-contract.md)
- [DeliDev current-user service contract](../../../../docs/cmds-delidev-user-services-contract.md)
- [DeliDev Worker workspace contract](../../../../docs/cmds-delidev-workspace-contract.md)
- [Project: DeliDev](../../../../docs/project-delidev.md)
- [DeliDev v1 Connect contract](../../../../docs/protos-delidev-v1-contract.md)

- Calendar preview normalizes the existing pinned parser values and its wildcard-origin AND/OR day rule under the schedule contract. Keep this adapter aligned with parser upgrades and preserve original NextRun DST/search behavior.
