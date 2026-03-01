export enum ThenvFileType {
  Env = "FILE_TYPE_ENV",
  DevVars = "FILE_TYPE_DEV_VARS",
}

export enum ThenvRole {
  Reader = "ROLE_READER",
  Writer = "ROLE_WRITER",
  Admin = "ROLE_ADMIN",
}

export enum ThenvBundleStatus {
  Active = "BUNDLE_STATUS_ACTIVE",
  Archived = "BUNDLE_STATUS_ARCHIVED",
}

export enum ThenvAuditEventType {
  Unspecified = "AUDIT_EVENT_TYPE_UNSPECIFIED",
  Push = "AUDIT_EVENT_TYPE_PUSH",
  Pull = "AUDIT_EVENT_TYPE_PULL",
  List = "AUDIT_EVENT_TYPE_LIST",
  Rotate = "AUDIT_EVENT_TYPE_ROTATE",
  Activate = "AUDIT_EVENT_TYPE_ACTIVATE",
  PolicyUpdate = "AUDIT_EVENT_TYPE_POLICY_UPDATE",
}

export enum ThenvOutcome {
  Unspecified = "OUTCOME_UNSPECIFIED",
  Success = "OUTCOME_SUCCESS",
  Denied = "OUTCOME_DENIED",
  Failed = "OUTCOME_FAILED",
}

export interface ThenvScope {
  workspaceId: string;
  projectId: string;
  environmentId: string;
}

export interface ThenvBundleVersionSummary {
  bundleVersionId: string;
  status: ThenvBundleStatus;
  createdBy: string;
  createdAt?: string;
  fileTypes: ThenvFileType[];
  sourceVersionId?: string;
}

export interface ThenvPolicyBinding {
  subject: string;
  role: ThenvRole;
}

export interface ThenvAuditEvent {
  eventId: string;
  eventType: ThenvAuditEventType;
  actor: string;
  bundleVersionId?: string;
  targetBundleVersionId?: string;
  outcome: ThenvOutcome;
  requestId: string;
  traceId: string;
  createdAt?: string;
}

export interface ThenvPaginationQuery {
  limit?: number;
  cursor?: string;
}

export interface ThenvAuditQuery extends ThenvPaginationQuery {
  scope: ThenvScope;
  actor?: string;
  eventType?: ThenvAuditEventType;
  fromTime?: string;
  toTime?: string;
}

export interface ThenvListVersionsResponse {
  versions: ThenvBundleVersionSummary[];
  nextCursor?: string;
}

export interface ThenvGetPolicyResponse {
  bindings: ThenvPolicyBinding[];
  policyRevision: number;
}

export interface ThenvListAuditEventsResponse {
  events: ThenvAuditEvent[];
  nextCursor?: string;
}

export const DEFAULT_THENV_SCOPE: ThenvScope = {
  workspaceId: "default-workspace",
  projectId: "default-project",
  environmentId: "dev",
};
