### Instructions for `apps/`

- Follow root `AGENTS.md` and project-specific docs before adding or changing app code.
- Keep app-specific contracts synchronized in the project index doc (`docs/project-*.md`) and relevant app-domain contract docs (`docs/apps-*.md`) in the same change.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Public documentation app content must not expose repository-internal implementation details. Use `docs/` as the internal source of truth, then curate `apps/public-docs`, `apps/binpm-docs`, and `apps/nodeup-docs` around user-facing behavior, supported workflows, stable public interfaces, and explicitly contracted maintainer-facing paths.
- Write all source and comments in English.
- Follow Toss Design Guidelines for frontend UX/UI decisions across web and mobile apps.
- For new static sites under `apps/`, default to Rsbuild/Rspress-style toolchains and Cloudflare Pages deployment unless a project contract documents a different platform.
- Prefer Rspack-family build tools for app build pipelines when they fit the runtime and deployment target.
- Root and package-local development use fixed loopback ports: public-docs `46302`, nodeup-docs `46303`, and binpm-docs `46304`. Development wrappers must prevent CLI address overrides, forward termination signals to their child server, wait for it to exit, and fail with an actionable conflict message instead of searching for or incrementing to another port.
- App file upload/download flows should default to Cloudflare R2 plus signed URLs unless the app contract documents a different storage or access pattern.
- If a form has a single critical input, that input must receive focus when the form is shown.
- Dialog UIs must support closing with the `Esc` key.

### Scope in This Domain

- `apps/mpapp`: Expo React Native mobile app.
- `apps/binpm-docs`: Rspress static documentation app for `binpm`.
- `apps/nodeup-docs`: Rspress static documentation app for `nodeup`.
- `apps/public-docs`: Rspress static public documentation app.
- `apps/devhud`: implemented deterministic React/TypeScript shell and Rust/Tauri CEF desktop-host foundation; product UI and mobile shell remain planned.
- `apps/devhud-chrome-extension`: planned Chrome Manifest V3 DevHud extension.
- `apps/devhud-admin`: planned DevHud administrator SPA embedded at `/admin`.

### DevHud Rules

- Implemented DevHud foundations are the `apps/devhud` desktop host, the `servers/devhud-api` Bootstrap/Settings/Account API and account/retention sweeper, `protos/devhud/v1`, and `packages/devhud-api-client`. Product UI, mobile, Upload, Diagnostics, Admin, extension, and administrator SPA runtime behavior remain documentation-first until their project/domain contracts are updated. Logto uses native callback `devhud://auth/callback`, platform client keys `desktop`/`ios`/`android`, an `admin` client key with the documented exact browser redirect, and Native Messaging host `io.delino.devhud.native_messaging` with one fixed release-configured extension ID. The Native Messaging host uses the documented authenticated v1 user-scoped IPC contract owned by the app.
- Fixed loopback ports are DevHud frontend `46305`, admin `46306`, and API `46307`; fail on conflicts and never auto-remap.
- DevHud API calls use the exact CORS origins `http://localhost:46305`, `http://127.0.0.1:46305`, `http://localhost:46306`, `http://127.0.0.1:46306`, and pinned Tauri origin `http://tauri.localhost`; direct R2 staging uploads use those origins with only `PUT`/`OPTIONS`, `Content-Type`, and `x-amz-checksum-sha256`; clients must use the documented Connect preflight behavior and read correlation IDs from the exposed `x-devhud-correlation-id` header. Upload checksums are 32 raw bytes, encoded as standard Base64 only for the R2 header.
- App and administrator API state must use the generated service-specific Connect Query exports. Clients preserve message correlation metadata, render typed errors, enforce bounded pagination, treat synchronized settings as at most 1 MiB RFC 8785 canonical JSON bytes, and explicitly resolve revision conflicts instead of silently retrying them.
- Crash submission is opt-in and user-previewed; its build/code strings obey the 256-byte identifier bound and its typed redacted summary and stack obey the 4 KiB/32 KiB bounds. Administrator surfaces validate every required mutation reason as non-blank, well-formed UTF-8 text capped at 4 KiB with credential and local-path patterns rejected before submission. They consume only metadata-only admin-safe upload views and must never display or log settings bodies, secrets, DOM, screenshots, public or signed asset locators, Deck results, agent output, or local paths.
- Desktop uses the exact pinned Tauri CEF revision `4af26a3f7f8b692d62cca549bbacd93f5ce90b41` from `https://github.com/tauri-apps/tauri`; mobile uses WKWebView/Android System WebView. Bundle ID is `io.delino.devhud`, deep-link scheme is `devhud`, and supported desktop targets are macOS 13+, Windows 10 22H2+, Ubuntu 22.04 LTS on X11, x64 and arm64. Packaged desktop hosts observe renderer termination during normal launches; deliberate renderer-crash injection remains smoke-only. Native Wayland is out of scope.
- User-facing DevHud UI, widgets, extension UI, validation, and errors support English and Korean. RealQA is desktop-only; Deck is desktop/mobile. Follow the documented browser-context, local-agent, secret, accessibility, and no-plugin boundaries.
- Deck refresh intervals are client-polling targets only; suspended widgets use OS-controlled best-effort scheduling and display stale state with the last successful refresh.
- Update `docs/project-devhud.md` and the applicable DevHud domain contract with every path, UI, platform, interface, or release change.
- The DevHud iOS widget target must use `io.delino.devhud.widget` with App Group `group.io.delino.devhud` and Keychain access group `$(AppIdentifierPrefix)io.delino.devhud.shared`; the desktop CEF session CSP may add only validated API, GitHub, and signed-upload origins. Chrome captures omit DOM-derived selectors, retain selected bounds only, and redact every path segment before persistence.
- The implemented DevHud shell keeps its first-party action registry closed and capability-filtered, shows unavailable capture/service actions as unavailable, and persists only versioned non-secret local preferences. First-run onboarding must remain usable when Web Storage is unavailable.
- Initialize and localize the native tray before the frontend can invoke tray commands. The command palette traps focus, restores its trigger on Escape and normal completion, and preserves focus for a destination surface's sole critical input. Native external links use only the documented closed allowlist and confirm opener success with a bounded background wait; timeout or opener failures must return an error and emit structured diagnostics.

### mpapp Rules

- `mpapp` must remain Expo-based unless a documented architecture decision changes it.
- Bluetooth capabilities and permissions must be explicitly documented in `docs/apps-mpapp-foundation.md`.


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
- If `apps/devhud` changes, run `pnpm --filter devhud test` and `pnpm --filter devhud verify:pins`; run its platform smoke on a supported native production artifact when the host is available.
- If `apps/binpm-docs` changes, run `pnpm --filter binpm-docs test` before finishing.
- If `apps/nodeup-docs` changes, run `pnpm --filter nodeup-docs test` before finishing.
- If `apps/public-docs` changes, run `pnpm --filter public-docs test` before finishing.
- Update relevant docs in `docs/` for every behavior, structure, or interface change.
