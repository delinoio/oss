# apps working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `apps/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Use `pnpm dev:public-docs` for documentation development on fixed loopback port `46302`; conflicts fail. Follow the public-docs contract for wrapper and shutdown behavior.

- Follow root `AGENTS.md` and project-specific docs before adding or changing app code.

- Keep repository and domain rules in the appropriate `AGENTS.md` files.

- Write all source and comments in English.

## Owning contracts

- [async-commit-hook application contract](../docs/apps-async-commit-hook-contract.md)
- [binpm public documentation foundation](../docs/apps-binpm-docs-foundation.md)
- [clibox public documentation foundation](../docs/apps-clibox-docs-foundation.md)
- [DeliDev desktop client](../docs/apps-delidev-desktop-contract.md)
- [DeliDev mobile client contract](../docs/apps-delidev-mobile-contract.md)
- [apps-devhud-foundation](../docs/apps-devhud-foundation.md)
- [DevHud Desktop Updater Contract](../docs/apps-devhud-updater-contract.md)
- [Nodeup public documentation foundation](../docs/apps-nodeup-docs-foundation.md)
- [pnport public documentation](../docs/apps-pnport-docs-foundation.md)
- [apps-public-docs-foundation](../docs/apps-public-docs-foundation.md)
- [React Forge public documentation](../docs/apps-react-forge-docs-foundation.md)
- [Runmoor public documentation foundation](../docs/apps-runmoor-docs-foundation.md)
- [Runmoor command foundation](../docs/cmds-runmoor-foundation.md)
- [crates-binpm-foundation](../docs/crates-binpm-foundation.md)
- [React Forge Node Contract](../docs/packages-react-forge-contract.md)
- [React Forge Figma Contract](../docs/packages-react-forge-figma-contract.md)
- [React Forge 3D scenes and animation](../docs/packages-react-forge-scene-contract.md)
- [React Forge SFX contract](../docs/packages-react-forge-sfx-contract.md)
- [Project: binpm](../docs/project-binpm.md)
- [Project: devhud](../docs/project-devhud.md)
- [Project: nodeup](../docs/project-nodeup.md)
- [pnport](../docs/project-pnport.md)
- [Runmoor](../docs/project-runmoor.md)
- [Repository Environment Contract](../docs/repository-environment-contract.md)
- [Linux Package Repository Contract](../docs/repository-linux-packages-contract.md)
- [Prebuilt dependency distribution](../docs/repository-prebuilt-dependencies-contract.md)
- [Repository Workflow Contract](../docs/repository-workflow-contract.md)
