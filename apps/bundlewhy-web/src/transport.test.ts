import { create, toJson } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  AnalysisSchema,
  AnalysisService,
  ReportMode,
  RunStage,
  SourceFileSchema,
  SourceRevisionEvidence,
} from "./gen/bundlewhy/v1/bundlewhy_pb";
import { BootErrorCode, parseEmbeddedReport } from "./report";
import {
  bearerInterceptor,
  bootTransport,
  createExportedTransport,
  readAccessToken,
  TOKEN_STORAGE_KEY,
  TransportMode,
} from "./transport";
import { describeError } from "./errors";
import { fixtureAnalysis, fixtureSource } from "./test-fixtures";

function reportJson(analysisOverrides: Record<string, unknown> = {}): string {
  return JSON.stringify({
    analysis: {
      ...(toJson(AnalysisSchema, fixtureAnalysis()) as object),
      ...analysisOverrides,
    },
    sources: [toJson(SourceFileSchema, fixtureSource())],
  });
}

afterEach(() => {
  document.getElementById("bundlewhy-report")?.remove();
  sessionStorage.clear();
  history.replaceState(null, "", "/");
  vi.restoreAllMocks();
});

describe("embedded report parsing", () => {
  it("accepts a valid report and forces exported mode", () => {
    const parsed = parseEmbeddedReport(reportJson());
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(parsed.report.analysis.mode).toBe(ReportMode.EXPORTED);
    expect(parsed.report.analysis.diagnostics).toHaveLength(3);
    expect(parsed.report.sources[0].id).toBe("src-1");
  });

  it("rejects unparseable JSON as BW_RESULT_INVALID", () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const parsed = parseEmbeddedReport("{not json");
    expect(parsed.ok).toBe(false);
    if (!parsed.ok) expect(parsed.error.code).toBe(BootErrorCode.ResultInvalid);
  });

  it("rejects structurally invalid JSON as BW_RESULT_INVALID", () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const parsed = parseEmbeddedReport(reportJson({ diagnostics: "nope" }));
    expect(parsed.ok).toBe(false);
    if (!parsed.ok) expect(parsed.error.code).toBe(BootErrorCode.ResultInvalid);
    const missing = parseEmbeddedReport(JSON.stringify({ sources: [] }));
    expect(missing.ok).toBe(false);
  });

  it("rejects a build snapshot summary without matching source evidence", () => {
    const unavailable = toJson(
      SourceFileSchema,
      create(SourceFileSchema, {
        ...fixtureSource(),
        revisionEvidence: SourceRevisionEvidence.UNAVAILABLE,
      }),
    );
    const report = JSON.parse(reportJson());
    report.sources = [unavailable];
    const parsed = parseEmbeddedReport(JSON.stringify(report));
    expect(parsed.ok).toBe(false);
    if (!parsed.ok) expect(parsed.error.code).toBe(BootErrorCode.ResultInvalid);
  });

  it("rejects unknown schema versions as BW_SCHEMA_UNSUPPORTED", () => {
    const parsed = parseEmbeddedReport(
      reportJson({ schemaVersion: 2, futureField: true }),
    );
    expect(parsed.ok).toBe(false);
    if (!parsed.ok)
      expect(parsed.error.code).toBe(BootErrorCode.SchemaUnsupported);
  });
});

describe("exported transport", () => {
  function client() {
    const parsed = parseEmbeddedReport(reportJson());
    if (!parsed.ok) throw new Error("fixture must parse");
    return createClient(
      AnalysisService,
      createExportedTransport(parsed.report),
    );
  }

  it("serves progress, analysis, sources and one run in memory", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const c = client();
    const progress = await c.getRunProgress({});
    expect(progress.progress?.mode).toBe(ReportMode.EXPORTED);
    expect(progress.progress?.stage).toBe(RunStage.FINISHED);
    expect(progress.progress?.cancellable).toBe(false);
    expect((await c.getAnalysis({})).analysis?.projectName).toBe("storefront");
    expect((await c.getSourceFile({ sourceId: "src-1" })).source?.path).toBe(
      "src/main.ts",
    );
    const runs = await c.listRuns({});
    expect(runs.runs).toHaveLength(1);
    expect(runs.runs[0].active).toBe(true);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects unknown sources and mutating methods", async () => {
    const c = client();
    await expect(
      c.getSourceFile({ sourceId: "missing" }),
    ).rejects.toMatchObject({
      code: Code.NotFound,
    });
    for (const call of [
      () => c.cancelRun({}),
      () => c.selectRun({ runId: "x" }),
      () => c.exportReport({ includeSources: true }),
    ]) {
      const error = await call().catch((e: unknown) => ConnectError.from(e));
      expect(error).toBeInstanceOf(ConnectError);
      expect((error as ConnectError).code).toBe(Code.FailedPrecondition);
      expect((error as ConnectError).rawMessage).toBe(
        "Not available in an exported report",
      );
    }
  });

  it("boots exported mode from the embedded script element", () => {
    const script = document.createElement("script");
    script.type = "application/json";
    script.id = "bundlewhy-report";
    script.textContent = reportJson();
    document.head.appendChild(script);
    const boot = bootTransport();
    expect(boot.ok && boot.mode).toBe(TransportMode.Exported);
  });
});

describe("access token handling", () => {
  it("reads the token from the hash, stores it and removes the hash", () => {
    history.replaceState(null, "", "/report?x=1#token=secret-123");
    expect(readAccessToken()).toBe("secret-123");
    expect(location.hash).toBe("");
    expect(location.pathname + location.search).toBe("/report?x=1");
    expect(sessionStorage.getItem(TOKEN_STORAGE_KEY)).toBe("secret-123");
    // A reload without the hash falls back to session storage.
    expect(readAccessToken()).toBe("secret-123");
  });

  it("reports a missing token", () => {
    const boot = bootTransport();
    expect(boot.ok).toBe(false);
    if (!boot.ok) {
      expect(boot.error.code).toBe(BootErrorCode.AccessTokenMissing);
      expect(boot.error.recovery).toBe(
        "Reopen the URL printed by `bundlewhy analyze` or `bundlewhy open`.",
      );
    }
  });

  it("survives storage failures", () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    history.replaceState(null, "", "/#token=abc");
    expect(readAccessToken()).toBe("abc");
    expect(location.hash).toBe("");
  });

  it("attaches the bearer token to every request", async () => {
    const request = { header: new Headers() };
    const next = vi.fn(async (req: typeof request) => ({ req }));
    await bearerInterceptor("tok")(next as never)(request as never);
    expect(next).toHaveBeenCalledOnce();
    expect(request.header.get("Authorization")).toBe("Bearer tok");
  });
});

describe("describeError", () => {
  it("extracts bundlewhy codes and the Connect code", () => {
    const d = describeError(
      new ConnectError(
        "BW_ACCESS_TOKEN_INVALID: token rejected",
        Code.Unauthenticated,
      ),
    );
    expect(d.code).toBe("BW_ACCESS_TOKEN_INVALID");
    expect(d.connectCode).toBe("unauthenticated");
    expect(d.message).toBe("token rejected");
    expect(d.recovery).toMatch(/bundlewhy analyze/);
    expect(
      describeError(new ConnectError("x", Code.FailedPrecondition)).connectCode,
    ).toBe("failed_precondition");
  });
});

describe("product error recovery", () => {
  it("prefers BW code guidance over the generic Connect status", () => {
    const description = describeError(
      new ConnectError(
        "BW_EXPORT_FAILED: Could not write the report",
        Code.Internal,
      ),
    );
    expect(description.code).toBe("BW_EXPORT_FAILED");
    expect(description.recovery).toMatch(/writable/);
    const generic = describeError(new ConnectError("boom", Code.Internal));
    expect(generic.recovery).toMatch(/Reload the page/);
  });
});
