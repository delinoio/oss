import { object, type Document } from "./documents";

export enum Comparison { WorkingTree = "working-tree", Staged = "staged", Creation = "creation", Branch = "branch" }
export type Diff = { comparison: Comparison; repository_id: string; path: string; base: "commit" | "empty-tree"; base_object: string; head_commit?: string; patch: string; untracked: string[]; revision: string; base_ref?: BaseReference; base_commit?: string; merge_base?: string };
export type BaseReference = { type: "local-branch" | "remote-branch" | "commit"; name: string; remote?: string };
export const sameReference = (a?: BaseReference, b?: BaseReference) => a?.type === b?.type && a?.name === b?.name && a?.remote === b?.remote;
export const referenceKey = (ref: BaseReference) => JSON.stringify([ref.type, ref.name, ref.remote]);
export const referenceLabel = (ref: BaseReference) => ref.type === "remote-branch" ? `${ref.remote}/${ref.name}` : ref.name;
export function readReference(value: unknown): BaseReference | undefined {
  const v = object(value);
  if (!exact(v, ["type", "name", ...(v.remote !== undefined ? ["remote"] : [])]) || !["local-branch", "remote-branch", "commit"].includes(String(v.type)) || typeof v.name !== "string" || !v.name || v.name.startsWith("-") || /[\r\n\0\uD800-\uDFFF]/u.test(v.name) || new TextEncoder().encode(v.name).length > 1024 || (v.type === "remote-branch" ? typeof v.remote !== "string" || !v.remote || !/^[A-Za-z0-9_.-]+$/.test(v.remote) || v.remote.startsWith("-") : v.remote !== undefined)) return;
  return v as BaseReference;
}
export type DiffChoice = { reference: BaseReference; available: boolean; configured: boolean };
export type DiffOptions = { version: 1; repository_id: string; choices: DiffChoice[]; default?: BaseReference };
export function readDiffOptions(raw: Uint8Array, repository: string): DiffOptions {
  const invalid = () => { throw new Error("The complete local reference inventory is unavailable."); };
  if (raw.byteLength > 131072 + 64) return invalid();
  let value: Document;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return invalid(); }
  const v = object(value.diff_options);
  if (!exact(value, ["size", "binary", "truncated", "diff_options"]) || value.size !== "0" || value.binary !== false || value.truncated !== false || !exact(v, ["version", "repository_id", "choices", ...(v.default !== undefined ? ["default"] : [])]) || v.version !== 1 || v.repository_id !== repository || !Array.isArray(v.choices) || v.choices.length > 1001 || new TextEncoder().encode(JSON.stringify(v)).length > 131072) return invalid();
  const seen = new Set<string>(); let configured = 0, native = 0;
  for (const item of v.choices) { const choice = object(item), ref = readReference(choice.reference); if (!exact(choice, ["reference", "available", "configured"]) || !ref || typeof choice.available !== "boolean" || typeof choice.configured !== "boolean" || (!choice.configured && ref.type === "commit") || seen.has(referenceKey(ref))) return invalid(); seen.add(referenceKey(ref)); if (choice.configured) configured++; else native++; }
  if (configured > 1 || native > 1000 || v.default !== undefined && (!readReference(v.default) || !v.choices.some(item => { const c = item as DiffChoice; return c.available && sameReference(c.reference, v.default as BaseReference); }))) return invalid();
  return v as DiffOptions;
}
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));
const oid = (v: unknown): v is string => typeof v === "string" && /^([0-9a-f]{40}|[0-9a-f]{64})$/.test(v);
function afterUTF8(value: string, previous: string): boolean {
  // Go sorts path bytes; JavaScript's UTF-16 order differs for supplementary
  // characters. Compare the wire encoding so valid filenames stay visible.
  const a = new TextEncoder().encode(value), b = new TextEncoder().encode(previous);
  for (let i = 0; i < Math.min(a.length, b.length); i++) if (a[i] !== b[i]) return a[i] > b[i];
  return a.length > b.length;
}

export function readDiff(raw: Uint8Array, repository: string, comparison: Comparison, path: string, baseRef?: BaseReference): Diff {
  const invalid = () => { throw new Error("The Git diff observation is malformed. Refresh the comparison."); };
  if (raw.byteLength > 512 * 1024) return invalid();
  let value: Document;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return invalid(); }
  const v = object(value.diff);
  if (!exact(value, ["size", "binary", "truncated", "diff"]) || value.size !== "0" || value.binary !== false || value.truncated !== false || !exact(v, ["comparison", "repository_id", "path", "base", "base_object", "patch", "untracked", "revision", ...(v.head_commit !== undefined ? ["head_commit"] : []), ...(comparison === Comparison.Branch ? ["base_ref", "base_commit", "merge_base"] : [])]) || v.repository_id !== repository || v.comparison !== comparison || v.path !== path || !oid(v.base_object) || typeof v.revision !== "string" || !/^[0-9a-f]{64}$/.test(v.revision) || typeof v.patch !== "string" || v.patch.includes("\0") || /[\uD800-\uDFFF]/u.test(v.patch) || new TextEncoder().encode(v.patch).length > 65536 || !Array.isArray(v.untracked) || v.untracked.length > 100) return invalid();
  if (comparison === Comparison.Branch ? v.base !== "commit" || !readReference(v.base_ref) || !sameReference(v.base_ref as BaseReference, baseRef) || !oid(v.head_commit) || !oid(v.base_commit) || v.merge_base !== v.base_object || v.base_commit.length !== v.head_commit.length || v.base_object.length !== v.head_commit.length : v.base === "commit" ? !oid(v.head_commit) || v.head_commit.length !== v.base_object.length || (comparison !== Comparison.Creation && v.head_commit !== v.base_object) : v.base !== "empty-tree" || v.head_commit !== undefined || comparison === Comparison.Creation) return invalid();
  let last = "", size = 0;
  for (const file of v.untracked) {
    if (typeof file !== "string" || !file || file.startsWith("/") || file.split("/").some((part) => !part || part === "." || part === "..") || /[\u0000-\u001F\\\uD800-\uDFFF]/u.test(file) || !afterUTF8(file, last)) return invalid();
    size += new TextEncoder().encode(file).length; last = file;
    if (size > 16384) return invalid();
  }
  return v as Diff;
}
