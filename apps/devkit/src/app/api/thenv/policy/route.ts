import { NextRequest, NextResponse } from "next/server";

import {
  callThenvRpc,
  parseScopeFromBody,
  parseScopeFromSearchParams,
} from "@/app/api/thenv/_lib/connect";
import {
  parsePolicyBindings,
  ThenvValidationError,
} from "@/app/api/thenv/_lib/validation";

export async function GET(request: NextRequest) {
  try {
    const scope = parseScopeFromSearchParams(request);
    const response = await callThenvRpc<object, unknown>(
      "/thenv.v1.PolicyService/GetPolicy",
      { scope },
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

export async function PUT(request: Request) {
  let body: unknown;

  try {
    body = await request.json();
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
    const scope = parseScopeFromBody(body);
    const bindings = parsePolicyBindings(body);

    const response = await callThenvRpc<object, unknown>(
      "/thenv.v1.PolicyService/SetPolicy",
      {
        scope,
        bindings,
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
