# Project: devkit

## Goal
Provide the Next.js micro-app host platform that serves mini apps with shared shell contracts and route conventions.

## Project ID
`devkit`

## Domain Ownership Map
- `apps/devkit`

## Domain Contract Documents
- `docs/apps-devkit-foundation.md`

## Cross-Domain Invariants
- Mini app identifiers must remain stable enum-style values.
- Mini app routes must keep the `/apps/<id>` contract.
- Shared shell behavior must remain separated from mini app business logic.

## Change Policy
- Update this index and `docs/apps-devkit-foundation.md` together whenever host routing, shell behavior, or mini app registration contracts change.
- Keep mini app project indexes aligned with this host contract.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
