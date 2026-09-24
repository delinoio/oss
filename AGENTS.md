### Instructions

- Use the `@docs/` directory as the source of truth for project contracts and implementation documents.
- All repository-wide rules must be defined in the appropriate AGENTS.md.
- Every repository-owned directory named `dist` is ignored generated output and must never be tracked. Generate required `dist` content explicitly before compilation, testing, or packaging, and remove generated `dist` directories from the final worktree.
- List files in `docs/` before starting each task, and keep `docs/` up-to-date.
- After completing each task, update the relevant `AGENTS.md` and `docs/` files in the same change when policies, structure, or contracts changed.
- For documentation authoring and editing tasks, do not arbitrarily omit, delete, or simplify requested or source-backed content; if content, scope, or intent is ambiguous, ask the user before deciding what to remove, merge, or reinterpret; if the documentation change affects repository or domain policy boundaries, update or create the relevant `AGENTS.md` file in the same change when needed.
- Public documentation surfaces must not document repository-internal implementation details. Keep internal source-of-truth contracts, architecture notes, repo-local paths, and operational internals in `docs/`; curate public docs under `apps/*-docs` and `apps/public-docs` around user-facing behavior, supported workflows, stable public interfaces, and maintainer-facing paths only when those paths are explicitly part of the public contract.
- Write all code and comments in English.
- When introducing a workaround, leave sufficient comments that explain why it exists, its scope, and the conditions for removing it.
- Prefer enum types over strings whenever possible.
- If you modified Rust code, run `cargo test` from the root directory before finishing your task.
- If you modified frontend code, run `pnpm test` from the frontend directory before finishing your task.
- Commit your work as frequent as possible using git. Do NOT use `--no-verify` flag.
- Run `git commit` only after `git add`; once files are staged, commit without unnecessary delay so staged changes are preserved in history.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- Root `pnpm install` must install Lefthook in linked worktrees when the effective `core.hooksPath` resolves to Git's shared common-directory hooks path, preserve Lefthook's protective failure for unrelated custom hook paths, and skip hook installation without blocking app preparation when Git metadata is unavailable.
- Root `pnpm dev` is the DevHud team workflow. Documentation development uses `pnpm dev:public-docs` on the consolidated fixed port documented in `apps/AGENTS.md`; it fails on conflicts instead of automatically remapping a server.
- `docs/repository-environment-contract.md` is the source of truth for environment ownership. Root development orchestration may pass only the bounded `DEVHUD_LOCAL_MODE` selector, optional non-secret `CARGO_HOME` and `RUSTUP_HOME` tool locations, and platform-conditional Linux X11/XWayland `DISPLAY`, `XAUTHORITY`, and per-user `XDG_RUNTIME_DIR` session/runtime context through Turbo's exact task environment allowlist; team values are injected and validated only by their owning service wrapper, whose loopback HTTP validation must reject numeric IPv4 spellings not accepted by Go and whose public asset-base validation must inspect the raw path before WHATWG normalization. Exact API/administrator issuer parity must be proved before migration and pinned through migration plus the API, administrator, and frontend Turbo launches without writing a raw value; the frontend wrapper may receive only that validated public issuer and may use only its origin in the fixed development CSP. OSS mode must never invoke Infisical. Team startup is non-interactive and exclusive while that private comparison pin exists, checkout identity material is published atomically, and every preflight, startup, or cleanup child run under installed root signal handlers must be lifecycle-tracked. Windows process-tree termination utilities receive only minimal system lookup context, never validated service configuration. Docker children may additionally inherit only `DOCKER_HOST` and `DOCKER_CONTEXT`, but OSS startup must reject effective remote daemon endpoints before Compose startup. OSS startup is exclusive per checkout through cleanup, and an idempotent one-shot database step must repair missing Logto database creation in preserved PostgreSQL volumes before Logto seeding. Environment tests must inject temporary generated state and an external temporary Infisical config directory, and must never replace checkout identity or project-binding material.
- Keep `.infisical.json`, `.dev-environment/`, real `.env` files, user credentials, and production/release/signing credentials uncommitted. Service-local `.env.example` files contain names, validation guidance, placeholders, and safe loopback defaults only; do not add a root environment example or expose internal secret paths in public product docs.
- After addressing pull request review comments and pushing updates, mark the corresponding review threads as resolved.
- When no explicit scope is specified and you are currently working within a pull request scope, interpret instructions within the current pull request scope.
- Do not guess; rather search for the web.
- Debug by logging. You should write enough logging code.
- Write sufficient logs for debugging and operational troubleshooting.
- Prefer structured logging libraries for business and system logs (Go: `log/slog`, Rust: `tracing`).
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
- Prefer React Query for frontend server-state management when it is available.
- When using React Query with Connect RPC, use `@connectrpc/connect-query` from `https://github.com/connectrpc/connect-query-es`.
- When accessing `github.com`, use the GitHub CLI (`gh`) instead of browser-based workflows when possible.
- Run GitHub CLI (`gh`) commands outside sandbox restrictions by default; use the required approval flow when escalation is needed.
- When writing shell commands or scripts, treat backticks and command substitution carefully, prefer `$(...)` over legacy backticks, and apply strict escaping for all dynamic values.
- If an operation is blocked by sandbox restrictions, retry it without sandbox restrictions using the required approval flow.

### Monorepo Structure Map

- `docs/`: Source of truth for project contracts and repository documentation.
- `apps/`: User-facing apps and documentation web surfaces.
- `crates/`: Rust crates and Rust-based tooling.
- `cmds/`: Go command tools for workflow orchestration.
- `servers/`: Backend services, including the implemented DevHud API foundation.
- `protos/`: Versioned protocol schemas, including the implemented DevHud Connect RPC schemas and committed Go bindings.
- `packages/`: Shared generated and runtime packages, including the implemented DevHud API client.
- `packaging/`: Package-manager template assets for release automation.
- `.agents/skills/`: Workspace-local Codex skills and reusable agent workflows.

### Canonical Directory Map

- `docs/README.md`: Canonical docs catalog and naming rules.
- `docs/repository-defaults.md`: Repository-wide default technology choices.
- `docs/repository-environment-contract.md`: Repository configuration classification, local development modes, secret ownership, and orchestration contract.
- `docs/project-template.md`: Required structure for `project-<id>` index docs.
- `docs/domain-template.md`: Required structure for domain-level contract docs.
- `docs/project-<id>.md`: Canonical project index docs (ownership + domain-doc index + cross-domain invariants).
- `docs/<domain>-<project-or-component>-<contract>.md`: Canonical domain contract docs (`apps`, `cmds`, `servers`, `crates`, `protos`, `packages`).
- `docs/repository-<topic>-contract.md`: Canonical repository-level contract docs for cross-project workflow and policy contracts.
- `docs/project-binpm.md`: binpm binary package manager project index.
- `docs/apps-binpm-docs-foundation.md`: binpm Rspress documentation app, route, validation, canonical production URL, and Cloudflare Pages deployment contract.
- `docs/project-cargo-mono.md`: Cargo subcommand project index.
- `docs/project-clibox.md`: clibox Rust CLI, npm distribution, and public documentation project index.
- `docs/apps-clibox-docs-foundation.md`: clibox public guides and route/validation contract.
- `docs/project-pnport.md`: pnport project index and unreleased 0.1.0 boundary.
- `docs/apps-pnport-docs-foundation.md`: pnport public guide routes and availability contract.
- `docs/project-nodeup.md`: Node.js version manager project index.
- `docs/project-with-watch.md`: Command rerun watcher CLI project index.
- `docs/project-derun.md`: Derun CLI project index.
- `docs/project-public-docs.md`: Public docs app project index.
- `docs/packages-docs-site-switcher-contract.md`: Shared accessible documentation site selector package contract.
- `docs/project-serde-feather.md`: Serde Feather multi-crate project index.
- `docs/project-rustia.md`: Rustia multi-crate project index.
- `docs/project-devhud.md`: DevHud cross-platform desktop/mobile utility project index and current issue #815 contract.
- `docs/crates-binpm-foundation.md`: binpm Rust CLI, release asset source selection, global cache, and local tooling contract.
- `docs/crates-with-watch-foundation.md`: with-watch CLI and watcher foundation contract.
- `docs/crates-rustia-core-foundation.md`: Rustia core runtime LLM data contract.
- `docs/crates-rustia-llm-foundation.md`: Rustia aisdk tool adapter contract.
- `docs/crates-rustia-macros-foundation.md`: Rustia macros derive contract.
- `docs/apps-nodeup-docs-foundation.md`: Nodeup Rspress documentation app, route, validation, and Cloudflare Pages deployment contract.
- `docs/apps-runmoor-docs-foundation.md`: Runmoor Rspress documentation app, route migration, fixed-port validation, and Cloudflare Pages deployment contract.

### Project Identifier Contract

Treat project IDs as stable enum-style values:

```ts
enum ProjectId {
  Binpm = "binpm",
  CargoMono = "cargo-mono",
  Clibox = "clibox",
  Pnport = "pnport",
  Nodeup = "nodeup",
  WithWatch = "with-watch",
  Derun = "derun",
  Runmoor = "runmoor",
  SerdeFeather = "serde-feather",
  Rustia = "rustia",
  PublicDocs = "public-docs",
  DevHud = "devhud",
  AsyncCommitHook = "async-commit-hook",
  Forge = "forge",
  ReactForge = "react-forge",
}
```

### React Forge Contract

- Figma creation/editing follows `docs/packages-react-forge-figma-contract.md`. Figma alone permits explicit official-MCP networking and remote publication. Reuse matching MCP Keychain credentials without refreshing/writing them. Preserve unselected external content; report partial/unknown writes and never blindly retry creation.

- `react-forge` is the private Node.js 24 / React 19.2.8 cross-platform document project in issue #968. Follow `docs/project-react-forge.md` and its complete requirements. Keep JavaScript reconciliation outside native workers, format models independent, sessions in memory, exports revision-pinned and atomic, and imported opaque content preserved. All required formats and evidence are required before completion.

- React Forge owns `packages/react-forge`, `crates/react-forge-node`, `crates/forge-package`, `crates/forge-document`, `crates/forge-docx`, `crates/forge-xlsx`, and `crates/forge-pdf`; reuse existing Forge presentation engines without changing CLI/MCP defaults.

### Project Domain Ownership

- `forge` -> `crates/forge-tree-doc`, `crates/forge-pptx`, `crates/delino-forge`; follow `docs/project-forge.md` and `docs/crates-forge-foundation.md`. Keep all three packages private, local-only, and preserve unsupported PPTX content during supported edits. Opened documents export to a separate path; reject replacement of their tracked source even with explicit overwrite. CLI/MCP share one core; optional preview is not a generation dependency.

- `nodeup` -> `crates/nodeup`, `apps/public-docs/docs/nodeup`
- `binpm` -> `crates/binpm`, `apps/public-docs/docs/binpm`
- `with-watch` -> `crates/with-watch`
- `cargo-mono` -> `crates/cargo-mono`
- `clibox` -> `crates/clibox`, `crates/clibox-config`, `crates/clibox-system`, `crates/clibox-transform`, `crates/clibox-wait`, `packages/clibox`, `apps/public-docs/docs/clibox`
- `pnport` -> `crates/pnport`, `crates/pnport-core`, `crates/pnport-preload`, macOS `crates/fspy_preload_unix`, `packages/pnport`, `apps/public-docs/docs/pnport`
- `runmoor` -> `cmds/runmoor`, `apps/public-docs/docs/runmoor`
- `derun` -> `cmds/derun`
- `serde-feather` -> `crates/serde-feather`, `crates/serde-feather-macros`
- `rustia` -> `crates/rustia`, `crates/rustia-llm`, `crates/rustia-macros`
- `public-docs` -> `apps/public-docs`, `packages/docs-site-switcher`
- `devhud` -> `apps/devhud` (shared shell, identity/settings/diagnostics, direct-client GitHub.com provider/setup and RealQA issue submission, desktop RealQA capture/editor/encrypted drafts/direct official and BYO R2 uploads, populated Deck surface, desktop/mobile hosts, production WidgetKit/AppWidgetProvider Deck widgets, and desktop Native Messaging listener implemented; other populated product surfaces planned), `apps/devhud-chrome-extension` (implemented), `apps/devhud-admin` (implemented), `servers/devhud-api` (Bootstrap/Settings/Upload/Account/Admin/Diagnostics and embedded administrator assets implemented), `protos/devhud/v1` (implemented), `packages/devhud-api-client` (implemented), `crates/devhud-native-messaging-host` (implemented)

### DevHud Contract

- Issue [#815](https://github.com/delinoio/oss/issues/815) is the current normative DevHud contract and supersedes closed historical issues #729, #755, and #757 without inheriting their architecture.
- DevHud fixed development ports are frontend `46305`, admin `46306`, and API `46307`; conflicts fail instead of remapping.
- DevHud local development is selected only as `team` or `oss`. `pnpm dev` is the fail-closed authorized-team path; it pins the preflight issuer comparison through migration and service launch, and concurrent team starts in one checkout fail while that pin is active. `pnpm dev:oss` is the contributor path and cannot depend on hosted Infisical. Both validate before migration and launch the same frontend/admin/API Turbo boundary. OSS Compose projects and preserved dependency volumes are scoped to the checkout's generated identity material, concurrent OSS starts in one checkout fail before dependency ownership is shared, interrupted Logto database initialization is repaired on retry, and `pnpm dev:oss:down` must not stop another checkout's dependencies.
- DevHud production API serving requires TLS termination at a trusted reverse proxy, with at least one configured `DEVHUD_TRUSTED_PROXY_CIDRS` entry and exactly one forwarded `https` protocol value from the trusted peer; the plaintext origin supports HTTP/1 and h2c for native gRPC forwarding.
- DevHud browser/API CORS is an exact allowlist: `http://localhost:46305`, `http://127.0.0.1:46305`, `http://localhost:46306`, `http://127.0.0.1:46306`, and the pinned Tauri shell origin `http://tauri.localhost`. Connect preflights allow only the documented Connect methods and headers, expose `x-devhud-correlation-id`, and forbid wildcard origins and headers.
- DevHud Deck refresh intervals apply only to active clients; suspended widgets use OS-controlled best-effort scheduling and must expose stale state with the last successful refresh.
- DevHud internal provider registry v1 contains GitHub.com only. PAT values remain in platform secure storage and never enter synchronized Settings or `devhud-api`; only stable non-secret UUID-v7 profile descriptors/references and pending PAT-removal tombstones synchronize. Deck profile references require a repository. Profile removal commits its descriptor deletion and tombstone before deleting the device-local PAT, retains the tombstone for retry on failure, and clears it only after secure deletion succeeds. Every device reconciles its internally indexed GitHub PATs against active synchronized profile IDs within a stable API-origin scope, so a device that missed a cleared tombstone removes the orphaned association without exposing credential enumeration, while the PAT remains until its last origin scope releases it. GitHub requests go directly to `https://api.github.com`, require an explicitly selected profile with no fallback, validate every referenced repository, accept a root-contents `404` only for a never-pushed empty repository, and expose only sanitized typed diagnostics.
- DevHud account deletion must purge or irreversibly pseudonymize official-upload metadata and invalidate public CDN copies; recovery is provided by an ownership-checked `AccountService.RestoreAccount` during the 30-day window. Restore and final purge use a mutually exclusive atomic account-state transition, and a successful restore must never follow irreversible purge work.
- DevHud desktop updates use the fixed signed manifest endpoint served by the API deployment and explicit platform/architecture mapping documented in `docs/project-devhud.md`; bootstrap is not an updater-discovery override.
- DevHud updater networking remains a closed native capability with no token and no frontend CSP/navigation access. Preserve the pinned-root/signed-successor/explicit-rollback trust chain, canonical artifact digest validation before approval, executor-separated blocking discovery and async download clients, installed-package selection with AppImage mount/executable correlation, immediate generation- and cancellation-guarded background checks, 30-second then 24-visible-unminimized-focused-hour schedule, focus-trapped download/install/restart approvals that defer Deck-link navigation, typed redacted diagnostics, explicit single-instance and Unix Native Messaging listener restart handoff that reaps a timed-out replacement before restoring ownership and exits the old process if restoration fails, atomic same-volume macOS bundle exchange/rollback, stale AppImage backup recovery, and truthful restart-only recovery after committed platform-package installs, a Debian or NSIS installer result that may have committed files, or a failed AppImage rollback without erasing an uncertain-install diagnostic on retry failure. Desktop release publication must run the exact active-declaration updater trust-root readiness gate defined in `docs/apps-devhud-updater-contract.md`.
- Desktop targets are macOS 13+, Windows 10 22H2+, and Ubuntu 22.04 LTS on X11, with x64 and arm64 artifacts. Mobile targets are iOS 16+ and Android 10/API 29+. Native Wayland, product analytics, remote feature flags, plugin SDK/ABI, server-side GitHub brokerage, and partial GA are excluded.
- Each fresh supported desktop process must create the CEF main window natively maximized, never fullscreen, retaining native decorations, work-area bounds, a 960×640 restored inner size, and a 640×480 minimum inner size. Tray, forwarded deep-link, and shortcut restoration must only unminimize, show, and focus the existing window, preserving any later user-selected size or maximization state for that process.
- DevHud desktop shortcuts are a closed six-action, structured-enum contract. Persist only `enabled`, modifier enums, and key enums: `right-primary` means physical right Command on macOS and physical right Control on Windows/X11. DevHud owns a passive, listen-only macOS CoreGraphics event tap that handles only physical virtual-key codes and pressed state, returns every event unchanged, and never invokes layout or text translation; `rdev` remains Windows/Linux-only. Unknown input is discarded before the bridge, and raw input, scan codes, key names, and display strings must never be persisted, emitted, or logged. Listener creation, permission, disablement, and run-loop failures use only the stable shortcut failure surface, preserve staged and last-valid bindings, retry with capped backoff, clear adapter and matcher state before replacement input, and reconcile every supported side-specific macOS modifier before processing recognized ordinary replacement input. Validate malformed, duplicate, reserved, registration, and permission failures before persistence; macOS guidance must cover Accessibility/Input Monitoring and Linux guidance must cover X11/XWayland, `DISPLAY`, and X authority.
- DevHud RealQA capture is desktop-only and uses injected macOS, Windows, and Ubuntu X11 still-image adapters without screen recording. Mobile frontend builds, including the direct x64 iOS simulator path, exclude the desktop-only RealQA annotation font. Every unsubmitted capture is an authenticated-encrypted UUID-v7 draft, renewed for 30 days from its last successful save under a default 10 GiB quota; unexpired drafts are never evicted, quota exhaustion is recoverable, and deletion occurs only after confirmed issue creation, explicit deletion, logout, or expiry. The capture controller remains mounted across desktop surface navigation so completion and the five-second floating confirmation survive, and region coordinates expose localized inline validation before native submission. Floating confirmation previews the first newly captured image, editor text is capped at 2,048 Unicode scalar values before native submission, and the editor never offers removal of the final active image. Flattened results are metadata-minimized sRGB PNGs with at most 10 images and 50 MiB per image; capture failures log only the structured action, platform, and stable error code, while image bytes, draft keys, editor text, and secrets never enter logs or synchronized settings.
- Direct RealQA shortcuts capture before restoring a hidden or minimized window so their visible confirmation cannot change the target. Cancellation is checked at the atomic draft create/append commit boundary, successful deletion remains authoritative when reconciliation fails, readable-draft deletion is serialized behind pending revision operations, coordinate-entered annotations expose localized in-image validation, and capture shortcuts close the command palette before opening another modal.
- Direct RealQA issue submission must retain every explicit repository/profile association, redact and validate the complete 1–256-character title before side effects, reconcile the UUID-v7 marker before image work, and percent-encode Markdown-significant image-URL parentheses. Fresh bootstrap public-asset URLs and official signed PUTs may use HTTP only on loopback in development; legacy offline bootstrap caches without the asset URL must still restore identity/settings while official uploads remain unavailable. The desktop native uploader independently retrieves and validates the official authority from unauthenticated Bootstrap at the compiled first-party `https://devhud.api.delino.io` origin, caches it for the native process session, and validates the signed URL before reading capture bytes; renderer-selected custom API origins affect authentication/settings and session CSP but never supply official-upload authority.
- DevHud production capture-picker geometry must remain compatible with the strict no-inline-style CSP. macOS and Windows opaque-black protected window frames return `protected-content` before persistence, while X11 and display captures retain legitimate opaque-black output. Bounded annotation gestures clamp to the final in-raster pixel. Draft recovery reclaims payloads no longer referenced by current, undo, or redo state before quota-checked editor mutations; flattened images commit as one revision-specific atomic bundle, and a revision change makes the prior bundle unreachable only after its manifest commit succeeds. Native line rasterization preserves requested even and odd stroke widths.
- RealQA annotation text metrics use static CSP-compatible styling, and blur previews crop the accumulated prior layer state before filtering so their boundary sampling matches native flattening. React Strict Mode setup must leave editor revision operations active.
- Desktop uses Tauri CEF from `https://github.com/tauri-apps/tauri` at commit `4af26a3f7f8b692d62cca549bbacd93f5ce90b41`; mobile uses system webviews. Packaged desktop hosts observe renderer termination during normal launches, while deliberate renderer-crash injection remains smoke-only. Bundle ID is `io.delino.devhud` and deep links use `devhud`, with native Logto callback `devhud://auth/callback`. Bootstrap also provides an `admin` client key and exact deployment-configured admin redirect; development uses `http://localhost:46306/auth/callback` and embedded production uses the API origin's `/admin/auth/callback`.
- DevHud Native Messaging uses host name `io.delino.devhud.native_messaging` and one fixed 32-character release-configured Chrome extension ID, shared by the extension, host manifest, and installer. The app-owned v1 IPC contract uses user-scoped platform endpoints and pairing-secret challenge/response authentication; it is not a Connect RPC or API path. Renderer configuration replacements are serialized in publication order so an older invocation cannot overwrite newer settings. Unix listener accept failures use capped backoff, and host connection establishment plus authentication share one absolute five-second deadline. Pairing retries omit a nonce already consumed by successful authentication, and unregister must invalidate live app session generations through its revocation-only IPC authentication and control path before reporting success, including while first pairing is pending. Linux pairing writes a non-secret per-user removal marker before the secure secret and deletes it only after credential cleanup succeeds. Debian removal must discover affected users from that marker independently of optional Chrome registration, invoke cleanup in every affected user's active session before deleting package-owned registration or binaries, and fail closed when user-scoped revocation cannot complete.
- DevHud iOS widgets use bundle ID `io.delino.devhud.widget`, App Group `group.io.delino.devhud`, and Keychain access group `$(AppIdentifierPrefix)io.delino.devhud.shared`; upload submissions own all groups and enforce 10 finalized images across groups, with 4096×4096/16,777,216-pixel pre-decode limits, 32-byte raw checksums encoded as Base64 only for the R2 checksum header, and immutable checksum/version-bound staging promotion.
- The implemented v1 wire model uses canonical lowercase RFC 9562 UUID-v7 wrappers, unsigned schema versions, RFC 8785 settings bytes capped at 1 MiB, matching Settings envelope/body schema versions, exact monotonic revisions, bounded opaque pagination, an explicit diagnostic browser platform with browser-only unknown architecture and empty native revisions, crash identifier strings capped at 256 UTF-8 bytes, typed bounded redacted crash details, a maximum of 100 retained crash reports per user with excess fresh submissions rejected as `ResourceExhausted`, and response/error correlation metadata mirrored to `x-devhud-correlation-id`; clients render the crash-report quota classification and server-provided limit separately from transient failures. Administrator mutation reasons are required, NUL-free, capped at 4 KiB of well-formed UTF-8 text, and reject credential, configured public asset locator, and local-path patterns before persistence. Generated Go and TypeScript sources are tool-owned and must reproduce without drift. Administrator message graphs must not expose settings bodies, secrets, DOM, screenshots, public or signed asset locators, Deck results, agent output, or local paths.
- DevHud settings schema v7 removes shortcuts, repository prompts, and R2 endpoint authority from synchronized snapshots while retaining bounded non-secret agent descriptors; `ReplaceSettings` accepts v7 only. Client v1-v6 reads and the server backfill migrate the complete validated legacy shape deterministically, validate and canonicalize the resulting v7 body, bind its exact SHA-256 digest, and increment a transformed stored revision once. Unsafe legacy R2 authority disables the R2 selection. The serialized aggregate of device-local shortcut bindings, agent identities, and prompts is capped at 1 MiB on read and before mutation, and interactive persistence failures are surfaced while guest memory fallback remains usable; signed-out sessions persist device-local edits through that guest snapshot and are shortcut-hydration-ready. Local agents are desktop-only, default-off, exact-version pinned, and never installed by DevHud. Their paths, observed versions/health, consent, prompts, and managed full-clone cache remain local. Every agent invocation is read-only, network-tool-disabled, and credential-free: a private clone PAT is dropped after workspace preparation, and only after strict Direct marker readiness does DevHud resolve a fresh PAT for one exact native argv-based `gh api` write. All modes use immutable bounded envelopes, isolated clones, strict output validation, atomically purge-gated cancellation/15-minute timeout, redacted diagnostics, and no automatic fallback.
- Invalid Logto credentials return `Unauthenticated`; transient Logto/JWKS verification failures return correlated `Unavailable` errors and are safely logged without credentials.
- The `apps/devhud/src-tauri` desktop/mobile host foundation and real `crates/devhud-native-messaging-host` skeleton are Cargo workspace members; the API registers Bootstrap, Settings, Upload, Account, Admin, and Diagnostics and embeds `apps/devhud-admin` at `/admin`, while the remaining planned paths remain documentation-only. Desktop Tauri, CLI, CEF runtime, sandbox dependencies, and six desktop platform archives remain immutable through `apps/devhud/cef-pins.json` and `pnpm --filter devhud verify:pins`; mobile target definitions are immutable through `apps/devhud/mobile-platforms.json` and `pnpm --filter devhud verify:mobile`. Never introduce CEF, desktop hooks, browser-extension integrations, or unapproved networking into a mobile dependency or artifact closure. Do not introduce `feat/cef`, a branch, fork, or arbitrary patch into the protected DevHud dependency graph, or a remote frontend dependency. The sole allowed patch redirects the official crates.io `tauri`, `tauri-plugin`, and `tauri-utils` packages to the same immutable authoritative Tauri revision so desktop-only official plugins share the CEF runtime's public types; the verifier rejects every other patch. The Native Messaging host remains desktop-only; its real crate skeleton is included in the workspace. `CreateUpload` must atomically reserve the signed-URL issuance quota before issuing a URL; `FinalizeUpload` must validate that reservation without charging it again and atomically recheck or reserve all other applicable upload quotas during finalization. Direct R2 staging uploads use the exact DevHud origins, `PUT`/`OPTIONS`, and checksum headers documented in `docs/servers-devhud-api-contract.md`. The implemented idempotent `devhud-api-sweeper` owns post-recovery account purge, upload-removal reconciliation, and retention pruning with multi-instance coordination; each iteration drains repeated transaction-bounded retention batches until request, audit, and crash-report tables all return a partial batch or its deadline ends. Staging expiry is added with UploadService. It ships as a separate signed/provenanced OCI image. Account restoration clears deletion state only and never clears an administrative block.
- Architecture-specific AppImage launcher bytes are immutable desktop packaging inputs in `apps/devhud/cef-pins.json`; AppImage packaging must verify the launcher's committed SHA-256 digest before the bundler can consume it.
- `apps/devhud-admin` is the sole Rsbuild producer of the ignored embedded administrator `dist`. Run `pnpm --filter devhud-admin build:embedded` before any API or sweeper Go compilation; the command builds the generated client, produces the `/admin/`-rooted hashed bundle without source maps, and validates its exact production structure. Docker must generate that bundle inside its build boundary and compile both binaries from the same generated tree.
- Android App Bundle validation must use the checksum-pinned artifact inspector declared in `apps/devhud/mobile-platforms.json` and verify the final merged base manifest's exact Deck widget receiver before widget evidence is recorded.
- DevHud private packaging has no automatic trigger: it is manually dispatchable and reusable only by an explicit caller, and is signed-only except for the secret-free `plan-only` dry run. Its stable release identity is exactly `devhud@v<MAJOR.MINOR.PATCH>`, with `packaging/devhud/release-metadata.json` synchronized to every source version. Preserve updater Ed25519 signatures, platform/store signatures, and Sigstore bundles as separate trust domains; unsigned or incomplete output is never public-ready. The workflow may retain a short-lived private artifact only and must never push a tag/image, create a release, submit a store build, or deploy.
- The coordinated DevHud release may retain only DevHud-owned public documentation in its release-bound candidate. Its candidate excludes root `search_index.*` data; Cloudflare Pages publication must rebuild the complete public-docs aggregate from current `main` and overlay only `/devhud` and its required shared runtime assets, so delayed or historical recovery cannot overwrite current root search data or newer package-local documentation subpaths.

### Repository Default Technology Choices

- Follow `docs/repository-defaults.md` when a more specific project or domain contract does not choose a different approach.
- New persisted entities should use UUID v7 identifiers by default unless a documented compatibility, storage, protocol, or product issue requires another ID shape.
- AI-based search should default to Cloudflare AI Search unless a project contract documents a different backend and migration boundary.
- When a new project does not specify its primary language, default to Golang.
- Prefer Rspack-family build tools when possible, including Rsbuild and Rspress for app and documentation surfaces.
- Static sites under `apps/` should use Rsbuild/Rspress-style toolchains and deploy to Cloudflare Pages by default. Existing documented exceptions remain valid until their project contract changes.
- File handling should default to Cloudflare R2 object storage plus signed URLs for upload and download access unless a project contract documents another storage or access pattern.

### binpm Cache Contract

- `~/.binpm/cache` is the user-level global asset cache shared by all `binpm` installs for the same account.
- `binpm` CLI source input may normalize GitHub.com shorthands such as `owner/repo` and supported `https://github.com/owner/repo` release URLs, but persisted manifests, lockfiles, package records, cache metadata, logs, and JSON diagnostics must use canonical `github:` or `gitlab:` source strings.
- `binpm` cache reuse must be validated with the strongest available integrity source: provider asset digest, upstream checksum material, successfully verified signature, or locally recorded SHA-256 metadata.
- `binpm` package signature verification is distinct from direct-installer verification for binpm's own release artifacts. Package signatures may satisfy strict verification only when a supported verifier validates the selected asset under the documented package trust policy; raw signature, SBOM, provenance, attestation, certificate, or Sigstore sidecar presence alone is not verification evidence and must be reported separately from trusted evidence when detected.
- `binpm --json` must preserve stable read-only diagnostic contracts and must also support stable final-result envelopes for mutating `install`, `add`, `update`, and `remove` commands. Successful JSON mode must emit exactly one compact object on stdout without ANSI color; progress, human diagnostics, and tracing must stay separate from stdout; errors must keep the parseable stderr envelope with `error.message` and `error.exit_code`.
- Cache management and diagnostic command identifiers are `list`, `prune`, `clean`, and `key` under `binpm cache`.
- `binpm cache prune` and `binpm cache clean` must not remove installed package records or executable links/copies under `~/.binpm/bin`.
- `binpm cache clean` must state the removed cache asset boundary and the preserved `~/.binpm/cache/refs`, package-record, and executable boundaries in human and JSON output.
- `binpm cache prune` must remove stale structured local-project cache references before asset pruning while preserving active and legacy references, and must guide legacy reference migration through future local install, update, or removal flows.
- `binpm cache key` must be read-only and must not download, install, or populate cache entries.
- `binpm cache key` must warn or expose structured status when `binpm.lock` is absent.

### binpm Source Contract

- Stable `binpm` source identifiers are `github:owner/repo[@version]`, `github:<host>/owner/repo[@version]`, and `gitlab:<host>/<namespace...>/<project>[@version]`. GitLab sources always require an explicit host, including `gitlab:gitlab.com/<namespace...>/<project>[@version]` for GitLab.com; `gitlab:group/project` is intentionally invalid.
- binpm provider tokens are host-scoped. GitHub.com may use `BINPM_GITHUB_TOKEN_GITHUB_COM`, `BINPM_GITHUB_TOKEN`, or `GITHUB_TOKEN`; GitHub Enterprise must use `BINPM_GITHUB_TOKEN_<NORMALIZED_HOST>`. GitLab.com may use `BINPM_GITLAB_TOKEN_GITLAB_COM`, `BINPM_GITLAB_TOKEN`, or `GITLAB_TOKEN`; self-managed GitLab must use `BINPM_GITLAB_TOKEN_<NORMALIZED_HOST>`. For explicit hosts, `<NORMALIZED_HOST>` must encode non-ASCII-alphanumeric UTF-8 bytes as `_HH_` uppercase hexadecimal so distinct hosts cannot share a token variable. Generic SaaS tokens must not be sent to enterprise or self-managed hosts.
- binpm release lookup diagnostics must distinguish missing authentication, insufficient permissions, and rate limiting while keeping tokens, authorization headers, private-token headers, query strings, fragments, and credential-bearing URLs out of logs, errors, persisted URLs, cache metadata, package records, and lockfiles. Missing-auth diagnostics for explicit GitHub Enterprise and self-managed GitLab hosts must print the exact expected host-scoped token variable name, and JSON diagnostics must expose safe env-var name fields without token values.
- `binpm` source versions are exact release tag requests only; omitted `@version` selects latest stable, while `@latest`, semver range-like selectors, channel selectors, and major-version pins must be rejected before manifest or lockfile persistence. Diagnostics may suggest an exact leading-`v` tag alternative when the release list shows one, but exact-match semantics must not change.
- GitLab versionless installs must exclude upcoming releases, releases with future `released_at` values, and known SemVer prerelease tag identifiers while preserving non-SemVer stable GitLab tags.
- GitLab release asset links must use HTTPS link URLs and HTTPS final redirect targets before candidate scoring or download.
- GitLab generated `assets.sources` source archives must not be selected as installable assets.
- Source-archive-only release diagnostics must remain distinct from no-asset and target-mismatch failures, list ignored source archive names when safe, and guide maintainers toward prebuilt portable archives or bare executables.
- Linux musl missing-libc diagnostics must name rejected assets and include safe remediation: upstream explicit `musl`/`static`/`portable`/`universal`/`any` naming first, then target overrides only after compatibility verification.
- Direct URLs, registries, and package-manager backends remain out of scope until documented in `docs/crates-binpm-foundation.md`; recognizable package-manager backend prefixes must fail with explicit unsupported-backend diagnostics.

### binpm Local Tooling Contract

- `binpm.toml` is the committed project-local tool declaration file.
- `binpm.lock` is the committed deterministic project-local resolution file and must keep target-specific records.
- `binpm init` manifest creation must target the current Git worktree root when available, otherwise the nearest ancestor containing `binpm.toml` when present, otherwise the current directory. It must print the resolved full manifest destination before creation or overwrite refusal and print a clear created-manifest line after successful creation. `--manifest-path <PATH>` is the documented explicit destination escape hatch for creating a new `binpm.toml`; it must still refuse existing files and must not overwrite manifests.
- `binpm.lock` must not include install timestamps, last-used timestamps, absolute cache paths, or other machine-local operational metadata.
- `binpm.lock` must store sanitized canonical asset URLs only, never query strings, fragments, credential-bearing URLs, or expiring signed download URLs.
- Project-local executable files must be installed under `$repoRoot/.binpm/bin`.
- `binpm install <source> --as <cmd> --bin <upstream-binary>` must preserve explicit global command aliases and selected upstream binaries in global package records without changing source identity. Human source-install output must show `install scope: global` before mutation, show the installed command alias and selected upstream binary as separate fields so repository names, local/global command aliases, and upstream binary names are not conflated, and when a project manifest is detected state that the manifest is not modified with guidance to use `binpm add <cmd> <source>` for project-local tools. Source-form install is global-only; `binpm install <source> --local` must be rejected with guidance to use `binpm add <cmd> <source>`.
- `binpm add <cmd> <source> --bin <upstream-binary>` must persist the upstream binary selection in `binpm.toml`; `binpm add --manifest-only` must only mutate `binpm.toml`; `binpm add ... --also <cmd=upstream-binary>` must expand to separate deterministic `[tools.<cmd>]` declarations; and `binpm x --package <source> --bin <upstream-binary> [cmd]` must use that upstream binary for one-off execution without inferring a source from command names. Manifest-only success output and later `list`, `doctor`, and frozen `x` diagnostics must make declared-but-not-installed state visible and point to `binpm install`.
- Archive binary ambiguity errors must list plausible executable candidates, include concrete retry commands using `--bin`, and mention repeated `--also <cmd=upstream-binary>` values for local multi-binary archives while keeping one `[tools.<cmd>]` manifest table per command.
- Local `binpm remove` must clean project-local package records when they exist.
- Local target-specific asset overrides must use `[tools.<cmd>.targets.<target-key>]` in `binpm.toml`.
- Local `binpm install`, `binpm update`, and `binpm x` must honor `--frozen-lockfile`; `CI=true` enables frozen behavior by default, and `--no-frozen-lockfile` is the explicit escape hatch. Frozen commands must fail when they would need to create or modify `binpm.lock`, except empty-manifest local updates that require no lockfile changes must succeed without creating `binpm.lock` and must report the no-op without file-change plans. Frozen mode is a lockfile write guard, not an offline or cache-only mode. Documented execution aliases `binpm exec` and `binpm run` must share `binpm x` lockfile and command execution behavior while `binpm x` remains canonical.
- Frozen local install and `x` may restore missing `.binpm/bin` executables and `.binpm/packages` package records from existing target lock records when cache bytes match the locked SHA-256. If cache repair needs a download, it may use only the lockfile's persisted sanitized asset URL, must validate the recorded SHA-256 before installing or populating cache, and must not require provider release-list pagination. Same-origin locked GitHub or GitLab provider URLs may use runtime-only host-scoped provider authentication when configured; external locked asset URLs must not receive provider credentials.
- `binpm verify --require-verified` must fail when no provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature under a documented trust policy is available, and strict failure diagnostics must distinguish missing trusted evidence from unsupported sidecar presence.
- Local and global `binpm update` and scoped `binpm remove` must print selected local/global scope before mutation and support `--dry-run` previews that do not mutate manifests, lockfiles, package records, cache references, or executables. `binpm update` with no command names must visibly state that all tools in the selected scope are targeted. Local update must advance exact-version manifest records to latest stable by updating `binpm.toml`, `binpm.lock`, and installed project-local executables consistently. `binpm update --global [cmd...]` must use existing global package records, preserve command aliases and selected upstream binaries, resolve latest stable releases, and finalize through the same cache/install/verification path as global installs.
- `--no-confirm` is a stable scripting flag for bypassing confirmation prompts on future dangerous operations.
- `binpm env --shell` must keep supported shell values explicit: `bash`, `zsh`, `fish`, `powershell`, and `pwsh` are supported; `pwsh` targets PowerShell 7 setup profiles; and `cmd` is accepted only to return a clear deferred-shell diagnostic with actionable cmd.exe PATH guidance. `--shell` may be omitted for best-effort shell inference, and `--global`/`--local` may narrow output to one PATH command without mutating profiles.
- Global install, add, doctor, and plain env PATH setup messaging must remain guided and non-mutating. `binpm env setup --shell <shell> [--dry-run]` is the explicit opt-in profile modification command and may append only the global bin PATH line after previewing the exact file and line; it must tell PowerShell 7 users to pass `--shell pwsh`, refuse ambiguous shell/profile targets, and not imply project-local `.binpm/bin` entries are suitable for profile persistence.

### binpm Documentation Contract

- `apps/public-docs/docs/binpm` is the Rspress content root for `binpm` and is built by the existing `apps/public-docs` workspace.
- The canonical production URL for `binpm` documentation is `https://oss.delino.io/binpm`.
- The consolidated `public-docs` build must use Cloudflare Pages and no standalone binpm documentation deployment exists.
- binpm documentation content must be sourced from repository contracts and must not infer product behavior or page content from the live canonical site.
- The binpm section must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in top-level social links and in the document-page footer.
- binpm direct-installer documentation must include latest docs-site installer commands for `https://oss.delino.io/binpm/install.sh` and `https://oss.delino.io/binpm/install.ps1`, preserve current and pinned first-party raw GitHub installer commands, describe checksum verification through `SHA256SUMS`, and keep binpm release verification separate from package verification.
- binpm installation and release documentation must describe Homebrew as prebuilt-only, describe disabled `cargo-binstall` quick-install and compile fallbacks, and distinguish first-party binpm release platforms from broader third-party target parsing support.

### Nodeup Documentation Contract

- `apps/public-docs/docs/nodeup` is the Rspress content root for `nodeup` and is built by the existing `apps/public-docs` workspace.
- The canonical production URL for `nodeup` documentation is `https://oss.delino.io/nodeup`.
- The consolidated build must publish direct-installer entrypoints at `https://oss.delino.io/nodeup/install.sh` and `https://oss.delino.io/nodeup/install.ps1`.
- The Nodeup section must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in top-level social links and in the document-page footer.

### nodeup Shim and Self Cleanup Contract

- `nodeup shim setup` is the stable idempotent setup/repair command for managed `node`, `npm`, `npx`, `yarn`, and `pnpm` shims.
- `nodeup shim setup` PATH activation remains non-mutating by default; output must provide shell- and OS-aware activation and verification guidance when the shim directory is not active.
- `nodeup self uninstall` removes Nodeup-owned data, cache, and config roots only; binary, managed shims, and shell profile/PATH cleanup remain manual and must be separated from removed data in human and JSON output with shell- and OS-aware follow-up guidance.

### Serde Feather Component Contract

`serde-feather` is a two-component project with fixed mapping:

```ts
enum SerdeFeatherComponent {
  Core = "core",
  Macros = "macros",
}
```

- `Core` -> `crates/serde-feather`
- `Macros` -> `crates/serde-feather-macros`

### Rustia Component Contract

`rustia` is a three-component project with fixed mapping:

```ts
enum RustiaComponent {
  Core = "core",
  Llm = "llm",
  Macros = "macros",
}
```

- `Core` -> `crates/rustia`
- `Llm` -> `crates/rustia-llm`
- `Macros` -> `crates/rustia-macros`

### Documentation-First Policy

- New project creation requires `docs/project-<id>.md` and at least one `docs/<domain>-<project-or-component>-<contract>.md` before runtime implementation.
- Every structural change to project paths must update the corresponding project index and relevant domain contract docs in the same change.
- Repository and domain policy updates must be written in the appropriate `AGENTS.md` in the same change.
- Domain-level `AGENTS.md` files must remain aligned with `docs/` contracts.

### New Project Onboarding Checklist

- Reserve a unique `project-id`.
- Create project path skeleton and add `.gitkeep` if implementation is not started.
- Add `docs/project-<project-id>.md` using `docs/project-template.md`.
- Add at least one domain contract doc using `docs/domain-template.md`.
- Documentation-only phase may mark canonical paths as `planned` before creating path skeletons; create the skeleton and add explicit workspace membership in the same change where Rust runtime implementation begins.
- Update root and domain `AGENTS.md` files when project ownership or contracts change.
- Ensure path and naming contracts are consistent across docs and AGENTS rules.

### Naming Rules

- Use lowercase kebab-case for project IDs and directory names unless runtime conventions require otherwise.
- Use `project-` prefix for project index docs.
- Use domain prefixes (`apps-`, `cmds-`, `servers-`, `crates-`, `protos-`, `packages-`) for domain contract docs.
- Use `repository-<topic>-contract.md` for repository-level contract docs that span project or domain ownership.
- Use enum-like canonical identifiers in documents where values must remain stable.

### GitHub Issue Style Contract

- Apply this contract to all open/new GitHub issues.
- Use issue titles in the format `<domain>: <description>`.
- `<domain>` must use stable lowercase identifiers from project/domain contracts (for example: `nodeup`, `serde-feather`).
- `<description>` should be concise, specific, and start with a lowercase verb phrase when possible.
- Do not use bracket-style project prefixes like `[serde-feather]`.
- Use the following Markdown section order for issue bodies:
  - `## Summary`
  - `## Evidence`
  - `## Current Gap`
  - `## Proposed Scope`
  - `## Acceptance Criteria`
  - `## Test Scenarios`
  - `## Out of Scope`
- Optional `## Additional Notes` may be appended only when needed.

### GitHub Pull Request Title Contract

- Apply this contract to newly created pull requests.
- Pull request titles must use Conventional Commit-style format with a required scope: `<type>(<scope>): <description>`.
- `<type>` must be an appropriate Conventional Commit type such as `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`, `build`, `perf`, or `revert`.
- `<scope>` must use a stable lowercase project, component, domain, or tooling identifier from repository contracts when one applies (for example: `nodeup`, `serde-feather`, `docs`, `ci`).
- `<description>` should be concise, specific, and start with a lowercase verb phrase when possible.
- Do not create unscoped pull request titles or use bracket-style project prefixes like `[serde-feather]`.

### Node Runtime Baseline

- Root `.nvmrc` is the canonical Node.js runtime selector for local development workflows.
- The current required runtime is Node.js `24` (LTS major line).
- When bumping the runtime baseline, update `.nvmrc` and relevant CI/runtime docs in the same change set.

### Frontend Design Rules

- Frontend work in `apps/` must follow Toss Design Guidelines for UX/UI decisions across web and mobile surfaces.
- If a form has a single critical input, that input must receive focus when the form is shown.
- Dialog UIs must support closing with the `Esc` key.

### Shell Command Safety Rules

- Use `$(...)` for command substitution; do not use legacy backticks in new scripts.
- Wrap all file paths in quotes by default in shell commands and scripts to prevent whitespace and glob-expansion bugs.
- Apply strict quoting and escaping for all dynamic shell values to prevent command injection and parsing bugs.
- Run GitHub CLI (`gh`) commands outside sandbox restrictions by default; use the required approval flow when escalation is needed.
- If an operation is blocked by sandbox restrictions, retry it without sandbox restrictions using the required approval flow.

### Logging Rules

- Write sufficient logs to support debugging, incident analysis, and operational troubleshooting.
- Prefer structured logging over ad-hoc plain text logs for business and system events.
- Go code should use `log/slog` (or a compatible structured logger built on it).
- Rust code should use `tracing` (or a compatible structured logging facade).
- CLI and operator-facing logs should enable ANSI color by default; allow opt-out with documented flags or environment variables.

### CI Baseline

Repository-wide quality CI is defined in `.github/workflows/CI.yml`. The three-OS Go test matrix uses an explicit 20-minute per-package watchdog for native Git, shell and durable SQLite integration; this is not a product command timeout.

Coverage expectations:
- `go-quality`: generates and validates the ignored administrator and ach UI bundles, then runs `go fmt ./...` (failing if formatting changes are applied) and `go vet ./...` on Ubuntu.
- `go-test`: generates and validates the ignored administrator and ach UI bundles, then runs `go test ./...` on `ubuntu-latest`, `macos-latest`, and `windows-latest`.
- `rust-fmt`: runs `cargo fmt --all --check`.
- `rust-clippy`: runs `cargo clippy --workspace --all-targets --all-features -- -D warnings`.
- `rust-test`: builds pnport and its injection companion with `cargo build --locked -p pnport -p pnport-preload`, then runs `cargo test --workspace --all-targets`.
- `node-public-docs-test`: runs `pnpm install --frozen-lockfile --ignore-scripts` and `pnpm --filter public-docs test`, covering the root and all six project content sections.
- `node-clibox-test`: runs `cargo test --locked -p clibox -p clibox-config -p clibox-system -p clibox-transform -p clibox-wait` for native utility/configuration/process/adapter behavior, native CLI consumer installation and launcher/distribution tests on Linux, macOS, and Windows, selected by shared CI planning and required by `CI Result`.
- `node-pnport-test`: checks launcher and package contracts, version synchronization, immutable artifacts, installer rollback, and fail-closed release publication on affected PRs and main pushes.
- `pnport-native`: on affected main pushes and manual CI dispatch, runs the six native targets, installed npm/Yarn PnP consumers, TypeScript conformance, archive packaging, and direct-installer smoke. PRs skip this native matrix; the pnport tag workflow independently requires the same six targets before publication.
- `node-public-docs-test`: runs `pnpm install --frozen-lockfile --ignore-scripts` and `pnpm --filter public-docs test`.
- `forge-test` and `forge-render`: validate the three private Forge crates on Linux/macOS/Windows, official stdio MCP interoperability, and mandatory Linux LibreOffice/Poppler rendering. Both follow central change planning and remain required in `CI Result`; optional local renderers do not make the selected render job optional.
- `ci-contracts`: validates workflow syntax and the repository CI contract with the checked-in Go `actionlint` tool and Node fixtures.
- `async-commit-hook`: follows the central change plan, runs Go race tests, local UI/docs/client tests, protocol freshness and release fixtures, and builds all six unsigned target archives. Shared setup actions restore caches; only successful main validation saves them.
- `devhud-frontend`, `devhud-extension`, and `devhud-admin`: run package-local type, lint, unit, component, accessibility, and deterministic frontend/package builds.
- `devhud-protocol`: runs schema formatting, lint, compatibility, and generated-freshness checks; Go binding tests; and TypeScript client lint, tests, and build on Ubuntu.
- `devhud-api`: runs package-local Go format, vet, unit, PostgreSQL migration, integration, API, and sweeper conformance.
- `devhud-rust-conformance`: runs package-local capture, shortcut, IPC, updater, and native-host protocol tests in addition to the repository Rust baseline.
- `devhud-security`: runs credential, redaction, logout, deletion, restore, direct GitHub/R2, and agent adapter fixtures.
- `devhud-desktop`: validates the exact CEF pin and feasible macOS, Windows, and Ubuntu x64/arm64 native packages, installer/native-host lifecycle, and Linux X11 smoke.
- `devhud-mobile-contracts`, `devhud-ios-simulator`, and `devhud-android-emulator`: validate iOS/Android app and widget generation and production/simulator/emulator builds.
- `devhud-oci`: builds both API and sweeper OCI layouts for amd64/arm64 and validates non-root execution, embedded migrations, and SPDX SBOMs without pushing.
- `devhud-supply-chain`: validates installer, Native Messaging host, extension ZIP, updater/key-rotation signature, SBOM, and provenance fixtures.
- `devhud-release-contracts`: runs deterministic static/dry Node tests for the reusable private candidate, exact public release identity, configuration failure, signing/preflight failure, review retry, channel ordering, rollback, and redaction contracts without exercising publication.
- `ci-result`: retains the `CI Result` status and checks every dependency against the exact `changes` plan; failed/cancelled jobs, missing dependencies, and unexpected skips or execution fail the aggregate.
- The DevHud release-contract job also validates the internal operations runbook, repository workflow contract, and read-only CEF review workflow through `scripts/release/devhud-operations.test.mjs`.

Change-scoped execution rules:
- A single `changes` job selects domain jobs before runner allocation using `scripts/ci/job-paths.json` and `scripts/ci/plan.mjs`. `ci-contracts` always runs. Go and environment checks retain all three operating systems when selected.
- PRs run affected validation, including OCI checks, but never allocate the two Linux CLI package, six pnport native, ten desktop, three iOS, or four Android package entries. Relevant main pushes run the complete existing native matrices. Manual dispatch runs every check and platform. There is no nightly CI schedule.
- PR comparisons use the base/head merge-base; main comparisons use the exact `before..sha` trees, including all commits in the push. Missing or invalid comparisons fail. Deleted and renamed files select both affected owners.
- Node workspace jobs use `scripts/ci/run-affected.mjs` to invoke the installed Turbo Node entry point directly with `turbo run <task> --affected --filter <workspace>` arguments, without a shell or package-manager shim, and with the planner's exact `TURBO_SCM_BASE` and `TURBO_SCM_HEAD`. External inputs and forced runs omit `--affected`; an otherwise empty affected set is a successful no-op.
- Because `public-docs` builds six project content roots directly, changes under `apps/public-docs/docs/{async-commit-hook,binpm,nodeup,runmoor,clibox,pnport}` select and force `node-public-docs-test`.
- Central path rules cover Go, Rust, every Node workspace, repository environment tooling, DevHud domains, packaging, public docs, and package/release/review workflows. Runmoor-only release scripts do not select DevHud native packaging; shared DevHud packaging inputs still do.
- The PR frontend job runs the complete DevHud test command, including native-script fixtures, clean desktop/mobile frontend output validation, static mobile/widget contracts, and immutable CEF pins. Its aggregate `test` task is non-cacheable because it validates consecutive clean builds and external contract inputs.
- Protocol generation and package-local frontend outputs are deterministic and cacheable; the ignored administrator and ach UI embeds, native package, mobile, smoke, signing, release, and deployment tasks remain non-cacheable.
- Changes to `.github/workflows/CI.yml`, `.github/actions/**`, or `scripts/ci/**` force every check eligible for that event; PRs still exclude native packaging. `workflow_dispatch` runs all domain jobs regardless of changed paths.
- CI installs always use the frozen pnpm lockfile with `--ignore-scripts`. Shared pnpm/Go setup actions restore caches scoped by OS, architecture, tool version, and lockfile; only successful main jobs save them. Rust compilation caches likewise save only on successful main jobs; the DevHud desktop matrix must cache dependencies only with target caching disabled, and rustfmt has no dependency cache. PRs may restore main caches but never create branch-scoped caches. The Runmoor workflow uses the same Go cache policy and cancels superseded executions on the same ref.
- CI is read-only: it does not consume release secrets, push tags or images, create releases, upload stores, deploy services/docs, or mutate updater/controller state.
- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

Release automation baseline:
- CLI release orchestration is owned by `docs/repository-workflow-contract.md` and the manual `Release Project` workflow: only binpm, cargo-mono, nodeup, with-watch, derun, runmoor, clibox, pnport, and async-commit-hook are selectable. Do not restore main-push workspace publishing. Version commits and individual release-tag pushes use the repository-scoped `delino-release-bot` GitHub App; Homebrew uses a separate tap-scoped token. Preserve exact release-source validation, version-only run-ID recovery, non-forced pushes, and existing signed artifact workflows. Keep bot keys/tokens out of files, artifacts, logs, Git URLs, and configuration.
- Trigger contract: `release-project.yml` accepts only manual `main` runs in `delinoio/oss`, with closed `project` and `bump` choices. All nine projects proceed from version preparation through registry validation/publication and the exact tag push without inspecting or waiting for main CI. Main CI runs independently; pending, failed, or canceled CI does not block release orchestration. For Rust targets that publish to crates.io, registry publication must still succeed before the tag push.
- The coordinator ends after the verified release-tag push. The eight tag-triggered project release workflows run asynchronously; downstream release failures are repaired from that workflow's Actions page and do not require retrying the coordinator when the tag push succeeded. async-commit-hook is preparation-only: its tag never starts publication, and its summary directs maintainers to the separate manual release workflow.
- Publish command contract: `cargo run --locked -p cargo-mono -- publish --package "$RELEASE_PROJECT"` for Rust CLI targets other than clibox and pnport; Go, clibox, and pnport targets validate the release source without a registry upload.
- Authentication contract: checkout disables persisted credentials, read-only release-source inspection uses the built-in token, and fresh `delino-release-bot` installation tokens perform source/tag and Homebrew writes with separate repository scopes. Configuration is `DELINO_RELEASE_BOT_CLIENT_ID` (Actions variable), `DELINO_RELEASE_BOT_PRIVATE_KEY` (Actions secret), and `CARGO_REGISTRY_TOKEN` (Rust upload secret). No PAT is required by these workflows.
- `release-cargo-mono` is defined in `.github/workflows/release-cargo-mono.yml`.
- Trigger contract: runs on tag push `cargo-mono@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS cargo-mono release artifacts to GitHub Releases for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.
- `release-binpm` is defined in `.github/workflows/release-binpm.yml`.
- Trigger contract: runs on tag push `binpm@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS binpm release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`binpm-<os>-<arch>[.exe]`) and archive assets (`binpm-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`binpm`) from prebuilt archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `release-nodeup` is defined in `.github/workflows/release-nodeup.yml`.
- Trigger contract: runs on tag push `nodeup@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS nodeup release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`nodeup-<os>-<arch>[.exe]`) and archive assets (`nodeup-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`nodeup`) from prebuilt archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `release-derun` is defined in `.github/workflows/release-derun.yml`.
- Trigger contract: runs on tag push `derun@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS derun release artifacts and updates Homebrew (`derun`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`).
- `release-with-watch` is defined in `.github/workflows/release-with-watch.yml`.
- Trigger contract: runs on tag push `with-watch@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS with-watch release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`with-watch-<os>-<arch>[.exe]`) and archive assets (`with-watch-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`with-watch`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`, `linux-arm64`).

- `release-devhud` is defined in `.github/workflows/release-devhud.yml` and is manual-only with exact `main` version input, an optional exact lowercase ancestor revision for interrupted-release recovery, plus `dry-run` or protected `release` mode. Historical recovery must reuse the retained revision-bound candidate and its original non-secret release-configuration fingerprint, must not rebuild signing output, and must bind every checkout and public boundary to the selected revision and destinations rather than the newer dispatch SHA or environment values. Controller authorization separately binds that newer dispatch SHA to the GitHub OIDC `sha` claim and permits a differing selected revision only after independent ancestor validation.
- `devhud-cef-security-review` is defined in `.github/workflows/devhud-cef-security-review.yml`, runs monthly or by explicit dispatch, and has read-only contents permission. It compares the committed Tauri revision with an immutable upstream `feat/cef` revision, emits only bounded redacted metadata, and may upload its report artifact; it must not mutate source, pins, lockfiles, releases, stores, registries, deployments, alerts, updater state, or GA state. High-risk CEF response is maintainer-owned and must update `apps/devhud/cef-pins.json`, every authoritative Cargo and verifier pin consumer, every matching `Cargo.lock` source entry, compatibility evidence, signed candidate evidence, and release contracts before any updater publication.
- DevHud public release contract: serialize every version through one project-wide release group; retain, discover by exact revision across every attempt of every recovery run, reuse, and revalidate the original complete private signed candidate through the protected review window; fail closed across every documented signing, store, GitHub, Logto, PostgreSQL, R2, asset, registry, docs, and exact-identity provider-neutral deployment boundary; wait on protected review gates; reconcile absent App Store versions, already uploaded exact builds, pending submissions, same-commit retries, interrupted partial store publication, and draft GitHub Release assets without repeating completed mutations; require an existing draft's exact `targetCommitish` and an existing published release's remotely resolved tag to match the selected revision before reconciliation or reuse; validate and reuse an exact already-published immutable GitHub Release without deleting, replacing, or re-uploading assets; block the first publication on the exact docs candidate; bind the deployed `/devhud` page to that candidate; remotely reverify all exact stores, the exact GitHub asset set, the complete extracted evidence archive and expected keyless bundle inventory, every published payload and updater manifest against signed checksums and signatures, and source-bound immutable OCI digests and keyless signatures both before and immediately after final GA approval; require every downstream environment-bound publication, deployment, cleanup, store-publication, and GA job's complete release-variable fingerprint, including the Chrome extension and OCI production push-principal identities, to match the fully validated preflight environment before its first external check or mutation; query every exact store state before cleanup, withdraw every held store submission after any pre-publication failure or cancellation including a failed or timed-out final store-publication gate, and reconcile attempted infrastructure promotion from live controller status before rollback only while no store is public; and serialize infrastructure, all stores, regular GitHub Release, updater, public docs, independent verification, and GA without beta or partial GA.
- DevHud public preflight keeps private updater, desktop, iOS, and Android signing material confined to `devhud-private-build`; the publication environment receives only live release credentials and required dual-use Apple/Chrome identity material, checks Apple submission authority through a read-only API surface, binds the protected public-asset base URL to the controller's exact runtime authority through SHA-256 before promotion, requires the protected Google Play production-release service-account principal and the operator-confirmed OCI production push principal to match their credentials before any network access, classifies terminal App Store `INVALID_BINARY` state as rejected, replaces terminal canceled Apple review submissions with a live draft, and uses immediate/default Chrome publication only after the protected held-review gate. Controller updater input archives normalize ordering, ownership, modes, timestamps, and gzip headers so retries reproduce identical bytes.
- DevHud permission/deployment contract: start with no GitHub permissions, grant job-local read/OIDC/write scopes only where required, and keep the official API host operator-selected behind `docs/servers-devhud-release-controller-contract.md`.

### Documentation Lifecycle Rules

- Every structural repository change must update relevant project index docs and domain contract docs in the same change set.
- New project creation is blocked until its project index doc and at least one domain contract doc exist.
- Documentation-only project onboarding may use `planned` paths, but runtime implementation must not begin before canonical paths are created and documented.
- Repository-wide and domain rules must be maintained in the appropriate `AGENTS.md`.
- Documentation policy updates and documentation changes that introduce or modify repository/domain policy guidance must update the relevant `AGENTS.md` files in the same change, and documentation edits must not silently omit or reinterpret ambiguous requested or source-backed content without user confirmation.
- When user-facing documentation content changes, update relevant pages in `apps/public-docs` in the same change set as needed.
- Run `git commit` only after `git add`; once files are staged, create the commit without unnecessary delay.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, resolve the corresponding review threads.
- If a project splits into multiple deployables, the project index must include path ownership and integration boundaries, and component-level domain docs must exist.

### async-commit-hook Contract

- Project ID `async-commit-hook` owns `cmds/async-commit-hook`, `apps/async-commit-hook`, `apps/public-docs/docs/async-commit-hook`, `protos/async_commit_hook/v1` and `packages/async-commit-hook-api-client`; only executable `ach` is distributed.
- Follow `docs/project-async-commit-hook.md` and its domain contracts. Issue #897 applies with the owner's recorded exclusions of actual six-target machine validation and actual public publication.
- `release-async-commit-hook.yml` is manual-only from `main`, defaults to unsigned nonpublishing dry-run artifacts, and follows `docs/cmds-async-commit-hook-release-contract.md`. Ordinary CI must never sign or publish ach artifacts, mutate the Homebrew tap or deploy Pages.
- `Release Project` prepares async-commit-hook versions and tags only. Keep the Go version, local UI/docs/client package versions and release metadata synchronized in one version-only commit; installer defaults must resolve the latest published stable release so an automatic Public Docs deployment cannot target an unpublished prepared version. Reject drift and missing or ambiguous declarations before writes. Keep Cargo publication credentials out of this path. Actual publication retains the separate manual main workflow and its exact signing identity and tag/dispatch-commit validation.
- CLI/MCP/Connect share one core, exact-commit latest-compatible-attempt validation and explicit per-run acknowledgements. Never resurrect old successful evidence after pruning.
- State, reports and logs remain local and account-owned; no telemetry. User commands require explicit repository trust. Cancellation must reconcile owned descendants before releasing exclusive scheduling groups.
- Development uses frontend 46308 and local UI/API 46309 with conflict failure; docs development/preview use 46310/46281. Root DevHud development remains unchanged.
- The daemon and on-demand viewer serve the same embedded UI without pairing. Every RPC requires exact same-origin POST and the API version header. `https://oss.delino.io/async-commit-hook` is documentation-only.
- Run `pnpm --filter async-commit-hook build:embedded` before ach Go compilation or repository-wide Go checks, including commit hooks. The app owns the generated command webassets/dist; never commit or substitute placeholder assets. Root Go checks also require the existing DevHud administrator embed.

### Linux CLI Package Distribution

- Follow `docs/repository-linux-packages-contract.md` for the seven CLI APT/DNF repositories at `https://pkgs.oss.delino.io`. Native package publication is part of each selected CLI release, uses the dedicated `linux-packages` environment, and enrolls binpm, cargo-mono, nodeup, with-watch, derun, runmoor and clibox in stable.
- Native package release callers must explicitly inherit secrets so the reusable publisher can resolve its protected `linux-packages` environment. Only the guarded publication job references production credentials; validation and installation jobs remain credential-free. Preserve the environment boundary for manual recovery of already-published release identities.
- Runmoor uses stable for both source releases and native packages. Preview remains reserved and separately registered; callers cannot override project channels.
- APT signing-certificate updates are distributed by the shared `delino-archive-keyring` dependency in both suites. Keep certificate versions immutable, retain historical public signing subkeys, and require a completed 30-day old-signer publication overlap before switching CI subkeys.
- Relevant main pushes, including Rust CLI source, Cargo workspace/configuration and toolchain changes, must select the Linux package CI job so both native architectures retain the AlmaLinux 9 compatibility baseline. Manual CI dispatch always selects it. PRs skip this job even when CI configuration changes force all eligible checks; its `CI Result` dependency remains and must match the planned skip. General Linux validation, static package contracts, and release-time packaging checks remain enabled.

### Runmoor Contract

- Public Runmoor documentation is Markdown-owned by `apps/public-docs/docs/runmoor` and published at `https://oss.delino.io/runmoor`; follow `docs/apps-runmoor-docs-foundation.md`. Keep all public discovery links pointed at the consolidated subpath.

- Follow `docs/project-runmoor.md`, `docs/cmds-runmoor-foundation.md`, and `cmds/runmoor/AGENTS.md` for issue #893. Runmoor owns local ephemeral GitHub Actions runners through Docker and Tart, with host-only credentials, durable ownership, fair resource budgets, and single-job disposable environments.
- Runmoor binaries use the stable release channel for darwin-arm64, linux-amd64, and linux-arm64 under `runmoor@v<MAJOR.MINOR.PATCH>`; publication dry runs are credential-free and non-publishing. No Homebrew distribution is added.

- Runmoor release fixtures must run with Node built-ins and no workspace dependency installation; YAML workflow assertions belong to `scripts/ci/` under `pnpm ci:contracts`.
- Runmoor release dry runs are secret-free and non-publishing. Only the guarded publication job can obtain OIDC/signing and release-write authority; stable releases use the exact `runmoor@v<MAJOR.MINOR.PATCH>` source identity and three documented platform archives while disclosing the live GitHub/Tart verification limits.

### clibox Contract

- Public clibox documentation is owned by `apps/public-docs/docs/clibox` at `https://oss.delino.io/clibox`, a major project alongside Runmoor in the shared selector. Follow `docs/apps-clibox-docs-foundation.md`; synchronize public behavior with native/npm guides and keep release internals in `docs/`.
- Repository tooling consumes the published prebuilt through the exact root `clibox-prebuilt` npm alias and lockfile, independently of the private source workspace. Invoke `pnpm exec clibox` directly from the repository root. Verify the installed version in the `setup-clibox` workflow action; do not add a repository launcher, compile Rust, or download at command runtime. Keep public installers and minimal toolchain bootstraps independent, and preserve existing data formats, secret handling, and stronger readiness/lifecycle checks.

- clibox CLI consistency uses canonical `run env`, `port list`, and `hash compute` without old-name aliases. Report `--quiet` suppresses stdout; PID selection is only `port list --pids`. File-output commands interpret `--output -` as stdout and `./-` as a literal dash file; `--force` requires real file output or `--in-place`. Keep short/long help, static redacted migration guidance, numeric owned-operation cancellation (130/143), filtered-error visibility, and native/npm behavior synchronized.

- The executable crate and installed command are `clibox`; the public npm entry point is `@delino/clibox`. Keep the Cargo manifest/lock, private npm source manifest, executable version, and all nine generated npm packages at the same exact version.
- Keep `clibox` as the root CLI composer with direct path dependencies on `clibox-config`, `clibox-system`, `clibox-transform`, and `clibox-wait`; companion crates must not depend on one another or the executable. Family command definitions, runtimes, errors, and unit tests belong to their owning crate. Only the executable version participates in product release synchronization; companion versions remain internal. All five crates must be selected by clibox CI/release tests and covered by npm task cache inputs and change detection.
- Missing subcommands for `clibox run`, `port`, `clipboard`, `wait`, `text`, `time`, `base64`, `hash`, `dotenv`, and `yaml` must show command-specific clap help on stderr with exit code 2. Preserve root no-argument and explicit help success on stdout, and redact all other parser failures.
- The npm launcher supports Node.js 22+, macOS/Windows x64 and arm64, and Linux x64/arm64 with separate glibc/musl packages. It resolves only the matching exact-version `@delino/clibox-*` optional dependency and has no shell, PATH fallback, install script, runtime download, or Rust compilation fallback.
- Generate public npm packages from the private source workspace under ignored `dist` or temporary directories. Ordinary workspace installation must not resolve unpublished clibox dependencies. Never track generated tarballs or binaries.
- clibox implements `run env`, `port list`, `port kill`, `open`, and text `clipboard copy`/`paste` alongside help/version. Preserve child argv/signal compatibility, revalidated port-owner termination with one shared five-second wait, explicit-app-only waiting, 16 MiB NUL-free UTF-8 clipboard validation and Linux background clipboard ownership. Use current-user/session authority without persistence, elevation, automatic retries or sensitive diagnostic values.
- The seven offline text/time/Base64/hash commands specified by issue #917 coexist with the issue #916 OS utilities. Preserve enum-backed modes, redacted stderr diagnostics, cancellable processing with numeric 130/143 for handled transformation cancellation, permission-preserving atomic file publication, and bundled timezone data. Keep OS-command signal propagation separate from transformation publication supervision. All five clibox crates must remain `publish = false`. Release Project validates the exact release source and pushes `clibox@v<version>` without waiting for main CI, requiring or injecting a Cargo registry token or publishing to crates.io. npm and GitHub publishers must not depend on a crates.io version. The same verified npm GNU binaries also produce two signed Linux GitHub Release archives and stable APT/DNF packages; Homebrew remains excluded. The npm publication flag controls npm only.
- The CLI provides help/version and `wait tcp`, `wait http`, and `wait file` under issue #919, alongside the #916 utilities, the seven offline #917 transformations, and the #920 configuration commands. Waits are stateless, use immediate nonoverlapping polling and monotonic deadlines, support handled cancellation, and expose only redacted human/quiet/JSON results. The GNU binaries also feed the stable native repository described above.
- Issue #953 adds `run with-rate-limit`, `run with-lock`, `run with-service`, `run with-retry`, and `run with-timeout`. Reuse the existing environment execution grammar without a shell; preserve companion-crate independence and keep supervision in the system command family. Local lock/bucket state is the sole clibox application state: it is private, versioned, hashed, same-user/same-machine only, atomically published under OS-owned locks, and fails closed on unsafe storage, corruption, or configuration mismatch. Preserve redacted lifecycle tracing, bounded owned-process cleanup, external-service non-ownership, literal stdin/argv behavior, native child statuses, and 124/75/0/2/1/130/143 wrapper outcomes across native and installed npm launches.
- Preserve issue #920's `dotenv list`, `dotenv merge`, and `yaml normalize` contracts: offline Rust processing, exact value/precision preservation, independent 64 MiB input/output limits, 128 YAML collection levels, atomic permission-preserving file publication, handled cancellation, and strictly redacted stderr diagnostics.
- HTTPS uses OS trust with Rustls/ring and no implicit proxies, credentials, redirects, custom CA overrides, or body reads. Keep parser and dependency errors redacted even under `RUST_LOG=trace`. musl crypto compilation uses `musl-tools`/target-specific `CC=musl-gcc`, while final linking remains pinned self-contained `rust-lld`; no dynamic OpenSSL dependency is permitted.
- `release-clibox.yml` runs native unit/process tests and validates all eight native targets and the full nine-package set before publication. Publish and verify platform packages before the main package; reuse only identical registry integrity on retries. Dry runs are secret-free and non-publishing.
- `CLIBOX_NPM_PUBLISH_ENABLED=true` and an exact first-party Trusted Publisher on all nine packages are required for npm OIDC/provenance publication. Only the separately guarded npm `publish` and GitHub `publish-release` jobs receive `id-token: write`; the latter uses it solely for Sigstore signing. The npm job must explicitly install the pinned OIDC-capable npm version before validating support and publishing, independent of Node's bundled npm.
- Keep `docs/project-clibox.md`, the clibox domain contracts, root/domain AGENTS rules, release versioning, CI selection/aggregation, and distribution fixtures synchronized.

### pnport Contract

- Issue #958 and docs/project-pnport.md define the complete pnport 0.1.0 contract. Keep all six native execution/installation gates; no partial preview release.
- Use private explicit Cargo members, pnp exactly 0.12.12, the pinned fspy provenance and the root nightly toolchain. Never substitute protected executables or silently run without virtualization.
- Keep dependency views read-only, graph/peer identity stable, cache ownership private, publication atomic and active leases protected. No runtime networking, telemetry, automatic eviction or self-update.
- Native/npm versions and pnport@v<version> identity agree; skip Cargo registry credentials/publication. Complete-set execution/install verification precedes publication authority.
- Keep unpublished pnport Cargo, lock, and npm source versions at `0.0.0` until the first minor bump to `0.1.0`; reject patch and major bumps from `0.0.0`. Release Project follows the common prepare/registry/tag/summary path without native validation or Cargo publication. The separate exact-tag workflow requires all six native execution, installed npm/Yarn PnP, TypeScript, archive, and installer gates before npm, GitHub Release, or Homebrew publication; it defaults to a credential-free dry run. A failed gate leaves the tag intact and blocks publication; recover in the pnport workflow without moving the tag. The public `/pnport` guides remain explicitly unreleased until publication.

- `react-forge`: required affected Node 24 library, native and installed-CLI validation on macOS/Windows/glibc Linux x64/arm64; require real Windows console cancellation, macOS/Linux Office/PDF rendering and six-host benchmarks. Retain existing Forge regression jobs when shared package primitives change. Native/system-font work is uncached, render evidence expires after seven days, and this private workflow cannot publish packages or install production dependencies at runtime.
