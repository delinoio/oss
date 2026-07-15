# public-docs

Rspress-based public documentation app for the Delino OSS monorepo.

## Commands

Run from the repository root:

```bash
pnpm --filter public-docs dev
pnpm --filter public-docs test
pnpm --filter public-docs build
pnpm --filter public-docs preview
```

`pnpm --filter public-docs dev` runs Rspress on fixed port `46249`.
Production output is written to `doc_build` for Cloudflare Pages. Rspress clean
URLs are enabled, so stable public routes such as `/getting-started` do not use
`.html` suffixes.

`pnpm --filter public-docs test` builds the site and runs
`scripts/validate-clean-urls.mjs`. The validator checks generated artifacts for
`/`, `/getting-started`, `/projects-overview`, `/documentation-lifecycle`,
`/cargo-mono`, `/derun`, `/with-watch`, and the legacy `/nodeup` handoff, and
rejects generated internal `.html` route links.

## Files

- `rspress.config.ts`: Rspress site configuration, navigation, and sidebar.
- `scripts/validate-clean-urls.mjs`: Production clean-route validator.
- `docs/index.md`: Landing page.
- `docs/getting-started.md`: Local setup and contribution flow.
- `docs/projects-overview.md`: High-level public project catalog.
- `docs/documentation-lifecycle.md`: Rules for updating internal and public docs together.
- `docs/cargo-mono.md`: Public project guide for `cargo-mono`.
- `docs/derun.md`: Public project guide for `derun`.
- `docs/with-watch.md`: Public project guide for `with-watch`.
- `docs/nodeup.md`: Compatibility handoff page for legacy `/nodeup` links.

Cargo Mono, Derun, and With Watch remain in-site product documentation. Nodeup
and binpm are external top-level links to their standalone documentation apps:

- Nodeup documentation is owned by `apps/nodeup-docs` and published at `https://nodeup.delino.io`.
- binpm documentation is owned by `apps/binpm-docs` and published at `https://binpm.delino.io`.

The legacy `/nodeup` route is kept as a lightweight handoff to
`https://nodeup.delino.io` for existing external links. Do not add in-site
Nodeup or binpm guide routes under `apps/public-docs`.
