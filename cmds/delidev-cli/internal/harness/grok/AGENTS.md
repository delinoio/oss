# cmds/delidev-cli/internal/harness/grok working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/harness/grok/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Owning contracts

- [DeliDev account lifecycle](../../../../../docs/cmds-delidev-accounts-contract.md)
- [DeliDev native harness adapter contract](../../../../../docs/cmds-delidev-harness-contract.md)
- [DeliDev Grok subscription contract](../../../../../docs/cmds-delidev-grok-subscription-contract.md)
- Keep the API profile byte-for-byte unchanged when touching the managed subscription profile (`Credential` hook, `cached_token`, `subscriptionEnvironment`), and never let the harness read, return or delete the login it receives; the Worker owns credential custody.
