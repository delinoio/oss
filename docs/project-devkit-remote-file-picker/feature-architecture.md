# Feature: architecture

## Architecture
- Route renders `RemoteFilePickerApp` inside Devkit shell.
- Host request parser validates a base64url `request` query parameter and rejects invalid payloads with stable error codes.
- Host request parser enforces provider-specific signed URL host validation (`aws-s3` must target S3 hosts, `gcp-cloud-storage` must target Cloud Storage hosts).
- Source adapter layer supports local file picker and mobile camera capture.
- Upload orchestrator performs signed URL uploads with `XMLHttpRequest` to emit progress and converts synchronous setup failures into structured upload failure results.
- Completion bridge attempts `window.opener.postMessage` handoff first, then uses redirect callback delivery as the confirmed completion path.
- Client-side metadata transformation/compression is explicitly skipped in Phase 1 and logged as skipped.

