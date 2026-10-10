// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { SessionRevertController } from "./session-revert-controller";

it("releases only an exact original rejected action and keeps foreign sessions", () => {
  const store = new SessionRevertController();
  const pending = { action: "original", message: "message", context: 9n, draft: "original draft" };
  store.set("a", () => ({ pending }));
  store.set("b", () => ({ pending: { ...pending, action: "other" } }));
  for (const [key, id, requestId] of [["revert:a", "a", "wrong"], ["revert:b", "a", "original"], ["control:a", "a", "original"]]) {
    store.rejected(key!, { mutation: { id, requestId } });
    expect(store.get("a").pending).toEqual(pending);
  }
  store.rejected("revert:a", { mutation: { id: "a", requestId: "original" } });
  expect(store.get("a").pending).toBeUndefined();
  expect(store.get("b").pending?.action).toBe("other");
});

it("bounds retention without evicting original pending draft guards", () => {
  const store = new SessionRevertController();
  const pending = { action: "original", message: "message", context: 0n, draft: "draft" };
  for (let i = 0; i < 1000; i++) store.set(String(i), () => ({ pending }));
  expect(() => store.set("new", () => ({ pending }))).toThrow();
  expect(store.get("0").pending).toEqual(pending);
  expect(() => store.set("0", () => ({ pending: { ...pending, draft: "x".repeat(8 << 20) } }))).toThrow();
  expect(store.get("0").pending).toEqual(pending);
  store.rejected("revert:0", { mutation: { id: "0", requestId: "original" } });
  expect(() => store.set("new", () => ({ pending }))).not.toThrow();
  expect(store.get("999").pending).toEqual(pending);
  store.clear();
  expect(store.get("999").pending).toBeUndefined();
});
