import type { SubmitCrashReportRequest } from "./gen/devhud/v1/diagnostics_pb.js";

export const MAX_SETTINGS_JSON_BYTES = 1_048_576;
export const MAX_CRASH_SUMMARY_BYTES = 4_096;
export const MAX_CRASH_STACK_BYTES = 32_768;

const UUID_V7_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder("utf-8", { fatal: true });

const forbiddenDiagnosticPatterns: ReadonlyArray<RegExp> = [
  /(?:^|[\s\p{P}])(?:[A-Za-z]:\\|\\\\)[^\s]*/u,
  /(?:^|[\s\p{P}])(?:~\/|\/(?:Users|home|private|tmp|var|etc|opt|mnt|srv)\/)[^\s]*/u,
  /file:\/\/[^\s]*/iu,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----/u,
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/u,
  /\bAuthorization\s*:\s*(?:Basic|Bearer)\s+\S+/iu,
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

  const source = textDecoder.decode(value);
  const parsed: unknown = JSON.parse(source);
  if (canonicalizeJson(parsed) !== source) {
    throw new TypeError("settings JSON must use RFC 8785 canonical encoding");
  }
  return parsed;
}

export function validateCrashReport(report: SubmitCrashReportRequest): void {
  validateRedactedText(report.redactedSummary, MAX_CRASH_SUMMARY_BYTES, "redactedSummary");
  validateRedactedText(report.redactedStackTrace, MAX_CRASH_STACK_BYTES, "redactedStackTrace");
}

function validateRedactedText(value: string, maximum: number, field: string): void {
  if (textEncoder.encode(value).byteLength > maximum) {
    throw new RangeError(`${field} must not exceed ${maximum} UTF-8 bytes`);
  }
  for (const pattern of forbiddenDiagnosticPatterns) {
    if (pattern.test(value)) {
      throw new TypeError(`${field} contains forbidden sensitive or local-path content`);
    }
  }
}

function canonicalizeJson(value: unknown): string {
  if (typeof value === "string") {
    assertWellFormedUnicode(value);
    return JSON.stringify(value);
  }
  if (value === null || typeof value === "boolean") {
    return JSON.stringify(value);
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new TypeError("settings JSON contains a non-finite number");
    }
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalizeJson).join(",")}]`;
  }
  if (typeof value === "object") {
    const object = value as Record<string, unknown>;
    return `{${Object.keys(object)
      .sort()
      .map((key) => {
        assertWellFormedUnicode(key);
        return `${JSON.stringify(key)}:${canonicalizeJson(object[key])}`;
      })
      .join(",")}}`;
  }
  throw new TypeError("settings JSON contains a value outside the JSON data model");
}

function assertWellFormedUnicode(value: string): void {
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index);
    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      const nextCodeUnit = value.charCodeAt(index + 1);
      if (!(nextCodeUnit >= 0xdc00 && nextCodeUnit <= 0xdfff)) {
        throw new TypeError("settings JSON contains invalid Unicode data");
      }
      index += 1;
    } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
      throw new TypeError("settings JSON contains invalid Unicode data");
    }
  }
}
