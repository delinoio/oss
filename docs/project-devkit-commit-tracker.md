# Project: devkit-commit-tracker

## Goal
Provide commit-level metric tracking with time-series visualization and PR comparison reporting.

## Project ID
`devkit-commit-tracker`

## Domain Ownership Map
- `apps/devkit/src/apps/commit-tracker` (`web-app`)
- `servers/commit-tracker` (`api-server`)

## Domain Contract Documents
- `docs/apps-devkit-commit-tracker-web-app-foundation.md`

## Cross-Domain Invariants
- Mini app ID must remain `commit-tracker`.
- Route contract must remain `/apps/commit-tracker`.
- Web app and API server are active. Collector CLI component is deferred.

## Change Policy
- Update this index and `docs/apps-devkit-commit-tracker-web-app-foundation.md` together for route or scaffold behavior changes.
- Keep `docs/project-devkit.md` and `docs/apps-devkit-foundation.md` synchronized when host registration changes.

## References
- `docs/project-devkit.md`
- `docs/apps-devkit-foundation.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
