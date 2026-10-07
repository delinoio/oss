import { type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName, text } from "./documents";
import { sessionTitlePresentation } from "./session-title";

// Home deliberately retains only navigation metadata. Full Resource documents
// belong to disposable bounded queries, never to an accumulated inventory.
export interface NavigationRow {
  id: string;
  projectId: string;
  revision: bigint;
  name: string;
  workspace: string;
  outcome: string;
  archive: string;
  title?: ReturnType<typeof sessionTitlePresentation>;
}
export function navigationRow(row: Resource): NavigationRow {
  const data = document(row);
  return { id: row.id, projectId: row.projectId, revision: row.revision, name: resourceName(row), workspace: text(data.workspace), outcome: text(data.outcome), archive: text(data.archive), title: sessionTitlePresentation(data) };
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
