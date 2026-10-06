import { copy } from "./localization";
import { supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";

export type Document = Record<string, unknown>;
export function document(resource?: Resource): Document {
  if (!resource || !supportsResourceSchema(resource) || resource.documentJson.byteLength > 1 << 20) return {};
  try {
    const value: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson));
    return object(value);
  } catch { return {}; }
}
export function object(value: unknown): Document {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Document : {};
}
export function text(value: unknown): string { return typeof value === "string" ? value : ""; }
export function items(value: unknown): unknown[] { return Array.isArray(value) ? value : []; }
export function encode(value: unknown): Uint8Array { return new TextEncoder().encode(JSON.stringify(value)); }

export enum Workspace { Worktree = "worktree", Local = "local", GeneralChat = "general-chat" }
export enum Mode { Execute = "execute", Plan = "plan" }
export const workspaceNames: Record<Workspace, string> = { get [Workspace.Worktree]() { return copy("documents.worktree_c893ba"); }, get [Workspace.Local]() { return copy("documents.localComputer_09d55f"); }, get [Workspace.GeneralChat]() { return copy("documents.generalChat_f634bc"); } };
export function resourceName(resource?: Resource): string { const data = document(resource); return text(data.name) || text(data.alias) || copy("documents.extra.e504e6152194"); }
