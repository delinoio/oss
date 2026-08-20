// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Code, ConnectError } from "@connectrpc/connect";
import { toJsonString } from "@bufbuild/protobuf";
import { DiagnosticArchitecture, DiagnosticComponent, DiagnosticPlatform, DiagnosticSeverity, ErrorMetadataSchema, PermissionFailureReason, PermissionFailureSchema, QuotaFailureSchema, QuotaKind, StaticCapability, SubmitCrashReportRequestSchema } from "@delinoio/devhud-api-client";
import { createElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  DiagnosticsMaximumEvents,
  DiagnosticsMaximumExportBytes,
  DiagnosticsCorrelationsKey,
  DiagnosticsRetentionDays,
  DiagnosticsStorageKey,
  appendDiagnosticCorrelation,
  appendDiagnosticEvent,
  beginDiagnosticWriteSuppression,
  captureDiagnosticEvent,
  prepareDiagnosticsBundle,
  readDiagnosticCorrelations,
  recentDiagnosticCorrelationIds,
  readDiagnosticEvents,
  redactDiagnosticValue,
  uuidV7,
  type LocalDiagnosticEvent,
} from "./diagnostics";
import { LifecycleState, RuntimePlatform, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";
import { DiagnosticsPanel, diagnosticsSubmissionBlock } from "./diagnostics-ui";
import { NativeBridgeError, NativeBridgeErrorCode, nativeBridge, validateDiagnosticsExport } from "./native-bridge";
import { messages } from "./localization";
import { clearAllContractedLocalData } from "./local-data";
import * as serviceBoundary from "./service-boundary";

const diagnosticsMutation = vi.hoisted(() => ({ mutateAsync: vi.fn(), isPending: false }));
vi.mock("@connectrpc/connect-query", async (importOriginal) => ({
  ...await importOriginal<typeof import("@connectrpc/connect-query")>(),
  useMutation: () => diagnosticsMutation,
}));

const runtime: RuntimeSnapshot = {
  bridgeVersion: 1,
  platform: RuntimePlatform.Desktop,
  operatingSystem: "linux",
  architecture: "x86_64",
  osVersion: "linux",
  appVersion: "0.1.0",
  buildId: "test-build",
  tauriRevision: "4af26a3f7f8b692d62cca549bbacd93f5ce90b41",
  cefRevision: "150.0.10+g8042e43+chromium-150.0.7871.101",
  lifecycle: LifecycleState.Active,
  capabilities: { secureSettings: true, notifications: false, storeUpdates: false, widgets: false },
};

class RecoverableStorage implements Storage {
  readonly #values = new Map<string, string>();
  rejectRemovals = false;
  rejectWrites = true;

  get length() { return this.#values.size; }
  clear() { this.#values.clear(); }
  getItem(key: string) { return this.#values.get(key) ?? null; }
  key(index: number) { return [...this.#values.keys()][index] ?? null; }
  removeItem(key: string) {
    if (this.rejectRemovals) throw new DOMException("denied", "SecurityError");
    this.#values.delete(key);
  }
  setItem(key: string, value: string) {
    if (this.rejectWrites) throw new DOMException("quota exceeded", "QuotaExceededError");
    this.#values.set(key, value);
  }
}

beforeEach(() => {
  localStorage.clear();
  delete window.showSaveFilePicker;
  diagnosticsMutation.mutateAsync.mockReset();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("diagnostics privacy boundary", () => {
  it("recursively excludes hostile nested values, DOM-like objects, paths, and keystrokes", () => {
    const hostile = {
      safe: "bounded classification",
      Authorization: "Bearer abc",
      nested: [{ githubPat: "ghp_secret", deeper: { r2_secret: "secret", signingKey: "private" } }],
      browser: { dom: { nodeType: 1, nodeName: "FORM", value: "private" }, screenshot: "pixels" },
      issueBody: "private body",
      agentPrompt: "private prompt",
      output: { childEnvironment: { TOKEN: "private" } },
      fullPath: "/home/alice/project/main.ts",
      importedCredential: "ghp_0123456789abcdefghijklmnopqrstuvwxyz",
      importedJwt: "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
      importedAwsKey: "AKIA0123456789ABCDEF",
      importedPrivateKey: "-----BEGIN PRIVATE KEY-----",
      importedAssignment: "password=hunter2",
      importedLocation: "/workspace/project/main.ts",
      encodedImportedLocation: "source=%2Fworkspace%2Fprivate%2Fapp.ts",
      doublyEncodedImportedLocation: "source=%252Fworkspace%252Fprivate%252Fapp.ts",
      request_body: "email=alice@example.test",
      responseHeaders: "Set-Cookie: session=abc",
      shortcut: "Ctrl+Shift+P",
      urlFragment: "https://example.test/path#private",
      customScheme: "devhud://auth/callback",
      customEditorScheme: "subl://open/home/alice/app.ts",
      ftpLocation: "ftp://private.example/repo",
      localFileLocation: "file:///home/alice/app.ts",
      punctuationDelimitedPath: "renderer,/home/alice/project/main.ts",
      safeSlashLabel: "React/Native renderer failed",
    };
    const serialized = JSON.stringify(redactDiagnosticValue(hostile));
    expect(serialized).toContain("bounded classification");
    expect(serialized).toContain("React/Native renderer failed");
    for (const prohibited of ["Bearer", "githubPat", "r2_secret", "signingKey", "nodeType", "screenshot", "issueBody", "agentPrompt", "childEnvironment", "hunter2", "alice@example.test", "session=abc", "/home/alice", "ghp_", "eyJ", "AKIA", "PRIVATE KEY", "/workspace/", "%2Fworkspace", "%252Fworkspace", "Ctrl+", "#private", "devhud://", "subl://", "ftp://", "file://"]) {
      expect(serialized).not.toContain(prohibited);
    }
  });

  it("drops persisted events containing singly or multiply encoded local paths", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const singlyEncoded = { ...fixtureEvent(now - 2), summary: "source=%2Fworkspace%2Fprivate%2Fapp.ts" };
    const multiplyEncoded = { ...fixtureEvent(now - 1), summary: "source=%252Fworkspace%252Fprivate%252Fapp.ts" };
    const safe = fixtureEvent(now);
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([singlyEncoded, multiplyEncoded, safe]));

    expect(readDiagnosticEvents(localStorage, now)).toEqual([safe]);
    expect(JSON.parse(localStorage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual([safe]);
  });

  it("drops persisted events containing structural relative paths before export", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const safe = fixtureEvent(now);
    for (const relativePath of [
      "at render (src/private/customer/app.ts:10:2)",
      "at render (src/private/customer/app.ts)",
      "config/.env",
      "config/Dockerfile",
      "src/private/module:10",
      "./src/private/app.ts",
    ]) {
      const unsafe = { ...fixtureEvent(now - 1), summary: relativePath };
      localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([unsafe, safe]));

      const events = readDiagnosticEvents(localStorage, now);
      expect(events, relativePath).toEqual([safe]);
      expect(JSON.parse(localStorage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual([safe]);
      expect(redactDiagnosticValue({ relativePath })).toEqual({});
      expect(prepareDiagnosticsBundle(safe, events).exportJson).not.toContain(relativePath);
    }

    expect(redactDiagnosticValue({ label: "React/Native iOS/18.6 build 1.0.0/42" })).toEqual({
      label: "React/Native iOS/18.6 build 1.0.0/42",
    });
  });

  it("bounds nested local diagnostic parameter scanning without dropping safe events", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const bounded = { ...fixtureEvent(now - 3), summary: `${"?x=".repeat(16)}safe` };
    const excessive = { ...fixtureEvent(now - 2), summary: `${"?x=".repeat(17)}safe` };
    const pathological = { ...fixtureEvent(now - 1), summary: `${"?x=".repeat(1300)}safe` };
    const safe = fixtureEvent(now);
    localStorage.setItem(
      DiagnosticsStorageKey,
      JSON.stringify([bounded, excessive, pathological, safe]),
    );

    expect(readDiagnosticEvents(localStorage, now)).toEqual([bounded, safe]);
    expect(JSON.parse(localStorage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual([
      bounded,
      safe,
    ]);
    expect(redactDiagnosticValue({ pathological: pathological.summary })).toEqual({});
  });

  it("fails closed when diagnostic percent decoding exceeds its fixed-point bound", () => {
    let encoded = "/workspace/private/app.ts";
    for (let index = 0; index < 10; index += 1) encoded = encodeURIComponent(encoded);

    expect(redactDiagnosticValue({ encoded })).toEqual({});
  });

  it("generates canonical UUID v7 values and keeps stack metadata path-free", () => {
    const id = uuidV7(1_700_000_000_000, new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]));
    expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-8[0-9a-f]{3}-[0-9a-f]{12}$/u);
    const error = new Error("Authorization: Bearer private");
    error.stack = "Error\n    at render (/home/alice/project/App.tsx:1:2)\n    at safeFrame (bundle.js:2:3)";
    const event = captureDiagnosticEvent(runtime, { component: DiagnosticComponent.APP, severity: DiagnosticSeverity.ERROR, errorCode: "APP_FAILURE", error }, 1_700_000_000_000);
    expect(event.stackFrames.join("\n")).not.toContain("/home/");
    expect(event.stackFrames).toEqual(["at render", "at safeFrame"]);
  });

  it("enforces seven-day, event-count, and storage retention while offline", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const recent = fixtureEvent(now);
    const old = fixtureEvent(now - (DiagnosticsRetentionDays + 1) * 86_400_000);
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([old]));
    for (let index = 0; index <= DiagnosticsMaximumEvents; index += 1) appendDiagnosticEvent(localStorage, { ...recent, correlationId: uuidV7(now + index, new Uint8Array(10).fill(index % 255)) }, now);
    const events = readDiagnosticEvents(localStorage, now);
    expect(events).toHaveLength(DiagnosticsMaximumEvents);
    expect(events.some((event) => event.occurredAt === old.occurredAt)).toBe(false);
    expect(new TextEncoder().encode(localStorage.getItem(DiagnosticsStorageKey) ?? "").byteLength).toBeLessThanOrEqual(1024 * 1024);
  }, 30_000);

  it("retains bounded diagnostic events in memory until Web Storage recovers", () => {
    const storage = new RecoverableStorage();
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const recent = fixtureEvent(now);
    for (let index = 0; index <= DiagnosticsMaximumEvents; index += 1) {
      appendDiagnosticEvent(storage, { ...recent, correlationId: uuidV7(now + index, new Uint8Array(10).fill(index % 255)) }, now);
    }

    expect(storage.getItem(DiagnosticsStorageKey)).toBeNull();
    expect(readDiagnosticEvents(storage, now)).toHaveLength(DiagnosticsMaximumEvents);

    storage.rejectWrites = false;
    const recovered = readDiagnosticEvents(storage, now);
    expect(recovered).toHaveLength(DiagnosticsMaximumEvents);
    expect(JSON.parse(storage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual(recovered);
  });

  it("retains bounded diagnostic correlations in memory until Web Storage recovers", () => {
    const storage = new RecoverableStorage();
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    for (let index = 0; index <= 128; index += 1) {
      appendDiagnosticCorrelation(storage, uuidV7(now + index, new Uint8Array(10).fill(index % 255)), "/devhud.v1.DiagnosticsService/SubmitCrashReport", index, now);
    }

    expect(storage.getItem(DiagnosticsCorrelationsKey)).toBeNull();
    expect(readDiagnosticCorrelations(storage, now)).toHaveLength(128);

    storage.rejectWrites = false;
    const recovered = readDiagnosticCorrelations(storage, now);
    expect(recovered).toHaveLength(128);
    expect(JSON.parse(storage.getItem(DiagnosticsCorrelationsKey) ?? "null")).toEqual(recovered);
  });

  it("removes in-memory diagnostic events and correlations during contracted local cleanup", () => {
    const storage = new RecoverableStorage();
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    appendDiagnosticEvent(storage, fixtureEvent(now), now);
    appendDiagnosticCorrelation(storage, fixtureEvent(now).correlationId, "/devhud.v1.DiagnosticsService/SubmitCrashReport", 1, now);
    expect(readDiagnosticEvents(storage, now)).toHaveLength(1);
    expect(readDiagnosticCorrelations(storage, now)).toHaveLength(1);

    expect(clearAllContractedLocalData(storage)).toBe(true);
    expect(readDiagnosticEvents(storage, now)).toEqual([]);
    expect(readDiagnosticCorrelations(storage, now)).toEqual([]);
  });

  it("suppresses diagnostic writers until every active suppression is released", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const event = fixtureEvent(now);
    const releaseFirst = beginDiagnosticWriteSuppression(localStorage);
    const releaseSecond = beginDiagnosticWriteSuppression(localStorage);

    expect(appendDiagnosticEvent(localStorage, event, now)).toEqual([]);
    appendDiagnosticCorrelation(localStorage, event.correlationId, "/devhud.v1.DiagnosticsService/SubmitCrashReport", 1, now);
    expect(localStorage.getItem(DiagnosticsStorageKey)).toBeNull();
    expect(localStorage.getItem(DiagnosticsCorrelationsKey)).toBeNull();

    releaseFirst();
    expect(appendDiagnosticEvent(localStorage, event, now)).toEqual([]);
    releaseSecond();
    expect(appendDiagnosticEvent(localStorage, event, now)).toEqual([event]);
    appendDiagnosticCorrelation(localStorage, event.correlationId, "/devhud.v1.DiagnosticsService/SubmitCrashReport", 1, now);
    expect(localStorage.getItem(DiagnosticsStorageKey)).not.toBeNull();
    expect(localStorage.getItem(DiagnosticsCorrelationsKey)).not.toBeNull();
  });

  it("keeps failed contracted diagnostic cleanup tombstoned until removal succeeds", () => {
    const storage = new RecoverableStorage();
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    storage.rejectWrites = false;
    appendDiagnosticEvent(storage, fixtureEvent(now), now);
    appendDiagnosticCorrelation(storage, fixtureEvent(now).correlationId, "/devhud.v1.DiagnosticsService/SubmitCrashReport", 1, now);
    storage.rejectRemovals = true;

    expect(clearAllContractedLocalData(storage)).toBe(false);
    expect(storage.getItem(DiagnosticsStorageKey)).not.toBeNull();
    expect(storage.getItem(DiagnosticsCorrelationsKey)).not.toBeNull();
    expect(readDiagnosticEvents(storage, now)).toEqual([]);
    expect(readDiagnosticCorrelations(storage, now)).toEqual([]);

    storage.rejectRemovals = false;
    expect(clearAllContractedLocalData(storage)).toBe(true);
    expect(storage.getItem(DiagnosticsStorageKey)).toBeNull();
    expect(storage.getItem(DiagnosticsCorrelationsKey)).toBeNull();
  });

  it("physically removes expired events and correlations during read maintenance", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const expiredAt = now - (DiagnosticsRetentionDays + 1) * 86_400_000;
    const expired = fixtureEvent(expiredAt);
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([expired]));
    localStorage.setItem(DiagnosticsCorrelationsKey, JSON.stringify([{
      source: "connect-response",
      correlationId: expired.correlationId,
      operation: "diagnostics",
      occurredAt: expired.occurredAt,
      durationMilliseconds: 1,
    }]));

    expect(readDiagnosticEvents(localStorage, now)).toEqual([]);
    expect(readDiagnosticCorrelations(localStorage, now)).toEqual([]);
    expect(localStorage.getItem(DiagnosticsStorageKey)).toBeNull();
    expect(localStorage.getItem(DiagnosticsCorrelationsKey)).toBeNull();
  });

  it("physically removes future-dated events and correlations during read maintenance", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const current = fixtureEvent(now);
    const future = fixtureEvent(now + 1);
    const currentCorrelation = {
      source: "connect-response",
      correlationId: current.correlationId,
      operation: "diagnostics",
      occurredAt: current.occurredAt,
      durationMilliseconds: 1,
    };
    const futureCorrelation = {
      ...currentCorrelation,
      correlationId: future.correlationId,
      occurredAt: future.occurredAt,
    };
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([current, future]));
    localStorage.setItem(DiagnosticsCorrelationsKey, JSON.stringify([currentCorrelation, futureCorrelation]));

    expect(readDiagnosticEvents(localStorage, now)).toEqual([current]);
    expect(readDiagnosticCorrelations(localStorage, now)).toEqual([currentCorrelation]);
    expect(JSON.parse(localStorage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual([current]);
    expect(JSON.parse(localStorage.getItem(DiagnosticsCorrelationsKey) ?? "null")).toEqual([currentCorrelation]);
  });

  it("reports browser development without fabricated native runtime revisions", async () => {
    const response = await nativeBridge.request({ operation: "runtime.snapshot" });
    expect(response.kind).toBe("runtime");
    if (response.kind !== "runtime") return;
    expect(response.snapshot).toMatchObject({
      platform: RuntimePlatform.Browser,
      operatingSystem: "browser",
      architecture: "unknown",
      tauriRevision: "",
      cefRevision: "",
    });
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const event = captureDiagnosticEvent(response.snapshot, {
      component: DiagnosticComponent.APP,
      severity: DiagnosticSeverity.ERROR,
      errorCode: "BROWSER_FAILURE",
    }, now);
    const prepared = prepareDiagnosticsBundle(event, [event]);
    expect(prepared.request.clientBuild?.platform).toBe(DiagnosticPlatform.BROWSER);
    expect(prepared.request.clientBuild?.architecture).toBe(DiagnosticArchitecture.UNSPECIFIED);
    expect(prepared.request.clientBuild?.tauriRevision).toBe("");
    expect(prepared.request.clientBuild?.cefRevision).toBe("");
  });

  it("uses reliable client hints for browser architecture", async () => {
    Object.defineProperty(navigator, "userAgentData", {
      configurable: true,
      value: { getHighEntropyValues: async () => ({ architecture: "arm", bitness: "64" }) },
    });
    try {
      const response = await nativeBridge.request({ operation: "runtime.snapshot" });
      expect(response).toMatchObject({ kind: "runtime", snapshot: { architecture: "arm64" } });
    } finally {
      Reflect.deleteProperty(navigator, "userAgentData");
    }
  });

  it("maps 32-bit ARM browser hints to the browser-safe unknown architecture", async () => {
    Object.defineProperty(navigator, "userAgentData", {
      configurable: true,
      value: { getHighEntropyValues: async () => ({ architecture: "arm", bitness: "32" }) },
    });
    try {
      const response = await nativeBridge.request({ operation: "runtime.snapshot" });
      if (response.kind !== "runtime") throw new Error("runtime-snapshot-failed");
      expect(response.snapshot.architecture).toBe("unknown");
      const event = captureDiagnosticEvent(response.snapshot, {
        component: DiagnosticComponent.APP,
        severity: DiagnosticSeverity.ERROR,
        errorCode: "BROWSER_ARM32_FAILURE",
      }, Date.parse("2026-08-17T00:00:00.000Z"));
      expect(prepareDiagnosticsBundle(event, [event]).request.clientBuild?.architecture).toBe(DiagnosticArchitecture.UNSPECIFIED);
    } finally {
      Reflect.deleteProperty(navigator, "userAgentData");
    }
  });

  it("normalizes persisted events to the closed local schema and drops unsafe classifications", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const valid = { ...fixtureEvent(now), persistentUserId: "user-123" };
    const invalid = { ...fixtureEvent(now + 1), component: 999 };
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([valid, invalid]));
    const events = readDiagnosticEvents(localStorage, now);
    expect(events).toHaveLength(1);
    expect(events[0]).not.toHaveProperty("persistentUserId");
  });

  it("drops persisted build metadata that cannot be submitted", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const event = fixtureEvent(now);
    const invalidCefRevision = { ...event, build: { ...event.build, cefRevision: "legacy-cef" } };
    const invalidTauriRevision = { ...event, build: { ...event.build, tauriRevision: "a".repeat(40) } };
    const invalidArmV7 = [
      DiagnosticPlatform.MACOS,
      DiagnosticPlatform.WINDOWS,
      DiagnosticPlatform.LINUX,
      DiagnosticPlatform.IOS,
      DiagnosticPlatform.BROWSER,
    ].map((platform) => {
      const browser = platform === DiagnosticPlatform.BROWSER;
      const mobile = platform === DiagnosticPlatform.IOS;
      return {
        ...event,
        build: {
          ...event.build,
          platform,
          architecture: DiagnosticArchitecture.ARMV7,
          tauriRevision: browser ? "" : event.build.tauriRevision,
          cefRevision: browser || mobile ? "" : event.build.cefRevision,
        },
      };
    });
    const androidArmV7 = {
      ...event,
      build: {
        ...event.build,
        platform: DiagnosticPlatform.ANDROID,
        architecture: DiagnosticArchitecture.ARMV7,
        cefRevision: "",
      },
    };
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([invalidCefRevision, invalidTauriRevision, ...invalidArmV7, androidArmV7]));

    const events = readDiagnosticEvents(localStorage, now);

    expect(events).toEqual([androidArmV7]);
    expect(JSON.parse(localStorage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual([androidArmV7]);
    expect(() => prepareDiagnosticsBundle(events[0]!, events)).not.toThrow();
  });

  it("strips unknown persisted diagnostic build fields before preview and export", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const event = fixtureEvent(now);
    const imported = { ...event, build: { ...event.build, credential: "hunter2" } };
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([imported]));

    const events = readDiagnosticEvents(localStorage, now);

    expect(events).toHaveLength(1);
    expect(events[0]?.build).toEqual(event.build);
    expect(events[0]?.build).not.toHaveProperty("credential");
    expect(localStorage.getItem(DiagnosticsStorageKey)).not.toContain("hunter2");
    expect(prepareDiagnosticsBundle(events[0]!, events).exportJson).not.toContain("hunter2");
  });

  it("drops persisted events containing unlabeled credential shapes", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const safe = fixtureEvent(now);
    for (const credential of [
      "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
      "AKIA0123456789ABCDEF",
      "-----BEGIN PRIVATE KEY-----",
      "password=hunter2",
      "oauth_code: secret",
      "credential=hunter2",
      "credentials: hunter2",
      "password%3Dhunter2",
      "pat=hunter2",
      "session_id=secret",
      "signing_value=secret",
      "r2_access_key_id=0123456789abcdef",
      "DEVHUD_R2_ACCESS_KEY_ID=0123456789abcdef",
      "r2.access-key-id=0123456789abcdef",
      "callback?r2.access-key-id=0123456789abcdef",
      "callback?code=secret",
      "/auth/callback#access_token=secret",
      "auth/callback#access_token=secret",
      "callback?co%64e=secret",
      "auth/callback#access%2Dtoken=secret",
      "callback?safe=code%3Dsecret",
      "callback?safe=x%26code%3Dsecret",
      "callback%3Fcode%3Dsecret",
      "state=ok&code=abc123",
      "state=ok;code=abc123",
    ]) {
      const unsafe = { ...fixtureEvent(now - 1), summary: credential };
      localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([unsafe, safe]));

      const events = readDiagnosticEvents(localStorage, now);
      expect(events).toEqual([safe]);
      expect(JSON.parse(localStorage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual([safe]);
      expect(redactDiagnosticValue({ credential })).toEqual({});
      expect(prepareDiagnosticsBundle(safe, events).exportJson).not.toContain(credential);
    }
    expect(redactDiagnosticValue({ callback: "callback?state=opaque" })).toEqual({
      callback: "callback?state=opaque",
    });
    expect(redactDiagnosticValue({ parameters: "state=opaque&component=renderer" })).toEqual({
      parameters: "state=opaque&component=renderer",
    });
    expect(redactDiagnosticValue({ parameters: "state=opaque;component=renderer" })).toEqual({
      parameters: "state=opaque;component=renderer",
    });
  });

  it("drops persisted events containing shortcut labels", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const safe = fixtureEvent(now);
    for (const shortcut of [
      "shortcut_key=Digit1",
      "shortcut-keystroke: KeyK",
      "shortcut key = Digit2",
    ]) {
      const unsafe = { ...fixtureEvent(now - 1), summary: shortcut };
      localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([unsafe, safe]));

      const events = readDiagnosticEvents(localStorage, now);
      expect(events).toEqual([safe]);
      expect(JSON.parse(localStorage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual([safe]);
      expect(redactDiagnosticValue({ detail: shortcut })).toEqual({});
      expect(prepareDiagnosticsBundle(safe, events).exportJson).not.toContain(shortcut);
    }
  });

  it("drops ill-formed Unicode from persisted events and recursive redaction", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const event = fixtureEvent(now);
    const invalidEvents = [
      { ...event, summary: "bad\ud800" },
      { ...event, stackFrames: ["at bad\udc00"] },
      { ...event, build: { ...event.build, appVersion: "bad\ud800" } },
      { ...event, build: { ...event.build, buildId: "bad\udc00" } },
      { ...event, build: { ...event.build, osVersion: "bad\ud800" } },
    ];

    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify(invalidEvents));

    expect(readDiagnosticEvents(localStorage, now)).toEqual([]);
    expect(localStorage.getItem(DiagnosticsStorageKey)).toBeNull();
    expect(redactDiagnosticValue({ invalid: "bad\ud800" })).toEqual({});
  });

  it("drops legacy events containing request or response payload labels before export", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const safe = fixtureEvent(now);
    for (const payload of [
      "request_body=email=alice@example.test",
      "response-body=email=alice@example.test",
      "request_headers=Authorization: redacted",
      "response-headers: Set-Cookie: session=abc",
    ]) {
      const unsafe = { ...fixtureEvent(now - 1), summary: payload };
      localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([unsafe, safe]));

      const events = readDiagnosticEvents(localStorage, now);
      expect(events).toEqual([safe]);
      expect(JSON.parse(localStorage.getItem(DiagnosticsStorageKey) ?? "null")).toEqual([safe]);
      expect(prepareDiagnosticsBundle(safe, events).exportJson).not.toContain(payload);
    }
  });

  it("drops persisted events containing scheme-shaped URLs or punctuation-delimited paths", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    for (const locator of [
      "devhud://auth/callback",
      "subl://open/home/alice/app.ts",
      "ftp://private.example/repo",
      "file:///home/alice/app.ts",
      "renderer,/home/alice/project/main.ts",
    ]) {
      localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([{ ...fixtureEvent(now), summary: locator }]));
      expect(readDiagnosticEvents(localStorage, now)).toEqual([]);
    }
  });

  it("binds export preview and sent request to byte-identical protobuf JSON", () => {
    const eventTime = Date.parse("2026-08-17T00:00:00.000Z");
    const event = fixtureEvent(eventTime);
    const imported = {
      ...event,
      summary: "ghp_0123456789abcdefghijklmnopqrstuvwxyz",
      stackFrames: [
        "/workspace/project/main.ts",
        "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
        "AKIA0123456789ABCDEF",
        "-----BEGIN PRIVATE KEY-----",
      ],
    };
    const assigned = { ...event, correlationId: uuidV7(eventTime - 1), summary: "password=hunter2" };
    const prepared = prepareDiagnosticsBundle(event, [imported, assigned, event]);
    expect(toJsonString(SubmitCrashReportRequestSchema, prepared.request, { prettySpaces: 2 })).toBe(prepared.requestJson);
    const exported = JSON.parse(prepared.exportJson);
    expect(exported.crashReport).toEqual(JSON.parse(prepared.requestJson));
    expect(exported.localEvents.at(-1).build.platform).toBe(DiagnosticPlatform.LINUX);
    expect(prepared.exportJson).not.toContain("ghp_");
    expect(prepared.exportJson).not.toContain("eyJ");
    expect(prepared.exportJson).not.toContain("AKIA");
    expect(prepared.exportJson).not.toContain("PRIVATE KEY");
    expect(prepared.exportJson).not.toContain("hunter2");
    expect(prepared.exportJson).not.toContain("/workspace/");
    expect(prepared.request.clientCorrelationId?.value).toBe(event.correlationId);
  });

  it("trims the oldest local events until the complete export fits", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const event = fixtureEvent(now);
    const largeEvent = { ...fixtureEvent(now - 1), stackFrames: Array.from({ length: 64 }, () => `at ${"A".repeat(500)}`) };
    const allEvents = [...Array.from({ length: 40 }, () => largeEvent), event];

    const prepared = prepareDiagnosticsBundle(event, allEvents);
    const exported = JSON.parse(prepared.exportJson) as { localEvents: LocalDiagnosticEvent[] };

    expect(new TextEncoder().encode(prepared.exportJson).byteLength).toBeLessThanOrEqual(DiagnosticsMaximumExportBytes);
    expect(exported.localEvents.length).toBeLessThan(allEvents.length);
    expect(exported.localEvents.at(-1)?.correlationId).toBe(event.correlationId);
  });

  it("retains bounded Connect response correlations without user identifiers", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const correlation = uuidV7(now, new Uint8Array(10).fill(7));
    appendDiagnosticCorrelation(localStorage, correlation, "/devhud.v1.DiagnosticsService/SubmitCrashReport", 12.6, now);
    appendDiagnosticCorrelation(localStorage, "not-a-v7", "/private/user/42", 1, now);
    expect(readDiagnosticCorrelations(localStorage, now)).toEqual([{
      source: "connect-response",
      correlationId: correlation,
      operation: "diagnostics",
      occurredAt: "2026-08-17T00:00:00.000Z",
      durationMilliseconds: 13,
    }]);
    expect(recentDiagnosticCorrelationIds(localStorage, now)).toEqual([correlation]);
    expect(localStorage.getItem("devhud.diagnostics.v1.correlations")).not.toContain("private/user");
  });

  it("allows exactly one explicitly consented authenticated online submission", () => {
    expect(diagnosticsSubmissionBlock("guest", true, true, true)).toBe("guest");
    expect(diagnosticsSubmissionBlock("blocked", true, true, true)).toBe("blocked");
    expect(diagnosticsSubmissionBlock("deletion-pending", true, true, true)).toBe("blocked");
    expect(diagnosticsSubmissionBlock("authenticated", true, true, false)).toBe("unsupported");
    expect(diagnosticsSubmissionBlock("authenticated", false, true, true)).toBe("offline");
    expect(diagnosticsSubmissionBlock("authenticated", true, false, true)).toBe("consent-required");
    expect(diagnosticsSubmissionBlock("authenticated", true, true, true)).toBeNull();
  });

  it("announces an empty event store only after preview is requested", () => {
    mockAuthenticatedIdentity([]);
    renderDiagnosticsPanel();

    expect(screen.queryByText(messages.en.diagnosticsNoEvents)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));

    expect(screen.getByText(messages.en.diagnosticsNoEvents)).toBeTruthy();
  });

  it("keeps preview and export available without the crash-report capability", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
    mockAuthenticatedIdentity([]);
    renderDiagnosticsPanel();

    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));

    expect(screen.getByRole("button", { name: messages.en.diagnosticsExport })).toBeTruthy();
    expect(screen.queryByRole("checkbox", { name: messages.en.diagnosticsConsent })).toBeNull();
    expect(screen.queryByRole("button", { name: messages.en.diagnosticsSubmit })).toBeNull();
  });

  for (const invalidatingStatus of ["deletion-pending", "signed-out"] as const) {
    it(`invalidates a prepared bundle when the account becomes ${invalidatingStatus}`, async () => {
      const now = Date.parse("2026-08-17T00:00:00.000Z");
      appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
      let status: serviceBoundary.IdentityStatus = "authenticated";
      vi.spyOn(serviceBoundary, "useIdentitySettings").mockImplementation(() => ({
        status,
        bootstrap: { capabilities: [] },
      } as unknown as ReturnType<typeof serviceBoundary.useIdentitySettings>));
      const request = vi.fn(async () => ({ kind: "diagnostics-export", outcome: "saved" } as const));
      const bridge: NativeBridgeV1 = { request, async listen() { return () => {}; } };
      const view = render(createElement(DiagnosticsPanel, { copy: messages.en, runtime, bridge, storage: localStorage, online: true }));
      fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
      expect(screen.getByRole("button", { name: messages.en.diagnosticsExport })).toBeTruthy();

      status = invalidatingStatus;
      view.rerender(createElement(DiagnosticsPanel, { copy: messages.en, runtime, bridge, storage: localStorage, online: true }));

      await waitFor(() => expect(screen.queryByRole("button", { name: messages.en.diagnosticsExport })).toBeNull());
      expect(screen.queryByTestId("diagnostics-export-preview")).toBeNull();
      expect(request).not.toHaveBeenCalled();
    });
  }

  for (const latestAction of ["uncheck", "preview"] as const) {
    it(`discards a pending consent digest after ${latestAction}`, async () => {
      const now = Date.parse("2026-08-17T00:00:00.000Z");
      appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
      mockAuthenticatedIdentity([StaticCapability.CRASH_REPORTS]);
      let resolveDigest!: (value: ArrayBuffer) => void;
      const pendingDigest = new Promise<ArrayBuffer>((resolve) => { resolveDigest = resolve; });
      vi.spyOn(crypto.subtle, "digest").mockReturnValueOnce(pendingDigest);
      renderDiagnosticsPanel();
      fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
      const consent = screen.getByRole("checkbox", { name: messages.en.diagnosticsConsent });
      fireEvent.click(consent);

      fireEvent.click(latestAction === "uncheck" ? consent : screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
      await act(async () => {
        resolveDigest(new Uint8Array(32).buffer);
        await pendingDigest;
      });

      expect((screen.getByRole("checkbox", { name: messages.en.diagnosticsConsent }) as HTMLInputElement).checked).toBe(false);
      expect((screen.getByRole("button", { name: messages.en.diagnosticsSubmit }) as HTMLButtonElement).disabled).toBe(true);
    });

    it(`does not submit after consent is revoked by ${latestAction}`, async () => {
      const now = Date.parse("2026-08-17T00:00:00.000Z");
      appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
      mockAuthenticatedIdentity([StaticCapability.CRASH_REPORTS]);
      let resolveVerification!: (value: ArrayBuffer) => void;
      const pendingVerification = new Promise<ArrayBuffer>((resolve) => { resolveVerification = resolve; });
      const digest = vi.spyOn(crypto.subtle, "digest")
        .mockResolvedValueOnce(new Uint8Array(32).buffer)
        .mockReturnValueOnce(pendingVerification);
      renderDiagnosticsPanel();
      fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
      const consent = screen.getByRole("checkbox", { name: messages.en.diagnosticsConsent });
      fireEvent.click(consent);
      await waitFor(() => expect((screen.getByRole("button", { name: messages.en.diagnosticsSubmit }) as HTMLButtonElement).disabled).toBe(false));

      fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsSubmit }));
      await waitFor(() => expect(digest).toHaveBeenCalledTimes(2));
      fireEvent.click(latestAction === "uncheck" ? consent : screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
      await act(async () => {
        resolveVerification(new Uint8Array(32).buffer);
        await pendingVerification;
      });

      expect(diagnosticsMutation.mutateAsync).not.toHaveBeenCalled();
    });
  }

  it("clears a completed export result when replacing the preview", async () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
    mockAuthenticatedIdentity([]);
    const bridge: NativeBridgeV1 = {
      async request() { return { kind: "diagnostics-export", outcome: "saved" }; },
      async listen() { return () => {}; },
    };
    renderDiagnosticsPanel(bridge);
    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsExport }));
    expect(await screen.findByText(messages.en.diagnosticsExportSaved)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));

    expect(screen.queryByText(messages.en.diagnosticsExportSaved)).toBeNull();
  });

  it("renders an unconfirmed browser download as initiated instead of saved", async () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
    mockAuthenticatedIdentity([]);
    const bridge: NativeBridgeV1 = {
      async request() { return { kind: "diagnostics-export", outcome: "initiated" }; },
      async listen() { return () => {}; },
    };
    renderDiagnosticsPanel(bridge);
    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsExport }));

    expect(await screen.findByText(messages.en.diagnosticsExportInitiated)).toBeTruthy();
    expect(screen.queryByText(messages.en.diagnosticsExportSaved)).toBeNull();
  });

  it("serializes diagnostics exports until the active request settles", async () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
    mockAuthenticatedIdentity([]);
    let resolveExport!: (value: { kind: "diagnostics-export"; outcome: "saved" }) => void;
    const pendingExport = new Promise<{ kind: "diagnostics-export"; outcome: "saved" }>((resolve) => { resolveExport = resolve; });
    const request = vi.fn(async () => pendingExport);
    renderDiagnosticsPanel({ request, async listen() { return () => {}; } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
    const exportButton = screen.getByRole("button", { name: messages.en.diagnosticsExport });

    fireEvent.click(exportButton);
    fireEvent.click(exportButton);

    expect(request).toHaveBeenCalledOnce();
    expect((exportButton as HTMLButtonElement).disabled).toBe(true);
    await act(async () => {
      resolveExport({ kind: "diagnostics-export", outcome: "saved" });
      await pendingExport;
    });
    expect((exportButton as HTMLButtonElement).disabled).toBe(false);
    expect(screen.getByText(messages.en.diagnosticsExportSaved)).toBeTruthy();
  });

  it("ignores an export completion after replacing the preview", async () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
    mockAuthenticatedIdentity([]);
    let resolveExport!: (value: { kind: "diagnostics-export"; outcome: "saved" }) => void;
    const pendingExport = new Promise<{ kind: "diagnostics-export"; outcome: "saved" }>((resolve) => { resolveExport = resolve; });
    const bridge: NativeBridgeV1 = {
      async request() { return pendingExport; },
      async listen() { return () => {}; },
    };
    renderDiagnosticsPanel(bridge);
    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsExport }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
    await act(async () => {
      resolveExport({ kind: "diagnostics-export", outcome: "saved" });
      await pendingExport;
    });

    expect(screen.queryByText(messages.en.diagnosticsExportSaved)).toBeNull();
  });

  for (const completion of ["success", "failure"] as const) {
    it(`ignores a stale submission ${completion} after replacing the preview`, async () => {
      const now = Date.parse("2026-08-17T00:00:00.000Z");
      const correlationId = "0198c8b0-77d6-7d4a-a7d9-e4d7b11c4404";
      appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
      mockAuthenticatedIdentity([StaticCapability.CRASH_REPORTS]);
      let resolveSubmit!: (value: { metadata: { correlationId: { value: string } } }) => void;
      let rejectSubmit!: (reason: unknown) => void;
      const pendingSubmit = new Promise<{ metadata: { correlationId: { value: string } } }>((resolve, reject) => {
        resolveSubmit = resolve;
        rejectSubmit = reject;
      });
      diagnosticsMutation.mutateAsync.mockReturnValue(pendingSubmit);
      renderDiagnosticsPanel();
      fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
      fireEvent.click(screen.getByRole("checkbox", { name: messages.en.diagnosticsConsent }));
      await waitFor(() => expect((screen.getByRole("button", { name: messages.en.diagnosticsSubmit }) as HTMLButtonElement).disabled).toBe(false));
      fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsSubmit }));
      await waitFor(() => expect(diagnosticsMutation.mutateAsync).toHaveBeenCalledOnce());

      fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
      await act(async () => {
        if (completion === "success") {
          resolveSubmit({ metadata: { correlationId: { value: correlationId } } });
          await pendingSubmit;
        } else {
          rejectSubmit(new ConnectError("retry", Code.Unavailable, { "x-devhud-correlation-id": correlationId }));
          await pendingSubmit.catch(() => undefined);
        }
      });

      expect(document.body.textContent).not.toContain(messages.en.diagnosticsSent);
      expect(document.body.textContent).not.toContain(messages.en.diagnosticsSubmitFailed);
      expect(document.body.textContent).not.toContain(correlationId);
      expect(screen.queryByRole("alert")).toBeNull();
      expect((screen.getByRole("checkbox", { name: messages.en.diagnosticsConsent }) as HTMLInputElement).checked).toBe(false);
      expect((screen.getByRole("button", { name: messages.en.diagnosticsSubmit }) as HTMLButtonElement).disabled).toBe(true);
    });
  }

  it("preserves typed denial errors and their server correlation", async () => {
    const correlationId = "0198c8b0-77d6-7d4a-a7d9-e4d7b11c4402";
    diagnosticsMutation.mutateAsync.mockRejectedValue(new ConnectError("blocked", Code.PermissionDenied, undefined, [
      { desc: PermissionFailureSchema, value: { reason: PermissionFailureReason.USER_BLOCKED } },
      { desc: ErrorMetadataSchema, value: { correlationId: { value: correlationId } } },
    ]));
    await renderDiagnosticsPanelAndSubmit();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(messages.en.diagnosticsSubmitDenied);
    expect(alert.textContent).toContain(`diagnostics-connect-${Code.PermissionDenied}`);
    expect(alert.textContent).toContain(correlationId);
  });

  it("preserves retryable submission classifications and header correlations", async () => {
    const correlationId = "0198c8b0-77d6-7d4a-a7d9-e4d7b11c4403";
    diagnosticsMutation.mutateAsync.mockRejectedValue(new ConnectError("retry", Code.Unavailable, {
      "x-devhud-correlation-id": correlationId,
    }));
    await renderDiagnosticsPanelAndSubmit();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(messages.en.diagnosticsSubmitFailed);
    expect(alert.textContent).toContain(`diagnostics-connect-${Code.Unavailable}`);
    expect(alert.textContent).toContain(correlationId);
    expect(screen.getByTestId("diagnostics-export-preview")).toBeTruthy();
  });

  it("renders the retained crash-report quota and limit distinctly from retryable failures", async () => {
    const correlationId = "0198c8b0-77d6-7d4a-a7d9-e4d7b11c4405";
    diagnosticsMutation.mutateAsync.mockRejectedValue(new ConnectError("quota exhausted", Code.ResourceExhausted, undefined, [
      { desc: QuotaFailureSchema, value: { quota: QuotaKind.CRASH_REPORTS, limit: 100n, observed: 101n } },
      { desc: ErrorMetadataSchema, value: { correlationId: { value: correlationId } } },
    ]));
    await renderDiagnosticsPanelAndSubmit();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(messages.en.diagnosticsSubmitQuotaExceeded);
    expect(alert.textContent).toContain(`${messages.en.diagnosticsSubmitQuotaLimit}: 100`);
    expect(alert.textContent).not.toContain(messages.en.diagnosticsSubmitFailed);
    expect(alert.textContent).toContain(`diagnostics-connect-${Code.ResourceExhausted}`);
    expect(alert.textContent).toContain(correlationId);
  });

  it("accepts only bounded redacted exports with an app-selected name", () => {
    const name = "devhud-diagnostics-0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400.json";
    expect(() => validateDiagnosticsExport({ suggestedName: name, contents: "{}" })).not.toThrow();
    expect(() => validateDiagnosticsExport({ suggestedName: "../../report.json", contents: "{}" })).toThrow(NativeBridgeError);
    expect(() => validateDiagnosticsExport({ suggestedName: name, contents: "not-json" })).toThrow(NativeBridgeError);
  });

  it("reports a user-cancelled browser destination without claiming a saved export", async () => {
    window.showSaveFilePicker = async () => { throw new DOMException("cancelled", "AbortError"); };
    await expect(nativeBridge.request({ operation: "diagnostics.export", suggestedName: "devhud-diagnostics-0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400.json", contents: "{}" }))
      .resolves.toEqual({ kind: "diagnostics-export", outcome: "cancelled" });
  });

  for (const phase of ["create", "write", "close"] as const) {
    it(`maps an aborted browser ${phase} phase to the stable storage classification`, async () => {
      let aborted = false;
      window.showSaveFilePicker = async () => ({
        createWritable: async () => {
          if (phase === "create") throw new DOMException("aborted", "AbortError");
          return {
            write: async () => { if (phase === "write") throw new DOMException("aborted", "AbortError"); },
            close: async () => { if (phase === "close") throw new DOMException("aborted", "AbortError"); },
            abort: async () => { aborted = true; },
          };
        },
      });
      await expect(nativeBridge.request({ operation: "diagnostics.export", suggestedName: "devhud-diagnostics-0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400.json", contents: "{}" }))
        .rejects.toMatchObject({ code: NativeBridgeErrorCode.StorageFailure });
      expect(aborted).toBe(phase !== "create");
    });
  }

  it("reports a fallback browser download as initiated because completion is unobservable", async () => {
    const NativeUrl = URL;
    class StubUrl extends NativeUrl {}
    const createObjectURL = vi.fn(() => "blob:diagnostics");
    const revokeObjectURL = vi.fn();
    Object.defineProperties(StubUrl, {
      createObjectURL: { value: createObjectURL },
      revokeObjectURL: { value: revokeObjectURL },
    });
    vi.stubGlobal("URL", StubUrl);
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);

    await expect(nativeBridge.request({ operation: "diagnostics.export", suggestedName: "devhud-diagnostics-0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400.json", contents: "{}" }))
      .resolves.toEqual({ kind: "diagnostics-export", outcome: "initiated" });
    expect(click).toHaveBeenCalledOnce();
    expect(createObjectURL).toHaveBeenCalledOnce();
    expect(revokeObjectURL).not.toHaveBeenCalled();
    await new Promise((resolve) => window.setTimeout(resolve, 0));
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:diagnostics");
  });

  it("writes the exact redacted bundle to the destination selected by the user", async () => {
    let written = "";
    window.showSaveFilePicker = async () => ({ createWritable: async () => ({
      write: async (data) => {
        written = await new Promise<string>((resolve, reject) => {
          const reader = new FileReader();
          reader.addEventListener("load", () => resolve(String(reader.result)));
          reader.addEventListener("error", () => reject(reader.error));
          reader.readAsText(data);
        });
      },
      close: async () => undefined,
      abort: async () => undefined,
    }) });
    const contents = '{"schemaVersion":1,"redacted":true}';
    await expect(nativeBridge.request({ operation: "diagnostics.export", suggestedName: "devhud-diagnostics-0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400.json", contents }))
      .resolves.toEqual({ kind: "diagnostics-export", outcome: "saved" });
    expect(written).toBe(contents);
  });

  it("maps browser destination write failures to the stable storage classification", async () => {
    let aborted = false;
    window.showSaveFilePicker = async () => ({ createWritable: async () => ({
      write: async () => { throw new Error("private destination details"); },
      close: async () => undefined,
      abort: async () => { aborted = true; },
    }) });
    await expect(nativeBridge.request({ operation: "diagnostics.export", suggestedName: "devhud-diagnostics-0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400.json", contents: "{}" }))
      .rejects.toMatchObject({ code: NativeBridgeErrorCode.StorageFailure });
    expect(aborted).toBe(true);
  });
});

function fixtureEvent(now: number): LocalDiagnosticEvent {
  return captureDiagnosticEvent(runtime, {
    component: DiagnosticComponent.APP,
    severity: DiagnosticSeverity.ERROR,
    errorCode: "APP_FAILURE",
    occurredAt: new Date(now),
  }, now);
}

async function renderDiagnosticsPanelAndSubmit(): Promise<void> {
  const now = Date.parse("2026-08-17T00:00:00.000Z");
  appendDiagnosticEvent(localStorage, fixtureEvent(now), now);
  mockAuthenticatedIdentity([StaticCapability.CRASH_REPORTS]);
  renderDiagnosticsPanel();
  fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsPreview }));
  fireEvent.click(screen.getByRole("checkbox", { name: messages.en.diagnosticsConsent }));
  await waitFor(() => expect((screen.getByRole("button", { name: messages.en.diagnosticsSubmit }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: messages.en.diagnosticsSubmit }));
  await waitFor(() => expect(diagnosticsMutation.mutateAsync).toHaveBeenCalledOnce());
}

function mockAuthenticatedIdentity(capabilities: readonly StaticCapability[]): void {
  vi.spyOn(serviceBoundary, "useIdentitySettings").mockReturnValue({
    status: "authenticated",
    bootstrap: { capabilities },
  } as ReturnType<typeof serviceBoundary.useIdentitySettings>);
}

function renderDiagnosticsPanel(bridge: NativeBridgeV1 = {
    async request() { return { kind: "ok" }; },
    async listen() { return () => {}; },
  }): void {
  render(createElement(DiagnosticsPanel, { copy: messages.en, runtime, bridge, storage: localStorage, online: true }));
}
