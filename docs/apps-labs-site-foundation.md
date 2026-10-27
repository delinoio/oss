# labs-site foundation

## Scope

Own `apps/labs-site`, the standalone static marketing site for Delino Labs. The scaffold reserves the content slot without selecting a marketing design or publishing product claims.

## Runtime and Language

Use the root Node.js 24 selector and pinned pnpm for development. Use TypeScript and Rsbuild with plain HTML; the production host serves generated static assets without a Node.js server. React is not required by this scaffold.

## Users and Operators

Visitors access the public marketing site. Maintainers author content and build the site. Cloudflare operators manage the Pages project, custom domain, DNS, and deployment credentials.

## Interfaces and Contracts

- Canonical origin: `https://labs.delino.io`.
- Route `/` uses `index.html`; `src/index.ts` is the browser entry point for future enhancements. Content must remain available in HTML without JavaScript.
- A top-level `public/404.html` prevents Pages from treating the site as a single-page application. Unknown routes must return HTTP 404 on the production host.
- The scaffold is intentionally blank and uses `noindex` plus a disallowing `robots.txt`. Replace these directives when approved marketing content is ready for publication.
- `pnpm dev:labs-site` starts development at `http://127.0.0.1:46312` with strict port binding. The shared development wrapper rejects host/port overrides, forwards signals, and waits for shutdown.
- Cloudflare Pages project name: `labs-site`. Build from the repository root with `pnpm --filter labs-site build`; publish `apps/labs-site/dist`. The app-local `wrangler.toml` identifies the project and output directory.
- Attach `labs.delino.io` through Pages custom-domain configuration and complete its DNS validation. Do not treat the canonical URL or Wrangler file as proof of domain ownership or deployment.
- No automatic publisher is introduced. Production activation and account acceptance remain operator work.

## Localization

Follow the [shared Labs localization contract](apps-labs-localization-contract.md) for catalogs, localized routes, ChatGPT plan generation, credential ownership and offline validation.

## Storage

No persistent data, cookies, uploads, or browser storage. `dist/` is ignored generated output and must be removed from the final worktree.

## Security

The static app requires no secrets or environment file. Do not add credentials to client code or configuration. No third-party scripts, forms, tracking, or external assets are bundled.

## Logging

Rsbuild owns build and development diagnostics. The affected CI runner records task and revision metadata. There is no business runtime or visitor telemetry in the scaffold.

## Build and Test

- Install at the repository root with `pnpm install --frozen-lockfile`.
- Run `pnpm test` from `apps/labs-site` for TypeScript checking and a production build.
- `pnpm --filter labs-site build` generates `dist/` for deployment.
- `pnpm --filter labs-site preview` serves the build on strict loopback port `46313`.
- Root `pnpm format` and `pnpm format:check` own formatting with the exact comparison required by the workflow contract.
- The central `node-labs-site-test` job runs the workspace test through the affected runner on Ubuntu for selected PRs, main pushes, and affected manual runs. The final CI result includes this job. CI does not deploy the site.
- Build success establishes static artifact generation only. HTTP 404 status, DNS, TLS, and custom-domain acceptance require deployed-host checks.

## Dependencies and Integrations

Use the workspace's pinned `@rsbuild/core` and TypeScript versions. The pnpm workspace glob discovers the app. Package-local Turbo configuration declares the test's generated `dist/**` output. Cloudflare Pages serves the build; no other project depends on this app.

## Change Triggers

Update [the project index](project-labs-site.md) for origin or path changes. Update the environment and workflow contracts for development commands and CI ownership changes. Follow the [instruction-update policy](README.md#instruction-update-policy) for procedure or ownership changes. Keep marketing content independent of public product documentation.

## References

- [Project index](project-labs-site.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Pages configuration](https://developers.cloudflare.com/pages/functions/wrangler-configuration/)
- [Pages custom domains](https://developers.cloudflare.com/pages/configuration/custom-domains/)
