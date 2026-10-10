// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext } from "react";

export class PRBackgroundQueue {
  private tail: Promise<unknown> = Promise.resolve();
  private controllers = new Set<AbortController>();
  readonly avatars = new Map<string, string>();
  readonly pages = new Map<number, string[]>();
  page(number: number, seeds?: string[]) { if (seeds) this.pages.set(number, seeds); else this.pages.delete(number); this.version++; for (const listener of this.listeners) listener(); }
  readonly rows = new Map<string, Record<string, unknown>>();
  private version = 0;
  private listeners = new Set<() => void>();
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };
  snapshot = () => this.version;
  publish(rows: Record<string, unknown>[]) { for (const row of rows) this.rows.set(String((row.item as Record<string, unknown>).number), row); while (this.rows.size > 100) this.rows.delete(this.rows.keys().next().value!); this.version++; for (const listener of this.listeners) listener(); }
  foreground = false;
  run<T>(signal: AbortSignal, read: (signal: AbortSignal) => Promise<T>): Promise<T> {
    const controller = new AbortController();
    const cancel = () => controller.abort();
    signal.addEventListener("abort", cancel, { once: true });
    this.controllers.add(controller);
    const job = this.tail.catch(() => undefined).then(async () => {
      if (signal.aborted || controller.signal.aborted || this.foreground) throw new DOMException("Interrupted background read", "AbortError");
      return read(controller.signal);
    }).finally(() => { signal.removeEventListener("abort", cancel); this.controllers.delete(controller); });
    this.tail = job;
    return job;
  }
  prioritize(busy: boolean) { this.foreground = busy; if (busy) for (const controller of this.controllers) controller.abort(); }
  remember(reference: string, url: string) {
    const existing = this.avatars.get(reference); if (existing) { URL.revokeObjectURL(url); return existing; }
    if (this.avatars.size >= 64) { const first = this.avatars.keys().next().value!; URL.revokeObjectURL(this.avatars.get(first)!); this.avatars.delete(first); }
    this.avatars.set(reference, url); return url;
  }
  dispose() { this.prioritize(true); for (const url of this.avatars.values()) URL.revokeObjectURL(url); this.avatars.clear(); }
}
export const PRWorkspaceContext = createContext<{ supported: boolean; busy: boolean; queue: PRBackgroundQueue } | undefined>(undefined);
export const usePRWorkspaceContext = () => useContext(PRWorkspaceContext);
