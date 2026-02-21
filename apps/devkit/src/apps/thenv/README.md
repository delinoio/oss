# Thenv Mini App

This directory hosts the Devkit mini app with the stable id `thenv`.

## Route Contract
- `/apps/thenv`

## Responsibilities
- Render metadata-only thenv management views.
- Show bundle version inventory and active version switch controls.
- Provide policy binding management and audit event browsing.
- Never render or persist plaintext secret payloads in browser state.

## Integration
- Frontend calls Devkit local API routes under `/api/thenv/*`.
- Devkit API routes proxy to `servers/thenv` Connect RPC endpoints.

## References
- `docs/project-thenv.md`
- `docs/project-devkit.md`
