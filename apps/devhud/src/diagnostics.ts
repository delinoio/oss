import { create, toJsonString, type JsonValue } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  ClientBuildSchema,
  DiagnosticArchitecture,
  DiagnosticComponent,
  DiagnosticPlatform,
  DiagnosticSeverity,
  SubmitCrashReportRequestSchema,
  UuidV7Schema,
  validateCrashReport,
  type SubmitCrashReportRequest,
} from "@delinoio/devhud-api-client";
import { RuntimePlatform, type RuntimeSnapshot } from "./native-bridge";

export const DiagnosticsStorageKey = "devhud.diagnostics.v1.events";
export const DiagnosticsCorrelationsKey = "devhud.diagnostics.v1.correlations";
export const DiagnosticsRetentionDays = 7;
export const DiagnosticsMaximumEvents = 500;
export const DiagnosticsMaximumStorageBytes = 1024 * 1024;
export const DiagnosticsMaximumExportBytes = 1024 * 1024;

const maximumStackFrames = 64;
const maximumRelatedCorrelations = 32;
const maximumDepth = 5;
const maximumCollectionEntries = 64;
const maximumStringBytes = 512;
const exactTauriRevision = "4af26a3f7f8b692d62cca549bbacd93f5ce90b41";
const exactCefRevision = "150.0.10+g8042e43+chromium-150.0.7871.101";
const textEncoder = new TextEncoder();

const forbiddenValue = /(?:authorization|bearer\s|github[_-]?pat|access[_-]?token|refresh[_-]?token|r2[_-]?(?:secret|token|key)|signing[_-]?(?:secret|key)|-----BEGIN [A-Z ]*PRIVATE KEY-----|\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b|\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b|\bAKIA[0-9A-Z]{16}\b|browser.?dom|innerhtml|outerhtml|screenshot|form.?value|issue.?body|agent.?(?:prompt|output)|child.?env|https?:\/\/\S*|(?:^|[\s(\[{<"'=:])(?:[a-z]:[\\/]\S*|\\\\\S+|~\/\S+|\/[^/\s]\S*)|(?:ctrl|control|cmd|command|meta|alt|option|shift)\s*[+-]\s*[a-z0-9])/iu;
const safeCode = /^[A-Z][A-Z0-9_]{0,63}$/u;
const safeFrameName = /(?:^|\s)(?:at\s+)?([A-Za-z_$][A-Za-z0-9_$.<>-]{0,95})/u;

export const DiagnosticOutcome = {
  Failed: "failed",
  Fatal: "fatal",
} as const;
export type DiagnosticOutcome = (typeof DiagnosticOutcome)[keyof typeof DiagnosticOutcome];

export interface LocalDiagnosticEvent {
  readonly schemaVersion: 1;
  readonly correlationId: string;
  readonly occurredAt: string;
  readonly durationMilliseconds: number;
  readonly component: DiagnosticComponent;
  readonly severity: DiagnosticSeverity;
  readonly outcome: DiagnosticOutcome;
  readonly errorCode: string;
  readonly summary: string;
  readonly stackFrames: readonly string[];
  readonly relatedCorrelationIds: readonly string[];
  readonly build: DiagnosticBuild;
}

export interface DiagnosticBuild {
  readonly appVersion: string;
  readonly buildId: string;
  readonly platform: DiagnosticPlatform;
  readonly architecture: DiagnosticArchitecture;
  readonly osVersion: string;
  readonly tauriRevision: string;
  readonly cefRevision: string;
}

export interface CaptureDiagnosticInput {
  readonly component: DiagnosticComponent;
  readonly severity: DiagnosticSeverity;
  readonly errorCode: string;
  readonly error?: unknown;
  readonly occurredAt?: Date;
  readonly startedAtMilliseconds?: number;
  readonly relatedCorrelationIds?: readonly string[];
}

export interface PreparedDiagnosticsBundle {
  readonly correlationId: string;
  readonly request: SubmitCrashReportRequest;
  readonly requestJson: string;
  readonly exportJson: string;
}

export const DiagnosticConnectOperation = {
  Bootstrap: "bootstrap",
  Account: "account",
  Settings: "settings",
  Upload: "upload",
  Diagnostics: "diagnostics",
  Other: "other",
} as const;
export type DiagnosticConnectOperation = (typeof DiagnosticConnectOperation)[keyof typeof DiagnosticConnectOperation];

export interface DiagnosticCorrelationEvent {
  readonly source: "connect-response";
  readonly correlationId: string;
  readonly operation: DiagnosticConnectOperation;
  readonly occurredAt: string;
  readonly durationMilliseconds: number;
}

export function uuidV7(nowMilliseconds = Date.now(), random = crypto.getRandomValues(new Uint8Array(10))): string {
  if (!Number.isSafeInteger(nowMilliseconds) || nowMilliseconds < 0 || random.byteLength !== 10) throw new TypeError("invalid UUID v7 input");
  const bytes = new Uint8Array(16);
  let timestamp = BigInt(nowMilliseconds);
  for (let index = 5; index >= 0; index -= 1) {
    bytes[index] = Number(timestamp & 0xffn);
    timestamp >>= 8n;
  }
  bytes[6] = 0x70 | (random[0]! & 0x0f);
  bytes[7] = random[1]!;
  bytes[8] = 0x80 | (random[2]! & 0x3f);
  bytes.set(random.slice(3), 9);
  const hexadecimal = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hexadecimal.slice(0, 8)}-${hexadecimal.slice(8, 12)}-${hexadecimal.slice(12, 16)}-${hexadecimal.slice(16, 20)}-${hexadecimal.slice(20)}`;
}

export function diagnosticBuild(runtime: RuntimeSnapshot): DiagnosticBuild {
  return {
    appVersion: runtime.appVersion,
    buildId: runtime.buildId,
    platform: diagnosticPlatform(runtime),
    architecture: diagnosticArchitecture(runtime.architecture),
    osVersion: boundedSafeText(runtime.osVersion, "system"),
    tauriRevision: runtime.tauriRevision,
    cefRevision: runtime.cefRevision,
  };
}

export function captureDiagnosticEvent(runtime: RuntimeSnapshot, input: CaptureDiagnosticInput, now = Date.now()): LocalDiagnosticEvent {
  if (!safeCode.test(input.errorCode)) throw new TypeError("diagnostic error code must be an enum classification");
  if (!isDiagnosticComponent(input.component) || !isDiagnosticSeverity(input.severity)) throw new TypeError("diagnostic classifications must be specified");
  const occurredAt = input.occurredAt ?? new Date(now);
  const duration = input.startedAtMilliseconds === undefined ? 0 : Math.max(0, Math.min(86_400_000, Math.round(now - input.startedAtMilliseconds)));
  const related = [...new Set((input.relatedCorrelationIds ?? []).filter(isUuidV7))].slice(0, maximumRelatedCorrelations);
  const event: LocalDiagnosticEvent = {
    schemaVersion: 1,
    correlationId: uuidV7(occurredAt.getTime()),
    occurredAt: occurredAt.toISOString(),
    durationMilliseconds: duration,
    component: input.component,
    severity: input.severity,
    outcome: input.severity === DiagnosticSeverity.FATAL ? DiagnosticOutcome.Fatal : DiagnosticOutcome.Failed,
    errorCode: input.errorCode,
    summary: "A classified application failure was captured.",
    stackFrames: Object.freeze(redactedStackFrames(input.error)),
    relatedCorrelationIds: Object.freeze(related),
    build: Object.freeze(diagnosticBuild(runtime)),
  };
  if (!isLocalDiagnosticEvent(event)) throw new TypeError("runtime diagnostics metadata is invalid");
  return Object.freeze(event);
}

export function redactedStackFrames(error: unknown): string[] {
  const stack = error instanceof Error ? error.stack : undefined;
  if (typeof stack !== "string") return [];
  const frames: string[] = [];
  for (const line of stack.split("\n").slice(1)) {
    const match = safeFrameName.exec(line.trim());
    if (!match?.[1]) continue;
    const frame = `at ${match[1]}`;
    if (forbiddenValue.test(frame)) continue;
    frames.push(frame);
    if (frames.length === maximumStackFrames) break;
  }
  return frames;
}

// This generic redactor is deliberately recursive because imported logs and
// thrown objects can hide credentials inside arrays or nested properties.
export function redactDiagnosticValue(value: unknown, depth = 0, seen = new WeakSet<object>()): JsonValue | undefined {
  if (depth > maximumDepth || value === null || value === undefined) return value === null ? null : undefined;
  if (typeof value === "boolean" || typeof value === "number") return Number.isFinite(value as number) ? value : undefined;
  if (typeof value === "string") return safeDiagnosticString(value);
  if (typeof value === "bigint") return value.toString();
  if (typeof value !== "object" || isDOMLike(value) || seen.has(value)) return undefined;
  seen.add(value);
  if (Array.isArray(value)) {
    const output: JsonValue[] = [];
    for (const item of value.slice(0, maximumCollectionEntries)) {
      const redacted = redactDiagnosticValue(item, depth + 1, seen);
      if (redacted !== undefined) output.push(redacted);
    }
    return output;
  }
  const output: Record<string, JsonValue> = {};
  for (const [key, item] of Object.entries(value).slice(0, maximumCollectionEntries)) {
    if (isForbiddenDiagnosticKey(key)) continue;
    const redacted = redactDiagnosticValue(item, depth + 1, seen);
    if (redacted !== undefined) output[key] = redacted;
  }
  return output;
}

function isForbiddenDiagnosticKey(key: string): boolean {
  const normalized = key.toLowerCase().replace(/[^a-z0-9]/gu, "");
  if (["form", "value", "body", "output", "pat", "dom", "html"].includes(normalized)) return true;
  return [
    "authorization", "bearer", "token", "githubpat", "password", "secret", "cookie", "session",
    "r2", "signing", "browserdom", "innerhtml", "outerhtml", "screenshot", "url", "fragment",
    "formvalue", "formdata", "formfield", "issuebody", "prompt", "agentoutput", "localagent",
    "environment", "childenv", "path", "shortcut", "keystroke", "keybinding",
  ].some((term) => normalized.includes(term));
}

export function appendDiagnosticEvent(storage: Storage, event: LocalDiagnosticEvent, now = Date.now()): readonly LocalDiagnosticEvent[] {
  const normalized = normalizeLocalDiagnosticEvent(event);
  if (!normalized) throw new TypeError("local diagnostic event is invalid");
  const { events, serialized } = boundedDiagnosticEvents([...readDiagnosticEvents(storage, now), normalized], now);
  try { storage.setItem(DiagnosticsStorageKey, serialized); } catch { /* Local troubleshooting remains best-effort in memory-constrained hosts. */ }
  return events;
}

export function readDiagnosticEvents(storage: Storage, now = Date.now()): LocalDiagnosticEvent[] {
  try {
    const persisted = storage.getItem(DiagnosticsStorageKey);
    if (persisted === null) return [];
    const parsed: unknown = JSON.parse(persisted);
    if (!Array.isArray(parsed)) {
      removeStorageKey(storage, DiagnosticsStorageKey);
      return [];
    }
    const { events, serialized } = boundedDiagnosticEvents(parsed.map(normalizeLocalDiagnosticEvent).filter((event): event is LocalDiagnosticEvent => event !== null), now);
    persistPrunedCollection(storage, DiagnosticsStorageKey, persisted, serialized, events.length);
    return events;
  } catch {
    removeStorageKey(storage, DiagnosticsStorageKey);
    return [];
  }
}

export function clearDiagnosticEvents(storage: Pick<Storage, "removeItem">): void {
  try { storage.removeItem(DiagnosticsStorageKey); storage.removeItem(DiagnosticsCorrelationsKey); } catch { /* Native cleanup still proceeds independently. */ }
}

export function appendDiagnosticCorrelation(storage: Storage, correlationId: string | null, procedure: string, durationMilliseconds: number, now = Date.now()): void {
  if (!isUuidV7(correlationId)) return;
  const correlations = readDiagnosticCorrelations(storage, now);
  correlations.push({ source: "connect-response", correlationId, operation: connectOperation(procedure), occurredAt: new Date(now).toISOString(), durationMilliseconds: Math.max(0, Math.min(86_400_000, Math.round(durationMilliseconds))) });
  try { storage.setItem(DiagnosticsCorrelationsKey, JSON.stringify(correlations.slice(-128))); } catch { /* Correlation breadcrumbs are optional offline data. */ }
}

export function readDiagnosticCorrelations(storage: Storage, now = Date.now()): DiagnosticCorrelationEvent[] {
  try {
    const persisted = storage.getItem(DiagnosticsCorrelationsKey);
    if (persisted === null) return [];
    const value: unknown = JSON.parse(persisted);
    if (!Array.isArray(value)) {
      removeStorageKey(storage, DiagnosticsCorrelationsKey);
      return [];
    }
    const correlations = value.filter((candidate): candidate is DiagnosticCorrelationEvent => {
      if (candidate === null || typeof candidate !== "object") return false;
      const record = candidate as Partial<DiagnosticCorrelationEvent>;
      return record.source === "connect-response" && isUuidV7(record.correlationId) && Object.values<string>(DiagnosticConnectOperation).includes(record.operation ?? "") && typeof record.occurredAt === "string" && isWithinDiagnosticsRetention(record.occurredAt, now) && Number.isSafeInteger(record.durationMilliseconds) && record.durationMilliseconds! >= 0 && record.durationMilliseconds! <= 86_400_000;
    }).slice(-128);
    const serialized = JSON.stringify(correlations);
    persistPrunedCollection(storage, DiagnosticsCorrelationsKey, persisted, serialized, correlations.length);
    return correlations;
  } catch {
    removeStorageKey(storage, DiagnosticsCorrelationsKey);
    return [];
  }
}

export function recentDiagnosticCorrelationIds(storage: Storage, now = Date.now()): string[] {
  return [...new Set(readDiagnosticCorrelations(storage, now).map((event) => event.correlationId))].slice(-maximumRelatedCorrelations);
}

export function prepareDiagnosticsBundle(event: LocalDiagnosticEvent, allEvents: readonly LocalDiagnosticEvent[]): PreparedDiagnosticsBundle {
  const request = deepFreeze(create(SubmitCrashReportRequestSchema, {
    reportSchemaVersion: 1,
    clientBuild: create(ClientBuildSchema, event.build),
    occurredAt: timestampFromDate(new Date(event.occurredAt)),
    component: event.component,
    severity: event.severity,
    errorCode: event.errorCode,
    redactedSummary: event.summary,
    redactedStackTrace: event.stackFrames.join("\n"),
    relatedCorrelationIds: event.relatedCorrelationIds.map((value) => create(UuidV7Schema, { value })),
    clientCorrelationId: create(UuidV7Schema, { value: event.correlationId }),
    durationMilliseconds: BigInt(event.durationMilliseconds),
  }));
  validateCrashReport(request);
  const requestJson = toJsonString(SubmitCrashReportRequestSchema, request, { prettySpaces: 2 });
  const safeEvents = allEvents.slice(-DiagnosticsMaximumEvents).map((candidate) => redactDiagnosticValue(candidate)).filter((candidate) => candidate !== undefined);
  const exportBundle = { schemaVersion: 1, generatedAt: new Date().toISOString(), crashReport: JSON.parse(requestJson), localEvents: safeEvents };
  let exportJson = JSON.stringify(exportBundle, null, 2);
  while (textEncoder.encode(exportJson).byteLength > DiagnosticsMaximumExportBytes && safeEvents.length > 0) {
    safeEvents.shift();
    exportJson = JSON.stringify(exportBundle, null, 2);
  }
  if (textEncoder.encode(exportJson).byteLength > DiagnosticsMaximumExportBytes) throw new Error("diagnostics-export-too-large");
  return Object.freeze({ correlationId: event.correlationId, request, requestJson, exportJson });
}

export async function diagnosticsConsentDigest(preview: string): Promise<string> {
  const bytes = await crypto.subtle.digest("SHA-256", textEncoder.encode(preview));
  return [...new Uint8Array(bytes)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

function pruneDiagnosticEvents(events: LocalDiagnosticEvent[], now: number): LocalDiagnosticEvent[] {
  return events.filter((event) => isWithinDiagnosticsRetention(event.occurredAt, now)).slice(-DiagnosticsMaximumEvents);
}

function isWithinDiagnosticsRetention(occurredAt: string, now: number): boolean {
  const timestamp = Date.parse(occurredAt);
  const cutoff = now - DiagnosticsRetentionDays * 24 * 60 * 60 * 1000;
  return Number.isFinite(timestamp) && timestamp >= cutoff && timestamp <= now;
}

function boundedDiagnosticEvents(events: LocalDiagnosticEvent[], now: number): { events: LocalDiagnosticEvent[]; serialized: string } {
  const bounded = pruneDiagnosticEvents(events, now);
  let serialized = JSON.stringify(bounded);
  while (textEncoder.encode(serialized).byteLength > DiagnosticsMaximumStorageBytes && bounded.length > 0) {
    bounded.shift();
    serialized = JSON.stringify(bounded);
  }
  return { events: bounded, serialized };
}

function persistPrunedCollection(storage: Storage, key: string, persisted: string, serialized: string, length: number): void {
  try {
    if (length === 0) storage.removeItem(key);
    else if (serialized !== persisted) storage.setItem(key, serialized);
  } catch { /* Retention remains enforced in memory when Web Storage is unavailable. */ }
}

function removeStorageKey(storage: Pick<Storage, "removeItem">, key: string): void {
  try { storage.removeItem(key); } catch { /* Invalid persisted diagnostics are ignored in unavailable storage. */ }
}

function isLocalDiagnosticEvent(value: unknown): value is LocalDiagnosticEvent {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const event = value as Partial<LocalDiagnosticEvent>;
  const frames = event.stackFrames;
  const related = event.relatedCorrelationIds;
  return event.schemaVersion === 1
    && typeof event.occurredAt === "string" && Number.isFinite(Date.parse(event.occurredAt))
    && isUuidV7(event.correlationId)
    && Number.isSafeInteger(event.durationMilliseconds) && event.durationMilliseconds! >= 0 && event.durationMilliseconds! <= 86_400_000
    && isDiagnosticComponent(event.component) && isDiagnosticSeverity(event.severity)
    && event.outcome === (event.severity === DiagnosticSeverity.FATAL ? DiagnosticOutcome.Fatal : DiagnosticOutcome.Failed)
    && safeCode.test(event.errorCode ?? "")
    && typeof event.summary === "string" && textEncoder.encode(event.summary).byteLength <= 4 * 1024 && !forbiddenValue.test(event.summary)
    && Array.isArray(frames) && frames.length <= maximumStackFrames
    && frames.every((frame) => typeof frame === "string" && textEncoder.encode(frame).byteLength <= maximumStringBytes && !forbiddenValue.test(frame))
    && textEncoder.encode(frames.join("\n")).byteLength <= 32 * 1024
    && Array.isArray(related) && related.length <= maximumRelatedCorrelations
    && related.every(isUuidV7) && new Set(related).size === related.length && !related.includes(event.correlationId)
    && isDiagnosticBuild(event.build);
}

function normalizeLocalDiagnosticEvent(value: unknown): LocalDiagnosticEvent | null {
  if (!isLocalDiagnosticEvent(value)) return null;
  return {
    schemaVersion: 1,
    correlationId: value.correlationId,
    occurredAt: value.occurredAt,
    durationMilliseconds: value.durationMilliseconds,
    component: value.component,
    severity: value.severity,
    outcome: value.outcome,
    errorCode: value.errorCode,
    summary: value.summary,
    stackFrames: [...value.stackFrames],
    relatedCorrelationIds: [...value.relatedCorrelationIds],
    build: { ...value.build },
  };
}

function isDiagnosticBuild(value: unknown): value is DiagnosticBuild {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const build = value as Partial<DiagnosticBuild>;
  const strings = [build.appVersion, build.buildId, build.osVersion, build.tauriRevision, build.cefRevision];
  if (!strings.every((item) => typeof item === "string" && textEncoder.encode(item).byteLength <= 256 && !forbiddenValue.test(item))) return false;
  if (!build.appVersion || !build.buildId || !build.osVersion) return false;
  if (!isDiagnosticPlatform(build.platform)) return false;
  const browser = build.platform === DiagnosticPlatform.BROWSER;
  if (!isDiagnosticArchitecture(build.architecture, browser)) return false;
  if (build.architecture === DiagnosticArchitecture.ARMV7 && build.platform !== DiagnosticPlatform.ANDROID) return false;
  if (browser ? build.tauriRevision !== "" : build.tauriRevision !== exactTauriRevision) return false;
  const desktop = build.platform! >= DiagnosticPlatform.MACOS && build.platform! <= DiagnosticPlatform.LINUX;
  return desktop ? build.cefRevision === exactCefRevision : build.cefRevision === "";
}

function isDiagnosticPlatform(value: unknown): value is DiagnosticPlatform {
  return typeof value === "number" && value >= DiagnosticPlatform.MACOS && value <= DiagnosticPlatform.BROWSER;
}

function isDiagnosticArchitecture(value: unknown, browser = false): value is DiagnosticArchitecture {
  return typeof value === "number" && ((browser && value === DiagnosticArchitecture.UNSPECIFIED)
    || (value >= DiagnosticArchitecture.X86_64 && value <= DiagnosticArchitecture.ARMV7));
}

function isDiagnosticComponent(value: unknown): value is DiagnosticComponent {
  return typeof value === "number" && value >= DiagnosticComponent.APP && value <= DiagnosticComponent.NATIVE_SHELL;
}

function isDiagnosticSeverity(value: unknown): value is DiagnosticSeverity {
  return value === DiagnosticSeverity.ERROR || value === DiagnosticSeverity.FATAL;
}

function safeDiagnosticString(value: string): string | undefined {
  if (value === "" || forbiddenValue.test(value)) return undefined;
  const bytes = textEncoder.encode(value);
  if (bytes.byteLength <= maximumStringBytes) return value;
  return new TextDecoder().decode(bytes.slice(0, maximumStringBytes));
}

function boundedSafeText(value: string, fallback: string): string {
  return safeDiagnosticString(value)?.slice(0, 256) || fallback;
}

function isDOMLike(value: object): boolean {
  const candidate = value as { readonly nodeType?: unknown; readonly nodeName?: unknown };
  return typeof candidate.nodeType === "number" && typeof candidate.nodeName === "string";
}

function isUuidV7(value: unknown): value is string {
  return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(value);
}

function diagnosticPlatform(runtime: RuntimeSnapshot): DiagnosticPlatform {
  if (runtime.platform === RuntimePlatform.Browser) return DiagnosticPlatform.BROWSER;
  if (runtime.platform === RuntimePlatform.Ios) return DiagnosticPlatform.IOS;
  if (runtime.platform === RuntimePlatform.Android) return DiagnosticPlatform.ANDROID;
  if (runtime.operatingSystem === "macos") return DiagnosticPlatform.MACOS;
  if (runtime.operatingSystem === "windows") return DiagnosticPlatform.WINDOWS;
  return DiagnosticPlatform.LINUX;
}

function diagnosticArchitecture(value: string): DiagnosticArchitecture {
  if (value === "aarch64" || value === "arm64") return DiagnosticArchitecture.ARM64;
  if (value === "arm" || value === "armv7") return DiagnosticArchitecture.ARMV7;
  if (value === "x86_64" || value === "amd64") return DiagnosticArchitecture.X86_64;
  return DiagnosticArchitecture.UNSPECIFIED;
}

function connectOperation(procedure: string): DiagnosticConnectOperation {
  if (procedure.includes("Bootstrap")) return DiagnosticConnectOperation.Bootstrap;
  if (procedure.includes("Account")) return DiagnosticConnectOperation.Account;
  if (procedure.includes("Settings")) return DiagnosticConnectOperation.Settings;
  if (procedure.includes("Upload")) return DiagnosticConnectOperation.Upload;
  if (procedure.includes("Diagnostics")) return DiagnosticConnectOperation.Diagnostics;
  return DiagnosticConnectOperation.Other;
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const nested of Object.values(value)) deepFreeze(nested);
  }
  return value;
}
