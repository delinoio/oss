// SPDX-License-Identifier: Apache-2.0
import { object, type Document } from "./documents";
import { copy, useLocale } from "./localization";

const fields = (v: Document, required: readonly string[], optional: readonly string[] = []) => required.every(key => Object.hasOwn(v, key)) && Object.keys(v).every(key => required.includes(key) || optional.includes(key));
const bounded = (v: unknown, maximum: number, required = false): v is string => typeof v === "string" && (!required || v.length > 0) && new TextEncoder().encode(v).length <= maximum && !/[\u0000\uD800-\uDFFF]/u.test(v);
const identity = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);

// Media stays inert presence metadata. This decoder has no retrieval callback,
// and encrypted bytes cannot be represented by its closed content union.
export function codexFunctionOutput(data: Document, id: string): Document | undefined {
 const v = object(data.codex_function_output), output = object(v.output);
 if (data.role !== "tool" || !identity(data.execution_id) || !identity(data.native_thread_id) || !identity(data.native_turn_id) || !Number.isSafeInteger(data.first_sequence) || Number(data.first_sequence) < 1 || !Number.isSafeInteger(data.last_sequence) || Number(data.last_sequence) < Number(data.first_sequence) || ["tool", "artifact", "progress", "claude", "claude_tool", "claude_progress", "claude_interruption", "grok_tool", "grok_text", "grok_user", "input_id", "native_parent_id", "phase", "attachments"].some(key => Object.hasOwn(data, key)) || !fields(v, ["version", "id", "native_id", "name", "namespace", "stage", "output"]) || v.version !== 1 || v.id !== id || !identity(v.id) || !bounded(v.native_id, 1024, true) || v.native_id !== data.native_id || !bounded(v.name, 1024, true) || (v.namespace !== null && !bounded(v.namespace, 1024)) || (v.stage === "started" ? data.state !== "streaming" : v.stage !== "completed" || data.state !== "complete")) return;
 let plain = "";
 if (output.variant === "string") {
  if (!fields(output, ["variant", "text"]) || !bounded(output.text, 256 << 10)) return;
  plain = output.text;
 } else {
  if (output.variant !== "contents" || !fields(output, ["variant", "contents"]) || !Array.isArray(output.contents) || output.contents.length > 128) return;
  for (const value of output.contents) {
   const c = object(value);
   switch (c.type) {
    case "input_text": if (!fields(c, ["type", "text"]) || !bounded(c.text, 256 << 10)) return; plain += c.text; break;
    case "input_image": if (!fields(c, ["type", "reference_kind", "reference_present"], ["detail"]) || !["image_url", "file_id"].includes(String(c.reference_kind)) || c.reference_present !== true || c.detail !== undefined && !["auto", "low", "high", "original"].includes(String(c.detail))) return; break;
    case "input_audio": if (!fields(c, ["type", "reference_kind", "reference_present"]) || c.reference_kind !== "audio_url" || c.reference_present !== true) return; break;
    case "encrypted_content": if (!fields(c, ["type", "present"]) || c.present !== true) return; break;
    default: return;
   }
  }
 }
 if (!bounded(plain, 256 << 10) || data.text !== plain) return;
 return v;
}
export function NativeCodexFunctionOutput({ data, id }: { data: Document; id: string }) {
 useLocale();
 const v = codexFunctionOutput(data, id), output = object(v?.output);
 return <article className="message" aria-label={copy("nativeCodexFunctionOutput.label")}>
  <header>
   <strong>{copy("session.tool_7c9bbe")}</strong>
   {v ? <span>{String(v.name)}{v.namespace !== null ? <> · {String(v.namespace)}</> : null}</span> : null}
  </header>
  {!v ? <p>{copy("nativeCodexFunctionOutput.unavailable")}</p> : output.variant === "string" ? <pre>{String(output.text)}</pre> :
   (output.contents as Document[]).map((c, index) => c.type === "input_text" ? <pre key={index}>{String(c.text)}</pre> :
    <p key={index}>
     {c.type === "input_image" ? copy("nativeCodexFunctionOutput.image") : c.type === "input_audio" ? copy("nativeCodexFunctionOutput.audio") : copy("nativeCodexFunctionOutput.encrypted")}
     {c.reference_kind ? <> · {String(c.reference_kind)}</> : null}
     {c.detail ? <> · {String(c.detail)}</> : null}
    </p>)}
 </article>;
}
