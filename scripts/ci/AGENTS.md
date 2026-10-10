# scripts/ci working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `scripts/ci/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Validation procedure

- Keep manual full validation and compared manual validation distinct. Validate the exact comparison identity before selecting or aggregating jobs. Retain complete event-owned checks for each selected job, and follow the workflow contract for fresh execution and selection artifacts.
- Hosted browser fixtures use a closed executable inventory. Register new approved fixtures explicitly; missing fixtures must fail rather than pass as skipped. Follow the owning QA contract for screenshot policy and fixture acceptance boundaries.

## Owning contracts

- [Repository Workflow Contract](../../docs/repository-workflow-contract.md)

- [DeliDev parallel browser QA](../../docs/apps-delidev-qa-contract.md)
