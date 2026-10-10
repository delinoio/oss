# cmds/delidev-cli/internal/harness/codex working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/harness/codex/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

- Keep private notification decoding and passive disposal separate from search/resource ownership. Follow the owning harness contract before adding any consumer.

## Owning contracts

- [API account browser OAuth](../../../../../docs/cmds-delidev-account-oauth-contract.md)
- [DeliDev account lifecycle](../../../../../docs/cmds-delidev-accounts-contract.md)
- [DeliDev provider and model catalog](../../../../../docs/cmds-delidev-catalog-contract.md)
- [DeliDev native session compaction](../../../../../docs/cmds-delidev-compaction-contract.md)
- [DeliDev protected credential storage](../../../../../docs/cmds-delidev-credentials-contract.md)
- [DeliDev direct execution startup](../../../../../docs/cmds-delidev-execution-startup-contract.md)
- [DeliDev same-account native session forks](../../../../../docs/cmds-delidev-forks-contract.md)
- [DeliDev native harness adapter contract](../../../../../docs/cmds-delidev-harness-contract.md)
- [DeliDev image input contract](../../../../../docs/cmds-delidev-image-input-contract.md)
- [DeliDev provider inspection](../../../../../docs/cmds-delidev-providers-contract.md)
- [DeliDev native read-only Sidechat](../../../../../docs/cmds-delidev-sidechat-contract.md)
- [DeliDev native subagent observations](../../../../../docs/cmds-delidev-subagents-contract.md)
- [DeliDev native subscriptions](../../../../../docs/cmds-delidev-subscription-contract.md)
- [DeliDev Session Terminals Contract](../../../../../docs/cmds-delidev-terminals-contract.md)
