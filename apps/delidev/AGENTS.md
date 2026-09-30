# DeliDev desktop

## Presentation naming

- The New session machine selector has the exact visible label and accessible name `Runs on`. Its placeholder, inventory statuses and unavailable-selection fallback use `Runner Device`; other existing `Execution Worker` / `Execution Workers` labels and user-facing messages use `Runner Device` / `Runner Devices`, including `Remediation Runner Device` and `Runner Device and harness`. This is presentation-only: preserve `Agent Worker`, generic technical Worker terminology, existing generic `execution machine` references, user-assigned names, CLI commands, structured logs/error codes, authorization, RPC/storage fields and the Settings category value `execution-workers`. Follow `docs/apps-delidev-desktop-contract.md`.

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `apps/delidev/src-tauri/AGENTS.md`
- `apps/delidev/scripts/AGENTS.md`
- `apps/delidev/src/AGENTS.md`

Keep implementation evidence in independent files under `docs/evidence/delidev/issue-<number>/`. Update instructions only when their rules or ownership change, not merely to record another validation run.
