import { normalizeRoleValue, parseAuditEventLabel } from "./thenv-normalize";

export type ThenvScope = {
  workspaceId: string;
  projectId: string;
  environmentId: string;
};

export type VersionSummary = {
  bundleVersionId: string;
  status: string;
  createdBy: string;
  createdAt: string;
  sourceVersionId?: string;
};

export type PolicyBinding = {
  subject: string;
  role: string;
};

export type AuditEvent = {
  eventId: string;
  eventType: string;
  actor: string;
  targetBundleVersionId?: string;
  result: string;
  createdAt: string;
  metadata?: string;
};

export type DashboardData = {
  scope: ThenvScope;
  versions: VersionSummary[];
  policy: PolicyBinding[];
  policyRevision: number;
  auditEvents: AuditEvent[];
  failures: string[];
};

const procedures = {
  listVersions: "/thenv.v1.BundleService/ListBundleVersions",
  activateVersion: "/thenv.v1.BundleService/ActivateBundleVersion",
  getPolicy: "/thenv.v1.PolicyService/GetPolicy",
  setPolicy: "/thenv.v1.PolicyService/SetPolicy",
  listAudit: "/thenv.v1.AuditService/ListAuditEvents",
} as const;

const defaultServerURL = "http://127.0.0.1:8080";

function baseURL(): string {
  return (process.env.THENV_SERVER_URL ?? defaultServerURL).replace(/\/$/, "");
}

function authHeaders(): Record<string, string> {
  const token = process.env.THENV_WEB_TOKEN?.trim();
  if (!token) {
    return {
      "Connect-Protocol-Version": "1",
      "Content-Type": "application/json",
    };
  }
  return {
    Authorization: `Bearer ${token}`,
    "Connect-Protocol-Version": "1",
    "Content-Type": "application/json",
  };
}

async function callProcedure<TResponse>(procedure: string, body: unknown): Promise<TResponse> {
  const response = await fetch(`${baseURL()}${procedure}`, {
    method: "POST",
    headers: authHeaders(),
    body: JSON.stringify(body),
    cache: "no-store",
  });

  if (!response.ok) {
    const failureText = await response.text();
    throw new Error(`request failed for ${procedure}: ${response.status} ${failureText}`);
  }

  return (await response.json()) as TResponse;
}

export async function listBundleVersions(scope: ThenvScope, limit = 20): Promise<VersionSummary[]> {
  const payload = await callProcedure<{ versions?: Array<Record<string, unknown>> }>(procedures.listVersions, {
    scope,
    limit,
  });

  return (payload.versions ?? []).map((version) => ({
    bundleVersionId: String(version.bundleVersionId ?? ""),
    status: String(version.status ?? "unknown"),
    createdBy: String(version.createdBy ?? ""),
    createdAt: String(version.createdAt ?? ""),
    sourceVersionId: String(version.sourceVersionId ?? ""),
  }));
}

export async function getPolicy(scope: ThenvScope): Promise<{ policyRevision: number; bindings: PolicyBinding[] }> {
  const payload = await callProcedure<{ policyRevision?: number; bindings?: Array<Record<string, unknown>> }>(procedures.getPolicy, {
    scope,
  });

  return {
    policyRevision: Number(payload.policyRevision ?? 0),
    bindings: (payload.bindings ?? []).map((binding) => ({
      subject: String(binding.subject ?? ""),
      role: normalizeRoleValue(String(binding.role ?? "reader")),
    })),
  };
}

export async function listAuditEvents(scope: ThenvScope, limit = 30): Promise<AuditEvent[]> {
  const payload = await callProcedure<{ events?: Array<Record<string, unknown>> }>(procedures.listAudit, {
    scope,
    limit,
  });

  return (payload.events ?? []).map((event) => ({
    eventId: String(event.eventId ?? ""),
    eventType: parseAuditEventLabel(String(event.eventType ?? "unspecified")),
    actor: String(event.actor ?? ""),
    targetBundleVersionId: String(event.targetBundleVersionId ?? ""),
    result: String(event.result ?? ""),
    createdAt: String(event.createdAt ?? ""),
    metadata: String(event.metadata ?? ""),
  }));
}

export async function activateVersion(scope: ThenvScope, bundleVersionId: string): Promise<void> {
  await callProcedure(procedures.activateVersion, {
    scope,
    bundleVersionId,
  });
}

export async function setPolicy(scope: ThenvScope, bindings: PolicyBinding[]): Promise<void> {
  await callProcedure(procedures.setPolicy, {
    scope,
    bindings: bindings.map((binding) => ({
      subject: binding.subject,
      role: normalizeRoleValue(binding.role),
    })),
  });
}

export async function loadDashboard(scope: ThenvScope): Promise<DashboardData> {
  const [versionsResult, policyResult, auditResult] = await Promise.allSettled([
    listBundleVersions(scope),
    getPolicy(scope),
    listAuditEvents(scope),
  ]);

  const failures: string[] = [];

  const versions = versionsResult.status === "fulfilled" ? versionsResult.value : [];
  if (versionsResult.status === "rejected") {
    failures.push(`versions: ${versionsResult.reason}`);
  }

  const policy = policyResult.status === "fulfilled" ? policyResult.value.bindings : [];
  const policyRevision = policyResult.status === "fulfilled" ? policyResult.value.policyRevision : 0;
  if (policyResult.status === "rejected") {
    failures.push(`policy: ${policyResult.reason}`);
  }

  const auditEvents = auditResult.status === "fulfilled" ? auditResult.value : [];
  if (auditResult.status === "rejected") {
    failures.push(`audit: ${auditResult.reason}`);
  }

  return {
    scope,
    versions,
    policy,
    policyRevision,
    auditEvents,
    failures,
  };
}
