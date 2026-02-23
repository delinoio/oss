import { vi } from "vitest";

import {
  RemoteFilePickerCompletionStatus,
  SignedUrlProvider,
} from "@/apps/remote-file-picker/contracts";
import {
  deliverCompletionResult,
  REMOTE_FILE_PICKER_POST_MESSAGE_TYPE,
} from "@/apps/remote-file-picker/completion-bridge";
import { decodeBase64Url } from "@/apps/remote-file-picker/encoding";

function buildCompletionResult() {
  return {
    requestId: "req-43",
    provider: SignedUrlProvider.AwsS3,
    status: RemoteFilePickerCompletionStatus.Success,
    uploadedAt: "2026-02-23T10:10:00.000Z",
    file: {
      name: "image.png",
      sizeBytes: 1024,
      mimeType: "image/png",
    },
  };
}

describe("deliverCompletionResult", () => {
  it("uses redirect fallback after postMessage handoff", () => {
    const postMessage = vi.fn();
    const assign = vi.fn();

    const completion = buildCompletionResult();
    const result = deliverCompletionResult(
      completion,
      {
        returnUrl: "https://host.example/completion",
        postMessageTargetOrigin: "https://host.example",
      },
      {
        windowRef: {
          opener: {
            closed: false,
            postMessage,
          },
          location: {
            assign,
          },
        },
      },
    );

    expect(result).toEqual({
      delivered: true,
      channel: "redirect",
      message: "Completion delivered via redirect callback after postMessage handoff.",
    });
    expect(postMessage).toHaveBeenCalledWith(
      {
        type: REMOTE_FILE_PICKER_POST_MESSAGE_TYPE,
        payload: completion,
      },
      "https://host.example",
    );
    expect(assign).toHaveBeenCalledTimes(1);

    const redirectedUrl = new URL(assign.mock.calls[0][0] as string);
    const encodedResult = redirectedUrl.searchParams.get("result");
    expect(JSON.parse(decodeBase64Url(encodedResult || ""))).toEqual(completion);
  });

  it("falls back to redirect when opener is unavailable", () => {
    const assign = vi.fn();

    const completion = buildCompletionResult();
    const result = deliverCompletionResult(
      completion,
      {
        returnUrl: "https://host.example/completion?source=popup",
      },
      {
        windowRef: {
          opener: null,
          location: {
            assign,
          },
        },
      },
    );

    expect(result).toEqual({
      delivered: true,
      channel: "redirect",
      message: "Completion delivered via redirect callback.",
    });
    expect(assign).toHaveBeenCalledTimes(1);

    const redirectedUrl = new URL(assign.mock.calls[0][0] as string);
    expect(redirectedUrl.searchParams.get("source")).toBe("popup");

    const encodedResult = redirectedUrl.searchParams.get("result");
    expect(encodedResult).toBeTruthy();
    expect(JSON.parse(decodeBase64Url(encodedResult || ""))).toEqual(completion);
  });

  it("redirects when postMessage throws", () => {
    const postMessage = vi.fn(() => {
      throw new Error("cross-origin blocked");
    });
    const assign = vi.fn();

    const completion = buildCompletionResult();
    const result = deliverCompletionResult(
      completion,
      {
        returnUrl: "https://host.example/completion",
        postMessageTargetOrigin: "https://host.example",
      },
      {
        windowRef: {
          opener: {
            closed: false,
            postMessage,
          },
          location: {
            assign,
          },
        },
      },
    );

    expect(result).toEqual({
      delivered: true,
      channel: "redirect",
      message: "Completion delivered via redirect callback.",
    });
    expect(postMessage).toHaveBeenCalledTimes(1);
    expect(assign).toHaveBeenCalledTimes(1);
  });

  it("reports failure when redirect fallback cannot be completed", () => {
    const postMessage = vi.fn();
    const assign = vi.fn(() => {
      throw new Error("redirect blocked");
    });

    const result = deliverCompletionResult(
      buildCompletionResult(),
      {
        returnUrl: "https://host.example/completion",
        postMessageTargetOrigin: "https://host.example",
      },
      {
        windowRef: {
          opener: {
            closed: false,
            postMessage,
          },
          location: {
            assign,
          },
        },
      },
    );

    expect(result).toEqual({
      delivered: false,
      channel: "failed",
      message: "Completion delivery failed for both postMessage and redirect fallback.",
    });
    expect(postMessage).toHaveBeenCalledTimes(1);
    expect(assign).toHaveBeenCalledTimes(1);
  });
});
