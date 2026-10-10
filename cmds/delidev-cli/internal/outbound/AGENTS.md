# cmds/delidev-cli/internal/outbound working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/outbound/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Follow `docs/cmds-delidev-network-contract.md` and the parent ownership instructions.

## Owning contracts

- [DeliDev protected credential storage](../../../../docs/cmds-delidev-credentials-contract.md)
- [DeliDev explicit outbound network contract](../../../../docs/cmds-delidev-network-contract.md)
- [DeliDev native API relay contract](../../../../docs/cmds-delidev-proxy-contract.md)
