// SPDX-License-Identifier: Apache-2.0
import { resolveMessage, type OwnedMessage } from "./localization";
export enum ToastKind { Success = "success", Info = "info", Warning = "warning", Error = "error" }
export enum ToastPause { Hover, Focus }
export interface ToastOptions { kind: ToastKind; message: string | OwnedMessage; id?: string; durationMs?: number }
export interface ToastNotification { id: string; kind: ToastKind; message: string | OwnedMessage }
export interface NotificationController { notify(options: ToastOptions): string; dismiss(id: string): void }
interface Entry extends ToastNotification {
  remaining: number;
  duration: number;
  pauses: Set<ToastPause>;
  started?: number;
  timer?: ReturnType<typeof setTimeout>;
}

// Each provider owns one store. Only its viewport subscribes; publishing a
// notification must not rerender the connection's queries or conversation.
export class ToastStore implements NotificationController {
  private alive = true;
  private suspended = false;
  private sequence = 0;
  private readonly entries = new Map<string, Entry>();
  private readonly listeners = new Set<() => void>();
  private snapshot: readonly ToastNotification[] = [];
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  getSnapshot = () => this.snapshot;
  activate() { this.alive = true; this.schedule(); }
  dispose() {
    this.alive = false;
    for (const entry of this.entries.values()) this.stop(entry);
    this.entries.clear();
    this.publish();
  }
  notify = ({ kind, message, id = `toast-${++this.sequence}`, durationMs = 5000 }: ToastOptions): string => {
    if (!this.alive) return id;
    const presentation = resolveMessage(message)!;
    if (!Object.values(ToastKind).includes(kind) || !presentation.trim() || presentation.length > 4096) throw new Error("Toast content is invalid.");
    if (!Number.isInteger(durationMs) || durationMs < 0 || durationMs > 0x7fffffff) throw new Error("Toast duration is invalid.");
    const previous = this.entries.get(id);
    if (previous) this.stop(previous);
    // Preserve arrival order and hover/focus pauses when an existing ID changes.
    this.entries.set(id, { id, kind, message, duration: durationMs, remaining: durationMs, pauses: previous?.pauses ?? new Set() });
    if (this.entries.size > 20) {
      const oldestQueued = [...this.entries.keys()][3]!;
      this.entries.delete(oldestQueued);
    }
    this.schedule();
    this.publish();
    return id;
  };
  dismiss = (id: string) => {
    const entry = this.entries.get(id);
    if (!entry) return;
    this.stop(entry);
    this.entries.delete(id);
    this.schedule();
    this.publish();
  };
  pause(id: string, reason: ToastPause, paused: boolean) {
    const entry = this.entries.get(id);
    if (!entry) return;
    if (paused) entry.pauses.add(reason); else entry.pauses.delete(reason);
    this.schedule();
  }
  suspend(value: boolean) { this.suspended = value; this.schedule(); }
  private stop(entry: Entry) {
    if (entry.timer === undefined) return;
    clearTimeout(entry.timer);
    entry.remaining = Math.max(0, entry.remaining - (performance.now() - entry.started!));
    entry.timer = undefined;
    entry.started = undefined;
  }
  private schedule() {
    let index = 0;
    for (const entry of this.entries.values()) {
      const running = this.alive && !this.suspended && index++ < 3 && entry.pauses.size === 0 && entry.duration !== 0;
      if (!running) { this.stop(entry); continue; }
      if (entry.timer !== undefined) continue;
      entry.started = performance.now();
      entry.timer = setTimeout(() => { if (this.entries.get(entry.id) === entry) this.dismiss(entry.id); }, entry.remaining);
    }
  }
  private publish() {
    this.snapshot = [...this.entries.values()].slice(0, 3).map(({ id, kind, message }) => ({ id, kind, message }));
    for (const listener of this.listeners) listener();
  }
}
