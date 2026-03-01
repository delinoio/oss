import { beforeEach, describe, expect, it, vi } from "vitest";

import { listVersions } from "@/apps/thenv/api-client";
import { DEFAULT_THENV_SCOPE } from "@/apps/thenv/contracts";

const fetchMock = vi.fn();

describe("thenv api-client listVersions", () => {
  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ versions: [], nextCursor: "" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
  });

  it("calls versions endpoint with scope params and no pagination by default", async () => {
    await listVersions(DEFAULT_THENV_SCOPE);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [requestUrl, requestInit] = fetchMock.mock.calls[0] as [
      string,
      RequestInit,
    ];
    const url = new URL(requestUrl, "http://localhost");

    expect(url.pathname).toBe("/api/thenv/versions");
    expect(url.searchParams.get("workspace")).toBe(DEFAULT_THENV_SCOPE.workspaceId);
    expect(url.searchParams.get("project")).toBe(DEFAULT_THENV_SCOPE.projectId);
    expect(url.searchParams.get("environment")).toBe(DEFAULT_THENV_SCOPE.environmentId);
    expect(url.searchParams.get("limit")).toBeNull();
    expect(url.searchParams.get("cursor")).toBeNull();
    expect(requestInit).toMatchObject({ cache: "no-store" });
  });

  it("calls versions endpoint with cursor and limit when provided", async () => {
    await listVersions(DEFAULT_THENV_SCOPE, { limit: 25, cursor: "1" });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [requestUrl] = fetchMock.mock.calls[0] as [string];
    const url = new URL(requestUrl, "http://localhost");

    expect(url.pathname).toBe("/api/thenv/versions");
    expect(url.searchParams.get("workspace")).toBe(DEFAULT_THENV_SCOPE.workspaceId);
    expect(url.searchParams.get("project")).toBe(DEFAULT_THENV_SCOPE.projectId);
    expect(url.searchParams.get("environment")).toBe(DEFAULT_THENV_SCOPE.environmentId);
    expect(url.searchParams.get("limit")).toBe("25");
    expect(url.searchParams.get("cursor")).toBe("1");
  });
});
