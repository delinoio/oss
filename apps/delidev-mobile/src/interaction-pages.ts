// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";

export const interactionPageLimit = 20;
export const interactionByteLimit = 8 << 20;
interface Page { token: string; resources: Resource[]; next: string }
export interface InteractionPages { pages: Page[]; resources: Resource[]; next: string }
export const emptyInteractionPages = (): InteractionPages => ({ pages: [], resources: [], next: "" });

// Reject ambiguous identity/token chains instead of assigning response authority
// to a duplicate row. Refresh begins a new bounded read, never a mutation retry.
export function acceptInteractionPage(previous: InteractionPages, token: string, resources: Resource[], next: string): InteractionPages {
  if (resources.length > 50 || token.length > 4096 || next.length > 4096) throw new Error("interaction-page-bound");
  const existing = previous.pages.findIndex(page => page.token === token);
  if (token && existing < 0 && token !== previous.next) throw new Error("interaction-page-order");
  const prefix = token ? existing < 0 ? previous.pages : previous.pages.slice(0, existing) : [];
  const pages = [...prefix, { token, resources, next }];
  if (pages.length > interactionPageLimit || (next && pages.some(page => page.token === next))) throw new Error("interaction-page-cycle");
  const rows = pages.flatMap(page => page.resources), ids = new Set<string>();
  let bytes = 0;
  for (const row of rows) {
    if (!row.id || ids.has(row.id) || row.revision <= 0n) throw new Error("interaction-page-identity");
    ids.add(row.id); bytes += row.documentJson.byteLength;
  }
  if (bytes > interactionByteLimit) throw new Error("interaction-page-bound");
  return { pages, resources: rows, next };
}
