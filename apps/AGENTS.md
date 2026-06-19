### Instructions for `apps/`

- Follow root `AGENTS.md` and project-specific docs before adding or changing app code.
- Keep app-specific contracts synchronized in the project index doc (`docs/project-*.md`) and relevant app-domain contract docs (`docs/apps-*.md`) in the same change.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Follow Toss Design Guidelines for frontend UX/UI decisions across web and mobile apps.
- For new static sites under `apps/`, default to Rsbuild/Rspress-style toolchains and Cloudflare Pages deployment unless a project contract documents a different platform.
- Prefer Rspack-family build tools for app build pipelines when they fit the runtime and deployment target.
- App file upload/download flows should default to Cloudflare R2 plus signed URLs unless the app contract documents a different storage or access pattern.
- If a form has a single critical input, that input must receive focus when the form is shown.
- Dialog UIs must support closing with the `Esc` key.

### Scope in This Domain

- `apps/mpapp`: Expo React Native mobile app.
- `apps/nodeup-docs`: Rspress static documentation app for `nodeup`.
- `apps/public-docs`: Mintlify public documentation app.

### mpapp Rules

- `mpapp` must remain Expo-based unless a documented architecture decision changes it.
- Bluetooth capabilities and permissions must be explicitly documented in `docs/apps-mpapp-foundation.md`.

### public-docs Rules

- `public-docs` must remain Mintlify-based unless a documented architecture decision changes it.
- `public-docs` is an existing documented exception to the default Rsbuild/Rspress-style static-site toolchain and Cloudflare Pages deployment preference.
- Mintlify page IDs and navigation in `apps/public-docs/docs.json` must stay aligned with `docs/apps-public-docs-foundation.md`.
- When user-facing documentation behavior changes, update related `apps/public-docs` pages in the same change set.

### nodeup-docs Rules

- `nodeup-docs` must remain Rspress-based unless `docs/apps-nodeup-docs-foundation.md` documents a different architecture decision.
- `nodeup-docs` must keep Cloudflare Pages as the default deployment target unless the app contract documents a different platform.
- `nodeup-docs` package scripts must include non-interactive `prepare:app`, `dev`, `build`, and `test` commands.
- Rspress route IDs and sidebar links must stay aligned with `docs/apps-nodeup-docs-foundation.md`.
- When user-facing `nodeup` documentation behavior changes, update related `apps/nodeup-docs` pages and `docs/project-nodeup.md` contracts in the same change set.

### Testing and Validation

- If frontend code changes in this domain, run `pnpm test` before finishing.
- If `apps/public-docs` changes, run `pnpm --filter public-docs test` before finishing.
- If `apps/nodeup-docs` changes, run `pnpm --filter nodeup-docs test` before finishing.
- Update relevant docs in `docs/` for every behavior, structure, or interface change.
