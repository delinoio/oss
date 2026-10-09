// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useState, useSyncExternalStore, type ReactNode } from "react";
import { Comparison } from "./session-diff-model";

export enum SessionTabKind {
  Conversation = "conversation", Files = "files", File = "file", Diff = "diff",
  Comparison = "comparison", Terminals = "terminals", Terminal = "terminal",
  Browser = "browser", Page = "page", Diagnostics = "diagnostics", Sidechat = "sidechat",
}
export type SessionTab =
  | { kind: SessionTabKind.Conversation | SessionTabKind.Files | SessionTabKind.Diff | SessionTabKind.Terminals | SessionTabKind.Browser | SessionTabKind.Diagnostics }
  | { kind: SessionTabKind.File; repository: string; path: string }
  | { kind: SessionTabKind.Comparison; repository: string; comparison: Comparison; path: string }
  | { kind: SessionTabKind.Terminal; id: string }
  | { kind: SessionTabKind.Page; profile: string; id: string; title: string; label?: string }
  | { kind: SessionTabKind.Sidechat; id: string; name: string };
export const conversationTab: SessionTab = { kind: SessionTabKind.Conversation };
export function sessionTabKey(tab: SessionTab): string {
  switch (tab.kind) {
    case SessionTabKind.File: return JSON.stringify([tab.kind, tab.repository, tab.path]);
    case SessionTabKind.Comparison: return JSON.stringify([tab.kind, tab.repository, tab.comparison, tab.path]);
    case SessionTabKind.Terminal:
    case SessionTabKind.Sidechat: return JSON.stringify([tab.kind, tab.id]);
    case SessionTabKind.Page: return JSON.stringify([tab.kind, tab.profile, tab.id]);
    default: return tab.kind;
  }
}
export interface SessionTabsSnapshot { readonly tabs: readonly SessionTab[]; readonly selected: string }
const initial: SessionTabsSnapshot = { tabs: [conversationTab], selected: SessionTabKind.Conversation };
type SidechatTab = Extract<SessionTab, { kind: SessionTabKind.Sidechat }>;

/** Connection-owned descriptors contain no resource bodies, preview bytes or requests. */
export class SessionTabsStore {
  private parents = new Map<string, string>();
  private children = new Map<string, Map<string, SidechatTab>>();
  private sessions = new Map<string, SessionTabsSnapshot>();
  private listeners = new Set<() => void>();
  subscribe = (callback: () => void) => {
    this.listeners.add(callback);
    return () => { this.listeners.delete(callback); };
  };
  snapshot = (id: string) => this.sessions.get(id) ?? initial;
  private publish(id: string, value: SessionTabsSnapshot) {
    this.sessions.set(id, value);
    for (const callback of this.listeners) callback();
  }
  open(id: string, tab: SessionTab) {
    if (tab.kind === SessionTabKind.Sidechat) {
      this.parents.set(tab.id, id);
      const children = this.children.get(id) ?? new Map<string, SidechatTab>();
      children.set(tab.id, tab);
      this.children.set(id, children);
    }
    const previous = this.snapshot(id), key = sessionTabKey(tab);
    const found = previous.tabs.findIndex(value => sessionTabKey(value) === key);
    const tabs = [...previous.tabs];
    if (found < 0) tabs.push(tab); else tabs[found] = tab;
    this.publish(id, { tabs, selected: key });
  }
  select(id: string, key: string) {
    const previous = this.snapshot(id);
    if (previous.tabs.some(tab => sessionTabKey(tab) === key) && previous.selected !== key) {
      this.publish(id, { ...previous, selected: key });
    }
  }
  position(id: string, number: number) {
    const tab = this.snapshot(id).tabs[number - 1];
    if (number >= 1 && number <= 9 && tab) this.select(id, sessionTabKey(tab));
  }
  close(id: string, key: string) {
    const previous = this.snapshot(id), index = previous.tabs.findIndex(tab => sessionTabKey(tab) === key);
    if (index <= 0) return;
    this.publish(id, {
      tabs: previous.tabs.filter((_, at) => at !== index),
      selected: previous.selected === key ? sessionTabKey(previous.tabs[index - 1]!) : previous.selected,
    });
  }
  // Child controllers survive presentation close and retain their original parent.
  sidechats(parent: string) { return [...(this.children.get(parent)?.values() ?? [])]; }
  parent(child: string) { return this.parents.get(child); }
}
const Context = createContext<SessionTabsStore | undefined>(undefined);
export function SessionTabsProvider({ children }: { children: ReactNode }) {
  const [store] = useState(() => new SessionTabsStore());
  return <Context.Provider value={store}>{children}</Context.Provider>;
}
export function useSessionTabsStore() {
  const context = useContext(Context), [fallback] = useState(() => new SessionTabsStore());
  return context ?? fallback;
}
export function useSessionTabs(id: string) {
  const store = useSessionTabsStore();
  const state = useSyncExternalStore(store.subscribe, () => store.snapshot(id));
  return { store, ...state, tab: state.tabs.find(tab => sessionTabKey(tab) === state.selected) ?? conversationTab };
}
