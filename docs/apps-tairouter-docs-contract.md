# TaiRouter public documentation contract

## Scope

Owns `apps/labs-docs/docs/en/tairouter`, English public guides published by the independent Delino Labs documentation application. TaiRouter means **Team AI Router** and is an open-source Delino Labs product. Repository source-of-truth contracts remain in `docs/`.

## Runtime and Language

TypeScript with Rspress and the root Node.js baseline. Static publication targets Cloudflare Pages under repository defaults; this documentation hosting does not deploy a hosted TaiRouter service.

## Users and Operators

Self-hosting operators, end users, and CLI users.

## Interfaces and Contracts

Include English public guidance for all of:

1. Installation, browser-managed console installation (including secure-context and online-only limits), upgrades, rollback, backup, and restore.
2. OIDC, SMTP, HTTPS, and required R2 configuration.
3. Organizations, invitations, and API keys.
4. Provider/OAuth setup and routing.
5. Compatibility and actual live-verification status.
6. Observability and troubleshooting.
7. Manual migration from existing setups.

Keep internal design, source-of-truth contracts and operational internals in repository `docs/`. Exclude internal runbooks from public pages.

Keep public docs focused on user/operator workflows and supported interfaces; keep internal implementation details in repository contracts. `apps/labs-docs` builds and publishes the guides at `https://docs.labs.delino.io/tairouter/`. Keep all eleven routes in the Labs route catalog and navigation. Preserve all migrated guide content and verification limits. Publish these guides in English first, with exact `sourceOnly.pages` entries in the Labs localization registry. Every localized Labs sidebar labels the TaiRouter group as English and links to the same unprefixed routes. Do not add translated copies or missing-language route aliases.

The marketing page at `https://labs.delino.io/tairouter/` links to these guides and introduces the product in all eight Labs languages. Remove TaiRouter content, route registration and site-selector entries from `apps/public-docs`; it no longer publishes TaiRouter. The new canonical location is a route migration with no old-host redirect or alias. Existing external links require updating by their owners.

Exclude automatic import of opencodex settings, credentials, or usage. Users register providers/accounts/routing again using manual migration guidance.

Exclude user data export. Provide console inspection/deletion and operator backup only.

Publish supported workflows and interfaces with actual compatibility and verification limits. Do not copy repository-internal contracts or operational internals into public pages. The canonical publication origin is `https://docs.labs.delino.io`; source integration does not establish deployment acceptance.

## Storage

Static content only; no application credentials, tenant data, or raw inference content.

## Security

Examples must contain placeholders and safe values. Explain signed-URL bearer access, local relay trust, secret storage confirmation, backup key separation, post-restore invalidation, and manual revocation/recovery without exposing internal secret paths.

## Logging

Documentation build/dev diagnostics must not reveal secrets. Documentation servers use a fixed loopback port and fail on conflicts under the environment contract.

## Build and Test

Run `pnpm test` from `apps/labs-docs` after frontend changes. Use root `pnpm dev:labs-docs` on fixed loopback port 46303 for documentation development. The existing Labs Docs publisher owns deployment; no additional app or port is introduced. Build and validate navigation, links, examples, and the seven required guidance areas against owning contracts. Do not label fixtures as live operational acceptance.

## Dependencies and Integrations

- [servers-tairouter-service-contract](servers-tairouter-service-contract.md)
- [cmds-tairouter-client-contract](cmds-tairouter-client-contract.md)
- [servers-tairouter-operations-contract](servers-tairouter-operations-contract.md)
- [apps-tairouter-console-contract](apps-tairouter-console-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-tairouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)
