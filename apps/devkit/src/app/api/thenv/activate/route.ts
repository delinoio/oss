import { NextResponse } from "next/server";

import { callThenvRpc, parseScopeFromBody } from "@/app/api/thenv/_lib/connect";
import {
  parseRequestBodyObject,
  parseRequiredBodyString,
  ThenvValidationError,
} from "@/app/api/thenv/_lib/validation";

export async function POST(request: Request) {
  let bodyPayload: unknown;

  try {
    bodyPayload = await request.json();
  } catch (error) {
    if (error instanceof SyntaxError) {
      return NextResponse.json(
        { error: "request body must be valid JSON" },
        { status: 400 },
      );
    }

    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }

  try {
    const body = parseRequestBodyObject(bodyPayload);
    const scope = parseScopeFromBody(body);
    const bundleVersionId = parseRequiredBodyString(
      body,
      "bundleVersionId",
      "bundleVersionId is required",
    );

    const response = await callThenvRpc<object, unknown>(
      "/thenv.v1.BundleService/ActivateBundleVersion",
      {
        scope,
        bundleVersionId,
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
