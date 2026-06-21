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

Production deployment is static Cloudflare Pages output from `doc_build`. The production URL is deployment metadata; docs content must come from repository contracts, not from assumptions about the current live site contents.

## Files

- `rspress.config.ts`: Rspress site configuration and navigation.
- `docs/index.md`: binpm docs landing page.
- `docs/installation.md`: Installation scope and current implementation status.
- `docs/getting-started.md`: First-use guide for existing documented behavior.
- `docs/commands.md`: Command surface overview.
- `docs/local-tooling.md`: `binpm.toml`, `binpm.lock`, local bin paths, and frozen-lockfile behavior.
- `docs/cache-and-verification.md`: Cache, checksum, and verification contracts.
- `docs/troubleshooting.md`: Common diagnostics and validation commands.
- `docs/reference.md`: Stable source, route, storage, and validation reference.
