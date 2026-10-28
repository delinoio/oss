# labs-site foundation

## Scope

Own `apps/labs-site`, the standalone static marketing site for Delino Labs. The root introduces Delino Labs and links to its products, documentation and source. The TaiRouter product page presents the approved marketing design and source-backed product behavior.

## Runtime and Language

Use the root Node.js 24 selector and pinned pnpm for development. Use TypeScript and Rsbuild with plain HTML; the production host serves generated static assets without a Node.js server. React is not required by this scaffold.

## Users and Operators

Visitors access the public marketing site. Maintainers author content and build the site. Cloudflare operators manage the Pages project, custom domain, DNS, and deployment credentials.

## Interfaces and Contracts

- Canonical origin: `https://labs.delino.io`.
- Route `/` uses `index.html`; `src/index.ts` is the browser entry point for future enhancements. Content must remain available in HTML without JavaScript.
- A top-level `public/404.html` prevents Pages from treating the site as a single-page application. Unknown routes must return HTTP 404 on the production host.
- Root language entries publish the product landing page with canonical, alternate-language and social metadata; they permit indexing. `/tairouter/` and `/<lang>/tairouter/` use `tairouter.html`, include canonical and alternate-language metadata, and permit indexing. `robots.txt` permits crawling the landing and product pages.
- TaiRouter means **Team AI Router**, an open-source Delino Labs product. The product page uses an off-white background, near-black typography, cobalt actions and a static personal-routing illustration. Its hierarchy is a product introduction, personal accounts/routing/self-hosting features, client setup guidance, and documentation/source calls to action. Keep the illustration conceptual; do not present it as a live console or imply shared credentials.
- All product content is available without JavaScript. Marketing text comes from the eight language catalogs at build time. The native language menu remains usable without JavaScript; the optional browser entry adds Escape and outside-click dismissal. Mobile uses one column, wrapping actions and navigation. Preserve visible keyboard focus, skip navigation, headings and diagram alternative text.
- Documentation links target `https://docs.labs.delino.io/tairouter/`; source and license links target the repository on GitHub. Use illustrative endpoint/model values and preserve release and compatibility limits. No invented provider acceptance, performance figures, testimonials, pricing or hosted-service availability is published.
- `pnpm dev:labs-site` starts development at `http://127.0.0.1:46312` with strict port binding. The shared development wrapper rejects host/port overrides, forwards signals, and waits for shutdown.
- Cloudflare Pages project name: `labs-site`. Build from the repository root with `pnpm --filter labs-site build`; publish `apps/labs-site/dist`. The app-local `wrangler.toml` identifies the project and output directory.
- Attach `labs.delino.io` through Pages custom-domain configuration and complete its DNS validation. Do not treat the canonical URL or Wrangler file as proof of domain ownership or deployment.
- No automatic publisher is introduced. Production activation and account acceptance remain operator work.

## Landing page

The home page uses the same off-white, near-black and cobalt visual language as the TaiRouter product page. Its hierarchy is a Delino Labs introduction with product/source actions, a static product-map illustration, a featured TaiRouter section, developer-tool cards for binpm, nodeup and cargo-mono, and a closing source call to action. The map is decorative and does not imply integration between the products. The TaiRouter illustration is conceptual, with separate personal credential paths and generic provider labels.

The product action scrolls to the featured section. TaiRouter links to the same-language product route and the English Labs guides. Developer-tool documentation links target `https://oss.delino.io/binpm/`, `https://oss.delino.io/nodeup/` and `https://oss.delino.io/cargo-mono/`. The main Docs link targets `https://docs.labs.delino.io/`. Marketing copy describes source-backed capabilities without promising hosted services, release availability or performance. No signup, pricing, visitor telemetry or external assets are introduced.

At narrow widths, the hero, featured section and tool cards become a single column; navigation and actions wrap. Keep content available without JavaScript, touch targets at least 44 pixels, visible keyboard focus, a skip link and native language navigation with Escape dismissal. Long translations must wrap without horizontal scrolling. Root language selection stays on the landing page; product language selection stays on the product page.

## Localization

Follow the [shared Labs localization contract](apps-labs-localization-contract.md) for catalogs, localized routes, ChatGPT plan generation, credential ownership and offline validation.

## Storage

No persistent data, cookies, uploads, or browser storage. `dist/` is ignored generated output and must be removed from the final worktree.

## Security

The static app requires no secrets or environment file. Do not add credentials to client code or configuration. No third-party scripts, forms, tracking, or external assets are bundled.

## Logging

Rsbuild owns build and development diagnostics. The affected CI runner records task and revision metadata. There is no business runtime or visitor telemetry on the site.

## Build and Test

- Install at the repository root with `pnpm install --frozen-lockfile`.
- Run `pnpm test` from `apps/labs-site` for localization fixtures, TypeScript checking, a production build and static route/content checks. Check each product language, canonical/alternate links, documentation/source actions and populated landing pages. Inspect desktop and mobile layouts and language-menu keyboard dismissal when changing the page.
- `pnpm --filter labs-site build` generates `dist/` for deployment.
- `pnpm --filter labs-site preview` serves the build on strict loopback port `46313`.
- Root `pnpm format` and `pnpm format:check` own formatting with the exact comparison required by the workflow contract.
- The central `node-labs-site-test` job runs the workspace test through the affected runner on Ubuntu for selected PRs, main pushes, and affected manual runs. The final CI result includes this job. CI does not deploy the site.
- Build success establishes static artifact generation only. HTTP 404 status, DNS, TLS, and custom-domain acceptance require deployed-host checks.

## Dependencies and Integrations

Use the workspace's pinned `@rsbuild/core` and TypeScript versions. Declare Node.js 24 types directly for the Node APIs used by the build configuration; do not rely on another workspace's test runner to supply them. The pnpm workspace glob discovers the app. Package-local Turbo configuration declares the test's generated `dist/**` output. Cloudflare Pages serves the build; no other project depends on this app.

## Change Triggers

Update [the project index](project-labs-site.md) for origin or path changes. Update the environment and workflow contracts for development commands and CI ownership changes. Follow the [instruction-update policy](README.md#instruction-update-policy) for procedure or ownership changes. Keep marketing content independent of public product documentation.

## References

- [Project index](project-labs-site.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Pages configuration](https://developers.cloudflare.com/pages/functions/wrangler-configuration/)
- [Pages custom domains](https://developers.cloudflare.com/pages/configuration/custom-domains/)
