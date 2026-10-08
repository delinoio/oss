// SPDX-License-Identifier: Apache-2.0
import { useCallback, useSyncExternalStore } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";

export const browserSplitterWidth = 8;
export function browserSplitBounds(available: number) {
  const wide = Number.isFinite(available) && available >= 960;
  const maximum = wide ? available - browserSplitterWidth - 360 : 480;
  return { wide, minimum: 480, maximum, initial: wide ? Math.max(480, Math.min(maximum, (available - browserSplitterWidth) * .55)) : 480 };
}
export interface BrowserSplit { width?: number; expanded: boolean }
const initialSplit: BrowserSplit = Object.freeze({ expanded: false });
export class SessionBrowserLayouts {
  private values = new Map<string, BrowserSplit>();
  private listeners = new Set<() => void>();
  read = (sessionId: string) => this.values.get(sessionId) ?? initialSplit;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  save(sessionId: string, value: BrowserSplit) {
    if (value.width !== undefined && (!Number.isFinite(value.width) || value.width < 480)) return;
    this.values.delete(sessionId); this.values.set(sessionId, Object.freeze({ ...value }));
    // Presentation metadata only. The connection owner retains at most 1,000
    // session widths; it holds no address, profile or native presentation data.
    if (this.values.size > 1000) this.values.delete(this.values.keys().next().value!);
    this.listeners.forEach(listener => listener());
  }
}
const connections = new WeakMap<QueryClient, SessionBrowserLayouts>();
export function useSessionBrowserLayout(sessionId: string, available: number) {
  const client = useQueryClient();
  let owner = connections.get(client);
  if (!owner) { owner = new SessionBrowserLayouts(); connections.set(client, owner); }
  const current = useSyncExternalStore(owner.subscribe, useCallback(() => owner!.read(sessionId), [owner, sessionId]));
  const bounds = browserSplitBounds(available);
  const clamp = (width: number) => Math.max(bounds.minimum, Math.min(bounds.maximum, width));
  const width = clamp(current.expanded ? bounds.maximum : current.width ?? bounds.initial);
  return { ...bounds, width, expanded: current.expanded, resize: (next: number) => { if (bounds.wide) owner!.save(sessionId, { width: clamp(next), expanded: false }); }, toggleExpanded: () => {
    if (bounds.wide) owner!.save(sessionId, { width: current.expanded ? current.width ?? bounds.initial : width, expanded: !current.expanded });
  } };
}
