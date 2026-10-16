// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useContext,
  useEffect,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import {
  terminalFallback,
  TerminalPresentation,
} from "./terminal-presentation";
import {
  Comparison,
  sameReference,
  type BaseReference,
} from "./session-diff-model";
import { PendingCloseRegistry } from "./session-pending-close";

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
  | {
      kind: SessionTabKind.Sidechat;
      id: string;
      name: string;
      /**
       * The name is a product-identity placeholder because the descriptor was
       * restored before its Session could be verified. It is presentation
       * state, not identity, so it is not part of the tab key.
       */
      provisional?: boolean;
    }
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
type ComparisonTab = Extract<SessionTab, { kind: SessionTabKind.Comparison }>;

/** Connection-owned descriptors contain no resource bodies, preview bytes or requests. */
export class SessionTabsStore {
  constructor(private pendingCloses = new PendingCloseRegistry()) {
    // A sibling window proved a Close complete: retained Sessions here have no
    // inventory poll, so drop the dead descriptor without re-broadcasting.
    this.pendingCloses.onSettled((key) => {
      try {
        const [profile, pageId] = JSON.parse(key) as [string, string];
        if (typeof profile === "string" && typeof pageId === "string")
          this.closePageInSessions(profile, pageId);
      } catch {
        // A malformed key from another window is ignored.
      }
    });
  }
  /** (Re)opens the cross-window pending-close channel; idempotent. */
  connect() {
    this.pendingCloses.connect();
  }
  /** Releases holds and closes the channel when the store is discarded. */
  dispose() {
    this.pendingCloses.dispose();
  }
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
  // Sidechat children whose latest tab publication was automatic (a pending
  // tab promoted or published in the background) rather than a user `open`.
  private automatic = new Set<string>();
  automaticSidechat = (id: string) => this.automatic.has(id);
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
  // Monotonic per-Session navigation generation: bumped whenever a publish
  // changes the selected tab, so a pending acknowledgment can tell that the
  // user navigated after sending even when the same key is selected again.
  private navigations = new Map<string, number>();
  navigation = (id: string) => this.navigations.get(id) ?? 0;
  private publish(id: string, value: SessionTabsSnapshot) {
    if (value.selected !== this.snapshot(id).selected)
      this.navigations.set(id, this.navigation(id) + 1);
    this.sessions.set(id, value);
    for (const callback of this.listeners) callback();
  }
  // A Sidechat child must be a nonempty Session distinct from its parent and
  // from every ancestor, so malformed ownership can never form a render cycle.
  private validChild(parent: string, child: string) {
    if (!child) return false;
    const seen = new Set<string>();
    for (
      let ancestor: string | undefined = parent;
      ancestor !== undefined && !seen.has(ancestor);
      ancestor = this.parents.get(ancestor)
    ) {
      if (ancestor === child) return false;
      seen.add(ancestor);
    }
    return true;
  }
  open(id: string, tab: SessionTab) {
    if (tab.kind === SessionTabKind.Sidechat && !this.validChild(id, tab.id))
      return;
    if (
      tab.kind === SessionTabKind.Terminal &&
      this.terminalPresentation(id).hidden(tab.id)
    )
      return;
    // Opening a verified terminal assigns its stable presentation number.
    if (tab.kind === SessionTabKind.Terminal)
      this.terminalPresentation(id).number(tab.id);
    if (tab.kind === SessionTabKind.Sidechat) {
      this.automatic.delete(tab.id);
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
    if (found < 0) tabs.push(tab);
    else tabs[found] = tab;
    this.publish(id, { tabs, selected: key });
  }
  /**
   * Opens a Comparison tab. With `inheritDropped`, the `dropped_base` that an
   * already open destination owns is kept and the incoming one only fills a
   * destination without a record. Otherwise the incoming `dropped_base` (or
   * its absence) replaces the destination's.
   */
  openComparison(id: string, tab: ComparisonTab, inheritDropped = false) {
    const key = sessionTabKey(tab),
      existing = this.snapshot(id).tabs.find(
        (value) => sessionTabKey(value) === key,
      );
    this.open(
      id,
      inheritDropped &&
        existing?.kind === SessionTabKind.Comparison &&
        existing.dropped_base
        ? { ...tab, dropped_base: existing.dropped_base }
        : tab,
    );
  }
  /**
   * Ends every `dropped_base` record of one repository's Comparison tabs that
   * names the reference an explicit Base control selection just restored. The
   * invalidation those records belong to is over, so a later invalidation of
   * the same reference never binds a tab that did not take part in it. Tab
   * order, selection and identity are untouched, and nothing publishes when no
   * record matches.
   */
  clearDroppedBase(id: string, repository: string, reference: BaseReference) {
    const previous = this.snapshot(id);
    let changed = false;
    const tabs = previous.tabs.map((tab) => {
      if (
        tab.kind !== SessionTabKind.Comparison ||
        tab.repository !== repository ||
        !tab.dropped_base ||
        !sameReference(tab.dropped_base, reference)
      )
        return tab;
      changed = true;
      const ended: ComparisonTab = { ...tab };
      delete ended.dropped_base;
      return ended;
    });
    if (changed) this.publish(id, { ...previous, tabs });
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
   * Removes one acknowledged Page descriptor from every Session snapshot that
   * still holds it, including inactive and retained Sessions. The native close
   * is profile-scoped, so the descriptor is dead everywhere once it is
   * acknowledged. Each affected Session follows the `closePage` selection rule
   * only when its selected descriptor was removed; Sessions without the exact
   * profile/page descriptor publish nothing, which keeps repeated
   * acknowledgment idempotent. No native operation is issued.
   */
  closePageEverywhere(profile: string, pageId: string) {
    this.closePageInSessions(profile, pageId);
    // The proof is profile-scoped: sibling product windows drop it as well.
    this.pendingCloses.settle(this.pendingCloseKey(profile, pageId));
  }
  private closePageInSessions(profile: string, pageId: string) {
    const key = sessionTabKey({
      kind: SessionTabKind.Page,
      profile,
      id: pageId,
      title: "",
    });
    const holders = [...this.sessions.entries()]
      .filter(([, snapshot]) =>
        snapshot.tabs.some((tab) => sessionTabKey(tab) === key),
      )
      .map(([id]) => id);
    for (const id of holders) this.closePage(id, profile, pageId);
  }
  // Unsettled original Close operations by profile/page key. The close is
  // profile-scoped, so every Session and every same-origin product window
  // keeps the descriptor until the owning view observes the original proof.
  // Product windows have independent renderer memory, so the registry also
  // exchanges its holds over a same-origin channel.
  private pendingCloseKey(profile: string, pageId: string) {
    return JSON.stringify([profile, pageId]);
  }
  /** Marks a page as owned by an unsettled Close; `owner` identifies the holder.
   * Resolves once every other product window recorded the hold. */
  holdPendingClose(profile: string, pageId: string, owner: object) {
    return this.pendingCloses.hold(
      this.pendingCloseKey(profile, pageId),
      owner,
    );
  }
  /** Whether `owner`'s hold is still announced (false after pagehide/dispose). */
  holdsPendingClose(profile: string, pageId: string, owner: object) {
    return this.pendingCloses.holds(
      this.pendingCloseKey(profile, pageId),
      owner,
    );
  }
  /** Records that `owner`'s native Close was dispatched, so its hold is
   * restored after a bfcache return. */
  markPendingCloseSent(profile: string, pageId: string, owner: object) {
    this.pendingCloses.markSent(this.pendingCloseKey(profile, pageId), owner);
  }
  /** Releases one holder once its Close settled, was rejected or was abandoned. */
  releasePendingClose(profile: string, pageId: string, owner: object) {
    this.pendingCloses.release(this.pendingCloseKey(profile, pageId), owner);
  }
  /**
   * Aligns the Page descriptors with one native inventory of the admitted
   * profile. Pages already listed keep their order and selection and only
   * refresh their label; newly discovered pages are appended in inventory
   * order without being selected; descriptors the inventory no longer lists
   * (and foreign-profile descriptors) are removed unless a pending close still
   * owns them in any Session or product window. A restoration never opens or selects a page.
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
        !(
          tab.profile === profile &&
          (owned.has(tab.id) ||
            this.pendingCloses.has(this.pendingCloseKey(profile, tab.id)))
        )
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
  /**
   * Applies an authoritative removal-pending (unavailable) profile to every
   * retained Session snapshot, including inactive Sessions that never poll.
   * Only Page descriptors of exactly `profile` are removed, each through the
   * `closePage` selection rule. Descriptors owned by the caller's operation
   * (`owned`) or by an unsettled original Close in any Session or product
   * window survive. Other profiles, non-Page tabs, ordering and unrelated
   * selections are untouched. The reconciliation is store-only: it never
   * sends a native operation, never selects a Session and is idempotent
   * because Sessions without a removable descriptor publish nothing.
   */
  reconcileUnavailableProfile(
    profile: string,
    owned: ReadonlySet<string> = new Set(),
  ) {
    const removals = [...this.sessions.entries()].flatMap(([id, snapshot]) =>
      snapshot.tabs.flatMap((tab) =>
        tab.kind === SessionTabKind.Page &&
        tab.profile === profile &&
        !owned.has(tab.id) &&
        !this.pendingCloses.has(this.pendingCloseKey(profile, tab.id))
          ? [{ id, page: tab.id }]
          : [],
      ),
    );
    for (const { id, page } of removals) this.closePage(id, profile, page);
    if (removals.length)
      console.info("delidev.browser_unavailable_profile", {
        phase: "retained_sessions_reconciled",
        profile,
        removed: removals.length,
        sessions: new Set(removals.map(({ id }) => id)).size,
      });
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
  /** Opened Terminal content tabs that are not presentation tombstoned. */
  liveTerminals(id: string) {
    const presentation = this.terminalPresentation(id);
    return this.snapshot(id).tabs.flatMap((tab) =>
      tab.kind === SessionTabKind.Terminal && !presentation.hidden(tab.id)
        ? [tab.id]
        : [],
    );
  }
  dismissTerminal(id: string, terminalId: string, inventoryFallback = "") {
    const key = sessionTabKey({
      kind: SessionTabKind.Terminal,
      id: terminalId,
    });
    const previous = this.snapshot(id);
    const selected = previous.selected === key;
    // A tombstoned terminal tab (dismissed earlier in the same observation
    // batch) is never a fallback target; only live opened terminals qualify.
    const presentation = this.terminalPresentation(id);
    const next = selected
      ? terminalFallback(
          previous.tabs.flatMap((tab) =>
            tab.kind === SessionTabKind.Terminal &&
            (tab.id === terminalId || !presentation.hidden(tab.id))
              ? [tab.id]
              : [],
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
    // Reject malformed child identity before any ownership or draft mutation.
    if (!this.validChild(parent, tab.id)) return false;
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
    this.automatic.add(tab.id);
    this.parents.set(tab.id, parent);
    const children =
      this.children.get(parent) ?? new Map<string, SidechatTab>();
    children.set(tab.id, tab);
    this.children.set(parent, children);
    // Publication cannot reopen closed presentation or select a background result.
    if (index < 0) return true;
    const tabs = [...previous.tabs];
    tabs[index] = tab;
    this.publish(parent, {
      tabs,
      selected:
        previous.selected === key ? sessionTabKey(tab) : previous.selected,
    });
    return true;
  }
  /** Whether the Sidechat descriptor still carries a restore placeholder name. */
  provisionalSidechat(parent: string, id: string) {
    return this.children.get(parent)?.get(id)?.provisional === true;
  }
  /**
   * Replaces the placeholder name of a restored Sidechat descriptor with the
   * verified name. Order and selection are untouched, and nothing publishes for
   * a descriptor that is not provisional.
   */
  settleSidechat(parent: string, id: string, name: string) {
    const current = this.children.get(parent)?.get(id);
    if (!current?.provisional) return;
    const settled: SidechatTab = { kind: SessionTabKind.Sidechat, id, name };
    this.children.get(parent)!.set(id, settled);
    const previous = this.snapshot(parent),
      key = sessionTabKey(settled);
    const found = previous.tabs.findIndex(
      (value) => sessionTabKey(value) === key,
    );
    if (found < 0) return;
    const tabs = [...previous.tabs];
    tabs[found] = settled;
    this.publish(parent, { ...previous, tabs });
  }
  /**
   * Ends a restored Sidechat descriptor that its own Session proved is not a
   * Sidechat of this parent: the placeholder tab and its ownership are removed.
   * A verified descriptor is never touched.
   */
  discardProvisionalSidechat(parent: string, id: string) {
    if (!this.provisionalSidechat(parent, id)) return;
    this.children.get(parent)?.delete(id);
    if (this.parents.get(id) === parent) this.parents.delete(id);
    this.close(
      parent,
      sessionTabKey({ kind: SessionTabKind.Sidechat, id, name: "" }),
    );
  }
  // Child controllers survive presentation close and retain their original parent.
  sidechats(parent: string) {
    return [...(this.children.get(parent)?.values() ?? [])].filter((tab) =>
      this.validChild(parent, tab.id),
    );
  }
  parent(child: string) {
    return this.parents.get(child);
  }
}
const Context = createContext<SessionTabsStore | undefined>(undefined);
export function SessionTabsProvider({ children }: { children: ReactNode }) {
  const [store] = useState(
    () =>
      new SessionTabsStore(new PendingCloseRegistry({ deferConnect: true })),
  );
  useEffect(() => {
    store.connect();
    return () => store.dispose();
  }, [store]);
  return <Context.Provider value={store}>{children}</Context.Provider>;
}
export function useSessionTabsStore() {
  const context = useContext(Context),
    [fallback] = useState(
      () =>
        new SessionTabsStore(new PendingCloseRegistry({ deferConnect: true })),
    );
  useEffect(() => {
    // The fallback store only serves callers outside a provider.
    if (context) {
      fallback.dispose();
      return;
    }
    fallback.connect();
    return () => fallback.dispose();
  }, [context, fallback]);
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
