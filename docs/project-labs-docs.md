# Project: labs-docs

## Goal

Provide a static documentation site for Delino Labs at `https://docs.labs.delino.io`.

## Project ID

`labs-docs`

## Domain Ownership Map

- apps: `apps/labs-docs` owns the standalone documentation app and its public content.

- `scripts/localization`: shared local translation generation and offline validation.

## Domain Contract Documents

- [Labs documentation foundation](apps-labs-docs-foundation.md)

- [Shared Labs localization](apps-labs-localization-contract.md)
- [TaiRouter public documentation](apps-tairouter-docs-contract.md)

## Cross-Domain Invariants

- The canonical production origin is `https://docs.labs.delino.io` with base `/`.
- This app is independent of the consolidated `public-docs` publisher and site registry.
- Public pages use the default Rspress theme and English source text with registered translations. TaiRouter is an open-source Delino Labs product; its eleven public guides are initially English-only at `/tairouter/` and `/tairouter/guide/*`, linked from every language sidebar. Internal contracts remain in `docs/`.
- TypeScript configuration and Markdown content use Rspress, with Cloudflare Pages as the deployment target, following repository defaults.

## Change Policy

Update this index, the app contract and the documentation catalog together when paths or interfaces change. Update instructions only when development procedures, ownership or rules change. Record validation and deployment limits in PRs and CI records.

## References

- [Repository defaults](repository-defaults.md)
- [Documentation catalog](README.md)
