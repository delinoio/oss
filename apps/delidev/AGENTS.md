# DeliDev desktop

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `apps/delidev/src-tauri/AGENTS.md`
- `apps/delidev/scripts/AGENTS.md`
- `apps/delidev/src/AGENTS.md`

Keep implementation evidence in independent files under `docs/evidence/delidev/issue-<number>/`. Update instructions only when their rules or ownership change, not merely to record another validation run.

- Home-only sidebar Inbox/Search actions and their consumed wide/compact focus handoff follow `docs/apps-delidev-desktop-contract.md` and the scoped `src/AGENTS.md` rules. Preserve mounted connection state and existing read-only navigation.

GitHub Integrations presentation follows `docs/apps-delidev-desktop-contract.md` and `docs/cmds-delidev-integrations-contract.md`. Keep one create action, truthful read states, separate token-storage/identity observations, complete official-form guidance and the shared Settings lifecycle. The source owner retains exact mutation, credential and pagination safeguards.

Diagnostics presentation is owned by `src/doctor.tsx`, `src/doctor.css` and the scoped frontend instructions, following `docs/apps-delidev-diagnostics-contract.md`.
