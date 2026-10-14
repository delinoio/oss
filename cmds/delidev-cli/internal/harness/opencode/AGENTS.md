# cmds/delidev-cli/internal/harness/opencode working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/harness/opencode/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Image input

OpenCode image inputs follow the [image input contract](../../../../../docs/cmds-delidev-image-input-contract.md#closed-adapter-profiles) and the [harness contract](../../../../../docs/cmds-delidev-harness-contract.md#opencode-image-input). Keep text-only bodies and `TextInputClaimDigest` byte-identical, keep Worker paths and image bytes out of claims and logs, accept only the closed native expansion verified by SHA-256, and keep replacement, Fork, Sidechat and compaction profiles rejecting image-bearing history.

## Owning contracts

- [DeliDev account lifecycle](../../../../../docs/cmds-delidev-accounts-contract.md)
- [DeliDev native session compaction](../../../../../docs/cmds-delidev-compaction-contract.md)
- [DeliDev protected credential storage](../../../../../docs/cmds-delidev-credentials-contract.md)
- [DeliDev direct execution startup](../../../../../docs/cmds-delidev-execution-startup-contract.md)
- [DeliDev same-account native session forks](../../../../../docs/cmds-delidev-forks-contract.md)
- [DeliDev native harness adapter contract](../../../../../docs/cmds-delidev-harness-contract.md)
- [DeliDev provider inspection](../../../../../docs/cmds-delidev-providers-contract.md)
- [DeliDev native subagent observations](../../../../../docs/cmds-delidev-subagents-contract.md)
- [DeliDev Session Terminals Contract](../../../../../docs/cmds-delidev-terminals-contract.md)
- [DeliDev native usage ledger](../../../../../docs/cmds-delidev-usage-contract.md)
