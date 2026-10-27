# apps working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `apps/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Use `pnpm dev:labs-site` for the marketing site on fixed loopback port `46312`; conflicts fail. Follow the labs-site foundation for build and preview procedures.

- Labs documentation uses `pnpm dev:labs-docs` on fixed loopback port `46303`; follow the [Labs app contract](../docs/apps-labs-docs-foundation.md).

- Use `pnpm dev:public-docs` for documentation development on fixed loopback port `46302`; conflicts fail. Follow the public-docs contract for wrapper and shutdown behavior.

- Follow root `AGENTS.md` and project-specific docs before adding or changing app code.

- Keep repository and domain rules in the appropriate `AGENTS.md` files.

- Write all source and comments in English.

## Owning contracts

- [Shared Labs localization](../docs/apps-labs-localization-contract.md)

- [labs-site marketing foundation](../docs/apps-labs-site-foundation.md)

- [Labs documentation foundation](../docs/apps-labs-docs-foundation.md)
- [Labs documentation project](../docs/project-labs-docs.md)

- [async-commit-hook application contract](../docs/apps-async-commit-hook-contract.md)
- [binpm public documentation foundation](../docs/apps-binpm-docs-foundation.md)
- [clibox public documentation foundation](../docs/apps-clibox-docs-foundation.md)
- [Nodeup public documentation foundation](../docs/apps-nodeup-docs-foundation.md)
- [pnport public documentation](../docs/apps-pnport-docs-foundation.md)
- [apps-public-docs-foundation](../docs/apps-public-docs-foundation.md)
- [Runmoor public documentation foundation](../docs/apps-runmoor-docs-foundation.md)
- [Runmoor command foundation](../docs/cmds-runmoor-foundation.md)
- [crates-binpm-foundation](../docs/crates-binpm-foundation.md)
- [Project: binpm](../docs/project-binpm.md)
- [Project: nodeup](../docs/project-nodeup.md)
- [pnport](../docs/project-pnport.md)
- [Runmoor](../docs/project-runmoor.md)
- [Repository Environment Contract](../docs/repository-environment-contract.md)
- [Linux Package Repository Contract](../docs/repository-linux-packages-contract.md)
- [Prebuilt dependency distribution](../docs/repository-prebuilt-dependencies-contract.md)
- [Repository Workflow Contract](../docs/repository-workflow-contract.md)

- [AIPRouter](../docs/project-aiprouter.md)
- [AIPRouter domain contract](../docs/apps-aiprouter-console-contract.md)
