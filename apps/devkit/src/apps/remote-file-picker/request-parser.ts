import {
  CompletionCallback,
  FileConstraints,
  ParseRemoteFilePickerRequestResult,
  PickerSource,
  RemoteFilePickerRequest,
  RequestValidationError,
  RequestValidationErrorCode,
  SignedUrlProvider,
  SignedUrlUploadTarget,
  UploadHttpMethod,
} from "@/apps/remote-file-picker/contracts";
import { decodeBase64Url } from "@/apps/remote-file-picker/encoding";

const PHASE_ONE_SOURCES = new Set<PickerSource>([
  PickerSource.LocalFile,
  PickerSource.MobileCamera,
]);
const ALLOWED_CALLBACK_PROTOCOLS = new Set(["http:", "https:"]);

function buildError(
  code: RequestValidationErrorCode,
  message: string,
): ParseRemoteFilePickerRequestResult {
  return {
    ok: false,
    error: {
      code,
      message,
    },
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isValidationError(value: unknown): value is RequestValidationError {
  return (
    isRecord(value) &&
    typeof value.code === "string" &&
    typeof value.message === "string"
  );
}

function parseNonEmptyString(
  rawValue: unknown,
  code: RequestValidationErrorCode,
  fieldPath: string,
): string | RequestValidationError {
  if (typeof rawValue !== "string") {
    return {
      code,
      message: `${fieldPath} must be a string.`,
    };
  }

  const value = rawValue.trim();
  if (!value) {
    return {
      code,
      message: `${fieldPath} is required.`,
    };
  }

  return value;
}

function parseStringMap(
  rawValue: unknown,
  fieldPath: string,
): Record<string, string> | RequestValidationError {
  if (rawValue === undefined) {
    return {};
  }

  if (!isRecord(rawValue)) {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: `${fieldPath} must be an object map of string values.`,
    };
  }

  const output: Record<string, string> = {};
  for (const [key, value] of Object.entries(rawValue)) {
    if (typeof value !== "string") {
      return {
        code: RequestValidationErrorCode.InvalidRequestPayload,
        message: `${fieldPath}.${key} must be a string.`,
      };
    }
    output[key] = value;
  }

  return output;
}

function parseUploadTarget(
  rawValue: unknown,
  now: Date,
): SignedUrlUploadTarget | RequestValidationError {
  if (!isRecord(rawValue)) {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: "uploadTarget must be an object.",
    };
  }

  const provider = parseNonEmptyString(
    rawValue.provider,
    RequestValidationErrorCode.InvalidRequestPayload,
    "uploadTarget.provider",
  );
  if (typeof provider !== "string") {
    return provider;
  }

  if (!Object.values(SignedUrlProvider).includes(provider as SignedUrlProvider)) {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: `uploadTarget.provider is unsupported: ${provider}.`,
    };
  }

  const method = parseNonEmptyString(
    rawValue.method,
    RequestValidationErrorCode.InvalidRequestPayload,
    "uploadTarget.method",
  );
  if (typeof method !== "string") {
    return method;
  }

  if (!Object.values(UploadHttpMethod).includes(method as UploadHttpMethod)) {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: `uploadTarget.method is unsupported: ${method}.`,
    };
  }

  const normalizedMethod = method as UploadHttpMethod;
  const normalizedProvider = provider as SignedUrlProvider;

  if (
    normalizedProvider === SignedUrlProvider.GcpCloudStorage &&
    normalizedMethod !== UploadHttpMethod.Put
  ) {
    return {
      code: RequestValidationErrorCode.UnsupportedProviderMethod,
      message: "GCP Cloud Storage signed URL uploads only support PUT in Phase 1.",
    };
  }

  const urlValue = parseNonEmptyString(
    rawValue.url,
    RequestValidationErrorCode.InvalidRequestPayload,
    "uploadTarget.url",
  );
  if (typeof urlValue !== "string") {
    return urlValue;
  }

  let parsedUploadUrl: URL;
  try {
    parsedUploadUrl = new URL(urlValue);
  } catch {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: "uploadTarget.url must be an absolute URL.",
    };
  }

  if (parsedUploadUrl.protocol !== "https:") {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: "uploadTarget.url must use https.",
    };
  }

  const expiresAt = parseNonEmptyString(
    rawValue.expiresAt,
    RequestValidationErrorCode.InvalidRequestPayload,
    "uploadTarget.expiresAt",
  );
  if (typeof expiresAt !== "string") {
    return expiresAt;
  }

  const expiresAtDate = new Date(expiresAt);
  if (Number.isNaN(expiresAtDate.getTime())) {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: "uploadTarget.expiresAt must be an ISO timestamp.",
    };
  }

  if (expiresAtDate.getTime() <= now.getTime()) {
    return {
      code: RequestValidationErrorCode.SignedUrlExpired,
      message: "Signed URL is expired. Request a new upload URL from the host app.",
    };
  }

  const headers = parseStringMap(rawValue.headers, "uploadTarget.headers");
  if (isValidationError(headers)) {
    return headers;
  }

  const formFields = parseStringMap(rawValue.formFields, "uploadTarget.formFields");
  if (isValidationError(formFields)) {
    return formFields;
  }

  const fileFieldNameRaw = rawValue.fileFieldName;
  if (fileFieldNameRaw !== undefined && typeof fileFieldNameRaw !== "string") {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: "uploadTarget.fileFieldName must be a string when provided.",
    };
  }

  return {
    provider: normalizedProvider,
    method: normalizedMethod,
    url: parsedUploadUrl.toString(),
    expiresAt: expiresAtDate.toISOString(),
    headers,
    formFields,
    fileFieldName: fileFieldNameRaw?.trim() || undefined,
  };
}

function parseAllowedSources(
  rawValue: unknown,
): PickerSource[] | RequestValidationError {
  if (!Array.isArray(rawValue)) {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: "allowedSources must be an array.",
    };
  }

  if (rawValue.length === 0) {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: "allowedSources must contain at least one source.",
    };
  }

  const uniqueSources = new Set<PickerSource>();
  for (const source of rawValue) {
    if (typeof source !== "string") {
      return {
        code: RequestValidationErrorCode.InvalidRequestPayload,
        message: "allowedSources entries must be strings.",
      };
    }

    if (!Object.values(PickerSource).includes(source as PickerSource)) {
      return {
        code: RequestValidationErrorCode.InvalidRequestPayload,
        message: `allowedSources contains unsupported value: ${source}.`,
      };
    }

    const normalizedSource = source as PickerSource;

    if (!PHASE_ONE_SOURCES.has(normalizedSource)) {
      return {
        code: RequestValidationErrorCode.UnsupportedSource,
        message:
          "google-drive and onedrive sources are deferred to Phase 2. Use local-file or mobile-camera.",
      };
    }

    uniqueSources.add(normalizedSource);
  }

  return Array.from(uniqueSources);
}

function parseFileConstraints(rawValue: unknown): FileConstraints | RequestValidationError {
  if (rawValue === undefined) {
    return {};
  }

  if (!isRecord(rawValue)) {
    return {
      code: RequestValidationErrorCode.InvalidRequestPayload,
      message: "fileConstraints must be an object.",
    };
  }

  const maxBytesRaw = rawValue.maxBytes;
  if (maxBytesRaw !== undefined) {
    if (!Number.isInteger(maxBytesRaw) || Number(maxBytesRaw) <= 0) {
      return {
        code: RequestValidationErrorCode.InvalidRequestPayload,
        message: "fileConstraints.maxBytes must be a positive integer.",
      };
    }
  }

  const allowedMimeTypesRaw = rawValue.allowedMimeTypes;
  if (allowedMimeTypesRaw !== undefined) {
    if (!Array.isArray(allowedMimeTypesRaw)) {
      return {
        code: RequestValidationErrorCode.InvalidRequestPayload,
        message: "fileConstraints.allowedMimeTypes must be an array of strings.",
      };
    }

    for (const mimeType of allowedMimeTypesRaw) {
      if (typeof mimeType !== "string" || !mimeType.trim()) {
        return {
          code: RequestValidationErrorCode.InvalidRequestPayload,
          message: "fileConstraints.allowedMimeTypes entries must be non-empty strings.",
        };
      }
    }
  }

  return {
    maxBytes: maxBytesRaw === undefined ? undefined : Number(maxBytesRaw),
    allowedMimeTypes:
      allowedMimeTypesRaw === undefined
        ? undefined
        : (allowedMimeTypesRaw as string[]).map((mimeType) => mimeType.trim()),
  };
}

function parseCallback(rawValue: unknown): CompletionCallback | RequestValidationError {
  if (!isRecord(rawValue)) {
    return {
      code: RequestValidationErrorCode.InvalidCallback,
      message: "callback must be an object.",
    };
  }

  const returnUrl = parseNonEmptyString(
    rawValue.returnUrl,
    RequestValidationErrorCode.InvalidCallback,
    "callback.returnUrl",
  );
  if (typeof returnUrl !== "string") {
    return returnUrl;
  }

  let parsedReturnUrl: URL;
  try {
    parsedReturnUrl = new URL(returnUrl);
  } catch {
    return {
      code: RequestValidationErrorCode.InvalidCallback,
      message: "callback.returnUrl must be an absolute URL.",
    };
  }

  if (!ALLOWED_CALLBACK_PROTOCOLS.has(parsedReturnUrl.protocol)) {
    return {
      code: RequestValidationErrorCode.InvalidCallback,
      message: "callback.returnUrl must use http or https.",
    };
  }

  const postMessageTargetOriginRaw = rawValue.postMessageTargetOrigin;
  if (postMessageTargetOriginRaw !== undefined && typeof postMessageTargetOriginRaw !== "string") {
    return {
      code: RequestValidationErrorCode.InvalidCallback,
      message: "callback.postMessageTargetOrigin must be a string when provided.",
    };
  }

  if (postMessageTargetOriginRaw === undefined) {
    return {
      returnUrl: parsedReturnUrl.toString(),
    };
  }

  const targetOrigin = postMessageTargetOriginRaw.trim();
  if (!targetOrigin) {
    return {
      code: RequestValidationErrorCode.InvalidCallback,
      message: "callback.postMessageTargetOrigin cannot be empty.",
    };
  }

  if (targetOrigin === "*") {
    return {
      returnUrl: parsedReturnUrl.toString(),
      postMessageTargetOrigin: targetOrigin,
    };
  }

  let parsedTargetOrigin: URL;
  try {
    parsedTargetOrigin = new URL(targetOrigin);
  } catch {
    return {
      code: RequestValidationErrorCode.InvalidCallback,
      message: "callback.postMessageTargetOrigin must be a valid absolute URL origin.",
    };
  }

  if (targetOrigin !== parsedTargetOrigin.origin) {
    return {
      code: RequestValidationErrorCode.InvalidCallback,
      message:
        "callback.postMessageTargetOrigin must include only protocol and host (no path/query).",
    };
  }

  return {
    returnUrl: parsedReturnUrl.toString(),
    postMessageTargetOrigin: parsedTargetOrigin.origin,
  };
}

function parseRequestPayload(
  rawPayload: unknown,
  now: Date,
): ParseRemoteFilePickerRequestResult {
  if (!isRecord(rawPayload)) {
    return buildError(
      RequestValidationErrorCode.InvalidRequestPayload,
      "request payload must be an object.",
    );
  }

  const requestId = parseNonEmptyString(
    rawPayload.requestId,
    RequestValidationErrorCode.InvalidRequestPayload,
    "requestId",
  );
  if (typeof requestId !== "string") {
    return { ok: false, error: requestId };
  }

  const uploadTarget = parseUploadTarget(rawPayload.uploadTarget, now);
  if (isValidationError(uploadTarget)) {
    return { ok: false, error: uploadTarget };
  }

  const allowedSources = parseAllowedSources(rawPayload.allowedSources);
  if (!Array.isArray(allowedSources)) {
    return { ok: false, error: allowedSources };
  }

  const fileConstraints = parseFileConstraints(rawPayload.fileConstraints);
  if (isValidationError(fileConstraints)) {
    return { ok: false, error: fileConstraints };
  }

  const callback = parseCallback(rawPayload.callback);
  if (isValidationError(callback)) {
    return { ok: false, error: callback };
  }

  const parsedRequest: RemoteFilePickerRequest = {
    requestId,
    uploadTarget,
    allowedSources,
    callback,
  };

  if (Object.keys(fileConstraints).length > 0) {
    parsedRequest.fileConstraints = fileConstraints;
  }

  return {
    ok: true,
    value: parsedRequest,
  };
}

export function parseRemoteFilePickerRequestFromSearch(
  search: string,
  now: Date,
): ParseRemoteFilePickerRequestResult {
  const params = new URLSearchParams(search);
  const encodedRequest = params.get("request")?.trim() ?? "";

  if (!encodedRequest) {
    return buildError(
      RequestValidationErrorCode.MissingRequestParam,
      "Missing request payload. Add the request query parameter.",
    );
  }

  let decodedRequest = "";
  try {
    decodedRequest = decodeBase64Url(encodedRequest);
  } catch {
    return buildError(
      RequestValidationErrorCode.InvalidRequestEncoding,
      "request payload must be valid base64url JSON.",
    );
  }

  let rawPayload: unknown;
  try {
    rawPayload = JSON.parse(decodedRequest);
  } catch {
    return buildError(
      RequestValidationErrorCode.InvalidRequestJson,
      "request payload JSON is invalid.",
    );
  }

  return parseRequestPayload(rawPayload, now);
}
