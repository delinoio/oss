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
- `apps/binpm-docs`: Rspress static documentation app for `binpm`.
- `apps/nodeup-docs`: Rspress static documentation app for `nodeup`.
- `apps/public-docs`: Mintlify public documentation app.

### mpapp Rules

- `mpapp` must remain Expo-based unless a documented architecture decision changes it.
- Bluetooth capabilities and permissions must be explicitly documented in `docs/apps-mpapp-foundation.md`.

### binpm-docs Rules

- `binpm-docs` must remain Rspress-based unless `docs/project-binpm.md` and `docs/apps-binpm-docs-foundation.md` document a replacement.
- `binpm-docs` must use Cloudflare Pages as the default static deployment target unless the app contract documents a replacement.
- `binpm-docs` has canonical production URL `https://binpm.delino.io`.
- Rspress routes and navigation in `apps/binpm-docs/rspress.config.ts` must stay aligned with `docs/apps-binpm-docs-foundation.md`.
- Stable `binpm-docs` route IDs are `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/troubleshooting`, and `/reference`.
- `binpm-docs` content must remain documentation-only and must not imply new binpm runtime behavior before `docs/project-binpm.md` and `docs/crates-binpm-foundation.md` document it.
- `binpm-docs` content must not infer behavior, status, or page contents from the live `https://binpm.delino.io` site; repository contracts are the source of truth.
- When binpm source, target, local tooling, cache, verification, install, execution, diagnostic, or output behavior changes, update related `apps/binpm-docs` pages in the same change set.

### public-docs Rules

- `public-docs` must remain Mintlify-based unless a documented architecture decision changes it.
- `public-docs` is an existing documented exception to the default Rsbuild/Rspress-style static-site toolchain and Cloudflare Pages deployment preference.
- Mintlify page IDs and navigation in `apps/public-docs/docs.json` must stay aligned with `docs/apps-public-docs-foundation.md`.
- Current public-docs top-level product page IDs are `cargo-mono`, `derun`, and `with-watch`; Nodeup documentation is published through `apps/nodeup-docs`, not `apps/public-docs`.
- When user-facing documentation behavior changes, update related `apps/public-docs` pages in the same change set.

### nodeup-docs Rules

- `nodeup-docs` must remain Rspress-based unless `docs/project-nodeup.md` and `docs/apps-nodeup-docs-foundation.md` document a replacement.
- `nodeup-docs` must use Cloudflare Pages as the default static deployment target unless the app contract documents a replacement.
- `nodeup-docs` canonical production URL is `https://nodeup.delino.io`.
- Rspress routes and navigation in `apps/nodeup-docs/rspress.config.ts` must stay aligned with `docs/apps-nodeup-docs-foundation.md`.
- Stable `nodeup-docs` route IDs are `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, and `/reference`.
- When Nodeup user-facing runtime, release, installer, shim, completion, package-manager, or color-control behavior changes, update related `apps/nodeup-docs` pages in the same change set.

### Testing and Validation

- If frontend code changes in this domain, run `pnpm test` before finishing.
- If `apps/binpm-docs` changes, run `pnpm --filter binpm-docs test` before finishing.
- If `apps/nodeup-docs` changes, run `pnpm --filter nodeup-docs test` before finishing.
- If `apps/public-docs` changes, run `pnpm --filter public-docs test` before finishing.
- Update relevant docs in `docs/` for every behavior, structure, or interface change.
