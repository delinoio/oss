import type { ReactNode, Ref } from "react";

export enum Format {
  Pptx = "pptx",
  Docx = "docx",
  Xlsx = "xlsx",
  Pdf = "pdf",
  Wav = "wav",
  Figma = "figma",
}

export enum ErrorCode {
  Authentication = "authentication",
  PermissionDenied = "permission_denied",
  RateLimited = "rate_limited",
  QuotaExceeded = "quota_exceeded",
  Remote = "remote",
  PartialPublish = "partial_publish",
  UnknownOutcome = "unknown_outcome",
  MalformedInput = "malformed_input",
  UnsupportedPackage = "unsupported_package",
  UnsupportedEdit = "unsupported_edit",
  InvalidTarget = "invalid_target",
  Conflict = "conflict",
  ResourceLimit = "resource_limit",
  MissingFont = "missing_font",
  LayoutOverflow = "layout_overflow",
  Cancelled = "cancelled",
  Disposed = "disposed",
  Io = "io",
  Render = "render",
}

export enum Stage {
  Import = "import",
  Render = "render",
  Layout = "layout",
  Export = "export",
  Publish = "publish",
  Dispose = "dispose",
}

export interface Diagnostic {
  readonly source?: "javascript" | "native";
  readonly operation?: "generate" | "inspect" | "update";
  readonly status?: "started" | "completed" | "failed";
  readonly stage: Stage;
  readonly format: Format;
  readonly revision: number;
  readonly durationMs: number;
  readonly code?: ErrorCode;
  readonly location?: string;
  readonly calls?:number;
  readonly retries?:number;
  readonly waitMs?:number;
  readonly batches?:number;
  readonly changes?:number;
}

export interface NodeHandle {
  readonly documentId: string;
  readonly nodeId: string;
}

export interface Geometry {
  readonly coordinateSpace?: "word_flow" | "worksheet" | "mounted_region" | "page" | "timeline";
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
  readonly page?: number;
  readonly revision: number;
}

export interface ElementProps {
  children?: ReactNode;
  ref?: Ref<NodeHandle>;
}

export interface TextStyle {
  fontFamily?: string;
  fontSize?: number;
  bold?: boolean;
  italic?: boolean;
  underline?: boolean;
  color?: string;
  background?: string;
  align?: "left" | "center" | "right" | "justify";
  language?: string;
  direction?: "auto" | "ltr" | "rtl";
}

export interface Frame {
  x: number;
  y: number;
  width: number;
  height: number;
}

export type AssetSource = Uint8Array | { path: string };
export interface AssetHandle { readonly assetId: string; readonly documentId: string }

export const limits = Object.freeze({
  sfxSeconds: 30,
  sfxLayers: 256,
  sfxVoiceSamples: 16_000_000,
  officeBytes: 256 * 1024 * 1024,
  pdfBytes: 256 * 1024 * 1024,
  expandedOfficeBytes: 512 * 1024 * 1024,
  zipEntries: 10_000,
  partBytes: 64 * 1024 * 1024,
  xmlDepth: 128,
  xmlNodes: 1_000_000,
  treeBytes: 16 * 1024 * 1024,
  treeDepth: 48,
  treeNodes: 20_000,
  imageBytes: 64 * 1024 * 1024,
  imagePixels: 64_000_000,
});
