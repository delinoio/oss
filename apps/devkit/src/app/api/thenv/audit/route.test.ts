import { NextRequest } from "next/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { GET } from "./route";
import { callThenvRpc } from "@/app/api/thenv/_lib/connect";
import { ThenvAuditEventType } from "@/apps/thenv/contracts";

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
  return new NextRequest(`http://localhost/api/thenv/audit?${search}`);
}

describe("thenv audit route", () => {
  beforeEach(() => {
    vi.mocked(callThenvRpc).mockReset();
  });

  it("forwards fromTime/toTime and other query params to the RPC request", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ events: [], nextCursor: "" });

    const response = await GET(
      buildRequest(
        "workspace=ws-a&project=proj-a&environment=dev&limit=25&cursor=1&actor=alice&eventType=AUDIT_EVENT_TYPE_PUSH&fromTime=2026-01-01T00:00:00Z&toTime=2026-01-31T23:59:59Z",
      ),
    );

    expect(response.status).toBe(200);
    expect(callThenvRpc).toHaveBeenCalledWith(
      "/thenv.v1.AuditService/ListAuditEvents",
      {
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        limit: 25,
        cursor: "1",
        actor: "alice",
        eventType: "AUDIT_EVENT_TYPE_PUSH",
        fromTime: "2026-01-01T00:00:00Z",
        toTime: "2026-01-31T23:59:59Z",
      },
    );
  });

  it("applies default values when optional params are not provided", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ events: [], nextCursor: "" });

    const response = await GET(
      buildRequest("workspace=ws-a&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(200);
    expect(callThenvRpc).toHaveBeenCalledWith(
      "/thenv.v1.AuditService/ListAuditEvents",
      {
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        limit: 20,
        cursor: "",
        actor: "",
        eventType: ThenvAuditEventType.Unspecified,
        fromTime: undefined,
        toTime: undefined,
      },
    );
  });

  it("returns 502 for unexpected errors", async () => {
    vi.mocked(callThenvRpc).mockRejectedValue(new Error("network down"));

    const response = await GET(
      buildRequest("workspace=ws-a&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "network down" });
  });

  it.each(["abc", "0", "101"])(
    "returns 400 for invalid limit value %s",
    async (limit) => {
      vi.mocked(callThenvRpc).mockResolvedValue({ events: [], nextCursor: "" });

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

  it("returns 400 for invalid eventType values", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ events: [], nextCursor: "" });

    const response = await GET(
      buildRequest(
        "workspace=ws-a&project=proj-a&environment=dev&eventType=AUDIT_EVENT_TYPE_UNKNOWN",
      ),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error:
        "eventType must be one of: AUDIT_EVENT_TYPE_UNSPECIFIED, AUDIT_EVENT_TYPE_PUSH, AUDIT_EVENT_TYPE_PULL, AUDIT_EVENT_TYPE_LIST, AUDIT_EVENT_TYPE_ROTATE, AUDIT_EVENT_TYPE_ACTIVATE, AUDIT_EVENT_TYPE_POLICY_UPDATE",
    });
  });

  it("returns 400 for invalid cursor values", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ events: [], nextCursor: "" });

    const response = await GET(
      buildRequest("workspace=ws-a&project=proj-a&environment=dev&cursor=abc"),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "cursor must be a non-negative integer",
    });
  });

  it("returns 400 for malformed scope query values", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ events: [], nextCursor: "" });

    const response = await GET(
      buildRequest("workspace=&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "workspace must be a non-empty string",
    });
  });
});
