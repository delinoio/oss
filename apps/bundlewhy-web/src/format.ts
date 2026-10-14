import type {
  EngineIdentity,
  SourceLocation,
} from "./gen/bundlewhy/v1/bundlewhy_pb";
import { bundlerLabel } from "./labels";
import { Bundler } from "./gen/bundlewhy/v1/bundlewhy_pb";

/** Formats a byte count with binary units, for example `12.3 KiB`. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}

/** Exact byte count for tooltips, for example `12,345 bytes`. */
export function formatExactBytes(bytes: number): string {
  return `${bytes.toLocaleString("en-US")} ${bytes === 1 ? "byte" : "bytes"}`;
}

/** Formats milliseconds as `42s` or `3m 05s`. */
export function formatElapsed(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes === 0) return `${seconds}s`;
  return `${minutes}m ${String(seconds).padStart(2, "0")}s`;
}

/** Human summary of the engine, for example `Vite 8.3.4 · Oxc minifier 1.2.13`. */
export function engineSummary(engine: EngineIdentity | undefined): string {
  if (!engine) return "Engine unknown";
  const bundlerName =
    engine.bundler === Bundler.UNSPECIFIED && engine.bundlerPackage
      ? engine.bundlerPackage
      : bundlerLabel(engine.bundler);
  const parts = [
    [bundlerName, engine.bundlerVersion].filter(Boolean).join(" "),
  ];
  if (engine.minifier) {
    const name = /minif|plugin/i.test(engine.minifier)
      ? engine.minifier
      : `${engine.minifier} minifier`;
    parts.push([name, engine.minifierVersion].filter(Boolean).join(" "));
  }
  return parts.join(" · ");
}

/** Formats `path:line:column`, omitting unknown (zero) positions. */
export function formatLocation(location: SourceLocation): string {
  let text = location.path || "(unknown path)";
  if (location.line > 0) {
    text += `:${location.line}`;
    if (location.column > 0) text += `:${location.column}`;
  }
  return text;
}

export interface SplitMessage {
  /** Stable bundlewhy code such as `BW_RUN_NOT_FOUND`, when present. */
  code?: string;
  message: string;
}

/** Splits a server message of the form `BW_XXX: detail` into code and detail. */
export function splitCodedMessage(raw: string): SplitMessage {
  const match = /^(BW_[A-Z0-9_]+):\s*([\s\S]*)$/.exec(raw.trim());
  if (!match) return { message: raw };
  return { code: match[1], message: match[2] || raw };
}

/** Formats an ISO timestamp for display; returns the raw value if unparseable. */
export function formatTimestamp(value: string): string {
  if (!value) return "Unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
