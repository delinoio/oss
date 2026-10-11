# .github working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `.github/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

- Run DeliDev repository popup geometry and dismissal checks in the frontend checks phase using the isolated browser runner described in the [workflow contract](../docs/repository-workflow-contract.md#repository-sidebar-popup-browser-checks). Keep browser fixture evidence separate from native acceptance.

## Owning contracts

- [DeliDev native package verification](../docs/apps-delidev-packaging-contract.md)
- [DeliDev provider and model catalog](../docs/cmds-delidev-catalog-contract.md)
- [pnport](../docs/project-pnport.md)
- [Repository Workflow Contract](../docs/repository-workflow-contract.md)
