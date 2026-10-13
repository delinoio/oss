import { useSyncExternalStore } from "react";
import { referenceKey, type BaseReference } from "./session-diff-model";

/**
 * Renderer-lifetime record of explicit Git bases that an accepted options
 * inventory reported absent or unavailable. The owner binds each record to one
 * session and repository. It deliberately outlives the Diff panel's mount, so
 * leaving and reopening the Diff presentation or switching repositories never
 * restores trust. Only an explicit Base control selection of the same
 * reference clears its invalidation. The record never leaves the renderer and
 * holds no response or diagnostic text. The base a retained base-free
 * comparison tab dropped is not kept here: it lives on that tab's descriptor
 * (`dropped_base`), so it ends with the tab and never depends on a cache.
 */
export class DiffBaseTrust {
  /** Every invalidated reference per owner, keyed so a later invalidation never replaces an earlier one. */
  private owners = new Map<string, Map<string, BaseReference>>();
  private listeners = new Set<() => void>();
  private revision = 0;
  subscribe = (callback: () => void) => {
    this.listeners.add(callback);
    return () => {
      this.listeners.delete(callback);
    };
  };
  snapshot = () => this.revision;
  private publish() {
    this.revision += 1;
    for (const callback of this.listeners) callback();
  }
  isInvalidated(owner: string, reference: BaseReference) {
    return Boolean(this.owners.get(owner)?.has(referenceKey(reference)));
  }
  /** Returns true when the reference was not already invalidated. */
  invalidate(owner: string, reference: BaseReference) {
    const trust = this.owners.get(owner) ?? new Map<string, BaseReference>(),
      key = referenceKey(reference);
    if (trust.has(key)) return false;
    trust.set(key, reference);
    this.owners.set(owner, trust);
    this.publish();
    return true;
  }
  /** An explicit Base control selection restores trust for that reference only. */
  reselect(owner: string, reference: BaseReference) {
    if (this.owners.get(owner)?.delete(referenceKey(reference))) this.publish();
  }
}

export const diffBaseTrust = new DiffBaseTrust();

/** Subscribes the caller to invalidation changes of the shared record. */
export function useDiffBaseTrust(store: DiffBaseTrust = diffBaseTrust) {
  useSyncExternalStore(store.subscribe, store.snapshot);
  return store;
}
