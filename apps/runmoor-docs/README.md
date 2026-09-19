# runmoor-docs

Rspress documentation for Runmoor, published at `https://runmoor.delino.io`.

## Commands

From the repository root:

```sh
pnpm dev:runmoor-docs
pnpm --filter runmoor-docs test
pnpm --filter runmoor-docs build
pnpm --filter runmoor-docs preview
```

Package-local `pnpm dev` and root `pnpm dev:runmoor-docs` use `127.0.0.1:46309`. Production preview uses `127.0.0.1:46271`. Both commands keep their fixed loopback address, fail with recovery guidance if the port is occupied, and stop their server when interrupted.

`pnpm test` builds the site and validates all seven route artifacts, article headings and links, main landmarks, clean internal URLs, absence of legacy `/runmoor` route links, and public-content restrictions. Regression tests inject synthetic credentials and private paths into temporary copies of the generated HTML and verify rejection without echoing the values. Valid public URLs, static assets, and documented credential placeholders remain supported. The routes are `/`, `/install`, `/configuration`, `/commands`, `/docker`, `/tart`, and `/operations`.

## Cloudflare Pages

- Build root: repository root.
- Install dependencies with the repository-pinned pnpm and lockfile.
- Build command: `pnpm --filter runmoor-docs build`.
- Output directory: `apps/runmoor-docs/doc_build`.
- Production domain: `runmoor.delino.io`.

Hosting project creation, domain configuration, and publication are separate operator steps. Generated output is ignored and must not be committed.

## Content ownership

The seven Runmoor pages have moved from `public-docs`. The old `/runmoor` and child routes are removed without redirects or handoff pages; the public site links here through its top navigation, home, and project catalog.

Keep the guides aligned with `docs/project-runmoor.md`, `docs/cmds-runmoor-foundation.md`, and `docs/apps-runmoor-docs-foundation.md`. Preserve all preview, verification, security, and licensing limits. The CLI release README remains available independently of this site.
