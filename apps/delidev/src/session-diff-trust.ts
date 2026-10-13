import { useSyncExternalStore } from "react";
import { referenceKey, type BaseReference } from "./session-diff-model";

interface OwnerTrust {
  /** Every invalidated reference, keyed so a later invalidation never replaces an earlier one. */
  invalidated: Map<string, BaseReference>;
  /**
   * The invalidated reference each tabbed base-free comparison descriptor
   * dropped, keyed by that descriptor. The record survives every return to
   * Branch because the descriptor's tab stays retained; it ends when the
   * reference is explicitly reselected or the descriptor drops another base.
   */
  departed: Map<string, BaseReference>;
}

/**
 * Renderer-lifetime record of explicit Git bases that an accepted options
 * inventory reported absent or unavailable. The owner binds each record to one
 * session and repository. It deliberately outlives the Diff panel's mount, so
 * leaving and reopening the Diff presentation or switching repositories never
 * restores trust. Only an explicit Base control selection of the same
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
    const trust = this.owners.get(owner) ?? {
        invalidated: new Map(),
        departed: new Map(),
      },
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
    const key = referenceKey(reference);
    const removed = trust.invalidated.delete(key);
    // Drop every dropped-selection record of that reference; others stay.
    let cleared = false;
    for (const [descriptor, departed] of trust.departed)
      if (referenceKey(departed) === key) {
        trust.departed.delete(descriptor);
        cleared = true;
      }
    if (removed || cleared) this.publish();
  }
  /**
   * Remembers the invalidated base that the base-free comparison descriptor
   * `descriptor` is about to drop. The record belongs to that descriptor only,
   * so an unrelated retained base-free tab can neither consume nor overwrite it.
   */
  depart(owner: string, descriptor: string, reference: BaseReference) {
    const trust = this.owners.get(owner);
    if (!trust) return;
    if (trust.invalidated.has(referenceKey(reference)))
      trust.departed.set(descriptor, reference);
    // A departure from a still-trusted base supersedes an older record.
    else trust.departed.delete(descriptor);
  }
  /** The descriptor was left from Branch without an invalidated base. */
  forget(owner: string, descriptor: string) {
    this.owners.get(owner)?.departed.delete(descriptor);
  }
  /**
   * Shares a descriptor's dropped base with the base-free descriptor it opens.
   * The source tab stays retained, so its own record is kept; a record that the
   * destination already owns is never overwritten.
   */
  move(owner: string, from: string, to: string) {
    const trust = this.owners.get(owner),
      reference = trust?.departed.get(from);
    if (!trust || !reference || from === to || trust.departed.has(to)) return;
    trust.departed.set(to, reference);
  }
  /** The base dropped by `descriptor`, while it is still invalidated. */
  departed(owner: string, descriptor: string) {
    const reference = this.owners.get(owner)?.departed.get(descriptor);
    return reference && this.isInvalidated(owner, reference)
      ? reference
      : undefined;
  }
}

export const diffBaseTrust = new DiffBaseTrust();

/** Subscribes the caller to invalidation changes of the shared record. */
export function useDiffBaseTrust(store: DiffBaseTrust = diffBaseTrust) {
  useSyncExternalStore(store.subscribe, store.snapshot);
  return store;
}
