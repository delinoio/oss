import { fromJson, type JsonValue } from "@bufbuild/protobuf";
import {
  AnalysisSchema,
  ReportMode,
  RunStatus,
  SourceFileSchema,
  SourceRevisionEvidence,
  type Analysis,
  type SourceFile,
} from "./gen/bundlewhy/v1/bundlewhy_pb";

/** The only embedded report schema version this UI understands. */
export const SUPPORTED_SCHEMA_VERSION = 1;

/** Element id of the `<script type="application/json">` report payload. */
export const EMBEDDED_REPORT_ELEMENT_ID = "bundlewhy-report";

export enum BootErrorCode {
  AccessTokenMissing = "BW_ACCESS_TOKEN_MISSING",
  ResultInvalid = "BW_RESULT_INVALID",
  SchemaUnsupported = "BW_SCHEMA_UNSUPPORTED",
}

export interface BootError {
  code: BootErrorCode;
  title: string;
  message: string;
  recovery: string;
}

export interface EmbeddedReport {
  analysis: Analysis;
  sources: SourceFile[];
}

export type ParseResult =
  { ok: true; report: EmbeddedReport } | { ok: false; error: BootError };

function invalid(message: string): ParseResult {
  return {
    ok: false,
    error: {
      code: BootErrorCode.ResultInvalid,
      title: "This report cannot be read",
      message,
      recovery:
        "The embedded result is damaged or incomplete. Export the report again with `bundlewhy`, or open the saved run with `bundlewhy open <run-dir>`.",
    },
  };
}

function unsupported(version: unknown): ParseResult {
  return {
    ok: false,
    error: {
      code: BootErrorCode.SchemaUnsupported,
      title: "This report uses an unsupported format",
      message: `Report schema version ${String(version)} is not supported. This viewer supports version ${SUPPORTED_SCHEMA_VERSION}.`,
      recovery:
        "Open the report with the bundlewhy version that created it, or export it again with this bundlewhy version.",
    },
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Parses and validates the JSON payload of an exported report:
 * `{ "analysis": <Analysis>, "sources": [<SourceFile>...] }` in proto3 JSON.
 */
export function parseEmbeddedReport(text: string): ParseResult {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (error) {
    console.warn("bundlewhy.report.parse_failed", {
      reason: error instanceof Error ? error.message : String(error),
    });
    return invalid("The embedded report is not valid JSON.");
  }
  if (!isRecord(raw) || !isRecord(raw.analysis))
    return invalid("The embedded report has no analysis object.");
  if (raw.sources !== undefined && !Array.isArray(raw.sources))
    return invalid("The embedded report sources must be a list.");

  // Check the version before strict decoding: a newer schema can add fields
  // that strict decoding would report as a generic structure error.
  const rawVersion = raw.analysis.schemaVersion ?? raw.analysis.schema_version;
  if (
    rawVersion !== undefined &&
    Number(rawVersion) !== SUPPORTED_SCHEMA_VERSION
  )
    return unsupported(rawVersion);

  let analysis: Analysis;
  let sources: SourceFile[];
  try {
    analysis = fromJson(AnalysisSchema, raw.analysis as JsonValue);
    sources = ((raw.sources ?? []) as JsonValue[]).map((source) =>
      fromJson(SourceFileSchema, source),
    );
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    console.warn("bundlewhy.report.decode_failed", { reason });
    return invalid(`The embedded report structure is invalid: ${reason}`);
  }
  if (analysis.schemaVersion !== SUPPORTED_SCHEMA_VERSION)
    return unsupported(analysis.schemaVersion);

  const sourcesById = new Map(sources.map((source) => [source.id, source]));
  for (const summary of analysis.sources) {
    const source = sourcesById.get(summary.id);
    if (
      summary.revisionEvidence === SourceRevisionEvidence.BUILD_SNAPSHOT &&
      (!source ||
        summary.path !== source.path ||
        source.revisionEvidence !== SourceRevisionEvidence.BUILD_SNAPSHOT)
    )
      return invalid("The embedded report has inconsistent source records.");
  }

  analysis.mode = ReportMode.EXPORTED;
  // An exported report is a finished snapshot; never present it as running.
  if (
    analysis.status === RunStatus.RUNNING ||
    analysis.status === RunStatus.UNSPECIFIED
  )
    analysis.status = RunStatus.PARTIAL;
  return { ok: true, report: { analysis, sources } };
}
