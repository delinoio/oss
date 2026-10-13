// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useContext,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import {
  terminalFallback,
  TerminalPresentation,
} from "./terminal-presentation";
import { Comparison, type BaseReference } from "./session-diff-model";

export enum SessionTabKind {
  Conversation = "conversation",
  Files = "files",
  File = "file",
  Diff = "diff",
  Comparison = "comparison",
  Terminal = "terminal",
  Page = "page",
  Diagnostics = "diagnostics",
  Sidechat = "sidechat",
  PendingSidechat = "pending-sidechat",
}
export type SessionTab =
  | {
      kind:
        | SessionTabKind.Conversation
        | SessionTabKind.Files
        | SessionTabKind.Diff
        | SessionTabKind.Diagnostics;
    }
  | { kind: SessionTabKind.File; repository: string; path: string }
  | {
      kind: SessionTabKind.Comparison;
      repository: string;
      comparison: Comparison;
      path: string;
      base_ref?: BaseReference;
      /**
       * The invalidated explicit base that this base-free tab dropped. It is
       * owned by the tab (not part of its identity), so closing the tab ends
       * it and a recreated tab starts without it.
       */
      dropped_base?: BaseReference;
    }
  | { kind: SessionTabKind.Terminal; id: string }
  | {
      kind: SessionTabKind.Page;
      profile: string;
      id: string;
      title: string;
      label?: string;
    }
  | { kind: SessionTabKind.Sidechat; id: string; name: string }
  | { kind: SessionTabKind.PendingSidechat; requestId: string; name: string };
export const conversationTab: SessionTab = {
  kind: SessionTabKind.Conversation,
};
export function sessionTabKey(tab: SessionTab): string {
  switch (tab.kind) {
    case SessionTabKind.File:
      return JSON.stringify([tab.kind, tab.repository, tab.path]);
    case SessionTabKind.Comparison:
      return JSON.stringify([
        tab.kind,
        tab.repository,
        tab.comparison,
        tab.path,
        ...(tab.base_ref
          ? [tab.base_ref.type, tab.base_ref.name, tab.base_ref.remote]
          : []),
      ]);
    case SessionTabKind.Terminal:
    case SessionTabKind.Sidechat:
      return JSON.stringify([tab.kind, tab.id]);
    case SessionTabKind.PendingSidechat:
      return JSON.stringify([tab.kind, tab.requestId]);
    case SessionTabKind.Page:
      return JSON.stringify([tab.kind, tab.profile, tab.id]);
    default:
      return tab.kind;
  }
}
export interface SessionTabsSnapshot {
  readonly tabs: readonly SessionTab[];
  readonly selected: string;
}
const initial: SessionTabsSnapshot = {
  tabs: [conversationTab],
  selected: SessionTabKind.Conversation,
};
type SidechatTab = Extract<SessionTab, { kind: SessionTabKind.Sidechat }>;

/** Connection-owned descriptors contain no resource bodies, preview bytes or requests. */
export class SessionTabsStore {
  private terminals = new Map<string, TerminalPresentation>();
  terminalPresentation(id: string) {
    let value = this.terminals.get(id);
    if (!value) {
      value = new TerminalPresentation();
      this.terminals.set(id, value);
    }
    return value;
  }
  private parents = new Map<string, string>();
  private children = new Map<string, Map<string, SidechatTab>>();
  private sessions = new Map<string, SessionTabsSnapshot>();
  private listeners = new Set<() => void>();
  subscribe = (callback: () => void) => {
    this.listeners.add(callback);
    return () => {
      this.listeners.delete(callback);
    };
  };
  snapshot = (id: string) => this.sessions.get(id) ?? initial;
  private publish(id: string, value: SessionTabsSnapshot) {
    this.sessions.set(id, value);
    for (const callback of this.listeners) callback();
  }
  /**
   * `inheritDropped` keeps the `dropped_base` that an already open destination
   * Comparison tab owns; the incoming value only fills a destination without
   * one. Without it the incoming `dropped_base` (or its absence) replaces the
   * destination's.
   */
  open(
    id: string,
    tab: SessionTab,
    options?: { inheritDropped?: boolean },
  ) {
    if (
      tab.kind === SessionTabKind.Terminal &&
      this.terminalPresentation(id).hidden(tab.id)
    )
      return;
    // Opening a verified terminal assigns its stable presentation number.
    if (tab.kind === SessionTabKind.Terminal)
      this.terminalPresentation(id).number(tab.id);
    if (tab.kind === SessionTabKind.Sidechat) {
      this.parents.set(tab.id, id);
      const children = this.children.get(id) ?? new Map<string, SidechatTab>();
      children.set(tab.id, tab);
      this.children.set(id, children);
    }
    const previous = this.snapshot(id),
      key = sessionTabKey(tab);
    const found = previous.tabs.findIndex(
      (value) => sessionTabKey(value) === key,
    );
    const tabs = [...previous.tabs];
    const existing = previous.tabs[found];
    if (found < 0) tabs.push(tab);
    else if (
      options?.inheritDropped &&
      tab.kind === SessionTabKind.Comparison &&
      existing?.kind === SessionTabKind.Comparison &&
      existing.dropped_base
    )
      tabs[found] = { ...tab, dropped_base: existing.dropped_base };
    else tabs[found] = tab;
    this.publish(id, { tabs, selected: key });
  }
  select(id: string, key: string) {
    const previous = this.snapshot(id);
    if (
      previous.tabs.some((tab) => sessionTabKey(tab) === key) &&
      previous.selected !== key
    ) {
      this.publish(id, { ...previous, selected: key });
    }
  }
  /**
   * Refreshes the presentation fields of an existing Page descriptor addressed
   * by its exact profile/page identity. Order, selection and foreign pages are
   * never touched, and an unchanged label publishes nothing.
   */
  updatePage(
    id: string,
    page: { profile: string; id: string; title: string; label?: string },
  ) {
    const previous = this.snapshot(id),
      key = sessionTabKey({ kind: SessionTabKind.Page, ...page });
    const found = previous.tabs.findIndex(
      (value) => sessionTabKey(value) === key,
    );
    const current = previous.tabs[found];
    if (
      !current ||
      current.kind !== SessionTabKind.Page ||
      (current.title === page.title && current.label === page.label)
    )
      return;
    const tabs = [...previous.tabs];
    tabs[found] = { ...current, title: page.title, label: page.label };
    this.publish(id, { ...previous, tabs });
  }
  /**
   * Removes exactly one acknowledged Page descriptor by profile/page identity.
   * Closing the final browser Page leaves the workspace on Conversation;
   * otherwise the shared close rule selects the existing left neighbor.
   */
  closePage(id: string, profile: string, pageId: string) {
    const key = sessionTabKey({
      kind: SessionTabKind.Page,
      profile,
      id: pageId,
      title: "",
    });
    const previous = this.snapshot(id);
    const last =
      previous.selected === key &&
      previous.tabs.filter((tab) => tab.kind === SessionTabKind.Page).length ===
        1;
    this.close(id, key);
    if (last) this.select(id, SessionTabKind.Conversation);
  }
  /**
   * Aligns the Page descriptors with one native inventory of the admitted
   * profile. Pages already listed keep their order and selection and only
   * refresh their label; newly discovered pages are appended in inventory
   * order without being selected; descriptors the inventory no longer lists
   * (and foreign-profile descriptors) are removed unless a pending close still
   * owns them. A restoration never opens or selects a page.
   */
  reconcilePages(
    id: string,
    profile: string,
    pages: readonly { id: string; title: string; label: string }[],
    owned: ReadonlySet<string> = new Set(),
  ) {
    for (const page of pages) this.updatePage(id, { profile, ...page });
    const listed = new Set(pages.map((page) => page.id));
    for (const tab of this.snapshot(id).tabs) {
      if (
        tab.kind === SessionTabKind.Page &&
        (tab.profile !== profile || !listed.has(tab.id)) &&
        !(tab.profile === profile && owned.has(tab.id))
      )
        this.closePage(id, tab.profile, tab.id);
    }
    const previous = this.snapshot(id);
    const known = new Set(
      previous.tabs.flatMap((tab) =>
        tab.kind === SessionTabKind.Page && tab.profile === profile
          ? [tab.id]
          : [],
      ),
    );
    const added = pages
      .filter((page) => !known.has(page.id))
      .map((page) => ({
        kind: SessionTabKind.Page as const,
        profile,
        ...page,
      }));
    if (added.length)
      this.publish(id, { ...previous, tabs: [...previous.tabs, ...added] });
  }
  position(id: string, number: number) {
    const tab = this.snapshot(id).tabs[number - 1];
    if (number >= 1 && number <= 9 && tab) this.select(id, sessionTabKey(tab));
  }
  close(id: string, key: string) {
    const previous = this.snapshot(id),
      index = previous.tabs.findIndex((tab) => sessionTabKey(tab) === key);
    if (index <= 0) return;
    this.publish(id, {
      tabs: previous.tabs.filter((_, at) => at !== index),
      selected:
        previous.selected === key
          ? sessionTabKey(previous.tabs[index - 1]!)
          : previous.selected,
    });
  }
  dismissTerminal(id: string, terminalId: string, inventoryFallback = "") {
    const key = sessionTabKey({
      kind: SessionTabKind.Terminal,
      id: terminalId,
    });
    const previous = this.snapshot(id);
    const selected = previous.selected === key;
    const next = selected
      ? terminalFallback(
          previous.tabs.flatMap((tab) =>
            tab.kind === SessionTabKind.Terminal ? [tab.id] : [],
          ),
          terminalId,
        ) || inventoryFallback
      : "";
    this.close(id, key);
    if (next) this.open(id, { kind: SessionTabKind.Terminal, id: next });
    return this.snapshot(id).selected;
  }
  // Authoring is connection-owned, never part of a descriptor or Session RPC ID.
  private childDrafts = new Map<
    string,
    { text: string; start: number; end: number; focus: boolean }
  >();
  childDraft(id: string) {
    return this.childDrafts.get(id);
  }
  takeChildFocus(id: string) {
    this.childDrafts.delete(id);
  }
  publishSidechat(
    parent: string,
    requestId: string,
    tab: SidechatTab,
    draft: { text: string; start: number; end: number; focus: boolean },
  ) {
    const previous = this.snapshot(parent);
    const key = sessionTabKey({
      kind: SessionTabKind.PendingSidechat,
      requestId,
      name: "",
    });
    const index = previous.tabs.findIndex(
      (value) => sessionTabKey(value) === key,
    );
    this.childDrafts.set(tab.id, draft);
    this.parents.set(tab.id, parent);
    const children =
      this.children.get(parent) ?? new Map<string, SidechatTab>();
    children.set(tab.id, tab);
    this.children.set(parent, children);
    // Publication cannot reopen closed presentation or select a background result.
    if (index < 0) return;
    const tabs = [...previous.tabs];
    tabs[index] = tab;
    this.publish(parent, {
      tabs,
      selected:
        previous.selected === key ? sessionTabKey(tab) : previous.selected,
    });
  }
  // Child controllers survive presentation close and retain their original parent.
  sidechats(parent: string) {
    return [...(this.children.get(parent)?.values() ?? [])];
  }
  parent(child: string) {
    return this.parents.get(child);
  }
}
const Context = createContext<SessionTabsStore | undefined>(undefined);
export function SessionTabsProvider({ children }: { children: ReactNode }) {
  const [store] = useState(() => new SessionTabsStore());
  return <Context.Provider value={store}>{children}</Context.Provider>;
}
export function useSessionTabsStore() {
  const context = useContext(Context),
    [fallback] = useState(() => new SessionTabsStore());
  return context ?? fallback;
}
export function useSessionTabs(id: string) {
  const store = useSessionTabsStore();
  const state = useSyncExternalStore(store.subscribe, () => store.snapshot(id));
  return {
    store,
    ...state,
    tab:
      state.tabs.find((tab) => sessionTabKey(tab) === state.selected) ??
      conversationTab,
  };
}
