import {
  DiagnosticArchitecture,
  DiagnosticComponent,
  DiagnosticPlatform,
  DiagnosticSeverity,
  type SubmitCrashReportRequest,
} from "./gen/devhud/v1/diagnostics_pb.js";
import { assertWellFormedUnicode } from "./unicode.js";

export const MAX_SETTINGS_JSON_BYTES = 1_048_576;
export const MAX_ADMIN_REASON_BYTES = 4_096;
export const MAX_CRASH_IDENTIFIER_BYTES = 256;
export const MAX_CRASH_SUMMARY_BYTES = 4_096;
export const MAX_CRASH_STACK_BYTES = 32_768;

const UUID_V7_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder("utf-8", { fatal: true });
const unicodeNonWhitespacePattern = /\P{White_Space}/u;
const urlPattern = /\b[A-Za-z][A-Za-z0-9+.-]*:[^\s<>"']+/gu;
const trailingUrlPunctuationPattern = /[)\]}>.,;]+$/u;
const credentialParameterNamePattern =
  /^(?:code|password|passwd|pwd|secret|token|client[_.-]?secret|(?:access|refresh|id)[_.-]?token|api[_.-]?key|private[_.-]?key|authorization|cookie|set-cookie|x-amz-(?:credential|signature))$/iu;
const MIN_PROTOBUF_TIMESTAMP_SECONDS = -62_135_596_800n;
const MAX_PROTOBUF_TIMESTAMP_SECONDS = 253_402_300_799n;
const MAX_PROTOBUF_TIMESTAMP_NANOS = 999_999_999;
const diagnosticPlatforms: ReadonlySet<DiagnosticPlatform> = new Set([
  DiagnosticPlatform.MACOS,
  DiagnosticPlatform.WINDOWS,
  DiagnosticPlatform.LINUX,
  DiagnosticPlatform.IOS,
  DiagnosticPlatform.ANDROID,
]);
const diagnosticArchitectures: ReadonlySet<DiagnosticArchitecture> = new Set([
  DiagnosticArchitecture.X86_64,
  DiagnosticArchitecture.ARM64,
]);
const diagnosticComponents: ReadonlySet<DiagnosticComponent> = new Set([
  DiagnosticComponent.APP,
  DiagnosticComponent.AUTHENTICATION,
  DiagnosticComponent.SETTINGS,
  DiagnosticComponent.UPLOAD,
  DiagnosticComponent.ACCOUNT,
  DiagnosticComponent.NATIVE_SHELL,
]);
const diagnosticSeverities: ReadonlySet<DiagnosticSeverity> = new Set([
  DiagnosticSeverity.ERROR,
  DiagnosticSeverity.FATAL,
]);

const forbiddenSensitiveTextPatterns: ReadonlyArray<RegExp> = [
  // Keep this lookbehind-free while iOS 16.0-16.3 system webviews are supported.
  /(?:^(?:[\s\p{P}])?|[^:][\s\p{P}]|:\s+)(?:[A-Za-z]:[\\/][^\s]*|\\\\[^\s]+|~\/[^\s]+|\/(?!\/)[^\s]+)/u,
  // Relative paths require explicit prefixes or structural/file evidence so labels such as
  // React/Native, iOS/18.6, and 1.0.0/42 remain valid diagnostic text.
  /(?:^|[\s([{<"'=:])\.{1,2}[\\/](?:[\p{L}\p{N}_@.-]+[\\/])*[\p{L}\p{N}_@.-]+(?::\d+){0,2}(?=$|[\s\p{P}])/u,
  /(?:^|[\s([{<"'=:])(?:[\p{L}\p{N}_@.-]+[\\/])+(?:\.[\p{L}\p{N}_@.-]+|[\p{L}\p{N}_@.-]+\.[\p{L}][\p{L}\p{N}]*|Dockerfile|Makefile)(?::\d+){0,2}(?=$|[\s\p{P}])/u,
  /(?:^|[\s([{<"'=:])(?:[\p{L}\p{N}_@.-]+[\\/])+[\p{L}\p{N}_@.-]+:\d+(?::\d+)?(?=$|[\s\p{P}])/u,
  /(?:^|[\s([{<"'=:])[\p{L}\p{N}_@.-]+\.[\p{L}][\p{L}\p{N}]*:\d+(?::\d+)?(?=$|[\s\p{P}])/u,
  /file:\/\/[^\s]*/iu,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----/u,
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/u,
  /\bAuthorization\s*:\s*(?:Basic|Bearer)\s+\S+/iu,
  /\b(?:[\p{L}\p{N}]+_)*(?:password|passwd|pwd|secret(?:_access_key)?|token|client[_.-]?secret|(?:access|refresh|id)[_.-]?token|api[_.-]?key|private[_.-]?key|authorization|cookie|set-cookie)\b["']?\s*[:=]\s*\S+/iu,
  /\bAKIA[0-9A-Z]{16}\b/u,
];

export function assertUuidV7(value: string): void {
  if (!UUID_V7_PATTERN.test(value)) {
    throw new TypeError("value must be a canonical lowercase RFC 9562 UUID v7");
  }
}

export function assertSha256(value: Uint8Array): void {
  if (value.byteLength !== 32) {
    throw new RangeError("SHA-256 values must contain exactly 32 raw bytes");
  }
}

export function validateCanonicalSettingsJson(value: Uint8Array): unknown {
  if (value.byteLength > MAX_SETTINGS_JSON_BYTES) {
    throw new RangeError(`settings JSON must not exceed ${MAX_SETTINGS_JSON_BYTES} bytes`);
  }
  if (value[0] === 0xef && value[1] === 0xbb && value[2] === 0xbf) {
    throw new TypeError("settings JSON must not begin with a UTF-8 byte-order mark");
  }

  const source = textDecoder.decode(value);
  const parsed: unknown = JSON.parse(source);
  if (canonicalizeJson(parsed) !== source) {
    throw new TypeError("settings JSON must use RFC 8785 canonical encoding");
  }
  return parsed;
}

export function validateAdminReason(reason: string): void {
  if (!unicodeNonWhitespacePattern.test(reason)) {
    throw new TypeError("reason must contain at least one non-whitespace character");
  }
  validateSensitiveText(reason, MAX_ADMIN_REASON_BYTES, "reason");
}

export function validateCrashReport(report: SubmitCrashReportRequest): void {
  if (
    !Number.isInteger(report.reportSchemaVersion) ||
    report.reportSchemaVersion < 1 ||
    report.reportSchemaVersion > 0xffff_ffff
  ) {
    throw new RangeError("reportSchemaVersion must be an integer from 1 through 4294967295");
  }
  if (report.clientBuild === undefined) {
    throw new TypeError("clientBuild is required");
  }
  if (report.occurredAt === undefined) {
    throw new TypeError("occurredAt is required");
  }
  if (
    report.occurredAt.seconds < MIN_PROTOBUF_TIMESTAMP_SECONDS ||
    report.occurredAt.seconds > MAX_PROTOBUF_TIMESTAMP_SECONDS ||
    !Number.isInteger(report.occurredAt.nanos) ||
    report.occurredAt.nanos < 0 ||
    report.occurredAt.nanos > MAX_PROTOBUF_TIMESTAMP_NANOS
  ) {
    throw new RangeError("occurredAt must be a valid google.protobuf.Timestamp");
  }
  if (!diagnosticPlatforms.has(report.clientBuild.platform)) {
    throw new TypeError("clientBuild.platform must be a recognized nonzero value");
  }
  if (!diagnosticArchitectures.has(report.clientBuild.architecture)) {
    throw new TypeError("clientBuild.architecture must be a recognized nonzero value");
  }
  if (!diagnosticComponents.has(report.component)) {
    throw new TypeError("component must be a recognized nonzero value");
  }
  if (!diagnosticSeverities.has(report.severity)) {
    throw new TypeError("severity must be a recognized nonzero value");
  }

  validateSensitiveText(report.errorCode, MAX_CRASH_IDENTIFIER_BYTES, "errorCode");
  validateSensitiveText(
    report.clientBuild.appVersion,
    MAX_CRASH_IDENTIFIER_BYTES,
    "clientBuild.appVersion",
  );
  validateSensitiveText(
    report.clientBuild.buildId,
    MAX_CRASH_IDENTIFIER_BYTES,
    "clientBuild.buildId",
  );
  validateSensitiveText(
    report.clientBuild.osVersion,
    MAX_CRASH_IDENTIFIER_BYTES,
    "clientBuild.osVersion",
  );
  validateSensitiveText(report.redactedSummary, MAX_CRASH_SUMMARY_BYTES, "redactedSummary");
  validateSensitiveText(
    report.redactedStackTrace,
    MAX_CRASH_STACK_BYTES,
    "redactedStackTrace",
  );
  for (const correlationId of report.relatedCorrelationIds) {
    assertUuidV7(correlationId.value);
  }
}

function validateSensitiveText(value: string, maximum: number, field: string): void {
  assertWellFormedUnicode(value, field);
  if (textEncoder.encode(value).byteLength > maximum) {
    throw new RangeError(`${field} must not exceed ${maximum} UTF-8 bytes`);
  }
  for (const pattern of forbiddenSensitiveTextPatterns) {
    if (pattern.test(value)) {
      throw new TypeError(`${field} contains forbidden sensitive or local-path content`);
    }
  }
  if (containsForbiddenUrlContent(value)) {
    throw new TypeError(`${field} contains forbidden sensitive or local-path content`);
  }
}

function containsForbiddenUrlContent(value: string): boolean {
  for (const match of value.matchAll(urlPattern)) {
    const matchedUrl = match[0];
    if (matchedUrl === undefined) {
      continue;
    }
    const candidate = matchedUrl.replace(trailingUrlPunctuationPattern, "");

    // URL and URLSearchParams tolerate malformed percent escapes, so validate the complete
    // encoded URL first and reject undecodable URL-shaped content conservatively.
    try {
      decodeURI(candidate);
    } catch {
      return true;
    }

    let url: URL;
    try {
      url = new URL(candidate);
    } catch {
      continue;
    }

    if (url.username !== "" || url.password !== "") {
      return true;
    }
    if (
      containsCredentialParameterName(url.search.slice(1)) ||
      containsCredentialParameterName(url.hash.slice(1))
    ) {
      return true;
    }
  }
  return false;
}

function containsCredentialParameterName(parameters: string): boolean {
  for (const parameter of parameters.split("&")) {
    const separatorIndex = parameter.search(/[=:]/u);
    const encodedName = separatorIndex === -1 ? parameter : parameter.slice(0, separatorIndex);

    let name: string;
    try {
      name = decodeURIComponent(encodedName.replace(/\+/gu, " "));
    } catch {
      return true;
    }
    if (credentialParameterNamePattern.test(name)) {
      return true;
    }
  }
  return false;
}

type CanonicalizationFrame =
  | { readonly kind: "token"; readonly value: string }
  | { readonly kind: "value"; readonly value: unknown };

function canonicalizeJson(value: unknown): string {
  const output: string[] = [];
  // The byte limit still permits nesting deep enough to overflow the JavaScript call stack.
  const pending: CanonicalizationFrame[] = [{ kind: "value", value }];

  while (pending.length > 0) {
    const frame = pending.pop();
    if (frame === undefined) {
      break;
    }
    if (frame.kind === "token") {
      output.push(frame.value);
      continue;
    }

    const current = frame.value;
    if (typeof current === "string") {
      assertWellFormedUnicode(current);
      output.push(JSON.stringify(current));
    } else if (current === null || typeof current === "boolean") {
      output.push(JSON.stringify(current));
    } else if (typeof current === "number") {
      if (!Number.isFinite(current)) {
        throw new TypeError("settings JSON contains a non-finite number");
      }
      output.push(JSON.stringify(current));
    } else if (Array.isArray(current)) {
      output.push("[");
      pending.push({ kind: "token", value: "]" });
      for (let index = current.length - 1; index >= 0; index -= 1) {
        if (index < current.length - 1) {
          pending.push({ kind: "token", value: "," });
        }
        pending.push({ kind: "value", value: current[index] });
      }
    } else if (typeof current === "object") {
      const object = current as Record<string, unknown>;
      const keys = Object.keys(object).sort();
      output.push("{");
      pending.push({ kind: "token", value: "}" });
      for (let index = keys.length - 1; index >= 0; index -= 1) {
        const key = keys[index];
        if (key === undefined) {
          continue;
        }
        assertWellFormedUnicode(key);
        if (index < keys.length - 1) {
          pending.push({ kind: "token", value: "," });
        }
        pending.push({ kind: "value", value: object[key] });
        pending.push({ kind: "token", value: ":" });
        pending.push({ kind: "token", value: JSON.stringify(key) });
      }
    } else {
      throw new TypeError("settings JSON contains a value outside the JSON data model");
    }
  }

  return output.join("");
}
