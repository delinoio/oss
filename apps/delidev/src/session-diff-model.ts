import { object, type Document } from "./documents";

export enum Comparison { WorkingTree = "working-tree", Staged = "staged", Creation = "creation" }
export type Diff = { comparison: Comparison; repository_id: string; path: string; base: "commit" | "empty-tree"; base_object: string; head_commit?: string; patch: string; untracked: string[]; revision: string };
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));
const oid = (v: unknown): v is string => typeof v === "string" && /^([0-9a-f]{40}|[0-9a-f]{64})$/.test(v);
function afterUTF8(value: string, previous: string): boolean {
  // Go sorts path bytes; JavaScript's UTF-16 order differs for supplementary
  // characters. Compare the wire encoding so valid filenames stay visible.
  const a = new TextEncoder().encode(value), b = new TextEncoder().encode(previous);
  for (let i = 0; i < Math.min(a.length, b.length); i++) if (a[i] !== b[i]) return a[i] > b[i];
  return a.length > b.length;
}

export function readDiff(raw: Uint8Array, repository: string, comparison: Comparison, path: string): Diff {
  const invalid = () => { throw new Error("The Git diff observation is malformed. Refresh the comparison."); };
  if (raw.byteLength > 512 * 1024) return invalid();
  let value: Document;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return invalid(); }
  const v = object(value.diff);
  if (!exact(value, ["size", "binary", "truncated", "diff"]) || value.size !== "0" || value.binary !== false || value.truncated !== false || !exact(v, ["comparison", "repository_id", "path", "base", "base_object", "patch", "untracked", "revision", ...(v.head_commit !== undefined ? ["head_commit"] : [])]) || v.repository_id !== repository || v.comparison !== comparison || v.path !== path || !oid(v.base_object) || typeof v.revision !== "string" || !/^[0-9a-f]{64}$/.test(v.revision) || typeof v.patch !== "string" || v.patch.includes("\0") || /[\uD800-\uDFFF]/u.test(v.patch) || new TextEncoder().encode(v.patch).length > 65536 || !Array.isArray(v.untracked) || v.untracked.length > 100) return invalid();
  if (v.base === "commit" ? !oid(v.head_commit) || v.head_commit.length !== v.base_object.length || (comparison !== Comparison.Creation && v.head_commit !== v.base_object) : v.base !== "empty-tree" || v.head_commit !== undefined || comparison === Comparison.Creation) return invalid();
  let last = "", size = 0;
  for (const file of v.untracked) {
    if (typeof file !== "string" || !file || file.startsWith("/") || file.split("/").some((part) => !part || part === "." || part === "..") || /[\u0000-\u001F\\\uD800-\uDFFF]/u.test(file) || !afterUTF8(file, last)) return invalid();
    size += new TextEncoder().encode(file).length; last = file;
    if (size > 16384) return invalid();
  }
  return v as Diff;
}
