import { useSyncExternalStore } from "react";
import { referenceKey, type BaseReference } from "./session-diff-model";

interface OwnerTrust {
  /** Every invalidated reference, keyed so a later invalidation never replaces an earlier one. */
  invalidated: Map<string, BaseReference>;
  /** The invalidated reference a tabbed non-branch descriptor dropped, until Branch is viewed again. */
  departed?: BaseReference;
}

/**
 * Renderer-lifetime record of explicit Git bases that an accepted options
 * inventory reported absent or unavailable. The owner binds each record to one
 * session and repository. Only an explicit Base control selection of the same
 * reference clears its invalidation. The record never leaves the renderer and
 * holds no response or diagnostic text.
 */
export class DiffBaseTrust {
  private owners = new Map<string, OwnerTrust>();
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
    return Boolean(
      this.owners.get(owner)?.invalidated.has(referenceKey(reference)),
    );
  }
  /** Returns true when the reference was not already invalidated. */
  invalidate(owner: string, reference: BaseReference) {
    const trust = this.owners.get(owner) ?? { invalidated: new Map() },
      key = referenceKey(reference);
    if (trust.invalidated.has(key)) return false;
    trust.invalidated.set(key, reference);
    this.owners.set(owner, trust);
    this.publish();
    return true;
  }
  /** An explicit Base control selection restores trust for that reference only. */
  reselect(owner: string, reference: BaseReference) {
    const trust = this.owners.get(owner);
    if (!trust) return;
    const removed = trust.invalidated.delete(referenceKey(reference));
    const cleared = trust.departed !== undefined;
    trust.departed = undefined;
    if (removed || cleared) this.publish();
  }
  /** Remembers the invalidated base a base-free descriptor is about to drop. */
  depart(owner: string, reference: BaseReference) {
    const trust = this.owners.get(owner);
    if (!trust?.invalidated.has(referenceKey(reference))) return;
    trust.departed = reference;
  }
  /** The dropped invalidated base, while it is still invalidated. */
  departed(owner: string) {
    const reference = this.owners.get(owner)?.departed;
    return reference && this.isInvalidated(owner, reference)
      ? reference
      : undefined;
  }
  /** Branch is displayed again, so the dropped selection has been republished or abandoned. */
  settle(owner: string) {
    const trust = this.owners.get(owner);
    if (trust) trust.departed = undefined;
  }
}

export const diffBaseTrust = new DiffBaseTrust();

/** Subscribes the caller to invalidation changes of the shared record. */
export function useDiffBaseTrust(store: DiffBaseTrust = diffBaseTrust) {
  useSyncExternalStore(store.subscribe, store.snapshot);
  return store;
}
