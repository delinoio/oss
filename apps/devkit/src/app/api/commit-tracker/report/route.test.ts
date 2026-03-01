import { beforeEach, describe, expect, it, vi } from "vitest";

import { POST } from "./route";
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

function buildRequest(): Request {
  return new Request("http://localhost/api/commit-tracker/report", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      provider: "GIT_PROVIDER_KIND_GITHUB",
      repository: "acme/repo",
      pullRequest: 42,
      baseCommitSha: "base",
      headCommitSha: "head",
      environment: "ci",
      metricKeys: ["binary-size"],
    }),
  });
}

describe("commit-tracker report route", () => {
  beforeEach(() => {
    vi.mocked(callCommitTrackerRpc).mockReset();
  });

  it.each([400, 401, 412])(
    "propagates upstream status %s for RPC errors",
    async (status) => {
      vi.mocked(callCommitTrackerRpc).mockRejectedValue(
        new CommitTrackerApiError({
          status,
          procedure:
            "/committracker.v1.ProviderReportService/PublishPullRequestReport",
          message: `rpc error ${status}`,
          body: "{\"code\":\"rpc_error\"}",
        }),
      );

      const response = await POST(buildRequest());
      expect(response.status).toBe(status);
      await expect(response.json()).resolves.toEqual({
        error: `rpc error ${status}`,
      });
    },
  );

  it("returns 502 for unexpected errors", async () => {
    vi.mocked(callCommitTrackerRpc).mockRejectedValue(new Error("network down"));

    const response = await POST(buildRequest());
    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "network down" });
  });
});
