// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError } from "@connectrpc/connect";
import type { Document } from "./documents";

export interface RevertPresentation {
  pending?: { action: string; message: string; context: bigint; draft: string };
  replacement?: Document;
  restoredAction?: string;
}
const empty: RevertPresentation = Object.freeze({});

/** Original draft guards belong to the connection, never to a presenter. */
export class SessionRevertController {
  private entries = new Map<string, RevertPresentation>();
  private listeners = new Set<() => void>();
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  get = (id: string) => this.entries.get(id) ?? empty;
  set(id: string, update: (current: RevertPresentation) => RevertPresentation) {
    const next = update(this.get(id));
    const size = (value: RevertPresentation) => new TextEncoder().encode(JSON.stringify(value, (_key, field) => typeof field === "bigint" ? String(field) : field)).byteLength;
    const bytes = [...this.entries].reduce((sum, [key, value]) => sum + (key === id ? 0 : size(value)), size(next));
    if (bytes > 8 << 20) throw new ConnectError("Resolve pending Revert drafts before retaining more input.", Code.ResourceExhausted);
    if (!this.entries.has(id) && this.entries.size >= 1000) {
      const disposable = [...this.entries].find(([, value]) => !value.pending && !value.replacement);
      if (!disposable) throw new ConnectError("Resolve pending Revert operations before starting more work.", Code.ResourceExhausted);
      this.entries.delete(disposable[0]);
    }
    this.entries.set(id, next);
    for (const listener of this.listeners) listener();
  }
  rejected(key: string, request: object) {
    const mutation = (request as { mutation?: { id?: string; requestId?: string } }).mutation;
    const id = mutation?.id;
    if (!id || key !== `revert:${id}` || this.get(id).pending?.action !== mutation?.requestId) return;
    this.set(id, current => ({ ...current, pending: undefined }));
  }
  clear() { this.entries.clear(); this.listeners.clear(); }
}
