# DeliDev desktop

- Follow `docs/apps-delidev-desktop-contract.md`, the full issue #964 contract and `docs/packages-delidev-api-client-contract.md`. A frontend build does not establish complete desktop acceptance.
- Keep Go business authority and direct generated Connect/Connect Query calls. Rust owns only infrastructure and native presentation. Never proxy agent traffic through Tauri or add a browser product.
- Preserve drafts and independent outcome/archive/dispatch/recovery state. Bind mutations to original revisions/request identities and expose exact retries after uncertainty; never retry a mutation automatically.
- Render content as inert text, bound pages/caches, fetch changed messages by ID and cancel stale connection work. No secrets, cursors, prompts or transcript content in logs, Web Storage or query keys.
- Keep settings modal accessible with focus containment, Escape and focus restoration without remounting the session. Give every workspace icon an accessible name. Do not reinterpret a server capability or unavailable evidence as permission to execute.
- Frontend development uses fixed loopback port 46311 and fails on conflicts. Run package `pnpm test` and build verification; remove generated `dist` output. Native Rust changes also require root Cargo tests and actual host lifecycle validation.
