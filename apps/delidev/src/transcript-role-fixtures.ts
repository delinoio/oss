// SPDX-License-Identifier: Apache-2.0
// Synthetic records for component and browser presentation checks only.
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { encode, type Document } from "./documents";
export const transcriptSessionId = "01960dcb-e1fa-7000-8000-000000000001";
const thread = transcriptSessionId;
export function transcriptResource(data: Document, index = 2, revision = 1n) {
  return create(ResourceSchema, { id: `01960dcb-e1fa-7000-8000-${String(index).padStart(12, "0")}`, sessionId: transcriptSessionId, kind: EntityKind.MESSAGE, schemaVersion: 1, revision, documentJson: encode(data) });
}
export function transcriptRoleFixtures() {
  const user: Document = { role: "user", state: "complete", text: "Short user text" };
  const assistant: Document = { role: "assistant", state: "complete", text: "Original assistant text\nSecond line <script>inert()</script> 한국어" };
  const grokUser: Document = { execution_id: thread, native_thread_id: thread, native_turn_id: "526452fa-1956-42dd-b5f4-60e2b23dfe92", input_id: "01960dcb-e1fa-7000-8000-000000000099", native_id: `${thread}-2`, role: "user", state: "complete", text: "Original Grok user\n" + "unbrokentoken".repeat(80), first_sequence: 5, last_sequence: 5, grok_user: { source: "closed-first-text", native_event_id: `${thread}-2`, timestamp_ms: "1", prompt_index: "0", model: "Synthetic model", input_digest: "ab".repeat(32) } };
  const grokText: Document = { execution_id: thread, native_thread_id: thread, native_turn_id: "526452fa-1956-42dd-b5f4-60e2b23dfe92", native_id: `${thread}-10`, role: "assistant", state: "complete", text: "Original Grok assistant", first_sequence: 3, last_sequence: 4, grok_text: { response_ordinal: 1, chunks: [{ event_id: `${thread}-10`, chunk_id: "1", context_tokens: "42", timestamp_ms: "1", stream_start_ms: "0", turn_start_ms: "0" }] } };
  const claude: Document = { role: "assistant", state: "complete", text: "", claude: { model: "Synthetic model", blocks: [{ index: 0, block: { kind: "text", text: "Original Claude assistant" }, state: "stopped" }, { index: 1, block: { kind: "thinking", text: "Original reasoning" }, state: "stopped" }], stop_reason: "end_turn", stop_sequence: null } };
  return { user, assistant, grokUser, grokText, claude };
}
