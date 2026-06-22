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
- `binpm-docs` must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in top-level social links and in the document-page footer.
- Stable `binpm-docs` route IDs are `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/releases`, `/troubleshooting`, and `/reference`.
- `binpm-docs` must keep Rspress clean URLs enabled and validate that stable route IDs have build output artifacts and generated internal links do not use `.html` suffixes.
- `binpm-docs` content must remain documentation-only and must not imply new binpm runtime behavior before `docs/project-binpm.md` and `docs/crates-binpm-foundation.md` document it.
- `binpm-docs` content must not infer behavior, status, or page contents from the live `https://binpm.delino.io` site; repository contracts are the source of truth.
- binpm direct-installer guidance must include copy-pasteable remote POSIX and PowerShell commands that use first-party `delinoio/oss` raw GitHub URLs, keep `scripts/install/binpm.sh` and `scripts/install/binpm.ps1` visible for maintainer workflows, present `cosign` as a required prerequisite, and distinguish binpm release artifact verification from package verification for tools installed by binpm.
- When binpm source, target, local tooling, cache, verification, install, execution, release distribution, installer, diagnostic, or output behavior changes, update related `apps/binpm-docs` pages in the same change set.

### public-docs Rules

- `public-docs` must remain Mintlify-based unless a documented architecture decision changes it.
- `public-docs` is an existing documented exception to the default Rsbuild/Rspress-style static-site toolchain and Cloudflare Pages deployment preference.
- Mintlify page IDs and navigation in `apps/public-docs/docs.json` must stay aligned with `docs/apps-public-docs-foundation.md`.
- Current public-docs in-site top-level product page IDs are `cargo-mono`, `derun`, and `with-watch`.
- Nodeup and binpm are major public projects exposed from `apps/public-docs` through external top-level navigation links: Nodeup points to `https://nodeup.delino.io` and binpm points to `https://binpm.delino.io`.
- The legacy `/nodeup` public-docs route must remain a lightweight handoff page to `https://nodeup.delino.io` for compatibility with previously shared URLs.
- Do not add or restore in-site `nodeup` or `binpm` guide routes under `apps/public-docs`; their public documentation is owned by `apps/nodeup-docs` and `apps/binpm-docs`.
- When user-facing documentation behavior changes, update related `apps/public-docs` pages in the same change set.

### nodeup-docs Rules

- `nodeup-docs` must remain Rspress-based unless `docs/project-nodeup.md` and `docs/apps-nodeup-docs-foundation.md` document a replacement.
- `nodeup-docs` must use Cloudflare Pages as the default static deployment target unless the app contract documents a replacement.
- `nodeup-docs` canonical production URL is `https://nodeup.delino.io`.
- Rspress routes and navigation in `apps/nodeup-docs/rspress.config.ts` must stay aligned with `docs/apps-nodeup-docs-foundation.md`.
- `nodeup-docs` must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in top-level social links and in the document-page footer.
- Stable `nodeup-docs` route IDs are `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, and `/reference`.
- `nodeup-docs` generated theme controls must preserve keyboard and screen-reader accessibility: mobile documentation navigation closes on `Esc`, returns focus to its opener, keeps closed mobile-sidebar links out of the tab order without hiding the persistent desktop sidebar, uses a labeled mobile search button, avoids redundant ambiguous hamburger labels, keeps search overlays clear of the sticky header, removes decorative heading anchors from sequential keyboard navigation, and keeps Markdown tables horizontally readable on mobile viewports.
- Nodeup direct-installer guidance must include copy-pasteable remote POSIX and PowerShell commands that use first-party `delinoio/oss` raw GitHub URLs, keep `scripts/install/nodeup.sh` and `scripts/install/nodeup.ps1` visible for maintainer workflows, present `cosign` as a required prerequisite before installer commands, and distinguish missing-prerequisite failures from checksum or Sigstore verification failures.
- Nodeup installation, release, and troubleshooting guidance must explain that `cargo-binstall` uses first-party release assets only and does not enable `quick-install` or `compile` fallback strategies.
- When Nodeup user-facing runtime, release, installer, shim, completion, package-manager, or color-control behavior changes, update related `apps/nodeup-docs` pages in the same change set.

### Testing and Validation

- If frontend code changes in this domain, run `pnpm test` before finishing.
- If `apps/binpm-docs` changes, run `pnpm --filter binpm-docs test` before finishing.
- If `apps/nodeup-docs` changes, run `pnpm --filter nodeup-docs test` before finishing.
- If `apps/public-docs` changes, run `pnpm --filter public-docs test` before finishing.
- Update relevant docs in `docs/` for every behavior, structure, or interface change.
