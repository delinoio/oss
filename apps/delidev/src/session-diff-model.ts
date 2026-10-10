import { object, type Document } from "./documents";

export enum Comparison { WorkingTree = "working-tree", Staged = "staged", Creation = "creation", Branch = "branch" }
export enum ReferenceType { Local = "local-branch", Remote = "remote-branch", Commit = "commit" }
export type Reference = { type: ReferenceType; name: string; remote?: string };
export type ComparisonIdentity = {repository: string; comparison: Comparison; path: string; base_ref?: Reference};
export type Diff = { base_ref?: Reference; base_commit?: string; merge_base?: string; comparison: Comparison; repository_id: string; path: string; base: "commit" | "empty-tree"; base_object: string; head_commit?: string; patch: string; untracked: string[]; revision: string };
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));
const oid = (v: unknown): v is string => typeof v === "string" && /^([0-9a-f]{40}|[0-9a-f]{64})$/.test(v);
function afterUTF8(value: string, previous: string): boolean {
  // Go sorts path bytes; JavaScript's UTF-16 order differs for supplementary
  // characters. Compare the wire encoding so valid filenames stay visible.
  const a = new TextEncoder().encode(value), b = new TextEncoder().encode(previous);
  for (let i = 0; i < Math.min(a.length, b.length); i++) if (a[i] !== b[i]) return a[i] > b[i];
  return a.length > b.length;
}

export function readDiff(raw: Uint8Array, repository: string, comparison: Comparison, path: string, baseRef?: Reference): Diff {
  const invalid = () => { throw new Error("The Git diff observation is malformed. Refresh the comparison."); };
  if (raw.byteLength > 512 * 1024) return invalid();
  let value: Document;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return invalid(); }
  const v = object(value.diff);
  if (!exact(value, ["size", "binary", "truncated", "diff"]) || value.size !== "0" || value.binary !== false || value.truncated !== false || !exact(v, ["comparison", "repository_id", "path", "base", "base_object", "patch", "untracked", "revision", ...(v.head_commit !== undefined ? ["head_commit"] : []), ...(comparison === Comparison.Branch ? ["base_ref", "base_commit", "merge_base"] : [])]) || v.repository_id !== repository || v.comparison !== comparison || v.path !== path || !oid(v.base_object) || typeof v.revision !== "string" || !/^[0-9a-f]{64}$/.test(v.revision) || typeof v.patch !== "string" || v.patch.includes("\0") || /[\uD800-\uDFFF]/u.test(v.patch) || new TextEncoder().encode(v.patch).length > 65536 || !Array.isArray(v.untracked) || v.untracked.length > 100) return invalid();
  if (v.base === "commit" ? !oid(v.head_commit) || v.head_commit.length !== v.base_object.length || (comparison !== Comparison.Creation && comparison !== Comparison.Branch && v.head_commit !== v.base_object) : v.base !== "empty-tree" || v.head_commit !== undefined || (comparison === Comparison.Creation || comparison === Comparison.Branch)) return invalid();
  if (comparison === Comparison.Branch && (!readReference(v.base_ref) || referenceKey(v.base_ref as Reference) !== referenceKey(baseRef) || !oid(v.base_commit) || v.base_commit.length !== (v.head_commit as string).length || v.merge_base !== v.base_object)) return invalid();
  let last = "", size = 0;
  for (const file of v.untracked) {
    if (typeof file !== "string" || !file || file.startsWith("/") || file.split("/").some((part) => !part || part === "." || part === "..") || /[\u0000-\u001F\\\uD800-\uDFFF]/u.test(file) || !afterUTF8(file, last)) return invalid();
    size += new TextEncoder().encode(file).length; last = file;
    if (size > 16384) return invalid();
  }
  return v as Diff;
}

export function referenceKey(ref?: Reference): string { return ref ? JSON.stringify([ref.type, ref.remote ?? "", ref.name]) : ""; }
export function referenceLabel(ref: Reference): string { return ref.remote ? `${ref.remote}/${ref.name}` : ref.name; }
export function readReference(value: unknown): value is Reference {
 const v=object(value), bounded=(x:unknown,n:number):x is string=>typeof x==="string" && x.length>0 && new TextEncoder().encode(x).length<=n && !/[\u0000-\u001f\uD800-\uDFFF]/u.test(x);
 return exact(v,["type","name",...(v.remote!==undefined?["remote"]:[])]) && Object.values(ReferenceType).includes(v.type as ReferenceType) && bounded(v.name,1024) && !v.name.startsWith("-") && (v.type===ReferenceType.Remote ? bounded(v.remote,256) && !/[: \\/]/.test(v.remote) : v.remote===undefined);
}
export type DiffOptions = {version:1;repository_id:string;path:string;choices:{reference:Reference;configured:boolean}[];default?:Reference;default_available:boolean};
export function readDiffOptions(raw:Uint8Array, repository:string, path:string):DiffOptions {
 const invalid=():never=>{throw new Error("The Git comparison options are malformed.");};
 if(raw.byteLength>256*1024)return invalid();
 let v:Document;
 try {const root=object(JSON.parse(new TextDecoder("utf-8",{fatal:true}).decode(raw)));if(!exact(root,["size","binary","truncated","diff_options"]) || root.size!=="0" || root.binary!==false || root.truncated!==false)return invalid();v=object(root.diff_options);}catch{return invalid();}
 if(!exact(v,["version","repository_id","path","choices","default_available",...(v.default!==undefined?["default"]:[])]) || v.version!==1 || v.repository_id!==repository || v.path!==path || !Array.isArray(v.choices) || v.choices.length>1001 || typeof v.default_available!=="boolean" || new TextEncoder().encode(JSON.stringify(v)).length>128*1024)return invalid();
 const seen=new Set<string>();let configured=0;
 for(const item of v.choices){const c=object(item);if(!exact(c,["reference","configured"]) || !readReference(c.reference) || typeof c.configured!=="boolean" || seen.has(referenceKey(c.reference)))return invalid();seen.add(referenceKey(c.reference));if(c.configured)configured++;else if(c.reference.type===ReferenceType.Commit)return invalid();}
 if(configured>1 || v.choices.length-configured>1000 || (v.default!==undefined && (!readReference(v.default) || !seen.has(referenceKey(v.default)))) || (v.default_available && v.default===undefined))return invalid();
 return v as DiffOptions;
}
export function diffQuery(diff:Diff) { return {operation:"git-diff",repository_id:diff.repository_id,comparison:diff.comparison,path:diff.path,...(diff.base_ref?{base_ref:diff.base_ref}:{})}; }
