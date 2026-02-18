# Project: devkit-remote-file-picker

## Goal
`devkit-remote-file-picker` is a Devkit mini app for signed URL based image uploads.
It allows users to pick files from cloud providers, local folders, or a mobile camera flow, then upload directly from the client to AWS or GCP signed URLs.

## Path
- `apps/devkit/src/apps/remote-file-picker`

## Runtime and Language
- Next.js 16 mini app module (TypeScript)

## Users
- Product flows that need delegated file selection and upload
- End users who must upload images from Google Drive, OneDrive, local storage, or camera capture

## In Scope
- Entry flow from host app via redirect or popup with upload request payload.
- Source picker UI with Google Drive, OneDrive, local file picker, and mobile camera capture.
- Direct client-side upload to signed URLs.
- Signed URL type support for AWS S3 and GCP Cloud Storage.
- Return flow to host app after upload completion.
- Upload metadata options for file format conversion and size compression.

## Out of Scope
- Persistent media library management.
- Long-running server-side transcoding pipelines.
- Provider account admin or enterprise policy management.

## Architecture
- Entry request parser and contract validator.
- Source adapter layer for each picker source.
- Client-side preprocessing stage for format conversion and compression.
- Signed URL upload orchestrator with progress/error handling.
- Host-app return bridge (redirect/postMessage callback contract).

## Interfaces
Canonical mini app identifier:

```ts
enum MiniAppId {
  RemoteFilePicker = "remote-file-picker",
}
```

Signed URL target contract:

```ts
enum SignedUrlProvider {
  AwsS3 = "aws-s3",
  GcpCloudStorage = "gcp-cloud-storage",
}
```

Picker source contract:

```ts
enum PickerSource {
  GoogleDrive = "google-drive",
  OneDrive = "onedrive",
  LocalFile = "local-file",
  MobileCamera = "mobile-camera",
}
```

Upload metadata contract (conceptual):

```ts
enum OutputImageFormat {
  Original = "original",
  Jpeg = "jpeg",
  Png = "png",
  Webp = "webp",
}

interface UploadMetadata {
  outputFormat: OutputImageFormat;
  compressionQualityPercent?: number;
  maxWidthPx?: number;
  maxHeightPx?: number;
}
```

Route contract:
- `/apps/remote-file-picker`

Conceptual host request contract:
- Signed URL payload (URL, headers, provider type, expiry metadata)
- Allowed sources and file constraints
- Return URL or callback channel details
- Optional metadata transformation preferences

## Storage
- Ephemeral client state for active request, picker selection, and upload progress.
- No standalone mini app database.
- Sensitive request tokens must stay in memory-only state when possible.

## Security
- Validate signed URL origin and expiry before upload attempts.
- Restrict provider OAuth scopes to minimum read/upload requirements.
- Never log signed URL query secrets or provider access tokens.
- Enforce file type and size guards before upload.

## Logging
Required baseline logs:
- Entry request validation result
- Picker source selection and source adapter failures
- Preprocessing result and compression decision
- Upload request/result with correlation identifier
- Return flow success/failure

## Build and Test
Planned commands:
- `pnpm --filter devkit... test`
- Module-focused tests for picker source adapters and signed URL upload orchestration.

## Roadmap
- Phase 1: Local file and mobile camera upload to signed URLs.
- Phase 2: Google Drive and OneDrive source adapters.
- Phase 3: Metadata transform presets and reliability hardening.

## Open Questions
- OAuth authorization flow ownership between host app and mini app.
- Exact callback transport contract for popup mode vs redirect mode.
- Supported transformation limits for low-memory mobile browsers.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
