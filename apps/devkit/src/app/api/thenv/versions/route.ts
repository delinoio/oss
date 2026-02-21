import { NextRequest, NextResponse } from "next/server";

import { callThenvRpc, parseScopeFromSearchParams } from "@/app/api/thenv/_lib/connect";

interface ListBundleVersionsResponse {
  versions: Array<{
    bundleVersionId: string;
    status: string;
    createdBy: string;
    createdAt?: string;
    fileTypes: string[];
    sourceVersionId?: string;
  }>;
  nextCursor?: string;
}

export async function GET(request: NextRequest) {
  try {
    const scope = parseScopeFromSearchParams(request);
    const limitRaw = request.nextUrl.searchParams.get("limit");
    const cursor = request.nextUrl.searchParams.get("cursor") ?? "";

    const limit = limitRaw ? Number.parseInt(limitRaw, 10) : 20;

    const response = await callThenvRpc<object, ListBundleVersionsResponse>(
      "/thenv.v1.BundleService/ListBundleVersions",
      {
        scope,
        limit: Number.isNaN(limit) ? 20 : limit,
        cursor,
      },
    );

    return NextResponse.json(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
