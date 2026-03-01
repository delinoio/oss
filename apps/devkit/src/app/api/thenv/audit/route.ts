import { NextRequest, NextResponse } from "next/server";

import { callThenvRpc, parseScopeFromSearchParams } from "@/app/api/thenv/_lib/connect";
import {
  parseAuditEventType,
  parseCursor,
  parseLimit,
  ThenvValidationError,
} from "@/app/api/thenv/_lib/validation";

export async function GET(request: NextRequest) {
  try {
    const scope = parseScopeFromSearchParams(request);
    const limit = parseLimit(request.nextUrl.searchParams.get("limit"));
    const cursor = parseCursor(request.nextUrl.searchParams.get("cursor"));
    const actor = request.nextUrl.searchParams.get("actor") ?? "";
    const eventType = parseAuditEventType(
      request.nextUrl.searchParams.get("eventType"),
    );
    const fromTime = request.nextUrl.searchParams.get("fromTime") ?? "";
    const toTime = request.nextUrl.searchParams.get("toTime") ?? "";

    const response = await callThenvRpc<object, unknown>(
      "/thenv.v1.AuditService/ListAuditEvents",
      {
        scope,
        limit,
        cursor,
        actor,
        eventType,
        fromTime: fromTime || undefined,
        toTime: toTime || undefined,
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
