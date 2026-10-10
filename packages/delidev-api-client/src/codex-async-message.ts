// SPDX-License-Identifier: Apache-2.0
export interface CodexAsyncQuestion { title: string; options: string[] | null }
export interface CodexAsyncMessage { version: 1; delivery_present: boolean; delivery: "async" | null; questions_present: boolean; questions: CodexAsyncQuestion[] | null }
const bytes = (value: string) => new TextEncoder().encode(value).byteLength;
const record = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
const keys = (v: Record<string, unknown>, names: string[]) => Object.keys(v).length === names.length && names.every(name => Object.hasOwn(v, name));
const bounded = (v: unknown): v is string => typeof v === "string" && bytes(v) <= 4096 && !v.includes("\0");
// Decode inert content only. This profile contains no reply or execution owner.
export function codexAsyncMessage(value: unknown): CodexAsyncMessage | undefined {
 if (!record(value) || !keys(value, ["version", "delivery_present", "delivery", "questions_present", "questions"]) || value.version !== 1 || typeof value.delivery_present !== "boolean" || typeof value.questions_present !== "boolean" || value.delivery !== null && value.delivery !== "async" || !value.delivery_present && value.delivery !== null || !value.questions_present && value.questions !== null) return;
 if (value.questions !== null) {
  if (!Array.isArray(value.questions) || value.questions.length > 64) return;
  let size = 0;
  for (const q of value.questions) {
   if (!record(q) || !keys(q, ["title", "options"]) || !bounded(q.title) || q.options !== null && (!Array.isArray(q.options) || q.options.length > 32 || !q.options.every(bounded))) return;
   size += bytes(q.title) + (q.options === null ? 0 : (q.options as string[]).reduce((n, option) => n + bytes(option), 0));
  }
  if (size > 65536) return;
 }
 return value as unknown as CodexAsyncMessage;
}
