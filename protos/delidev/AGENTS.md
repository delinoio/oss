# protos/delidev working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `protos/delidev/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

## OpenCode General Chat Fork capabilities

- System 26 / Worker 15 `OPENCODE_GENERAL_CHAT_FORK_V1` own the plain-text profile `opencode-general-chat-v1`. System 104 / Worker 69 `OPENCODE_GENERAL_CHAT_FORK_REASONING_V1` independently add profile `opencode-general-chat-v2` (completed plain timed reasoning history).
- Never reuse 26/15 for reasoning. A v2 peer must hold both pairs, a v1-only Worker must keep rejecting reasoning sources before any child exists, and v1 job, receipt and checkpoint bytes must stay unchanged.
- Allocate new numbers from `allocations.json` at change time; the [protocol contract](../../docs/protos-delidev-v1-contract.md) and the [fork contract](../../docs/cmds-delidev-forks-contract.md) own the semantics.

## Owning contracts

- [API account browser OAuth](../../docs/cmds-delidev-account-oauth-contract.md)
- [DeliDev provider and model catalog](../../docs/cmds-delidev-catalog-contract.md)
- [DeliDev Claude native context and manual compaction contract](../../docs/cmds-delidev-claude-compaction-contract.md)
- [DeliDev native session compaction](../../docs/cmds-delidev-compaction-contract.md)
- [DeliDev Portable Configuration](../../docs/cmds-delidev-configuration-transfer-contract.md)
- [DeliDev direct execution startup](../../docs/cmds-delidev-execution-startup-contract.md)
- [DeliDev Session Files and Git Comparisons](../../docs/cmds-delidev-files-contract.md)
- [DeliDev same-account native session forks](../../docs/cmds-delidev-forks-contract.md)
- [DeliDev Session Development-Server Forwarding](../../docs/cmds-delidev-forwarding-contract.md)
- [DeliDev Grok Build subscription contract](../../docs/cmds-delidev-grok-subscription-contract.md)
- [DeliDev image input contract](../../docs/cmds-delidev-image-input-contract.md)
- [DeliDev retained inbox contract](../../docs/cmds-delidev-inbox-contract.md)
- [DeliDev GitHub Integration Profiles](../../docs/cmds-delidev-integrations-contract.md)
- [DeliDev native Codex model observations](../../docs/cmds-delidev-native-models-contract.md)
- [DeliDev explicit outbound network contract](../../docs/cmds-delidev-network-contract.md)
- [OpenCode Go subscription contract](../../docs/cmds-delidev-opencode-go-subscription-contract.md)
- [DeliDev API provider activation](../../docs/cmds-delidev-provider-activation-contract.md)
- [DeliDev automatic session titles](../../docs/cmds-delidev-session-titles-contract.md)
- [DeliDev session acceptance and input queue contract](../../docs/cmds-delidev-sessions-contract.md)
- [DeliDev native read-only Sidechat](../../docs/cmds-delidev-sidechat-contract.md)
- [DeliDev ssh setup contract](../../docs/cmds-delidev-ssh-setup-contract.md)
- [DeliDev storage operations](../../docs/cmds-delidev-storage-contract.md)
- [DeliDev source ownership and compatibility](../../docs/cmds-delidev-structure-contract.md)
- [DeliDev native subagent observations](../../docs/cmds-delidev-subagents-contract.md)
- [DeliDev native subscriptions](../../docs/cmds-delidev-subscription-contract.md)
- [DeliDev Session Terminals Contract](../../docs/cmds-delidev-terminals-contract.md)
- [DeliDev updates contract](../../docs/cmds-delidev-updates-contract.md)
- [DeliDev current-user service contract](../../docs/cmds-delidev-user-services-contract.md)
- [DeliDev v1 Connect contract](../../docs/protos-delidev-v1-contract.md)
