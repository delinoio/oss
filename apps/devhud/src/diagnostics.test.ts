// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Code, ConnectError } from "@connectrpc/connect";
import { toJsonString } from "@bufbuild/protobuf";
import { DiagnosticArchitecture, DiagnosticComponent, DiagnosticPlatform, DiagnosticSeverity, ErrorMetadataSchema, PermissionFailureReason, PermissionFailureSchema, StaticCapability, SubmitCrashReportRequestSchema } from "@delinoio/devhud-api-client";
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

beforeEach(() => {
  localStorage.clear();
  delete window.showSaveFilePicker;
  diagnosticsMutation.mutateAsync.mockReset();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
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
      importedLocation: "/workspace/project/main.ts",
      shortcut: "Ctrl+Shift+P",
      urlFragment: "https://example.test/path#private",
      safeSlashLabel: "React/Native renderer failed",
    };
    const serialized = JSON.stringify(redactDiagnosticValue(hostile));
    expect(serialized).toContain("bounded classification");
    expect(serialized).toContain("React/Native renderer failed");
    for (const prohibited of ["Bearer", "githubPat", "r2_secret", "signingKey", "nodeType", "screenshot", "issueBody", "agentPrompt", "childEnvironment", "/home/alice", "ghp_", "/workspace/", "Ctrl+", "#private"]) {
      expect(serialized).not.toContain(prohibited);
    }
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

  it("normalizes persisted events to the closed local schema and drops unsafe classifications", () => {
    const now = Date.parse("2026-08-17T00:00:00.000Z");
    const valid = { ...fixtureEvent(now), persistentUserId: "user-123" };
    const invalid = { ...fixtureEvent(now + 1), component: 999 };
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([valid, invalid]));
    const events = readDiagnosticEvents(localStorage, now);
    expect(events).toHaveLength(1);
    expect(events[0]).not.toHaveProperty("persistentUserId");
  });

  it("binds export preview and sent request to byte-identical protobuf JSON", () => {
    const event = fixtureEvent(Date.parse("2026-08-17T00:00:00.000Z"));
    const imported = {
      ...event,
      summary: "ghp_0123456789abcdefghijklmnopqrstuvwxyz",
      stackFrames: ["/workspace/project/main.ts"],
    };
    const prepared = prepareDiagnosticsBundle(event, [imported, event]);
    expect(toJsonString(SubmitCrashReportRequestSchema, prepared.request, { prettySpaces: 2 })).toBe(prepared.requestJson);
    const exported = JSON.parse(prepared.exportJson);
    expect(exported.crashReport).toEqual(JSON.parse(prepared.requestJson));
    expect(exported.localEvents.at(-1).build.platform).toBe(DiagnosticPlatform.LINUX);
    expect(prepared.exportJson).not.toContain("ghp_");
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
