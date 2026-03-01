import {
  ThenvAuditQuery,
  ThenvGetPolicyResponse,
  ThenvListAuditEventsResponse,
  ThenvListVersionsResponse,
  ThenvPolicyBinding,
  ThenvScope,
} from "@/apps/thenv/contracts";

function withScope(pathname: string, scope: ThenvScope): string {
  const query = scopeParams(scope);
  return `${pathname}?${query.toString()}`;
}

function scopeParams(scope: ThenvScope): URLSearchParams {
  const query = new URLSearchParams();
  query.set("workspace", scope.workspaceId);
  query.set("project", scope.projectId);
  query.set("environment", scope.environmentId);
  return query;
}

function toAuditParams(query: ThenvAuditQuery): URLSearchParams {
  const params = scopeParams(query.scope);

  if (query.actor) {
    params.set("actor", query.actor);
  }
  if (query.eventType) {
    params.set("eventType", query.eventType);
  }
  if (query.fromTime) {
    params.set("fromTime", query.fromTime);
  }
  if (query.toTime) {
    params.set("toTime", query.toTime);
  }
  if (query.limit && query.limit > 0) {
    params.set("limit", String(query.limit));
  }
  if (query.cursor) {
    params.set("cursor", query.cursor);
  }

  return params;
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
  query: ThenvAuditQuery,
): Promise<ThenvListAuditEventsResponse> {
  const response = await fetch(`/api/thenv/audit?${toAuditParams(query).toString()}`, {
    cache: "no-store",
  });
  return parseJsonResponse<ThenvListAuditEventsResponse>(response);
}
