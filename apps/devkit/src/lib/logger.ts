import { DevkitMiniAppId, DevkitRoute } from "@/lib/mini-app-registry";

export enum LogEvent {
  Navigation = "navigation",
  RouteRender = "route-render",
  RouteLoadError = "route-load-error",
  CommitTrackerSeriesLoad = "commit-tracker-series-load",
  CommitTrackerComparisonLoad = "commit-tracker-comparison-load",
  CommitTrackerReportPublish = "commit-tracker-report-publish",
  RemoteFilePickerRequestValidation = "remote-file-picker-request-validation",
  RemoteFilePickerSourceSelection = "remote-file-picker-source-selection",
  RemoteFilePickerSourceAdapterFailure = "remote-file-picker-source-adapter-failure",
  RemoteFilePickerPreprocessDecision = "remote-file-picker-preprocess-decision",
  RemoteFilePickerUploadStart = "remote-file-picker-upload-start",
  RemoteFilePickerUploadResult = "remote-file-picker-upload-result",
  RemoteFilePickerCallbackResult = "remote-file-picker-callback-result",
}

export interface DevkitLogEntry {
  event: LogEvent;
  route: DevkitRoute | string;
  miniAppId?: DevkitMiniAppId;
  message?: string;
  error?: unknown;
  requestId?: string;
  source?: string;
  provider?: string;
  statusCode?: number;
  outcome?: string;
  channel?: string;
  context?: Record<string, string | number | boolean | undefined>;
}

function serializeError(error: unknown): string | undefined {
  if (!error) {
    return undefined;
  }

  if (error instanceof Error) {
    return `${error.name}: ${error.message}`;
  }

  if (typeof error === "string") {
    return error;
  }

  return JSON.stringify(error);
}

function toLogPayload(entry: DevkitLogEntry): Record<string, unknown> {
  return {
    event: entry.event,
    route: entry.route,
    miniAppId: entry.miniAppId,
    requestId: entry.requestId,
    source: entry.source,
    provider: entry.provider,
    statusCode: entry.statusCode,
    outcome: entry.outcome,
    channel: entry.channel,
    message: entry.message,
    context: entry.context,
    error: serializeError(entry.error),
  };
}

export function logInfo(entry: DevkitLogEntry): void {
  console.info("[devkit]", toLogPayload(entry));
}

export function logError(entry: DevkitLogEntry): void {
  console.error("[devkit]", toLogPayload(entry));
}
