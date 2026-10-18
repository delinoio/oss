# servers working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `servers/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Follow root `AGENTS.md` and the owning project/domain contracts.

- Write Go code and comments in English; use `log/slog` structured logging and never log secrets or sensitive payloads.

## Owning contracts

- [DevHud Desktop Updater Contract](../docs/apps-devhud-updater-contract.md)
- [Project: devhud](../docs/project-devhud.md)
- [protos-devhud-v1-contract](../docs/protos-devhud-v1-contract.md)
- [Repository Environment Contract](../docs/repository-environment-contract.md)
- [servers-devhud-api-contract](../docs/servers-devhud-api-contract.md)
- [servers-devhud-release-controller-contract](../docs/servers-devhud-release-controller-contract.md)
