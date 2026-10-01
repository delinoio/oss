# DeliDev desktop

- Execution-device presentation follows issue #1136 and `docs/apps-delidev-desktop-contract.md`: New session's visible and accessible machine-selector label is exactly `Runs on`; its resource noun and other former Execution Worker labels/messages are `Runner Device` / `Runner Devices`. Preserve Agent Worker, generic technical Worker terms, user-assigned names, machine IDs, RPC/storage fields, CLI commands, logs, error codes and the `execution-workers` Settings category value. `ResourceChoice.resourceLabel` defaults to `label` and changes only placeholder/status nouns.
- New schedule creation presentation is owned by `src/schedule-creation.tsx` and `src/schedule-creation.css` (relative to `apps/delidev`) and follows the issue #1152 section in `docs/apps-delidev-desktop-contract.md`: creation-only Task/Execution/Repeat layout with bounded responsive columns and an unobscured main-content action row, mounted collapsed overrides, native radios, explicit catalog states and once-only name focus. Frequency/time/weekday/disclosure stay in connection memory, emit only canonical existing Cron fields, preserve raw Custom transitions and invalid Time drafts, and never replace server calendar authority. Preserve strict schema-v1 writes, limits, fresh Local proof, exact uncertain retry and all existing edit/list/detail/history/sidebar behavior. Keep native viewport/zoom evidence separate from component checks.

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `apps/delidev/src-tauri/AGENTS.md`
- `apps/delidev/scripts/AGENTS.md`
- `apps/delidev/src/AGENTS.md`

Record implementation status and validation results in pull requests, issues and CI logs/artifacts under the root DeliDev validation policy. Do not add repository evidence documents. Update instructions only when their rules or ownership change, not merely to record another validation run.

The Schedules context pane owns the issue #1153 presentation and connection-memory retained-history disclosure. Follow `src/AGENTS.md` and `docs/apps-delidev-desktop-contract.md`; keep shared shell defaults and the Settings opening lifetime unchanged.

- Issue #1137 makes a fresh trusted main host own one Go-admitted launch before supervision, without renderer-triggered startup. Keep same-process Stop, native-service ownership and saved-window authority independent. Use persistent Connection & diagnostics for advanced controls, with product startup/sidebar/tray wording. Follow docs/apps-delidev-desktop-contract.md.
Agent Workers alone follows `docs/apps-delidev-desktop-contract.md`: use the approved centered 1040px maximum column, exact title/summary/scope, below-1100px toolbar exception, success-only empty panel and divided rows with name-scoped Edit/Preview routing/Delete actions. Keep one eligible New action, inert full names/IDs and existing schema gates. Preserve the common Settings-opening lifecycle: drafts and exact retries survive reflow and same-identity reconnect within that opening; Close, Escape and navigation away dispose it, guard late continuations and retain authoritative server effects and sibling workflows without replay or rollback. Ordinary reopening starts at AI Subscription; explicit New Project/Repositories entries remain targeted.

- Settings > Projects follows the Projects-only presentation exception in `docs/apps-delidev-desktop-contract.md` and `src/AGENTS.md`. Preserve issue #1138 opening disposal and the shared shell and other categories.

- Home-only sidebar Inbox/Search actions and their consumed wide/compact focus handoff follow `docs/apps-delidev-desktop-contract.md` and the scoped `src/AGENTS.md` rules. Preserve mounted connection state and existing read-only navigation.

GitHub Integrations presentation follows `docs/apps-delidev-desktop-contract.md` and `docs/cmds-delidev-integrations-contract.md`. Keep one create action, truthful read states, separate token-storage/identity observations, complete official-form guidance and the shared Settings lifecycle. The source owner retains exact mutation, credential and pagination safeguards.

Diagnostics presentation is owned by `src/doctor.tsx`, `src/doctor.css` and the scoped frontend instructions, following `docs/apps-delidev-diagnostics-contract.md`.

Unsupported-schema Agent display names/aliases are projected only within the existing 256-byte UTF-8 Agent name limit; larger values keep the Unnamed fallback and disabled actions while preserving the full resource ID.

- Activity filter presentation is owned by `src/activity-sidebar.css` and the existing Activity controller in `src/views.tsx`. Follow the bounded issue #1156 treatment in `docs/apps-delidev-desktop-contract.md` and the scoped source instructions; preserve shared shell/server controls and the authority of issues #1137/#1149.

- Import / Export presentation alone is owned by `src/configuration-transfer.tsx` and `src/configuration-transfer.css` under issue #1243 and the desktop/portable-configuration contracts. Keep its centered 880px column, exact panel/input/guidance copy, noninteractive state-derived stages and complete mapping/review/retry/results. Preserve original JSON bytes, bigint revisions, every existing operation/guard and Settings-host lifetime/locks; styling/reflow cannot remount controllers or acquire shell ownership from #1236. Record browser/component and native acceptance separately in PRs/issues/CI, never repository evidence files.
