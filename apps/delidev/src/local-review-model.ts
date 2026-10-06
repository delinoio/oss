import { ProductError, copy } from "./localization";
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { encode, object, document, Mode, type Document } from "./documents";
import { Comparison, readDiff, type Diff } from "./session-diff-model";

export class ReviewContextError extends ProductError {}

export enum AnchorKind { File = "file", Lines = "lines" }
export enum ReviewSide { Old = "old", New = "new" }
export type Selection = { path: string; kind: AnchorKind; side?: ReviewSide; start?: number; end?: number };
export type Anchor = { repository_id: string; comparison: Comparison; query_path: string; diff_revision: string; selection: Selection; file_digest: string; context: string };
export type Comment = { anchor: Anchor; body: string; content_revision: string; last_submission_id?: string; last_submitted_content_revision?: string };
export type ReviewLine = { old?: number; new?: number; text: string; newline: boolean; hunk: number };
export type ReviewFile = { path: string; kind: "text" | "non-line"; digest: string; lines: ReviewLine[] };
export type ReviewContext = { diff: Diff; files: ReviewFile[] };
export type Submission = { input_id: string; mode: Mode; comments: { id: string; content_revision: string; anchor: Anchor; body: string; freshness: "current" | "stale" }[] };
const digest = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{64}$/.test(v);
const uuid = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const integer = (v: unknown): v is number => typeof v === "number" && Number.isInteger(v) && v > 0 && v < 4294967295;
const bounded = (v: unknown, limit: number): v is string => typeof v === "string" && !/[\0\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= limit;
const path = (v: unknown): v is string => bounded(v, 4096) && v !== "." && v.split("/").every((part) => part && part !== "." && part !== "..") && !/[\u0000-\u001F\\]/u.test(v);
const exact = (v: Document, required: string[], optional: string[] = []) => required.every((key) => Object.hasOwn(v, key)) && Object.keys(v).every((key) => required.includes(key) || optional.includes(key));
const decimal = (v: unknown): v is string => typeof v === "string" && /^[1-9][0-9]{0,18}$/.test(v) && BigInt(v) < 9223372036854775808n;

function selection(value: unknown): value is Selection {
  const v = object(value);
  return path(v.path) && (v.kind === AnchorKind.File ? exact(v, ["path", "kind"]) : v.kind === AnchorKind.Lines && exact(v, ["path", "kind", "side", "start", "end"]) && (v.side === ReviewSide.Old || v.side === ReviewSide.New) && integer(v.start) && integer(v.end) && v.end >= v.start && v.end - v.start < 20);
}
export function readAnchor(value: unknown): Anchor | undefined {
  const v = object(value);
  if (!exact(v, ["repository_id", "comparison", "query_path", "diff_revision", "selection", "file_digest", "context"]) || !uuid(v.repository_id) || !Object.values(Comparison).includes(v.comparison as Comparison) || !(v.query_path === "." || path(v.query_path)) || !digest(v.diff_revision) || !digest(v.file_digest) || !selection(v.selection) || !bounded(v.context, 8192) || (v.selection.kind === AnchorKind.File && v.context !== "") || (v.query_path !== "." && v.selection.path !== v.query_path && !v.selection.path.startsWith(`${v.query_path}/`))) return;
  return v as Anchor;
}
export function readComment(resource: Resource, session: string): Comment | undefined {
  if (resource.kind !== EntityKind.REVIEW || resource.sessionId !== session) return;
  const value = document(resource), v = object(value.comment);
  if (!exact(value, ["version", "type", "comment"]) || value.version !== 1 || value.type !== "comment" || !exact(v, ["anchor", "body", "content_revision"], ["last_submission_id", "last_submitted_content_revision"]) || !readAnchor(v.anchor) || !bounded(v.body, 8192) || !v.body.trim() || !decimal(v.content_revision)) return;
  if (v.last_submission_id === undefined ? v.last_submitted_content_revision !== undefined : !uuid(v.last_submission_id) || !decimal(v.last_submitted_content_revision) || BigInt(v.last_submitted_content_revision) > BigInt(v.content_revision)) return;
  return v as Comment;
}
export function readReviewContext(raw: Uint8Array, expected: Diff): ReviewContext {
  const invalid = () => { throw new ReviewContextError("validation.03d54ce010c1"); };
  if (raw.byteLength > 1048576) return invalid();
  let value: Document;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return invalid(); }
  if (!exact(value, ["diff", "files"]) || !Array.isArray(value.files) || value.files.length > 256) return invalid();
  const diff = readDiff(encode({ size: "0", binary: false, truncated: false, diff: value.diff }), expected.repository_id, expected.comparison, expected.path);
  if (diff.revision !== expected.revision) throw new ReviewContextError("validation.376b1888b589");
  const seen = new Set<string>();
  for (const item of value.files) {
    const file = object(item);
    if (!exact(file, ["path", "kind", "digest", "lines"]) || !path(file.path) || seen.has(file.path) || !digest(file.digest) || !Array.isArray(file.lines) || (file.kind === "non-line" ? file.lines.length !== 0 : file.kind !== "text" || file.lines.length === 0)) return invalid();
    if (diff.path !== "." && file.path !== diff.path && !file.path.startsWith(`${diff.path}/`)) return invalid();
    seen.add(file.path);
    for (const item of file.lines) {
      const line = object(item);
      if (!exact(line, ["text", "newline", "hunk"], ["old", "new"]) || !bounded(line.text, 65536) || typeof line.newline !== "boolean" || !integer(line.hunk) || (line.old === undefined && line.new === undefined) || (line.old !== undefined && !integer(line.old)) || (line.new !== undefined && !integer(line.new))) return invalid();
    }
  }
  return { diff, files: value.files as ReviewFile[] };
}
export function readSubmission(resource: Resource, session: string): Submission | undefined {
  if (resource.kind !== EntityKind.REVIEW || resource.sessionId !== session) return;
  const value = document(resource), v = object(value.submission);
  if (!exact(value, ["version", "type", "submission"]) || value.version !== 1 || value.type !== "submission" || !exact(v, ["input_id", "mode", "comments"]) || !uuid(v.input_id) || !Object.values(Mode).includes(v.mode as Mode) || !Array.isArray(v.comments) || v.comments.length < 1 || v.comments.length > 25) return;
  const seen = new Set<string>();
  for (const item of v.comments) { const c = object(item); if (!exact(c, ["id", "content_revision", "anchor", "body", "freshness"]) || !uuid(c.id) || seen.has(c.id) || !decimal(c.content_revision) || !readAnchor(c.anchor) || !bounded(c.body, 8192) || !c.body.trim() || (c.freshness !== "current" && c.freshness !== "stale")) return; seen.add(c.id); }
  return v as Submission;
}
export function selectedContext(file: ReviewFile | undefined, pick: Selection): string | undefined {
  if (!file || file.path !== pick.path || !selection(pick)) return;
  if (pick.kind === AnchorKind.File) return "";
  if (file.kind !== "text") return;
  const rows = file.lines.filter((line) => { const n = line[pick.side!]; return n !== undefined && n >= pick.start! && n <= pick.end!; });
  if (rows.length !== pick.end! - pick.start! + 1 || rows.some((line, i) => line[pick.side!] !== pick.start! + i || line.hunk !== rows[0].hunk)) return;
  const result = rows.map((line) => line.text + (line.newline ? "\n" : "")).join("");
  return bounded(result, 8192) ? result : undefined;
}
export function freshness(anchor: Anchor, diff: Diff): string {
  if (anchor.repository_id !== diff.repository_id || anchor.comparison !== diff.comparison || anchor.query_path !== diff.path) return copy("local-review-model.extra.a05610ae5f5f");
  return anchor.diff_revision === diff.revision ? copy("local-review-model.extra.85c996c70928") : copy("local-review-model.extra.01167438c368");
}
