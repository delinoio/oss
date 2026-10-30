# Project: labs-docs

## Goal

The `labs-docs` application is maintained in `delinoio/apps`. This repository retains this ownership record for cross-project consumers.

## Project ID

`labs-docs`

## Domain Ownership Map

- External application: `https://github.com/delinoio/apps/tree/main/apps/labs-docs`.
- Project and domain contracts are maintained in the destination repository.

## Domain Contract Documents

- [Application boundary](apps-labs-docs-foundation.md)
- [Destination project contract](https://github.com/delinoio/apps/blob/main/docs/project-labs-docs.md)

## Cross-Domain Invariants

The canonical public origin remains `https://docs.labs.delino.io`. TaiRouter runtime, console, protocols and distributions remain in `delinoio/oss`. Builds, development commands, localization catalogs and application validation are owned by the destination. Production cutover requires separate Cloudflare account, domain and HTTP acceptance; source relocation does not establish deployment.

## Change Policy

Update this boundary record when external ownership or integration changes. Record migration validation and deployment limits in PR and CI records.
