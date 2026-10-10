// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { SidebarMotionAdmission, SidebarPaneMotion, sidebarMotionDuration, sidebarMotionFallback } from "./sidebar-motion";

afterEach(() => { vi.useRealTimers(); document.body.replaceChildren(); });
function fixture() {
  vi.useFakeTimers();
  const layout = document.createElement("div"), pane = document.createElement("dialog"), content = document.createElement("div"), draft = document.createElement("textarea"), toggle = document.createElement("button");
  layout.className = "app"; draft.value = "Retained draft";
  content.append(draft); pane.append(content); layout.append(pane, toggle); document.body.append(layout);
  let width = 288, successor = false;
  pane.getBoundingClientRect = () => ({ width } as DOMRect);
  content.getBoundingClientRect = () => ({ width: 288 } as DOMRect);
  layout.getAnimations = () => successor ? [{ playState: "running" } as Animation] : [];
  const ready = vi.fn(), motion = new SidebarPaneMotion(layout, pane, () => toggle, ready);
  const transition = (kind = "transitionend") => { const event = new Event(kind); Object.defineProperty(event, "propertyName", { value: "grid-template-columns" }); layout.dispatchEvent(event); };
  motion.update(false, false);
  return { layout, pane, draft, toggle, ready, motion, transition, geometry: (next: number) => { width = next; }, successor: (value: boolean) => { successor = value; } };
}
it("blocks closing input/focus before visual settlement and retains the mounted draft", () => {
  const f = fixture(); f.draft.focus(); f.draft.setSelectionRange(2, 7);
  f.motion.update(true, true);
  expect(document.activeElement).toBe(f.toggle); expect(f.pane.inert).toBe(true); expect(f.pane.getAttribute("aria-hidden")).toBe("true");
  expect(f.pane.hidden).toBe(false); expect(f.pane.hasAttribute("open")).toBe(true); expect(f.layout.classList.contains("sidebar-motion")).toBe(true);
  f.geometry(140); f.transition(); expect(f.pane.hidden).toBe(false);
  f.geometry(0); f.transition(); expect(f.pane.hidden).toBe(true); expect(f.pane.hasAttribute("open")).toBe(false);
  f.motion.update(false, true); expect(f.ready).toHaveBeenLastCalledWith(false); expect(document.activeElement).toBe(f.toggle);
  f.geometry(288); f.transition(); expect(f.ready).toHaveBeenLastCalledWith(true);
  expect(f.pane.querySelector("textarea")).toBe(f.draft); expect(f.draft.value).toBe("Retained draft"); expect([f.draft.selectionStart, f.draft.selectionEnd]).toEqual([2, 7]);
});
it("reverses from current geometry and retires predecessor fallback/completion", () => {
  const f = fixture(); f.motion.update(true, true); vi.advanceTimersByTime(90); f.geometry(130);
  f.motion.update(false, true); f.successor(true); f.transition("transitioncancel"); f.transition();
  expect(f.pane.hidden).toBe(false); expect(f.layout.classList.contains("sidebar-motion")).toBe(true);
  vi.advanceTimersByTime(150); expect(f.layout.classList.contains("sidebar-motion")).toBe(true);
  f.geometry(288); f.transition(); expect(f.pane.hidden).toBe(false); expect(f.ready).toHaveBeenLastCalledWith(true);
  vi.advanceTimersByTime(500); expect(f.pane.hidden).toBe(false);
});
it("bounds missing completion at 240ms and applies immediate cancellation endpoints", () => {
  const f = fixture(); expect(sidebarMotionDuration).toBe(180); expect(sidebarMotionFallback).toBe(240);
  f.motion.update(true, true); vi.advanceTimersByTime(239); expect(f.pane.hidden).toBe(false);
  vi.advanceTimersByTime(1); expect(f.pane.hidden).toBe(true);
  f.motion.update(false, true); f.motion.update(false, false); expect(f.pane.hidden).toBe(false); expect(f.ready).toHaveBeenLastCalledWith(true);
  vi.advanceTimersByTime(500); expect(f.pane.hidden).toBe(false);
  f.motion.update(true, true); f.transition("transitioncancel"); expect(f.pane.hidden).toBe(true);
});
it("retires callbacks on disposal and does not animate immediate restoration", () => {
  const f = fixture(); f.motion.update(true, false); expect(f.pane.hidden).toBe(true); expect(f.layout.classList.contains("sidebar-motion")).toBe(false);
  f.motion.update(false, true); f.motion.dispose(); const calls = f.ready.mock.calls.length;
  vi.advanceTimersByTime(1000); f.transition(); expect(f.ready).toHaveBeenCalledTimes(calls);
});

it("admits only stable committed wide changes and preserves running motion while a reversal saves", () => {
  const owner = new SidebarMotionAdmission();
  const boundary = { collapsed: false, compact: false, narrowWide: false, surface: "sessions", selected: "", connectionReady: true, allowed: false, saving: false, reduced: false, navigation: "page", drawer: false };
  expect(owner.update(boundary).animate).toBe(false);
  expect(owner.update({ ...boundary, collapsed: true, allowed: true }).animate).toBe(false);
  const expanded = { ...boundary, allowed: true };
  expect(owner.update(expanded).animate).toBe(true);
  expect(owner.update({ ...expanded, saving: true })).toEqual({ animate: false, changed: false });
  expect(owner.update({ ...expanded, saving: true, collapsed: true }).animate).toBe(true);
});
it.each(["compact", "narrowWide", "surface", "connectionReady", "reduced", "navigation"])("does not animate restoration or pending preference replacement across %s", field => {
  const owner = new SidebarMotionAdmission();
  const boundary = { collapsed: false, compact: false, narrowWide: false, surface: "sessions", selected: "", connectionReady: true, allowed: true, saving: false, reduced: false, navigation: "page", drawer: false };
  owner.update(boundary); owner.update({ ...boundary, saving: true });
  const changed = { ...boundary, saving: true, [field]: typeof boundary[field as keyof typeof boundary] === "boolean" ? !boundary[field as keyof typeof boundary] : "replacement" };
  expect(owner.update(changed).animate).toBe(false);
  expect(owner.update({ ...changed, collapsed: true, saving: false }).animate).toBe(false);
});
