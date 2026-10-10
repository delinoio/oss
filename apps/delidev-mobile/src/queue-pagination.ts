// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";

export const queuePageSize = 50;
export const maxPendingInputs = 1000;
export interface QueuePage { token: string; inputs: Resource[]; next: string }
export interface QueuePages { pages: QueuePage[]; invalid: boolean }
export const emptyQueue = (): QueuePages => ({ pages: [], invalid: false });
export function reachedQueue(state: QueuePages): Resource[] { return state.pages.flatMap(page => page.inputs); }
export function queueContinuation(state: QueuePages): string { return state.pages.at(-1)?.next ?? ""; }

/** A page failure preserves reached originals and requires explicit inspection. */
export function acceptQueuePage(state: QueuePages, token: string, inputs: Resource[], next: string, valid: (row: Resource) => boolean): QueuePages {
  const fail = () => ({ ...state, invalid: true });
  if (inputs.length > queuePageSize || typeof next !== "string" || new TextEncoder().encode(next).length > 4096) return fail();
  const existing = state.pages.findIndex(page => page.token === token);
  const prefix = existing >= 0 ? state.pages.slice(0, existing) : state.pages;
  if (token !== (prefix.at(-1)?.next ?? "") || (token !== "" && prefix.length === 0)) return fail();
  const previous = prefix.flatMap(page => page.inputs);
  const ids = new Set(previous.map(row => row.id));
  for (const row of inputs) {
    if (!valid(row) || ids.has(row.id)) return fail();
    ids.add(row.id);
  }
  const count = previous.length + inputs.length;
  if (count > maxPendingInputs || next && (inputs.length === 0 || next === token || prefix.some(page => page.token === next) || count >= maxPendingInputs)) return fail();
  // A refreshed page invalidates every cursor after it. Do not merge old suffixes
  // into a new observation or silently overwrite original target revisions.
  return { pages: [...prefix, { token, inputs, next }], invalid: false };
}
