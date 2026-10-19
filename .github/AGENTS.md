# .github working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `.github/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

- Resolve CI platform matrices before runner allocation. Do not allocate macOS or iOS runners in CI; retain owning release gates.

- The separate pnport Windows development workflow runs native x64/arm64 checks on affected PRs and manual dispatch. Keep CI.yml native exclusions and release gates unchanged.

## Owning contracts

- [pnport](../docs/project-pnport.md)
- [Repository Workflow Contract](../docs/repository-workflow-contract.md)
