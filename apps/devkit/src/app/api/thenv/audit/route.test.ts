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
        "workspace=ws-a&project=proj-a&environment=dev&limit=25&cursor=cursor-1&actor=alice&eventType=AUDIT_EVENT_TYPE_PUSH&fromTime=2026-01-01T00:00:00Z&toTime=2026-01-31T23:59:59Z",
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
        cursor: "cursor-1",
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
});
