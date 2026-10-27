# protos working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `protos/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Follow root `AGENTS.md` and the owning project/domain contracts.

- Write schemas and comments in English.
- Generate Go bindings beside their schemas as defined in the [protocol contract](../docs/protos-async-commit-hook-v1-contract.md). Do not enable output-directory cleaning for the shared source tree.

## Owning contracts

- [async-commit-hook v1 protocol contract](../docs/protos-async-commit-hook-v1-contract.md)
