import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { describe, it, expect } from "vitest";
import {
  SessionTabsStore,
  SessionTabKind,
  sessionTabKey,
} from "./session-tabs";
import { Comparison } from "./session-diff-model";
describe("connection session tab descriptors", () => {
  it("pins Conversation and preserves append order with exact resource identities", () => {
    const store = new SessionTabsStore();
    const file = {
      kind: SessionTabKind.File as const,
      repository: "repo",
      path: "a",
    };
    store.open("s", file);
    store.open("s", { ...file, path: "b" });
    store.open("s", file);
    expect(store.snapshot("s").tabs).toHaveLength(3);
    expect(store.snapshot("s").selected).toBe(sessionTabKey(file));
    store.close("s", SessionTabKind.Conversation);
    expect(store.snapshot("s").tabs[0]?.kind).toBe(SessionTabKind.Conversation);
    store.open("s", {
      kind: SessionTabKind.Comparison,
      repository: "repo",
      comparison: Comparison.Staged,
      path: "a",
    });
    expect(store.snapshot("s").tabs).toHaveLength(4);
  });
  it("closes presentation with left fallback and keeps inactive selection", () => {
    const s = new SessionTabsStore();
    s.open("s", { kind: SessionTabKind.Terminal, id: "one" });
    s.open("s", { kind: SessionTabKind.Terminal, id: "two" });
    s.close("s", JSON.stringify(["terminal", "one"]));
    expect(s.snapshot("s").selected).toBe(JSON.stringify(["terminal", "two"]));
    s.close("s", JSON.stringify(["terminal", "two"]));
    expect(s.snapshot("s").selected).toBe("conversation");
  });
  it("selects exact positions1 and9 with no last-tab alias or absent selection", () => {
    const s = new SessionTabsStore();
    for (let n = 0; n < 10; n++)
      s.open("s", { kind: SessionTabKind.Terminal, id: String(n) });
    s.position("s", 9);
    expect(s.snapshot("s").selected).toBe(JSON.stringify(["terminal", "7"]));
    s.position("s", 1);
    expect(s.snapshot("s").selected).toBe("conversation");
    s.position("other", 9);
    expect(s.snapshot("other").selected).toBe("conversation");
    s.position("s", 10);
    expect(s.snapshot("s").selected).toBe("conversation");
  });
  it("scopes order to session/connection and retains original Sidechat parent", () => {
    const s = new SessionTabsStore();
    s.open("parent", {
      kind: SessionTabKind.Sidechat,
      id: "child",
      name: "Child",
    });
    expect(s.parent("child")).toBe("parent");
    expect(s.snapshot("other").tabs).toHaveLength(1);
    expect(new SessionTabsStore().snapshot("parent").tabs).toHaveLength(1);
  });
});
it("retains Sidechat parent and safe descriptor after presentation close", () => {
  const store = new SessionTabsStore();
  const child = {
    kind: SessionTabKind.Sidechat as const,
    id: "child",
    name: "Original child",
  };
  store.open("parent", child);
  store.close("parent", sessionTabKey(child));
  expect(store.parent("child")).toBe("parent");
  expect(store.sidechats("parent")).toEqual([child]);
  store.open("parent", child);
  expect(store.snapshot("parent").tabs).toHaveLength(2);
});

it("dismisses the selected terminal toward its nearest left terminal across other content tabs", () => {
  const store = new SessionTabsStore();
  store.open("session", { kind: SessionTabKind.Terminal, id: "one" });
  store.open("session", { kind: SessionTabKind.Files });
  store.open("session", { kind: SessionTabKind.Terminal, id: "two" });
  store.open("session", { kind: SessionTabKind.Terminal, id: "three" });
  store.select(
    "session",
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "two" }),
  );
  store.dismissTerminal("session", "two", "three");
  expect(store.snapshot("session").selected).toBe(
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "one" }),
  );
  store.dismissTerminal("session", "one");
  expect(store.snapshot("session").selected).toBe(
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "three" }),
  );
});
it("keeps inactive terminal dismissal selection and uses inventory only without another opened terminal", () => {
  const store = new SessionTabsStore();
  store.open("session", { kind: SessionTabKind.Terminal, id: "one" });
  store.open("session", { kind: SessionTabKind.Files });
  store.dismissTerminal("session", "one", "unopened");
  expect(store.snapshot("session").selected).toBe(SessionTabKind.Files);
  expect(
    store
      .snapshot("session")
      .tabs.some((tab) => tab.kind === SessionTabKind.Terminal),
  ).toBe(false);
  store.open("session", { kind: SessionTabKind.Terminal, id: "one" });
  store.dismissTerminal("session", "one", "remaining");
  expect(store.snapshot("session").selected).toBe(
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "remaining" }),
  );
});

it("dismissal skips tombstoned terminal tabs and liveTerminals excludes them", () => {
  const store = new SessionTabsStore();
  const exited = (id: string) =>
    create(ResourceSchema, {
      id,
      sessionId: "session",
      kind: EntityKind.TERMINAL,
      schemaVersion: 1,
      revision: 5n,
      documentJson: encode({ state: "exited", cleanup_verified: true }),
    });
  for (const id of ["a", "b", "c"])
    store.open("session", { kind: SessionTabKind.Terminal, id });
  store.select(
    "session",
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "c" }),
  );
  // C and B exit in one refresh while C is selected: A is the live survivor.
  store.terminalPresentation("session").observe("session", exited("c"));
  store.terminalPresentation("session").observe("session", exited("b"));
  expect(store.liveTerminals("session")).toEqual(["a"]);
  store.dismissTerminal("session", "c", "inventory");
  expect(store.snapshot("session").selected).toBe(
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "a" }),
  );
  store.dismissTerminal("session", "b");
  expect(store.snapshot("session").selected).toBe(
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "a" }),
  );
  expect(store.liveTerminals("session")).toEqual(["a"]);
  expect(store.liveTerminals("other")).toEqual([]);
});

it("late original creation receipts cannot reopen a dismissed content tab", () => {
  const store = new SessionTabsStore();
  store.terminalPresentation("session").observe(
    "session",
    create(ResourceSchema, {
      id: "original",
      sessionId: "session",
      kind: EntityKind.TERMINAL,
      schemaVersion: 1,
      revision: 5n,
      documentJson: encode({ state: "exited", cleanup_verified: true }),
    }),
  );
  store.open("session", { kind: SessionTabKind.Terminal, id: "original" });
  expect(store.snapshot("session").tabs).toHaveLength(1);
  expect(store.snapshot("session").selected).toBe(SessionTabKind.Conversation);
});
describe("shared Page descriptors follow native browser pages", () => {
  const page = (profile: string, id: string, title = id, label = title) => ({
    kind: SessionTabKind.Page as const,
    profile,
    id,
    title,
    label,
  });
  const setup = () => {
    const store = new SessionTabsStore();
    store.open("s", page("p1", "a"));
    store.open("s", page("p1", "b"));
    store.open("s", page("p2", "a"));
    store.select("s", sessionTabKey(page("p1", "a")));
    return store;
  };
  it("updates only the exact owned label without changing order or selection", () => {
    const store = setup();
    const before = store.snapshot("s");
    store.updatePage("s", {
      profile: "p1",
      id: "b",
      title: "moved.test/next",
      label: "https://moved.test/next",
    });
    const after = store.snapshot("s");
    expect(after.selected).toBe(before.selected);
    expect(after.tabs.map(sessionTabKey)).toEqual(
      before.tabs.map(sessionTabKey),
    );
    expect(after.tabs[2]).toMatchObject({
      title: "moved.test/next",
      label: "https://moved.test/next",
    });
    expect(after.tabs[1]).toEqual(before.tabs[1]);
    expect(after.tabs[3]).toEqual(before.tabs[3]);
  });
  it("ignores unknown pages and publishes nothing for unchanged labels", () => {
    const store = setup();
    const before = store.snapshot("s");
    store.updatePage("s", { profile: "p3", id: "a", title: "x", label: "x" });
    store.updatePage("s", { profile: "p1", id: "zzz", title: "x", label: "x" });
    store.updatePage("s", { profile: "p1", id: "a", title: "a", label: "a" });
    expect(store.snapshot("s")).toBe(before);
  });
  it("closes exactly one acknowledged page and falls back to a valid selection", () => {
    const store = setup();
    store.closePage("s", "p1", "a");
    const keys = store.snapshot("s").tabs.map(sessionTabKey);
    expect(keys).toEqual([
      SessionTabKind.Conversation,
      sessionTabKey(page("p1", "b")),
      sessionTabKey(page("p2", "a")),
    ]);
    expect(keys).toContain(store.snapshot("s").selected);
    store.closePage("s", "p9", "a");
    expect(store.snapshot("s").tabs).toHaveLength(3);
  });
  it("closes an inactive page without changing the active selection", () => {
    const store = setup();
    const selected = store.snapshot("s").selected;
    store.closePage("s", "p1", "b");
    expect(store.snapshot("s").selected).toBe(selected);
    expect(store.snapshot("s").tabs.map(sessionTabKey)).not.toContain(
      sessionTabKey(page("p1", "b")),
    );
  });
  it("selects the left neighbor after closing the active page and Conversation after the last", () => {
    const store = new SessionTabsStore();
    store.open("s", { kind: SessionTabKind.Diagnostics });
    store.open("s", page("p1", "a"));
    store.open("s", page("p1", "b"));
    store.closePage("s", "p1", "b");
    expect(store.snapshot("s").selected).toBe(sessionTabKey(page("p1", "a")));
    store.closePage("s", "p1", "a");
    expect(store.snapshot("s").selected).toBe(SessionTabKind.Conversation);
    // The unrelated tool tab and the pinned Conversation remain.
    expect(store.snapshot("s").tabs.map(sessionTabKey)).toEqual([
      SessionTabKind.Conversation,
      SessionTabKind.Diagnostics,
    ]);
  });
  it("keeps the selection when the last page closes while another tab is active", () => {
    const store = new SessionTabsStore();
    store.open("s", page("p1", "a"));
    store.open("s", { kind: SessionTabKind.Diagnostics });
    store.closePage("s", "p1", "a");
    expect(store.snapshot("s").selected).toBe(SessionTabKind.Diagnostics);
  });
  describe("restoring a native inventory", () => {
    const listed = (id: string, label = `https://${id}.test/`) => ({
      id,
      title: `${id}.test`,
      label,
    });
    it("appends discovered pages in inventory order without selecting them or duplicating", () => {
      const store = new SessionTabsStore();
      store.open("s", { kind: SessionTabKind.Diagnostics });
      const selected = store.snapshot("s").selected;
      store.reconcilePages("s", "p1", [listed("a"), listed("b")]);
      store.reconcilePages("s", "p1", [listed("a"), listed("b")]);
      expect(store.snapshot("s").tabs.map(sessionTabKey)).toEqual([
        SessionTabKind.Conversation,
        SessionTabKind.Diagnostics,
        sessionTabKey(page("p1", "a")),
        sessionTabKey(page("p1", "b")),
      ]);
      expect(store.snapshot("s").selected).toBe(selected);
    });
    it("preserves existing descriptor order and selection while refreshing labels", () => {
      const store = new SessionTabsStore();
      store.open("s", page("p1", "b"));
      store.open("s", page("p1", "a"));
      store.select("s", sessionTabKey(page("p1", "b")));
      store.reconcilePages("s", "p1", [
        listed("a", "https://moved.test/"),
        listed("b"),
        listed("c"),
      ]);
      const snapshot = store.snapshot("s");
      expect(snapshot.tabs.map(sessionTabKey)).toEqual([
        SessionTabKind.Conversation,
        sessionTabKey(page("p1", "b")),
        sessionTabKey(page("p1", "a")),
        sessionTabKey(page("p1", "c")),
      ]);
      expect(snapshot.selected).toBe(sessionTabKey(page("p1", "b")));
      expect(snapshot.tabs[2]).toMatchObject({ label: "https://moved.test/" });
    });
    it("removes descriptors the inventory no longer lists, except pages an operation still owns", () => {
      const store = new SessionTabsStore();
      store.open("s", page("p1", "a"));
      store.open("s", page("p1", "b"));
      store.open("s", page("p2", "z"));
      store.reconcilePages("s", "p1", [listed("a")], new Set(["b"]));
      expect(store.snapshot("s").tabs.map(sessionTabKey)).toEqual([
        SessionTabKind.Conversation,
        sessionTabKey(page("p1", "a")),
        sessionTabKey(page("p1", "b")),
      ]);
      store.reconcilePages("s", "p1", [listed("a")]);
      expect(store.snapshot("s").tabs.map(sessionTabKey)).toEqual([
        SessionTabKind.Conversation,
        sessionTabKey(page("p1", "a")),
      ]);
    });
    it("never creates descriptors for an empty inventory", () => {
      const store = new SessionTabsStore();
      const before = store.snapshot("s");
      store.reconcilePages("s", "p1", []);
      expect(store.snapshot("s")).toBe(before);
    });
  });
});

describe("terminal presentation numbers", () => {
  const open = (store: SessionTabsStore, session: string, id: string) =>
    store.open(session, { kind: SessionTabKind.Terminal, id });
  it("allocates one monotonic number per original ID and never reuses it", () => {
    const store = new SessionTabsStore();
    open(store, "s", "uuid-a");
    open(store, "s", "uuid-b");
    const numbers = store.terminalPresentation("s");
    expect([numbers.number("uuid-a"), numbers.number("uuid-b")]).toEqual([
      1, 2,
    ]);
    store.close(
      "s",
      sessionTabKey({ kind: SessionTabKind.Terminal, id: "uuid-a" }),
    );
    open(store, "s", "uuid-c");
    open(store, "s", "uuid-a");
    expect(numbers.number("uuid-a")).toBe(1);
    expect(numbers.number("uuid-c")).toBe(3);
  });
  it("keeps sessions independent and numbers authenticated inventory encounters before opening", () => {
    const store = new SessionTabsStore();
    const row = (id: string, sessionId: string) =>
      create(ResourceSchema, {
        id,
        sessionId,
        kind: EntityKind.TERMINAL,
        schemaVersion: 1,
        revision: 1n,
        documentJson: encode({ state: "running" }),
      });
    store.terminalPresentation("one").observe("one", row("x", "one"));
    store.terminalPresentation("one").observe("one", row("y", "one"));
    open(store, "two", "y");
    expect(store.terminalPresentation("one").number("y")).toBe(2);
    expect(store.terminalPresentation("two").number("y")).toBe(1);
  });
  it("retains a dismissed terminal number and a replacement store resets it", () => {
    const store = new SessionTabsStore();
    open(store, "s", "gone");
    store.terminalPresentation("s").observe(
      "s",
      create(ResourceSchema, {
        id: "gone",
        sessionId: "s",
        kind: EntityKind.TERMINAL,
        schemaVersion: 1,
        revision: 2n,
        documentJson: encode({ state: "exited", cleanup_verified: true }),
      }),
    );
    open(store, "s", "next");
    expect(store.terminalPresentation("s").number("gone")).toBe(1);
    expect(store.terminalPresentation("s").number("next")).toBe(2);
    expect(
      new SessionTabsStore().terminalPresentation("s").number("next"),
    ).toBe(1);
  });

  describe("Sidechat child identity", () => {
    const draft = { text: "Keep", start: 1, end: 2, focus: false };
    const pending = (store: SessionTabsStore) => {
      store.open("parent", {
        kind: SessionTabKind.PendingSidechat,
        requestId: "request",
        name: "Pending",
      });
    };
    it.each([
      ["empty", ""],
      ["parent", "parent"],
    ])(
      "rejects a published %s child id without mutating ownership",
      (_, id) => {
        const store = new SessionTabsStore();
        pending(store);
        const before = store.snapshot("parent");
        expect(
          store.publishSidechat(
            "parent",
            "request",
            { kind: SessionTabKind.Sidechat, id, name: "Bad" },
            draft,
          ),
        ).toBe(false);
        expect(store.snapshot("parent")).toBe(before);
        expect(store.sidechats("parent")).toEqual([]);
        expect(store.parent(id)).toBeUndefined();
        expect(store.childDraft(id)).toBeUndefined();
      },
    );
    it("publishes a distinct nonempty child once in place", () => {
      const store = new SessionTabsStore();
      pending(store);
      expect(
        store.publishSidechat(
          "parent",
          "request",
          { kind: SessionTabKind.Sidechat, id: "child", name: "Child" },
          draft,
        ),
      ).toBe(true);
      expect(store.parent("child")).toBe("parent");
      expect(store.sidechats("parent")).toHaveLength(1);
      expect(store.childDraft("child")).toEqual(draft);
      expect(store.snapshot("parent").tabs).toHaveLength(2);
    });
    it("ignores opening a Sidechat equal to its parent or an ancestor", () => {
      const store = new SessionTabsStore();
      store.open("root", {
        kind: SessionTabKind.Sidechat,
        id: "child",
        name: "Child",
      });
      store.open("child", {
        kind: SessionTabKind.Sidechat,
        id: "child",
        name: "Self",
      });
      store.open("child", {
        kind: SessionTabKind.Sidechat,
        id: "root",
        name: "Ancestor",
      });
      expect(store.sidechats("child")).toEqual([]);
      expect(store.parent("root")).toBeUndefined();
      expect(store.snapshot("child").tabs).toHaveLength(1);
    });
  });
});
