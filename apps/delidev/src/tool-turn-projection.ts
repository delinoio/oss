// SPDX-License-Identifier: Apache-2.0
import { responseEvidence, type ResponseEvidence } from "./session-progress";
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document as readDocument, object } from "./documents";
import { claudeToolReference } from "./native-claude-tool";
import { validGrokTool } from "./native-grok-interactions";

export interface ConversationProjection { id: string; revision: bigint; executionId?: string; inputId?: string; role?: string; inherited?: boolean; response?: ResponseEvidence; tool?: { owner: string; name: string; state: string } }
enum NativeToolName { Read = "read", Shell = "bash", Todo = "todowrite" }
// These closed adapters preserve exactly these native names in the Worker;
// other tools retain their recorded name/kind without reconstruction.
const nativeNames: Record<string, NativeToolName> = { "opencode-read": NativeToolName.Read, "opencode-shell": NativeToolName.Shell, "opencode-todo": NativeToolName.Todo };
const uuid = (value: unknown): value is string => typeof value === "string" && /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(value);
const identity = (value: unknown): value is string => typeof value === "string" && value.trim().length > 0 && new TextEncoder().encode(value).length <= 1024 && !/[\u0000-\u001f\u007f\uD800-\uDFFF]/u.test(value);
const label = (value: unknown) => identity(value) && value.length <= 256 ? value : "";
/** Retain only bounded owner/display metadata, never arguments or output. */
export function conversationProjection(row: Resource, sessionId: string): ConversationProjection {
  const projection: ConversationProjection = { id: row.id, revision: row.revision, response: responseEvidence(row, sessionId) };
  const d = readDocument(row);
  if(row.kind===EntityKind.MESSAGE&&row.sessionId===sessionId&&uuid(d.execution_id)){projection.executionId=d.execution_id;projection.inputId=uuid(d.input_id)?d.input_id:undefined;projection.role=typeof d.role==="string"&&["user","assistant","tool"].includes(d.role)?d.role:undefined;projection.inherited=Boolean(d.inherited);}
  if (row.kind !== EntityKind.MESSAGE || row.sessionId !== sessionId || !uuid(row.id) || !uuid(sessionId) || !uuid(d.execution_id) || !identity(d.native_thread_id) || !identity(d.native_turn_id) || d.role !== "tool") return projection;
  const families = ["tool", "claude_tool", "grok_tool"].filter(key => Object.hasOwn(d, key));
  if (families.length !== 1 || ["artifact", "progress", "claude", "claude_progress", "claude_interruption", "grok_text", "grok_user"].some(key => Object.hasOwn(d, key))) return projection;
  let name = "", state = label(d.state);
  if (families[0] === "claude_tool") {
    const c = object(d.claude_tool), ref = claudeToolReference(c.reference);
    if (!ref || ref.id !== row.id || ref.native_id !== d.native_id || c.native_message_id !== d.native_parent_id) return projection;
    name = ref.name;
  } else if (families[0] === "grok_tool") {
    if (!validGrokTool(d)) return projection;
    const g = object(d.grok_tool), update = object(object(g.payload).update);
    name = label(update.name) || label(update.title) || label(update.sessionUpdate) || label(g.method);
    state = label(update.status) || state;
  } else {
    const tool = object(d.tool), started = object(tool.started), completed = object(tool.completed);
    if (!Object.keys(tool).length) return projection;
    const observations = Array.isArray(tool.states) ? tool.states : [];
    const latest = object(object(observations.at(-1)).snapshot);
    name = started.kind === "image-view" ? "view_image" : started.kind === "opencode-builtin" ? label(object(started.builtin).name) || label(started.kind) : nativeNames[label(started.kind)] || label(started.kind);
    state = label(completed.status) || (latest.kind === started.kind ? label(latest.status) : "") || label(started.status) || state;
  }
  projection.tool = { owner: JSON.stringify([sessionId, d.execution_id, d.native_thread_id, d.native_turn_id]), name, state };
  return projection;
}
