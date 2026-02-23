import {
  PickerSource,
  RequestValidationErrorCode,
  SignedUrlProvider,
  UploadHttpMethod,
} from "@/apps/remote-file-picker/contracts";
import { encodeBase64Url, encodeJsonBase64Url } from "@/apps/remote-file-picker/encoding";
import { parseRemoteFilePickerRequestFromSearch } from "@/apps/remote-file-picker/request-parser";

const NOW = new Date("2026-02-23T10:00:00.000Z");

function buildValidRequestPayload() {
  return {
    requestId: "req-43",
    uploadTarget: {
      provider: SignedUrlProvider.AwsS3,
      method: UploadHttpMethod.Put,
      url: "https://bucket.s3.amazonaws.com/path/image.png?signature=redacted",
      expiresAt: "2026-02-23T11:00:00.000Z",
      headers: {
        "x-amz-acl": "private",
      },
    },
    allowedSources: [PickerSource.LocalFile, PickerSource.MobileCamera],
    fileConstraints: {
      maxBytes: 1024 * 1024,
      allowedMimeTypes: ["image/png", "image/jpeg"],
    },
    callback: {
      returnUrl: "https://host.example/upload/complete",
      postMessageTargetOrigin: "https://host.example",
    },
  };
}

describe("parseRemoteFilePickerRequestFromSearch", () => {
  it("parses a valid request payload", () => {
    const search = `?request=${encodeJsonBase64Url(buildValidRequestPayload())}`;

    const result = parseRemoteFilePickerRequestFromSearch(search, NOW);

    expect(result.ok).toBe(true);
    if (!result.ok) {
      return;
    }

    expect(result.value.requestId).toBe("req-43");
    expect(result.value.uploadTarget.provider).toBe(SignedUrlProvider.AwsS3);
    expect(result.value.allowedSources).toEqual([
      PickerSource.LocalFile,
      PickerSource.MobileCamera,
    ]);
    expect(result.value.callback.returnUrl).toBe("https://host.example/upload/complete");
  });

  it("rejects missing request payload", () => {
    const result = parseRemoteFilePickerRequestFromSearch("", NOW);

    expect(result).toEqual({
      ok: false,
      error: {
        code: RequestValidationErrorCode.MissingRequestParam,
        message: "Missing request payload. Add the request query parameter.",
      },
    });
  });

  it("rejects invalid base64url payload", () => {
    const result = parseRemoteFilePickerRequestFromSearch("?request=@@@", NOW);

    expect(result).toEqual({
      ok: false,
      error: {
        code: RequestValidationErrorCode.InvalidRequestEncoding,
        message: "request payload must be valid base64url JSON.",
      },
    });
  });

  it("rejects invalid JSON payload", () => {
    const encodedInvalidJson = encodeBase64Url("not-json");

    const result = parseRemoteFilePickerRequestFromSearch(
      `?request=${encodedInvalidJson}`,
      NOW,
    );

    expect(result).toEqual({
      ok: false,
      error: {
        code: RequestValidationErrorCode.InvalidRequestJson,
        message: "request payload JSON is invalid.",
      },
    });
  });

  it("rejects missing required fields", () => {
    const payload = buildValidRequestPayload();
    payload.requestId = "";

    const result = parseRemoteFilePickerRequestFromSearch(
      `?request=${encodeJsonBase64Url(payload)}`,
      NOW,
    );

    expect(result).toEqual({
      ok: false,
      error: {
        code: RequestValidationErrorCode.InvalidRequestPayload,
        message: "requestId is required.",
      },
    });
  });

  it("rejects unsupported cloud source adapters for phase 1", () => {
    const payload = buildValidRequestPayload();
    payload.allowedSources = [PickerSource.LocalFile, PickerSource.GoogleDrive];

    const result = parseRemoteFilePickerRequestFromSearch(
      `?request=${encodeJsonBase64Url(payload)}`,
      NOW,
    );

    expect(result).toEqual({
      ok: false,
      error: {
        code: RequestValidationErrorCode.UnsupportedSource,
        message:
          "google-drive and onedrive sources are deferred to Phase 2. Use local-file or mobile-camera.",
      },
    });
  });

  it("rejects expired signed URLs", () => {
    const payload = buildValidRequestPayload();
    payload.uploadTarget.expiresAt = "2026-02-23T09:59:59.000Z";

    const result = parseRemoteFilePickerRequestFromSearch(
      `?request=${encodeJsonBase64Url(payload)}`,
      NOW,
    );

    expect(result).toEqual({
      ok: false,
      error: {
        code: RequestValidationErrorCode.SignedUrlExpired,
        message: "Signed URL is expired. Request a new upload URL from the host app.",
      },
    });
  });

  it("rejects invalid callback URLs", () => {
    const payload = buildValidRequestPayload();
    payload.callback.returnUrl = "/host/complete";

    const result = parseRemoteFilePickerRequestFromSearch(
      `?request=${encodeJsonBase64Url(payload)}`,
      NOW,
    );

    expect(result).toEqual({
      ok: false,
      error: {
        code: RequestValidationErrorCode.InvalidCallback,
        message: "callback.returnUrl must be an absolute URL.",
      },
    });
  });
});
