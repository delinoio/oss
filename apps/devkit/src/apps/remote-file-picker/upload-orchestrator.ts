import {
  SignedUrlUploadResult,
  SignedUrlUploadTarget,
  UploadFailureCode,
  UploadHttpMethod,
  UploadProgress,
} from "@/apps/remote-file-picker/contracts";

interface UploadRequestLike {
  upload: {
    onprogress: ((event: ProgressEvent<EventTarget>) => void) | null;
  };
  onload: (() => void) | null;
  onerror: (() => void) | null;
  onabort: (() => void) | null;
  status: number;
  responseText: string;
  getAllResponseHeaders(): string;
  open(method: string, url: string): void;
  setRequestHeader(name: string, value: string): void;
  send(body: XMLHttpRequestBodyInit | null): void;
}

export interface UploadFileToSignedUrlParams {
  file: File;
  target: SignedUrlUploadTarget;
  onProgress?: (progress: UploadProgress) => void;
  createRequest?: () => UploadRequestLike;
}

function toErrorMessage(error: unknown, fallbackMessage: string): string {
  if (error instanceof Error && error.message) {
    return error.message;
  }
  if (typeof error === "string" && error) {
    return error;
  }
  return fallbackMessage;
}

function parseResponseHeaders(rawHeaders: string): Record<string, string> {
  return rawHeaders
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .reduce<Record<string, string>>((headers, line) => {
      const separatorIndex = line.indexOf(":");
      if (separatorIndex < 0) {
        return headers;
      }
      const key = line.slice(0, separatorIndex).trim().toLowerCase();
      const value = line.slice(separatorIndex + 1).trim();
      headers[key] = value;
      return headers;
    }, {});
}

function buildFailureResult(
  statusCode: number,
  code: UploadFailureCode,
  message: string,
  responseText?: string,
): SignedUrlUploadResult {
  return {
    ok: false,
    statusCode,
    code,
    message,
    responseText,
  };
}

function createBody(file: File, target: SignedUrlUploadTarget): XMLHttpRequestBodyInit {
  if (target.method === UploadHttpMethod.Put) {
    return file;
  }

  const payload = new FormData();
  const formFields = target.formFields ?? {};
  for (const [key, value] of Object.entries(formFields)) {
    payload.append(key, value);
  }

  payload.append(target.fileFieldName || "file", file);
  return payload;
}

export function uploadFileToSignedUrl({
  file,
  target,
  onProgress,
  createRequest,
}: UploadFileToSignedUrlParams): Promise<SignedUrlUploadResult> {
  const requestFactory = createRequest ?? (() => new XMLHttpRequest());

  return new Promise<SignedUrlUploadResult>((resolve) => {
    let isSettled = false;
    const settle = (result: SignedUrlUploadResult) => {
      if (isSettled) {
        return;
      }
      isSettled = true;
      resolve(result);
    };

    let request: UploadRequestLike;
    try {
      request = requestFactory();
    } catch (error) {
      settle(
        buildFailureResult(
          0,
          UploadFailureCode.NetworkError,
          `Failed to initialize upload request: ${toErrorMessage(
            error,
            "unknown initialization error",
          )}.`,
        ),
      );
      return;
    }

    request.upload.onprogress = (event) => {
      if (!onProgress) {
        return;
      }

      const totalBytes = event.total || file.size;
      const loadedBytes = event.loaded;
      const percent = totalBytes > 0 ? Math.round((loadedBytes / totalBytes) * 100) : 0;

      onProgress({
        loadedBytes,
        totalBytes,
        percent,
      });
    };

    request.onload = () => {
      const responseText = request.responseText || "";
      if (request.status >= 200 && request.status < 300) {
        settle({
          ok: true,
          statusCode: request.status,
          responseText,
          responseHeaders: parseResponseHeaders(request.getAllResponseHeaders()),
        });
        return;
      }

      settle(
        buildFailureResult(
          request.status,
          UploadFailureCode.HttpError,
          `Upload failed with status ${request.status}.`,
          responseText,
        ),
      );
    };

    request.onerror = () => {
      settle(
        buildFailureResult(
          request.status || 0,
          UploadFailureCode.NetworkError,
          "Network error while uploading file.",
          request.responseText,
        ),
      );
    };

    request.onabort = () => {
      settle(
        buildFailureResult(
          request.status || 0,
          UploadFailureCode.Aborted,
          "Upload was aborted.",
          request.responseText,
        ),
      );
    };

    try {
      request.open(target.method, target.url);
      for (const [headerName, headerValue] of Object.entries(target.headers ?? {})) {
        request.setRequestHeader(headerName, headerValue);
      }

      request.send(createBody(file, target));
    } catch (error) {
      settle(
        buildFailureResult(
          request.status || 0,
          UploadFailureCode.NetworkError,
          `Failed to start upload request: ${toErrorMessage(
            error,
            "unknown setup error",
          )}.`,
          request.responseText,
        ),
      );
    }
  });
}
