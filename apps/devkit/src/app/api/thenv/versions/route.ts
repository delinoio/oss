import { NextRequest, NextResponse } from "next/server";

import { callThenvRpc, parseScopeFromSearchParams } from "@/app/api/thenv/_lib/connect";
import {
  parseCursor,
  parseLimit,
  ThenvValidationError,
} from "@/app/api/thenv/_lib/validation";

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
    const limit = parseLimit(request.nextUrl.searchParams.get("limit"));
    const cursor = parseCursor(request.nextUrl.searchParams.get("cursor"));

    const response = await callThenvRpc<object, ListBundleVersionsResponse>(
      "/thenv.v1.BundleService/ListBundleVersions",
      {
        scope,
        limit,
        cursor,
      },
    );

    return NextResponse.json(response);
  } catch (error) {
    if (error instanceof ThenvValidationError) {
      return NextResponse.json({ error: error.message }, { status: 400 });
    }

    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
