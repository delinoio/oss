import { NextRequest, NextResponse } from "next/server";

import { callCommitTrackerRpc } from "@/app/api/commit-tracker/_lib/connect";

export async function GET(request: NextRequest) {
  try {
    const provider =
      request.nextUrl.searchParams.get("provider") ??
      "GIT_PROVIDER_KIND_GITHUB";
    const repository = request.nextUrl.searchParams.get("repository") ?? "";
    const baseCommitSha = request.nextUrl.searchParams.get("baseCommitSha") ?? "";
    const headCommitSha = request.nextUrl.searchParams.get("headCommitSha") ?? "";
    const environment = request.nextUrl.searchParams.get("environment") ?? "";
    const metricKeys = request.nextUrl.searchParams
      .getAll("metricKey")
      .map((value) => value.trim())
      .filter((value) => value.length > 0);

    const response = await callCommitTrackerRpc<object, unknown>(
      "/committracker.v1.MetricQueryService/GetPullRequestComparison",
      {
        provider,
        repository,
        baseCommitSha,
        headCommitSha,
        environment,
        metricKeys,
      },
    );

    return NextResponse.json(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
