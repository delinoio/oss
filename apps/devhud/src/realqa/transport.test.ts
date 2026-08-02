import { create } from "@bufbuild/protobuf";
import {
  GetPresetRequestSchema,
  GetPresetResponseSchema,
} from "@delinoio/devhud-realqa-connect/devhud-realqa/v1/preset_pb";
import { invoke } from "@tauri-apps/api/core";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  RealQaProcedure,
  bytesToBase64,
  invokeRealQaProcedure,
  openRealQaGitHubAuthorization,
  putRealQaImage,
} from "./transport";

vi.mock("@tauri-apps/api/core", () => ({ invoke: vi.fn() }));

const invokeMock = vi.mocked(invoke);

describe("closed RealQA native transport", () => {
  beforeEach(() => invokeMock.mockReset());

  it("sends only a closed procedure and protobuf body", async () => {
    invokeMock.mockResolvedValueOnce({
      bodyBase64: bytesToBase64(
        new Uint8Array([10, 0]),
      ),
    });
    const request = create(GetPresetRequestSchema, {
      presetId: { value: "01900000-0000-7000-8000-000000000757" },
    });

    await invokeRealQaProcedure(
      RealQaProcedure.GetPreset,
      GetPresetRequestSchema,
      GetPresetResponseSchema,
      request,
    );

    expect(invokeMock).toHaveBeenCalledWith("realqa_connect", {
      request: {
        procedure: "get-preset",
        bodyBase64: expect.any(String),
      },
    });
    expect(JSON.stringify(invokeMock.mock.calls[0])).not.toContain(
      "realqa.deli.dev",
    );
  });

  it("keeps signed PUT authority separate and sends no custom headers", async () => {
    invokeMock.mockResolvedValueOnce(undefined);
    await putRealQaImage({
      signedPutUrl:
        "https://assets.realqa.deli.dev/uploads/opaque?X-Amz-Signature=fixture",
      contentType: "image/png",
      sha256: "a".repeat(64),
      body: new Uint8Array([1, 2, 3]),
    });

    expect(invokeMock).toHaveBeenCalledWith("realqa_signed_put", {
      request: {
        signedPutUrl: expect.stringContaining("/uploads/opaque"),
        contentType: "image/png",
        sha256: "a".repeat(64),
        bodyBase64: "AQID",
      },
    });
  });

  it("uses the typed native GitHub authorization handoff", async () => {
    invokeMock.mockResolvedValueOnce(undefined);
    const target =
      "https://github.com/apps/fixture-realqa/installations/new?state=abcdefghijklmnopqrstuvwxyz123456";

    await openRealQaGitHubAuthorization(target);

    expect(invokeMock).toHaveBeenCalledWith(
      "realqa_open_github_authorization",
      { target },
    );
  });
});
