import { NextRequest, NextResponse } from "next/server";

import {
  callThenvRpc,
  parseScopeFromBody,
  parseScopeFromSearchParams,
} from "@/app/api/thenv/_lib/connect";

interface PolicyBindingPayload {
  subject: string;
  role: string;
}

interface SetPolicyBody {
  scope?: {
    workspaceId: string;
    projectId: string;
    environmentId: string;
  };
  bindings?: PolicyBindingPayload[];
}

export async function GET(request: NextRequest) {
  try {
    const scope = parseScopeFromSearchParams(request);
    const response = await callThenvRpc<object, unknown>(
      "/thenv.v1.PolicyService/GetPolicy",
      { scope },
    );
    return NextResponse.json(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}

export async function PUT(request: Request) {
  try {
    const body = (await request.json()) as SetPolicyBody;
    const scope = parseScopeFromBody(body);
    const bindings = (body.bindings ?? [])
      .filter((binding) => binding.subject.trim().length > 0)
      .map((binding) => ({
        subject: binding.subject.trim(),
        role: binding.role,
      }));

    const response = await callThenvRpc<object, unknown>(
      "/thenv.v1.PolicyService/SetPolicy",
      {
        scope,
        bindings,
      },
    );
    return NextResponse.json(response);
  } catch (error) {
    const message = error instanceof Error ? error.message : "Unknown error";
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
