# Project: devkit-remote-file-picker

## Goal
Provide a signed-URL upload mini app for local file/camera input with direct S3/GCS upload and result callback.

## Project ID
`devkit-remote-file-picker`

## Domain Ownership Map
- `apps/devkit/src/apps/remote-file-picker` (`web-app`)
- `servers/remote-file-picker` (`api-server`)

## Domain Contract Documents
- `docs/apps-devkit-remote-file-picker-foundation.md`

## Cross-Domain Invariants
- Mini app ID must remain `remote-file-picker`.
- Route contract must remain `/apps/remote-file-picker`.
- Web app and API server are active. Real S3/GCS storage adapter integration uses mock signed URLs; production adapter is deferred.

## Change Policy
- Update this index and `docs/apps-devkit-remote-file-picker-foundation.md` together for route or scaffold behavior changes.
- Keep `docs/project-devkit.md` and `docs/apps-devkit-foundation.md` synchronized when host registration changes.

## References
- `docs/project-devkit.md`
- `docs/apps-devkit-foundation.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
