import { NextRequest, NextResponse } from "next/server";

import {
  CommitTrackerApiError,
  callCommitTrackerRpc,
} from "@/app/api/commit-tracker/_lib/connect";

export async function GET(request: NextRequest) {
  try {
    const provider =
      request.nextUrl.searchParams.get("provider") ??
      "GIT_PROVIDER_KIND_GITHUB";
    const repository = request.nextUrl.searchParams.get("repository") ?? "";
    const branch = request.nextUrl.searchParams.get("branch") ?? "";
    const environment = request.nextUrl.searchParams.get("environment") ?? "";
    const metricKey = request.nextUrl.searchParams.get("metricKey") ?? "";
    const fromTime = request.nextUrl.searchParams.get("fromTime") ?? "";
    const toTime = request.nextUrl.searchParams.get("toTime") ?? "";
    const limitRaw = request.nextUrl.searchParams.get("limit") ?? "50";

    const limit = Number.parseInt(limitRaw, 10);
    const response = await callCommitTrackerRpc<object, unknown>(
      "/committracker.v1.MetricQueryService/ListMetricSeries",
      {
        provider,
        repository,
        branch,
        environment,
        metricKey,
        fromTime: fromTime || undefined,
        toTime: toTime || undefined,
        limit: Number.isNaN(limit) ? 50 : limit,
      },
    );

    return NextResponse.json(response);
  } catch (error) {
    if (error instanceof CommitTrackerApiError) {
      return NextResponse.json(
        { error: error.message },
        { status: error.status },
      );
    }

    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
