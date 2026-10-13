# scripts/ci working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `scripts/ci/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

- Propagate the explicit CI execution mode and exact planner comparison to every selector and result check. Do not infer full validation from a manual event alone.

- Keep the actionlint `queue: max` compatibility adapter in `workflows.mjs` limited to the two exact declarations in its allow-list (Linux publisher job-level, mobile beta workflow-level). Mask only the validated queue line and reject all other queue declarations.

## Owning contracts

- [Repository Workflow Contract](../../docs/repository-workflow-contract.md)
