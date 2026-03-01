# Thenv Mini App

This directory hosts the Devkit mini app with the stable id `thenv`.

## Route Contract
- `/apps/thenv`

## Responsibilities
- Render metadata-only thenv management views.
- Show bundle version inventory and active version switch controls.
- Provide policy binding management and audit event browsing with outcome badges.
- Support audit time-range filtering (`fromTime`, `toTime`) in the metadata console.
- Support cursor-based "Load More" pagination for version inventory and audit history views.
- Keep unsaved policy draft bindings intact when applying or clearing audit filters.
- Never render or persist plaintext secret payloads in browser state.

## Integration
- Frontend calls Devkit local API routes under `/api/thenv/*`.
- Devkit API routes proxy to `servers/thenv` Connect RPC endpoints.

## References
- `docs/project-thenv.md`
- `docs/project-devkit.md`
