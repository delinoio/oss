// SPDX-License-Identifier: Apache-2.0
import { object, text } from "./documents";

export enum FileOperation { Roots = "roots", Directory = "directory", File = "file" }
export enum EntryKind { Directory = "directory", File = "file", Link = "link", Other = "other" }
export type Root = { repository_id: string; name: string; primary: boolean };
export type Entry = { name: string; kind: EntryKind; size: string };
export type Observation = { roots: Root[]; entries: Entry[]; next: string; text: string; size: string; binary: boolean; truncated: boolean };

export function observation(raw: Uint8Array): Observation {
  const invalid = () => { throw new Error("The workspace observation is malformed. Refresh the view."); };
  if (raw.byteLength > 512 * 1024) return invalid();
  let value;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return invalid(); }
  const roots = value.roots ?? [], entries = value.entries ?? [];
  if (!Array.isArray(roots) || roots.length > 100 || !Array.isArray(entries) || entries.length > 100 || typeof value.binary !== "boolean" || typeof value.truncated !== "boolean" || typeof value.size !== "string" || !/^\d{1,19}$/.test(value.size) || (value.text !== undefined && typeof value.text !== "string") || (value.next_page_token !== undefined && typeof value.next_page_token !== "string")) return invalid();
  return {
    roots: roots.map((row) => { const item = object(row); if (typeof item.name !== "string" || typeof item.primary !== "boolean" || (item.repository_id !== undefined && typeof item.repository_id !== "string")) return invalid(); return { repository_id: text(item.repository_id), name: item.name, primary: item.primary }; }),
    entries: entries.map((row) => { const item = object(row); if (typeof item.name !== "string" || !Object.values(EntryKind).includes(item.kind as EntryKind) || typeof item.size !== "string" || !/^\d{1,19}$/.test(item.size)) return invalid(); return { name: item.name, kind: item.kind as EntryKind, size: item.size }; }),
    next: text(value.next_page_token), text: text(value.text), size: value.size, binary: value.binary, truncated: value.truncated,
  };
}
