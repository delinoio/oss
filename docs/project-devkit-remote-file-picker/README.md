# Project: devkit-remote-file-picker

## Documentation Layout
- Canonical entrypoint for this project: docs/project-devkit-remote-file-picker/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`devkit-remote-file-picker` is a Devkit mini app for signed URL based image uploads.
Phase 1 is implemented for local file and mobile camera sources with direct client uploads to AWS S3 and GCP Cloud Storage signed URLs.


## Path
- `apps/devkit/src/apps/remote-file-picker`
- Route implementation: `apps/devkit/src/app/apps/remote-file-picker/page.tsx`


## Runtime and Language
- Next.js 16 mini app module (TypeScript)


## Users
- Product flows that need delegated file selection and upload
- End users who must upload images from local storage or mobile camera capture


## In Scope
- Entry flow from host app with base64url-encoded request payload in query params.
- Source picker UI for `local-file` and `mobile-camera`.
- Direct client-side upload to signed URLs.
- Signed URL type support for AWS S3 (`PUT`/`POST`) and GCP Cloud Storage (`PUT`).
- Upload progress and error UX.
- Return flow to host app after upload completion via postMessage or redirect fallback.


## Out of Scope
- Google Drive and OneDrive adapters (deferred to Phase 2).
- Persistent media library management.
- Long-running server-side transcoding pipelines.
- Provider account admin or enterprise policy management.


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
