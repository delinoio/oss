# AIPRouter console contract

## Scope

Owns `apps/aiprouter`, the responsive English web console. This is a separate build component from the service and public documentation.

## Runtime and Language

React, TypeScript, Rsbuild, React Query, and `@connectrpc/connect-query`. Node.js follows root `.nvmrc`. The authenticated console is served in the self-hosted Compose workflow, an explicit exception to the default Cloudflare Pages deployment for static apps.

## Users and Operators

Members, owners, administrators, and instance operators, with distinct authority defined in the service contract.

## Interfaces and Contracts

### Web requirements, documentation, and support

Web requirements:

- English only.
- Responsive desktop/mobile support.
- WCAG 2.2 AA.
- Follow repository requirements for focus on a single critical form input and Escape dismissal of dialogs.
- Make decided permission, unknown-cost, invitation failure/expiry, and restoration-conflict outcomes distinguishable.
- The behavior contract keeps visual decisions separate. Implement the approved [console design](apps-aiprouter-console-design.md).

Use generated TypeScript management clients from `packages/aiprouter-client`. Support organization/member/key/provider/usage/operator flows and browser device approval. Onboarding is choose organization → connect personal providers → configure models/routing → issue API key → connect client. Users without organizations choose creation or invitation acceptance; do not create one automatically.

### Page routes

Console navigation uses absolute pathname routes and the `organization` query parameter for workspace scope. Personal pages use `/overview`, `/providers`, `/pools`, `/routing`, `/keys`, `/usage`, and `/connect`. Organization administration uses `/members`, `/invitations`, `/organization-usage`, `/audit`, and `/settings`. Account and creation use `/account` and `/onboarding`.

Standalone flows use `/sign-in`, `/device`, `/invitation`, and `/ownership`; preserve their error, code, token, invitation, and organization parameters. Instance sections use `/instance/health`, `/instance/settings` (request limits), `/instance/retention`, `/instance/destinations`, `/instance/prices`, and `/instance/audit`. The health page includes instance usage aggregates.

Keep existing `?page=` links and `org`/`organization_id` workspace selectors compatible, normalizing them to canonical addresses without adding browser history entries. Preserve `/`, `/organizations`, `/connect-client`, `/invitations/accept`, `/instance`, and `/instance/usage` as aliases for overview, overview, connect, invitation acceptance, instance health, and instance health respectively. Unknown paths must not render the overview. The service console allowlist and OIDC return targets must support the same routes as the frontend.

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
