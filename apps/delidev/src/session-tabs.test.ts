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

it("retains monotonic session-local terminal numbers through inventory and presentation changes", () => {
  const store = new SessionTabsStore();
  const inventory = store.terminalPresentation("session");
  expect(inventory.number("later-page")).toBe(1);
  store.open("session", { kind: SessionTabKind.Terminal, id: "opened" });
  expect(store.snapshot("session").tabs[1]).toMatchObject({
    id: "opened",
    number: 2,
  });
  store.close(
    "session",
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "opened" }),
  );
  store.open("session", { kind: SessionTabKind.Terminal, id: "later-page" });
  store.open("session", { kind: SessionTabKind.Terminal, id: "opened" });
  expect(
    store
      .snapshot("session")
      .tabs.slice(1)
      .map((tab) =>
        tab.kind === SessionTabKind.Terminal ? tab.number : undefined,
      ),
  ).toEqual([1, 2]);
  expect(inventory.number("new-original")).toBe(3);
  expect(store.terminalPresentation("other-session").number("opened")).toBe(1);
  expect(
    new SessionTabsStore().terminalPresentation("session").number("opened"),
  ).toBe(1);
  expect(
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "opened", number: 99 }),
  ).toBe(
    sessionTabKey({ kind: SessionTabKind.Terminal, id: "opened", number: 2 }),
  );
});

it("refreshes only existing matching pages without publication for unchanged observations", () => {
  const store = new SessionTabsStore();
  const page = {
    kind: SessionTabKind.Page as const,
    profile: "original",
    id: "a",
    title: "old",
    label: "https://fixture.test/old",
  };
  store.open("s", page);
  store.open("s", { ...page, id: "b" });
  store.open("s", { ...page, profile: "foreign" });
  store.select("s", SessionTabKind.Conversation);
  const previous = store.snapshot("s");
  store.refreshPages("s", "original", [
    { id: "a", title: "new", label: "https://fixture.test/new" },
    { id: "absent", title: "unused" },
  ]);
  const updated = store.snapshot("s");
  expect(updated.selected).toBe(previous.selected);
  expect(updated.tabs.map(sessionTabKey)).toEqual(
    previous.tabs.map(sessionTabKey),
  );
  expect(updated.tabs[1]).toMatchObject({
    ...page,
    title: "new",
    label: "https://fixture.test/new",
  });
  expect(updated.tabs[2]).toEqual(previous.tabs[2]);
  expect(updated.tabs[3]).toEqual(previous.tabs[3]);
  store.refreshPages("s", "original", [
    { id: "a", title: "new", label: "https://fixture.test/new" },
  ]);
  expect(store.snapshot("s")).toBe(updated);
  expect(store.snapshot("other").tabs).toHaveLength(1);
});
it("dismisses an acknowledged page to its native survivor or Browser without removing other profiles", () => {
  const store = new SessionTabsStore();
  const page = {
    kind: SessionTabKind.Page as const,
    profile: "original",
    id: "a",
    title: "page",
  };
  store.open("s", { kind: SessionTabKind.Browser });
  store.open("s", page);
  store.open("s", { ...page, id: "b" });
  const foreign = { ...page, profile: "foreign" };
  store.open("s", foreign);
  store.select("s", sessionTabKey(page));
  store.dismissPage("s", "original", "a", "b");
  expect(store.snapshot("s").selected).toBe(
    sessionTabKey({ ...page, id: "b" }),
  );
  expect(store.snapshot("s").tabs).toContainEqual(foreign);
  store.dismissPage("s", "original", "b", "");
  expect(store.snapshot("s").selected).toBe(SessionTabKind.Browser);
  expect(store.snapshot("s").tabs).toContainEqual(foreign);
  const snapshot = store.snapshot("s");
  store.dismissPage("s", "original", "absent", "");
  expect(store.snapshot("s")).toBe(snapshot);
});
it("preserves selection when an inactive acknowledged page closes", () => {
  const store = new SessionTabsStore();
  const page = {
    kind: SessionTabKind.Page as const,
    profile: "original",
    id: "a",
    title: "page",
  };
  store.open("s", page);
  store.open("s", { kind: SessionTabKind.Files });
  store.dismissPage("s", "original", "a", "");
  expect(store.snapshot("s").selected).toBe(SessionTabKind.Files);
  expect(store.snapshot("s").tabs.map(sessionTabKey)).not.toContain(
    sessionTabKey(page),
  );
});
