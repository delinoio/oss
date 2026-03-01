import { NextRequest, NextResponse } from "next/server";

import { callThenvRpc, parseScopeFromSearchParams } from "@/app/api/thenv/_lib/connect";
import { ThenvAuditEventType } from "@/apps/thenv/contracts";

export async function GET(request: NextRequest) {
  try {
    const scope = parseScopeFromSearchParams(request);
    const limitRaw = request.nextUrl.searchParams.get("limit");
    const cursor = request.nextUrl.searchParams.get("cursor") ?? "";
    const actor = request.nextUrl.searchParams.get("actor") ?? "";
    const eventType =
      request.nextUrl.searchParams.get("eventType") ??
      ThenvAuditEventType.Unspecified;
    const fromTime = request.nextUrl.searchParams.get("fromTime") ?? "";
    const toTime = request.nextUrl.searchParams.get("toTime") ?? "";
    const limit = limitRaw ? Number.parseInt(limitRaw, 10) : 20;

    const response = await callThenvRpc<object, unknown>(
      "/thenv.v1.AuditService/ListAuditEvents",
      {
        scope,
        limit: Number.isNaN(limit) ? 20 : limit,
        cursor,
        actor,
        eventType,
        fromTime: fromTime || undefined,
        toTime: toTime || undefined,
      },
    );

    return NextResponse.json(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
