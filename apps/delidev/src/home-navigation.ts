import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text, Workspace, type Document } from "./documents";
import { sessionTitlePresentation } from "./session-title";

// Home deliberately retains only navigation metadata. Full Resource documents
// belong to disposable bounded queries, never to an accumulated inventory.
export enum ConversationKind { Unknown = "unknown", GeneralChat = "general-chat", WorkSession = "work-session", Fork = "fork", Sidechat = "sidechat" }

// This is a closed presentation hint, never native or mutation authority.
export function conversationProvenance(row: Resource, data: Document): { conversationKind: ConversationKind; parentId?: string; sidechatParent?: string } {
  const unknown = { conversationKind: ConversationKind.Unknown };
  if (row.kind !== EntityKind.SESSION || !Object.values(Workspace).includes(data.workspace as Workspace)) return unknown;
  if (Object.hasOwn(data, "fork")) {
    const fork = object(data.fork), parent = text(fork.source_session_id);
    if (!Object.keys(fork).length || !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(parent)) return unknown;
    if (Object.hasOwn(fork, "sidechat_parent_snapshot")) {
      if (!Object.keys(object(fork.sidechat_parent_snapshot)).length || parent === row.id) return unknown;
      return { conversationKind: ConversationKind.Sidechat, parentId: parent, sidechatParent: parent };
    }
    return { conversationKind: ConversationKind.Fork, parentId: parent };
  }
  return { conversationKind: data.workspace === Workspace.GeneralChat ? ConversationKind.GeneralChat : ConversationKind.WorkSession };
}

export interface NavigationRow {
  id: string;
  projectId: string;
  revision: bigint;
  name: string;
  workspace: string;
  outcome: string;
  archive: string;
  title?: ReturnType<typeof sessionTitlePresentation>;
  conversationKind: ConversationKind;
  parentId?: string;
  sidechatParent?: string;
}
export function navigationRow(row: Resource): NavigationRow {
  const data = document(row);
  const provenance = conversationProvenance(row, data);
  return { id: row.id, projectId: row.projectId, revision: row.revision, name: resourceName(row), workspace: text(data.workspace), outcome: text(data.outcome), archive: text(data.archive), title: sessionTitlePresentation(data), ...provenance };
}
export { ReadStage } from "./scroll-pagination";
import { PaginationChain, type PaginationBatch, type PaginationPage, type PaginationFailure, type PaginationSnapshot, type PaginationReader } from "./scroll-pagination";

export type NavigationBatch = PaginationBatch<NavigationRow>;
export type NavigationPage = PaginationPage<NavigationRow>;
export type NavigationFailure = PaginationFailure;
export type NavigationSnapshot = PaginationSnapshot<NavigationRow>;
export type NavigationReader = PaginationReader<NavigationRow>;
export class NavigationChain extends PaginationChain<NavigationRow> {}

export class HomeNavigation {
  // Explicit choices survive filters, regrouping and same-identity reconnect.
  collapsedConversations = new Set<string>();
  catalog = new NavigationChain();
  global = new NavigationChain();
  projects = new Map<string, NavigationChain>();
  project(id: string) {
    let chain = this.projects.get(id);
    if (!chain) { chain = new NavigationChain(); this.projects.set(id, chain); }
    return chain;
  }
  resetSessions() { this.global.reset(); for (const chain of this.projects.values()) chain.reset(); }
}

export interface ConversationNode { row: NavigationRow; children: ConversationNode[]; parent?: ConversationNode }
// The accepted order is the only ordering clock. Links never cross a scope.
export function conversationForest(rows: readonly NavigationRow[]): ConversationNode[] {
  const nodes = new Map<string, ConversationNode>();
  for (const row of rows) if (!nodes.has(row.id)) nodes.set(row.id, { row, children: [] });
  for (const node of nodes.values()) {
    const parent = node.row.parentId && nodes.get(node.row.parentId);
    if (parent && parent.row.projectId === node.row.projectId) node.parent = parent;
  }
  const done = new Set<ConversationNode>();
  for (const start of nodes.values()) {
    const path: ConversationNode[] = [], positions = new Map<ConversationNode, number>();
    let current: ConversationNode | undefined = start;
    while (current && !done.has(current) && !positions.has(current)) {
      positions.set(current, path.length); path.push(current); current = current.parent;
    }
    if (current && positions.has(current)) for (const node of path.slice(positions.get(current))) node.parent = undefined;
    for (const node of path) done.add(node);
  }
  const roots: ConversationNode[] = [];
  for (const node of nodes.values()) if (node.parent) node.parent.children.push(node); else roots.push(node);
  return roots;
}
export function revealConversation(roots: readonly ConversationNode[], id: string, collapsed: Set<string>): boolean {
  const pending = [...roots]; let changed = false;
  while (pending.length) {
    const node = pending.pop()!;
    if (node.row.id === id) { for (let parent = node.parent; parent; parent = parent.parent) changed = collapsed.delete(parent.row.id) || changed; break; }
    for (const child of node.children) pending.push(child);
  }
  return changed;
}
