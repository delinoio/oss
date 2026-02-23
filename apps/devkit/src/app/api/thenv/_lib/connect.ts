import { NextRequest } from "next/server";

import { DEFAULT_THENV_SCOPE, ThenvScope } from "@/apps/thenv/contracts";

const CONNECT_PROTOCOL_VERSION = "1";

function resolveServerURL(): string {
  const configured =
    process.env.THENV_SERVER_URL ??
    process.env.NEXT_PUBLIC_THENV_SERVER_URL ??
    "http://127.0.0.1:8087";

  if (configured.includes("://")) {
    return configured;
  }
  return `http://${configured}`;
}

function resolveToken(): string {
  return (
    process.env.THENV_WEB_TOKEN ??
    process.env.THENV_TOKEN ??
    process.env.NEXT_PUBLIC_THENV_TOKEN ??
    "admin"
  ).trim();
}

function resolveSubject(token: string): string {
  const configured =
    process.env.THENV_WEB_SUBJECT ??
    process.env.THENV_SUBJECT ??
    process.env.NEXT_PUBLIC_THENV_SUBJECT ??
    token;
  return configured.trim() || token;
}

export async function callThenvRpc<Req extends object, Res>(
  procedure: string,
  requestBody: Req,
): Promise<Res> {
  const token = resolveToken();
  const subject = resolveSubject(token);

  const response = await fetch(`${resolveServerURL()}${procedure}`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      "Connect-Protocol-Version": CONNECT_PROTOCOL_VERSION,
      "X-Request-Id": `devkit-${Date.now()}`,
      "X-Thenv-Subject": subject,
      "X-Trace-Id": `devkit-trace-${Date.now()}`,
    },
    body: JSON.stringify(requestBody),
    cache: "no-store",
  });

  const payloadText = await response.text();
  if (!response.ok) {
    throw new Error(
      payloadText || `RPC ${procedure} failed with status ${response.status}`,
    );
  }

  if (!payloadText) {
    return {} as Res;
  }
  return JSON.parse(payloadText) as Res;
}

export function parseScopeFromSearchParams(request: NextRequest): ThenvScope {
  const workspaceId =
    request.nextUrl.searchParams.get("workspace") ??
    DEFAULT_THENV_SCOPE.workspaceId;
  const projectId =
    request.nextUrl.searchParams.get("project") ?? DEFAULT_THENV_SCOPE.projectId;
  const environmentId =
    request.nextUrl.searchParams.get("environment") ??
    DEFAULT_THENV_SCOPE.environmentId;

  return {
    workspaceId,
    projectId,
    environmentId,
  };
}

export interface ScopeBody {
  scope?: ThenvScope;
}

export function parseScopeFromBody(payload: ScopeBody): ThenvScope {
  const bodyScope = payload.scope;
  if (!bodyScope) {
    return DEFAULT_THENV_SCOPE;
  }

  return {
    workspaceId: bodyScope.workspaceId || DEFAULT_THENV_SCOPE.workspaceId,
    projectId: bodyScope.projectId || DEFAULT_THENV_SCOPE.projectId,
    environmentId: bodyScope.environmentId || DEFAULT_THENV_SCOPE.environmentId,
  };
}
