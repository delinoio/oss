# Remote File Picker Mini App

This directory hosts the Devkit mini app with the stable id `remote-file-picker`.

## Route Contract
- `/apps/remote-file-picker`

## Implemented in Phase 1
- Parse and validate host upload requests from `request=<base64url-json>` query payloads.
- Validate signed URL hosts against declared providers before upload.
- Support `local-file` and `mobile-camera` source selection.
- Upload selected files directly to AWS S3 and GCP Cloud Storage signed URLs.
- Render upload progress and clear validation/upload errors.
- Attempt completion handoff via `postMessage` and finalize delivery through redirect callback fallback.

## Deferred to Phase 2
- Google Drive adapter
- OneDrive adapter
- Client-side image transformation presets

## References
- `docs/project-devkit-remote-file-picker.md`
- `docs/project-devkit.md`
