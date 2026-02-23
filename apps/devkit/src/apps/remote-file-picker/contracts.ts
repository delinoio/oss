export enum SignedUrlProvider {
  AwsS3 = "aws-s3",
  GcpCloudStorage = "gcp-cloud-storage",
}

export enum PickerSource {
  LocalFile = "local-file",
  MobileCamera = "mobile-camera",
  GoogleDrive = "google-drive",
  OneDrive = "onedrive",
}

export enum UploadHttpMethod {
  Put = "PUT",
  Post = "POST",
}

export interface SignedUrlUploadTarget {
  provider: SignedUrlProvider;
  method: UploadHttpMethod;
  url: string;
  expiresAt: string;
  headers?: Record<string, string>;
  formFields?: Record<string, string>;
  fileFieldName?: string;
}

export interface FileConstraints {
  maxBytes?: number;
  allowedMimeTypes?: string[];
}

export interface CompletionCallback {
  returnUrl: string;
  postMessageTargetOrigin?: string;
}

export interface RemoteFilePickerRequest {
  requestId: string;
  uploadTarget: SignedUrlUploadTarget;
  allowedSources: PickerSource[];
  fileConstraints?: FileConstraints;
  callback: CompletionCallback;
}

export enum RemoteFilePickerCompletionStatus {
  Success = "success",
  Failure = "failure",
}

export interface UploadedFileDetails {
  name: string;
  sizeBytes: number;
  mimeType: string;
}

export interface RemoteFilePickerCompletionError {
  code: string;
  message: string;
  httpStatus?: number;
}

export interface RemoteFilePickerCompletionResult {
  requestId: string;
  provider: SignedUrlProvider;
  status: RemoteFilePickerCompletionStatus;
  uploadedAt: string;
  file?: UploadedFileDetails;
  error?: RemoteFilePickerCompletionError;
}

export enum RequestValidationErrorCode {
  MissingRequestParam = "missing-request-param",
  InvalidRequestEncoding = "invalid-request-encoding",
  InvalidRequestJson = "invalid-request-json",
  InvalidRequestPayload = "invalid-request-payload",
  InvalidSignedUrlHost = "invalid-signed-url-host",
  UnsupportedSource = "unsupported-source",
  SignedUrlExpired = "signed-url-expired",
  UnsupportedProviderMethod = "unsupported-provider-method",
  InvalidCallback = "invalid-callback",
}

export interface RequestValidationError {
  code: RequestValidationErrorCode;
  message: string;
}

export type ParseRemoteFilePickerRequestResult =
  | { ok: true; value: RemoteFilePickerRequest }
  | { ok: false; error: RequestValidationError };

export enum CompletionDeliveryChannel {
  PostMessage = "post-message",
  Redirect = "redirect",
  Failed = "failed",
}

export interface CompletionDeliveryResult {
  delivered: boolean;
  channel: CompletionDeliveryChannel;
  message: string;
}

export interface UploadProgress {
  loadedBytes: number;
  totalBytes: number;
  percent: number;
}

export enum UploadFailureCode {
  HttpError = "http-error",
  NetworkError = "network-error",
  Aborted = "aborted",
}

export type SignedUrlUploadResult =
  | {
      ok: true;
      statusCode: number;
      responseText: string;
      responseHeaders: Record<string, string>;
    }
  | {
      ok: false;
      statusCode: number;
      code: UploadFailureCode;
      message: string;
      responseText?: string;
    };
