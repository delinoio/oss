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
export const MAX_CRASH_RELATED_CORRELATIONS = 32;
export const MAX_CRASH_DURATION_MILLISECONDS = 86_400_000n;
export const MAX_CRASH_STACK_LINES = 64;
export const MAX_CRASH_STACK_LINE_BYTES = 512;

const UUID_V7_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder("utf-8", { fatal: true });
const unicodeNonWhitespacePattern = /\P{White_Space}/u;
const urlPattern = /[A-Za-z][A-Za-z0-9+.-]*:[^\s<>"']+/gu;
const urlParametersPattern = /[?#][^\s<>"']+/gu;
const webDiagnosticUrlProtocols = new Set(["http:", "https:"]);
const webDiagnosticUrlAuthorityPattern = /^https?:\/\/[^/?#]+/iu;
const trailingUrlPunctuationPattern = /[)\]}>.,;]+$/u;
const percentEncodedOctetsPattern = /(?:%[0-9a-f]{2})+/giu;
const encodedWindowsDrivePathPattern = /^[A-Za-z]:(?:%2f|%5c)/iu;
const credentialParameterNamePattern =
  /^(?:code|oauth[_.-]?code|password|passwd|pwd|pat|secret|token|client[_.-]?secret|(?:access|refresh|id)[_.-]?token|(?:r2[_.-]?)?access[_.-]?key[_.-]?id|api[_.-]?key|private[_.-]?key|authorization|cookie|set-cookie|session[_.-]?id|signing[_.-]?(?:secret|key|value)|x-amz-(?:credential|signature))$/iu;
const diagnosticAssignmentPattern =
  /(?:^|\s|[(\[{,;])["']?([A-Za-z][A-Za-z0-9_.-]{0,63})["']?\s*[:=]\s*\S+/gu;
const MIN_PROTOBUF_TIMESTAMP_SECONDS = -62_135_596_800n;
const MAX_PROTOBUF_TIMESTAMP_SECONDS = 253_402_300_799n;
const MAX_PROTOBUF_TIMESTAMP_NANOS = 999_999_999;
const MAX_DIAGNOSTIC_DECODINGS = 8;
const MAX_DIAGNOSTIC_PARAMETER_SCANS = 16;
const MAX_DIAGNOSTIC_SCAN_BYTES = 2 * MAX_CRASH_STACK_BYTES;
const EXACT_TAURI_REVISION = "4af26a3f7f8b692d62cca549bbacd93f5ce90b41";
const EXACT_CEF_REVISION = "150.0.10+g8042e43+chromium-150.0.7871.101";
const diagnosticPlatforms: ReadonlySet<DiagnosticPlatform> = new Set([
  DiagnosticPlatform.MACOS,
  DiagnosticPlatform.WINDOWS,
  DiagnosticPlatform.LINUX,
  DiagnosticPlatform.IOS,
  DiagnosticPlatform.ANDROID,
  DiagnosticPlatform.BROWSER,
]);
const diagnosticArchitectures: ReadonlySet<DiagnosticArchitecture> = new Set([
  DiagnosticArchitecture.X86_64,
  DiagnosticArchitecture.ARM64,
  DiagnosticArchitecture.ARMV7,
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
const diagnosticErrorCodePattern = /^[A-Z][A-Z0-9_]{0,63}$/u;
const forbiddenDiagnosticContentPatterns: ReadonlyArray<RegExp> = [
  /\b(?:browser[._ -]?dom|outerhtml|innerhtml|screenshot|form[._ -]?value|issue[._ -]?body|agent[._ -]?(?:prompt|output)|child[._ -]?env|(?:request|response)[._ -]?(?:headers?|bod(?:y|ies))|shortcut[._ -]?(?:key|keystroke))\b/iu,
  /https?:\/\/[^\s]*#/iu,
  /\b(?:ctrl|control|cmd|command|meta|alt|option|shift)\s*[+-]\s*[a-z0-9]/iu,
];

const forbiddenLocalPathPatterns: ReadonlyArray<RegExp> = [
  // Keep this lookbehind-free while iOS 16.0-16.3 system webviews are supported.
  /(?:^(?:[\s\p{P}])?|[^:][\s\p{P}=]|:\s+)(?:[A-Za-z]:[\\/][^\s]*|\\\\[^\s]+|~\/[^\s]+|\/(?!\/)[^\s]+)/u,
  // Relative paths require explicit prefixes or structural/file evidence so labels such as
  // React/Native, iOS/18.6, and 1.0.0/42 remain valid diagnostic text.
  /(?:^|[\s([{<"'=:])\.{1,2}[\\/](?:[\p{L}\p{N}_@.-]+[\\/])*[\p{L}\p{N}_@.-]+(?::\d+){0,2}(?=$|[\s\p{P}])/u,
  /(?:^|[\s([{<"'=:])(?:[\p{L}\p{N}_@.-]+[\\/])+(?:\.[\p{L}\p{N}_@.-]+|[\p{L}\p{N}_@.-]+\.[\p{L}][\p{L}\p{N}]*|Dockerfile|Makefile)(?::\d+){0,2}(?=$|[\s\p{P}])/u,
  /(?:^|[\s([{<"'=:])(?:[\p{L}\p{N}_@.-]+[\\/])+[\p{L}\p{N}_@.-]+:\d+(?::\d+)?(?=$|[\s\p{P}])/u,
  /(?:^|[\s([{<"'=:])[\p{L}\p{N}_@.-]+\.[\p{L}][\p{L}\p{N}]*:\d+(?::\d+)?(?=$|[\s\p{P}])/u,
];

const forbiddenSensitiveTextPatterns: ReadonlyArray<RegExp> = [
  /file:\/\/[^\s]*/iu,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----/u,
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/u,
  /\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/u,
  /\bBearer\s+\S+/iu,
  /\bAuthorization\s*:\s*(?:Basic|Bearer)\s+\S+/iu,
  /\b(?:[\p{L}\p{N}]+_)*(?:password|passwd|pwd|pat|secret(?:_access_key)?|token|client[_.-]?secret|(?:access|refresh|id)[_.-]?token|access[_.-]?key[_.-]?id|api[_.-]?key|private[_.-]?key|authorization|cookie|set-cookie|session[_.-]?id|signing[_.-]?(?:secret|key|value))\b["']?\s*[:=]\s*\S+/iu,
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
  if (canonicalizeSettingsJson(parsed) !== source) {
    throw new TypeError("settings JSON must use RFC 8785 canonical encoding");
  }
  return parsed;
}

export function validateAdminReason(reason: string, publicAssetBaseUrl: string): void {
  if (!unicodeNonWhitespacePattern.test(reason)) {
    throw new TypeError("reason must contain at least one non-whitespace character");
  }
  if (reason.includes("\0")) {
    throw new TypeError("reason must not contain NUL characters");
  }
  validateSensitiveText(
    reason,
    MAX_ADMIN_REASON_BYTES,
    "reason",
    parsePublicAssetBaseUrl(publicAssetBaseUrl),
  );
}

export function validateCrashReport(report: SubmitCrashReportRequest): void {
  if (report.reportSchemaVersion !== 1) {
    throw new RangeError("reportSchemaVersion must be 1");
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
  const browser = report.clientBuild.platform === DiagnosticPlatform.BROWSER;
  if (!diagnosticArchitectures.has(report.clientBuild.architecture)
      && !(browser && report.clientBuild.architecture === DiagnosticArchitecture.UNSPECIFIED)) {
    throw new TypeError("clientBuild.architecture must be recognized or unknown only in browsers");
  }
  if (report.clientBuild.architecture === DiagnosticArchitecture.ARMV7
      && report.clientBuild.platform !== DiagnosticPlatform.ANDROID) {
    throw new TypeError("clientBuild.architecture ARMv7 is supported only on Android");
  }
  if (!diagnosticComponents.has(report.component)) {
    throw new TypeError("component must be a recognized nonzero value");
  }
  if (!diagnosticSeverities.has(report.severity)) {
    throw new TypeError("severity must be a recognized nonzero value");
  }
  if (report.clientCorrelationId === undefined) {
    throw new TypeError("clientCorrelationId is required");
  }
  assertUuidV7(report.clientCorrelationId.value);
  if (report.relatedCorrelationIds.length > MAX_CRASH_RELATED_CORRELATIONS) {
    throw new RangeError(`relatedCorrelationIds must not exceed ${MAX_CRASH_RELATED_CORRELATIONS}`);
  }
  if (
    report.durationMilliseconds < 0n ||
    report.durationMilliseconds > MAX_CRASH_DURATION_MILLISECONDS
  ) {
    throw new RangeError("durationMilliseconds must be between zero and 24 hours");
  }
  if (!diagnosticErrorCodePattern.test(report.errorCode)) {
    throw new TypeError("errorCode must be an enum-style classification");
  }

  validateDiagnosticText(
    report.clientBuild.appVersion,
    MAX_CRASH_IDENTIFIER_BYTES,
    "clientBuild.appVersion",
  );
  validateDiagnosticText(
    report.clientBuild.buildId,
    MAX_CRASH_IDENTIFIER_BYTES,
    "clientBuild.buildId",
  );
  validateDiagnosticText(
    report.clientBuild.osVersion,
    MAX_CRASH_IDENTIFIER_BYTES,
    "clientBuild.osVersion",
  );
  validateDiagnosticText(
    report.clientBuild.tauriRevision,
    MAX_CRASH_IDENTIFIER_BYTES,
    "clientBuild.tauriRevision",
  );
  validateDiagnosticText(
    report.clientBuild.cefRevision,
    MAX_CRASH_IDENTIFIER_BYTES,
    "clientBuild.cefRevision",
  );
  if (!report.clientBuild.appVersion || !report.clientBuild.buildId || !report.clientBuild.osVersion) {
    throw new TypeError("clientBuild version and operating-system fields must not be empty");
  }
  if (browser !== (report.clientBuild.tauriRevision.length === 0)) {
    throw new TypeError(
      "clientBuild.tauriRevision must be exact on native hosts and empty in browsers",
    );
  }
  if (!browser && report.clientBuild.tauriRevision !== EXACT_TAURI_REVISION) {
    throw new TypeError("clientBuild.tauriRevision must be the supported native revision");
  }
  const desktop = report.clientBuild.platform === DiagnosticPlatform.MACOS
    || report.clientBuild.platform === DiagnosticPlatform.WINDOWS
    || report.clientBuild.platform === DiagnosticPlatform.LINUX;
  if (desktop ? report.clientBuild.cefRevision !== EXACT_CEF_REVISION : report.clientBuild.cefRevision.length !== 0) {
    throw new TypeError("clientBuild.cefRevision must be the supported desktop revision or empty on mobile and browser hosts");
  }
  validateDiagnosticText(report.redactedSummary, MAX_CRASH_SUMMARY_BYTES, "redactedSummary");
  validateDiagnosticText(
    report.redactedStackTrace,
    MAX_CRASH_STACK_BYTES,
    "redactedStackTrace",
  );
  const stackLines = report.redactedStackTrace.split("\n");
  if (stackLines.length > MAX_CRASH_STACK_LINES) {
    throw new RangeError(`redactedStackTrace must not exceed ${MAX_CRASH_STACK_LINES} lines`);
  }
  if (stackLines.some((line) => textEncoder.encode(line).byteLength > MAX_CRASH_STACK_LINE_BYTES)) {
    throw new RangeError(`redactedStackTrace lines must not exceed ${MAX_CRASH_STACK_LINE_BYTES} bytes`);
  }
  const seenCorrelations = new Set([report.clientCorrelationId.value]);
  for (const correlationId of report.relatedCorrelationIds) {
    assertUuidV7(correlationId.value);
    if (seenCorrelations.has(correlationId.value)) {
      throw new TypeError("correlation identifiers must be unique");
    }
    seenCorrelations.add(correlationId.value);
  }
}

function validateDiagnosticText(value: string, maximum: number, field: string): void {
  validateTextShape(value, maximum, field);
  if (containsForbiddenDiagnosticContent(value)) {
    throw new TypeError(`${field} contains prohibited diagnostic content`);
  }
}

function containsForbiddenDiagnosticContent(value: string): boolean {
  const budget: DiagnosticScanBudget = {
    remainingBytes: MAX_DIAGNOSTIC_SCAN_BYTES,
    remainingParameters: MAX_DIAGNOSTIC_PARAMETER_SCANS,
  };
  return containsForbiddenDiagnosticContentWithBudget(value, budget);
}

function containsForbiddenDiagnosticContentWithBudget(
  value: string,
  budget: DiagnosticScanBudget,
): boolean {
  let decodings = 0;
  for (;;) {
    const valueBytes = textEncoder.encode(value).byteLength;
    if (valueBytes > budget.remainingBytes) return true;
    budget.remainingBytes -= valueBytes;
    if (
      forbiddenDiagnosticContentPatterns.some((pattern) => pattern.test(value)) ||
      forbiddenSensitiveTextPatterns.some((pattern) => pattern.test(value)) ||
      containsForbiddenCredentialAssignment(value) ||
      containsForbiddenLocalPath(value) ||
      containsForbiddenUrlContent(value, {
        allowedProtocols: webDiagnosticUrlProtocols,
        diagnosticBudget: budget,
      })
    ) {
      return true;
    }
    const decoded = decodePercentEncodedOctets(value);
    if (decoded === value) return false;
    if (decodings === MAX_DIAGNOSTIC_DECODINGS) return true;
    value = decoded;
    decodings += 1;
  }
}

function containsForbiddenCredentialAssignment(value: string): boolean {
  const decoded = decodePercentEncodedOctets(value);
  for (const candidate of decoded === value ? [value] : [value, decoded]) {
    for (const match of candidate.matchAll(diagnosticAssignmentPattern)) {
      if (credentialParameterNamePattern.test(match[1] ?? "")) {
        return true;
      }
    }
  }
  return false;
}

function validateSensitiveText(
  value: string,
  maximum: number,
  field: string,
  publicAssetBaseUrl?: URL,
): void {
  validateTextShape(value, maximum, field);
  const forbiddenContent =
    publicAssetBaseUrl === undefined
      ? "sensitive or local-path"
      : "sensitive, public asset locator, or local-path";
  if (containsForbiddenLocalPath(value)) {
    throw new TypeError(`${field} contains forbidden ${forbiddenContent} content`);
  }
  for (const pattern of forbiddenSensitiveTextPatterns) {
    if (pattern.test(value)) {
      throw new TypeError(`${field} contains forbidden ${forbiddenContent} content`);
    }
  }
  const urlPolicy = publicAssetBaseUrl === undefined ? {} : { publicAssetBaseUrl };
  if (containsForbiddenUrlContent(value, urlPolicy)) {
    throw new TypeError(`${field} contains forbidden ${forbiddenContent} content`);
  }
}

function validateTextShape(value: string, maximum: number, field: string): void {
  assertWellFormedUnicode(value, field);
  if (value.includes("\0")) {
    throw new TypeError(`${field} must not contain NUL bytes`);
  }
  if (textEncoder.encode(value).byteLength > maximum) {
    throw new RangeError(`${field} must not exceed ${maximum} UTF-8 bytes`);
  }
}

function containsForbiddenLocalPath(value: string): boolean {
  if (matchesForbiddenLocalPath(value)) {
    return true;
  }

  let previousEnd = 0;
  for (const match of value.matchAll(urlPattern)) {
    const matchedUrl = match[0];
    if (matchedUrl === undefined || match.index === undefined) {
      continue;
    }

    if (
      matchesForbiddenLocalPath(decodePercentEncodedOctets(value.slice(previousEnd, match.index)))
    ) {
      return true;
    }

    // A percent-encoded Windows drive path is URL-shaped to the platform parser, but it is
    // still a local path. Keep other URL spans encoded so remote URL paths are not mistaken
    // for local filesystem paths after decoding.
    if (
      encodedWindowsDrivePathPattern.test(matchedUrl) &&
      matchesForbiddenLocalPath(decodePercentEncodedOctets(matchedUrl))
    ) {
      return true;
    }
    previousEnd = match.index + matchedUrl.length;
  }

  return matchesForbiddenLocalPath(decodePercentEncodedOctets(value.slice(previousEnd)));
}

function matchesForbiddenLocalPath(value: string): boolean {
  return forbiddenLocalPathPatterns.some((pattern) => pattern.test(value));
}

function decodePercentEncodedOctets(value: string): string {
  return value.replace(percentEncodedOctetsPattern, (encoded) => {
    try {
      return decodeURIComponent(encoded);
    } catch {
      return encoded;
    }
  });
}

function parsePublicAssetBaseUrl(value: string): URL {
  assertWellFormedUnicode(value, "publicAssetBaseUrl");
  try {
    decodeURI(value);
    return new URL(value);
  } catch {
    throw new TypeError("publicAssetBaseUrl must be an absolute URL");
  }
}

interface DiagnosticScanBudget {
  remainingBytes: number;
  remainingParameters: number;
}

interface UrlScanPolicy {
  readonly publicAssetBaseUrl?: URL;
  readonly allowedProtocols?: ReadonlySet<string>;
  readonly diagnosticBudget?: DiagnosticScanBudget;
}

function containsForbiddenUrlContent(value: string, policy: UrlScanPolicy = {}): boolean {
  let previousEnd = 0;
  for (const match of value.matchAll(urlPattern)) {
    const matchedUrl = match[0];
    if (matchedUrl === undefined || match.index === undefined) {
      continue;
    }
    if (
      policy.diagnosticBudget !== undefined &&
      containsForbiddenRelativeUrlContent(value.slice(previousEnd, match.index), policy)
    ) {
      return true;
    }
    previousEnd = match.index + matchedUrl.length;
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
      if (policy.allowedProtocols !== undefined) return true;
      continue;
    }

    if (
      url.protocol === "file:" ||
      (policy.allowedProtocols !== undefined &&
        (!policy.allowedProtocols.has(url.protocol) ||
          !webDiagnosticUrlAuthorityPattern.test(candidate) ||
          url.hostname === ""))
    ) {
      return true;
    }
    if (
      policy.publicAssetBaseUrl !== undefined &&
      isPublicAssetLocator(url, policy.publicAssetBaseUrl)
    ) {
      return true;
    }
    if (url.username !== "" || url.password !== "") {
      return true;
    }
    if (
      containsForbiddenParameterContent(url.search.slice(1), policy) ||
      containsForbiddenParameterContent(url.hash.slice(1), policy)
    ) {
      return true;
    }
  }
  return (
    policy.diagnosticBudget !== undefined &&
    containsForbiddenRelativeUrlContent(value.slice(previousEnd), policy)
  );
}

function containsForbiddenRelativeUrlContent(value: string, policy: UrlScanPolicy): boolean {
  for (const match of value.matchAll(urlParametersPattern)) {
    const matchedParameters = match[0];
    if (matchedParameters === undefined) {
      continue;
    }
    const parameters = matchedParameters.slice(1).replace(trailingUrlPunctuationPattern, "");
    if (containsForbiddenParameterContent(parameters, policy)) {
      return true;
    }
  }
  return false;
}

function isPublicAssetLocator(url: URL, publicAssetBaseUrl: URL): boolean {
  if (url.origin !== publicAssetBaseUrl.origin) {
    return false;
  }

  const basePath = decodeURIComponent(publicAssetBaseUrl.pathname).replace(/\/+$/u, "");
  const candidatePath = decodeURIComponent(url.pathname);
  return (
    basePath === "" || candidatePath === basePath || candidatePath.startsWith(`${basePath}/`)
  );
}

function containsForbiddenParameterContent(
  parameters: string,
  policy: UrlScanPolicy,
): boolean {
  if (policy.diagnosticBudget !== undefined) {
    const parameterBytes = textEncoder.encode(parameters).byteLength;
    if (parameterBytes > policy.diagnosticBudget.remainingBytes) return true;
    policy.diagnosticBudget.remainingBytes -= parameterBytes;
  }
  for (const parameter of parameters.split(/[&;]/u)) {
    if (parameter === "") continue;
    if (policy.diagnosticBudget !== undefined) {
      if (policy.diagnosticBudget.remainingParameters === 0) return true;
      policy.diagnosticBudget.remainingParameters -= 1;
    }
    const separatorIndex = parameter.search(/[=:]/u);
    const encodedName = separatorIndex === -1 ? parameter : parameter.slice(0, separatorIndex);
    const encodedValue = separatorIndex === -1 ? "" : parameter.slice(separatorIndex + 1);

    let name: string;
    let value: string;
    try {
      name = decodeURIComponent(encodedName.replace(/\+/gu, " "));
      value = decodeURIComponent(encodedValue.replace(/\+/gu, " "));
    } catch {
      return true;
    }
    if (credentialParameterNamePattern.test(name)) {
      return true;
    }
    if (policy.diagnosticBudget !== undefined) {
      if (
        containsForbiddenDiagnosticContentWithBudget(value, policy.diagnosticBudget) ||
        (value !== encodedValue && containsForbiddenParameterContent(value, policy))
      ) {
        return true;
      }
    } else {
      if (
        containsForbiddenLocalPath(value) ||
        forbiddenSensitiveTextPatterns.some((pattern) => pattern.test(value)) ||
        containsForbiddenCredentialAssignment(value) ||
        containsForbiddenUrlContent(value, policy) ||
        (value !== encodedValue && containsForbiddenParameterContent(value, policy))
      ) {
        return true;
      }
    }
  }
  return false;
}

type CanonicalizationFrame =
  | { readonly kind: "token"; readonly value: string }
  | { readonly kind: "value"; readonly value: unknown };

export function canonicalizeSettingsJson(value: unknown): string {
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

export function encodeCanonicalSettingsJson(value: unknown): Uint8Array {
  const encoded = textEncoder.encode(canonicalizeSettingsJson(value));
  if (encoded.byteLength > MAX_SETTINGS_JSON_BYTES) {
    throw new RangeError(`settings JSON must not exceed ${MAX_SETTINGS_JSON_BYTES} bytes`);
  }
  return encoded;
}
