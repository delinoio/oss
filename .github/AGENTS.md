# .github working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `.github/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Owning contracts

- [DeliDev native package verification](../docs/apps-delidev-packaging-contract.md)
- [DeliDev provider and model catalog](../docs/cmds-delidev-catalog-contract.md)
- [pnport](../docs/project-pnport.md)
- [Repository Workflow Contract](../docs/repository-workflow-contract.md)

## Browser regression validation

- The DeliDev frontend checks phase runs the synthetic computed appearance color regression in runner-owned Chromium. Install its exact pinned Playwright version and browsers outside the checkout; keep product dependencies unchanged and do not treat browser fixture results as native CEF/platform acceptance. Follow the repository workflow contract.
