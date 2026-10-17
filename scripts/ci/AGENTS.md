# scripts/ci working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `scripts/ci/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

- Propagate the explicit CI execution mode and exact planner comparison to every selector and result check. Do not infer full validation from a manual event alone.
- pnport Windows development validation uses `pnport-windows.ps1` on the package-owned native x64/arm64 runners. Keep it distinct from complete release and minimum-OS acceptance; follow the owning repository workflow contract.

- The dedicated pnport Windows workflow reuses this validator for affected PRs and manual dispatch; derive both native runners from the package-owned development matrix.

## Owning contracts

- [Repository Workflow Contract](../../docs/repository-workflow-contract.md)
