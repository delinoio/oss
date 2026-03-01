import { beforeEach, describe, expect, it, vi } from "vitest";

import { POST } from "./route";
import { callThenvRpc } from "@/app/api/thenv/_lib/connect";

vi.mock("@/app/api/thenv/_lib/connect", async () => {
  const actual = await vi.importActual<typeof import("@/app/api/thenv/_lib/connect")>(
    "@/app/api/thenv/_lib/connect",
  );

  return {
    ...actual,
    callThenvRpc: vi.fn(),
  };
});

function buildPostRequest(body: unknown): Request {
  return new Request("http://localhost/api/thenv/activate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

describe("thenv activate route", () => {
  beforeEach(() => {
    vi.mocked(callThenvRpc).mockReset();
  });

  it("forwards valid activate requests", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({});

    const response = await POST(
      buildPostRequest({
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        bundleVersionId: " version-1 ",
      }),
    );

    expect(response.status).toBe(200);
    expect(callThenvRpc).toHaveBeenCalledWith(
      "/thenv.v1.BundleService/ActivateBundleVersion",
      {
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        bundleVersionId: "version-1",
      },
    );
  });

  it("returns 400 when bundleVersionId is missing", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({});

    const response = await POST(
      buildPostRequest({
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
      }),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "bundleVersionId is required",
    });
  });

  it("returns 400 for malformed scope values", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({});

    const response = await POST(
      buildPostRequest({
        scope: {
          workspaceId: "   ",
          projectId: "proj-a",
          environmentId: "dev",
        },
        bundleVersionId: "version-1",
      }),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "scope.workspaceId must be a non-empty string",
    });
  });

  it("returns 400 when request body is malformed JSON", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({});

    const response = await POST(buildPostRequest('{"scope":'));

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "request body must be valid JSON",
    });
  });

  it("returns 502 for unexpected errors", async () => {
    vi.mocked(callThenvRpc).mockRejectedValue(new Error("network down"));

    const response = await POST(
      buildPostRequest({
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        bundleVersionId: "version-1",
      }),
    );

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "network down" });
  });
});
