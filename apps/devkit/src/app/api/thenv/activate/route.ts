import { NextResponse } from "next/server";

import { callThenvRpc, parseScopeFromBody } from "@/app/api/thenv/_lib/connect";

interface ActivateBundleVersionBody {
  scope?: {
    workspaceId: string;
    projectId: string;
    environmentId: string;
  };
  bundleVersionId?: string;
}

export async function POST(request: Request) {
  try {
    const body = (await request.json()) as ActivateBundleVersionBody;
    const scope = parseScopeFromBody(body);
    const bundleVersionId = (body.bundleVersionId ?? "").trim();

    if (!bundleVersionId) {
      return NextResponse.json(
        { error: "bundleVersionId is required" },
        { status: 400 },
      );
    }

    const response = await callThenvRpc<object, unknown>(
      "/thenv.v1.BundleService/ActivateBundleVersion",
      {
        scope,
        bundleVersionId,
      },
    );

    return NextResponse.json(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
