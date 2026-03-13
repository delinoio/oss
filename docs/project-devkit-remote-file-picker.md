# Project: devkit-remote-file-picker

## Goal
Provide the Devkit Remote File Picker mini app contract for remote repository path discovery and selection workflows.

## Project ID
`devkit-remote-file-picker`

## Domain Ownership Map
- `apps/devkit/src/apps/remote-file-picker`

## Domain Contract Documents
- `docs/apps-devkit-remote-file-picker-foundation.md`

## Cross-Domain Invariants
- Mini app ID must remain `remote-file-picker`.
- Route contract must remain `/apps/remote-file-picker` inside the Devkit host.
- Remote listing and selection UX must remain compatible with host shell integration.

## Change Policy
- Update this index and `docs/apps-devkit-remote-file-picker-foundation.md` together for behavior or interface updates.
- Keep `docs/project-devkit.md` aligned when host registration or route-level integration changes.

## References
- `docs/project-devkit.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
