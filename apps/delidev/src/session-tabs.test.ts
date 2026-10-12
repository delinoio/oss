import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { describe, it, expect, vi } from "vitest";
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

it("refreshes only existing exact session/profile/page metadata without selection or order changes", () => {
  const store = new SessionTabsStore();
  const page = {
    kind: SessionTabKind.Page as const,
    profile: "profile",
    id: "page",
    title: "original",
    label: "https://fixture.test/original",
  };
  const foreign = { ...page, profile: "foreign" };
  store.open("session", page);
  store.open("session", foreign);
  store.open("other", page);
  store.open("session", { kind: SessionTabKind.Files });
  const previous = store.snapshot("session");
  const other = store.snapshot("other");
  const publish = vi.fn();
  store.subscribe(publish);
  const pages = [
    { id: "page", title: "updated", label: "https://fixture.test/updated" },
    { id: "absent", title: "absent", label: "https://fixture.test/absent" },
  ];
  store.refreshPages("session", "profile", pages);
  expect(publish).toHaveBeenCalledTimes(1);
  const changed = store.snapshot("session");
  expect(changed.selected).toBe(previous.selected);
  expect(changed.tabs.map(sessionTabKey)).toEqual(
    previous.tabs.map(sessionTabKey),
  );
  expect(changed.tabs[1]).toEqual({ ...page, ...pages[0] });
  expect(changed.tabs[2]).toBe(foreign);
  expect(store.snapshot("other")).toBe(other);
  store.refreshPages("session", "profile", pages);
  store.refreshPages("session", "absent-profile", pages);
  store.refreshPages("absent-session", "profile", pages);
  expect(store.snapshot("session")).toBe(changed);
  expect(publish).toHaveBeenCalledTimes(1);
  store.close("session", sessionTabKey(page));
  const closed = store.snapshot("session");
  publish.mockClear();
  store.refreshPages("session", "profile", pages);
  expect(store.snapshot("session")).toBe(closed);
  expect(publish).not.toHaveBeenCalled();
});


it("allocates shared session numbers on original-ID opens and authenticated encounters", () => {
  const store = new SessionTabsStore();
  const one = { kind: SessionTabKind.Terminal as const, id: "one" };
  store.open("session", { kind: SessionTabKind.Files });
  store.open("session", one);
  const presentation = store.terminalPresentation("session");
  expect(presentation.number("one")).toBe(1);
  presentation.observe("session", create(ResourceSchema, {
    id: "inventory", sessionId: "session", kind: EntityKind.TERMINAL,
    schemaVersion: 1, revision: 2n, documentJson: encode({ state: "running" }),
  }));
  store.close("session", sessionTabKey(one));
  store.open("session", { kind: SessionTabKind.Terminal, id: "inventory" });
  store.open("session", one);
  expect(presentation.number("one")).toBe(1);
  expect(presentation.number("inventory")).toBe(2);
  store.open("session", { kind: SessionTabKind.Terminal, id: "third" });
  expect(presentation.number("third")).toBe(3);
  expect(store.snapshot("session").selected).toBe(JSON.stringify(["terminal", "third"]));
  store.open("other", one);
  expect(store.terminalPresentation("other").number("one")).toBe(1);
  expect(new SessionTabsStore().terminalPresentation("session").number("third")).toBe(1);
  expect(Object.values(SessionTabKind)).not.toContain("terminals");
});
