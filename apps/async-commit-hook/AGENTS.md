# apps/async-commit-hook working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `apps/async-commit-hook/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Run `pnpm test` from this directory after frontend changes. Generated `dist` is untracked and must be removed from the final worktree.

## Owning contracts

- [async-commit-hook application contract](../../docs/apps-async-commit-hook-contract.md)
- [Repository Workflow Contract](../../docs/repository-workflow-contract.md)
