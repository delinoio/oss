# AIPRouter console contract

## Scope

Owns `apps/aiprouter`, the responsive multilingual web console. This is a separate build component from the service and public documentation.

## Runtime and Language

React, TypeScript, Rsbuild, React Query, and `@connectrpc/connect-query`. Node.js follows root `.nvmrc`. The authenticated console is served in the self-hosted Compose workflow, an explicit exception to the default Cloudflare Pages deployment for static apps.

## Users and Operators

Members, owners, administrators, and instance operators, with distinct authority defined in the service contract.

## Interfaces and Contracts

### Web requirements, documentation, and support

Web requirements:

- English is the source and default language. Support Korean (`ko`), Japanese (`ja`), Simplified Chinese (`zh-Hans`), Spanish (`es`), French (`fr`), German (`de`), and Brazilian Portuguese (`pt-BR`).
- Responsive desktop/mobile support.
- WCAG 2.2 AA.
- Follow repository requirements for focus on a single critical form input and Escape dismissal of dialogs.
- Make decided permission, unknown-cost, invitation failure/expiry, and restoration-conflict outcomes distinguishable.
- The behavior contract keeps visual decisions separate. Implement the approved [console design](apps-aiprouter-console-design.md).

Use generated TypeScript management clients from `packages/aiprouter-client`. Support organization/member/key/provider/usage/operator flows and browser device approval. Onboarding is choose organization → connect personal providers → configure models/routing → issue API key → connect client. Users without organizations choose creation or invitation acceptance; do not create one automatically.

### Installable web app

Expose a same-origin web app manifest on every console page, with stable ID and start URL `/overview`, root scope, standalone display, and AIPRouter name. Supply 192px and 512px PNG icons with maskable safe padding and a 180px Apple touch icon. Use the console primary and background colors for installation metadata.

Installation uses the browser's native install or Add to Home Screen controls. Require HTTPS outside loopback evaluation. The installed console uses the existing OIDC session and route rules and requires network access. Do not register a service worker, add offline response caches, or persist credentials or management content for installation. Browser/OS installation and OIDC acceptance require separate platform verification.

### Page routes

Ordinary internal link activation updates browser history and the rendered route without reloading the document. Preserve the authenticated shell and query caches within the same user and organization while lazy page code loads. Back and Forward restore the route and organization scope. Confirm before discarding unsaved edits. Modified clicks, new-tab targets, downloads, fragment links, external URLs, and authentication endpoints retain native browser behavior.

Console navigation uses absolute pathname routes and the `organization` query parameter for workspace scope. Personal pages use `/overview`, `/providers`, `/pools`, `/routing`, `/keys`, `/usage`, and `/connect`. Organization administration uses `/members`, `/invitations`, `/organization-usage`, `/audit`, and `/settings`. Account and creation use `/account` and `/onboarding`.

Standalone flows use `/sign-in`, `/device`, `/invitation`, and `/ownership`; preserve their error, code, token, invitation, and organization parameters. Instance sections use `/instance/health`, `/instance/settings` (request limits), `/instance/retention`, `/instance/destinations`, `/instance/prices`, and `/instance/audit`. The health page includes instance usage aggregates.

Only canonical pathname routes are supported. Reject `?page=` navigation, `org` workspace selectors, and `organization_id` workspace selectors on authenticated pages. The former aliases `/`, `/organizations`, `/connect-client`, `/invitations/accept`, `/instance`, and `/instance/usage` are not console routes. Unknown or retired routes must not render the overview. Internal navigation and default OIDC return targets use `/overview`. The service console allowlist and OIDC return targets must enforce the same route rules as the frontend.

Omit an empty `organization` workspace selector from canonical console addresses. For example, `/keys?organization=` normalizes to `/keys`; a selected organization uses `/keys?organization=<id>`. Keep standalone flow parameters, including ownership acceptance's `organization_id`, unchanged.

### Localization

The console owns `i18n/locales.json`, `i18n/messages/<lang>.json`, and `i18n/state.json`. Source catalog keys are stable. Translate console-owned copy, accessible labels, status/enum displays, and validation notices. Preserve technical identifiers, commands, URLs, provider names, user-entered values, and service data. Format display dates and numbers with the selected locale; keep date timezones explicit and preserve decimal cost precision.

A labeled native language selector appears above every page, including sign-in, standalone approval flows, and the instance console. Use native language names, a 44px minimum control height, visible focus, keyboard selection and native Escape dismissal. Align the selector right on desktop; wrap at 320 CSS pixels. Use the existing console tokens. Language changes preserve component identity, unsaved inputs, routes, query parameters, organization scope and pending actions. Update the HTML language attribute to the content language. Default to English without browser-language redirects. Save only the locale in versioned browser storage; denied storage falls back to in-memory selection. Load committed target catalogs on demand; retain the current language on load failure and show retry guidance. Target catalog generation is deferred: if a selected locale has no catalog yet, keep English text and HTML language while formatting dates and numbers with the selected locale. Announce loading and errors accessibly.

Reuse the local ChatGPT-plan authentication, generation and offline structural validation in `scripts/localization`, under the [shared tooling contract](apps-labs-localization-contract.md#authentication-and-inference). Run root `pnpm i18n:login` and `pnpm i18n:models`, then `pnpm --dir apps/aiprouter i18n:translate` (optionally append a language). These commands require eligible plan consent; no API key or copied Codex session is used. Review translations before commit. Console `pnpm i18n:check`, tests and builds report wholly unstarted languages as deferred without account access. Once a catalog or state entry exists, require complete, current translations with protected placeholders; do not treat partial output or removal of a tracked catalog as deferred work. Keep unstarted languages out of generated state instead of fabricating translations or fingerprints. Root translation/check commands include this workspace. Never translate internal contracts or bundle authentication modules or tokens in the console.

## Storage

PostgreSQL through server APIs is authoritative. React Query caches must respect organization/user boundaries and invalidate affected views after logout, organization changes, deletion, and permission changes. Temporary UI state must not become durable secret/content storage.

## Security

The server checks current permissions on every management request. A stale page does not retain administrator authority. Do not expose another member’s credentials or request content. Render unknown cost and missing counts distinctly from zero. Use the [service session, consent, and deletion rules](servers-aiprouter-service-contract.md).

## Logging

Use structured logs and request identifiers. Never log provider secrets, API keys, device secrets, raw prompts, or raw responses. Distinguish unknown usage, write failures, cancellation, limits, and upstream/gateway errors. See the [observability contract](servers-aiprouter-operations-contract.md).

## Build and Test

Run `pnpm test` from `apps/aiprouter` after frontend changes. Rstest owns the console unit and component suite; `rstest.config.ts` configures jsdom, shared setup, and automatic mock restoration. Include automated responsive/accessibility, keyboard, focus, Escape, permission, device-approval, and failure-state checks. Follow [release validation](servers-aiprouter-operations-contract.md); visual design and native/live acceptance are separate from these checks.

## Dependencies and Integrations

- [packages-aiprouter-client-contract](packages-aiprouter-client-contract.md)
- [protos-aiprouter-management-contract](protos-aiprouter-management-contract.md)
- [servers-aiprouter-service-contract](servers-aiprouter-service-contract.md)
- [apps-aiprouter-docs-contract](apps-aiprouter-docs-contract.md)
- [servers-aiprouter-operations-contract](servers-aiprouter-operations-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-aiprouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)
