# Project: devkit-remote-file-picker

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

## Architecture
- Route renders `RemoteFilePickerApp` inside Devkit shell.
- Host request parser validates a base64url `request` query parameter and rejects invalid payloads with stable error codes.
- Source adapter layer supports local file picker and mobile camera capture.
- Upload orchestrator performs signed URL uploads with `XMLHttpRequest` to emit progress and converts synchronous setup failures into structured upload failure results.
- Completion bridge delivers results through `window.opener.postMessage` first, with redirect callback fallback.
- Client-side metadata transformation/compression is explicitly skipped in Phase 1 and logged as skipped.

## Interfaces
Canonical mini app identifier:

```ts
enum MiniAppId {
  RemoteFilePicker = "remote-file-picker",
}
```

Signed URL provider contract:

```ts
enum SignedUrlProvider {
  AwsS3 = "aws-s3",
  GcpCloudStorage = "gcp-cloud-storage",
}
```

Picker source contract:

```ts
enum PickerSource {
  LocalFile = "local-file",
  MobileCamera = "mobile-camera",
  GoogleDrive = "google-drive",
  OneDrive = "onedrive",
}
```

Upload target contract:

```ts
enum UploadHttpMethod {
  Put = "PUT",
  Post = "POST",
}

interface SignedUrlUploadTarget {
  provider: SignedUrlProvider;
  method: UploadHttpMethod;
  url: string;
  expiresAt: string;
  headers?: Record<string, string>;
  formFields?: Record<string, string>;
  fileFieldName?: string;
}
```

Host request contract:

```ts
interface RemoteFilePickerRequest {
  requestId: string;
  uploadTarget: SignedUrlUploadTarget;
  allowedSources: PickerSource[];
  fileConstraints?: {
    maxBytes?: number;
    allowedMimeTypes?: string[];
  };
  callback: {
    returnUrl: string;
    postMessageTargetOrigin?: string;
  };
}
```

Completion contract:

```ts
enum RemoteFilePickerCompletionStatus {
  Success = "success",
  Failure = "failure",
}

interface RemoteFilePickerCompletionResult {
  requestId: string;
  provider: SignedUrlProvider;
  status: RemoteFilePickerCompletionStatus;
  uploadedAt: string;
  file?: {
    name: string;
    sizeBytes: number;
    mimeType: string;
  };
  error?: {
    code: string;
    message: string;
    httpStatus?: number;
  };
}
```

Route contract:
- `/apps/remote-file-picker`
- Entry payload transport: query param `request=<base64url-json>`.

## Storage
- Ephemeral client state for active request, picker selection, upload progress, and completion status.
- No standalone mini app database.
- Sensitive request tokens stay in memory and are never persisted.

## Security
- Validate signed URL origin/protocol and expiry before upload attempts.
- Enforce provider/method compatibility (`gcp-cloud-storage` only `PUT` in Phase 1).
- Validate callback return URLs with explicit protocol allowlist (`http`/`https`) before redirect fallback.
- Never log signed URL query secrets or provider access tokens.
- Enforce file type and size constraints before upload.

## Logging
Required baseline logs:
- Entry request validation result
- Picker source selection and source adapter failures
- Preprocessing decision (`skipped` in Phase 1)
- Upload request/result with request correlation id
- Return flow success/failure

## Build and Test
Current commands:
- `pnpm --filter devkit... test`
- Module-focused tests:
  - request parser validation
  - upload orchestrator success/failure
  - completion bridge channel fallback

## Roadmap
- Phase 1: Implemented local file and mobile camera upload to signed URLs.
- Phase 2: Google Drive and OneDrive source adapters.
- Phase 3: Metadata transform presets and reliability hardening.

## Open Questions
- OAuth authorization flow ownership between host app and mini app for Phase 2 cloud providers.
- Supported transformation limits for low-memory mobile browsers once preprocessing is enabled.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
