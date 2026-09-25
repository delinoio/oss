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
Production output is written to `doc_build` for Cloudflare Pages. This is one
Rspress site: project Markdown is owned directly by
`docs/runmoor`, `docs/nodeup`, `docs/binpm`, `docs/async-commit-hook`, `docs/clibox`, `docs/pnport`, and `docs/react-forge`, so
the build emits every root and project-subpath route in one pass. Installer
entrypoints are copied from the canonical files in `scripts/install` before the
build. Rspress clean URLs are enabled, so stable public routes do not use
`.html` suffixes.

`pnpm --filter public-docs test` builds the site and runs
the shared `@delinoio/docs-site-switcher` test, then runs
`scripts/validate-clean-urls.mjs`, `scripts/validate-public-docs.mjs`, and
the clibox generated-output regression fixtures.
The validators check root and project-subpath artifacts, clean routes, required
headings and links, accessibility landmarks, site-selector markup, installer
byte identity, the async `/docs` compatibility route, public-content limits,
and forbidden paths in HTML resources and CSS `url()` values.

## Files

- `rspress.config.ts`: Rspress site configuration, navigation, and sidebar.
- `docs/public/_headers`: Root Cloudflare Pages headers for the async-commit-hook subpath.
- `scripts/copy-public-assets.mjs`: Copies canonical installer files into the Cloudflare Pages public root.
- `scripts/validate-clean-urls.mjs`: Root production clean-route validator.
- `scripts/validate-public-docs.mjs`: Project-subpath, selector, installer, and compatibility validator.
- `docs/index.md`: Landing page.
- `docs/getting-started.md`: Local setup and contribution flow.
- `docs/projects-overview.md`: High-level public project catalog.
- `docs/documentation-lifecycle.md`: Rules for updating internal and public docs together.
- `docs/clibox/`: Twelve clibox user guides, including the complete command index.
- `docs/pnport/`: Ten pnport guides, published before 0.1.0 with explicit unreleased status.
- `docs/react-forge/`: Fourteen guides for the public library, formats, Office editing, Figma, CLI, and local MCP.
- `docs/cargo-mono.md`: Public project guide for `cargo-mono`.
- `docs/derun.md`: Public project guide for `derun`.
- `docs/with-watch.md`: Public project guide for `with-watch`.
- `docs/devhud/`: Stable DevHud installation, usage, privacy, security, support, administration, and release guidance routes.

Cargo Mono, Derun, and With Watch remain in-site product documentation. Nodeup,
binpm, Runmoor, async-commit-hook, clibox, pnport, and React Forge are first-class subdirectories of
this app and publish at `https://oss.delino.io/nodeup/`,
`https://oss.delino.io/binpm/`, `https://oss.delino.io/runmoor/`, and
`https://oss.delino.io/async-commit-hook/`, and
`https://oss.delino.io/clibox/`, `https://oss.delino.io/pnport/`, and `https://oss.delino.io/react-forge/`. Former standalone domains for migrated projects are not
redirected by this repository; their Pages projects and DNS records are an
operator decommissioning task after the consolidated publication is verified.
