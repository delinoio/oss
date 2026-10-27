# AIPRouter public documentation contract

## Scope

Owns `apps/public-docs/docs/aiprouter`, English public guides published by the consolidated documentation application. Repository source-of-truth contracts remain in `docs/`.

## Runtime and Language

TypeScript with Rspress and the root Node.js baseline. Static publication targets Cloudflare Pages under repository defaults; this documentation hosting does not deploy a hosted AIPRouter service.

## Users and Operators

Self-hosting operators, end users, and CLI users.

## Interfaces and Contracts

Include English public guidance for all of:

1. Installation, upgrades, rollback, backup, and restore.
2. OIDC, SMTP, HTTPS, and required R2 configuration.
3. Organizations, invitations, and API keys.
4. Provider/OAuth setup and routing.
5. Compatibility and actual live-verification status.
6. Observability and troubleshooting.
7. Manual migration from existing setups.

Include internal design and source-of-truth contract documents. Exclude internal runbooks.

Keep public docs focused on user/operator workflows and supported interfaces; keep internal implementation details in repository contracts. `apps/public-docs` builds and publishes the guides at `https://oss.delino.io/aiprouter`. Keep all eleven routes in the shared route catalog, navigation and site selector. The standalone upstream docs app is replaced by this content root; no second publisher or development port is introduced.

Exclude automatic import of opencodex settings, credentials, or usage. Users register providers/accounts/routing again using manual migration guidance.

Exclude user data export. Provide console inspection/deletion and operator backup only.

Publish supported workflows and interfaces with actual compatibility and verification limits. Do not copy repository-internal contracts or operational internals into public pages. The canonical publication origin is `https://oss.delino.io`; source integration does not establish deployment acceptance.

## Storage

Static content only; no application credentials, tenant data, or raw inference content.

## Security

Examples must contain placeholders and safe values. Explain signed-URL bearer access, local relay trust, secret storage confirmation, backup key separation, post-restore invalidation, and manual revocation/recovery without exposing internal secret paths.

## Logging

Documentation build/dev diagnostics must not reveal secrets. Documentation servers use a fixed loopback port and fail on conflicts under the environment contract.

## Build and Test

Run `pnpm test` from `apps/public-docs` after frontend changes. Use root `pnpm dev:public-docs` on fixed loopback port 46302 for documentation development. Build and validate navigation, links, examples, and the seven required guidance areas against owning contracts. Do not label fixtures as live operational acceptance.

## Dependencies and Integrations

- [servers-aiprouter-service-contract](servers-aiprouter-service-contract.md)
- [cmds-aiprouter-client-contract](cmds-aiprouter-client-contract.md)
- [servers-aiprouter-operations-contract](servers-aiprouter-operations-contract.md)
- [apps-aiprouter-console-contract](apps-aiprouter-console-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-aiprouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)
