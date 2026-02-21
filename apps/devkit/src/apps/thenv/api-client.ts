import {
  ThenvGetPolicyResponse,
  ThenvListAuditEventsResponse,
  ThenvListVersionsResponse,
  ThenvPolicyBinding,
  ThenvScope,
} from "@/apps/thenv/contracts";

interface ScopeQueryParams {
  workspace: string;
  project: string;
  environment: string;
}

function toScopeQueryParams(scope: ThenvScope): ScopeQueryParams {
  return {
    workspace: scope.workspaceId,
    project: scope.projectId,
    environment: scope.environmentId,
  };
}

function withScope(pathname: string, scope: ThenvScope): string {
  const query = new URLSearchParams(toScopeQueryParams(scope));
  return `${pathname}?${query.toString()}`;
}

async function parseJsonResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const details = await response.text();
    throw new Error(`Request failed (${response.status}): ${details}`);
  }

  return (await response.json()) as T;
}

export async function listVersions(
  scope: ThenvScope,
): Promise<ThenvListVersionsResponse> {
  const response = await fetch(withScope("/api/thenv/versions", scope), {
    cache: "no-store",
  });
  return parseJsonResponse<ThenvListVersionsResponse>(response);
}

export async function activateVersion(
  scope: ThenvScope,
  bundleVersionId: string,
): Promise<void> {
  const response = await fetch("/api/thenv/activate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ scope, bundleVersionId }),
  });

  if (!response.ok) {
    const details = await response.text();
    throw new Error(`Activate failed (${response.status}): ${details}`);
  }
}

export async function getPolicy(
  scope: ThenvScope,
): Promise<ThenvGetPolicyResponse> {
  const response = await fetch(withScope("/api/thenv/policy", scope), {
    cache: "no-store",
  });
  return parseJsonResponse<ThenvGetPolicyResponse>(response);
}

export async function setPolicy(
  scope: ThenvScope,
  bindings: ThenvPolicyBinding[],
): Promise<ThenvGetPolicyResponse> {
  const response = await fetch("/api/thenv/policy", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ scope, bindings }),
  });
  return parseJsonResponse<ThenvGetPolicyResponse>(response);
}

export async function listAuditEvents(
  scope: ThenvScope,
): Promise<ThenvListAuditEventsResponse> {
  const response = await fetch(withScope("/api/thenv/audit", scope), {
    cache: "no-store",
  });
  return parseJsonResponse<ThenvListAuditEventsResponse>(response);
}
