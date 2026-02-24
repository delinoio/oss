import { NextResponse } from "next/server";

import { callCommitTrackerRpc } from "@/app/api/commit-tracker/_lib/connect";

interface PublishPullRequestReportBody {
  provider?: string;
  repository?: string;
  pullRequest?: number;
  baseCommitSha?: string;
  headCommitSha?: string;
  environment?: string;
  metricKeys?: string[];
}

export async function POST(request: Request) {
  try {
    const body = (await request.json()) as PublishPullRequestReportBody;

    if (!body.pullRequest || body.pullRequest <= 0) {
      return NextResponse.json(
        { error: "pullRequest must be greater than zero" },
        { status: 400 },
      );
    }

    const response = await callCommitTrackerRpc<object, unknown>(
      "/committracker.v1.ProviderReportService/PublishPullRequestReport",
      {
        provider: body.provider ?? "GIT_PROVIDER_KIND_GITHUB",
        repository: body.repository ?? "",
        pullRequest: body.pullRequest,
        baseCommitSha: body.baseCommitSha ?? "",
        headCommitSha: body.headCommitSha ?? "",
        environment: body.environment ?? "",
        metricKeys: (body.metricKeys ?? [])
          .map((value) => value.trim())
          .filter((value) => value.length > 0),
      },
    );

    return NextResponse.json(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
