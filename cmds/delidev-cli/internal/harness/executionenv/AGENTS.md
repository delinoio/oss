# cmds/delidev-cli/internal/harness/executionenv working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/harness/executionenv/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Owning contracts

- [DeliDev native harness adapter contract](../../../../../docs/cmds-delidev-harness-contract.md)

- Native homes passed to adapters come from `internal/nativeprofile` persistent profiles; adapters must verify and reuse owned state instead of requiring empty runtimes. See the [persistent native profiles contract](../../../../../docs/cmds-delidev-harness-contract.md#persistent-native-profiles).
