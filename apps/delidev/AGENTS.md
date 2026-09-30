# DeliDev desktop

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `apps/delidev/src-tauri/AGENTS.md`
- `apps/delidev/scripts/AGENTS.md`
- `apps/delidev/src/AGENTS.md`

Keep implementation evidence in independent files under `docs/evidence/delidev/issue-<number>/`. Update instructions only when their rules or ownership change, not merely to record another validation run.

- Issue #1137 makes a fresh trusted main host own one Go-admitted launch before supervision, without renderer-triggered startup. Keep same-process Stop, native-service ownership and saved-window authority independent. Use persistent Connection & diagnostics for advanced controls, with product startup/sidebar/tray wording. Follow docs/apps-delidev-desktop-contract.md.
