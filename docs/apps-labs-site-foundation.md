# labs-site repository boundary

## Scope

The static app, source content, build configuration, localization catalogs and validation moved to [`delinoio/apps`](https://github.com/delinoio/apps/tree/main/apps/labs-site). This repository no longer builds or develops the app.

## Interfaces and Contracts

The canonical origin remains `https://labs.delino.io`. The marketing site continues to link to TaiRouter source in this repository.

Use the [destination foundation](https://github.com/delinoio/apps/blob/main/docs/apps-labs-site-foundation.md) for development ports, test commands and Cloudflare Pages build settings. The destination owns app CI. This source removal must be merged only after both destination app imports are merged and their builds are verified. Cloudflare production cutover remains separate operator work.

## Dependencies and Integrations

TaiRouter retains its independent local translation CLI under `scripts/localization`; destination static apps consume the destination repository's existing copy. No frontend imports authentication modules. Public URLs and Apache-2.0 licensing remain unchanged.

## References

- [Project boundary](project-labs-site.md)
- [TaiRouter public documentation requirements](apps-tairouter-docs-contract.md)
