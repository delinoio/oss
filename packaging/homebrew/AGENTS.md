# packaging/homebrew working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `packaging/homebrew/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Run relevant release fixtures and workflow contracts when changing templates or publication. Keep README and public installation guidance synchronized with actual supported distribution channels.

## Owning contracts

- [Runmoor command foundation](../../docs/cmds-runmoor-foundation.md)
- [clibox Rust foundation](../../docs/crates-clibox-foundation.md)
- [clibox npm and native distribution](../../docs/packages-clibox-distribution-contract.md)
