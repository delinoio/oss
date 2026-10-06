// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ToastKind, ToastPause, ToastStore } from "./toast-store";

beforeEach(() => vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] }));
afterEach(() => vi.useRealTimers());

it("updates one ID without reordering it and replaces its original expiry", () => {
  const store = new ToastStore();
  store.notify({ id: "save", kind: ToastKind.Info, message: "Saving" });
  store.notify({ id: "other", kind: ToastKind.Success, message: "Other", durationMs: 0 });
  vi.advanceTimersByTime(4000);
  store.notify({ id: "save", kind: ToastKind.Success, message: "Saved" });
  expect(store.getSnapshot().map(value => value.id)).toEqual(["save", "other"]);
  vi.advanceTimersByTime(4999);
  expect(store.getSnapshot()[0]?.message).toBe("Saved");
  vi.advanceTimersByTime(1);
  expect(store.getSnapshot().map(value => value.id)).toEqual(["other"]);
  store.dispose();
});

it("starts queued durations only when a slot opens and bounds the queue at twenty", () => {
  const store = new ToastStore();
  for (let index = 0; index < 21; index++) store.notify({ id: `${index}`, kind: ToastKind.Info, message: `Message ${index}`, durationMs: index < 3 ? 0 : 5000 });
  vi.advanceTimersByTime(100000);
  expect(store.getSnapshot().map(value => value.id)).toEqual(["0", "1", "2"]);
  store.dismiss("0");
  expect(store.getSnapshot().map(value => value.id)).toEqual(["1", "2", "4"]);
  vi.advanceTimersByTime(4999);
  expect(store.getSnapshot()[2]?.id).toBe("4");
  vi.advanceTimersByTime(1);
  expect(store.getSnapshot()[2]?.id).toBe("5");
  store.dispose();
});

it("retains remaining time across overlapping hover, focus and global pauses", () => {
  const store = new ToastStore();
  const id = store.notify({ kind: ToastKind.Warning, message: "Warning" });
  vi.advanceTimersByTime(2000);
  store.pause(id, ToastPause.Hover, true);
  store.pause(id, ToastPause.Focus, true);
  vi.advanceTimersByTime(10000);
  store.pause(id, ToastPause.Hover, false);
  vi.advanceTimersByTime(10000);
  store.suspend(true);
  store.pause(id, ToastPause.Focus, false);
  vi.advanceTimersByTime(10000);
  expect(store.getSnapshot()).toHaveLength(1);
  store.suspend(false);
  vi.advanceTimersByTime(2999);
  expect(store.getSnapshot()).toHaveLength(1);
  vi.advanceTimersByTime(1);
  expect(store.getSnapshot()).toHaveLength(0);
});

it("disposes timers and rejects late publication while allowing Strict Mode activation", () => {
  const store = new ToastStore();
  store.notify({ kind: ToastKind.Error, message: "First" });
  store.dispose();
  store.notify({ kind: ToastKind.Error, message: "Late" });
  expect(store.getSnapshot()).toHaveLength(0);
  expect(vi.getTimerCount()).toBe(0);
  store.activate();
  store.notify({ kind: ToastKind.Success, message: "Replacement" });
  vi.advanceTimersByTime(5000);
  expect(store.getSnapshot()).toHaveLength(0);
});

it("rejects invalid duration and oversized content before creating timers", () => {
  const store = new ToastStore();
  for (const durationMs of [-1, NaN, Infinity, 0x80000000]) expect(() => store.notify({ kind: ToastKind.Info, message: "Message", durationMs })).toThrow("Toast duration is invalid.");
  expect(() => store.notify({ kind: ToastKind.Info, message: "x".repeat(4097) })).toThrow("Toast content is invalid.");
  expect(store.getSnapshot()).toHaveLength(0);
  expect(vi.getTimerCount()).toBe(0);
});
