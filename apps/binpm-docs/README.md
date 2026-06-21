# binpm-docs

Rspress-based documentation app for the `binpm` project.

Canonical production URL: `https://binpm.delino.io`.

## Commands

Run from the repository root:

```bash
pnpm --filter binpm-docs dev
pnpm --filter binpm-docs test
pnpm --filter binpm-docs build
pnpm --filter binpm-docs preview
```

`pnpm --filter binpm-docs dev` runs Rspress on fixed port `46260`.
`pnpm --filter binpm-docs preview` serves the production build on fixed port `46261`.

Production deployment is static Cloudflare Pages output from `doc_build`. Rspress clean URLs are enabled, so stable public route IDs such as `/installation` must be generated and internal links must not point at `.html` suffixes.

`pnpm --filter binpm-docs test` builds the site and runs `scripts/validate-clean-urls.mjs`. The validator checks the stable route IDs `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/releases`, `/troubleshooting`, and `/reference`; each route must have a build output artifact and generated internal HTML links must use clean public route IDs.

The production URL is deployment metadata; docs content must come from repository contracts, not from assumptions about the current live site contents.

## Files

- `rspress.config.ts`: Rspress site configuration and navigation.
- `scripts/validate-clean-urls.mjs`: Production build validator for stable clean URL routes and links.
- `docs/index.md`: binpm docs landing page.
- `docs/installation.md`: Installation scope, supported release assets, PATH setup, and verification boundary.
- `docs/getting-started.md`: First-use guide for existing documented behavior.
- `docs/commands.md`: Command surface overview.
- `docs/local-tooling.md`: `binpm.toml`, `binpm.lock`, local bin paths, and frozen-lockfile behavior.
- `docs/cache-and-verification.md`: Cache, checksum, and verification behavior.
- `docs/troubleshooting.md`: Common diagnostic and verification commands.
- `docs/reference.md`: Stable source and target reference.
