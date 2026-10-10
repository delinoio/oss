// SPDX-License-Identifier: Apache-2.0
import { object, type Document } from "./documents";
import { copy } from "./localization";
const uuid = (v: unknown): v is string => typeof v === "string" && /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(v);
const text = (v: unknown, max: number, required = false): v is string => typeof v === "string" && (!required || v.trim() !== "") && new TextEncoder().encode(v).length <= max && !/[\u0000-\u001f\u007f]/u.test(v);
const digest = (v: unknown): v is string => typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const closed = (v: Document, allowed: string[], required = allowed) => Object.keys(v).every(k => allowed.includes(k)) && required.every(k => Object.hasOwn(v, k));
const base = ["version", "id", "stage", "call_id", "namespace", "tool", "arguments", "status", "content_items", "success", "duration_ms"];
const numericRequest = (value: unknown): value is string => typeof value === "string" && /^-?(0|[1-9][0-9]{0,18})$/.test(value) && value !== "-0" && BigInt(value) >= -9223372036854775808n && BigInt(value) <= 9223372036854775807n;
const request = ["arrival_id", "request_id", "response_id", "delivery", "negative_outcome", "request_resolved"];
/** Closed inert projection: no native argument value or fetchable reference. */
export function validCodexDynamicTool(data: Document): boolean {
  if (data.role !== "tool" || data.state !== "complete" || !uuid(data.execution_id) || !text(data.native_thread_id, 1024, true) || !text(data.native_turn_id, 1024, true) || !Number.isSafeInteger(data.first_sequence) || Number(data.first_sequence) <= 0 || data.first_sequence !== data.last_sequence || ["tool", "artifact", "progress", "claude", "claude_tool", "claude_progress", "claude_interruption", "grok_tool", "grok_text", "grok_user", "input_id", "phase", "native_parent_id"].some(k => Object.hasOwn(data, k))) return false;
  const d = object(data.codex_dynamic_tool), args = object(d.arguments);
  if (!closed(d, [...base, ...request], base) || d.version !== 1 || !uuid(d.id) || !text(d.call_id, 1024, true) || data.native_id !== d.call_id || !text(d.tool, 256, true) || !(d.namespace === null || text(d.namespace, 256)) || !closed(args, ["present", "type", "digest"]) || args.present !== true || !["null", "object", "array", "string", "number", "boolean"].includes(String(args.type)) || !digest(args.digest)) return false;
  if (["started", "completed"].includes(String(d.stage))) {
    if (request.some(k => Object.hasOwn(d, k)) || !["inProgress", "completed", "failed"].includes(String(d.status)) || (d.stage === "started") !== (d.status === "inProgress") || !(d.success === null || typeof d.success === "boolean") || !(d.duration_ms === null || Number.isSafeInteger(d.duration_ms) && Number(d.duration_ms) >= 0) || !(d.content_items === null || Array.isArray(d.content_items))) return false;
    let size = 0;
    for (const raw of (d.content_items ?? []) as unknown[]) {
      const item = object(raw);
      if (item.type === "inputText") {
        if (!closed(item, ["type", "text"]) || typeof item.text !== "string" || item.text.includes("\0") || new TextEncoder().encode(item.text).length > 262144) return false;
        size += new TextEncoder().encode(item.text).length;
      } else if (["inputImage", "inputAudio"].includes(String(item.type))) {
        if (!closed(item, ["type", "reference_kind", "reference_present", "reference_digest"]) || item.reference_kind !== (item.type === "inputImage" ? "image_url" : "audio_url") || typeof item.reference_present !== "boolean" || !digest(item.reference_digest)) return false;
      } else return false;
    }
    return size <= 262144 && (!Array.isArray(d.content_items) || d.content_items.length <= 128);
  }
  if (!["requested", "reply-delivery", "request-resolved"].includes(String(d.stage)) || d.status !== null || d.content_items !== null || d.success !== null || d.duration_ms !== null || !uuid(d.arrival_id) || typeof d.request_resolved !== "boolean" || !["not-sent", "send-started", "transmitted", "uncertain"].includes(String(d.delivery))) return false;
  const id = object(d.request_id);
  if (!(closed(id,["kind","value"]) && (id.kind === "text" && text(id.value, 128, true) || id.kind === "number" && numericRequest(id.value)))) return false;
  if (d.stage === "requested") return d.delivery === "not-sent" && d.request_resolved === false && !Object.hasOwn(d, "response_id") && !Object.hasOwn(d, "negative_outcome");
  if (d.stage === "reply-delivery") return d.delivery !== "not-sent" && d.request_resolved === false && uuid(d.response_id) && d.negative_outcome === false;
  return d.request_resolved === true && (d.delivery === "not-sent" ? !Object.hasOwn(d, "response_id") && !Object.hasOwn(d, "negative_outcome") : uuid(d.response_id) && d.negative_outcome === false);
}
export function NativeCodexDynamicTool({ data }: { data: Document }) {
  if (!validCodexDynamicTool(data)) return <p role="status">{copy("native-codex-dynamic-tool.unavailable")}</p>;
  const d = object(data.codex_dynamic_tool);
  return <article className="message" aria-label={copy("session.toolMessage_adbec1")}>
    <header><strong>{String(d.tool)}</strong></header>
    {d.negative_outcome === false ? <pre>{String(data.text)}</pre> : null}
    {((d.content_items ?? []) as unknown[]).map((raw, index) => {
      const item = object(raw);
      // React text escaping is the complete rendering path; no markdown,
      // remote image/audio element, native path, URL, or tool action exists.
      return item.type === "inputText" ? <pre key={index}>{String(item.text)}</pre> : <small key={index}>{copy(item.type === "inputImage" ? "native-codex-dynamic-tool.imageOutput" : "native-codex-dynamic-tool.audioOutput")}</small>;
    })}
  </article>;
}
