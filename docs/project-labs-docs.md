# Project: labs-docs

## Goal

Provide a static documentation site for Delino Labs at `https://docs.labs.delino.io`.

## Project ID

`labs-docs`

## Domain Ownership Map

- apps: `apps/labs-docs` owns the standalone documentation app and its public content.

## Domain Contract Documents

- [Labs documentation foundation](apps-labs-docs-foundation.md)

## Cross-Domain Invariants

- The canonical production origin is `https://docs.labs.delino.io` with base `/`.
- This app is independent of the consolidated `public-docs` publisher and site registry.
- Public pages use the default Rspress theme and English text. Internal contracts remain in `docs/`.
- TypeScript configuration and Markdown content use Rspress, with Cloudflare Pages as the deployment target, following repository defaults.

## Change Policy

Update this index, the app contract and the documentation catalog together when paths or interfaces change. Update instructions only when development procedures, ownership or rules change. Record validation and deployment limits in PRs and CI records.

## References

- [Repository defaults](repository-defaults.md)
- [Documentation catalog](README.md)
