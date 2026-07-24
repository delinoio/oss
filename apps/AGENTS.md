### Instructions for `apps/`

- Follow root `AGENTS.md` and project-specific docs before adding or changing app code.
- Keep app-specific contracts synchronized in the project index doc (`docs/project-*.md`) and relevant app-domain contract docs (`docs/apps-*.md`) in the same change.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Public documentation app content must not expose repository-internal implementation details. Use `docs/` as the internal source of truth, then curate `apps/public-docs`, `apps/binpm-docs`, and `apps/nodeup-docs` around user-facing behavior, supported workflows, stable public interfaces, and explicitly contracted maintainer-facing paths.
- Write all source and comments in English.
- Follow Toss Design Guidelines for frontend UX/UI decisions across web and mobile apps.
- For new static sites under `apps/`, default to Rsbuild/Rspress-style toolchains and Cloudflare Pages deployment unless a project contract documents a different platform.
- Prefer Rspack-family build tools for app build pipelines when they fit the runtime and deployment target.
- App file upload/download flows should default to Cloudflare R2 plus signed URLs unless the app contract documents a different storage or access pattern.
- If a form has a single critical input, that input must receive focus when the form is shown.
- Dialog UIs must support closing with the `Esc` key.

### Scope in This Domain

- `apps/mpapp`: Expo React Native mobile app.
- `apps/delidev-app`: React/TypeScript/Rsbuild Cloudflare Pages PWA for project `delidev`.
- `apps/devhud`: local-only React/TypeScript/Rsbuild plus Tauri common feasibility scaffold for project `devhud`; the sole canonical DevHud implementation path and currently gate-blocked.
- `apps/binpm-docs`: Rspress static documentation app for `binpm`.
- `apps/nodeup-docs`: Rspress static documentation app for `nodeup`.
- `apps/public-docs`: Rspress static public documentation app.

### mpapp Rules

- `mpapp` must remain Expo-based unless a documented architecture decision changes it.
- Bluetooth capabilities and permissions must be explicitly documented in `docs/apps-mpapp-foundation.md`.

### DevHud Rules

- `apps/devhud` is the sole canonical implementation path for `devhud`. Keep it independent from `apps/delidev-app`, DeliDev accounts, catalog, billing, APIs, routes, contracts, and authentication.
- The feasibility package, Rust workspace member, and isolated native macOS CEF gate are present. They must remain a non-product bundled-asset probe with package-local deterministic checks. The gate may privately build and validate x64/ARM64 DMGs and updater bundles, but it must retain only safe path-free evidence and never publish packages or signing material. Do not add product, mobile/widget, production packaging/updater, release, publisher, or support work while the complete gate is blocked.
- Desktop uses the pinned upstream CEF runtime and sandbox directly; target-specific dependencies reserve standard Tauri iOS/Android system webviews for later work from the same package. Do not create Tauri/WRY/`cef-rs` forks or local runtime patches, and never follow the moving `feat/cef` branch.
- The exact upstream pin includes the macOS `TerminationSignals` target-guard correction. Do not reintroduce a downstream `libc` shim or local upstream patch; native x64/ARM64 gate evidence, rather than compilation alone, determines whether a macOS condition passed.
- The pinned upstream revision exposes renderer-termination callbacks publicly only on macOS/iOS and discards the CEF handler on Windows/Linux. This separate failed gate also stops product-foundation and release work pending an architecture decision.
- Preserve the exact DevHud identifiers `dev.deli.devhud`, `devhud.settings.v1`, `devhud.widget-configuration.v1`, `group.dev.deli.devhud`, and `dev.deli.devhud.widget`.
- Production tools and user-visible widgets remain empty in `0.1.0`. Compile-only WidgetKit and Android AppWidget foundations must not be embedded or manifest-registered, and no CLI, backend, public API, plugin SDK, deep link, telemetry, account system, or DeliDev integration is authorized.
- The only network exception is unauthenticated GitHub Releases update discovery/download for compatible signed `devhud@v*` releases. Never add GitHub tokens, remote configuration, telemetry, or another service dependency.
- The scaffold's package-local tasks cover deterministic frontend build/rebuild, typecheck, lint, unit probes, contract/pin checks, lockfile checks, Rust checks, a debug desktop build, host-appropriate smoke startup, and the native macOS x64/ARM64 gate. Accessibility, the remaining desktop matrix, mobile/widget, production packaging/updater, release, SBOM, provenance, and measurement tasks remain blocked and must not be represented by passing placeholders.
- Release publication requires the documented signing and publisher prerequisites, architecture-specific desktop artifacts, TestFlight/Google Play beta builds, and the documented manual rollback/upstream-pin/support runbooks. Missing credentials or the unresolved CEF gate blocks publication. No release automation exists.

### DeliDev Rules

- `apps/delidev-app` is owned by project `delidev`; its contract is `docs/apps-delidev-app-foundation.md`.
- Canonical future origin is `https://deli.dev`; produce only a static artifact under `dist` with SPA fallback. Do not activate or deploy the site in issue #722.
- Use React, TypeScript, Rsbuild, React Query, and `@connectrpc/connect-query`; consume the versioned `delibase.v1` contract from `protos/delibase/v1`.
- Stable routes, anonymous catalog boundaries, authenticated organization routes, Logto trust boundary, unique Logto-`sub` onboarding identity, and the `https://delibase.deli.dev` API origin/audience are defined in the domain contract and must remain synchronized with delibase/proto docs.
- Cache only versioned static shell and public catalog data. Never cache authenticated organization/team data, balances, ledgers, usage, invitation tokens, or auth tokens; disable server-backed actions offline.
- Keep Logto access, refresh, and ID tokens in memory only. PKCE state and non-sensitive one-shot protected return paths may use same-tab `sessionStorage` solely across the Logto redirect; consume the return path on callback and never place it in `localStorage`, React Query, the service worker, logs, or diagnostics. Invitation return handoffs must not serialize raw bearer tokens, must be bound to the matching OIDC callback state, and must keep bearer tokens out of document metadata.
- Follow Toss Design Guidelines and WCAG 2.2 AA, including focus management, `Esc` dialog closing, keyboard navigation, and screen-reader states.
- `pnpm --filter delidev-app typecheck`, `pnpm --filter delidev-app lint`, `pnpm --filter delidev-app test`, and `pnpm --filter delidev-app build` are the baseline checks once the app exists, alongside PWA/accessibility/browser validation.
- `apps/delidev-app/scripts/postbuild.mjs` owns deterministic SPA fallback and versioned service-worker generation. The app typecheck, test, and build commands must build the generated Connect client first. `pnpm --filter delidev-app test:pwa` validates the generated artifact and sensitive-cache boundary, and CI must reject a deterministic rebuild that changes the checked-in `dist` artifact.
- Cloudflare Pages configuration is artifact-only in `apps/delidev-app/wrangler.jsonc`; changing it must not activate, create, or deploy a Pages project.

### binpm-docs Rules

- `binpm-docs` must remain Rspress-based unless `docs/project-binpm.md` and `docs/apps-binpm-docs-foundation.md` document a replacement.
- `binpm-docs` must use Cloudflare Pages as the default static deployment target unless the app contract documents a replacement.
- `binpm-docs` has canonical production URL `https://binpm.delino.io`.
- Rspress routes and navigation in `apps/binpm-docs/rspress.config.ts` must stay aligned with `docs/apps-binpm-docs-foundation.md`.
- `binpm-docs` must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in top-level social links and in the document-page footer.
- `binpm-docs` top-level navigation must include all stable docs routes so the mobile site navigation exposes the same stable route set as the documentation sidebar.
- `binpm-docs` must provide a skip-to-content link, expose user-facing accessible names for search, repository, theme, mobile navigation, sidebar, page-outline, permalink, and code-copy controls, keep closed mobile navigation drawers out of the focus order, keep decorative heading permalink markers out of accessible heading names, and support closing mobile drawers with `Esc`.
- `binpm-docs` must expose the Rspress search overlay as an accessible modal dialog with a role and accessible name, contained keyboard focus while open, a named focusable close button, `Esc` close behavior, focus return to the search trigger, and unchanged search result navigation.
- Stable `binpm-docs` route IDs are `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/releases`, `/troubleshooting`, and `/reference`.
- `binpm-docs` must keep Rspress clean URLs enabled and validate that stable route IDs have build output artifacts and generated internal links do not use `.html` suffixes.
- `binpm-docs` content must remain documentation-only and must not imply new binpm runtime behavior before `docs/project-binpm.md` and `docs/crates-binpm-foundation.md` document it.
- `binpm-docs` content must not infer behavior, status, or page contents from the live `https://binpm.delino.io` site; repository contracts are the source of truth.
- `binpm-docs` must not document repository-internal implementation details from those source contracts unless the detail is itself a stable public interface, user-visible behavior, or explicitly public maintainer workflow.
- binpm direct-installer guidance must include copy-pasteable latest remote POSIX and PowerShell commands that use the short docs-site URLs `https://binpm.delino.io/install.sh` and `https://binpm.delino.io/install.ps1`, preserve current and tag- or commit-pinned first-party `delinoio/oss` raw GitHub examples, keep `scripts/install/binpm.sh` and `scripts/install/binpm.ps1` visible for maintainer workflows, describe checksum verification through `SHA256SUMS`, and distinguish binpm release artifact verification from package verification for tools installed by binpm.
- binpm installation and release guidance must describe Homebrew as prebuilt-only, describe disabled `cargo-binstall` quick-install and compile fallbacks, and distinguish first-party binpm release platforms from broader third-party target parsing support.
- When binpm source, target, local tooling, cache, verification, install, execution, release distribution, installer, diagnostic, or output behavior changes, update related `apps/binpm-docs` pages in the same change set.

### public-docs Rules

- `public-docs` must remain Rspress-based and use Cloudflare Pages static output unless its project contract documents a replacement.
- Rspress routes, navigation, and sidebar in `apps/public-docs/rspress.config.ts` must stay aligned with `docs/apps-public-docs-foundation.md`.
- `public-docs` must use clean URLs, write production output to `apps/public-docs/doc_build`, and validate stable route artifacts plus generated internal `.html` links through `pnpm --filter public-docs test`.
- Current public-docs in-site top-level product page IDs are `cargo-mono`, `derun`, and `with-watch`.
- Nodeup and binpm are major public projects exposed from `apps/public-docs` through external top-level navigation links: Nodeup points to `https://nodeup.delino.io` and binpm points to `https://binpm.delino.io`.
- The legacy `/nodeup` public-docs route must remain a lightweight handoff page to `https://nodeup.delino.io` for compatibility with previously shared URLs.
- Do not add or restore in-site `nodeup` or `binpm` guide routes under `apps/public-docs`; the lightweight legacy `/nodeup` handoff is the sole in-site Nodeup route, and their public documentation is owned by `apps/nodeup-docs` and `apps/binpm-docs`.
- `public-docs` must curate repository contracts into public guidance and must not document repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.
- When user-facing documentation behavior changes, update related `apps/public-docs` pages in the same change set.

### nodeup-docs Rules

- `nodeup-docs` must remain Rspress-based unless `docs/project-nodeup.md` and `docs/apps-nodeup-docs-foundation.md` document a replacement.
- `nodeup-docs` must use Cloudflare Pages as the default static deployment target unless the app contract documents a replacement.
- `nodeup-docs` canonical production URL is `https://nodeup.delino.io`.
- Rspress routes and navigation in `apps/nodeup-docs/rspress.config.ts` must stay aligned with `docs/apps-nodeup-docs-foundation.md`.
- `nodeup-docs` must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in top-level social links and in the document-page footer.
- Stable `nodeup-docs` route IDs are `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, and `/reference`.
- `nodeup-docs` generated theme controls must preserve keyboard and screen-reader accessibility: mobile documentation navigation closes on `Esc`, returns focus to its opener, keeps closed mobile-sidebar links out of the tab order without hiding the persistent desktop sidebar, uses a labeled mobile search button, avoids redundant ambiguous hamburger labels, keeps search overlays clear of the sticky header, removes decorative heading anchors from sequential keyboard navigation, and keeps Markdown tables horizontally readable on mobile viewports.
- Nodeup installation guidance must include an install-method chooser near the top of the installation page and briefly explain when to use Homebrew, direct installers, `cargo-binstall`, and binpm.
- Nodeup direct-installer guidance must include copy-pasteable remote POSIX and PowerShell commands that use the public Nodeup docs-site entrypoints `https://nodeup.delino.io/install.sh` and `https://nodeup.delino.io/install.ps1`, preserve current and pinned first-party `delinoio/oss` raw GitHub URL examples, keep `scripts/install/nodeup.sh` and `scripts/install/nodeup.ps1` visible for maintainer workflows, describe checksum verification through `SHA256SUMS`, and distinguish unsupported-host, missing release material, and checksum verification failures.
- `nodeup-docs` must not document repository-internal implementation details from source contracts unless the detail is itself a stable public interface, user-visible behavior, or explicitly public maintainer workflow.
- Nodeup installation, release, and troubleshooting guidance must explain that `cargo-binstall` uses first-party release assets only and does not enable `quick-install` or `compile` fallback strategies.
- Nodeup release and installation guidance must explain that `amd64` release asset names correspond to x64 hosts.
- Nodeup completion guidance must document the difference between generating a completion script and installing or sourcing it for each supported shell.
- When Nodeup user-facing runtime, release, installer, shim, completion, package-manager, or color-control behavior changes, update related `apps/nodeup-docs` pages in the same change set.

### Testing and Validation

- If frontend code changes in this domain, run `pnpm test` before finishing.
- If `apps/binpm-docs` changes, run `pnpm --filter binpm-docs test` before finishing.
- If `apps/nodeup-docs` changes, run `pnpm --filter nodeup-docs test` before finishing.
- If `apps/public-docs` changes, run `pnpm --filter public-docs test` before finishing.
- If `apps/devhud` changes after implementation begins, run its documented typecheck, lint, unit/accessibility, desktop/mobile/widget, and release-validation tasks; frontend changes also require the repository frontend test baseline.
- Update relevant docs in `docs/` for every behavior, structure, or interface change.
