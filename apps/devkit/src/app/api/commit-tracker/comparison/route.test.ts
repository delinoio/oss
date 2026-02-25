import { NextRequest } from "next/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { GET } from "./route";
import {
  CommitTrackerApiError,
  callCommitTrackerRpc,
} from "@/app/api/commit-tracker/_lib/connect";

vi.mock("@/app/api/commit-tracker/_lib/connect", () => {
  class CommitTrackerApiError extends Error {
    readonly status: number;
    readonly procedure: string;
    readonly body: string;

    constructor(params: {
      status: number;
      procedure: string;
      message: string;
      body: string;
    }) {
      super(params.message);
      this.name = "CommitTrackerApiError";
      this.status = params.status;
      this.procedure = params.procedure;
      this.body = params.body;
    }
  }

  return {
    CommitTrackerApiError,
    callCommitTrackerRpc: vi.fn(),
  };
});

function buildRequest(): NextRequest {
  return new NextRequest(
    "http://localhost/api/commit-tracker/comparison?provider=GIT_PROVIDER_KIND_GITHUB&repository=acme/repo&baseCommitSha=base&headCommitSha=head&environment=ci",
  );
}

describe("commit-tracker comparison route", () => {
  beforeEach(() => {
    vi.mocked(callCommitTrackerRpc).mockReset();
  });

  it("propagates upstream status for RPC errors", async () => {
    vi.mocked(callCommitTrackerRpc).mockRejectedValue(
      new CommitTrackerApiError({
        status: 401,
        procedure: "/committracker.v1.MetricQueryService/GetPullRequestComparison",
        message: "unauthenticated",
        body: "{\"code\":\"unauthenticated\"}",
      }),
    );

    const response = await GET(buildRequest());
    expect(response.status).toBe(401);
    await expect(response.json()).resolves.toEqual({
      error: "unauthenticated",
    });
  });

  it("returns 502 for unexpected errors", async () => {
    vi.mocked(callCommitTrackerRpc).mockRejectedValue(new Error("network down"));

    const response = await GET(buildRequest());
    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "network down" });
  });
});
