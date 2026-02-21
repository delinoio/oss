import { DevkitMiniAppId, DevkitRoute } from "@/lib/mini-app-registry";

export enum LogEvent {
  Navigation = "navigation",
  RouteRender = "route-render",
  RouteLoadError = "route-load-error",
}

export interface DevkitLogEntry {
  event: LogEvent;
  route: DevkitRoute | string;
  miniAppId?: DevkitMiniAppId;
  message?: string;
  error?: unknown;
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

function toLogPayload(entry: DevkitLogEntry): Record<string, string | undefined> {
  return {
    event: entry.event,
    route: entry.route,
    miniAppId: entry.miniAppId,
    message: entry.message,
    error: serializeError(entry.error),
  };
}

export function logInfo(entry: DevkitLogEntry): void {
  console.info("[devkit]", toLogPayload(entry));
}

export function logError(entry: DevkitLogEntry): void {
  console.error("[devkit]", toLogPayload(entry));
}
