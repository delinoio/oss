import { NextRequest } from "next/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { GET } from "./route";
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

function buildRequest(search: string): NextRequest {
  return new NextRequest(`http://localhost/api/thenv/versions?${search}`);
}

describe("thenv versions route", () => {
  beforeEach(() => {
    vi.mocked(callThenvRpc).mockReset();
  });

  it("forwards validated scope and pagination values", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ versions: [], nextCursor: "2" });

    const response = await GET(
      buildRequest("workspace=ws-a&project=proj-a&environment=dev&limit=25&cursor=1"),
    );

    expect(response.status).toBe(200);
    expect(callThenvRpc).toHaveBeenCalledWith(
      "/thenv.v1.BundleService/ListBundleVersions",
      {
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        limit: 25,
        cursor: "1",
      },
    );
  });

  it("applies default pagination values when params are omitted", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ versions: [], nextCursor: "" });

    const response = await GET(
      buildRequest("workspace=ws-a&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(200);
    expect(callThenvRpc).toHaveBeenCalledWith(
      "/thenv.v1.BundleService/ListBundleVersions",
      {
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        limit: 20,
        cursor: "",
      },
    );
  });

  it.each(["abc", "0", "101"])(
    "returns 400 for invalid limit value %s",
    async (limit) => {
      vi.mocked(callThenvRpc).mockResolvedValue({ versions: [], nextCursor: "" });

      const response = await GET(
        buildRequest(`workspace=ws-a&project=proj-a&environment=dev&limit=${limit}`),
      );

      expect(response.status).toBe(400);
      expect(callThenvRpc).not.toHaveBeenCalled();
      await expect(response.json()).resolves.toEqual({
        error: "limit must be an integer between 1 and 100",
      });
    },
  );

  it("returns 400 for invalid cursor values", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ versions: [], nextCursor: "" });

    const response = await GET(
      buildRequest("workspace=ws-a&project=proj-a&environment=dev&cursor=-1"),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "cursor must be a non-negative integer",
    });
  });

  it("returns 400 for malformed scope values", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ versions: [], nextCursor: "" });

    const response = await GET(
      buildRequest("workspace=&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "workspace must be a non-empty string",
    });
  });

  it("returns 502 for unexpected errors", async () => {
    vi.mocked(callThenvRpc).mockRejectedValue(new Error("network down"));

    const response = await GET(
      buildRequest("workspace=ws-a&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "network down" });
  });
});
