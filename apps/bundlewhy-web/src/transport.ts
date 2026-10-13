import { create } from "@bufbuild/protobuf";
import {
  Code,
  ConnectError,
  createRouterTransport,
  type Interceptor,
  type Transport,
} from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import {
  AnalysisService,
  ReportMode,
  RunProgressSchema,
  RunStage,
  RunSummarySchema,
} from "./gen/bundlewhy/v1/bundlewhy_pb";
import {
  BootErrorCode,
  EMBEDDED_REPORT_ELEMENT_ID,
  parseEmbeddedReport,
  type BootError,
  type EmbeddedReport,
} from "./report";

export const TOKEN_STORAGE_KEY = "bundlewhy.token";
export const EXPORTED_UNAVAILABLE_MESSAGE =
  "Not available in an exported report";

export enum TransportMode {
  Server = "server",
  Exported = "exported",
}

export type Boot =
  | { ok: true; mode: TransportMode; transport: Transport }
  | { ok: false; error: BootError };

/**
 * Reads the access token from `#token=...`, stores it for reloads in this tab
 * and removes it from the address bar. Falls back to the stored token.
 */
export function readAccessToken(win: Window = window): string | null {
  const params = new URLSearchParams(win.location.hash.replace(/^#/, ""));
  const fromHash = params.get("token");
  if (fromHash) {
    try {
      win.sessionStorage.setItem(TOKEN_STORAGE_KEY, fromHash);
    } catch (error) {
      console.warn("bundlewhy.token.store_failed", {
        reason: error instanceof Error ? error.message : String(error),
      });
    }
    // Keep the token out of history, bookmarks and copied URLs.
    win.history.replaceState(
      win.history.state,
      "",
      win.location.pathname + win.location.search,
    );
    return fromHash;
  }
  try {
    return win.sessionStorage.getItem(TOKEN_STORAGE_KEY);
  } catch {
    return null;
  }
}

/** Adds `Authorization: Bearer <token>` to every request. */
export function bearerInterceptor(token: string): Interceptor {
  return (next) => async (request) => {
    request.header.set("Authorization", `Bearer ${token}`);
    return next(request);
  };
}

export function createServerTransport(
  token: string,
  baseUrl: string,
): Transport {
  return createConnectTransport({
    baseUrl,
    useBinaryFormat: false,
    interceptors: [bearerInterceptor(token)],
  });
}

function exportedUnavailable(): never {
  throw new ConnectError(EXPORTED_UNAVAILABLE_MESSAGE, Code.FailedPrecondition);
}

/** In-memory AnalysisService over an exported report. Never performs I/O. */
export function createExportedTransport(report: EmbeddedReport): Transport {
  const { analysis, sources } = report;
  const progress = () =>
    create(RunProgressSchema, {
      runId: analysis.runId,
      status: analysis.status,
      stage: RunStage.FINISHED,
      message: "",
      elapsedMs: analysis.durationMs,
      mode: ReportMode.EXPORTED,
      cancellable: false,
    });
  return createRouterTransport((router) =>
    router.service(AnalysisService, {
      getRunProgress: () => ({ progress: progress() }),
      getAnalysis: () => ({ analysis }),
      getSourceFile: (request) => {
        const source = sources.find((s) => s.id === request.sourceId);
        if (!source)
          throw new ConnectError(
            `BW_SOURCE_NOT_FOUND: Source ${request.sourceId} is not part of this report.`,
            Code.NotFound,
          );
        return { source };
      },
      listRuns: () => ({
        runs: [
          create(RunSummarySchema, {
            runId: analysis.runId,
            status: analysis.status,
            projectName: analysis.projectName,
            bundler: analysis.engine?.bundler,
            startedAt: analysis.startedAt,
            active: true,
          }),
        ],
      }),
      cancelRun: exportedUnavailable,
      selectRun: exportedUnavailable,
      exportReport: exportedUnavailable,
    }),
  );
}

/** Chooses exported or server mode from the current document. */
export function bootTransport(
  doc: Document = document,
  win: Window = window,
): Boot {
  const embedded = doc.getElementById(EMBEDDED_REPORT_ELEMENT_ID);
  if (embedded) {
    const parsed = parseEmbeddedReport(embedded.textContent ?? "");
    if (!parsed.ok) return parsed;
    return {
      ok: true,
      mode: TransportMode.Exported,
      transport: createExportedTransport(parsed.report),
    };
  }
  const token = readAccessToken(win);
  if (!token)
    return {
      ok: false,
      error: {
        code: BootErrorCode.AccessTokenMissing,
        title: "Access token missing",
        message:
          "This page was opened without the access token for the local bundlewhy server.",
        recovery:
          "Reopen the URL printed by `bundlewhy analyze` or `bundlewhy open`.",
      },
    };
  return {
    ok: true,
    mode: TransportMode.Server,
    transport: createServerTransport(token, win.location.origin),
  };
}
