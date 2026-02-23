import {
  SignedUrlProvider,
  UploadFailureCode,
  UploadHttpMethod,
} from "@/apps/remote-file-picker/contracts";
import { uploadFileToSignedUrl } from "@/apps/remote-file-picker/upload-orchestrator";

interface MockRequestOptions {
  trigger: "load" | "error" | "abort";
  status: number;
  responseText?: string;
  responseHeaders?: string;
  progressEvents?: Array<{ loaded: number; total: number }>;
  onSend?: (body: XMLHttpRequestBodyInit | null) => void;
  onOpen?: (method: string, url: string) => void;
  onSetRequestHeader?: (name: string, value: string) => void;
  openError?: Error;
  setRequestHeaderError?: Error;
  sendError?: Error;
}

function createMockRequest(options: MockRequestOptions) {
  return {
    upload: {
      onprogress: null as ((event: ProgressEvent<EventTarget>) => void) | null,
    },
    onload: null as (() => void) | null,
    onerror: null as (() => void) | null,
    onabort: null as (() => void) | null,
    status: options.status,
    responseText: options.responseText ?? "",
    getAllResponseHeaders() {
      return options.responseHeaders ?? "";
    },
    open(method: string, url: string) {
      if (options.openError) {
        throw options.openError;
      }
      options.onOpen?.(method, url);
    },
    setRequestHeader(name: string, value: string) {
      if (options.setRequestHeaderError) {
        throw options.setRequestHeaderError;
      }
      options.onSetRequestHeader?.(name, value);
    },
    send(body: XMLHttpRequestBodyInit | null) {
      if (options.sendError) {
        throw options.sendError;
      }
      options.onSend?.(body);
      for (const progressEvent of options.progressEvents ?? []) {
        this.upload.onprogress?.({
          loaded: progressEvent.loaded,
          total: progressEvent.total,
        } as ProgressEvent<EventTarget>);
      }

      if (options.trigger === "load") {
        this.onload?.();
      } else if (options.trigger === "error") {
        this.onerror?.();
      } else {
        this.onabort?.();
      }
    },
  };
}

describe("uploadFileToSignedUrl", () => {
  it("uploads with PUT and reports progress", async () => {
    const file = new File(["hello-world"], "hello.txt", {
      type: "text/plain",
    });

    const observedProgress: number[] = [];
    let observedMethod = "";
    let observedUrl = "";
    let observedBody: XMLHttpRequestBodyInit | null = null;
    const observedHeaders: Record<string, string> = {};

    const result = await uploadFileToSignedUrl({
      file,
      target: {
        provider: SignedUrlProvider.AwsS3,
        method: UploadHttpMethod.Put,
        url: "https://s3.example.com/object",
        expiresAt: "2026-02-23T23:00:00.000Z",
        headers: {
          "x-amz-acl": "private",
        },
      },
      onProgress: (progress) => {
        observedProgress.push(progress.percent);
      },
      createRequest: () =>
        createMockRequest({
          trigger: "load",
          status: 200,
          responseText: "ok",
          responseHeaders: "etag: abc123\n",
          progressEvents: [
            { loaded: 5, total: 10 },
            { loaded: 10, total: 10 },
          ],
          onOpen: (method, url) => {
            observedMethod = method;
            observedUrl = url;
          },
          onSetRequestHeader: (name, value) => {
            observedHeaders[name] = value;
          },
          onSend: (body) => {
            observedBody = body;
          },
        }),
    });

    expect(result).toEqual({
      ok: true,
      statusCode: 200,
      responseText: "ok",
      responseHeaders: {
        etag: "abc123",
      },
    });
    expect(observedMethod).toBe("PUT");
    expect(observedUrl).toBe("https://s3.example.com/object");
    expect(observedHeaders).toEqual({
      "x-amz-acl": "private",
    });
    expect(observedBody).toBe(file);
    expect(observedProgress).toEqual([50, 100]);
  });

  it("uploads with POST and appends signed form fields", async () => {
    const file = new File(["binary"], "photo.jpg", {
      type: "image/jpeg",
    });

    let observedBody: XMLHttpRequestBodyInit | null = null;

    const result = await uploadFileToSignedUrl({
      file,
      target: {
        provider: SignedUrlProvider.AwsS3,
        method: UploadHttpMethod.Post,
        url: "https://s3.example.com/upload",
        expiresAt: "2026-02-23T23:00:00.000Z",
        formFields: {
          key: "uploads/photo.jpg",
          policy: "abc-policy",
        },
        fileFieldName: "uploadFile",
      },
      createRequest: () =>
        createMockRequest({
          trigger: "load",
          status: 201,
          onSend: (body) => {
            observedBody = body;
          },
        }),
    });

    expect(result.ok).toBe(true);
    expect(observedBody).toBeInstanceOf(FormData);

    const formEntries = Array.from((observedBody as FormData).entries());
    expect(formEntries).toEqual(
      expect.arrayContaining([
        ["key", "uploads/photo.jpg"],
        ["policy", "abc-policy"],
        ["uploadFile", file],
      ]),
    );
  });

  it("returns failure details for non-2xx responses", async () => {
    const file = new File(["x"], "file.txt", { type: "text/plain" });

    const result = await uploadFileToSignedUrl({
      file,
      target: {
        provider: SignedUrlProvider.GcpCloudStorage,
        method: UploadHttpMethod.Put,
        url: "https://storage.googleapis.com/example/object",
        expiresAt: "2026-02-23T23:00:00.000Z",
      },
      createRequest: () =>
        createMockRequest({
          trigger: "load",
          status: 403,
          responseText: "forbidden",
        }),
    });

    expect(result).toEqual({
      ok: false,
      statusCode: 403,
      code: UploadFailureCode.HttpError,
      message: "Upload failed with status 403.",
      responseText: "forbidden",
    });
  });

  it("returns network failures when request errors", async () => {
    const file = new File(["x"], "file.txt", { type: "text/plain" });

    const result = await uploadFileToSignedUrl({
      file,
      target: {
        provider: SignedUrlProvider.GcpCloudStorage,
        method: UploadHttpMethod.Put,
        url: "https://storage.googleapis.com/example/object",
        expiresAt: "2026-02-23T23:00:00.000Z",
      },
      createRequest: () =>
        createMockRequest({
          trigger: "error",
          status: 0,
        }),
    });

    expect(result).toEqual({
      ok: false,
      statusCode: 0,
      code: UploadFailureCode.NetworkError,
      message: "Network error while uploading file.",
      responseText: "",
    });
  });

  it("returns failure instead of throwing when request creation fails", async () => {
    const file = new File(["x"], "file.txt", { type: "text/plain" });

    await expect(
      uploadFileToSignedUrl({
        file,
        target: {
          provider: SignedUrlProvider.GcpCloudStorage,
          method: UploadHttpMethod.Put,
          url: "https://storage.googleapis.com/example/object",
          expiresAt: "2026-02-23T23:00:00.000Z",
        },
        createRequest: () => {
          throw new Error("factory exploded");
        },
      }),
    ).resolves.toEqual({
      ok: false,
      statusCode: 0,
      code: UploadFailureCode.NetworkError,
      message: "Failed to initialize upload request: factory exploded.",
      responseText: undefined,
    });
  });

  it("returns failure when header setup throws synchronously", async () => {
    const file = new File(["x"], "file.txt", { type: "text/plain" });

    const result = await uploadFileToSignedUrl({
      file,
      target: {
        provider: SignedUrlProvider.AwsS3,
        method: UploadHttpMethod.Put,
        url: "https://s3.example.com/object",
        expiresAt: "2026-02-23T23:00:00.000Z",
        headers: {
          "invalid header": "bad-value",
        },
      },
      createRequest: () =>
        createMockRequest({
          trigger: "load",
          status: 200,
          setRequestHeaderError: new Error("invalid header name"),
        }),
    });

    expect(result).toEqual({
      ok: false,
      statusCode: 200,
      code: UploadFailureCode.NetworkError,
      message: "Failed to start upload request: invalid header name.",
      responseText: "",
    });
  });
});
