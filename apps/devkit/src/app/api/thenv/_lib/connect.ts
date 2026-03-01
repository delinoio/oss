import { NextRequest } from "next/server";

import { ThenvScope } from "@/apps/thenv/contracts";
import {
  parseScopeFromBody as parseValidatedScopeFromBody,
  parseScopeFromSearchParams as parseValidatedScopeFromSearchParams,
} from "@/app/api/thenv/_lib/validation";

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
  return parseValidatedScopeFromSearchParams(request.nextUrl.searchParams);
}

export function parseScopeFromBody(payload: unknown): ThenvScope {
  return parseValidatedScopeFromBody(payload);
}
