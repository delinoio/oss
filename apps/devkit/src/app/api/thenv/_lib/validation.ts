import {
  DEFAULT_THENV_SCOPE,
  ThenvAuditEventType,
  ThenvRole,
  ThenvScope,
} from "@/apps/thenv/contracts";

export const DEFAULT_THENV_LIST_LIMIT = 20;
export const MAX_THENV_LIST_LIMIT = 100;

const NON_NEGATIVE_INTEGER_PATTERN = /^[0-9]+$/;

const THENV_AUDIT_EVENT_TYPES = Object.values(ThenvAuditEventType) as ThenvAuditEventType[];
const THENV_ROLE_VALUES = Object.values(ThenvRole) as ThenvRole[];

export class ThenvValidationError extends Error {
  readonly status = 400;

  constructor(message: string) {
    super(message);
    this.name = "ThenvValidationError";
  }
}

function hasOwn(record: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseRequiredNonEmptyString(value: unknown, fieldName: string): string {
  if (typeof value !== "string") {
    throw new ThenvValidationError(`${fieldName} must be a non-empty string`);
  }

  const normalized = value.trim();
  if (normalized.length === 0) {
    throw new ThenvValidationError(`${fieldName} must be a non-empty string`);
  }

  return normalized;
}

function parseScopeFieldFromQuery(
  searchParams: URLSearchParams,
  queryKey: string,
  defaultValue: string,
): string {
  if (!searchParams.has(queryKey)) {
    return defaultValue;
  }

  return parseRequiredNonEmptyString(searchParams.get(queryKey), queryKey);
}

function parseScopeFieldFromBody(
  scope: Record<string, unknown>,
  fieldName: keyof ThenvScope,
  defaultValue: string,
): string {
  if (!hasOwn(scope, fieldName)) {
    return defaultValue;
  }

  return parseRequiredNonEmptyString(scope[fieldName], `scope.${fieldName}`);
}

function parseEnumValue<T extends string>(
  value: string,
  fieldName: string,
  allowedValues: readonly T[],
): T {
  if (allowedValues.includes(value as T)) {
    return value as T;
  }

  throw new ThenvValidationError(
    `${fieldName} must be one of: ${allowedValues.join(", ")}`,
  );
}

function parseStrictInteger(rawValue: string, fieldName: string): number {
  const normalized = rawValue.trim();
  if (!NON_NEGATIVE_INTEGER_PATTERN.test(normalized)) {
    throw new ThenvValidationError(`${fieldName} must be a non-negative integer`);
  }

  const parsed = Number.parseInt(normalized, 10);
  if (!Number.isSafeInteger(parsed)) {
    throw new ThenvValidationError(`${fieldName} must be a non-negative integer`);
  }

  return parsed;
}

export function parseRequestBodyObject(payload: unknown): Record<string, unknown> {
  if (!isRecord(payload)) {
    throw new ThenvValidationError("request body must be a JSON object");
  }

  return payload;
}

export function parseScopeFromSearchParams(
  searchParams: URLSearchParams,
): ThenvScope {
  return {
    workspaceId: parseScopeFieldFromQuery(
      searchParams,
      "workspace",
      DEFAULT_THENV_SCOPE.workspaceId,
    ),
    projectId: parseScopeFieldFromQuery(
      searchParams,
      "project",
      DEFAULT_THENV_SCOPE.projectId,
    ),
    environmentId: parseScopeFieldFromQuery(
      searchParams,
      "environment",
      DEFAULT_THENV_SCOPE.environmentId,
    ),
  };
}

export function parseScopeFromBody(payload: unknown): ThenvScope {
  const body = parseRequestBodyObject(payload);

  if (!hasOwn(body, "scope")) {
    return DEFAULT_THENV_SCOPE;
  }

  const scope = body.scope;
  if (scope === undefined) {
    return DEFAULT_THENV_SCOPE;
  }

  if (!isRecord(scope)) {
    throw new ThenvValidationError("scope must be an object");
  }

  return {
    workspaceId: parseScopeFieldFromBody(
      scope,
      "workspaceId",
      DEFAULT_THENV_SCOPE.workspaceId,
    ),
    projectId: parseScopeFieldFromBody(
      scope,
      "projectId",
      DEFAULT_THENV_SCOPE.projectId,
    ),
    environmentId: parseScopeFieldFromBody(
      scope,
      "environmentId",
      DEFAULT_THENV_SCOPE.environmentId,
    ),
  };
}

export function parseLimit(rawValue: string | null): number {
  if (rawValue === null) {
    return DEFAULT_THENV_LIST_LIMIT;
  }

  const normalized = rawValue.trim();
  if (!NON_NEGATIVE_INTEGER_PATTERN.test(normalized)) {
    throw new ThenvValidationError(
      `limit must be an integer between 1 and ${MAX_THENV_LIST_LIMIT}`,
    );
  }

  const parsed = Number.parseInt(normalized, 10);
  if (!Number.isSafeInteger(parsed) || parsed < 1 || parsed > MAX_THENV_LIST_LIMIT) {
    throw new ThenvValidationError(
      `limit must be an integer between 1 and ${MAX_THENV_LIST_LIMIT}`,
    );
  }

  return parsed;
}

export function parseCursor(rawValue: string | null): string {
  if (rawValue === null) {
    return "";
  }

  const normalized = rawValue.trim();
  if (normalized.length === 0) {
    return "";
  }

  parseStrictInteger(normalized, "cursor");
  return normalized;
}

export function parseAuditEventType(
  rawValue: string | null,
): ThenvAuditEventType {
  if (rawValue === null) {
    return ThenvAuditEventType.Unspecified;
  }

  const normalized = parseRequiredNonEmptyString(rawValue, "eventType");
  return parseEnumValue(normalized, "eventType", THENV_AUDIT_EVENT_TYPES);
}

export function parseRole(value: unknown, fieldName: string): ThenvRole {
  const normalized = parseRequiredNonEmptyString(value, fieldName);
  return parseEnumValue(normalized, fieldName, THENV_ROLE_VALUES);
}

export interface ValidatedPolicyBinding {
  subject: string;
  role: ThenvRole;
}

export function parsePolicyBindings(payload: unknown): ValidatedPolicyBinding[] {
  const body = parseRequestBodyObject(payload);

  if (!hasOwn(body, "bindings") || body.bindings === undefined) {
    return [];
  }

  if (!Array.isArray(body.bindings)) {
    throw new ThenvValidationError("bindings must be an array");
  }

  return body.bindings.map((binding, index) => {
    if (!isRecord(binding)) {
      throw new ThenvValidationError(`bindings[${index}] must be an object`);
    }

    return {
      subject: parseRequiredNonEmptyString(
        binding.subject,
        `bindings[${index}].subject`,
      ),
      role: parseRole(binding.role, `bindings[${index}].role`),
    };
  });
}

export function parseRequiredBodyString(
  body: Record<string, unknown>,
  fieldName: string,
  requiredMessage?: string,
): string {
  const value = body[fieldName];
  if (typeof value !== "string") {
    throw new ThenvValidationError(
      requiredMessage ?? `${fieldName} must be a non-empty string`,
    );
  }

  const normalized = value.trim();
  if (normalized.length === 0) {
    throw new ThenvValidationError(
      requiredMessage ?? `${fieldName} must be a non-empty string`,
    );
  }

  return normalized;
}

export function isMalformedJsonError(error: unknown): boolean {
  return error instanceof SyntaxError;
}
