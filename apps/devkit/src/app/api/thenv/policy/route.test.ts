import { NextRequest } from "next/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { GET, PUT } from "./route";
import { callThenvRpc } from "@/app/api/thenv/_lib/connect";
import { ThenvRole } from "@/apps/thenv/contracts";

vi.mock("@/app/api/thenv/_lib/connect", async () => {
  const actual = await vi.importActual<typeof import("@/app/api/thenv/_lib/connect")>(
    "@/app/api/thenv/_lib/connect",
  );

  return {
    ...actual,
    callThenvRpc: vi.fn(),
  };
});

function buildGetRequest(search: string): NextRequest {
  return new NextRequest(`http://localhost/api/thenv/policy?${search}`);
}

function buildPutRequest(body: unknown): Request {
  return new Request("http://localhost/api/thenv/policy", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

describe("thenv policy route", () => {
  beforeEach(() => {
    vi.mocked(callThenvRpc).mockReset();
  });

  it("forwards validated scope for GET policy requests", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ bindings: [], policyRevision: 1 });

    const response = await GET(
      buildGetRequest("workspace=ws-a&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(200);
    expect(callThenvRpc).toHaveBeenCalledWith(
      "/thenv.v1.PolicyService/GetPolicy",
      {
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
      },
    );
  });

  it("returns 400 for malformed scope on GET requests", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ bindings: [], policyRevision: 1 });

    const response = await GET(
      buildGetRequest("workspace=&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "workspace must be a non-empty string",
    });
  });

  it("forwards validated bindings and scope for PUT policy requests", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ bindings: [], policyRevision: 2 });

    const response = await PUT(
      buildPutRequest({
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        bindings: [
          { subject: " alice ", role: ThenvRole.Admin },
          { subject: "bob", role: ThenvRole.Reader },
        ],
      }),
    );

    expect(response.status).toBe(200);
    expect(callThenvRpc).toHaveBeenCalledWith(
      "/thenv.v1.PolicyService/SetPolicy",
      {
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        bindings: [
          { subject: "alice", role: ThenvRole.Admin },
          { subject: "bob", role: ThenvRole.Reader },
        ],
      },
    );
  });

  it("returns 400 for invalid binding roles", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ bindings: [], policyRevision: 2 });

    const response = await PUT(
      buildPutRequest({
        bindings: [{ subject: "alice", role: "ROLE_OWNER" }],
      }),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error:
        "bindings[0].role must be one of: ROLE_READER, ROLE_WRITER, ROLE_ADMIN",
    });
  });

  it("returns 400 for empty binding subjects", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ bindings: [], policyRevision: 2 });

    const response = await PUT(
      buildPutRequest({
        bindings: [{ subject: "   ", role: ThenvRole.Writer }],
      }),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "bindings[0].subject must be a non-empty string",
    });
  });

  it("returns 400 when bindings is not an array", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ bindings: [], policyRevision: 2 });

    const response = await PUT(
      buildPutRequest({
        bindings: { subject: "alice", role: ThenvRole.Admin },
      }),
    );

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "bindings must be an array",
    });
  });

  it("returns 400 when request body is malformed JSON", async () => {
    vi.mocked(callThenvRpc).mockResolvedValue({ bindings: [], policyRevision: 2 });

    const response = await PUT(buildPutRequest('{"scope":'));

    expect(response.status).toBe(400);
    expect(callThenvRpc).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toEqual({
      error: "request body must be valid JSON",
    });
  });

  it("returns 502 when upstream payload parsing fails", async () => {
    vi.mocked(callThenvRpc).mockRejectedValue(
      new SyntaxError("upstream payload parse failed"),
    );

    const response = await PUT(
      buildPutRequest({
        scope: {
          workspaceId: "ws-a",
          projectId: "proj-a",
          environmentId: "dev",
        },
        bindings: [{ subject: "alice", role: ThenvRole.Admin }],
      }),
    );

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({
      error: "upstream payload parse failed",
    });
  });

  it("returns 502 for unexpected errors", async () => {
    vi.mocked(callThenvRpc).mockRejectedValue(new Error("network down"));

    const response = await GET(
      buildGetRequest("workspace=ws-a&project=proj-a&environment=dev"),
    );

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "network down" });
  });
});
