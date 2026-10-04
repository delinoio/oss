// SPDX-License-Identifier: Apache-2.0
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { EntityKind, ResourceService, type Resource } from "@delinoio/delidev-api-client";
import { document, object } from "./documents";

const native = (value: unknown, prefix: string) => typeof value === "string" && new RegExp(`^${prefix}_[0-9a-f]{12}[a-zA-Z0-9]{14}$`).test(value);
const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
const fields = ["execution_id", "native_thread_id", "native_turn_id", "native_id", "native_parent_id", "role", "input_id", "text", "state", "first_sequence", "last_sequence", "progress"];

// The summary cannot prove the complete plain-text profile. Read every retained
// canonical message before offering the action; Go independently revalidates it
// under acceptance/publication authority and the Worker verifies native history.
export function plainOpenCodeForkMessage(resource: Resource, sessionId: string, thread: unknown): "text" | "empty-changes" | undefined {
 const value = document(resource);
 if (resource.kind !== EntityKind.MESSAGE || resource.schemaVersion !== 1 || resource.sessionId !== sessionId || !uuid(resource.id) || Object.keys(value).some(key => !fields.includes(key)) || !uuid(value.execution_id) || !native(thread, "ses") || value.native_thread_id !== thread || !native(value.native_turn_id, "msg") || value.state !== "complete" || typeof value.text !== "string" || !Number.isSafeInteger(value.first_sequence) || Number(value.first_sequence) < 1 || !Number.isSafeInteger(value.last_sequence) || Number(value.last_sequence) < Number(value.first_sequence)) return;
 if (value.role === "progress") {
  const progress = object(value.progress), changes = object(progress.changes);
  if (value.text !== "" || value.native_id !== "" || value.native_parent_id != null && value.native_parent_id !== "" || value.input_id != null && value.input_id !== "" || value.last_sequence !== value.first_sequence || Object.keys(progress).some(key => !["kind", "changes"].includes(key)) || progress.kind !== "opencode-changes" || Object.keys(changes).some(key => !["source", "native_event_id", "native_message_id", "title", "body", "diffs"].includes(key)) || !native(changes.native_event_id, "evt") || !Array.isArray(changes.diffs) || changes.diffs.length !== 0 || changes.title != null || changes.body != null) return;
  if (changes.source === "session-diff" && changes.native_message_id == null || changes.source === "input-summary" && changes.native_message_id === value.native_turn_id) return "empty-changes";
  return;
 }
 if (value.progress != null || !native(value.native_id, "prt") || !native(value.native_parent_id, "msg")) return;
 if (value.role === "user" && value.native_parent_id === value.native_turn_id && uuid(value.input_id) || value.role === "assistant" && (value.input_id == null || value.input_id === "")) return "text";
}

export function useOpenCodeForkProfile(source: Resource | undefined, enabled: boolean) {
 const transport = useTransport();
 const thread = object(document(source).execution).native_thread_id;
 return useQuery({ queryKey: ["opencode-fork-profile", source?.id, source?.revision.toString(), thread], enabled: enabled && Boolean(source), retry: false, queryFn: async ({ signal }) => {
  if (!source) return false;
  const client = createClient(ResourceService, transport);
  const current = async () => { const response = await client.getResource({ kind: EntityKind.SESSION, id: source.id }, { signal }); return response.resource?.id === source.id && response.resource.revision === source.revision; };
  if (!await current()) return false;
  const ids = new Set<string>(), nativeIds = new Set<unknown>(), cursors = new Set<string>();
  let pageToken = "", bytes = 0, count = 0, total = 0;
  for (;;) {
   const page = await client.listResources({ filter: { kind: EntityKind.MESSAGE, sessionId: source.id, pageSize: 200, pageToken } }, { signal });
   if (page.resources.length > 200) return false;
   for (const row of page.resources) {
    bytes += row.documentJson.length;
    if (++total > 10000 || bytes > 8 << 20 || ids.has(row.id)) return false;
    ids.add(row.id);
    const kind = plainOpenCodeForkMessage(row, source.id, thread);
    if (!kind) return false;
    if (kind === "text") {
     const id = document(row).native_id;
     if (nativeIds.has(id)) return false;
     nativeIds.add(id); count++;
    }
   }
   if (!page.nextPageToken) return count >= 2 && await current();
   if (page.resources.length === 0 || cursors.has(page.nextPageToken) || cursors.size >= 50) return false;
   pageToken = page.nextPageToken; cursors.add(pageToken);
  }
 } });
}
