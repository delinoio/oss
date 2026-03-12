# Feature: interfaces

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

