# public-docs

Mintlify-based public documentation app for the Delino OSS monorepo.

## Commands

Run from repository root:

```bash
pnpm --filter public-docs dev
pnpm --filter public-docs test
```

`pnpm --filter public-docs dev` runs Mintlify on fixed port `46249`.

## Files

- `docs.json`: Mintlify site configuration and navigation.
- `index.mdx`: Landing page.
- `getting-started.mdx`: Local setup and contribution flow.
- `projects-overview.mdx`: High-level public project catalog.
- `documentation-lifecycle.mdx`: Rules for updating internal and public docs together.
- `cargo-mono.mdx`: Public project guide for `cargo-mono`.
- `derun.mdx`: Public project guide for `derun`.
- `with-watch.mdx`: Public project guide for `with-watch`.

Nodeup is intentionally not published through this Mintlify app. Its dedicated documentation app is `apps/nodeup-docs` at `https://nodeup.delino.io`.
