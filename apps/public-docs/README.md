# public-docs

Rspress-based public documentation app for the Delino OSS monorepo.

## Commands

Run from the repository root:

```bash
pnpm --filter public-docs dev
pnpm --filter public-docs test
pnpm --filter public-docs build
pnpm --filter public-docs build:frontend
pnpm --filter public-docs test:routes
pnpm --filter public-docs preview
```

`pnpm --filter public-docs dev` runs Rspress on fixed port `46302`. It checks the exact port before startup and exits on conflicts instead of automatically selecting another port.
Production output is written to `doc_build` for Cloudflare Pages. The build first
creates the root Rspress site and then assembles the validated outputs from
`runmoor-docs`, `nodeup-docs`, `binpm-docs`, and `async-commit-hook-docs` under
`doc_build/runmoor`, `doc_build/nodeup`, `doc_build/binpm`, and
`doc_build/async-commit-hook`. Rspress clean URLs are enabled, so stable public
routes do not use `.html` suffixes.

`pnpm --filter public-docs test` builds the site and runs
`scripts/validate-clean-urls.mjs` and `scripts/validate-integrated-docs.mjs`.
The validators check root and project-subpath artifacts, clean routes, required
headings and links, accessibility landmarks, site-selector markup, installer
byte identity, the async `/docs` compatibility route, public-content limits,
and forbidden paths in HTML resources and CSS `url()` values. The release
workflow injects the configured non-secret DevHud store identifiers into every
generated text asset before publication.

## Files

- `rspress.config.ts`: Rspress site configuration, navigation, and sidebar.
- `docs/public/_headers`: Root Cloudflare Pages headers for the assembled async-commit-hook subpath.
- `scripts/build-integrated-docs.mjs`: Builds and assembles the four project documentation apps.
- `scripts/validate-clean-urls.mjs`: Root production clean-route validator.
- `scripts/validate-integrated-docs.mjs`: Aggregated subpath, selector, installer, and compatibility validator.
- `docs/index.md`: Landing page.
- `docs/getting-started.md`: Local setup and contribution flow.
- `docs/projects-overview.md`: High-level public project catalog.
- `docs/documentation-lifecycle.md`: Rules for updating internal and public docs together.
- `docs/cargo-mono.md`: Public project guide for `cargo-mono`.
- `docs/derun.md`: Public project guide for `derun`.
- `docs/with-watch.md`: Public project guide for `with-watch`.
- `docs/devhud/`: Stable DevHud installation, usage, privacy, security, support, administration, and release guidance routes.

Cargo Mono, Derun, and With Watch remain in-site product documentation. Nodeup,
binpm, Runmoor, and async-commit-hook retain Markdown ownership in their own
apps and are assembled at `https://oss.delino.io/nodeup/`,
`https://oss.delino.io/binpm/`, `https://oss.delino.io/runmoor/`, and
`https://oss.delino.io/async-commit-hook/`. The old standalone domains are not
redirected by this repository; their Pages projects and DNS records are an
operator decommissioning task after the consolidated publication is verified.
