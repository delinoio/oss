# DeliDev desktop
- New schedule creation presentation is owned by `src/schedule-creation.tsx` and `src/schedule-creation.css` (relative to `apps/delidev`) and follows the issue #1152 section in `docs/apps-delidev-desktop-contract.md`: creation-only Task/Execution/Repeat layout with bounded responsive columns and an unobscured main-content action row, mounted collapsed overrides, native radios, explicit catalog states and once-only name focus. Frequency/time/weekday/disclosure stay in connection memory, emit only canonical existing Cron fields, preserve raw Custom transitions and invalid Time drafts, and never replace server calendar authority. Preserve strict schema-v1 writes, limits, fresh Local proof, exact uncertain retry and all existing edit/list/detail/history/sidebar behavior. Keep native viewport/zoom evidence separate from component checks.

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `apps/delidev/src-tauri/AGENTS.md`
- `apps/delidev/scripts/AGENTS.md`
- `apps/delidev/src/AGENTS.md`

Keep implementation evidence in independent files under `docs/evidence/delidev/issue-<number>/`. Update instructions only when their rules or ownership change, not merely to record another validation run.

The Schedules context pane owns the issue #1153 presentation and connection-memory retained-history disclosure. Follow `src/AGENTS.md` and `docs/apps-delidev-desktop-contract.md`; keep shared shell defaults and the Settings opening lifetime unchanged.

Agent Workers alone follows `docs/apps-delidev-desktop-contract.md`: use the approved centered 1040px maximum column, exact title/summary/scope, below-1100px toolbar exception, success-only empty panel and divided rows with name-scoped Edit/Preview routing/Delete actions. Keep one eligible New action, inert full names/IDs and existing schema gates. Preserve the common Settings-opening lifecycle: drafts and exact retries survive reflow and same-identity reconnect within that opening; Close, Escape and navigation away dispose it, guard late continuations and retain authoritative server effects and sibling workflows without replay or rollback. Ordinary reopening starts at AI Subscription; explicit New Project/Repositories entries remain targeted.

- Settings > Projects follows the Projects-only presentation exception in `docs/apps-delidev-desktop-contract.md` and `src/AGENTS.md`. Preserve issue #1138 opening disposal and the shared shell and other categories.

- Home-only sidebar Inbox/Search actions and their consumed wide/compact focus handoff follow `docs/apps-delidev-desktop-contract.md` and the scoped `src/AGENTS.md` rules. Preserve mounted connection state and existing read-only navigation.

GitHub Integrations presentation follows `docs/apps-delidev-desktop-contract.md` and `docs/cmds-delidev-integrations-contract.md`. Keep one create action, truthful read states, separate token-storage/identity observations, complete official-form guidance and the shared Settings lifecycle. The source owner retains exact mutation, credential and pagination safeguards.

Diagnostics presentation is owned by `src/doctor.tsx`, `src/doctor.css` and the scoped frontend instructions, following `docs/apps-delidev-diagnostics-contract.md`.

Unsupported-schema Agent display names/aliases are projected only within the existing 256-byte UTF-8 Agent name limit; larger values keep the Unnamed fallback and disabled actions while preserving the full resource ID.
