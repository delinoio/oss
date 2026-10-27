# labs-site

## Goal

Provide a standalone static marketing site for Delino Labs at `https://labs.delino.io`.

## Project ID

`labs-site`

## Domain Ownership Map

- `apps/labs-site`: static HTML, optional browser TypeScript, build configuration, and Cloudflare Pages configuration.

## Domain Contract Documents

- [Marketing site foundation](apps-labs-site-foundation.md)

## Cross-Domain Invariants

- The canonical origin is `https://labs.delino.io`; the site is independent of `oss.delino.io` and its documentation routes.
- Use TypeScript and Rsbuild for this browser/static-site component, following the static-site default. No backend service, RPC, database, or account is required.
- Cloudflare Pages is the deployment target. Build configuration does not establish DNS, a Pages project, or a live deployment.
- The scaffold provides an empty HTML content slot. Marketing content, design, forms, and analytics require separate product work.

## Change Policy

Update this index and the foundation contract together when paths, origin, or integration boundaries change. Follow the [instruction-update policy](README.md#instruction-update-policy). Record validation and deployment evidence in PRs and CI artifacts.

## References

- [Repository defaults](repository-defaults.md)
- [Environment contract](repository-environment-contract.md)
- [Workflow contract](repository-workflow-contract.md)
